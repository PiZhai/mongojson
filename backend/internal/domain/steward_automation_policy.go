package domain

import "time"

// StewardDataPolicy controls collection and model disclosure independently for
// one data level. SourcePattern may be "*" or a glob such as "collector:*".
type StewardDataPolicy struct {
	ID                    string     `json:"id"`
	DataLevel             string     `json:"data_level"`
	SourcePattern         string     `json:"source_pattern"`
	CollectMode           string     `json:"collect_mode"`
	ModelMode             string     `json:"model_mode"`
	ModelContentMode      string     `json:"model_content_mode"`
	AllowLocalPersistence bool       `json:"allow_local_persistence"`
	AllowSync             bool       `json:"allow_sync"`
	RequireEncryption     bool       `json:"require_encryption"`
	ConsentExpiresAt      *time.Time `json:"consent_expires_at,omitempty"`
	Description           string     `json:"description"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// StewardPermissionPolicy is the final execution gate for an operation
// permission level. ActionPattern supports action-specific overrides.
type StewardPermissionPolicy struct {
	ID                string    `json:"id"`
	PermissionLevel   string    `json:"permission_level"`
	ActionPattern     string    `json:"action_pattern"`
	ExecutionMode     string    `json:"execution_mode"`
	RequireSimulation bool      `json:"require_simulation"`
	RequireRollback   bool      `json:"require_rollback"`
	MaxBatchSize      int       `json:"max_batch_size"`
	CooldownSeconds   int       `json:"cooldown_seconds"`
	Description       string    `json:"description"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type StewardModelDispatch struct {
	ID              string     `json:"id"`
	ObservationID   string     `json:"observation_id"`
	ObservationTime time.Time  `json:"observation_time"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	ContentMode     string     `json:"content_mode"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	RequestSummary  string     `json:"request_summary"`
	ResponseSummary string     `json:"response_summary"`
	LastError       string     `json:"last_error,omitempty"`
	NextAttemptAt   *time.Time `json:"next_attempt_at,omitempty"`
	Provider        string     `json:"provider"`
	Model           string     `json:"model"`
	AuditID         *string    `json:"audit_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// StewardProactiveRun records one model-led daily or weekly reflection. The
// model decides whether the outcome is silence, a conversation message, or a
// governed execution; the record exists for deduplication and auditability.
type StewardProactiveRun struct {
	ID             string         `json:"id"`
	Cadence        string         `json:"cadence"`
	PeriodKey      string         `json:"period_key"`
	PeriodStart    time.Time      `json:"period_start"`
	PeriodEnd      time.Time      `json:"period_end"`
	Status         string         `json:"status"`
	Summary        string         `json:"summary"`
	Analysis       map[string]any `json:"analysis"`
	Decision       string         `json:"decision"`
	ConversationID *string        `json:"conversation_id,omitempty"`
	MessageID      *string        `json:"message_id,omitempty"`
	ExecutionID    *string        `json:"execution_id,omitempty"`
	EpisodeID      *string        `json:"episode_id,omitempty"`
	Provider       string         `json:"provider"`
	Model          string         `json:"model"`
	ErrorSummary   string         `json:"error_summary,omitempty"`
	AuditID        *string        `json:"audit_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
}

type StewardToolDefinition struct {
	ID                 string    `json:"id"`
	Action             string    `json:"action"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	Executable         string    `json:"executable"`
	Arguments          []string  `json:"arguments"`
	WorkingDirectory   string    `json:"working_directory"`
	PermissionLevel    string    `json:"permission_level"`
	RiskLevel          string    `json:"risk_level"`
	Enabled            bool      `json:"enabled"`
	TimeoutSeconds     int       `json:"timeout_seconds"`
	RollbackExecutable string    `json:"rollback_executable"`
	RollbackArguments  []string  `json:"rollback_arguments"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
