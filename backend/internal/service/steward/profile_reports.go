package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	intelligenceJobProfileConsolidation    = "profile_consolidation"
	intelligenceJobProfileCorrectionReview = "profile_correction_review"
	intelligenceJobReportDaily             = "report_daily"
	intelligenceJobReportWeekly            = "report_weekly"
	intelligenceJobReportMonthly           = "report_monthly"

	intelligenceJobPending      = "pending"
	intelligenceJobProcessing   = "processing"
	intelligenceJobExecuting    = "executing"
	intelligenceJobWaitingModel = "waiting_model"
	intelligenceJobCompleted    = "completed"
	intelligenceJobPartial      = "partial"
	intelligenceJobFailed       = "failed"
	intelligenceJobCancelled    = "cancelled"

	reportStatusPartial    = "partial"
	reportStatusComplete   = "complete"
	reportStatusSuperseded = "superseded"

	profileStableEvidenceDays           = 3
	profileConflictConfidenceMultiplier = 0.5
	reportEvidenceCoverageThreshold     = 0.95
	reportQualityMaxAttempts            = 3
)

var ErrProfileEvidenceInsufficient = errors.New("stable profile fact requires evidence from at least three distinct days or explicit user confirmation")

var (
	ErrProfileEvidenceRequired      = errors.New("profile fact requires verified evidence unless it is an explicit user-authored correction")
	ErrEvidenceSourceNotFound       = errors.New("evidence source does not exist")
	ErrEvidenceDayMismatch          = errors.New("evidence day does not match the persisted source")
	ErrReportJobContextMismatch     = errors.New("report does not match its background intelligence job")
	ErrProfileCorrectionPropagation = errors.New("profile correction was persisted but follow-up propagation failed")
)

type ProfileEvidenceInput struct {
	SourceType  string    `json:"source_type"`
	SourceID    string    `json:"source_id"`
	Summary     string    `json:"summary"`
	EvidenceDay time.Time `json:"evidence_day"`
	ContentHash string    `json:"content_hash"`
}

type UpsertProfileFactInput struct {
	Key           string                 `json:"key"`
	Value         map[string]any         `json:"value"`
	Summary       string                 `json:"summary"`
	Horizon       string                 `json:"horizon"`
	Confidence    float64                `json:"confidence"`
	UserConfirmed bool                   `json:"user_confirmed"`
	Evidence      []ProfileEvidenceInput `json:"evidence"`
	CreatedBy     string                 `json:"created_by"`
	JobID         string                 `json:"job_id"`
	Provider      string                 `json:"provider"`
	Model         string                 `json:"model"`
	ValidFrom     *time.Time             `json:"valid_from"`
}

type ListProfileFactsInput struct {
	Horizon string
	Status  string
	Key     string
	Limit   int
}

type WriteReportInput struct {
	Cadence     string                 `json:"cadence"`
	PeriodKey   string                 `json:"period_key"`
	PeriodStart time.Time              `json:"period_start"`
	PeriodEnd   time.Time              `json:"period_end"`
	Status      string                 `json:"status"`
	Title       string                 `json:"title"`
	Summary     string                 `json:"summary"`
	Body        string                 `json:"body"`
	Metrics     map[string]any         `json:"metrics"`
	Silent      bool                   `json:"silent"`
	Evidence    []ProfileEvidenceInput `json:"evidence"`
	EpisodeID   string                 `json:"episode_id"`
	JobID       string                 `json:"job_id"`
	Provider    string                 `json:"provider"`
	Model       string                 `json:"model"`
	Error       string                 `json:"error"`
}

type EnqueueIntelligenceJobInput struct {
	Kind        string         `json:"kind"`
	PeriodKey   string         `json:"period_key"`
	PeriodStart time.Time      `json:"period_start"`
	PeriodEnd   time.Time      `json:"period_end"`
	Input       map[string]any `json:"input"`
	MaxAttempts int            `json:"max_attempts"`
	DueAt       time.Time      `json:"due_at"`
}

type RegenerateReportInput struct {
	Reason string `json:"reason"`
}

type ReportRegenerationResult struct {
	SourceReportID string                        `json:"source_report_id"`
	SourceRevision int                           `json:"source_revision"`
	Job            domain.StewardIntelligenceJob `json:"job"`
	Created        bool                          `json:"created"`
}

type ProfileReportControllerResult struct {
	Scheduled  int `json:"scheduled"`
	Reconciled int `json:"reconciled"`
	Started    int `json:"started"`
	Deferred   int `json:"deferred"`
	Partial    int `json:"partial"`
}

func normalizeProfileKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), "_")
	return strings.Trim(value, "._-")
}

func validProfileHorizon(value string) bool {
	switch value {
	case domain.StewardProfileHorizonRecent, domain.StewardProfileHorizonStable, domain.StewardProfileHorizonExplicit:
		return true
	default:
		return false
	}
}

func normalizeProfileEvidence(values []ProfileEvidenceInput, now time.Time) []ProfileEvidenceInput {
	seen := map[string]bool{}
	result := make([]ProfileEvidenceInput, 0, len(values))
	for _, item := range values {
		item.SourceType = canonicalEvidenceSourceType(item.SourceType)
		item.SourceID = strings.TrimSpace(item.SourceID)
		item.Summary = truncateAdvisorText(strings.TrimSpace(item.Summary), 1000)
		item.ContentHash = strings.ToLower(strings.TrimSpace(item.ContentHash))
		if item.SourceType == "" || item.SourceID == "" {
			continue
		}
		if item.EvidenceDay.IsZero() {
			item.EvidenceDay = now
		}
		// evidence_day is a user-local calendar day, not an instant. Preserve the
		// source calendar components when normalizing it for PostgreSQL DATE.
		item.EvidenceDay = time.Date(item.EvidenceDay.Year(), item.EvidenceDay.Month(), item.EvidenceDay.Day(), 0, 0, 0, 0, time.UTC)
		key := item.SourceType + "\x00" + item.SourceID
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EvidenceDay.Equal(result[j].EvidenceDay) {
			return result[i].SourceType+result[i].SourceID < result[j].SourceType+result[j].SourceID
		}
		return result[i].EvidenceDay.Before(result[j].EvidenceDay)
	})
	return result
}

func distinctEvidenceDays(values []ProfileEvidenceInput) int {
	days := map[string]bool{}
	for _, item := range values {
		if !item.EvidenceDay.IsZero() {
			days[item.EvidenceDay.Format("2006-01-02")] = true
		}
	}
	return len(days)
}

type persistedEvidenceSource struct {
	SourceType   string
	StartedAt    time.Time
	EndedAt      time.Time
	UserAuthored bool
}

func canonicalEvidenceSourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "session", "activity_session":
		return "activity_session"
	case "observation", "activity_observation":
		return "observation"
	case "batch", "activity_batch":
		return "activity_batch"
	case "profile", "profile_fact":
		return "profile_fact"
	case "report":
		return "report"
	case "message", "conversation_message", "user_message":
		return "conversation_message"
	case "memory":
		return "memory"
	case "episode", "agent_episode":
		return "agent_episode"
	case "job", "intelligence_job":
		return "intelligence_job"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (s *Service) evidenceTimezone(ctx context.Context) *time.Location {
	if settings, err := s.GetIntelligenceSettings(ctx); err == nil {
		timezone := strings.TrimSpace(settings.Timezone)
		if timezone == "" {
			return time.Local
		}
		if location, loadErr := time.LoadLocation(timezone); loadErr == nil {
			return location
		}
	}
	return time.UTC
}

func calendarDayAt(value time.Time, location *time.Location) time.Time {
	value = value.In(location)
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, location)
}

func evidenceDayWithinSource(day, startedAt, endedAt time.Time, location *time.Location) bool {
	if endedAt.IsZero() || endedAt.Before(startedAt) {
		endedAt = startedAt
	}
	// A half-open source range ending exactly at midnight belongs to the
	// preceding calendar day. This avoids accepting a fabricated next day for
	// a session or report that ended at 00:00.
	if endedAt.After(startedAt) && endedAt.In(location).Hour() == 0 && endedAt.In(location).Minute() == 0 && endedAt.In(location).Second() == 0 && endedAt.In(location).Nanosecond() == 0 {
		endedAt = endedAt.Add(-time.Nanosecond)
	}
	claimed := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location)
	return !claimed.Before(calendarDayAt(startedAt, location)) && !claimed.After(calendarDayAt(endedAt, location))
}

func evidenceSourceOverlapsPeriod(source persistedEvidenceSource, periodStart, periodEnd time.Time) bool {
	if source.EndedAt.Equal(source.StartedAt) {
		return !source.StartedAt.Before(periodStart) && source.StartedAt.Before(periodEnd)
	}
	return source.EndedAt.After(periodStart) && source.StartedAt.Before(periodEnd)
}

