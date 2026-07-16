package steward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"mongojson/backend/internal/domain"
)

var (
	ErrRuntimePlannerUnsupported     = errors.New("runtime planner could not safely compile instruction")
	ErrRuntimePlannerToolUnavailable = errors.New("runtime planner tool is not enabled")
)

type PlanAgentRunInput struct {
	Instruction       string `json:"instruction"`
	IdempotencyKey    string `json:"idempotency_key"`
	RequestedBy       string `json:"requested_by"`
	TargetDevice      string `json:"target_device"`
	DataLevel         string `json:"data_level"`
	PermissionCeiling string `json:"permission_ceiling"`
	AutoStart         *bool  `json:"auto_start"`
}

type RuntimePlannerInput struct {
	Instruction string
	DataLevel   string
	Tools       []domain.StewardToolSpec
}

type RuntimePlanDraft struct {
	Summary        string                    `json:"summary"`
	Steps          []CreateAgentRunStepInput `json:"steps"`
	Planner        string                    `json:"-"`
	PlannerVersion string                    `json:"-"`
}

type RuntimePlanner interface {
	Status() domain.StewardRuntimePlannerStatus
	Plan(context.Context, RuntimePlannerInput) (RuntimePlanDraft, error)
}

type localRuntimePlanner struct{}

func (localRuntimePlanner) Status() domain.StewardRuntimePlannerStatus {
	return domain.StewardRuntimePlannerStatus{Enabled: true, Provider: "local-rules", Version: "2.0.0"}
}

var (
	createFilePattern = regexp.MustCompile(`(?is)^(?:创建(?:一个)?(?:新)?文件|create(?: a)?(?: new)? file)\s+(.+?)\s+(?:内容(?:为|是)?|with content)\s+(.+)$`)
	writeFilePattern  = regexp.MustCompile(`(?is)^(?:把|将)\s*(.+?)\s*(?:写入|保存到)\s*(?:新文件\s*)?(.+)$`)
)

func (localRuntimePlanner) Plan(_ context.Context, input RuntimePlannerInput) (RuntimePlanDraft, error) {
	instruction := strings.TrimSpace(input.Instruction)
	if instruction == "" {
		return RuntimePlanDraft{}, fmt.Errorf("%w: instruction is required", ErrRuntimePlannerUnsupported)
	}
	if value, ok := trimRuntimeInstructionPrefix(instruction, "列出目录", "查看目录", "list directory", "list files in"); ok {
		path := trimRuntimeQuoted(value)
		return availableRuntimePlan(input, "列出目录 "+path, "list", "fs.list", map[string]any{"path": path})
	}
	if value, ok := trimRuntimeInstructionPrefix(instruction, "读取文件", "查看文件", "read file", "show file"); ok {
		path := trimRuntimeQuoted(value)
		return availableRuntimePlan(input, "读取文本文件 "+path, "read", "fs.read_text", map[string]any{"path": path})
	}
	if match := createFilePattern.FindStringSubmatch(instruction); len(match) == 3 {
		path := trimRuntimeQuoted(match[1])
		content := trimRuntimeQuoted(match[2])
		return availableRuntimePlan(input, "创建文本文件 "+path, "create", "fs.create_text", map[string]any{"path": path, "content": content, "create_parents": true})
	}
	if match := writeFilePattern.FindStringSubmatch(instruction); len(match) == 3 {
		content := trimRuntimeQuoted(match[1])
		path := trimRuntimeQuoted(match[2])
		return availableRuntimePlan(input, "创建文本文件 "+path, "create", "fs.create_text", map[string]any{"path": path, "content": content, "create_parents": true})
	}
	if value, ok := trimRuntimeInstructionPrefix(instruction, "运行命令", "执行命令", "run command", "execute command"); ok {
		parts, err := splitRuntimeCommandLine(value)
		if err != nil || len(parts) == 0 {
			return RuntimePlanDraft{}, fmt.Errorf("%w: command syntax is invalid", ErrRuntimePlannerUnsupported)
		}
		arguments := make([]any, 0, len(parts)-1)
		for _, item := range parts[1:] {
			arguments = append(arguments, item)
		}
		return availableRuntimePlan(input, "执行白名单命令 "+parts[0], "command", "shell.exec", map[string]any{"command": parts[0], "args": arguments})
	}
	if value, ok := trimRuntimeInstructionPrefix(instruction, "获取网页", "读取网页", "fetch url", "fetch page"); ok {
		url := trimRuntimeQuoted(value)
		return availableRuntimePlan(input, "获取网页文本 "+url, "fetch", "web.fetch_text", map[string]any{"url": url})
	}
	if value, ok := trimRuntimeInstructionPrefix(instruction, "打开网页", "用浏览器打开", "open url", "open website"); ok {
		url := trimRuntimeQuoted(value)
		return availableRuntimePlan(input, "请求默认浏览器打开 "+url, "open", "browser.open_url", map[string]any{"url": url})
	}
	if value, ok := trimRuntimeInstructionPrefix(instruction,
		"执行高权限能力", "运行高权限能力", "执行系统能力",
		"run privileged capability", "execute privileged capability"); ok {
		capability := strings.ToLower(trimRuntimeQuoted(value))
		if !configuredToolActionPattern.MatchString(capability) {
			return RuntimePlanDraft{}, fmt.Errorf("%w: privileged capability must use the exact tool:<name> identifier", ErrRuntimePlannerUnsupported)
		}
		return availableRuntimePlan(input, "通过独立 Broker 执行 "+capability, "privileged", "privilege.execute", map[string]any{"capability": capability})
	}
	return RuntimePlanDraft{}, fmt.Errorf("%w: supported local forms are list/read/create file, run command, fetch/open URL, and an exact privileged capability", ErrRuntimePlannerUnsupported)
}

