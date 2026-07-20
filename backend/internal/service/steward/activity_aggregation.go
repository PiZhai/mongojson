package steward

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

type pendingObservation struct {
	ID                   string
	Source               string
	Type                 string
	Summary              string
	DataLevel            string
	DeviceID             string
	ContextKey           string
	InteractiveSessionID string
	SourceRevision       int64
	DuplicateCount       int
	OccurredAt           time.Time
	EndedAt              *time.Time
}

type activityScope struct {
	deviceID             string
	interactiveSessionID string
}

type activitySpan struct {
	start time.Time
	end   time.Time
}

type activityAFKIndex map[activityScope][]activitySpan

func (s *Service) AggregateActivitySessions(ctx context.Context, limit int) (int, error) {
	limit = normalizeLimit(limit, 1000, 5000)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, source, type, summary, data_level, device_id, context_key,
		       interactive_session_id, source_revision, duplicate_count, occurred_at, ended_at
		from steward_observations
		where session_id is null and status = 'active'
		  and coalesce(ingested_at,created_at) <= now() - interval '30 seconds'
		order by occurred_at asc limit $1
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("list pending activity observations: %w", err)
	}
	items := []pendingObservation{}
	for rows.Next() {
		var item pendingObservation
		if err := rows.Scan(&item.ID, &item.Source, &item.Type, &item.Summary, &item.DataLevel,
			&item.DeviceID, &item.ContextKey, &item.InteractiveSessionID, &item.SourceRevision,
			&item.DuplicateCount, &item.OccurredAt, &item.EndedAt); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	afks := buildActivityAFKIndex(items)
	groups := groupPendingObservations(items)
	created := 0
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		ok, err := s.persistActivitySession(ctx, group, afks)
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	return created, nil
}

func groupPendingObservations(items []pendingObservation) [][]pendingObservation {
	groups := [][]pendingObservation{}
	for _, item := range items {
		matched := -1
		for index := len(groups) - 1; index >= 0; index-- {
			group := groups[index]
			previous := group[len(group)-1]
			if item.OccurredAt.Sub(observationEnd(previous)) > heartbeatMergeWindow {
				break
			}
			if observationsShareSession(previous, item) && observationsHaveCompatibleContext(previous, item) {
				matched = index
				break
			}
		}
		if matched >= 0 {
			groups[matched] = append(groups[matched], item)
		} else {
			groups = append(groups, []pendingObservation{item})
		}
	}
	return groups
}

func observationsShareSession(left, right pendingObservation) bool {
	if left.DeviceID != right.DeviceID {
		return false
	}
	if left.InteractiveSessionID != "" && right.InteractiveSessionID != "" && left.InteractiveSessionID != right.InteractiveSessionID {
		return false
	}
	return true
}

func observationsHaveCompatibleContext(left, right pendingObservation) bool {
	leftAFK, rightAFK := isAFKObservation(left), isAFKObservation(right)
	if leftAFK || rightAFK {
		return leftAFK && rightAFK && strings.EqualFold(left.ContextKey, right.ContextKey)
	}
	if strings.EqualFold(strings.TrimSpace(left.ContextKey), strings.TrimSpace(right.ContextKey)) {
		return true
	}
	leftApp := canonicalActivityContext(left)
	rightApp := canonicalActivityContext(right)
	if leftApp != "" && leftApp == rightApp {
		return true
	}
	// ActivityWatch web events and the native browser foreground heartbeat
	// describe the same activity from different sources. Temporal overlap is
	// enough to combine those pieces of evidence in one session.
	return observationIntervalsOverlap(left, right) &&
		((isWebObservation(left) && isForegroundObservation(right)) ||
			(isWebObservation(right) && isForegroundObservation(left)))
}

func observationEnd(item pendingObservation) time.Time {
	if item.EndedAt != nil && item.EndedAt.After(item.OccurredAt) {
		return item.EndedAt.UTC()
	}
	return item.OccurredAt
}