func (s *Service) loadPersistedEvidenceSource(ctx context.Context, sourceType, sourceID string) (persistedEvidenceSource, error) {
	sourceType = canonicalEvidenceSourceType(sourceType)
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return persistedEvidenceSource{}, fmt.Errorf("%w: empty source id", ErrEvidenceSourceNotFound)
	}
	var source persistedEvidenceSource
	source.SourceType = sourceType
	var err error
	switch sourceType {
	case "observation":
		err = s.db.Pool.QueryRow(ctx, `select occurred_at,coalesce(ended_at,occurred_at),false
			from steward_observations where id::text=$1 order by occurred_at limit 1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "activity_session":
		err = s.db.Pool.QueryRow(ctx, `select started_at,ended_at,false from steward_activity_sessions where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "activity_batch":
		err = s.db.Pool.QueryRow(ctx, `select window_start,window_end,false from steward_activity_batches where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "profile_fact":
		err = s.db.Pool.QueryRow(ctx, `select coalesce(valid_from,created_at),coalesce(valid_from,created_at),
			(layer='explicit' or updated_by='user') from steward_profile_facts where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "report":
		err = s.db.Pool.QueryRow(ctx, `select period_start,period_end,false from steward_reports where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "conversation_message":
		err = s.db.Pool.QueryRow(ctx, `select created_at,created_at,(role='user') from steward_conversation_messages where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "memory":
		err = s.db.Pool.QueryRow(ctx, `select updated_at,updated_at,user_confirmed from steward_memories where id::text=$1 and deleted_at is null`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "agent_episode":
		err = s.db.Pool.QueryRow(ctx, `select created_at,coalesce(completed_at,updated_at),false from steward_agent_episodes where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	case "intelligence_job":
		err = s.db.Pool.QueryRow(ctx, `select window_start,window_end,false from steward_memory_consolidation_runs where id::text=$1`, sourceID).
			Scan(&source.StartedAt, &source.EndedAt, &source.UserAuthored)
	default:
		return persistedEvidenceSource{}, fmt.Errorf("%w: unsupported source_type %q", ErrEvidenceSourceNotFound, sourceType)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return persistedEvidenceSource{}, fmt.Errorf("%w: %s %s", ErrEvidenceSourceNotFound, sourceType, sourceID)
	}
	if err != nil {
		return persistedEvidenceSource{}, err
	}
	return source, nil
}

func (s *Service) validatePersistedEvidence(ctx context.Context, evidence []ProfileEvidenceInput, periodStart, periodEnd *time.Time) ([]ProfileEvidenceInput, bool, error) {
	location := s.evidenceTimezone(ctx)
	verified := make([]ProfileEvidenceInput, 0, len(evidence))
	hasUserAuthored := false
	for index, item := range evidence {
		source, err := s.loadPersistedEvidenceSource(ctx, item.SourceType, item.SourceID)
		if err != nil {
			return nil, false, fmt.Errorf("evidence[%d]: %w", index, err)
		}
		if !evidenceDayWithinSource(item.EvidenceDay, source.StartedAt, source.EndedAt, location) {
			return nil, false, fmt.Errorf("evidence[%d]: %w: claimed %s, source spans %s to %s", index,
				ErrEvidenceDayMismatch, item.EvidenceDay.Format("2006-01-02"), source.StartedAt.In(location).Format(time.RFC3339), source.EndedAt.In(location).Format(time.RFC3339))
		}
		if periodStart != nil && periodEnd != nil {
			if !evidenceDayWithinSource(item.EvidenceDay, *periodStart, *periodEnd, location) {
				return nil, false, fmt.Errorf("evidence[%d]: %w: claimed day is outside report period", index, ErrEvidenceDayMismatch)
			}
			if !evidenceSourceOverlapsPeriod(source, *periodStart, *periodEnd) {
				return nil, false, fmt.Errorf("evidence[%d]: %w: source does not overlap report period", index, ErrEvidenceDayMismatch)
			}
		}
		item.SourceType = source.SourceType
		verified = append(verified, item)
		hasUserAuthored = hasUserAuthored || source.UserAuthored
	}
	return verified, hasUserAuthored, nil
}

// UpsertProfileFact creates an immutable fact version. Matching inferences
// advance their own evidence branch, while contradictory inferred values stay
// active together for audit. Explicit user corrections supersede every active
// explicit value for the key; they do not erase recent/stable history because
// merged projection precedence resolves those layers.
func (s *Service) UpsertProfileFact(ctx context.Context, input UpsertProfileFactInput) (domain.StewardProfileFact, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return domain.StewardProfileFact{}, fmt.Errorf("profile database is not configured")
	}
	now := time.Now().UTC()
	input.Key = normalizeProfileKey(input.Key)
	input.Horizon = strings.ToLower(strings.TrimSpace(input.Horizon))
	if input.Key == "" || len(input.Key) > 200 {
		return domain.StewardProfileFact{}, fmt.Errorf("profile key is required and must not exceed 200 characters")
	}
	if !validProfileHorizon(input.Horizon) {
		return domain.StewardProfileFact{}, fmt.Errorf("profile horizon must be recent, stable, or explicit")
	}
	if len(input.Value) == 0 {
		return domain.StewardProfileFact{}, fmt.Errorf("profile value is required")
	}
	input.Evidence = normalizeProfileEvidence(input.Evidence, now)
	explicitUserCorrection := input.Horizon == domain.StewardProfileHorizonExplicit && input.UserConfirmed && strings.EqualFold(strings.TrimSpace(input.CreatedBy), "user")
	if len(input.Evidence) == 0 && !explicitUserCorrection {
		return domain.StewardProfileFact{}, ErrProfileEvidenceRequired
	}
	var hasUserAuthoredEvidence bool
	if len(input.Evidence) > 0 {
		verifiedEvidence, userAuthoredEvidence, verifyErr := s.validatePersistedEvidence(ctx, input.Evidence, nil, nil)
		if verifyErr != nil {
			return domain.StewardProfileFact{}, verifyErr
		}
		input.Evidence, hasUserAuthoredEvidence = verifiedEvidence, userAuthoredEvidence
	}
	evidenceDays := distinctEvidenceDays(input.Evidence)
	stableEvidenceDays := profileStableEvidenceDays
	if settings, settingsErr := s.GetIntelligenceSettings(ctx); settingsErr == nil && settings.StableMinEvidenceDays > 0 {
		stableEvidenceDays = settings.StableMinEvidenceDays
	}
	confirmedByUser := explicitUserCorrection || (input.UserConfirmed && hasUserAuthoredEvidence)
	if input.Horizon == domain.StewardProfileHorizonStable && !confirmedByUser && evidenceDays < stableEvidenceDays {
		return domain.StewardProfileFact{}, fmt.Errorf("%w: got %d distinct days, need %d", ErrProfileEvidenceInsufficient, evidenceDays, stableEvidenceDays)
	}
	if input.Horizon == domain.StewardProfileHorizonExplicit && (!input.UserConfirmed || (!explicitUserCorrection && !hasUserAuthoredEvidence)) {
		return domain.StewardProfileFact{}, fmt.Errorf("explicit profile facts must be user confirmed by a persisted user-authored source")
	}
	if input.Confidence <= 0 {
		input.Confidence = 0.7
	}
	if input.Confidence > 1 {
		input.Confidence = 1
	}
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "model")
	validFrom := now
	if input.ValidFrom != nil && !input.ValidFrom.IsZero() {
		validFrom = input.ValidFrom.UTC()
	}
	valueJSON, err := json.Marshal(input.Value)
	if err != nil {
		return domain.StewardProfileFact{}, fmt.Errorf("encode profile value: %w", err)
	}

	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.StewardProfileFact{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := "profile:" + input.Horizon + ":" + input.Key
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return domain.StewardProfileFact{}, err
	}
	var latestActiveID string
	var latestSameValueID string
	var previousVersion int
	err = tx.QueryRow(ctx, `select
		coalesce((select id::text from steward_profile_facts where profile_scope='default' and key=$1 and layer=$2
			and status='active' order by updated_at desc,id desc limit 1),''),
		coalesce((select id::text from steward_profile_facts where profile_scope='default' and key=$1 and layer=$2
			and status='active' and value=$3::jsonb order by updated_at desc,id desc limit 1),''),
		(select count(*)::int from steward_profile_facts where profile_scope='default' and key=$1 and layer=$2)`,
		input.Key, input.Horizon, string(valueJSON)).Scan(&latestActiveID, &latestSameValueID, &previousVersion)
	if err != nil {
		return domain.StewardProfileFact{}, err
	}
	version := previousVersion + 1
	if version <= 0 {
		version = 1
	}
	fact := domain.StewardProfileFact{
		ID: uuid.NewString(), Key: input.Key, Value: input.Value, Summary: truncateAdvisorText(strings.TrimSpace(input.Summary), 4000),
		Horizon: input.Horizon, Status: domain.StewardProfileFactActive, Version: version, Confidence: input.Confidence, EffectiveConfidence: input.Confidence,
		EvidenceCount: len(input.Evidence), EvidenceDays: evidenceDays, UserConfirmed: input.UserConfirmed,
		ConflictGroup: uuid.NewSHA1(uuid.NameSpaceOID, []byte("profile:"+input.Key)).String(), CreatedBy: input.CreatedBy, Provider: strings.TrimSpace(input.Provider), Model: strings.TrimSpace(input.Model),
		ValidFrom: validFrom, CreatedAt: now, UpdatedAt: now,
	}
	previousID := latestSameValueID
	if explicitUserCorrection {
		previousID = latestActiveID
	}
	if previousID != "" {
		fact.SupersedesFactID = &previousID
	}
	if strings.TrimSpace(input.JobID) != "" {
		fact.JobID = stringPtr(strings.TrimSpace(input.JobID))
	}
	idempotencyPayload, _ := json.Marshal(map[string]any{"value": input.Value, "summary": fact.Summary,
		"evidence": input.Evidence, "user_confirmed": input.UserConfirmed})
	idempotencyKey := fmt.Sprintf("profile:%s:%s:%s:%x", fact.Horizon, fact.Key, strings.TrimSpace(input.JobID), sha256.Sum256(idempotencyPayload))
	var existingID string
	if err = tx.QueryRow(ctx, `select id::text from steward_profile_facts where idempotency_key=$1`, idempotencyKey).Scan(&existingID); err == nil {
		if err = tx.Commit(ctx); err != nil {
			return domain.StewardProfileFact{}, err
		}
		existing, loadErr := s.getProfileFact(ctx, existingID)
		if loadErr != nil {
			return existing, loadErr
		}
		if rebuildErr := s.rebuildProfileSnapshotsAfterFact(ctx, input.JobID); rebuildErr != nil {
			return existing, rebuildErr
		}
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardProfileFact{}, err
	}
	if _, err = tx.Exec(ctx, `insert into steward_profile_facts (
		id,profile_scope,key,layer,value,status,confidence,evidence_count,evidence_days,valid_from,
		updated_by,provider,model,change_reason,supersedes_id,conflict_group_id,source_run_id,idempotency_key,created_at,updated_at
	) values ($1,'default',$2,$3,$4::jsonb,'active',$5,$6,$7,$8,$9,$10,$11,$12,nullif($13,'')::uuid,
		nullif($14,'')::uuid,nullif($15,'')::uuid,$16,$17,$17)`,
		fact.ID, fact.Key, fact.Horizon, string(valueJSON), fact.Confidence, fact.EvidenceCount, fact.EvidenceDays,
		fact.ValidFrom, fact.CreatedBy, fact.Provider, fact.Model, fact.Summary, previousID, fact.ConflictGroup,
		input.JobID, idempotencyKey, now); err != nil {
		return domain.StewardProfileFact{}, err
	}
	if explicitUserCorrection {
		if _, err = tx.Exec(ctx, `update steward_profile_facts set status='superseded',valid_to=$4,updated_at=$4
			where profile_scope='default' and key=$1 and layer=$2 and status='active' and id<>$3`,
			input.Key, input.Horizon, fact.ID, now); err != nil {
			return domain.StewardProfileFact{}, err
		}
	} else if latestSameValueID != "" {
		if _, err = tx.Exec(ctx, `update steward_profile_facts set status='superseded',valid_to=$5,updated_at=$5
			where profile_scope='default' and key=$1 and layer=$2 and status='active' and value=$3::jsonb and id<>$4`,
			input.Key, input.Horizon, string(valueJSON), fact.ID, now); err != nil {
			return domain.StewardProfileFact{}, err
		}
	}
	for _, item := range input.Evidence {
		evidence := domain.StewardProfileEvidence{
			ID: uuid.NewString(), FactID: fact.ID, SourceType: item.SourceType, SourceID: item.SourceID,
			Summary: item.Summary, EvidenceDay: item.EvidenceDay, ContentHash: item.ContentHash, CreatedAt: now,
		}
		evidenceKey := "profile-evidence:" + fact.ID + ":" + evidence.SourceType + ":" + evidence.SourceID
		if _, err = tx.Exec(ctx, `insert into steward_source_refs
			(id,target_type,target_id,source_type,source_id,location,summary,confidence,sensitive,displayable,
			 idempotency_key,source_occurred_at,tombstone,created_at)
			values ($1,'profile_fact',$2,$3,$4,$5,$6,$7,false,true,$8,$9,'{}'::jsonb,$10)
			on conflict (idempotency_key) where idempotency_key<>'' do nothing`, evidence.ID, evidence.FactID,
			evidence.SourceType, evidence.SourceID, evidence.ContentHash, evidence.Summary, fact.Confidence,
			evidenceKey, evidence.EvidenceDay, evidence.CreatedAt); err != nil {
			return domain.StewardProfileFact{}, err
		}
		fact.Evidence = append(fact.Evidence, evidence)
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StewardProfileFact{}, err
	}
	if err = s.rebuildProfileSnapshotsAfterFact(ctx, input.JobID); err != nil {
		return fact, err
	}
	return fact, nil
}

func (s *Service) rebuildProfileSnapshotsAfterFact(ctx context.Context, jobID string) error {
	recentDays := 14
	if settings, err := s.GetIntelligenceSettings(ctx); err == nil && settings.RecentProfileDays > 0 {
		recentDays = settings.RecentProfileDays
	}
	if _, err := s.RebuildProfileSnapshots(ctx, time.Now().UTC(), recentDays, strings.TrimSpace(jobID)); err != nil {
		return fmt.Errorf("persist profile fact but rebuild live snapshots: %w", err)
	}
	return nil
}

func (s *Service) CorrectProfileFact(ctx context.Context, input UpsertProfileFactInput) (domain.StewardProfileFact, error) {
	input.Horizon = domain.StewardProfileHorizonExplicit
	input.UserConfirmed = true
	input.CreatedBy = "user"
	input.Confidence = 1
	fact, err := s.UpsertProfileFact(ctx, input)
	if err != nil {
		return fact, err
	}
	if err := s.propagateProfileCorrection(ctx, fact); err != nil {
		return fact, fmt.Errorf("%w: %v", ErrProfileCorrectionPropagation, err)
	}
	// Upsert already rebuilds live snapshots, but propagation adds the durable
	// user-correction source reference afterward. Rebuild once more so the
	// projected fact and its correction evidence become visible together.
	if err := s.rebuildProfileSnapshotsAfterFact(ctx, input.JobID); err != nil {
		return fact, fmt.Errorf("%w: %v", ErrProfileCorrectionPropagation, err)
	}
	// Reload so the API response includes the durable user-correction evidence
	// created by propagation, including on an idempotent retry of the request.
	return s.getProfileFact(ctx, fact.ID)
}

func profileCorrectionAffectedWindow(fact domain.StewardProfileFact) (time.Time, time.Time) {
	start, end := fact.ValidFrom.UTC(), fact.ValidFrom.UTC()
	if start.IsZero() {
		start, end = fact.CreatedAt.UTC(), fact.CreatedAt.UTC()
	}
	if len(fact.Evidence) == 0 {
		// An evidence-free correction submitted through the dedicated user API
		// normally refers to the current activity/report. Include the preceding
		// day so a report generated just before the correction is still reviewed.
		start = start.Add(-24 * time.Hour)
	}
	for _, evidence := range fact.Evidence {
		occurredAt := evidence.EvidenceDay.UTC()
		if occurredAt.IsZero() {
			continue
		}
		if occurredAt.Before(start) {
			start = occurredAt
		}
		if occurredAt.After(end) {
			end = occurredAt
		}
	}
	end = end.Add(time.Nanosecond)
	if !end.After(start) {
		end = start.Add(time.Nanosecond)
	}
	return start, end
}

// propagateProfileCorrection atomically records the correction as auditable
// evidence, schedules model review of reminder policy, and queues regeneration
// for every currently active report whose period overlaps the affected window.
// The review job is the propagation marker: if it already exists, every report
// job from the same transaction also exists, so retries cannot start a chain of
// regenerations from revisions produced by the original correction.
func (s *Service) propagateProfileCorrection(ctx context.Context, fact domain.StewardProfileFact) error {
	if strings.TrimSpace(fact.ID) == "" {
		return fmt.Errorf("profile correction fact id is required")
	}
	start, end := profileCorrectionAffectedWindow(fact)
	now := time.Now().UTC()
	reviewKey := "intelligence:profile-correction-review:" + fact.ID
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, "profile-correction-propagation:"+fact.ID); err != nil {
		return err
	}
	summary := defaultString(strings.TrimSpace(fact.Summary), "用户明确纠正画像字段 "+fact.Key)
	tombstoneJSON, _ := json.Marshal(map[string]any{"kind": "explicit_user_correction", "profile_key": fact.Key})
	if _, err = tx.Exec(ctx, `insert into steward_source_refs
		(id,target_type,target_id,source_type,source_id,location,summary,confidence,sensitive,displayable,
		 idempotency_key,source_occurred_at,tombstone,created_at)
		values($1,'profile_fact',$2::uuid,'user_correction',$2::text,'profile_corrections_api',$3,1,false,true,$4,$5,$6::jsonb,$7)
		on conflict (idempotency_key) where idempotency_key<>'' do nothing`, uuid.NewString(), fact.ID, summary,
		"profile-correction-evidence:"+fact.ID, fact.ValidFrom.UTC(), string(tombstoneJSON), now); err != nil {
		return fmt.Errorf("persist correction evidence: %w", err)
	}
	var propagationExists bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from steward_memory_consolidation_runs where idempotency_key=$1)`, reviewKey).Scan(&propagationExists); err != nil {
		return err
	}
	if propagationExists {
		return tx.Commit(ctx)
	}

	type affectedReport struct {
		ID, Cadence, PeriodKey string
		Revision               int
		PeriodStart, PeriodEnd time.Time
	}
	rows, err := tx.Query(ctx, `select id::text,cadence,period_key,revision,period_start,period_end
		from steward_reports where profile_scope='default' and status in ('complete','partial')
		and period_start<$2 and period_end>$1 order by period_start,cadence,revision`, start, end)
	if err != nil {
		return fmt.Errorf("find reports affected by correction: %w", err)
	}
	reports := []affectedReport{}
	for rows.Next() {
		var report affectedReport
		if err := rows.Scan(&report.ID, &report.Cadence, &report.PeriodKey, &report.Revision, &report.PeriodStart, &report.PeriodEnd); err != nil {
			rows.Close()
			return err
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	reviewInput := EnqueueIntelligenceJobInput{
		Kind: intelligenceJobProfileCorrectionReview, PeriodKey: "correction:" + fact.ID,
		PeriodStart: start, PeriodEnd: end, DueAt: now, MaxAttempts: reportQualityMaxAttempts,
		Input: map[string]any{
			"correction_fact_id": fact.ID, "profile_key": fact.Key,
			"correction_evidence": "profile_fact:" + fact.ID,
			"requested_via":       "user_profile_correction",
		},
	}
	if err := insertIntelligenceJobTx(ctx, tx, reviewInput, reviewKey, now); err != nil {
		return fmt.Errorf("enqueue correction policy review: %w", err)
	}
	for _, report := range reports {
		jobInput := map[string]any{
			"regenerate_report_id": report.ID,
			"source_revision":      report.Revision,
			"reason":               "用户画像纠正要求重新核对报告结论和措辞",
			"correction_fact_id":   fact.ID,
			"correction_evidence":  "profile_fact:" + fact.ID,
			"requested_at":         now.Format(time.RFC3339Nano),
			"requested_via":        "user_profile_correction",
		}
		job := EnqueueIntelligenceJobInput{
			Kind: "report_" + report.Cadence, PeriodKey: report.PeriodKey,
			PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd,
			DueAt: now, MaxAttempts: reportQualityMaxAttempts, Input: jobInput,
		}
		key := "intelligence:profile-correction:" + fact.ID + ":report:" + report.ID
		if err := insertIntelligenceJobTx(ctx, tx, job, key, now); err != nil {
			return fmt.Errorf("enqueue correction report regeneration: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func scanProfileFact(row rowScanner) (domain.StewardProfileFact, error) {
	var item domain.StewardProfileFact
	var valueJSON []byte
	err := row.Scan(&item.ID, &item.Key, &valueJSON, &item.Summary, &item.Horizon, &item.Status, &item.Version,
		&item.Confidence, &item.EvidenceCount, &item.EvidenceDays, &item.UserConfirmed, &item.ConflictGroup,
		&item.SupersedesFactID, &item.CreatedBy, &item.JobID, &item.Provider, &item.Model, &item.ValidFrom,
		&item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal(valueJSON, &item.Value)
		if item.Value == nil {
			item.Value = map[string]any{}
		}
		item.EffectiveConfidence = item.Confidence
	}
	return item, err
}

const profileFactSelect = `with facts_versioned as (
	select facts.*,row_number() over (partition by profile_scope,key,layer order by created_at,id)::int as fact_version
	from steward_profile_facts facts where profile_scope='default'
) select id::text,key,value,change_reason,layer,status,fact_version,confidence,evidence_count,evidence_days,
	(layer='explicit' or updated_by='user'),coalesce(conflict_group_id::text,''),supersedes_id::text,updated_by,
	source_run_id::text,provider,model,coalesce(valid_from,created_at),valid_to,created_at,updated_at from facts_versioned`

func (s *Service) getProfileFact(ctx context.Context, id string) (domain.StewardProfileFact, error) {
	item, err := scanProfileFact(s.db.Pool.QueryRow(ctx, profileFactSelect+` where id=$1`, id))
	if err != nil {
		return item, err
	}
	item.Evidence, err = s.listProfileFactEvidence(ctx, item.ID)
	return item, err
}

func (s *Service) ListProfileFacts(ctx context.Context, input ListProfileFactsInput) ([]domain.StewardProfileFact, error) {
	limit := normalizeLimit(input.Limit, 100, 1000)
	rows, err := s.db.Pool.Query(ctx, profileFactSelect+`
		where ($1='' or layer=$1) and ($2='' or status=$2) and ($3='' or key=$3)
		order by key,layer,fact_version desc limit $4`, strings.TrimSpace(input.Horizon), strings.TrimSpace(input.Status), normalizeProfileKey(input.Key), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardProfileFact{}
	for rows.Next() {
		item, err := scanProfileFact(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		evidence, err := s.listProfileFactEvidence(ctx, items[index].ID)
		if err != nil {
			return nil, err
		}
		items[index].Evidence = evidence
	}
	return items, nil
}

func (s *Service) listProfileFactEvidence(ctx context.Context, factID string) ([]domain.StewardProfileEvidence, error) {
	rows, err := s.db.Pool.Query(ctx, `select id::text,target_id::text,source_type,source_id,summary,
		coalesce(source_occurred_at,created_at),location,created_at from steward_source_refs
		where target_type='profile_fact' and target_id=$1 order by source_occurred_at,created_at`, factID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardProfileEvidence{}
	for rows.Next() {
		var item domain.StewardProfileEvidence
		if err := rows.Scan(&item.ID, &item.FactID, &item.SourceType, &item.SourceID, &item.Summary,
			&item.EvidenceDay, &item.ContentHash, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// BuildProfileSnapshots is intentionally pure so projection precedence and
// confidence/decay behavior remain independently testable from PostgreSQL.
func BuildProfileSnapshots(facts []domain.StewardProfileFact, now time.Time, recentDays int) map[string]domain.StewardProfileSnapshot {
	if recentDays <= 0 {
		recentDays = 14
	}
	now = now.UTC()
	candidates := map[string]map[string][]domain.StewardProfileFact{
		domain.StewardProfileHorizonRecent: {}, domain.StewardProfileHorizonStable: {}, domain.StewardProfileHorizonExplicit: {},
	}
	for _, fact := range facts {
		if fact.Status != domain.StewardProfileFactActive || !validProfileHorizon(fact.Horizon) || fact.ValidFrom.After(now) || (fact.ValidTo != nil && !fact.ValidTo.After(now)) {
			continue
		}
		if fact.Horizon == domain.StewardProfileHorizonRecent && !fact.ValidFrom.After(now.Add(-time.Duration(recentDays)*24*time.Hour)) {
			continue
		}
		candidates[fact.Horizon][fact.Key] = append(candidates[fact.Horizon][fact.Key], fact)
	}
	byHorizon := map[string]map[string]domain.StewardProfileFact{
		domain.StewardProfileHorizonRecent: {}, domain.StewardProfileHorizonStable: {}, domain.StewardProfileHorizonExplicit: {},
	}
	for horizon, keyedCandidates := range candidates {
		for key, values := range keyedCandidates {
			distinctValues := map[string]bool{}
			for _, fact := range values {
				distinctValues[profileFactValueFingerprint(fact.Value)] = true
			}
			conflicted := len(distinctValues) > 1
			for _, fact := range values {
				fact.EffectiveConfidence = effectiveProfileFactConfidence(fact, now, recentDays, conflicted)
				previous, ok := byHorizon[horizon][key]
				if !ok || preferProfileFact(fact, previous) {
					byHorizon[horizon][key] = fact
				}
			}
		}
	}
	result := map[string]domain.StewardProfileSnapshot{}
	for _, horizon := range []string{domain.StewardProfileHorizonRecent, domain.StewardProfileHorizonStable, domain.StewardProfileHorizonExplicit} {
		result[horizon] = profileSnapshotFromFacts(horizon, byHorizon[horizon], now, recentDays)
	}
	merged := map[string]domain.StewardProfileFact{}
	for _, horizon := range []string{domain.StewardProfileHorizonRecent, domain.StewardProfileHorizonStable, domain.StewardProfileHorizonExplicit} {
		for key, fact := range byHorizon[horizon] {
			merged[key] = fact
		}
	}
	result[domain.StewardProfileHorizonMerged] = profileSnapshotFromFacts(domain.StewardProfileHorizonMerged, merged, now, recentDays)
	return result
}

func profileFactValueFingerprint(value map[string]any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(encoded)
}

func effectiveProfileFactConfidence(fact domain.StewardProfileFact, now time.Time, recentDays int, conflicted bool) float64 {
	confidence := fact.Confidence
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}
	if conflicted && !isExplicitUserProfileCorrection(fact) {
		confidence *= profileConflictConfidenceMultiplier
	}
	if fact.Horizon == domain.StewardProfileHorizonRecent {
		window := time.Duration(recentDays) * 24 * time.Hour
		age := now.Sub(fact.ValidFrom)
		if age >= window {
			return 0
		}
		if age > 0 {
			confidence *= 1 - float64(age)/float64(window)
		}
	}
	return confidence
}

func isExplicitUserProfileCorrection(fact domain.StewardProfileFact) bool {
	return fact.Horizon == domain.StewardProfileHorizonExplicit && fact.UserConfirmed && strings.EqualFold(strings.TrimSpace(fact.CreatedBy), "user")
}

func preferProfileFact(candidate, current domain.StewardProfileFact) bool {
	candidateExplicit := isExplicitUserProfileCorrection(candidate)
	currentExplicit := isExplicitUserProfileCorrection(current)
	if candidateExplicit != currentExplicit {
		return candidateExplicit
	}
	if candidate.EffectiveConfidence != current.EffectiveConfidence {
		return candidate.EffectiveConfidence > current.EffectiveConfidence
	}
	if candidate.Version != current.Version {
		return candidate.Version > current.Version
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	return candidate.ID > current.ID
}

func profileSnapshotFromFacts(horizon string, values map[string]domain.StewardProfileFact, now time.Time, recentDays int) domain.StewardProfileSnapshot {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := domain.StewardProfileSnapshot{Horizon: horizon, WindowEnd: now, Facts: []domain.StewardProfileFact{}, Profile: map[string]any{}, CreatedBy: "projector"}
	if horizon == domain.StewardProfileHorizonRecent || horizon == domain.StewardProfileHorizonMerged {
		start := now.AddDate(0, 0, -recentDays)
		result.WindowStart = &start
	}
	for _, key := range keys {
		fact := values[key]
		result.Facts = append(result.Facts, fact)
		result.Profile[key] = fact.Value
	}
	return result
}

func (s *Service) RebuildProfileSnapshots(ctx context.Context, now time.Time, recentDays int, jobID string) (domain.StewardProfileView, error) {
	facts, err := s.ListProfileFacts(ctx, ListProfileFactsInput{Status: domain.StewardProfileFactActive, Limit: 1000})
	if err != nil {
		return domain.StewardProfileView{}, err
	}
	projections := BuildProfileSnapshots(facts, now, recentDays)
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.StewardProfileView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, "profile-snapshot-projector"); err != nil {
		return domain.StewardProfileView{}, err
	}
	for _, horizon := range []string{domain.StewardProfileHorizonRecent, domain.StewardProfileHorizonStable, domain.StewardProfileHorizonExplicit, domain.StewardProfileHorizonMerged} {
		item := projections[horizon]
		if err := tx.QueryRow(ctx, `select coalesce(max(revision),0)+1 from steward_profile_snapshots where profile_scope='default' and view=$1`, horizon).Scan(&item.Revision); err != nil {
			return domain.StewardProfileView{}, err
		}
		item.ID, item.JobID, item.CreatedAt = uuid.NewString(), nil, now.UTC()
		if strings.TrimSpace(jobID) != "" {
			item.JobID = stringPtr(strings.TrimSpace(jobID))
		}
		documentJSON, _ := json.Marshal(map[string]any{"facts": item.Facts, "profile": item.Profile})
		digest := sha256.Sum256(documentJSON)
		var previousID string
		_ = tx.QueryRow(ctx, `select id::text from steward_profile_snapshots where profile_scope='default' and view=$1 order by revision desc limit 1`, horizon).Scan(&previousID)
		advisorStatus := s.autonomyAdvisor().Status()
		if _, err = tx.Exec(ctx, `insert into steward_profile_snapshots
			(id,profile_scope,view,revision,as_of,window_start,window_end,document,fact_count,content_hash,
			 source_run_id,supersedes_id,provider,model,created_at)
			values ($1,'default',$2,$3,$4,$5,$6,$7::jsonb,$8,$9,nullif($10,'')::uuid,nullif($11,'')::uuid,$12,$13,$14)`,
			item.ID, item.Horizon, item.Revision, item.WindowEnd, item.WindowStart, item.WindowEnd, string(documentJSON),
			len(item.Facts), hex.EncodeToString(digest[:]), jobID, previousID, advisorStatus.Provider, advisorStatus.Model, item.CreatedAt); err != nil {
			return domain.StewardProfileView{}, err
		}
		projections[horizon] = item
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StewardProfileView{}, err
	}
	return profileViewFromProjection(projections), nil
}

func profileViewFromProjection(values map[string]domain.StewardProfileSnapshot) domain.StewardProfileView {
	view := domain.StewardProfileView{}
	if item, ok := values[domain.StewardProfileHorizonRecent]; ok {
		value := item
		view.Recent = &value
	}
	if item, ok := values[domain.StewardProfileHorizonStable]; ok {
		value := item
		view.Stable = &value
	}
	if item, ok := values[domain.StewardProfileHorizonExplicit]; ok {
		value := item
		view.Explicit = &value
	}
	if item, ok := values[domain.StewardProfileHorizonMerged]; ok {
		value := item
		view.Merged = &value
	}
	return view
}

func (s *Service) GetProfileView(ctx context.Context) (domain.StewardProfileView, error) {
	rows, err := s.db.Pool.Query(ctx, `select distinct on (view) id::text,view,revision,window_start,
		coalesce(window_end,as_of),document,source_run_id::text,created_at from steward_profile_snapshots
		where profile_scope='default' order by view,revision desc`)
	if err != nil {
		return domain.StewardProfileView{}, err
	}
	defer rows.Close()
	items := map[string]domain.StewardProfileSnapshot{}
	for rows.Next() {
		var item domain.StewardProfileSnapshot
		var documentJSON []byte
		if err := rows.Scan(&item.ID, &item.Horizon, &item.Revision, &item.WindowStart, &item.WindowEnd,
			&documentJSON, &item.JobID, &item.CreatedAt); err != nil {
			return domain.StewardProfileView{}, err
		}
		var document struct {
			Facts   []domain.StewardProfileFact `json:"facts"`
			Profile map[string]any              `json:"profile"`
		}
		_ = json.Unmarshal(documentJSON, &document)
		item.Facts, item.Profile, item.CreatedBy = document.Facts, document.Profile, "projector"
		if item.Facts == nil {
			item.Facts = []domain.StewardProfileFact{}
		}
		for index := range item.Facts {
			if item.Facts[index].EffectiveConfidence == 0 && item.Facts[index].Confidence > 0 {
				// Snapshots written before effective confidence was introduced only
				// contain the asserted value; preserve their prior API semantics.
				item.Facts[index].EffectiveConfidence = item.Facts[index].Confidence
			}
		}
		if item.Profile == nil {
			item.Profile = map[string]any{}
		}
		items[item.Horizon] = item
	}
	if err := rows.Err(); err != nil {
		return domain.StewardProfileView{}, err
	}
	return profileViewFromProjection(items), nil
}

// ProfileSnapshotForView selects exactly one declared profile projection. It
// intentionally does not fall back to merged or another horizon: callers must
// state which contract they need and receive only that snapshot.
func ProfileSnapshotForView(view domain.StewardProfileView, projection string) (*domain.StewardProfileSnapshot, bool) {
	switch projection {
	case domain.StewardProfileHorizonRecent:
		return view.Recent, true
	case domain.StewardProfileHorizonStable:
		return view.Stable, true
	case domain.StewardProfileHorizonExplicit:
		return view.Explicit, true
	case domain.StewardProfileHorizonMerged:
		return view.Merged, true
	default:
		return nil, false
	}
}

func validReportCadence(value string) bool {
	switch value {
	case domain.StewardReportDaily, domain.StewardReportWeekly, domain.StewardReportMonthly:
		return true
	default:
		return false
	}
}

func defaultReportPeriodKey(cadence string, start time.Time) string {
	switch cadence {
	case domain.StewardReportWeekly:
		year, week := start.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	case domain.StewardReportMonthly:
		return start.Format("2006-01")
	default:
		return start.Format("2006-01-02")
	}
}

func reportJobMatchesInput(job domain.StewardIntelligenceJob, input WriteReportInput) bool {
	return intelligenceJobReportCadence(job.Kind) == input.Cadence &&
		job.PeriodKey == input.PeriodKey && timestampsEquivalent(job.PeriodStart, input.PeriodStart) && timestampsEquivalent(job.PeriodEnd, input.PeriodEnd)
}

func timestampsEquivalent(left, right time.Time) bool {
	delta := left.Sub(right)
	if delta < 0 {
		delta = -delta
	}
	return delta < time.Second
}

// resolveReportJobContext binds a model-written report to the durable
// background task even when the model omitted job_id. The preferred linkage is
// the current Episode; exact cadence/period matching is a recovery fallback for
// older providers that omit both identifiers.
func (s *Service) resolveReportJobContext(ctx context.Context, input *WriteReportInput) error {
	if input == nil {
		return nil
	}
	var job domain.StewardIntelligenceJob
	var err error
	switch {
	case strings.TrimSpace(input.JobID) != "":
		job, err = s.GetIntelligenceJob(ctx, strings.TrimSpace(input.JobID))
	case strings.TrimSpace(input.EpisodeID) != "":
		job, err = scanIntelligenceJob(s.db.Pool.QueryRow(ctx, `select `+intelligenceJobColumns+`
			from steward_memory_consolidation_runs where episode_id::text=$1
			order by updated_at desc limit 1`, strings.TrimSpace(input.EpisodeID)))
	default:
		job, err = scanIntelligenceJob(s.db.Pool.QueryRow(ctx, `select `+intelligenceJobColumns+`
			from steward_memory_consolidation_runs where kind=$1 and checkpoint->>'period_key'=$2
			and status in ('processing','executing','waiting_model')
			order by case status when 'executing' then 0 when 'processing' then 1 else 2 end,updated_at desc limit 1`,
			"report_"+input.Cadence, input.PeriodKey))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// An on-demand report is valid without a scheduled background job.
		return nil
	}
	if err != nil {
		return err
	}
	if !reportJobMatchesInput(job, *input) {
		return fmt.Errorf("%w: job %s expects %s/%s from %s to %s", ErrReportJobContextMismatch,
			job.ID, intelligenceJobReportCadence(job.Kind), job.PeriodKey, job.PeriodStart.Format(time.RFC3339), job.PeriodEnd.Format(time.RFC3339))
	}
	if input.EpisodeID != "" && (job.EpisodeID == nil || *job.EpisodeID != input.EpisodeID) {
		return fmt.Errorf("%w: episode %s does not own job %s", ErrReportJobContextMismatch, input.EpisodeID, job.ID)
	}
	input.JobID = job.ID
	if input.EpisodeID == "" && job.EpisodeID != nil {
		input.EpisodeID = *job.EpisodeID
	}
	return nil
}

// reportEvidenceCoverage measures the share of persisted activity sessions in
// the report window that the model actually cited, either directly or through
// a cited activity batch. This avoids the former all-or-nothing metric where a
// single evidence reference made a week-long report appear fully covered.
func (s *Service) reportEvidenceCoverage(ctx context.Context, periodStart, periodEnd time.Time, evidence []ProfileEvidenceInput) (float64, []string, error) {
	referenced := map[string]bool{}
	batchIDs := []string{}
	for _, item := range evidence {
		switch strings.TrimSpace(item.SourceType) {
		case "activity_session":
			referenced[strings.TrimSpace(item.SourceID)] = true
		case "activity_batch":
			if id := strings.TrimSpace(item.SourceID); id != "" {
				batchIDs = append(batchIDs, id)
			}
		}
	}
	if len(batchIDs) > 0 {
		rows, err := s.db.Pool.Query(ctx, `select distinct item_id from steward_activity_batch_items
			where item_type='activity_session' and batch_id::text=any($1::text[])`, batchIDs)
		if err != nil {
			return 0, nil, fmt.Errorf("read report activity-batch coverage: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, nil, err
			}
			referenced[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, nil, err
		}
		rows.Close()
	}
	rows, err := s.db.Pool.Query(ctx, `select id::text from steward_activity_sessions
		where status='closed' and started_at<$2 and ended_at>$1 order by started_at,id`, periodStart.UTC(), periodEnd.UTC())
	if err != nil {
		return 0, nil, fmt.Errorf("read report coverage sessions: %w", err)
	}
	expected := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, nil, err
		}
		expected = append(expected, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, nil, err
	}
	rows.Close()
	if len(expected) == 0 {
		if len(evidence) > 0 {
			return 1, []string{}, nil
		}
		return 0, []string{"no_persisted_evidence"}, nil
	}
	covered := 0
	missing := []string{}
	for _, id := range expected {
		if referenced[id] {
			covered++
			continue
		}
		if len(missing) < 100 {
			missing = append(missing, "activity_session:"+id)
		}
	}
	if omitted := len(expected) - covered - len(missing); omitted > 0 {
		missing = append(missing, fmt.Sprintf("and_%d_more_activity_sessions", omitted))
	}
	return float64(covered) / float64(len(expected)), missing, nil
}

func (s *Service) WriteReport(ctx context.Context, input WriteReportInput) (domain.StewardReport, error) {
	now := time.Now().UTC()
	timezone := input.PeriodStart.Location().String()
	input.Cadence = strings.ToLower(strings.TrimSpace(input.Cadence))
	if !validReportCadence(input.Cadence) {
		return domain.StewardReport{}, fmt.Errorf("report cadence must be daily, weekly, or monthly")
	}
	if input.PeriodStart.IsZero() || input.PeriodEnd.IsZero() || !input.PeriodEnd.After(input.PeriodStart) {
		return domain.StewardReport{}, fmt.Errorf("report period_start and period_end must form a positive interval")
	}
	input.PeriodStart, input.PeriodEnd = input.PeriodStart.UTC(), input.PeriodEnd.UTC()
	input.PeriodKey = defaultString(strings.TrimSpace(input.PeriodKey), defaultReportPeriodKey(input.Cadence, input.PeriodStart))
	if err := s.resolveReportJobContext(ctx, &input); err != nil {
		return domain.StewardReport{}, err
	}
	input.Status = strings.ToLower(defaultString(strings.TrimSpace(input.Status), reportStatusComplete))
	if input.Status != reportStatusComplete && input.Status != reportStatusPartial {
		return domain.StewardReport{}, fmt.Errorf("report status must be complete or partial")
	}
	requestedStatus := input.Status
	input.Title, input.Body = strings.TrimSpace(input.Title), strings.TrimSpace(input.Body)
	if input.Title == "" || input.Body == "" {
		return domain.StewardReport{}, fmt.Errorf("report title and body are required")
	}
	if input.Metrics == nil {
		input.Metrics = map[string]any{}
	}
	input.Evidence = normalizeProfileEvidence(input.Evidence, now)
	if len(input.Evidence) > 0 {
		var err error
		input.Evidence, _, err = s.validatePersistedEvidence(ctx, input.Evidence, &input.PeriodStart, &input.PeriodEnd)
		if err != nil {
			return domain.StewardReport{}, err
		}
	}
	evidenceJSON, err := json.Marshal(input.Evidence)
	if err != nil {
		return domain.StewardReport{}, err
	}
	coverage, missingEvidence, err := s.reportEvidenceCoverage(ctx, input.PeriodStart, input.PeriodEnd, input.Evidence)
	if err != nil {
		return domain.StewardReport{}, err
	}
	coverageInsufficient := requestedStatus == reportStatusComplete && coverage < reportEvidenceCoverageThreshold
	reportCheckpoint := map[string]any{}
	if coverageInsufficient {
		input.Status = reportStatusPartial
		input.Metrics["evidence_coverage"] = coverage
		input.Metrics["evidence_coverage_threshold"] = reportEvidenceCoverageThreshold
		input.Metrics["quality_status"] = "low_coverage"
		qualityError := fmt.Sprintf("report evidence coverage %.3f is below required %.2f", coverage, reportEvidenceCoverageThreshold)
		if current := strings.TrimSpace(input.Error); current != "" {
			input.Error = current + "; " + qualityError
		} else {
			input.Error = qualityError
		}
		reportCheckpoint["report_quality"] = map[string]any{
			"status":            "low_coverage",
			"coverage":          coverage,
			"required_coverage": reportEvidenceCoverageThreshold,
			"missing_evidence":  missingEvidence,
			"max_attempts":      reportQualityMaxAttempts,
		}
	}
	sectionsJSON, err := json.Marshal([]map[string]any{
		{"kind": "title", "content": truncateAdvisorText(input.Title, 500)},
		{"kind": "body", "content": truncateAdvisorText(input.Body, 128000)},
		{"kind": "metrics", "value": input.Metrics},
	})
	if err != nil {
		return domain.StewardReport{}, err
	}
	missingEvidenceJSON, err := json.Marshal(missingEvidence)
	if err != nil {
		return domain.StewardReport{}, err
	}
	reportCheckpointJSON, err := json.Marshal(reportCheckpoint)
	if err != nil {
		return domain.StewardReport{}, err
	}
	digest := sha256.Sum256(append(append([]byte{}, sectionsJSON...), evidenceJSON...))
	idempotencyKey := fmt.Sprintf("report:%s:%s:%x", input.Cadence, input.PeriodKey, digest)
	if strings.TrimSpace(input.JobID) != "" {
		idempotencyKey = fmt.Sprintf("job:%s:%x", strings.TrimSpace(input.JobID), digest)
	}
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.StewardReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, "report:"+input.Cadence+":"+input.PeriodKey); err != nil {
		return domain.StewardReport{}, err
	}
	var existingID string
	if err = tx.QueryRow(ctx, `select id::text from steward_reports where idempotency_key=$1`, idempotencyKey).Scan(&existingID); err == nil {
		if err = tx.Commit(ctx); err != nil {
			return domain.StewardReport{}, err
		}
		existing, loadErr := s.GetReport(ctx, existingID)
		if loadErr != nil {
			return domain.StewardReport{}, loadErr
		}
		return s.finalizeReportNotification(ctx, existing), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardReport{}, err
	}
	var previousID, previousStatus string
	var previousRevision int
	err = tx.QueryRow(ctx, `select id::text,status,revision from steward_reports where profile_scope='default' and cadence=$1 and period_key=$2
		and status in ('complete','partial') order by revision desc limit 1 for update`, input.Cadence, input.PeriodKey).Scan(&previousID, &previousStatus, &previousRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardReport{}, err
	}
	if input.Status == reportStatusPartial && previousStatus == reportStatusComplete && !coverageInsufficient {
		if err = tx.Commit(ctx); err != nil {
			return domain.StewardReport{}, err
		}
		existing, loadErr := s.GetReport(ctx, previousID)
		if loadErr != nil {
			return domain.StewardReport{}, loadErr
		}
		return s.finalizeReportNotification(ctx, existing), nil
	}
	revision := previousRevision + 1
	if revision <= 0 {
		revision = 1
	}
	report := domain.StewardReport{
		ID: uuid.NewString(), Cadence: input.Cadence, PeriodKey: input.PeriodKey, PeriodStart: input.PeriodStart,
		PeriodEnd: input.PeriodEnd, Revision: revision, Status: input.Status, Title: truncateAdvisorText(input.Title, 500),
		Summary: truncateAdvisorText(strings.TrimSpace(input.Summary), 8000), Body: truncateAdvisorText(input.Body, 128000),
		Metrics: input.Metrics, Silent: input.Silent, EvidenceCount: len(input.Evidence), Provider: strings.TrimSpace(input.Provider),
		EvidenceCoverage: coverage, MissingEvidence: missingEvidence,
		Model: strings.TrimSpace(input.Model), ErrorSummary: truncateAdvisorText(strings.TrimSpace(input.Error), 4000), CreatedAt: now, UpdatedAt: now,
	}
	if previousID != "" {
		report.SupersedesID = stringPtr(previousID)
	}
	if strings.TrimSpace(input.EpisodeID) != "" {
		report.EpisodeID = stringPtr(strings.TrimSpace(input.EpisodeID))
	}
	if strings.TrimSpace(input.JobID) != "" {
		report.JobID = stringPtr(strings.TrimSpace(input.JobID))
	}
	if input.Status == reportStatusComplete || input.Status == reportStatusPartial {
		report.CompletedAt = &now
	}
	deliveryDecision := "deliver"
	if report.Silent {
		deliveryDecision = "silent"
	}
	if _, err = tx.Exec(ctx, `insert into steward_reports (
		id,profile_scope,cadence,period_key,timezone,period_start,period_end,revision,status,summary,sections,
		evidence_manifest,evidence_count,evidence_coverage,missing_evidence,supersedes_id,episode_id,delivery_decision,
		due_at,last_started_at,completed_at,attempt_count,next_attempt_at,checkpoint,idempotency_key,control_generation,
		provider,model,error_summary,created_at,updated_at
	) values ($1,'default',$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12,$13,$14::jsonb,
		nullif($15,'')::uuid,nullif($16,'')::uuid,$17,$18,$18,$19,1,$18,$20::jsonb,$21,0,$22,$23,$24,$18,$18)`,
		report.ID, report.Cadence, report.PeriodKey, timezone, report.PeriodStart, report.PeriodEnd, report.Revision,
		report.Status, report.Summary, string(sectionsJSON), string(evidenceJSON), report.EvidenceCount, coverage,
		string(missingEvidenceJSON), previousID, input.EpisodeID, deliveryDecision, now, report.CompletedAt, string(reportCheckpointJSON), idempotencyKey, report.Provider,
		report.Model, report.ErrorSummary); err != nil {
		return domain.StewardReport{}, err
	}
	if previousID != "" {
		if _, err = tx.Exec(ctx, `update steward_reports set status='superseded',updated_at=$2 where id=$1 and status in ('complete','partial')`, previousID, now); err != nil {
			return domain.StewardReport{}, err
		}
	}
	for _, item := range input.Evidence {
		evidence := domain.StewardReportEvidence{ID: uuid.NewString(), ReportID: report.ID, SourceType: item.SourceType,
			SourceID: item.SourceID, Summary: item.Summary, ContentHash: item.ContentHash, CreatedAt: now}
		report.Evidence = append(report.Evidence, evidence)
	}
	if coverageInsufficient {
		qualityJSON, marshalErr := json.Marshal(reportCheckpoint["report_quality"])
		if marshalErr != nil {
			return domain.StewardReport{}, marshalErr
		}
		if jobID := strings.TrimSpace(input.JobID); jobID != "" {
			if _, err = tx.Exec(ctx, `update steward_memory_consolidation_runs set
				checkpoint=jsonb_set(
					jsonb_set(coalesce(checkpoint,'{}'::jsonb),'{max_attempts}',to_jsonb(
						case when coalesce(nullif(checkpoint->>'max_attempts','')::integer,0)>0
						then (checkpoint->>'max_attempts')::integer else $2::integer end
					),true),
					'{report_quality}',$3::jsonb,true
				),error_summary=$4,updated_at=$5 where id=$1`, jobID, reportQualityMaxAttempts, string(qualityJSON), report.ErrorSummary, now); err != nil {
				return domain.StewardReport{}, fmt.Errorf("checkpoint low-coverage report retry: %w", err)
			}
		} else {
			retry := EnqueueIntelligenceJobInput{
				Kind: "report_" + report.Cadence, PeriodKey: report.PeriodKey,
				PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd, DueAt: now,
				MaxAttempts: reportQualityMaxAttempts,
				Input: map[string]any{
					"regenerate_report_id": report.ID,
					"source_revision":      report.Revision,
					"reason":               report.ErrorSummary,
					"requested_via":        "report_coverage_gate",
					"missing_evidence":     missingEvidence,
				},
			}
			if err = insertIntelligenceJobTx(ctx, tx, retry, "intelligence:report-quality:"+report.ID, now); err != nil {
				return domain.StewardReport{}, fmt.Errorf("enqueue low-coverage report retry: %w", err)
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StewardReport{}, err
	}
	return s.finalizeReportNotification(ctx, report), nil
}

func reportNotificationDedupeKey(reportID string) string {
	return "steward-report:" + strings.TrimSpace(reportID)
}

func reportNotificationBody(report domain.StewardReport) string {
	if summary := strings.TrimSpace(report.Summary); summary != "" {
		return truncateAdvisorText(summary, 2000)
	}
	return "报告已生成，可在私人管家中查看完整内容。"
}

// finalizeReportNotification keeps report persistence independent from
// notification orchestration. A notification failure is recorded as durable
// retry state and never changes a successfully committed report into an API
// failure. context.WithoutCancel also gives the checkpoint write a short grace
// period when the request is cancelled immediately after the report commit.
func (s *Service) finalizeReportNotification(ctx context.Context, report domain.StewardReport) domain.StewardReport {
	if report.Silent || (report.Status != reportStatusComplete && report.Status != reportStatusPartial) {
		return report
	}
	deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, _ = s.reconcileReportNotification(deliveryCtx, report, time.Now().UTC())
	if updated, err := s.GetReport(deliveryCtx, report.ID); err == nil {
		return updated
	}
	return report
}

func (s *Service) reconcileReportNotification(ctx context.Context, report domain.StewardReport, now time.Time) (bool, error) {
	if report.Silent || (report.Status != reportStatusComplete && report.Status != reportStatusPartial) {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	dedupeKey := reportNotificationDedupeKey(report.ID)
	notification, createErr := s.CreateNotification(ctx, CreateNotificationInput{
		SourceType: "report",
		SourceID:   report.ID,
		Title:      report.Title,
		Body:       reportNotificationBody(report),
		Category:   "report",
		Priority:   "low",
		DedupeKey:  dedupeKey,
		Metadata: map[string]any{
			"report_id":  report.ID,
			"cadence":    report.Cadence,
			"period_key": report.PeriodKey,
			"revision":   report.Revision,
			"status":     report.Status,
		},
		DecisionContext: map[string]any{"origin": "steward_report"},
	})
	if createErr != nil {
		// CreateNotification can persist the deduplicated notification before
		// endpoint routing fails. Preserve that relationship so a later
		// reconciliation repairs the same notification rather than creating a
		// second user-visible message.
		var notificationID string
		lookupErr := s.db.Pool.QueryRow(ctx, `select id::text from steward_notifications where dedupe_key=$1`, dedupeKey).Scan(&notificationID)
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			return false, errors.Join(fmt.Errorf("create report notification: %w", createErr), fmt.Errorf("look up report notification: %w", lookupErr))
		}
		retryAt := now.Add(time.Minute)
		message := truncateAdvisorText(createErr.Error(), 2000)
		_, recordErr := s.db.Pool.Exec(ctx, `update steward_reports set
			notification_id=coalesce(notification_id,nullif($2::text,'')::uuid),
			checkpoint=jsonb_set(coalesce(checkpoint,'{}'::jsonb),'{notification_delivery}',jsonb_build_object(
				'status','retrying','last_error',$3::text,'next_attempt_at',$4::timestamptz,
				'attempt_count',coalesce(nullif(checkpoint#>>'{notification_delivery,attempt_count}','')::integer,0)+1
			),true),next_attempt_at=$4::timestamptz,updated_at=$5::timestamptz where id=$1`, report.ID, notificationID, message, retryAt, now)
		if recordErr != nil {
			return false, errors.Join(fmt.Errorf("create report notification: %w", createErr), fmt.Errorf("record report notification retry: %w", recordErr))
		}
		return false, nil
	}

	// A prior routing failure leaves the deduplicated notification in failed
	// state. Once reconciliation has recreated a queued delivery, make it
	// claimable again without disturbing terminal or already-sent messages.
	_, err := s.db.Pool.Exec(ctx, `update steward_notifications n set status='queued',updated_at=$2
		where n.id=$1 and n.status='failed' and exists (
			select 1 from steward_notification_deliveries d where d.notification_id=n.id
			and d.schedule_revision=n.schedule_revision and d.status in ('queued','retrying','sending')
		)`, notification.ID, now)
	if err != nil {
		return false, err
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_reports set notification_id=$2::uuid,
		checkpoint=jsonb_set(coalesce(checkpoint,'{}'::jsonb),'{notification_delivery}',jsonb_build_object(
			'status','queued','last_error','','notification_id',$2::text,'updated_at',$3::timestamptz,
			'attempt_count',coalesce(nullif(checkpoint#>>'{notification_delivery,attempt_count}','')::integer,0)
		),true),next_attempt_at=$3::timestamptz,updated_at=$3::timestamptz where id=$1`, report.ID, notification.ID, now)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReconcileReportNotifications repairs notification creation independently of
// report generation. CreateNotification's dedupe key makes repeated controller
// cycles and concurrent retries converge on one persisted notification.
func (s *Service) ReconcileReportNotifications(ctx context.Context, now time.Time, limit int) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	limit = normalizeLimit(limit, 16, 100)
	rows, err := s.db.Pool.Query(ctx, reportSelect+` where profile_scope='default'
		and status in ('complete','partial') and delivery_decision='deliver' and next_attempt_at<=$1
		and (notification_id is null or checkpoint#>>'{notification_delivery,status}'='retrying')
		order by next_attempt_at,created_at limit $2`, now, limit)
	if err != nil {
		return 0, err
	}
	reports := []domain.StewardReport{}
	for rows.Next() {
		report, scanErr := scanReport(rows)
		if scanErr != nil {
			rows.Close()
			return 0, scanErr
		}
		reports = append(reports, report)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	reconciled := 0
	var joined error
	for _, report := range reports {
		created, reconcileErr := s.reconcileReportNotification(ctx, report, now)
		if reconcileErr != nil {
			joined = errors.Join(joined, reconcileErr)
			continue
		}
		if created {
			reconciled++
		}
	}
	return reconciled, joined
}

func scanReport(row rowScanner) (domain.StewardReport, error) {
	var item domain.StewardReport
	var sectionsJSON, evidenceJSON, missingEvidenceJSON []byte
	var deliveryDecision, idempotencyKey string
	err := row.Scan(&item.ID, &item.Cadence, &item.PeriodKey, &item.PeriodStart, &item.PeriodEnd, &item.Revision,
		&item.Status, &item.Summary, &sectionsJSON, &evidenceJSON, &item.EvidenceCount, &item.EvidenceCoverage, &missingEvidenceJSON,
		&item.SupersedesID, &item.EpisodeID, &item.NotificationID, &idempotencyKey, &deliveryDecision, &item.Provider, &item.Model, &item.ErrorSummary,
		&item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	if err == nil {
		var sections []map[string]any
		_ = json.Unmarshal(sectionsJSON, &sections)
		item.Metrics = map[string]any{}
		for _, section := range sections {
			switch fmt.Sprint(section["kind"]) {
			case "title":
				item.Title = fmt.Sprint(section["content"])
			case "body":
				item.Body = fmt.Sprint(section["content"])
			case "metrics":
				if value, ok := section["value"].(map[string]any); ok {
					item.Metrics = value
				}
			}
		}
		var evidence []ProfileEvidenceInput
		_ = json.Unmarshal(evidenceJSON, &evidence)
		_ = json.Unmarshal(missingEvidenceJSON, &item.MissingEvidence)
		for _, value := range evidence {
			item.Evidence = append(item.Evidence, domain.StewardReportEvidence{
				ID: evidenceHash(value.SourceType, value.SourceID, value.ContentHash), ReportID: item.ID,
				SourceType: value.SourceType, SourceID: value.SourceID, Summary: value.Summary, ContentHash: value.ContentHash, CreatedAt: item.CreatedAt,
			})
		}
		item.Silent = deliveryDecision == "silent"
		if strings.HasPrefix(idempotencyKey, "job:") {
			parts := strings.SplitN(strings.TrimPrefix(idempotencyKey, "job:"), ":", 2)
			if len(parts) > 0 && parts[0] != "" {
				item.JobID = stringPtr(parts[0])
			}
		}
		if item.Metrics == nil {
			item.Metrics = map[string]any{}
		}
	}
	return item, err
}

const reportSelect = `select id::text,cadence,period_key,period_start,period_end,revision,status,summary,sections,
	evidence_manifest,evidence_count,evidence_coverage,missing_evidence,supersedes_id::text,episode_id::text,notification_id::text,idempotency_key,delivery_decision,provider,model,error_summary,
	created_at,updated_at,completed_at from steward_reports`

func (s *Service) GetReport(ctx context.Context, id string) (domain.StewardReport, error) {
	item, err := scanReport(s.db.Pool.QueryRow(ctx, reportSelect+` where id=$1`, id))
	if err != nil {
		return item, err
	}
	return item, nil
}

func (s *Service) ListReports(ctx context.Context, cadence string, limit int, includeHistory bool) ([]domain.StewardReport, error) {
	limit = normalizeLimit(limit, 50, 500)
	rows, err := s.db.Pool.Query(ctx, reportSelect+`
		where profile_scope='default' and ($1='' or cadence=$1) and ($2 or status<>'superseded') order by period_end desc,revision desc limit $3`,
		strings.ToLower(strings.TrimSpace(cadence)), includeHistory, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardReport{}
	for rows.Next() {
		item, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// RegenerateReport enqueues a durable report job for an existing report
// period. Concurrent/retried requests reuse the active regeneration for the
// same source report, while a later request after terminal completion creates
// a new job. The normal profile/report controller owns the lease and creates a
// resumable Agent Episode, so this API never pretends that regeneration has
// completed synchronously.
func (s *Service) RegenerateReport(ctx context.Context, reportID string, input RegenerateReportInput) (ReportRegenerationResult, error) {
	reportID = strings.TrimSpace(reportID)
	report, err := s.GetReport(ctx, reportID)
	if err != nil {
		return ReportRegenerationResult{}, err
	}
	if !validReportCadence(report.Cadence) {
		return ReportRegenerationResult{}, fmt.Errorf("report %s has unsupported cadence %q", report.ID, report.Cadence)
	}

	now := time.Now().UTC()
	reason := truncateAdvisorText(strings.TrimSpace(input.Reason), 2000)
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ReportRegenerationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := "report-regeneration:" + report.ID
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return ReportRegenerationResult{}, err
	}

	kind := "report_" + report.Cadence
	existing, existingErr := scanIntelligenceJob(tx.QueryRow(ctx, `select `+intelligenceJobColumns+`
		from steward_memory_consolidation_runs
		where profile_scope='default' and kind=$1 and checkpoint->'input'->>'regenerate_report_id'=$2
		  and status in ('pending','processing','executing','waiting_model')
		order by created_at desc limit 1 for update`, kind, report.ID))
	if existingErr == nil {
		if err = tx.Commit(ctx); err != nil {
			return ReportRegenerationResult{}, err
		}
		return ReportRegenerationResult{SourceReportID: report.ID, SourceRevision: report.Revision, Job: existing, Created: false}, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return ReportRegenerationResult{}, existingErr
	}

	jobID := uuid.NewString()
	idempotencyKey := "intelligence:regenerate:report:" + report.ID + ":" + uuid.NewString()
	jobInput := map[string]any{
		"regenerate_report_id": report.ID,
		"source_revision":      report.Revision,
		"requested_at":         now.Format(time.RFC3339Nano),
		"requested_via":        "management_api",
	}
	if reason != "" {
		jobInput["reason"] = reason
	}
	checkpointJSON, err := json.Marshal(map[string]any{
		"period_key":   report.PeriodKey,
		"input":        jobInput,
		"max_attempts": 0,
	})
	if err != nil {
		return ReportRegenerationResult{}, err
	}
	job, err := scanIntelligenceJob(tx.QueryRow(ctx, `insert into steward_memory_consolidation_runs
		(id,kind,profile_scope,window_start,window_end,status,due_at,attempt_count,next_attempt_at,checkpoint,
		 idempotency_key,control_generation,created_at,updated_at)
		values ($1,$2,'default',$3,$4,'pending',$5,0,$5,$6::jsonb,$7,0,$5,$5)
		returning `+intelligenceJobColumns,
		jobID, kind, report.PeriodStart.UTC(), report.PeriodEnd.UTC(), now, string(checkpointJSON), idempotencyKey))
	if err != nil {
		return ReportRegenerationResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ReportRegenerationResult{}, err
	}
	return ReportRegenerationResult{SourceReportID: report.ID, SourceRevision: report.Revision, Job: job, Created: true}, nil
}

func (s *Service) EnsurePartialReport(ctx context.Context, job domain.StewardIntelligenceJob, cause error) (domain.StewardReport, error) {
	cadence := strings.TrimPrefix(job.Kind, "report_")
	if !validReportCadence(cadence) {
		return domain.StewardReport{}, fmt.Errorf("job %s is not a report job", job.ID)
	}
	reason := "模型暂时不可用；当前报告只包含已完成的本地事实聚合，恢复后会生成完整修订版。"
	if cause != nil {
		reason += "\n\n错误摘要：" + truncateAdvisorText(sanitizeRuntimeError(cause), 1000)
	}
	return s.WriteReport(ctx, WriteReportInput{
		Cadence: cadence, PeriodKey: job.PeriodKey, PeriodStart: job.PeriodStart, PeriodEnd: job.PeriodEnd,
		Status: reportStatusPartial, Title: reportTitle(cadence, job.PeriodStart), Summary: "事实型临时报告",
		Body: reason, Metrics: map[string]any{"partial": true, "source": "deterministic_fallback"}, Silent: true,
		JobID: job.ID, Error: defaultString(job.FailureSummary, reason),
	})
}

func reportTitle(cadence string, start time.Time) string {
	switch cadence {
	case domain.StewardReportWeekly:
		year, week := start.Local().ISOWeek()
		return fmt.Sprintf("%d 年第 %02d 周周报", year, week)
	case domain.StewardReportMonthly:
		return start.Local().Format("2006 年 01 月") + "月报"
	default:
		return start.Local().Format("2006 年 01 月 02 日") + "日报"
	}
}

func validIntelligenceJobKind(value string) bool {
	switch value {
	case intelligenceJobProfileConsolidation, intelligenceJobProfileCorrectionReview,
		intelligenceJobReportDaily, intelligenceJobReportWeekly, intelligenceJobReportMonthly:
		return true
	default:
		return false
	}
}

func insertIntelligenceJobTx(ctx context.Context, tx pgx.Tx, input EnqueueIntelligenceJobInput, idempotencyKey string, now time.Time) error {
	checkpointJSON, err := json.Marshal(map[string]any{
		"period_key":   input.PeriodKey,
		"input":        input.Input,
		"max_attempts": input.MaxAttempts,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into steward_memory_consolidation_runs
		(id,kind,profile_scope,window_start,window_end,status,due_at,attempt_count,next_attempt_at,checkpoint,
		 idempotency_key,control_generation,created_at,updated_at)
		values ($1,$2,'default',$3,$4,'pending',$5,0,$5,$6::jsonb,$7,0,$8,$8)
		on conflict (idempotency_key) do nothing`, uuid.NewString(), input.Kind, input.PeriodStart.UTC(), input.PeriodEnd.UTC(),
		input.DueAt.UTC(), string(checkpointJSON), idempotencyKey, now.UTC())
	return err
}

func (s *Service) EnqueueIntelligenceJob(ctx context.Context, input EnqueueIntelligenceJobInput) (domain.StewardIntelligenceJob, bool, error) {
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	if !validIntelligenceJobKind(input.Kind) {
		return domain.StewardIntelligenceJob{}, false, fmt.Errorf("unsupported intelligence job kind %q", input.Kind)
	}
	if input.PeriodStart.IsZero() || input.PeriodEnd.IsZero() || !input.PeriodEnd.After(input.PeriodStart) {
		return domain.StewardIntelligenceJob{}, false, fmt.Errorf("job period is invalid")
	}
	if input.PeriodKey == "" {
		input.PeriodKey = defaultReportPeriodKey(strings.TrimPrefix(input.Kind, "report_"), input.PeriodStart)
	}
	if input.Input == nil {
		input.Input = map[string]any{}
	}
	if input.MaxAttempts <= 0 {
		input.MaxAttempts = 0
	}
	if input.DueAt.IsZero() {
		input.DueAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	idempotencyKey := "intelligence:" + input.Kind + ":" + input.PeriodKey
	checkpoint := map[string]any{
		"period_key":   input.PeriodKey,
		"input":        input.Input,
		"max_attempts": input.MaxAttempts,
	}
	checkpointJSON, _ := json.Marshal(checkpoint)
	id := uuid.NewString()
	row := s.db.Pool.QueryRow(ctx, `insert into steward_memory_consolidation_runs
		(id,kind,profile_scope,window_start,window_end,status,due_at,attempt_count,next_attempt_at,checkpoint,
		idempotency_key,control_generation,created_at,updated_at)
		values ($1,$2,'default',$3,$4,'pending',$5,0,$5,$6::jsonb,$7,0,$8,$8)
		on conflict (idempotency_key) do nothing returning `+intelligenceJobColumns,
		id, input.Kind, input.PeriodStart.UTC(), input.PeriodEnd.UTC(), input.DueAt.UTC(), string(checkpointJSON), idempotencyKey, now)
	job, err := scanIntelligenceJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		job, getErr := s.getIntelligenceJobByKey(ctx, idempotencyKey)
		return job, false, getErr
	}
	return job, err == nil, err
}

const intelligenceJobColumns = `id::text,kind,window_start,window_end,status,due_at,attempt_count,next_attempt_at,
	lease_owner,lease_expires_at,checkpoint,control_generation,episode_id::text,error_summary,created_at,updated_at,completed_at`

const intelligenceJobReturningColumns = `j.id::text,j.kind,j.window_start,j.window_end,j.status,j.due_at,j.attempt_count,j.next_attempt_at,
	j.lease_owner,j.lease_expires_at,j.checkpoint,j.control_generation,j.episode_id::text,j.error_summary,j.created_at,j.updated_at,j.completed_at`

func scanIntelligenceJob(row rowScanner) (domain.StewardIntelligenceJob, error) {
	var item domain.StewardIntelligenceJob
	var checkpointJSON []byte
	err := row.Scan(&item.ID, &item.Kind, &item.PeriodStart, &item.PeriodEnd, &item.Status, &item.DueAt,
		&item.Attempts, &item.NextAttemptAt, &item.LeaseOwner, &item.LeaseExpiresAt, &checkpointJSON,
		&item.ControlGeneration, &item.EpisodeID, &item.FailureSummary, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	if err == nil {
		_ = json.Unmarshal(checkpointJSON, &item.Checkpoint)
		if item.Checkpoint == nil {
			item.Checkpoint = map[string]any{}
		}
		item.PeriodKey, _ = item.Checkpoint["period_key"].(string)
		item.Input, _ = item.Checkpoint["input"].(map[string]any)
		if item.Input == nil {
			item.Input = map[string]any{}
		}
		item.MaxAttempts = checkpointInt(item.Checkpoint, "max_attempts")
		if reportID, ok := item.Checkpoint["report_id"].(string); ok && strings.TrimSpace(reportID) != "" {
			item.ReportID = stringPtr(reportID)
		}
	}
	return item, err
}

func checkpointInt(checkpoint map[string]any, key string) int {
	switch value := checkpoint[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func (s *Service) getIntelligenceJobByKey(ctx context.Context, idempotencyKey string) (domain.StewardIntelligenceJob, error) {
	return scanIntelligenceJob(s.db.Pool.QueryRow(ctx, `select `+intelligenceJobColumns+` from steward_memory_consolidation_runs where idempotency_key=$1`, idempotencyKey))
}

func (s *Service) GetIntelligenceJob(ctx context.Context, id string) (domain.StewardIntelligenceJob, error) {
	return scanIntelligenceJob(s.db.Pool.QueryRow(ctx, `select `+intelligenceJobColumns+` from steward_memory_consolidation_runs where id=$1`, id))
}

func (s *Service) ListIntelligenceJobs(ctx context.Context, status string, limit int) ([]domain.StewardIntelligenceJob, error) {
	limit = normalizeLimit(limit, 50, 200)
	rows, err := s.db.Pool.Query(ctx, `select `+intelligenceJobColumns+` from steward_memory_consolidation_runs
		where profile_scope='default' and ($1='' or status=$1) order by due_at desc,created_at desc limit $2`,
		strings.ToLower(strings.TrimSpace(status)), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.StewardIntelligenceJob, 0, limit)
	for rows.Next() {
		item, err := scanIntelligenceJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ClaimIntelligenceJobs(ctx context.Context, workerID string, now time.Time, leaseTTL time.Duration, limit int) ([]domain.StewardIntelligenceJob, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	if leaseTTL <= 0 {
		leaseTTL = 2 * time.Minute
	}
	limit = normalizeLimit(limit, 4, 32)
	rows, err := s.db.Pool.Query(ctx, `with picked as (
		select id from steward_memory_consolidation_runs where status in ('pending','waiting_model','processing')
		and next_attempt_at<=$1 and (lease_expires_at is null or lease_expires_at<$1)
		order by next_attempt_at,created_at for update skip locked limit $2
	) update steward_memory_consolidation_runs j set status='processing',attempt_count=j.attempt_count+1,
		last_started_at=$1,lease_owner=$3,lease_expires_at=$4,control_generation=j.control_generation+1,
		updated_at=$1 from picked where j.id=picked.id
		returning `+intelligenceJobReturningColumns, now.UTC(), limit, workerID, now.UTC().Add(leaseTTL))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardIntelligenceJob{}
	for rows.Next() {
		item, err := scanIntelligenceJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) LinkIntelligenceJobEpisode(ctx context.Context, job domain.StewardIntelligenceJob, episodeID string) error {
	tag, err := s.db.Pool.Exec(ctx, `update steward_memory_consolidation_runs set status='executing',episode_id=$4,
		lease_owner='',lease_expires_at=null,updated_at=$5 where id=$1 and status='processing' and lease_owner=$2 and control_generation=$3`,
		job.ID, job.LeaseOwner, job.ControlGeneration, episodeID, time.Now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAgentEpisodeClaimLost
	}
	return nil
}

func intelligenceRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for i := 1; i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	return delay
}

func (s *Service) DeferIntelligenceJob(ctx context.Context, job domain.StewardIntelligenceJob, cause error) error {
	now := time.Now().UTC()
	status := intelligenceJobWaitingModel
	var completedAt *time.Time
	if job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
		status = intelligenceJobFailed
		completedAt = &now
	}
	tag, err := s.db.Pool.Exec(ctx, `update steward_memory_consolidation_runs set status=$4,error_summary=$5,
		next_attempt_at=$6,lease_owner='',lease_expires_at=null,updated_at=$7,completed_at=$8 where id=$1 and status='processing'
		and lease_owner=$2 and control_generation=$3`, job.ID, job.LeaseOwner, job.ControlGeneration,
		status, truncateAdvisorText(sanitizeRuntimeError(cause), 4000), now.Add(intelligenceRetryDelay(job.Attempts)), now, completedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAgentEpisodeClaimLost
	}
	return nil
}

func (s *Service) CancelIntelligenceJob(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `update steward_memory_consolidation_runs set status='cancelled',control_generation=control_generation+1,
		lease_owner='',lease_expires_at=null,updated_at=$2,completed_at=$2 where id=$1 and status not in ('completed','partial','failed','cancelled')`, id, now)
	return err
}

type dueIntelligencePeriod struct {
	Kind, Key         string
	Start, End, DueAt time.Time
}

func dueIntelligencePeriods(now time.Time) []dueIntelligencePeriod {
	location := now.Location()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	result := []dueIntelligencePeriod{}
	if !now.Before(dayStart.Add(21*time.Hour + 30*time.Minute)) {
		result = append(result, dueIntelligencePeriod{intelligenceJobReportDaily, dayStart.Format("2006-01-02"), dayStart, now, dayStart.Add(21*time.Hour + 30*time.Minute)})
	}
	if now.Weekday() == time.Sunday && !now.Before(dayStart.Add(21*time.Hour+45*time.Minute)) {
		year, week := now.ISOWeek()
		start := dayStart.AddDate(0, 0, -6)
		result = append(result, dueIntelligencePeriod{intelligenceJobReportWeekly, fmt.Sprintf("%04d-W%02d", year, week), start, now, dayStart.Add(21*time.Hour + 45*time.Minute)})
	}
	if now.Day() == 1 && !now.Before(dayStart.Add(22*time.Hour)) {
		end := dayStart
		start := end.AddDate(0, -1, 0)
		result = append(result, dueIntelligencePeriod{intelligenceJobReportMonthly, start.Format("2006-01"), start, end, dayStart.Add(22 * time.Hour)})
	}
	return result
}

func dueIntelligencePeriodsForSettings(now time.Time, settings IntelligenceSettings) ([]dueIntelligencePeriod, error) {
	location, err := intelligenceScheduleLocation(settings.Timezone, time.Local)
	if err != nil {
		return nil, err
	}
	localNow := now.In(location)
	dailyHour, dailyMinute, err := parseLocalClock(settings.DailyReportFallbackLocal)
	if err != nil {
		return nil, err
	}
	weeklyHour, weeklyMinute, err := parseLocalClock(settings.WeeklyReportLocal)
	if err != nil {
		return nil, err
	}
	monthlyHour, monthlyMinute, err := parseLocalClock(settings.MonthlyReportLocal)
	if err != nil {
		return nil, err
	}

	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	latestDaily := dayStart
	if localNow.Before(time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), dailyHour, dailyMinute, 0, 0, location)) {
		latestDaily = latestDaily.AddDate(0, 0, -1)
	}
	periods := make([]dueIntelligencePeriod, 0, settings.ProfileBootstrapDays+settings.ReportCatchupDays+2)
	profileDays := settings.ProfileBootstrapDays
	if profileDays <= 0 {
		profileDays = 30
	}
	for offset := profileDays - 1; offset >= 0; offset-- {
		day := latestDaily.AddDate(0, 0, -offset)
		dueAt := time.Date(day.Year(), day.Month(), day.Day(), dailyHour, dailyMinute, 0, 0, location)
		start := dueAt.AddDate(0, 0, -1)
		periods = append(periods, dueIntelligencePeriod{Kind: intelligenceJobProfileConsolidation,
			Key: day.Format("2006-01-02"), Start: start, End: dueAt, DueAt: dueAt})
	}

	catchupDays := settings.ReportCatchupDays
	if catchupDays <= 0 {
		catchupDays = 7
	}
	earliestReportDay := latestDaily.AddDate(0, 0, -(catchupDays - 1))
	for day := earliestReportDay; !day.After(latestDaily); day = day.AddDate(0, 0, 1) {
		dailyDue := time.Date(day.Year(), day.Month(), day.Day(), dailyHour, dailyMinute, 0, 0, location)
		periods = append(periods, dueIntelligencePeriod{Kind: intelligenceJobReportDaily,
			Key: day.Format("2006-01-02"), Start: dailyDue.AddDate(0, 0, -1), End: dailyDue, DueAt: dailyDue})

		weeklyDue := time.Date(day.Year(), day.Month(), day.Day(), weeklyHour, weeklyMinute, 0, 0, location)
		if int(day.Weekday()) == settings.WeeklyReportDay && !weeklyDue.After(localNow) {
			year, week := day.ISOWeek()
			periods = append(periods, dueIntelligencePeriod{Kind: intelligenceJobReportWeekly,
				Key: fmt.Sprintf("%04d-W%02d", year, week), Start: weeklyDue.AddDate(0, 0, -7), End: weeklyDue, DueAt: weeklyDue})
		}

		monthlyDue := time.Date(day.Year(), day.Month(), day.Day(), monthlyHour, monthlyMinute, 0, 0, location)
		if day.Day() == 1 && !monthlyDue.After(localNow) {
			periodEnd := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, location)
			periodStart := periodEnd.AddDate(0, -1, 0)
			periods = append(periods, dueIntelligencePeriod{Kind: intelligenceJobReportMonthly,
				Key: periodStart.Format("2006-01"), Start: periodStart, End: periodEnd, DueAt: monthlyDue})
		}
	}
	sort.SliceStable(periods, func(i, j int) bool {
		if periods[i].DueAt.Equal(periods[j].DueAt) {
			return periods[i].Kind < periods[j].Kind
		}
		return periods[i].DueAt.Before(periods[j].DueAt)
	})
	return periods, nil
}

func intelligenceScheduleLocation(configured string, systemLocal *time.Location) (*time.Location, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		location, err := time.LoadLocation(configured)
		if err != nil {
			return nil, fmt.Errorf("load intelligence timezone: %w", err)
		}
		return location, nil
	}
	if systemLocal == nil {
		return time.Local, nil
	}
	return systemLocal, nil
}

func parseLocalClock(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid local intelligence schedule %q: %w", value, err)
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func (s *Service) EnsureDueIntelligenceJobs(ctx context.Context, now time.Time) (int, error) {
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return 0, err
	}
	if !settings.Enabled {
		return 0, nil
	}
	periods, err := dueIntelligencePeriodsForSettings(now, settings)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, period := range periods {
		timezone := strings.TrimSpace(settings.Timezone)
		if timezone == "" {
			timezone = period.DueAt.Location().String()
		}
		input := map[string]any{"timezone": timezone}
		if period.Kind == intelligenceJobProfileConsolidation {
			input["recent_profile_days"] = settings.RecentProfileDays
			input["stable_min_evidence_days"] = settings.StableMinEvidenceDays
			input["profile_bootstrap_days"] = settings.ProfileBootstrapDays
		}
		_, didCreate, err := s.EnqueueIntelligenceJob(ctx, EnqueueIntelligenceJobInput{Kind: period.Kind, PeriodKey: period.Key,
			PeriodStart: period.Start, PeriodEnd: period.End, DueAt: period.DueAt, Input: input})
		if err != nil {
			return created, err
		}
		if didCreate {
			created++
		}
	}
	return created, nil
}

func intelligenceJobReportCadence(kind string) string {
	if strings.HasPrefix(kind, "report_") {
		return strings.TrimPrefix(kind, "report_")
	}
	return ""
}

func (s *Service) startIntelligenceJobEpisode(ctx context.Context, job domain.StewardIntelligenceJob) (domain.StewardAgentEpisode, error) {
	episodeKey := fmt.Sprintf("intelligence-job:%s:attempt:%d", job.ID, job.Attempts)
	if existingID, existingErr := s.backgroundEpisodeByKey(ctx, episodeKey); existingErr == nil {
		return s.GetAgentEpisodeOverview(ctx, existingID, agentEpisodeOverviewTurnLimit)
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return domain.StewardAgentEpisode{}, existingErr
	}
	if _, ok := s.autonomyAdvisor().(AgentTurnAdvisor); !ok || !s.autonomyAdvisor().Status().Enabled {
		return domain.StewardAgentEpisode{}, fmt.Errorf("configured model does not support background Agent turns")
	}
	prompt := intelligenceJobPrompt(job)
	conversation, err := s.ensureProactiveConversation(ctx)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	trigger, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleSystem, prompt, DataD2,
		s.autonomyAdvisor().Status().Model, episodeKey)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	episode, err := s.enqueueBackgroundAgentEpisode(ctx, conversation, trigger, truncateAdvisorText(job.Kind+":"+job.PeriodKey, 2000), DataD2, job.Kind,
		"intelligence_job", job.ID, episodeKey)
	if err != nil {
		return episode, err
	}
	return episode, nil
}

func intelligenceJobPrompt(job domain.StewardIntelligenceJob) string {
	common := fmt.Sprintf("后台智能任务 %s，job_id=%s，时间范围 %s 至 %s。你可以调用 steward.activity.query、steward.profile.get(view=merged)、steward.profile.explain 和 steward.report.get 获取真实证据。证据必须引用这些工具实际返回的 source_type/source_id，并使用来源真实发生的 evidence_day；服务端会验证来源存在性和日期。不要把计划当作完成，不要伪造证据。",
		job.Kind, job.ID, job.PeriodStart.Format(time.RFC3339), job.PeriodEnd.Format(time.RFC3339))
	if job.Kind == intelligenceJobProfileCorrectionReview {
		factID, _ := job.Input["correction_fact_id"].(string)
		profileKey, _ := job.Input["profile_key"].(string)
		return common + fmt.Sprintf(" 用户已通过专用纠正入口明确修正画像字段 %s（fact_id=%s），该事实优先级最高。先调用 steward.profile.explain 复核该字段历史，并调用 steward.profile.get，参数必须是 {\"view\":\"merged\"}；再读取 reminder policy、近期 reminder feedback 与活动上下文。由模型判断现有提醒策略是否受影响：只有确需调整时才调用 steward.reminder_policy.update，并把 profile_fact:%s 放入 evidence_manifest；无需调整时明确说明保持现状，不要为了完成任务制造策略变更。",
			profileKey, factID, factID)
	}
	if cadence := intelligenceJobReportCadence(job.Kind); cadence != "" {
		if sourceReportID, _ := job.Input["regenerate_report_id"].(string); strings.TrimSpace(sourceReportID) != "" {
			common += fmt.Sprintf(" 这是报告 %s（源修订版 %d）的重新生成任务。先调用 steward.report.get 读取源报告，再结合最新活动、画像和证据生成新的修订版；不要把源报告本身误认成本次任务的结果。",
				strings.TrimSpace(sourceReportID), checkpointInt(job.Input, "source_revision"))
			if reason, _ := job.Input["reason"].(string); strings.TrimSpace(reason) != "" {
				common += " 用户要求重新生成的原因：" + truncateAdvisorText(strings.TrimSpace(reason), 1000) + "。"
			}
		}
		structure := "正文应明确覆盖：一句话概览；主要时间块；完成/推进/搁置/未完成事项；重要对话/承诺/等待回复；工作/学习/娱乐/休息/离开分布；与近期基线的变化；习惯候选变化；管家执行/任务/提醒；异常与数据缺口；下一步关注；证据与覆盖率。没有证据的章节必须标为缺失或推测，不能编造。"
		return common + fmt.Sprintf(" %s 必须为 cadence=%s、period_key=%s 调用 steward.report.write，并原样传入 job_id=%s。即使认为不应通知用户，也要设置 silent=true 并写入报告；silent 不能代替报告。", structure, cadence, job.PeriodKey, job.ID)
	}
	stableDays := checkpointInt(job.Input, "stable_min_evidence_days")
	if stableDays <= 0 {
		stableDays = profileStableEvidenceDays
	}
	return common + fmt.Sprintf(" 请结合近期与长期事实判断画像是否需要更新。recent 可以基于单日证据；stable 必须引用至少 %d 个不同日期的证据；explicit 只能来自用户明确表达。有充分证据的新事实时使用 steward.profile.upsert_fact 写入；没有变化时可以直接说明不更新。", stableDays)
}

func (s *Service) ReconcileIntelligenceJobs(ctx context.Context, now time.Time, limit int) (int, error) {
	limit = normalizeLimit(limit, 16, 100)
	idRows, err := s.db.Pool.Query(ctx, `select j.id::text,e.status from steward_memory_consolidation_runs j
		join steward_agent_episodes e on e.id=j.episode_id where j.status='executing'
		and e.status in ('completed','failed','cancelled','blocked') order by j.updated_at limit $1`, limit)
	if err != nil {
		return 0, err
	}
	defer idRows.Close()
	type idStatus struct{ id, status string }
	pairs := []idStatus{}
	for idRows.Next() {
		var pair idStatus
		if err := idRows.Scan(&pair.id, &pair.status); err != nil {
			return 0, err
		}
		pairs = append(pairs, pair)
	}
	if err := idRows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, pair := range pairs {
		job, err := s.GetIntelligenceJob(ctx, pair.id)
		if err != nil {
			return count, err
		}
		status := intelligenceJobCompleted
		if pair.status != agentEpisodeCompleted {
			status = intelligenceJobWaitingModel
		}
		var reportID string
		if cadence := intelligenceJobReportCadence(job.Kind); cadence != "" {
			_, regeneration := job.Input["regenerate_report_id"].(string)
			var persistedReportStatus string
			err = s.db.Pool.QueryRow(ctx, `select id::text,status from steward_reports where profile_scope='default'
				and status in ('complete','partial') and (
					idempotency_key like $1 or
					($2<>'' and episode_id::text=$2) or
					(not $5 and cadence=$3 and period_key=$4)
				) order by case status when 'complete' then 0 else 1 end,
				case when idempotency_key like $1 then 0 when $2<>'' and episode_id::text=$2 then 1 else 2 end,revision desc limit 1`, "job:"+job.ID+":%",
				stringValue(job.EpisodeID), cadence, job.PeriodKey, regeneration).Scan(&reportID, &persistedReportStatus)
			if errors.Is(err, pgx.ErrNoRows) {
				report, partialErr := s.EnsurePartialReport(ctx, job, fmt.Errorf("episode ended without steward.report.write"))
				if partialErr != nil {
					return count, partialErr
				}
				reportID, status = report.ID, intelligenceJobWaitingModel
			} else if err != nil {
				return count, err
			} else {
				if persistedReportStatus == reportStatusPartial {
					status = intelligenceJobWaitingModel
				} else {
					status = intelligenceJobCompleted
				}
			}
		} else if pair.status == agentEpisodeCompleted {
			recentDays := checkpointInt(job.Input, "recent_profile_days")
			if recentDays <= 0 {
				recentDays = 14
			}
			if _, err = s.RebuildProfileSnapshots(ctx, now, recentDays, job.ID); err != nil {
				return count, err
			}
		}
		next := now.UTC()
		if status == intelligenceJobWaitingModel {
			next = next.Add(intelligenceRetryDelay(job.Attempts))
		}
		if status == intelligenceJobWaitingModel && job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
			status = intelligenceJobFailed
		}
		completedAt := any(nil)
		if status == intelligenceJobCompleted || status == intelligenceJobFailed {
			completedAt = now.UTC()
		}
		tag, updateErr := s.db.Pool.Exec(ctx, `update steward_memory_consolidation_runs set status=$2,
			checkpoint=case when $3='' then checkpoint else jsonb_set(checkpoint,'{report_id}',to_jsonb($3::text),true) end,
			error_summary=case when $2='waiting_model' then $4 else error_summary end,next_attempt_at=$5,updated_at=$6,completed_at=$7,
			control_generation=control_generation+1,lease_owner='',lease_expires_at=null where id=$1 and status='executing' and control_generation=$8`,
			job.ID, status, reportID, "linked episode ended with "+pair.status, next, now.UTC(), completedAt, job.ControlGeneration)
		if updateErr != nil {
			return count, updateErr
		}
		if tag.RowsAffected() == 1 {
			count++
		}
	}
	return count, nil
}

func (s *Service) RunProfileReportController(ctx context.Context, now time.Time, workerID string, limit int) (ProfileReportControllerResult, error) {
	result := ProfileReportControllerResult{}
	if _, err := s.ReconcileReportNotifications(ctx, now, limit); err != nil {
		return result, err
	}
	created, err := s.EnsureDueIntelligenceJobs(ctx, now)
	if err != nil {
		return result, err
	}
	result.Scheduled = created
	reconciled, err := s.ReconcileIntelligenceJobs(ctx, now, limit)
	if err != nil {
		return result, err
	}
	result.Reconciled = reconciled
	jobs, err := s.ClaimIntelligenceJobs(ctx, workerID, now, 2*time.Minute, limit)
	if err != nil {
		return result, err
	}
	for _, job := range jobs {
		episode, startErr := s.startIntelligenceJobEpisode(ctx, job)
		if startErr != nil {
			if intelligenceJobReportCadence(job.Kind) != "" {
				if _, partialErr := s.EnsurePartialReport(ctx, job, startErr); partialErr == nil {
					result.Partial++
				}
			}
			if deferErr := s.DeferIntelligenceJob(ctx, job, startErr); deferErr != nil && !errors.Is(deferErr, errAgentEpisodeClaimLost) {
				return result, deferErr
			}
			result.Deferred++
			continue
		}
		if err := s.LinkIntelligenceJobEpisode(ctx, job, episode.ID); err != nil {
			return result, err
		}
		result.Started++
	}
	return result, nil
}

func evidenceHash(sourceType, sourceID, summary string) string {
	digest := sha256.Sum256([]byte(sourceType + "\x00" + sourceID + "\x00" + summary))
	return hex.EncodeToString(digest[:])
}