func availableRuntimePlan(input RuntimePlannerInput, summary string, key string, tool string, arguments map[string]any) (RuntimePlanDraft, error) {
	if len(input.Tools) > 0 {
		available := false
		for _, spec := range input.Tools {
			if spec.Name == tool {
				available = true
				break
			}
		}
		if !available {
			return RuntimePlanDraft{}, fmt.Errorf("%w: %s", ErrRuntimePlannerToolUnavailable, tool)
		}
	}
	return singleRuntimePlan(summary, key, tool, arguments), nil
}

func singleRuntimePlan(summary string, key string, tool string, arguments map[string]any) RuntimePlanDraft {
	return RuntimePlanDraft{
		Summary: summary, Planner: "local-rules", PlannerVersion: "2.0.0",
		Steps: []CreateAgentRunStepInput{{Key: key, Title: summary, ToolName: tool, Arguments: arguments}},
	}
}

func trimRuntimeInstructionPrefix(value string, prefixes ...string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			remainder := strings.TrimSpace(value[len(prefix):])
			if remainder != "" {
				return remainder, true
			}
		}
	}
	return "", false
}

func trimRuntimeQuoted(value string) string {
	value = strings.TrimSpace(value)
	pairs := [][2]string{{`"`, `"`}, {`'`, `'`}, {"“", "”"}, {"‘", "’"}, {"「", "」"}}
	for _, pair := range pairs {
		if strings.HasPrefix(value, pair[0]) && strings.HasSuffix(value, pair[1]) && len([]rune(value)) >= 2 {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, pair[0]), pair[1]))
		}
	}
	return value
}

func splitRuntimeCommandLine(value string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}
	runes := []rune(strings.TrimSpace(value))
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		if quote != 0 {
			if char == '\\' && index+1 < len(runes) && (runes[index+1] == quote || runes[index+1] == '\\') {
				current.WriteRune(runes[index+1])
				index++
				continue
			}
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if unicode.IsSpace(char) {
			flush()
			continue
		}
		current.WriteRune(char)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted argument")
	}
	flush()
	return parts, nil
}

type chainedRuntimePlanner struct {
	local    RuntimePlanner
	fallback RuntimePlanner
}

