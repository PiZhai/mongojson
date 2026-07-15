package domain

import "time"

type StewardObservation struct {
	ID               string         `json:"id"`
	Source           string         `json:"source"`
	Type             string         `json:"type"`
	Summary          string         `json:"summary"`
	DataLevel        string         `json:"data_level"`
	PermissionLevel  string         `json:"permission_level"`
	DeviceID         string         `json:"device_id"`
	ContextKey       string         `json:"context_key"`
	Fingerprint      string         `json:"fingerprint"`
	PayloadEncrypted bool           `json:"payload_encrypted"`
	HasMedia         bool           `json:"has_media"`
	MediaType        string         `json:"media_type,omitempty"`
	MediaSizeBytes   int64          `json:"media_size_bytes,omitempty"`
	Status           string         `json:"status"`
	SystemGenerated  bool           `json:"system_generated"`
	RetentionLocked  bool           `json:"retention_locked"`
	DuplicateCount   int            `json:"duplicate_count"`
	SessionID        *string        `json:"session_id,omitempty"`
	OccurredAt       time.Time      `json:"occurred_at"`
	EndedAt          *time.Time     `json:"ended_at,omitempty"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type StewardActivitySession struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	Source           string    `json:"source"`
	ContextKey       string    `json:"context_key"`
	DeviceID         string    `json:"device_id"`
	DataLevel        string    `json:"data_level"`
	Status           string    `json:"status"`
	ObservationCount int       `json:"observation_count"`
	Confidence       float64   `json:"confidence"`
	ValueScore       float64   `json:"value_score"`
	StartedAt        time.Time `json:"started_at"`
	EndedAt          time.Time `json:"ended_at"`
	TimelineID       *string   `json:"timeline_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type StewardEntity struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	CanonicalKey   string     `json:"canonical_key"`
	DisplayName    string     `json:"display_name"`
	Summary        string     `json:"summary"`
	DataLevel      string     `json:"data_level"`
	Status         string     `json:"status"`
	Confidence     float64    `json:"confidence"`
	EvidenceCount  int        `json:"evidence_count"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type StewardRelationEvidence struct {
	ID              string     `json:"id"`
	RelationID      string     `json:"relation_id"`
	SourceRefID     *string    `json:"source_ref_id,omitempty"`
	ObservationID   *string    `json:"observation_id,omitempty"`
	ObservationTime *time.Time `json:"observation_time,omitempty"`
	EvidenceType    string     `json:"evidence_type"`
	Summary         string     `json:"summary"`
	Confidence      float64    `json:"confidence"`
	CreatedAt       time.Time  `json:"created_at"`
}

type StewardRelation struct {
	ID             string                    `json:"id"`
	SourceEntityID string                    `json:"source_entity_id"`
	TargetEntityID string                    `json:"target_entity_id"`
	SourceEntity   *StewardEntity            `json:"source_entity,omitempty"`
	TargetEntity   *StewardEntity            `json:"target_entity,omitempty"`
	RelationType   string                    `json:"relation_type"`
	Confidence     float64                   `json:"confidence"`
	EvidenceCount  int                       `json:"evidence_count"`
	FirstSeenAt    time.Time                 `json:"first_seen_at"`
	LastSeenAt     time.Time                 `json:"last_seen_at"`
	ValidFrom      *time.Time                `json:"valid_from,omitempty"`
	ValidTo        *time.Time                `json:"valid_to,omitempty"`
	DataLevel      string                    `json:"data_level"`
	Status         string                    `json:"status"`
	InferenceState string                    `json:"inference_state"`
	Evidence       []StewardRelationEvidence `json:"evidence"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type StewardHabit struct {
	ID              string     `json:"id"`
	EntityID        *string    `json:"entity_id,omitempty"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Pattern         string     `json:"pattern"`
	Status          string     `json:"status"`
	DataLevel       string     `json:"data_level"`
	Confidence      float64    `json:"confidence"`
	EvidenceCount   int        `json:"evidence_count"`
	ValueScore      float64    `json:"value_score"`
	UserConfirmed   bool       `json:"user_confirmed"`
	RetentionLocked bool       `json:"retention_locked"`
	LastEvidenceAt  *time.Time `json:"last_evidence_at,omitempty"`
	QuarantinedAt   *time.Time `json:"quarantined_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type StewardInsight struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	SuggestedAction string     `json:"suggested_action"`
	Status          string     `json:"status"`
	DataLevel       string     `json:"data_level"`
	Confidence      float64    `json:"confidence"`
	EvidenceCount   int        `json:"evidence_count"`
	ValueScore      float64    `json:"value_score"`
	UserConfirmed   bool       `json:"user_confirmed"`
	RetentionLocked bool       `json:"retention_locked"`
	QuarantinedAt   *time.Time `json:"quarantined_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type StewardRetentionPolicy struct {
	ID                    string    `json:"id"`
	SourcePattern         string    `json:"source_pattern"`
	DataKind              string    `json:"data_kind"`
	DataLevel             string    `json:"data_level"`
	TTLDays               float64   `json:"ttl_days"`
	QuarantineDays        int       `json:"quarantine_days"`
	AutoPurge             bool      `json:"auto_purge"`
	RequirePreview        bool      `json:"require_preview"`
	ProtectUserConfirmed  bool      `json:"protect_user_confirmed"`
	ProtectReferenced     bool      `json:"protect_referenced"`
	DeletionTombstoneDays int       `json:"deletion_tombstone_days"`
	Description           string    `json:"description"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type StewardLifecycleLayerStatus struct {
	Kind             string `json:"kind"`
	Count            int    `json:"count"`
	Bytes            int64  `json:"bytes"`
	ExpiredCount     int    `json:"expired_count"`
	QuarantinedCount int    `json:"quarantined_count"`
}

type StewardLifecycleStatus struct {
	Profile              string                        `json:"profile"`
	VectorSearchEnabled  bool                          `json:"vector_search_enabled"`
	LocalEncryptionReady bool                          `json:"local_encryption_ready"`
	Layers               []StewardLifecycleLayerStatus `json:"layers"`
	RetentionPolicies    []StewardRetentionPolicy      `json:"retention_policies"`
	LastRuns             map[string]*time.Time         `json:"last_runs"`
	NextExpiringAt       *time.Time                    `json:"next_expiring_at,omitempty"`
	UpdatedAt            time.Time                     `json:"updated_at"`
}

type StewardLifecycleAction struct {
	TargetType      string     `json:"target_type"`
	TargetID        string     `json:"target_id"`
	Action          string     `json:"action"`
	Reason          string     `json:"reason"`
	ValueScore      float64    `json:"value_score"`
	RequiresPreview bool       `json:"requires_preview"`
	RecoverableTo   *time.Time `json:"recoverable_to,omitempty"`
}

type StewardLifecycleEvaluation struct {
	ID          string                   `json:"id"`
	DryRun      bool                     `json:"dry_run"`
	EvaluatedAt time.Time                `json:"evaluated_at"`
	Actions     []StewardLifecycleAction `json:"actions"`
	Counts      map[string]int           `json:"counts"`
}

type StewardPurgeResult struct {
	AuditID     string                   `json:"audit_id"`
	DryRun      bool                     `json:"dry_run"`
	Deleted     int                      `json:"deleted"`
	Quarantined int                      `json:"quarantined"`
	Skipped     int                      `json:"skipped"`
	Actions     []StewardLifecycleAction `json:"actions"`
	CompletedAt time.Time                `json:"completed_at"`
}
