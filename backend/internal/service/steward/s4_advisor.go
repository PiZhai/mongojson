package steward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	advisorProviderDisabled         = "disabled"
	advisorProviderOpenAICompatible = "openai-compatible"
)

var ErrAdvisorDataLevelDenied = errors.New("autonomy advisor data level denied")

type AutonomyAdvisor interface {
	Status() domain.StewardAutonomyAdvisorStatus
	Suggest(ctx context.Context, input AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error)
}

type AutonomyAdvisorInput struct {
	Kind             string
	SourceEntityType string
	Title            string
	Summary          string
	DataLevel        string
	RuleName         string
	RuleScope        string
}

type AutonomyAdvisorSuggestion struct {
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	TriggerReason   string `json:"trigger_reason"`
	SuggestedAction string `json:"suggested_action"`
	ImpactSummary   string `json:"impact_summary"`
}

type ProbeAutonomyAdvisorInput struct {
	Kind             string `json:"kind"`
	SourceEntityType string `json:"source_entity_type"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	DataLevel        string `json:"data_level"`
	RuleName         string `json:"rule_name"`
	RuleScope        string `json:"rule_scope"`
}

type ProbeAutonomyAdvisorResult struct {
	OK             bool                                `json:"ok"`
	Status         domain.StewardAutonomyAdvisorStatus `json:"status"`
	DataLevel      string                              `json:"data_level"`
	DurationMillis int64                               `json:"duration_ms"`
	Suggestion     *AutonomyAdvisorSuggestion          `json:"suggestion,omitempty"`
	Error          string                              `json:"error,omitempty"`
	ProbedAt       time.Time                           `json:"probed_at"`
}

type disabledAutonomyAdvisor struct {
	reason string
}

func DisabledAutonomyAdvisor(reason string) AutonomyAdvisor {
	return disabledAutonomyAdvisor{reason: defaultString(reason, "disabled")}
}

func (a disabledAutonomyAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{
		Enabled:  false,
		Provider: advisorProviderDisabled,
		Reason:   a.reason,
	}
}

func (a disabledAutonomyAdvisor) Suggest(context.Context, AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	return AutonomyAdvisorSuggestion{}, fmt.Errorf("autonomy advisor disabled: %s", a.reason)
}

func (s *Service) ProbeAutonomyAdvisor(ctx context.Context, input ProbeAutonomyAdvisorInput) (ProbeAutonomyAdvisorResult, error) {
	advisor := s.autonomyAdvisor()
	status := advisor.Status()
	probeInput := AutonomyAdvisorInput{
		Kind:             defaultString(input.Kind, "verification_probe"),
		SourceEntityType: defaultString(input.SourceEntityType, "verification"),
		Title:            defaultString(input.Title, "S4 advisor verification probe"),
		Summary:          defaultString(input.Summary, "D0 low-risk probe used to verify the configured autonomy advisor connection."),
		DataLevel:        defaultString(strings.ToUpper(strings.TrimSpace(input.DataLevel)), DataD0),
		RuleName:         defaultString(input.RuleName, "advisor-live-probe"),
		RuleScope:        defaultString(input.RuleScope, "local D0 verification only"),
	}
	result := ProbeAutonomyAdvisorResult{
		Status:    status,
		DataLevel: probeInput.DataLevel,
		ProbedAt:  time.Now().UTC(),
	}
	startedAt := time.Now()
	if !status.Enabled {
		result.Error = defaultString(status.Reason, "autonomy advisor is disabled")
		s.recordAdvisorProbeAudit(ctx, result)
		return result, nil
	}
	suggestion, err := advisor.Suggest(ctx, probeInput)
	result.DurationMillis = time.Since(startedAt).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		s.recordAdvisorProbeAudit(ctx, result)
		return result, nil
	}
	result.OK = true
	result.Suggestion = &suggestion
	s.recordAdvisorProbeAudit(ctx, result)
	return result, nil
}

func (s *Service) recordAdvisorProbeAudit(ctx context.Context, result ProbeAutonomyAdvisorResult) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return
	}
	status := ResultOK
	var errorSummary *string
	output := "advisor probe completed"
	if !result.OK {
		status = "failed"
		output = "advisor probe failed"
		if strings.TrimSpace(result.Error) != "" {
			value := result.Error
			errorSummary = &value
		}
	}
	userConfirmed := true
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "autonomy.advisor.probe",
		TargetType:      "autonomy_advisor",
		Source:          "verification",
		PermissionLevel: PermissionA3,
		DataLevel:       result.DataLevel,
		InputSummary:    result.Status.Provider + ":" + result.Status.Model,
		OutputSummary:   output,
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    status,
		ErrorSummary:    errorSummary,
	})
}

func (s *Service) recordAdvisorSuggestionFallback(ctx context.Context, input AutonomyAdvisorInput, cause error) {
	if s == nil || s.db == nil || s.db.Pool == nil || cause == nil {
		return
	}
	const minInterval = 5 * time.Minute
	now := time.Now().UTC()

	s.advisorAuditMu.Lock()
	if !s.lastAdvisorFallbackAudit.IsZero() && now.Sub(s.lastAdvisorFallbackAudit) < minInterval {
		s.advisorAuditMu.Unlock()
		return
	}
	s.lastAdvisorFallbackAudit = now
	s.advisorAuditMu.Unlock()

	status := ResultFailed
	if errors.Is(cause, ErrAdvisorDataLevelDenied) {
		status = ResultBlocked
	}
	userConfirmed := false
	syncable := false
	errorSummary := sanitizeAdvisorStatusError(cause)
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "system",
		Action:          "autonomy.advisor.fallback",
		TargetType:      "autonomy_advisor",
		Source:          "autonomy",
		PermissionLevel: PermissionA3,
		DataLevel:       defaultString(input.DataLevel, DataD0),
		InputSummary:    strings.Join([]string{input.Kind, input.SourceEntityType, input.RuleName}, " / "),
		OutputSummary:   "advisor suggestion unavailable; local rule fallback used",
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    status,
		ErrorSummary:    &errorSummary,
	})
}

type openAICompatibleAutonomyAdvisor struct {
	client       *http.Client
	baseURL      string
	apiKey       string
	model        string
	maxDataLevel string
}

func NewAutonomyAdvisorFromEnv() AutonomyAdvisor {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("STEWARD_LLM_PROVIDER")))
	if provider == "" || provider == "off" || provider == "disabled" || provider == "none" {
		return DisabledAutonomyAdvisor("STEWARD_LLM_PROVIDER is not enabled")
	}
	if provider != advisorProviderOpenAICompatible && provider != "openai" {
		return DisabledAutonomyAdvisor("unsupported STEWARD_LLM_PROVIDER " + provider)
	}
	model := strings.TrimSpace(os.Getenv("STEWARD_LLM_MODEL"))
	if model == "" {
		return DisabledAutonomyAdvisor("STEWARD_LLM_MODEL is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(envOrDefault("STEWARD_LLM_BASE_URL", "https://api.openai.com/v1")), "/")
	apiKey := strings.TrimSpace(os.Getenv("STEWARD_LLM_API_KEY"))
	allowNoKey, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("STEWARD_LLM_ALLOW_NO_API_KEY")))
	if apiKey == "" && !allowNoKey {
		return DisabledAutonomyAdvisor("STEWARD_LLM_API_KEY is required unless STEWARD_LLM_ALLOW_NO_API_KEY=true")
	}
	timeout := durationEnv("STEWARD_LLM_TIMEOUT", 20*time.Second)
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = 20 * time.Second
	}
	maxDataLevel := strings.ToUpper(strings.TrimSpace(envOrDefault("STEWARD_LLM_MAX_DATA_LEVEL", DataD1)))
	return openAICompatibleAutonomyAdvisor{
		client:       &http.Client{Timeout: timeout},
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		maxDataLevel: maxDataLevel,
	}
}

func (a openAICompatibleAutonomyAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{
		Enabled:      true,
		Provider:     advisorProviderOpenAICompatible,
		Model:        a.model,
		BaseURL:      a.baseURL,
		MaxDataLevel: a.maxDataLevel,
	}
}

func (a openAICompatibleAutonomyAdvisor) Suggest(ctx context.Context, input AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	if dataLevelRank(input.DataLevel) > dataLevelRank(a.maxDataLevel) {
		return AutonomyAdvisorSuggestion{}, fmt.Errorf("%w: data level %s exceeds advisor max %s", ErrAdvisorDataLevelDenied, input.DataLevel, a.maxDataLevel)
	}
	payload := map[string]any{
		"model":       a.model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": strings.Join([]string{
					"你是私人智能管家的低风险本地任务建议器。",
					"只能建议本地候选任务文字，不要请求发送消息、付款、删除、提交代码、修改系统配置或读取凭据。",
					"只输出一个 JSON 对象，字段为 title, summary, trigger_reason, suggested_action, impact_summary。",
				}, "\n"),
			},
			{
				"role":    "user",
				"content": autonomyAdvisorUserPrompt(input),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	if resp.StatusCode >= 400 {
		return AutonomyAdvisorSuggestion{}, fmt.Errorf("advisor request failed with %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	content, err := openAICompatibleMessageContent(data)
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	return parseAutonomyAdvisorSuggestion(content)
}

func autonomyAdvisorUserPrompt(input AutonomyAdvisorInput) string {
	return fmt.Sprintf(`类型：%s
来源实体：%s
数据级别：%s
规则：%s
规则范围：%s
标题：%s
摘要：%s

请生成一个低风险、本地候选任务建议。不要扩大权限，不要建议外部发送或不可逆操作。`,
		input.Kind,
		input.SourceEntityType,
		defaultString(input.DataLevel, DataD0),
		input.RuleName,
		input.RuleScope,
		input.Title,
		input.Summary,
	)
}

func openAICompatibleMessageContent(data []byte) (string, error) {
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("advisor response did not include message content")
	}
	return decoded.Choices[0].Message.Content, nil
}

func parseAutonomyAdvisorSuggestion(content string) (AutonomyAdvisorSuggestion, error) {
	raw := strings.TrimSpace(content)
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end >= start {
		raw = raw[start : end+1]
	}
	var suggestion AutonomyAdvisorSuggestion
	if err := json.Unmarshal([]byte(raw), &suggestion); err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	suggestion.Title = truncateAdvisorText(suggestion.Title, 120)
	suggestion.Summary = truncateAdvisorText(suggestion.Summary, 600)
	suggestion.TriggerReason = truncateAdvisorText(suggestion.TriggerReason, 600)
	suggestion.SuggestedAction = truncateAdvisorText(suggestion.SuggestedAction, 600)
	suggestion.ImpactSummary = truncateAdvisorText(suggestion.ImpactSummary, 600)
	if suggestion.Title == "" && suggestion.Summary == "" && suggestion.SuggestedAction == "" {
		return AutonomyAdvisorSuggestion{}, fmt.Errorf("advisor suggestion is empty")
	}
	return suggestion, nil
}

func truncateAdvisorText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func applyAdvisorSuggestion(input CreateAutonomyProposalInput, suggestion AutonomyAdvisorSuggestion) CreateAutonomyProposalInput {
	if suggestion.Title != "" {
		input.Title = suggestion.Title
	}
	if suggestion.Summary != "" {
		input.Summary = suggestion.Summary
	}
	if suggestion.TriggerReason != "" {
		input.TriggerReason = suggestion.TriggerReason
	}
	if suggestion.SuggestedAction != "" {
		input.SuggestedAction = suggestion.SuggestedAction
	}
	if suggestion.ImpactSummary != "" {
		input.ImpactSummary = suggestion.ImpactSummary + "；该建议只会进入本地候选，不会自动执行外部操作。"
	}
	return input
}

func dataLevelRank(level string) int {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "D0":
		return 0
	case "D1":
		return 1
	case "D2":
		return 2
	case "D3":
		return 3
	case "D4":
		return 4
	default:
		return 9
	}
}