func observationIntervalsOverlap(left, right pendingObservation) bool {
	return !observationEnd(left).Before(right.OccurredAt) && !observationEnd(right).Before(left.OccurredAt)
}

func isAFKObservation(item pendingObservation) bool {
	return strings.Contains(strings.ToLower(item.Type), "afk")
}

func isWebObservation(item pendingObservation) bool {
	value := strings.ToLower(item.Type + " " + item.Source)
	return strings.Contains(value, "web") || strings.Contains(value, "browser")
}

func isForegroundObservation(item pendingObservation) bool {
	value := strings.ToLower(item.Type)
	return strings.Contains(value, "window") || strings.Contains(value, "foreground")
}

func canonicalActivityContext(item pendingObservation) string {
	value := strings.ToLower(strings.TrimSpace(item.ContextKey))
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "|")
	return strings.TrimSpace(parts[0])
}

func (s *Service) persistActivitySession(ctx context.Context, group []pendingObservation, afks activityAFKIndex) (bool, error) {
	first := group[0]
	last := group[len(group)-1]
	endedAt := last.OccurredAt
	if last.EndedAt != nil {
		endedAt = *last.EndedAt
	}
	dataLevel := first.DataLevel
	observationCount := 0
	sources := map[string]bool{}
	summaries := []string{}
	seenSummaries := map[string]bool{}
	for _, item := range group {
		sources[item.Source] = true
		observationCount += item.DuplicateCount
		if dataLevelRank(item.DataLevel) > dataLevelRank(dataLevel) {
			dataLevel = item.DataLevel
		}
		if item.Summary != "" && !seenSummaries[item.Summary] && len(summaries) < 5 {
			seenSummaries[item.Summary] = true
			summaries = append(summaries, item.Summary)
		}
	}
	canonicalContext := canonicalActivityContext(first)
	title := strings.TrimSpace(first.ContextKey)
	if title == "" {
		title = strings.ReplaceAll(first.Type, "_", " ")
	}
	summary := strings.Join(summaries, "；")
	confidence := clamp01(0.55 + float64(minInt(observationCount, 9))*0.05)
	valueScore := CalculateStewardValue(StewardValueSignals{
		Actionability: 0.35, Recurrence: clamp01(float64(observationCount) / 10), Uniqueness: 0.55,
		Confidence: confidence, CrossSource: 0.2, Recency: 1, Redundancy: 0.1,
		SensitivityCost: sensitivityCost(dataLevel),
	})
	now := time.Now().UTC()
	activeSeconds, afkSeconds := activityDurations(group, afks)
	boundaryKind := "context_change"
	if isAFKObservation(first) {
		boundaryKind = strings.ToLower(strings.TrimSpace(first.ContextKey))
	}
	source := first.Source
	if len(sources) > 1 {
		source = "multi-source"
	}
	sessionID, timelineID := uuid.NewString(), uuid.NewString()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `
		insert into steward_activity_sessions (
			id, type, title, summary, source, context_key, device_id, data_level, status,
			observation_count, confidence, value_score, started_at, ended_at, timeline_id,
			created_at, updated_at, canonical_context, interactive_session_id, active_seconds,
			afk_seconds, source_count, last_event_at, boundary_kind
		) values ($1,$2,$3,$4,$5,$6,$7,$8,'closed',$9,$10,$11,$12,$13,null,$14,$14,$15,$16,$17,$18,$19,$20,$21)
	`, sessionID, first.Type, title, summary, source, first.ContextKey, first.DeviceID,
		dataLevel, observationCount, confidence, valueScore, first.OccurredAt, endedAt, now,
		canonicalContext, first.InteractiveSessionID, activeSeconds, afkSeconds, len(sources), endedAt, boundaryKind)
	if err != nil {
		return false, fmt.Errorf("create activity session: %w", err)
	}
	if result.RowsAffected() == 0 {
		return false, nil
	}
	_, err = tx.Exec(ctx, `
		insert into steward_timeline_segments (
			id, type, title, summary, status, source, data_level, permission_level, device_id,
			start_at, end_at, confidence, user_confirmed, version, created_at, updated_at,
			valid_from, inference_status, evidence_count, last_verified_at
		) values ($1,'activity_session',$2,$3,'active','activity-aggregator',$4,$5,$6,$7,$8,$9,false,1,$10,$10,$7,'derived',$11,$10)
	`, timelineID, title, summary, dataLevel, PermissionA1, first.DeviceID, first.OccurredAt,
		endedAt, confidence, now, len(group))
	if err != nil {
		return false, fmt.Errorf("create activity timeline: %w", err)
	}
	if _, err := tx.Exec(ctx, `update steward_activity_sessions set timeline_id=$2 where id=$1`, sessionID, timelineID); err != nil {
		return false, fmt.Errorf("link activity timeline: %w", err)
	}
	for ordinal, item := range group {
		result, err := tx.Exec(ctx, `
			update steward_observations set session_id = $1, status = 'aggregated'
			where id = $2 and occurred_at = $3 and session_id is null
		`, sessionID, item.ID, item.OccurredAt)
		if err != nil {
			return false, err
		}
		if result.RowsAffected() == 0 {
			return false, nil
		}
		itemActiveSeconds := observationActiveSeconds(item, afks)
		_, err = tx.Exec(ctx, `
			insert into steward_activity_session_items (
				id,session_id,observation_id,observation_time,source,role,active_seconds,ordinal,snapshot_hash
			) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			on conflict(session_id,observation_id,observation_time) do nothing
		`, uuid.NewString(), sessionID, item.ID, item.OccurredAt, item.Source,
			activityObservationRole(item), itemActiveSeconds, ordinal, fmt.Sprintf("%s:%d", item.ID, item.SourceRevision))
		if err != nil {
			return false, err
		}
		if len(group) <= 20 {
			_, err = tx.Exec(ctx, `
				insert into steward_source_refs (
					id, target_type, target_id, source_type, source_id, summary,
					confidence, sensitive, displayable, created_at
				) values ($1,'timeline_segment',$2,'observation',$3,$4,$5,$6,true,$7)
			`, uuid.NewString(), timelineID, item.ID, item.Type, confidence,
				dataLevelRank(item.DataLevel) >= dataLevelRank(DataD4), now)
			if err != nil {
				return false, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	s.storeActivitySessionEmbedding(ctx, sessionID, title+" "+summary)
	activityEntity, err := s.upsertEntity(ctx, "activity", sessionID, title, summary, dataLevel, confidence, first.OccurredAt)
	if err != nil {
		return false, err
	}
	deviceEntity, err := s.upsertEntity(ctx, "device", first.DeviceID, first.DeviceID, "活动设备", DataD2, 1, first.OccurredAt)
	if err != nil {
		return false, err
	}
	_, err = s.upsertRelationWithObservation(ctx, activityEntity.ID, deviceEntity.ID, "occurred_in",
		dataLevel, false, first.ID, first.OccurredAt, "活动会话设备来源", confidence)
	return true, err
}

func activityObservationRole(item pendingObservation) string {
	if isAFKObservation(item) {
		return "boundary"
	}
	return "evidence"
}

func activityScopeFor(item pendingObservation) activityScope {
	return activityScope{
		deviceID:             strings.TrimSpace(item.DeviceID),
		interactiveSessionID: strings.TrimSpace(item.InteractiveSessionID),
	}
}

func buildActivityAFKIndex(items []pendingObservation) activityAFKIndex {
	index := activityAFKIndex{}
	for _, item := range items {
		if !isAFKObservation(item) || !strings.EqualFold(strings.TrimSpace(item.ContextKey), "afk") {
			continue
		}
		end := observationEnd(item)
		if !end.After(item.OccurredAt) {
			continue
		}
		scope := activityScopeFor(item)
		index[scope] = append(index[scope], activitySpan{start: item.OccurredAt, end: end})
	}
	for scope, spans := range index {
		index[scope] = mergeActivitySpans(spans)
	}
	return index
}

func mergeActivitySpans(spans []activitySpan) []activitySpan {
	if len(spans) == 0 {
		return nil
	}
	values := append([]activitySpan(nil), spans...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].start.Equal(values[j].start) {
			return values[i].end.Before(values[j].end)
		}
		return values[i].start.Before(values[j].start)
	})
	merged := make([]activitySpan, 0, len(values))
	for _, value := range values {
		if !value.end.After(value.start) {
			continue
		}
		if len(merged) == 0 || value.start.After(merged[len(merged)-1].end) {
			merged = append(merged, value)
			continue
		}
		if value.end.After(merged[len(merged)-1].end) {
			merged[len(merged)-1].end = value.end
		}
	}
	return merged
}

