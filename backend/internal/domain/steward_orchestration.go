package domain

import "time"

// StewardOrchestrationAgent is a local R4.0 execution role. It is a policy
// identity, not an operating-system principal; all work still executes through
// Runtime V2 and its existing privilege boundaries.
type StewardOrchestrationAgent struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Role              string    `json:"role"`
	Description       string    `json:"description,omitempty"`
	PermissionCeiling string    `json:"permission_ceiling"`
	DataLevelCeiling  string    `json:"data_level_ceiling"`
	ToolAllowlist     []string  `json:"tool_allowlist"`
	MaxConcurrency    int       `json:"max_concurrency"`
	MaxRuntimeSeconds int       `json:"max_runtime_seconds"`
	MaxAttempts       int       `json:"max_attempts"`
	MaxEvidenceBytes  int       `json:"max_evidence_bytes"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type StewardAgentMessage struct {
	ID              string         `json:"id"`
	AgentID         string         `json:"agent_id"`
	OrchestrationID string         `json:"orchestration_id"`
	NodeID          string         `json:"node_id"`
	RuntimeRunID    string         `json:"runtime_run_id"`
	Type            string         `json:"type"`
	Status          string         `json:"status"`
	Payload         map[string]any `json:"payload"`
	Attempt         int            `json:"attempt"`
	MaxAttempts     int            `json:"max_attempts"`
	LeaseOwner      string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time     `json:"lease_expires_at,omitempty"`
	AvailableAt     time.Time      `json:"available_at"`
	LastError       string         `json:"last_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	AcknowledgedAt  *time.Time     `json:"acknowledged_at,omitempty"`
}

type StewardAgentWorkerStatus struct {
	WorkerID       string     `json:"worker_id"`
	AgentID        string     `json:"agent_id"`
	Status         string     `json:"status"`
	ProcessID      int        `json:"process_id"`
	CurrentMessage string     `json:"current_message,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	HeartbeatAt    time.Time  `json:"heartbeat_at"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
}

type StewardOrchestration struct {
	ID                string                              `json:"id"`
	Goal              string                              `json:"goal"`
	Status            string                              `json:"status"`
	PlanHash          string                              `json:"plan_hash"`
	IdempotencyKey    string                              `json:"idempotency_key,omitempty"`
	RequestedBy       string                              `json:"requested_by"`
	PermissionCeiling string                              `json:"permission_ceiling"`
	DataLevel         string                              `json:"data_level"`
	FailurePolicy     string                              `json:"failure_policy"`
	MaxParallel       int                                 `json:"max_parallel"`
	MaxChildren       int                                 `json:"max_children"`
	ControlGeneration int64                               `json:"control_generation"`
	FailureSummary    string                              `json:"failure_summary,omitempty"`
	Nodes             []StewardOrchestrationNode          `json:"nodes"`
	Evidence          StewardOrchestrationEvidenceSummary `json:"evidence"`
	Events            []StewardOrchestrationEvent         `json:"events"`
	Messages          []StewardAgentMessage               `json:"messages"`
	Workers           []StewardAgentWorkerStatus          `json:"workers"`
	CreatedAt         time.Time                           `json:"created_at"`
	UpdatedAt         time.Time                           `json:"updated_at"`
	StartedAt         *time.Time                          `json:"started_at,omitempty"`
	CompletedAt       *time.Time                          `json:"completed_at,omitempty"`
	DeadlineAt        *time.Time                          `json:"deadline_at,omitempty"`
}

