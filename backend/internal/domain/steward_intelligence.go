package domain

import "time"

const (
	StewardProfileHorizonRecent   = "recent"
	StewardProfileHorizonStable   = "stable"
	StewardProfileHorizonExplicit = "explicit"
	StewardProfileHorizonMerged   = "merged"

	StewardProfileFactActive     = "active"
	StewardProfileFactSuperseded = "superseded"
	StewardProfileFactRejected   = "rejected"

	StewardReportDaily   = "daily"
	StewardReportWeekly  = "weekly"
	StewardReportMonthly = "monthly"
)

// StewardProfileFact is an immutable, evidence-linked version of one user
// profile assertion. Corrections and refreshed inferences create a new row;
// older rows remain available for explanation and audit.
type StewardProfileFact struct {
	ID         string         `json:"id"`
	Key        string         `json:"key"`
	Value      map[string]any `json:"value"`
	Summary    string         `json:"summary"`
	Horizon    string         `json:"horizon"`
	Status     string         `json:"status"`
	Version    int            `json:"version"`
	Confidence float64        `json:"confidence"`
	// EffectiveConfidence preserves the asserted confidence above while exposing
	// conflict penalties and recent-layer time decay in a materialized snapshot.
	EffectiveConfidence float64                  `json:"effective_confidence"`
	EvidenceCount       int                      `json:"evidence_count"`
	EvidenceDays        int                      `json:"evidence_days"`
	UserConfirmed       bool                     `json:"user_confirmed"`
	ConflictGroup       string                   `json:"conflict_group"`
	SupersedesFactID    *string                  `json:"supersedes_fact_id,omitempty"`
	CreatedBy           string                   `json:"created_by"`
	JobID               *string                  `json:"job_id,omitempty"`
	Provider            string                   `json:"provider,omitempty"`
	Model               string                   `json:"model,omitempty"`
	ValidFrom           time.Time                `json:"valid_from"`
	ValidTo             *time.Time               `json:"valid_to,omitempty"`
	Evidence            []StewardProfileEvidence `json:"evidence,omitempty"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
}

type StewardProfileEvidence struct {
	ID          string    `json:"id"`
	FactID      string    `json:"fact_id"`
	SourceType  string    `json:"source_type"`
	SourceID    string    `json:"source_id"`
	Summary     string    `json:"summary,omitempty"`
	EvidenceDay time.Time `json:"evidence_day"`
	ContentHash string    `json:"content_hash,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// StewardProfileSnapshot is a materialized projection. Merged snapshots
// resolve one fact per key with explicit > stable > recent precedence.
type StewardProfileSnapshot struct {
	ID          string               `json:"id"`
	Horizon     string               `json:"horizon"`
	Revision    int64                `json:"revision"`
	WindowStart *time.Time           `json:"window_start,omitempty"`
	WindowEnd   time.Time            `json:"window_end"`
	Facts       []StewardProfileFact `json:"facts"`
	Profile     map[string]any       `json:"profile"`
	CreatedBy   string               `json:"created_by"`
	JobID       *string              `json:"job_id,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}

type StewardReport struct {
	ID               string                  `json:"id"`
	Cadence          string                  `json:"cadence"`
	PeriodKey        string                  `json:"period_key"`
	PeriodStart      time.Time               `json:"period_start"`
	PeriodEnd        time.Time               `json:"period_end"`
	Revision         int                     `json:"revision"`
	Status           string                  `json:"status"`
	Title            string                  `json:"title"`
	Summary          string                  `json:"summary"`
	Body             string                  `json:"body"`
	Metrics          map[string]any          `json:"metrics"`
	Silent           bool                    `json:"silent"`
	EvidenceCount    int                     `json:"evidence_count"`
	EvidenceCoverage float64                 `json:"evidence_coverage"`
	MissingEvidence  []string                `json:"missing_evidence,omitempty"`
	SupersedesID     *string                 `json:"supersedes_id,omitempty"`
	EpisodeID        *string                 `json:"episode_id,omitempty"`
	NotificationID   *string                 `json:"notification_id,omitempty"`
	JobID            *string                 `json:"job_id,omitempty"`
	Provider         string                  `json:"provider,omitempty"`
	Model            string                  `json:"model,omitempty"`
	ErrorSummary     string                  `json:"error_summary,omitempty"`
	Evidence         []StewardReportEvidence `json:"evidence,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	CompletedAt      *time.Time              `json:"completed_at,omitempty"`
}

type StewardReportEvidence struct {
	ID          string    `json:"id"`
	ReportID    string    `json:"report_id"`
	SourceType  string    `json:"source_type"`
	SourceID    string    `json:"source_id"`
	Summary     string    `json:"summary,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// StewardIntelligenceJob drives profile consolidation and report generation.
// LeaseOwner + LeaseExpiresAt + ControlGeneration fence stale workers and
// callbacks after restart, pause, retry, or cancellation.
type StewardIntelligenceJob struct {
	ID                string         `json:"id"`
	Kind              string         `json:"kind"`
	PeriodKey         string         `json:"period_key"`
	PeriodStart       time.Time      `json:"period_start"`
	PeriodEnd         time.Time      `json:"period_end"`
	Status            string         `json:"status"`
	Input             map[string]any `json:"input"`
	Checkpoint        map[string]any `json:"checkpoint"`
	Attempts          int            `json:"attempts"`
	MaxAttempts       int            `json:"max_attempts"`
	DueAt             time.Time      `json:"due_at"`
	NextAttemptAt     time.Time      `json:"next_attempt_at"`
	LeaseOwner        string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt    *time.Time     `json:"lease_expires_at,omitempty"`
	ControlGeneration int64          `json:"control_generation"`
	EpisodeID         *string        `json:"episode_id,omitempty"`
	ReportID          *string        `json:"report_id,omitempty"`
	FailureSummary    string         `json:"failure_summary,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	CompletedAt       *time.Time     `json:"completed_at,omitempty"`
}

type StewardProfileView struct {
	Recent   *StewardProfileSnapshot `json:"recent,omitempty"`
	Stable   *StewardProfileSnapshot `json:"stable,omitempty"`
	Explicit *StewardProfileSnapshot `json:"explicit,omitempty"`
	Merged   *StewardProfileSnapshot `json:"merged,omitempty"`
}

// StewardHealthIssue keeps the existing human-readable issue projection while
// also giving API consumers a stable code and an actionable recovery hint.
type StewardHealthIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action"`
}