func subtractActivitySpans(value activitySpan, excluded []activitySpan) []activitySpan {
	if !value.end.After(value.start) {
		return nil
	}
	remaining := make([]activitySpan, 0, len(excluded)+1)
	cursor := value.start
	for _, blocker := range excluded {
		if !blocker.end.After(cursor) {
			continue
		}
		if !blocker.start.Before(value.end) {
			break
		}
		if blocker.start.After(cursor) {
			end := blocker.start
			if end.After(value.end) {
				end = value.end
			}
			if end.After(cursor) {
				remaining = append(remaining, activitySpan{start: cursor, end: end})
			}
		}
		if blocker.end.After(cursor) {
			cursor = blocker.end
		}
		if !cursor.Before(value.end) {
			return remaining
		}
	}
	if cursor.Before(value.end) {
		remaining = append(remaining, activitySpan{start: cursor, end: value.end})
	}
	return remaining
}

func activitySpanSeconds(spans []activitySpan) float64 {
	var total time.Duration
	for _, span := range mergeActivitySpans(spans) {
		total += span.end.Sub(span.start)
	}
	if total < 0 {
		return 0
	}
	return total.Seconds()
}

func observationActiveSeconds(item pendingObservation, afks activityAFKIndex) float64 {
	if isAFKObservation(item) {
		return 0
	}
	span := activitySpan{start: item.OccurredAt, end: observationEnd(item)}
	return activitySpanSeconds(subtractActivitySpans(span, afks[activityScopeFor(item)]))
}