func (p chainedRuntimePlanner) Status() domain.StewardRuntimePlannerStatus {
	localStatus := p.local.Status()
	if p.fallback == nil {
		return localStatus
	}
	fallbackStatus := p.fallback.Status()
	if !fallbackStatus.Enabled {
		localStatus.Reason = "optional fallback disabled: " + fallbackStatus.Reason
		return localStatus
	}
	status := fallbackStatus
	status.Provider = "local-rules+" + status.Provider
	return status
}

func (p chainedRuntimePlanner) Plan(ctx context.Context, input RuntimePlannerInput) (RuntimePlanDraft, error) {
	plan, err := p.local.Plan(ctx, input)
	if err == nil {
		if plan.Planner == "" {
			plan.Planner = p.local.Status().Provider
			plan.PlannerVersion = p.local.Status().Version
		}
		return plan, nil
	}
	if !errors.Is(err, ErrRuntimePlannerUnsupported) || p.fallback == nil || !p.fallback.Status().Enabled {
		return RuntimePlanDraft{}, err
	}
	plan, err = p.fallback.Plan(ctx, input)
	if err == nil && plan.Planner == "" {
		plan.Planner = p.fallback.Status().Provider
		plan.PlannerVersion = p.fallback.Status().Version
	}
	return plan, err
}

type openAICompatibleRuntimePlanner struct {
	client       *http.Client
	baseURL      string
	apiKey       string
	model        string
	maxDataLevel string
	disabled     string
}

func newRuntimePlannerFromEnv() RuntimePlanner {
	local := localRuntimePlanner{}
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("STEWARD_RUNTIME_PLANNER_PROVIDER")))
	if provider == "" || provider == "local" || provider == "local-rules" {
		return chainedRuntimePlanner{local: local}
	}
	remote := &openAICompatibleRuntimePlanner{}
	if provider != "openai-compatible" && provider != "openai" {
		remote.disabled = "unsupported planner provider " + provider
		return chainedRuntimePlanner{local: local, fallback: remote}
	}
	remote.model = strings.TrimSpace(defaultString(os.Getenv("STEWARD_RUNTIME_PLANNER_MODEL"), os.Getenv("STEWARD_LLM_MODEL")))
	if remote.model == "" {
		remote.disabled = "STEWARD_RUNTIME_PLANNER_MODEL or STEWARD_LLM_MODEL is required"
		return chainedRuntimePlanner{local: local, fallback: remote}
	}
	rawBaseURL := strings.TrimSpace(defaultString(os.Getenv("STEWARD_RUNTIME_PLANNER_BASE_URL"), defaultString(os.Getenv("STEWARD_LLM_BASE_URL"), "https://api.openai.com/v1")))
	parsedBaseURL, err := url.Parse(rawBaseURL)
	if err != nil || parsedBaseURL.Hostname() == "" || (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") {
		remote.disabled = "planner base URL must be an absolute http or https URL"
		return chainedRuntimePlanner{local: local, fallback: remote}
	}
	remote.baseURL = strings.TrimRight(parsedBaseURL.String(), "/")
	remote.apiKey = strings.TrimSpace(defaultString(os.Getenv("STEWARD_RUNTIME_PLANNER_API_KEY"), os.Getenv("STEWARD_LLM_API_KEY")))
	allowNoKey, _ := strconv.ParseBool(strings.TrimSpace(defaultString(os.Getenv("STEWARD_RUNTIME_PLANNER_ALLOW_NO_API_KEY"), os.Getenv("STEWARD_LLM_ALLOW_NO_API_KEY"))))
	if remote.apiKey == "" && !allowNoKey {
		remote.disabled = "planner API key is required unless allow-no-api-key is enabled"
		return chainedRuntimePlanner{local: local, fallback: remote}
	}
	if allowNoKey && !runtimePlannerLoopbackHost(parsedBaseURL.Hostname()) {
		remote.disabled = "planner allow-no-api-key is restricted to loopback endpoints"
		return chainedRuntimePlanner{local: local, fallback: remote}
	}
	remote.maxDataLevel = strings.ToUpper(strings.TrimSpace(defaultString(os.Getenv("STEWARD_RUNTIME_PLANNER_MAX_DATA_LEVEL"), defaultString(os.Getenv("STEWARD_LLM_MAX_DATA_LEVEL"), DataD1))))
	if !validRuntimeDataLevel(remote.maxDataLevel) {
		remote.disabled = "planner max data level must be D0-D6"
		return chainedRuntimePlanner{local: local, fallback: remote}
	}
	timeout := durationEnv("STEWARD_RUNTIME_PLANNER_TIMEOUT", 30*time.Second)
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = 30 * time.Second
	}
	remote.client = &http.Client{Timeout: timeout}
	return chainedRuntimePlanner{local: local, fallback: remote}
}

func runtimePlannerLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *openAICompatibleRuntimePlanner) Status() domain.StewardRuntimePlannerStatus {
	if p == nil || p.disabled != "" || p.client == nil {
		reason := "planner is not configured"
		if p != nil && p.disabled != "" {
			reason = p.disabled
		}
		return domain.StewardRuntimePlannerStatus{Enabled: false, Provider: "openai-compatible", Reason: reason, Version: "2.0.0"}
	}
	return domain.StewardRuntimePlannerStatus{Enabled: true, Provider: "openai-compatible", Model: p.model, Version: "2.0.0"}
}

func (p *openAICompatibleRuntimePlanner) Plan(ctx context.Context, input RuntimePlannerInput) (RuntimePlanDraft, error) {
	if !p.Status().Enabled {
		return RuntimePlanDraft{}, fmt.Errorf("%w: %s", ErrRuntimePlannerUnsupported, p.Status().Reason)
	}
	if dataLevelRank(input.DataLevel) > dataLevelRank(p.maxDataLevel) {
		return RuntimePlanDraft{}, fmt.Errorf("%w: data level %s exceeds planner max %s", ErrAdvisorDataLevelDenied, input.DataLevel, p.maxDataLevel)
	}
	toolsJSON, _ := json.Marshal(input.Tools)
	payload := map[string]any{
		"model": p.model, "temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": strings.Join([]string{
				"你是本地执行计划编译器，只把用户指令映射到提供的工具，不得发明工具、扩大路径、命令、权限或步骤。",
				"只输出 JSON 对象：summary 和 steps。steps 每项只含 key,title,tool_name,arguments,expected_output,depends_on,max_attempts,timeout_seconds。",
				"删除、覆盖文件、凭据、付款、外部发送、系统设置和未在工具清单中的动作必须拒绝，不得生成替代危险命令。",
				"提权默认拒绝；唯一例外是用户原文明确给出 tool:<name>，且工具清单含 privilege.execute。不得猜测、改写或生成 capability 名称。",
				"工具清单：" + string(toolsJSON),
			}, "\n")},
			{"role": "user", "content": truncateAdvisorText(input.Instruction, 8000)},
		},
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return RuntimePlanDraft{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return RuntimePlanDraft{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return RuntimePlanDraft{}, err
	}
	if response.StatusCode >= 400 {
		return RuntimePlanDraft{}, fmt.Errorf("planner request failed with %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	content, err := openAICompatibleMessageContent(data)
	if err != nil {
		return RuntimePlanDraft{}, err
	}
	raw := strings.TrimSpace(content)
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```"), "```"))
	}
	if start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}"); start >= 0 && end >= start {
		raw = raw[start : end+1]
	}
	var plan RuntimePlanDraft
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return RuntimePlanDraft{}, fmt.Errorf("decode planner plan: %w", err)
	}
	plan.Summary = truncateAdvisorText(plan.Summary, 1000)
	if len(plan.Steps) == 0 || len(plan.Steps) > 20 {
		return RuntimePlanDraft{}, fmt.Errorf("planner returned invalid step count")
	}
	plan.Planner = "openai-compatible"
	plan.PlannerVersion = "2.0.0"
	return plan, nil
}

func (s *Service) GetRuntimePlannerStatus() domain.StewardRuntimePlannerStatus {
	if s == nil || !s.runtimeR2 {
		return domain.StewardRuntimePlannerStatus{Enabled: false, Provider: "disabled", Reason: "STEWARD_RUNTIME_R2 is disabled", Version: "2.0.0"}
	}
	if s.runtimePlanner == nil {
		return domain.StewardRuntimePlannerStatus{Enabled: false, Provider: "disabled", Reason: "planner is not configured", Version: "2.0.0"}
	}
	return s.runtimePlanner.Status()
}

