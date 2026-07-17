package steward

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	primaryModelSettingsID  = "primary"
	modelSettingsScope      = "steward-model-settings"
	modelSettingsSourceDB   = "database"
	modelSettingsSourceEnv  = "environment"
	modelSettingsSourceNone = "default"
)

type StewardModelSettings struct {
	Provider         string                              `json:"provider"`
	BaseURL          string                              `json:"base_url"`
	Model            string                              `json:"model"`
	APIKeyConfigured bool                                `json:"api_key_configured"`
	APIKeyMask       string                              `json:"api_key_mask,omitempty"`
	AllowNoAPIKey    bool                                `json:"allow_no_api_key"`
	MaxDataLevel     string                              `json:"max_data_level,omitempty"`
	TimeoutSeconds   int                                 `json:"timeout_seconds"`
	Source           string                              `json:"source"`
	Advisor          domain.StewardAutonomyAdvisorStatus `json:"advisor"`
	Planner          domain.StewardRuntimePlannerStatus  `json:"planner"`
	UpdatedAt        *time.Time                          `json:"updated_at,omitempty"`
}

type UpdateStewardModelSettingsInput struct {
	Provider       *string `json:"provider"`
	BaseURL        *string `json:"base_url"`
	Model          *string `json:"model"`
	APIKey         *string `json:"api_key"`
	AllowNoAPIKey  *bool   `json:"allow_no_api_key"`
	MaxDataLevel   *string `json:"max_data_level"`
	TimeoutSeconds *int    `json:"timeout_seconds"`
}

type modelSettingsValues struct {
	provider       string
	baseURL        string
	model          string
	apiKey         string
	allowNoAPIKey  bool
	maxDataLevel   string
	timeoutSeconds int
	source         string
	updatedAt      *time.Time
}

func (s *Service) GetModelSettings(ctx context.Context) (StewardModelSettings, error) {
	values, err := s.loadModelSettings(ctx)
	if err != nil {
		return StewardModelSettings{}, err
	}
	return s.publicModelSettings(values), nil
}