func activityDurations(group []pendingObservation, afks activityAFKIndex) (activeSeconds, afkSeconds float64) {
	active, afk := []activitySpan{}, []activitySpan{}
	for _, item := range group {
		end := observationEnd(item)
		if !end.After(item.OccurredAt) {
			continue
		}
		if isAFKObservation(item) && strings.EqualFold(strings.TrimSpace(item.ContextKey), "afk") {
			afk = append(afk, activitySpan{start: item.OccurredAt, end: end})
		} else if isAFKObservation(item) {
			continue
		} else {
			span := activitySpan{start: item.OccurredAt, end: end}
			active = append(active, subtractActivitySpans(span, afks[activityScopeFor(item)])...)
		}
	}
	return activitySpanSeconds(active), activitySpanSeconds(afk)
}

func (s *Service) EvaluateHabitsAndInsights(ctx context.Context, now time.Time) (map[string]int, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select context_key, min(title), min(summary), max(data_level), count(*),
		       count(distinct source), max(ended_at)
		from steward_activity_sessions
		where context_key <> '' and started_at >= $1
		group by context_key having count(*) >= 3
		order by count(*) desc limit 200
	`, now.AddDate(0, 0, -30))
	if err != nil {
		return nil, err
	}
	type candidate struct {
		pattern, title, summary, dataLevel string
		count, sources                     int
		last                               time.Time
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.pattern, &item.title, &item.summary, &item.dataLevel,
			&item.count, &item.sources, &item.last); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	counts := map[string]int{"habits_updated": 0, "insights_updated": 0, "memories_proposed": 0}
	for _, item := range candidates {
		confidence := clamp01(0.4 + float64(item.count)*0.04)
		value := CalculateStewardValue(StewardValueSignals{
			UserUse: 0.1, Actionability: 0.55, Recurrence: clamp01(float64(item.count) / 10),
			Uniqueness: 0.65, Confidence: confidence, CrossSource: clamp01(float64(item.sources) / 3),
			Recency: recencyScore(item.last, now, 30*24*time.Hour), Redundancy: 0.1,
			SensitivityCost: sensitivityCost(item.dataLevel),
		})
		habitID, err := s.upsertHabitCandidate(ctx, item.pattern, item.title, item.summary, item.dataLevel,
			item.count, confidence, value, item.last, now)
		if err != nil {
			return counts, err
		}
		counts["habits_updated"]++
		if item.count >= 8 {
			if err := s.upsertInsightCandidate(ctx, "automation_opportunity", "可自动化："+item.title,
				fmt.Sprintf("近 30 天重复 %d 次，可先模拟自动化规则。", item.count), "生成低风险自动化草案",
				item.dataLevel, item.count, confidence, value, now); err != nil {
				return counts, err
			}
			counts["insights_updated"]++
		}
		if value >= 0.70 {
			created, err := s.ensureHabitMemoryCandidate(ctx, habitID, item.title, item.summary, item.dataLevel,
				confidence, item.count, now)
			if err != nil {
				return counts, err
			}
			if created {
				counts["memories_proposed"]++
			}
		}
	}
	return counts, nil
}

func (s *Service) upsertHabitCandidate(ctx context.Context, pattern, title, summary, dataLevel string, evidenceCount int, confidence, value float64, lastEvidence, now time.Time) (string, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `select id::text from steward_habits where type = 'repeated_activity' and pattern = $1 limit 1`, pattern).Scan(&id)
	if err == nil {
		_, err = s.db.Pool.Exec(ctx, `
			update steward_habits set title=$2, summary=$3, data_level=$4, confidence=$5,
			       evidence_count=$6, value_score=$7, last_evidence_at=$8,
			       status=case when status='quarantined' then 'candidate' else status end,
			       quarantined_at=null, updated_at=$9 where id=$1
		`, id, title, summary, dataLevel, confidence, evidenceCount, value, lastEvidence, now)
		return id, err
	}
	id = uuid.NewString()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_habits (
			id, type, title, summary, pattern, status, data_level, confidence, evidence_count,
			value_score, user_confirmed, retention_locked, last_evidence_at, created_at, updated_at
		) values ($1,'repeated_activity',$2,$3,$4,'candidate',$5,$6,$7,$8,false,false,$9,$10,$10)
	`, id, title, summary, pattern, dataLevel, confidence, evidenceCount, value, lastEvidence, now)
	return id, err
}

