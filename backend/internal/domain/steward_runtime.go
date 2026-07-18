package domain

import "time"

// StewardAgentRun is a durable execution instance. Its plan hash is immutable
// and binds approvals and idempotency checks to the exact submitted plan.
type StewardAgentRun struct {
	ID                string                 `json:"id"`
	Goal              string                 `json:"goal"`
	Status            string                 `json:"status"`
	Mode              string                 `json:"mode"`
	PlanVersion       int                    `json:"plan_version"`
	PlanHash          string                 `json:"plan_hash"`
	IdempotencyKey    string                 `json:"idempotency_key,omitempty"`
	RequestedBy       string                 `json:"requested_by"`
	TargetDevice      string                 `json:"target_device"`
	DataLevel         string                 `json:"data_level"`
	PermissionCeiling string                 `json:"permission_ceiling"`
	Planner           string                 `json:"planner"`
	PlannerVersion    string                 `json:"planner_version"`
	SourceInstruction string                 `json:"source_instruction,omitempty"`
	PlanSummary       string                 `json:"plan_summary,omitempty"`
	PolicySummary     map[string]any         `json:"policy_summary"`
	CancelRequested   bool                   `json:"cancel_requested"`
	FailureSummary    string                 `json:"failure_summary,omitempty"`
	Steps             []StewardRunStep       `json:"steps"`
	Approvals         []StewardApprovalGrant `json:"approvals"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	StartedAt         *time.Time             `json:"started_at,omitempty"`
	CompletedAt       *time.Time             `json:"completed_at,omitempty"`
}

type StewardRunStep struct {
	ID               string                    `json:"id"`
	RunID            string                    `json:"run_id"`
	Key              string                    `json:"key"`
	Position         int                       `json:"position"`
	Title            string                    `json:"title"`
	ToolName         string                    `json:"tool_name"`
	ToolVersion      string                    `json:"tool_version"`
	Arguments        map[string]any            `json:"arguments"`
	ExpectedOutput   map[string]any            `json:"expected_output,omitempty"`
	DependsOn        []string                  `json:"depends_on"`
	Status           string                    `json:"status"`
	Attempt          int                       `json:"attempt"`
	MaxAttempts      int                       `json:"max_attempts"`
	TimeoutSeconds   int                       `json:"timeout_seconds"`
	IdempotencyKey   string                    `json:"idempotency_key"`
	ToolIdempotency  string                    `json:"tool_idempotency"`
	PolicyDecision   string                    `json:"policy_decision"`
	PolicyReason     string                    `json:"policy_reason"`
	RequiresApproval bool                      `json:"requires_approval"`
	LastError        string                    `json:"last_error,omitempty"`
	Invocations      []StewardToolInvocation   `json:"invocations"`
	Evidence         []StewardEvidenceArtifact `json:"evidence"`
	CreatedAt        time.Time                 `json:"created_at"`
	UpdatedAt        time.Time                 `json:"updated_at"`
	StartedAt        *time.Time                `json:"started_at,omitempty"`
	CompletedAt      *time.Time                `json:"completed_at,omitempty"`
}

type StewardToolSpec struct {
	Name              string         `json:"name"`
	Version           string         `json:"version"`
	Description       string         `json:"description"`
	InputSchema       map[string]any `json:"input_schema"`
	OutputSchema      map[string]any `json:"output_schema"`
	PermissionLevel   string         `json:"permission_level,omitempty"`
	RiskLevel         string         `json:"risk_level,omitempty"`
	SideEffect        string         `json:"side_effect"`
	ApprovalMode      string         `json:"approval_mode,omitempty"`
	IdempotencyMode   string         `json:"idempotency_mode"`
	Deterministic     bool           `json:"deterministic"`
	SupportsCancel    bool           `json:"supports_cancel"`
	DefaultTimeoutSec int            `json:"default_timeout_seconds"`
	UpdatedAt         time.Time      `json:"updated_at,omitempty"`
}

type StewardRuntimePlannerStatus struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Version  string `json:"version"`
}

type StewardAgentRunSummary struct {
	ID                string     `json:"id"`
	Goal              string     `json:"goal"`
	Status            string     `json:"status"`
	Mode              string     `json:"mode"`
	PlanHash          string     `json:"plan_hash"`
	Planner           string     `json:"planner"`
	PermissionCeiling string     `json:"permission_ceiling"`
	DataLevel         string     `json:"data_level"`
	StepCount         int        `json:"step_count"`
	CompletedSteps    int        `json:"completed_steps"`
	RequiresApproval  bool       `json:"requires_approval"`
	FailureSummary    string     `json:"failure_summary,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

type StewardRuntimeControlEvent struct {
	Sequence  int64     `json:"sequence"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	ChangedBy string    `json:"changed_by"`
	CreatedAt time.Time `json:"created_at"`
}

type StewardRuntimeExecutionControl struct {
	// Paused is retained for R2.5 API compatibility. Stopped is the R2.6
	// system-wide execution emergency-stop state.
	Paused     bool                         `json:"paused"`
	Stopped    bool                         `json:"stopped"`
	Generation int64                        `json:"generation"`
	Scopes     []string                     `json:"scopes"`
	Draining   bool                         `json:"draining"`
	Reason     string                       `json:"reason,omitempty"`
	ChangedBy  string                       `json:"changed_by"`
	ChangedAt  time.Time                    `json:"changed_at"`
	Watchdog   StewardRuntimeWatchdogStatus `json:"watchdog"`
	Broker     StewardPrivilegeBrokerStatus `json:"broker"`
	Events     []StewardRuntimeControlEvent `json:"events"`
}

type StewardPrivilegeBrokerStatus struct {
	Configured            bool                               `json:"configured"`
	Reachable             bool                               `json:"reachable"`
	Stopped               bool                               `json:"stopped"`
	Generation            int64                              `json:"generation"`
	InstanceID            string                             `json:"instance_id,omitempty"`
	PolicyDigest          string                             `json:"policy_digest,omitempty"`
	KeyID                 string                             `json:"key_id,omitempty"`
	CapabilityCount       int                                `json:"capability_count"`
	ActiveExecutions      int                                `json:"active_executions"`
	ApprovalProofRequired bool                               `json:"approval_proof_required"`
	ApprovalAuthorities   []StewardApprovalAuthority         `json:"approval_authorities"`
	Error                 string                             `json:"error,omitempty"`
	Capabilities          []StewardPrivilegeBrokerCapability `json:"capabilities"`
}

type StewardApprovalAuthority struct {
	Name           string   `json:"name"`
	Algorithm      string   `json:"algorithm"`
	KeyID          string   `json:"key_id"`
	CredentialID   string   `json:"credential_id,omitempty"`
	RPID           string   `json:"rp_id,omitempty"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
}

type StewardPrivilegeBrokerCapability struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	PermissionLevel  string `json:"permission_level"`
	RiskLevel        string `json:"risk_level"`
	ExecutableName   string `json:"executable_name"`
	ArgumentCount    int    `json:"argument_count"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	CapabilityDigest string `json:"capability_digest"`
}

type StewardRuntimeWatchdogStatus struct {
	Enabled           bool `json:"enabled"`
	LeaseTTLSeconds   int  `json:"lease_ttl_seconds"`
	ActiveInvocations int  `json:"active_invocations"`
	StaleInvocations  int  `json:"stale_invocations"`
}

type StewardToolInvocation struct {
	ID                string         `json:"id"`
	RunID             string         `json:"run_id"`
	StepID            string         `json:"step_id"`
	ToolName          string         `json:"tool_name"`
	ToolVersion       string         `json:"tool_version"`
	Attempt           int            `json:"attempt"`
	IdempotencyKey    string         `json:"idempotency_key"`
	Status            string         `json:"status"`
	Input             map[string]any `json:"input"`
	Output            map[string]any `json:"output,omitempty"`
	ErrorSummary      string         `json:"error_summary,omitempty"`
	LeaseOwner        string         `json:"lease_owner,omitempty"`
	ControlGeneration int64          `json:"control_generation"`
	StartedAt         time.Time      `json:"started_at"`
	FinishedAt        *time.Time     `json:"finished_at,omitempty"`
	HeartbeatAt       *time.Time     `json:"heartbeat_at,omitempty"`
	LeaseExpiresAt    *time.Time     `json:"lease_expires_at,omitempty"`
}

// StewardSystemChangeTransaction is the durable journal for one operating
// system mutation. A transaction is prepared before the tool is invoked and is
// committed only after the tool's postcondition has been independently
// verified. Failed and interrupted mutations retain enough state for a
// compensating rollback after process restart.
type StewardSystemChangeTransaction struct {
	ID                    string         `json:"id"`
	InvocationID          string         `json:"invocation_id"`
	RunID                 string         `json:"run_id"`
	StepID                string         `json:"step_id"`
	ToolName              string         `json:"tool_name"`
	ToolVersion           string         `json:"tool_version"`
	Status                string         `json:"status"`
	Arguments             map[string]any `json:"arguments"`
	Snapshot              map[string]any `json:"snapshot"`
	Result                map[string]any `json:"result"`
	FailureCode           string         `json:"failure_code,omitempty"`
	FailureCategory       string         `json:"failure_category,omitempty"`
	FailureSummary        string         `json:"failure_summary,omitempty"`
	RollbackResult        map[string]any `json:"rollback_result,omitempty"`
	RollbackError         string         `json:"rollback_error,omitempty"`
	RollbackAttempts      int            `json:"rollback_attempts"`
	NextRollbackAttemptAt *time.Time     `json:"next_rollback_attempt_at,omitempty"`
	PreparedAt            time.Time      `json:"prepared_at"`
	CommittedAt           *time.Time     `json:"committed_at,omitempty"`
	RolledBackAt          *time.Time     `json:"rolled_back_at,omitempty"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type StewardApprovalGrant struct {
	ID                     string     `json:"id"`
	RunID                  string     `json:"run_id"`
	PlanHash               string     `json:"plan_hash"`
	Scope                  string     `json:"scope"`
	GrantedBy              string     `json:"granted_by"`
	Status                 string     `json:"status"`
	Reason                 string     `json:"reason,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	RevokedAt              *time.Time `json:"revoked_at,omitempty"`
	ApprovalProofID        string     `json:"approval_proof_id,omitempty"`
	ApprovalKeyID          string     `json:"approval_key_id,omitempty"`
	ApprovalProofExpiresAt *time.Time `json:"approval_proof_expires_at,omitempty"`
}

type StewardEvidenceArtifact struct {
	ID               string         `json:"id"`
	RunID            string         `json:"run_id"`
	StepID           string         `json:"step_id"`
	Kind             string         `json:"kind"`
	Summary          string         `json:"summary"`
	DataLevel        string         `json:"data_level"`
	ContentType      string         `json:"content_type"`
	PayloadState     string         `json:"payload_state"`
	PayloadAvailable bool           `json:"payload_available"`
	SizeBytes        int64          `json:"size_bytes"`
	SHA256           string         `json:"sha256"`
	Redacted         bool           `json:"redacted"`
	Payload          map[string]any `json:"payload,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

type StewardRunEvent struct {
	Sequence  int64          `json:"sequence"`
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	StepID    *string        `json:"step_id,omitempty"`
	Type      string         `json:"type"`
	Status    string         `json:"status"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}