func (s *Service) UpdateModelSettings(ctx context.Context, input UpdateStewardModelSettingsInput) (StewardModelSettings, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return StewardModelSettings{}, errors.New("steward database is not configured")
	}
	current, err := s.loadModelSettings(ctx)
	if err != nil {
		return StewardModelSettings{}, err
	}
	if input.Provider != nil {
		current.provider = strings.ToLower(strings.TrimSpace(*input.Provider))
	}
	if input.BaseURL != nil {
		current.baseURL = strings.TrimSpace(*input.BaseURL)
	}
	if input.Model != nil {
		current.model = strings.TrimSpace(*input.Model)
	}
	if input.APIKey != nil {
		current.apiKey = strings.TrimSpace(*input.APIKey)
	}
	if input.AllowNoAPIKey != nil {
		current.allowNoAPIKey = *input.AllowNoAPIKey
	}
	if input.MaxDataLevel != nil {
		current.maxDataLevel = strings.ToUpper(strings.TrimSpace(*input.MaxDataLevel))
	}
	if input.TimeoutSeconds != nil {
		current.timeoutSeconds = *input.TimeoutSeconds
	}
	current.source = modelSettingsSourceDB
	if err := validateModelSettings(current); err != nil {
		return StewardModelSettings{}, err
	}

	encryptedKey := map[string]any{}
	if current.apiKey != "" {
		keyring, err := localPayloadKeyringFromEnv()
		if err != nil {
			return StewardModelSettings{}, fmt.Errorf("secure model API key storage requires STEWARD_LOCAL_ENCRYPTION_KEY: %w", err)
		}
		encryptedKey, err = encryptPayloadEnvelope(keyring, modelSettingsAAD(), map[string]any{"api_key": current.apiKey}, SyncEncryptionScopeLocalAtRest)
		if err != nil {
			return StewardModelSettings{}, fmt.Errorf("encrypt model API key: %w", err)
		}
	}
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_model_settings (
			id, provider, base_url, model, api_key_encrypted, allow_no_api_key, max_data_level, timeout_seconds, updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		on conflict (id) do update set
			provider=excluded.provider, base_url=excluded.base_url, model=excluded.model,
			api_key_encrypted=excluded.api_key_encrypted, allow_no_api_key=excluded.allow_no_api_key,
			max_data_level=excluded.max_data_level, timeout_seconds=excluded.timeout_seconds, updated_at=excluded.updated_at
	`, primaryModelSettingsID, current.provider, current.baseURL, current.model, encryptedKey,
		current.allowNoAPIKey, current.maxDataLevel, current.timeoutSeconds, now)
	if err != nil {
		return StewardModelSettings{}, fmt.Errorf("save model settings: %w", err)
	}
	current.updatedAt = &now
	s.applyModelSettings(current)

	userConfirmed := true
	syncable := false
	targetID := primaryModelSettingsID
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor: "user", Action: "model.settings.update", TargetType: "model_settings", TargetID: &targetID,
		Source: "web", PermissionLevel: PermissionA3, DataLevel: DataD0,
		InputSummary:  strings.Join([]string{current.provider, current.baseURL, current.model}, " / "),
		OutputSummary: "model connection updated; secret retained only as encrypted local state",
		UserConfirmed: &userConfirmed, Syncable: &syncable, ResultStatus: ResultOK,
	})
	return s.publicModelSettings(current), nil
}

func (s *Service) reloadPersistedModelSettings(ctx context.Context) error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil
	}
	values, err := s.loadPersistedModelSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateModelSettings(values); err != nil {
		return fmt.Errorf("stored model settings are invalid: %w", err)
	}
	s.applyModelSettings(values)
	return nil
}

func (s *Service) loadModelSettings(ctx context.Context) (modelSettingsValues, error) {
	if s != nil && s.db != nil && s.db.Pool != nil {
		values, err := s.loadPersistedModelSettings(ctx)
		if err == nil {
			return values, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return modelSettingsValues{}, err
		}
	}
	return modelSettingsFromEnv(), nil
}

func (s *Service) loadPersistedModelSettings(ctx context.Context) (modelSettingsValues, error) {
	var values modelSettingsValues
	var encryptedKey map[string]any
	var updatedAt time.Time
	err := s.db.Pool.QueryRow(ctx, `
		select provider, base_url, model, api_key_encrypted, allow_no_api_key, max_data_level, timeout_seconds, updated_at
		from steward_model_settings where id=$1
	`, primaryModelSettingsID).Scan(&values.provider, &values.baseURL, &values.model, &encryptedKey,
		&values.allowNoAPIKey, &values.maxDataLevel, &values.timeoutSeconds, &updatedAt)
	if err != nil {
		return modelSettingsValues{}, err
	}
	if len(encryptedKey) > 0 {
		keyring, err := localPayloadKeyringFromEnv()
		if err != nil {
			return modelSettingsValues{}, fmt.Errorf("decrypt model API key: %w", err)
		}
		payload, err := decryptPayloadEnvelope(keyring, modelSettingsAAD(), encryptedKey, "model API key")
		if err != nil {
			return modelSettingsValues{}, err
		}
		values.apiKey, _ = payload["api_key"].(string)
	}
	values.source = modelSettingsSourceDB
	values.updatedAt = &updatedAt
	return values, nil
}

func modelSettingsFromEnv() modelSettingsValues {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("STEWARD_LLM_PROVIDER")))
	if provider == "" || provider == "off" || provider == "none" {
		provider = "disabled"
	}
	allowNoKey, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("STEWARD_LLM_ALLOW_NO_API_KEY")))
	return modelSettingsValues{
		provider:       provider,
		baseURL:        strings.TrimSpace(envOrDefault("STEWARD_LLM_BASE_URL", "https://api.openai.com/v1")),
		model:          strings.TrimSpace(os.Getenv("STEWARD_LLM_MODEL")),
		apiKey:         strings.TrimSpace(os.Getenv("STEWARD_LLM_API_KEY")),
		allowNoAPIKey:  allowNoKey,
		maxDataLevel:   strings.ToUpper(strings.TrimSpace(envOrDefault("STEWARD_LLM_MAX_DATA_LEVEL", DataD1))),
		timeoutSeconds: int(durationEnv("STEWARD_LLM_TIMEOUT", 30*time.Second) / time.Second),
		source:         modelSettingsSourceEnv,
	}
}

func validateModelSettings(values modelSettingsValues) error {
	if values.provider == "disabled" {
		return nil
	}
	if values.provider != advisorProviderOpenAICompatible && values.provider != "openai" {
		return errors.New("provider must be disabled or openai-compatible")
	}
	if values.model == "" {
		return errors.New("model is required")
	}
	parsed, err := url.Parse(values.baseURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("base_url must be an absolute http or https URL")
	}
	if values.apiKey == "" && !values.allowNoAPIKey {
		return errors.New("API key is required unless no-key mode is enabled")
	}
	if values.allowNoAPIKey && !runtimePlannerLoopbackHost(parsed.Hostname()) {
		return errors.New("no-key mode is restricted to localhost endpoints")
	}
	if !validDataLevel(values.maxDataLevel) {
		return errors.New("max_data_level must be D0-D6")
	}
	if values.timeoutSeconds < 1 || values.timeoutSeconds > 120 {
		return errors.New("timeout_seconds must be between 1 and 120")
	}
	return nil
}

func (s *Service) applyModelSettings(values modelSettingsValues) {
	advisor, planner := modelClientsFromSettings(values)
	s.modelSettingsMu.Lock()
	s.advisor = resilientAutonomyAdvisorFromEnv(advisor)
	s.runtimePlanner = planner
	s.modelSettingsMu.Unlock()
}

func modelClientsFromSettings(values modelSettingsValues) (AutonomyAdvisor, RuntimePlanner) {
	local := localRuntimePlanner{}
	if values.provider == "disabled" {
		return DisabledAutonomyAdvisor("model connection is disabled"), chainedRuntimePlanner{local: local}
	}
	timeout := time.Duration(values.timeoutSeconds) * time.Second
	baseURL := strings.TrimRight(values.baseURL, "/")
	advisor := openAICompatibleAutonomyAdvisor{
		client: &http.Client{Timeout: timeout}, baseURL: baseURL, apiKey: values.apiKey,
		model: values.model, maxDataLevel: values.maxDataLevel,
	}
	planner := &openAICompatibleRuntimePlanner{
		client: &http.Client{Timeout: timeout}, baseURL: baseURL, apiKey: values.apiKey,
		model: values.model, maxDataLevel: values.maxDataLevel,
	}
	return advisor, chainedRuntimePlanner{local: local, fallback: planner}
}

func (s *Service) publicModelSettings(values modelSettingsValues) StewardModelSettings {
	result := StewardModelSettings{
		Provider: values.provider, BaseURL: values.baseURL, Model: values.model,
		APIKeyConfigured: values.apiKey != "", AllowNoAPIKey: values.allowNoAPIKey,
		MaxDataLevel: values.maxDataLevel, TimeoutSeconds: values.timeoutSeconds,
		Source: defaultString(values.source, modelSettingsSourceNone), UpdatedAt: values.updatedAt,
		Advisor: s.autonomyAdvisor().Status(), Planner: s.runtimePlannerValue().Status(),
	}
	if ownerModeEnabled() {
		result.MaxDataLevel = ""
	}
	if values.apiKey != "" {
		result.APIKeyMask = "••••••••" + lastRunes(values.apiKey, 4)
	}
	return result
}

func modelSettingsAAD() string { return modelSettingsScope + ":" + primaryModelSettingsID }

func lastRunes(value string, count int) string {
	chars := []rune(value)
	if len(chars) <= count {
		return string(chars)
	}
	return string(chars[len(chars)-count:])
}