type StewardOrchestrationNode struct {
	ID                string                     `json:"id"`
	OrchestrationID   string                     `json:"orchestration_id"`
	Key               string                     `json:"key"`
	Position          int                        `json:"position"`
	AgentID           string                     `json:"agent_id"`
	Goal              string                     `json:"goal"`
	Kind              string                     `json:"kind"`
	CompensationOfID  string                     `json:"compensation_of_id,omitempty"`
	TargetDevice      string                     `json:"target_device"`
	SelectedDeviceID  string                     `json:"selected_device_id,omitempty"`
	RemoteDispatch    *StewardRemoteDispatch     `json:"remote_dispatch,omitempty"`
	RemotePrivilege   *StewardRemotePrivilege    `json:"remote_privilege,omitempty"`
	Status            string                     `json:"status"`
	DependsOn         []string                   `json:"depends_on"`
	PermissionCeiling string                     `json:"permission_ceiling"`
	DataLevel         string                     `json:"data_level"`
	RuntimeRunID      string                     `json:"runtime_run_id,omitempty"`
	Delegation        StewardDelegationClaim     `json:"delegation,omitempty"`
	Steps             []StewardOrchestrationStep `json:"steps"`
	CompensationSteps []StewardOrchestrationStep `json:"compensation_steps,omitempty"`
	FailureSummary    string                     `json:"failure_summary,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
	StartedAt         *time.Time                 `json:"started_at,omitempty"`
	CompletedAt       *time.Time                 `json:"completed_at,omitempty"`
}

type StewardRemotePrivilege struct {
	Required          bool       `json:"required"`
	Status            string     `json:"status"`
	Capability        string     `json:"capability"`
	CredentialRefs    []string   `json:"credential_refs,omitempty"`
	Subject           string     `json:"subject,omitempty"`
	PlanHash          string     `json:"plan_hash,omitempty"`
	TargetBrokerKeyID string     `json:"target_broker_key_id,omitempty"`
	DelegationID      string     `json:"delegation_id,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

type StewardRemoteDispatch struct {
	ID              string         `json:"id"`
	OrchestrationID string         `json:"orchestration_id"`
	NodeID          string         `json:"node_id"`
	TargetDeviceID  string         `json:"target_device_id"`
	Status          string         `json:"status"`
	PlanHash        string         `json:"plan_hash"`
	Attempt         int            `json:"attempt"`
	LeaseExpiresAt  *time.Time     `json:"lease_expires_at,omitempty"`
	HeartbeatAt     *time.Time     `json:"heartbeat_at,omitempty"`
	RemoteRunID     string         `json:"remote_run_id,omitempty"`
	ResultPayload   map[string]any `json:"result_payload,omitempty"`
	ResultSignature string         `json:"result_signature,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

type StewardOrchestrationStep struct {
	Key              string         `json:"key"`
	Title            string         `json:"title,omitempty"`
	ToolName         string         `json:"tool_name"`
	ToolVersion      string         `json:"tool_version,omitempty"`
	Arguments        map[string]any `json:"arguments"`
	ExpectedOutput   map[string]any `json:"expected_output,omitempty"`
	DependsOn        []string       `json:"depends_on,omitempty"`
	MaxAttempts      int            `json:"max_attempts,omitempty"`
	TimeoutSeconds   int            `json:"timeout_seconds,omitempty"`
	RequiresApproval bool           `json:"requires_approval,omitempty"`
}

// StewardDelegationClaim binds one local Agent role to one immutable node plan.
// The Ed25519 signature is checked again when Runtime V2 claims the child run.
type StewardDelegationClaim struct {
	ID                string    `json:"id,omitempty"`
	AgentID           string    `json:"agent_id,omitempty"`
	NodeID            string    `json:"node_id,omitempty"`
	PlanHash          string    `json:"plan_hash,omitempty"`
	PermissionCeiling string    `json:"permission_ceiling,omitempty"`
	DataLevel         string    `json:"data_level,omitempty"`
	ControlGeneration int64     `json:"control_generation,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	Signature         string    `json:"signature,omitempty"`
}

type StewardOrchestrationEvidenceSummary struct {
	ChildRunCount  int      `json:"child_run_count"`
	ArtifactCount  int      `json:"artifact_count"`
	RedactedCount  int      `json:"redacted_count"`
	DataLevels     []string `json:"data_levels"`
	ManifestSHA256 string   `json:"manifest_sha256,omitempty"`
}

type StewardOrchestrationEvent struct {
	Sequence        int64          `json:"sequence"`
	ID              string         `json:"id"`
	OrchestrationID string         `json:"orchestration_id"`
	NodeID          string         `json:"node_id,omitempty"`
	Type            string         `json:"type"`
	Status          string         `json:"status"`
	Message         string         `json:"message"`
	Payload         map[string]any `json:"payload"`
	CreatedAt       time.Time      `json:"created_at"`
}