func (s *Service) PlanAgentRun(ctx context.Context, input PlanAgentRunInput) (domain.StewardAgentRun, error) {
	if err := s.runtimeEnabled(); err != nil {
		return domain.StewardAgentRun{}, err
	}
	if !s.runtimeR2 || s.runtimePlanner == nil {
		return domain.StewardAgentRun{}, ErrRuntimeR2Disabled
	}
	instruction := strings.TrimSpace(input.Instruction)
	if instruction == "" || len([]rune(instruction)) > 8000 {
		return domain.StewardAgentRun{}, fmt.Errorf("%w: instruction is required and must not exceed 8000 characters", ErrAgentRunInvalid)
	}
	dataLevel := strings.ToUpper(defaultString(strings.TrimSpace(input.DataLevel), DataD0))
	if !validRuntimeDataLevel(dataLevel) {
		return domain.StewardAgentRun{}, fmt.Errorf("%w: invalid data_level", ErrAgentRunInvalid)
	}
	plan, err := s.runtimePlanner.Plan(ctx, RuntimePlannerInput{Instruction: instruction, DataLevel: dataLevel, Tools: s.runtimeTools.specs()})
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	if err := s.validatePlannedPrivilegeCapabilities(ctx, plan.Steps); err != nil {
		return domain.StewardAgentRun{}, err
	}
	permissionCeiling := strings.ToUpper(strings.TrimSpace(input.PermissionCeiling))
	if permissionCeiling == "" {
		permissionCeiling = PermissionA0
		for _, step := range plan.Steps {
			if tool, ok := s.runtimeTools.get(step.ToolName); ok {
				required := normalizeRuntimeToolSpec(tool.Spec()).PermissionLevel
				if permissionRank(required) > permissionRank(permissionCeiling) {
					permissionCeiling = required
				}
			}
		}
	}
	autoStart := true
	if input.AutoStart != nil {
		autoStart = *input.AutoStart
	}
	status := s.runtimePlanner.Status()
	plannerName := defaultString(strings.TrimSpace(plan.Planner), status.Provider)
	plannerVersion := defaultString(strings.TrimSpace(plan.PlannerVersion), status.Version)
	planSummary := truncateAdvisorText(strings.TrimSpace(plan.Summary), 1000)
	goal := planSummary
	if goal == "" {
		goal = truncateAdvisorText(instruction, 2000)
	}
	run, err := s.CreateAgentRun(ctx, CreateAgentRunInput{
		Goal: goal, Mode: "planned", IdempotencyKey: input.IdempotencyKey,
		RequestedBy: input.RequestedBy, TargetDevice: input.TargetDevice, DataLevel: dataLevel,
		PermissionCeiling: permissionCeiling, AutoStart: false, Steps: plan.Steps,
		Planner: plannerName, PlannerVersion: plannerVersion, SourceInstruction: instruction, PlanSummary: planSummary,
	})
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	if autoStart && run.Status == RuntimeRunDraft {
		return s.StartAgentRun(ctx, run.ID)
	}
	return run, nil
}

func (s *Service) validatePlannedPrivilegeCapabilities(ctx context.Context, steps []CreateAgentRunStepInput) error {
	for _, step := range steps {
		if step.ToolName != "privilege.execute" {
			continue
		}
		if s == nil || !s.runtimeR3 || s.privilegeBroker == nil || s.privilegeBrokerError != nil {
			return fmt.Errorf("%w: R3 privilege broker is unavailable", ErrRuntimePlannerToolUnavailable)
		}
		capability, err := runtimeRequiredString(step.Arguments, "capability")
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRuntimePlannerToolUnavailable, err)
		}
		brokerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err = s.privilegeBroker.Capability(brokerCtx, strings.ToLower(capability))
		cancel()
		if err != nil {
			return fmt.Errorf("%w: Broker capability %s is not currently available: %v", ErrRuntimePlannerToolUnavailable, capability, err)
		}
	}
	return nil
}