func (s *Service) upsertInsightCandidate(ctx context.Context, insightType, title, summary, action, dataLevel string, evidenceCount int, confidence, value float64, now time.Time) error {
	var id string
	err := s.db.Pool.QueryRow(ctx, `select id::text from steward_insights where type=$1 and title=$2 limit 1`, insightType, title).Scan(&id)
	if err == nil {
		_, err = s.db.Pool.Exec(ctx, `
			update steward_insights set summary=$2, suggested_action=$3, data_level=$4,
			       confidence=$5, evidence_count=$6, value_score=$7,
			       status=case when status='quarantined' then 'candidate' else status end,
			       quarantined_at=null, updated_at=$8 where id=$1
		`, id, summary, action, dataLevel, confidence, evidenceCount, value, now)
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_insights (
			id, type, title, summary, suggested_action, status, data_level, confidence,
			evidence_count, value_score, user_confirmed, retention_locked, created_at, updated_at
		) values ($1,$2,$3,$4,$5,'candidate',$6,$7,$8,$9,false,false,$10,$10)
	`, uuid.NewString(), insightType, title, summary, action, dataLevel, confidence, evidenceCount, value, now)
	return err
}

func (s *Service) ensureHabitMemoryCandidate(ctx context.Context, habitID, title, summary, dataLevel string, confidence float64, evidenceCount int, now time.Time) (bool, error) {
	var exists bool
	err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_memories where source='habit-engine' and title=$1 and deleted_at is null)`, title).Scan(&exists)
	if err != nil || exists {
		return false, err
	}
	memoryID := uuid.NewString()
	confirmed, syncable := false, false
	auditID, err := s.recordAudit(ctx, AuditInput{Actor: "system", Action: "memory.candidate.create", TargetType: "memory", TargetID: &memoryID,
		Source: "habit-engine", PermissionLevel: PermissionA1, DataLevel: dataLevel, InputSummary: "habit candidate",
		OutputSummary: "unconfirmed memory candidate created", UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK})
	if err != nil {
		return false, err
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_memories (
			id, type, title, summary, content, scope, status, source, data_level, permission_level,
			device_id, confidence, user_confirmed, version, last_verified_at, audit_id, created_at,
			updated_at, valid_from, inference_status, evidence_count
		) values ($1,'habit_candidate',$2,$3,$3,'global','candidate','habit-engine',$4,$5,$6,$7,false,1,$8,$9,$8,$8,$8,'candidate',$10)
	`, memoryID, title, summary, dataLevel, PermissionA1, s.agentIDValue(), confidence, now, auditID, evidenceCount)
	if err != nil {
		return false, err
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_source_refs (
			id, target_type, target_id, source_type, source_id, summary, confidence,
			sensitive, displayable, created_at
		) values ($1,'memory',$2,'habit',$3,'习惯证据候选',$4,$5,true,$6)
	`, uuid.NewString(), memoryID, habitID, confidence, dataLevelRank(dataLevel) >= dataLevelRank(DataD4), now)
	return err == nil, err
}