// StewardRatioMetric carries its source counts so the displayed ratio remains
// auditable. A zero denominator is unavailable rather than being reported as a
// fabricated zero ratio.
type StewardRatioMetric struct {
	Available   bool    `json:"available"`
	Value       float64 `json:"value,omitempty"`
	Numerator   int     `json:"numerator"`
	Denominator int     `json:"denominator"`
	Reason      string  `json:"reason,omitempty"`
}

type StewardAgentEpisodeOutcomeMetrics struct {
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

type StewardReportCoverageMetrics struct {
	Available   bool    `json:"available"`
	ReportCount int     `json:"report_count"`
	Average     float64 `json:"average,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

type StewardReminderFeedbackMetrics struct {
	Total    int            `json:"total"`
	ByAction map[string]int `json:"by_action"`
}

// StewardModelUsageMetrics is deliberately explicit about unavailable
// provider accounting. The current schema persists provider response IDs but
// not token or price usage, so callers must not infer or estimate those values.
type StewardModelUsageMetrics struct {
	Available    bool     `json:"available"`
	InputTokens  *int64   `json:"input_tokens,omitempty"`
	OutputTokens *int64   `json:"output_tokens,omitempty"`
	TotalTokens  *int64   `json:"total_tokens,omitempty"`
	Cost         *float64 `json:"cost,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	Reason       string   `json:"reason"`
}

type StewardBackgroundMetrics struct {
	WindowStart             time.Time                         `json:"window_start"`
	WindowEnd               time.Time                         `json:"window_end"`
	Observations1H          int                               `json:"observations_1h"`
	Sessions1H              int                               `json:"sessions_1h"`
	SessionCompressionRatio StewardRatioMetric                `json:"session_compression_ratio"`
	BatchStatusCounts       map[string]int                    `json:"batch_status_counts"`
	ModelEpisodes1H         StewardAgentEpisodeOutcomeMetrics `json:"model_episodes_1h"`
	ReportCoverage          StewardReportCoverageMetrics      `json:"report_coverage"`
	ReminderFeedback1H      StewardReminderFeedbackMetrics    `json:"reminder_feedback_1h"`
	ModelUsage              StewardModelUsageMetrics          `json:"model_usage"`
}
