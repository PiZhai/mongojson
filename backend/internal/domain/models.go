package domain

import (
	"encoding/json"
	"time"
)

type FileRecord struct {
	ID           string     `json:"id"`
	OriginalName string     `json:"original_name"`
	StoredName   string     `json:"stored_name"`
	StoragePath  string     `json:"-"`
	MIMEType     string     `json:"mime_type"`
	SizeBytes    int64      `json:"size_bytes"`
	Category     string     `json:"category"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type JobRecord struct {
	ID           string         `json:"id"`
	ToolType     string         `json:"tool_type"`
	Status       string         `json:"status"`
	InputFileID  *string        `json:"input_file_id,omitempty"`
	OutputFileID *string        `json:"output_file_id,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	ErrorMessage *string        `json:"error_message,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty"`
	ExpiresAt    *time.Time     `json:"expires_at,omitempty"`
}

type PresetRecord struct {
	ID        string         `json:"id"`
	ToolType  string         `json:"tool_type"`
	Name      string         `json:"name"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type MemoRecord struct {
	ID            string             `json:"id"`
	Slug          string             `json:"slug"`
	Title         string             `json:"title"`
	ContentJSON   json.RawMessage    `json:"content_json"`
	ContentHTML   string             `json:"content_html"`
	ContentText   string             `json:"content_text"`
	FloatingCards []MemoFloatingCard `json:"floating_cards"`
	SchemaVersion int                `json:"schema_version"`
	Revision      int64              `json:"revision"`
	EditorType    string             `json:"editor_type"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type MemoDocumentSummary struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	Revision   int64     `json:"revision"`
	EditorType string    `json:"editor_type"`
	NoteCount  int       `json:"note_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type MemoFloatingCard struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type StewardAgentStatus struct {
	AgentID           string                        `json:"agent_id"`
	DeviceName        string                        `json:"device_name"`
	Platform          string                        `json:"platform"`
	Status            string                        `json:"status"`
	Version           string                        `json:"version"`
	EnabledCollectors []string                      `json:"enabled_collectors"`
	StartedAt         *time.Time                    `json:"started_at,omitempty"`
	LastHeartbeatAt   *time.Time                    `json:"last_heartbeat_at,omitempty"`
	LastError         *string                       `json:"last_error,omitempty"`
	BackgroundLoops   []StewardBackgroundLoopStatus `json:"background_loops"`
	UpdatedAt         time.Time                     `json:"updated_at"`
}

type StewardBackgroundLoopStatus struct {
	Name                string     `json:"name"`
	Enabled             bool       `json:"enabled"`
	Running             bool       `json:"running"`
	Interval            string     `json:"interval"`
	LastStartedAt       *time.Time `json:"last_started_at,omitempty"`
	LastCompletedAt     *time.Time `json:"last_completed_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastError           *string    `json:"last_error,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type StewardCollectorConfig struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	ScopeSummary    string         `json:"scope_summary"`
	Settings        map[string]any `json:"settings"`
	ExecutionTarget string         `json:"execution_target"`
	UserOverridden  bool           `json:"user_overridden"`
	LastRunAt       *time.Time     `json:"last_run_at,omitempty"`
	LastError       *string        `json:"last_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	AuditID         *string        `json:"audit_id,omitempty"`
}

type StewardConversation struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Status        string     `json:"status"`
	DataLevel     string     `json:"data_level"`
	MessageCount  int        `json:"message_count"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type StewardConversationMessage struct {
	ID               string                          `json:"id"`
	ConversationID   string                          `json:"conversation_id"`
	Role             string                          `json:"role"`
	Content          string                          `json:"content"`
	DataLevel        string                          `json:"data_level"`
	Model            string                          `json:"model,omitempty"`
	ContextSummary   string                          `json:"context_summary,omitempty"`
	PayloadEncrypted bool                            `json:"payload_encrypted"`
	Suggestions      []StewardConversationSuggestion `json:"suggestions"`
	Executions       []StewardConversationExecution  `json:"executions"`
	Episodes         []StewardAgentEpisode           `json:"episodes"`
	CreatedAt        time.Time                       `json:"created_at"`
}

type StewardAgentToolCall struct {
	ID             string         `json:"id"`
	ToolName       string         `json:"tool_name"`
	Arguments      map[string]any `json:"arguments"`
	TargetDeviceID string         `json:"target_device_id,omitempty"`
}

type StewardAgentToolResult struct {
	ToolCallID string         `json:"tool_call_id"`
	ToolName   string         `json:"tool_name"`
	Output     map[string]any `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	Evidence   map[string]any `json:"evidence,omitempty"`
}

type StewardAgentTurn struct {
	ID                 string                   `json:"id"`
	EpisodeID          string                   `json:"episode_id"`
	RoundIndex         int                      `json:"round_index"`
	Status             string                   `json:"status"`
	AssistantContent   string                   `json:"assistant_content,omitempty"`
	ReasoningContent   string                   `json:"-"`
	ToolCalls          []StewardAgentToolCall   `json:"tool_calls"`
	ToolResults        []StewardAgentToolResult `json:"tool_results"`
	Provider           string                   `json:"provider,omitempty"`
	Model              string                   `json:"model,omitempty"`
	ProviderResponseID string                   `json:"provider_response_id,omitempty"`
	ExecutionID        string                   `json:"execution_id,omitempty"`
	FailureSummary     string                   `json:"failure_summary,omitempty"`
	CreatedAt          time.Time                `json:"created_at"`
	UpdatedAt          time.Time                `json:"updated_at"`
	CompletedAt        *time.Time               `json:"completed_at,omitempty"`
}

// StewardAgentWorkingState is the bounded, durable context carried across
// very long Episodes. The complete turn/evidence history remains immutable in
// steward_agent_turns; this projection keeps the model's hot context small
// without forgetting early paths, identifiers, pending work or evidence.
type StewardAgentWorkingState struct {
	Summary            string   `json:"summary,omitempty"`
	Anchors            []string `json:"anchors,omitempty"`
	PendingItems       []string `json:"pending_items,omitempty"`
	EvidenceReferences []string `json:"evidence_references,omitempty"`
	CompletedRounds    int      `json:"completed_rounds,omitempty"`
}

type StewardAgentTurnPage struct {
	Turns           []StewardAgentTurn `json:"turns"`
	NextBeforeRound int                `json:"next_before_round,omitempty"`
	HasMore         bool               `json:"has_more"`
	Total           int                `json:"total"`
}

type StewardAgentEpisode struct {
	ID                  string                   `json:"id"`
	ConversationID      string                   `json:"conversation_id"`
	TriggerMessageID    string                   `json:"trigger_message_id"`
	ProgressMessageID   string                   `json:"progress_message_id,omitempty"`
	FinalMessageID      string                   `json:"final_message_id,omitempty"`
	TriggerKind         string                   `json:"trigger_kind"`
	Visibility          string                   `json:"visibility,omitempty"`
	ContextRefType      string                   `json:"context_ref_type,omitempty"`
	ContextRefID        string                   `json:"context_ref_id,omitempty"`
	ResultSink          string                   `json:"result_sink,omitempty"`
	IdempotencyKey      string                   `json:"idempotency_key,omitempty"`
	Goal                string                   `json:"goal"`
	DataLevel           string                   `json:"data_level"`
	Status              string                   `json:"status"`
	CurrentRound        int                      `json:"current_round"`
	ToolCallCount       int                      `json:"tool_call_count"`
	MaxRounds           int                      `json:"max_rounds"`
	MaxToolCalls        int                      `json:"max_tool_calls"`
	MaxDurationSeconds  int                      `json:"max_duration_seconds"`
	NoProgressLimit     int                      `json:"no_progress_limit"`
	NoProgressCount     int                      `json:"no_progress_count"`
	ModelFailureCount   int                      `json:"model_failure_count"`
	TargetDeviceID      string                   `json:"target_device_id,omitempty"`
	ActiveExecutionID   string                   `json:"active_execution_id,omitempty"`
	ControlGeneration   int64                    `json:"control_generation,omitempty"`
	FailureSummary      string                   `json:"failure_summary,omitempty"`
	LastResultSummary   string                   `json:"last_result_summary,omitempty"`
	HydratedToolNames   []string                 `json:"hydrated_tool_names,omitempty"`
	CatalogGeneration   int64                    `json:"catalog_generation,omitempty"`
	CurrentToolVersions map[string]string        `json:"current_tool_versions,omitempty"`
	WorkingState        StewardAgentWorkingState `json:"working_state,omitempty"`
	SummaryThroughRound int                      `json:"summary_through_round,omitempty"`
	TurnCount           int                      `json:"turn_count,omitempty"`
	TurnsHasMore        bool                     `json:"turns_has_more,omitempty"`
	Turns               []StewardAgentTurn       `json:"turns,omitempty"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
	DeadlineAt          *time.Time               `json:"deadline_at,omitempty"`
	CompletedAt         *time.Time               `json:"completed_at,omitempty"`
}

// StewardConversationExecution is the durable R4.5 bridge between one
// conversational request and exactly one Runtime V2 run or R4 orchestration.
// The linked executor remains the source of truth for live status and evidence.
type StewardConversationExecution struct {
	ID                   string         `json:"id"`
	ConversationID       string         `json:"conversation_id"`
	MessageID            string         `json:"message_id"`
	RequestMessageID     string         `json:"request_message_id"`
	Instruction          string         `json:"instruction"`
	Summary              string         `json:"summary"`
	Kind                 string         `json:"kind"`
	Status               string         `json:"status"`
	RunID                string         `json:"run_id,omitempty"`
	OrchestrationID      string         `json:"orchestration_id,omitempty"`
	TargetDeviceID       string         `json:"target_device_id"`
	TargetDeviceName     string         `json:"target_device_name"`
	PermissionLevel      string         `json:"permission_level"`
	RiskLevel            string         `json:"risk_level"`
	PlanHash             string         `json:"plan_hash"`
	RequiresConfirmation bool           `json:"requires_confirmation"`
	ConfirmationReason   string         `json:"confirmation_reason,omitempty"`
	Question             string         `json:"question,omitempty"`
	Capability           string         `json:"capability,omitempty"`
	ApprovalSubject      string         `json:"approval_subject,omitempty"`
	ControlGeneration    int64          `json:"control_generation,omitempty"`
	Evidence             map[string]any `json:"evidence"`
	ModelState           map[string]any `json:"-"`
	EpisodeID            string         `json:"episode_id,omitempty"`
	TurnID               string         `json:"turn_id,omitempty"`
	RoundIndex           int            `json:"round_index,omitempty"`
	FailureSummary       string         `json:"failure_summary,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	ConfirmedAt          *time.Time     `json:"confirmed_at,omitempty"`
	CompletedAt          *time.Time     `json:"completed_at,omitempty"`
}

type StewardConversationSuggestion struct {
	ID               string    `json:"id"`
	MessageID        string    `json:"message_id"`
	Kind             string    `json:"kind"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	Content          string    `json:"content"`
	SuggestedAction  string    `json:"suggested_action"`
	DataLevel        string    `json:"data_level"`
	PermissionLevel  string    `json:"permission_level"`
	RiskLevel        string    `json:"risk_level"`
	Status           string    `json:"status"`
	TargetID         *string   `json:"target_id,omitempty"`
	PayloadEncrypted bool      `json:"payload_encrypted"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type StewardEvent struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	Status          string     `json:"status"`
	DeviceID        string     `json:"device_id"`
	UserConfirmed   bool       `json:"user_confirmed"`
	Version         int        `json:"version"`
	AuditID         *string    `json:"audit_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

type StewardTask struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	DeviceID        string     `json:"device_id"`
	RiskLevel       string     `json:"risk_level"`
	UserConfirmed   bool       `json:"user_confirmed"`
	Version         int        `json:"version"`
	AuditID         *string    `json:"audit_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CanceledAt      *time.Time `json:"canceled_at,omitempty"`
}

type StewardAuditLog struct {
	ID              string    `json:"id"`
	OccurredAt      time.Time `json:"occurred_at"`
	Actor           string    `json:"actor"`
	Action          string    `json:"action"`
	TargetType      string    `json:"target_type"`
	TargetID        *string   `json:"target_id,omitempty"`
	Source          string    `json:"source"`
	PermissionLevel string    `json:"permission_level"`
	DataLevel       string    `json:"data_level"`
	InputSummary    string    `json:"input_summary"`
	OutputSummary   string    `json:"output_summary"`
	BeforeSummary   string    `json:"before_summary"`
	AfterSummary    string    `json:"after_summary"`
	Reason          string    `json:"reason"`
	UserConfirmed   bool      `json:"user_confirmed"`
	Syncable        bool      `json:"syncable"`
	Version         int       `json:"version"`
	DeviceID        string    `json:"device_id"`
	ResultStatus    string    `json:"result_status"`
	ErrorSummary    *string   `json:"error_summary,omitempty"`
}

type StewardTimelineSegment struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Status          string     `json:"status"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	DeviceID        string     `json:"device_id"`
	StartAt         *time.Time `json:"start_at,omitempty"`
	EndAt           *time.Time `json:"end_at,omitempty"`
	Confidence      float64    `json:"confidence"`
	UserConfirmed   bool       `json:"user_confirmed"`
	Version         int        `json:"version"`
	AuditID         *string    `json:"audit_id,omitempty"`
	EventCount      int        `json:"event_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

type StewardIntent struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Reason          string     `json:"reason"`
	SuggestedAction string     `json:"suggested_action"`
	RiskLevel       string     `json:"risk_level"`
	Status          string     `json:"status"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	DeviceID        string     `json:"device_id"`
	Confidence      float64    `json:"confidence"`
	UserConfirmed   bool       `json:"user_confirmed"`
	Version         int        `json:"version"`
	AuditID         *string    `json:"audit_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

type StewardMemory struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Content         string     `json:"content"`
	Scope           string     `json:"scope"`
	Status          string     `json:"status"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	DeviceID        string     `json:"device_id"`
	Confidence      float64    `json:"confidence"`
	UserConfirmed   bool       `json:"user_confirmed"`
	Version         int        `json:"version"`
	LastVerifiedAt  *time.Time `json:"last_verified_at,omitempty"`
	AuditID         *string    `json:"audit_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

type StewardMemoryVersion struct {
	ID        string    `json:"id"`
	MemoryID  string    `json:"memory_id"`
	Version   int       `json:"version"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Reason    string    `json:"reason"`
	AuditID   *string   `json:"audit_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type StewardKnowledgeItem struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Source          string     `json:"source"`
	OriginalURI     string     `json:"original_uri"`
	ImportMethod    string     `json:"import_method"`
	ContentHash     string     `json:"content_hash"`
	Status          string     `json:"status"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	DeviceID        string     `json:"device_id"`
	AllowIndex      bool       `json:"allow_index"`
	UserConfirmed   bool       `json:"user_confirmed"`
	Version         int        `json:"version"`
	AuditID         *string    `json:"audit_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

type StewardSourceRef struct {
	ID          string    `json:"id"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	SourceType  string    `json:"source_type"`
	SourceID    string    `json:"source_id"`
	Location    string    `json:"location"`
	Summary     string    `json:"summary"`
	Confidence  float64   `json:"confidence"`
	Sensitive   bool      `json:"sensitive"`
	Displayable bool      `json:"displayable"`
	AuditID     *string   `json:"audit_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type StewardDataTag struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Color       string    `json:"color"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type StewardDevice struct {
	ID               string     `json:"id"`
	DeviceName       string     `json:"device_name"`
	Platform         string     `json:"platform"`
	Role             string     `json:"role"`
	TrustStatus      string     `json:"trust_status"`
	SyncEnabled      bool       `json:"sync_enabled"`
	PermissionLevel  string     `json:"permission_level,omitempty"`
	PublicKey        string     `json:"public_key"`
	APIBaseURL       string     `json:"api_base_url"`
	BrokerPublicKey  string     `json:"broker_public_key,omitempty"`
	BrokerKeyID      string     `json:"broker_key_id,omitempty"`
	LastSyncSequence int64      `json:"last_sync_sequence"`
	LastSentSequence int64      `json:"last_sent_sequence"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
	LastSyncAt       *time.Time `json:"last_sync_at,omitempty"`
	LastSyncError    *string    `json:"last_sync_error,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type StewardDevicePermission struct {
	ID                 string    `json:"id"`
	DeviceID           string    `json:"device_id"`
	Capability         string    `json:"capability"`
	Policy             string    `json:"policy"`
	MaxPermissionLevel string    `json:"max_permission_level"`
	ScopeSummary       string    `json:"scope_summary"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type StewardDeviceCapability struct {
	DeviceID           string    `json:"device_id"`
	Capability         string    `json:"capability"`
	Description        string    `json:"description"`
	TargetType         string    `json:"target_type"`
	RiskLevel          string    `json:"risk_level"`
	MaxPermissionLevel string    `json:"max_permission_level"`
	Version            int       `json:"version"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type StewardSyncChange struct {
	ID             string         `json:"id"`
	Sequence       int64          `json:"sequence"`
	EntityType     string         `json:"entity_type"`
	EntityID       string         `json:"entity_id"`
	Operation      string         `json:"operation"`
	OriginDeviceID string         `json:"origin_device_id"`
	Version        int            `json:"version"`
	DataLevel      string         `json:"data_level"`
	Payload        map[string]any `json:"payload"`
	PayloadHash    string         `json:"payload_hash"`
	SyncStatus     string         `json:"sync_status"`
	ErrorSummary   *string        `json:"error_summary,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	AppliedAt      *time.Time     `json:"applied_at,omitempty"`
}

type StewardSyncConflict struct {
	ID             string     `json:"id"`
	EntityType     string     `json:"entity_type"`
	EntityID       string     `json:"entity_id"`
	LocalChangeID  *string    `json:"local_change_id,omitempty"`
	RemoteChangeID *string    `json:"remote_change_id,omitempty"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	Resolution     string     `json:"resolution"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

type StewardSyncSecurityStatus struct {
	ManagementAPIAddr          string   `json:"management_api_addr"`
	ManagementRemoteAccess     bool     `json:"management_remote_access"`
	PeerAPIAddr                string   `json:"peer_api_addr,omitempty"`
	PeerAPIEnabled             bool     `json:"peer_api_enabled"`
	PublicAPIBase              string   `json:"public_api_base,omitempty"`
	PeerAPIAdvertised          bool     `json:"peer_api_advertised"`
	AuthRequired               bool     `json:"auth_required"`
	InsecureModeActive         bool     `json:"insecure_mode_active"`
	HMACSecretConfigured       bool     `json:"hmac_secret_configured"`
	DevicePrivateKeyConfigured bool     `json:"device_private_key_configured"`
	DevicePrivateKeyValid      bool     `json:"device_private_key_valid"`
	DevicePublicKeyConfigured  bool     `json:"device_public_key_configured"`
	DevicePublicKeyValid       bool     `json:"device_public_key_valid"`
	DeviceSigningReady         bool     `json:"device_signing_ready"`
	DeviceIdentityAdvertisable bool     `json:"device_identity_advertisable"`
	SyncEncryptionConfigured   bool     `json:"sync_encryption_configured"`
	SyncEncryptionKeyID        string   `json:"sync_encryption_key_id,omitempty"`
	SyncPreviousKeyCount       int      `json:"sync_previous_key_count"`
	LocalEncryptionConfigured  bool     `json:"local_encryption_configured"`
	LocalEncryptionKeyID       string   `json:"local_encryption_key_id,omitempty"`
	LocalPreviousKeyCount      int      `json:"local_previous_key_count"`
	ConfigErrors               []string `json:"config_errors"`
}

type StewardDiscoveredPeer struct {
	DeviceID             string    `json:"device_id"`
	DeviceName           string    `json:"device_name"`
	Platform             string    `json:"platform"`
	PeerAPIBase          string    `json:"peer_api_base"`
	PublicKey            string    `json:"public_key"`
	PublicKeyFingerprint string    `json:"public_key_fingerprint"`
	IssuedAt             time.Time `json:"issued_at"`
	LastSeenAt           time.Time `json:"last_seen_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	SignatureVerified    bool      `json:"signature_verified"`
}

type StewardPeerDiscoveryStatus struct {
	Enabled               bool       `json:"enabled"`
	Running               bool       `json:"running"`
	ListenAddr            string     `json:"listen_addr,omitempty"`
	Targets               []string   `json:"targets"`
	CandidateCount        int        `json:"candidate_count"`
	RejectedAnnouncements uint64     `json:"rejected_announcements"`
	LastAnnouncementAt    *time.Time `json:"last_announcement_at,omitempty"`
	LastDiscoveryAt       *time.Time `json:"last_discovery_at,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
}

type StewardSyncStatus struct {
	LocalDevice      StewardDevice              `json:"local_device"`
	Devices          []StewardDevice            `json:"devices"`
	Permissions      []StewardDevicePermission  `json:"permissions"`
	Capabilities     []StewardDeviceCapability  `json:"capabilities"`
	Security         StewardSyncSecurityStatus  `json:"security"`
	Discovery        StewardPeerDiscoveryStatus `json:"discovery"`
	DiscoveredPeers  []StewardDiscoveredPeer    `json:"discovered_peers"`
	PendingChanges   int                        `json:"pending_changes"`
	PendingRelations int                        `json:"pending_relations"`
	ConflictCount    int                        `json:"conflict_count"`
	LastChangeAt     *time.Time                 `json:"last_change_at,omitempty"`
	RecentChanges    []StewardSyncChange        `json:"recent_changes"`
	Conflicts        []StewardSyncConflict      `json:"conflicts"`
	ChangeContract   StewardSyncChangeContract  `json:"change_contract"`
}

type StewardSyncChangeContract struct {
	Healthy        bool     `json:"healthy"`
	CheckedChanges int      `json:"checked_changes"`
	InvalidChanges int      `json:"invalid_changes"`
	Issues         []string `json:"issues"`
}

type StewardAutonomySettings struct {
	ID                string    `json:"id"`
	Paused            bool      `json:"paused"`
	Mode              string    `json:"mode"`
	MaxAutoPermission string    `json:"max_auto_permission,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type StewardAutonomyRule struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	TriggerType        string    `json:"trigger_type"`
	TargetType         string    `json:"target_type"`
	Action             string    `json:"action"`
	Policy             string    `json:"policy"`
	RiskLevel          string    `json:"risk_level"`
	MaxPermissionLevel string    `json:"max_permission_level"`
	Enabled            bool      `json:"enabled"`
	ScopeSummary       string    `json:"scope_summary"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type StewardAutonomyActionCapability struct {
	Action             string `json:"action"`
	Description        string `json:"description"`
	TargetType         string `json:"target_type"`
	RiskLevel          string `json:"risk_level"`
	MaxPermissionLevel string `json:"max_permission_level,omitempty"`
}

type StewardAutonomyProposal struct {
	ID                  string     `json:"id"`
	RuleID              *string    `json:"rule_id,omitempty"`
	SourceEntityType    string     `json:"source_entity_type"`
	SourceEntityID      *string    `json:"source_entity_id,omitempty"`
	Action              string     `json:"action"`
	Title               string     `json:"title"`
	Summary             string     `json:"summary"`
	TriggerReason       string     `json:"trigger_reason"`
	SuggestedAction     string     `json:"suggested_action"`
	RiskLevel           string     `json:"risk_level"`
	PermissionLevel     string     `json:"permission_level"`
	DataLevel           string     `json:"data_level"`
	Status              string     `json:"status"`
	Policy              string     `json:"policy"`
	ImpactSummary       string     `json:"impact_summary"`
	Score               float64    `json:"score"`
	ScoreReason         string     `json:"score_reason"`
	CreatedTaskID       *string    `json:"created_task_id,omitempty"`
	ExecutionTargetType string     `json:"execution_target_type,omitempty"`
	ExecutionTargetID   string     `json:"execution_target_id,omitempty"`
	AuditID             *string    `json:"audit_id,omitempty"`
	FailedAttempts      int        `json:"failed_attempts"`
	RetryEligible       bool       `json:"retry_eligible"`
	RetryExhausted      bool       `json:"retry_exhausted"`
	AutoRetryAt         *time.Time `json:"auto_retry_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type StewardApprovalRequest struct {
	ID                       string                           `json:"id"`
	ProposalID               *string                          `json:"proposal_id,omitempty"`
	RequestedAction          string                           `json:"requested_action"`
	RiskSummary              string                           `json:"risk_summary"`
	PlanSummary              string                           `json:"plan_summary"`
	Status                   string                           `json:"status"`
	DecidedBy                string                           `json:"decided_by"`
	DecisionReason           string                           `json:"decision_reason"`
	CreatedAt                time.Time                        `json:"created_at"`
	DecidedAt                *time.Time                       `json:"decided_at,omitempty"`
	ApprovalProofID          string                           `json:"approval_proof_id,omitempty"`
	ApprovalKeyID            string                           `json:"approval_key_id,omitempty"`
	ApprovalProofExpiresAt   *time.Time                       `json:"approval_proof_expires_at,omitempty"`
	ApprovalProofRequired    bool                             `json:"approval_proof_required"`
	ApprovalProofExpectation *StewardApprovalProofExpectation `json:"approval_proof_expectation,omitempty"`
}

type StewardApprovalProofExpectation struct {
	Subject           string `json:"subject"`
	PlanHash          string `json:"plan_hash"`
	Capability        string `json:"capability"`
	ControlGeneration int64  `json:"control_generation"`
}

type StewardAutonomousRun struct {
	ID            string    `json:"id"`
	ProposalID    *string   `json:"proposal_id,omitempty"`
	RuleID        *string   `json:"rule_id,omitempty"`
	Mode          string    `json:"mode"`
	Status        string    `json:"status"`
	TriggerReason string    `json:"trigger_reason"`
	ImpactSummary string    `json:"impact_summary"`
	RecoveryHint  string    `json:"recovery_hint"`
	AuditID       *string   `json:"audit_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type StewardAutonomyAdvisorStatus struct {
	Enabled             bool       `json:"enabled"`
	Provider            string     `json:"provider"`
	Model               string     `json:"model,omitempty"`
	BaseURL             string     `json:"base_url,omitempty"`
	MaxDataLevel        string     `json:"max_data_level,omitempty"`
	Reason              string     `json:"reason,omitempty"`
	CircuitOpen         bool       `json:"circuit_open,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
	RetryAt             *time.Time `json:"retry_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
}

type StewardAutonomyRetryPolicy struct {
	MaxAttempts int    `json:"max_attempts"`
	Backoff     string `json:"backoff"`
	MaxBackoff  string `json:"max_backoff"`
}

type StewardAutonomyPolicyGateStatus struct {
	Enabled                 bool   `json:"enabled"`
	Backend                 string `json:"backend"`
	CycleReadBarrier        bool   `json:"cycle_read_barrier"`
	ExecutionReadBarrier    bool   `json:"execution_read_barrier"`
	SettingsWriteBarrier    bool   `json:"settings_write_barrier"`
	RuleWriteBarrier        bool   `json:"rule_write_barrier"`
	CurrentRuleRevalidation bool   `json:"current_rule_revalidation"`
}

type StewardAutonomyOverview struct {
	Settings    StewardAutonomySettings           `json:"settings"`
	Advisor     StewardAutonomyAdvisorStatus      `json:"advisor"`
	RetryPolicy StewardAutonomyRetryPolicy        `json:"retry_policy"`
	PolicyGate  StewardAutonomyPolicyGateStatus   `json:"policy_gate"`
	Actions     []StewardAutonomyActionCapability `json:"actions"`
	Rules       []StewardAutonomyRule             `json:"rules"`
	Proposals   []StewardAutonomyProposal         `json:"proposals"`
	Approvals   []StewardApprovalRequest          `json:"approvals"`
	Runs        []StewardAutonomousRun            `json:"runs"`
}

type StewardEntityTag struct {
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Tag        StewardDataTag `json:"tag"`
	Source     string         `json:"source"`
	Confidence float64        `json:"confidence"`
	CreatedAt  time.Time      `json:"created_at"`
}

type StewardSearchResult struct {
	EntityType string    `json:"entity_type"`
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Status     string    `json:"status"`
	DataLevel  string    `json:"data_level"`
	Source     string    `json:"source"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type StewardOverview struct {
	Agent            StewardAgentStatus       `json:"agent"`
	Collectors       []StewardCollectorConfig `json:"collectors"`
	Events           []StewardEvent           `json:"events"`
	TimelineSegments []StewardTimelineSegment `json:"timeline_segments"`
	Tasks            []StewardTask            `json:"tasks"`
	Intents          []StewardIntent          `json:"intents"`
	Memories         []StewardMemory          `json:"memories"`
	KnowledgeItems   []StewardKnowledgeItem   `json:"knowledge_items"`
	SourceRefs       []StewardSourceRef       `json:"source_refs"`
	Tags             []StewardDataTag         `json:"tags"`
	AuditLogs        []StewardAuditLog        `json:"audit_logs"`
	Sync             *StewardSyncStatus       `json:"sync,omitempty"`
	Autonomy         *StewardAutonomyOverview `json:"autonomy,omitempty"`
	Counts           map[string]int           `json:"counts"`
}

type StewardNotificationAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

type StewardNotificationDelivery struct {
	ID                string     `json:"id"`
	NotificationID    string     `json:"notification_id"`
	EndpointID        *string    `json:"endpoint_id,omitempty"`
	Channel           string     `json:"channel"`
	Status            string     `json:"status"`
	ScheduleRevision  int        `json:"schedule_revision"`
	AttemptCount      int        `json:"attempt_count"`
	MaxAttempts       int        `json:"max_attempts"`
	NextAttemptAt     time.Time  `json:"next_attempt_at"`
	ProviderMessageID string     `json:"provider_message_id,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type StewardNotification struct {
	ID                 string                        `json:"id"`
	SourceType         string                        `json:"source_type"`
	SourceID           string                        `json:"source_id,omitempty"`
	Title              string                        `json:"title"`
	Body               string                        `json:"body"`
	Category           string                        `json:"category"`
	Priority           string                        `json:"priority"`
	Status             string                        `json:"status"`
	ScheduleRevision   int                           `json:"schedule_revision"`
	ScheduledAt        time.Time                     `json:"scheduled_at"`
	AllowedWindowStart *time.Time                    `json:"allowed_window_start,omitempty"`
	AllowedWindowEnd   *time.Time                    `json:"allowed_window_end,omitempty"`
	ExpiresAt          *time.Time                    `json:"expires_at,omitempty"`
	DedupeKey          string                        `json:"dedupe_key,omitempty"`
	Actions            []StewardNotificationAction   `json:"actions"`
	Metadata           map[string]any                `json:"metadata"`
	PolicyID           *string                       `json:"policy_id,omitempty"`
	DecisionContext    map[string]any                `json:"decision_context,omitempty"`
	AcknowledgedAt     *time.Time                    `json:"acknowledged_at,omitempty"`
	CancelledAt        *time.Time                    `json:"cancelled_at,omitempty"`
	Deliveries         []StewardNotificationDelivery `json:"deliveries"`
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

type StewardNotificationEndpoint struct {
	ID            string         `json:"id"`
	Channel       string         `json:"channel"`
	Name          string         `json:"name"`
	Enabled       bool           `json:"enabled"`
	Config        map[string]any `json:"config"`
	SecretSet     bool           `json:"secret_set"`
	LastSuccessAt *time.Time     `json:"last_success_at,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type MemoSideNoteRecord struct {
	ID            string          `json:"id"`
	DocumentID    string          `json:"document_id"`
	AnchorBlockID *string         `json:"anchor_block_id,omitempty"`
	BodyJSON      json.RawMessage `json:"body_json"`
	Color         string          `json:"color"`
	SortOrder     int             `json:"sort_order"`
	Collapsed     bool            `json:"collapsed"`
	Status        string          `json:"status"`
	Revision      int64           `json:"revision"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type MusicTrackRecord struct {
	ID               string          `json:"id"`
	FileID           string          `json:"-"`
	LyricFileID      *string         `json:"-"`
	Title            string          `json:"title"`
	Artist           string          `json:"artist,omitempty"`
	Note             string          `json:"note,omitempty"`
	OriginalName     string          `json:"original_name"`
	MIMEType         string          `json:"mime_type"`
	SizeBytes        int64           `json:"size_bytes"`
	Duration         *float64        `json:"duration,omitempty"`
	AudioQuality     json.RawMessage `json:"audio_quality,omitempty"`
	ContentSHA256    string          `json:"-"`
	StoragePath      string          `json:"-"`
	LyricFileName    string          `json:"lyric_file_name,omitempty"`
	LyricMIMEType    string          `json:"lyric_mime_type,omitempty"`
	LyricStoragePath string          `json:"-"`
	FileAvailable    bool            `json:"file_available"`
	RecordIssue      string          `json:"record_issue,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

type CanvasBoardRecord struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Scene     json.RawMessage `json:"scene,omitempty"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type CanvasAssetRecord struct {
	ID           string    `json:"id"`
	BoardID      string    `json:"board_id"`
	FileID       string    `json:"-"`
	CanvasFileID string    `json:"canvas_file_id"`
	OriginalName string    `json:"original_name"`
	MIMEType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	StoragePath  string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}