func (s *Service) ListHabits(ctx context.Context, limit int) ([]domain.StewardHabit, error) {
	limit = normalizeLimit(limit, 100, 500)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, entity_id::text, type, title, summary, pattern, status, data_level,
		       confidence, evidence_count, value_score, user_confirmed, retention_locked,
		       last_evidence_at, quarantined_at, created_at, updated_at
		from steward_habits where status <> 'deleted' order by value_score desc, updated_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardHabit{}
	for rows.Next() {
		var item domain.StewardHabit
		if err := rows.Scan(&item.ID, &item.EntityID, &item.Type, &item.Title, &item.Summary,
			&item.Pattern, &item.Status, &item.DataLevel, &item.Confidence, &item.EvidenceCount,
			&item.ValueScore, &item.UserConfirmed, &item.RetentionLocked, &item.LastEvidenceAt,
			&item.QuarantinedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListInsights(ctx context.Context, limit int) ([]domain.StewardInsight, error) {
	limit = normalizeLimit(limit, 100, 500)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, type, title, summary, suggested_action, status, data_level,
		       confidence, evidence_count, value_score, user_confirmed, retention_locked,
		       quarantined_at, created_at, updated_at
		from steward_insights where status <> 'deleted' order by value_score desc, updated_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardInsight{}
	for rows.Next() {
		var item domain.StewardInsight
		if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.Summary, &item.SuggestedAction,
			&item.Status, &item.DataLevel, &item.Confidence, &item.EvidenceCount, &item.ValueScore,
			&item.UserConfirmed, &item.RetentionLocked, &item.QuarantinedAt, &item.CreatedAt,
			&item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpdateHabit(ctx context.Context, id string, input UpdateInferenceInput) (domain.StewardHabit, error) {
	if err := validateInferenceUpdate(input); err != nil {
		return domain.StewardHabit{}, err
	}
	var item domain.StewardHabit
	err := s.db.Pool.QueryRow(ctx, `
		update steward_habits set status=coalesce($2,status), title=coalesce($3,title),
		       summary=coalesce($4,summary), user_confirmed=coalesce($5,user_confirmed),
		       retention_locked=case when coalesce($5,false) then true else retention_locked end,
		       quarantined_at=case when coalesce($2,status)='quarantined' then coalesce(quarantined_at,now()) else null end,
		       updated_at=now() where id=$1
		returning id::text, entity_id::text, type, title, summary, pattern, status, data_level,
		          confidence, evidence_count, value_score, user_confirmed, retention_locked,
		          last_evidence_at, quarantined_at, created_at, updated_at
	`, id, input.Status, input.Title, input.Summary, input.UserConfirmed).Scan(&item.ID, &item.EntityID,
		&item.Type, &item.Title, &item.Summary, &item.Pattern, &item.Status, &item.DataLevel,
		&item.Confidence, &item.EvidenceCount, &item.ValueScore, &item.UserConfirmed,
		&item.RetentionLocked, &item.LastEvidenceAt, &item.QuarantinedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.StewardHabit{}, err
	}
	s.recordInferenceDecision(ctx, "habit", id, item.Status)
	return item, nil
}

func (s *Service) UpdateInsight(ctx context.Context, id string, input UpdateInferenceInput) (domain.StewardInsight, error) {
	if err := validateInferenceUpdate(input); err != nil {
		return domain.StewardInsight{}, err
	}
	var item domain.StewardInsight
	err := s.db.Pool.QueryRow(ctx, `
		update steward_insights set status=coalesce($2,status), title=coalesce($3,title),
		       summary=coalesce($4,summary), user_confirmed=coalesce($5,user_confirmed),
		       retention_locked=case when coalesce($5,false) then true else retention_locked end,
		       quarantined_at=case when coalesce($2,status)='quarantined' then coalesce(quarantined_at,now()) else null end,
		       updated_at=now() where id=$1
		returning id::text, type, title, summary, suggested_action, status, data_level,
		          confidence, evidence_count, value_score, user_confirmed, retention_locked,
		          quarantined_at, created_at, updated_at
	`, id, input.Status, input.Title, input.Summary, input.UserConfirmed).Scan(&item.ID, &item.Type,
		&item.Title, &item.Summary, &item.SuggestedAction, &item.Status, &item.DataLevel,
		&item.Confidence, &item.EvidenceCount, &item.ValueScore, &item.UserConfirmed,
		&item.RetentionLocked, &item.QuarantinedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.StewardInsight{}, err
	}
	s.recordInferenceDecision(ctx, "insight", id, item.Status)
	return item, nil
}

func validateInferenceUpdate(input UpdateInferenceInput) error {
	if input.Status == nil {
		return nil
	}
	switch *input.Status {
	case "candidate", "confirmed", "ignored", "active", "quarantined", "archived":
		return nil
	default:
		return fmt.Errorf("unsupported inference status %q", *input.Status)
	}
}

func (s *Service) recordInferenceDecision(ctx context.Context, targetType, id, status string) {
	confirmed, syncable := true, false
	_, _ = s.recordAudit(ctx, AuditInput{Actor: "user", Action: targetType + ".decision", TargetType: targetType,
		TargetID: &id, Source: "lifecycle-workbench", PermissionLevel: PermissionA3, DataLevel: DataD0,
		InputSummary: "inference decision", OutputSummary: status, UserConfirmed: &confirmed,
		Syncable: &syncable, ResultStatus: ResultOK})
}

func recencyScore(when, now time.Time, window time.Duration) float64 {
	if when.After(now) {
		return 1
	}
	return clamp01(1 - now.Sub(when).Hours()/window.Hours())
}

func sensitivityCost(dataLevel string) float64 {
	switch dataLevel {
	case DataD4, DataD6:
		return 1
	case DataD3:
		return 0.5
	case DataD2:
		return 0.2
	default:
		return 0
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
