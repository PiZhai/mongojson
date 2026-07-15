package steward

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

type EvaluateLifecycleInput struct {
	Limit int `json:"limit,omitempty"`
}

type PurgeLifecycleInput struct {
	EvaluationID string `json:"evaluation_id"`
	Execute      bool   `json:"execute"`
	Automatic    bool   `json:"-"`
}

func (s *Service) GetLifecycleStatus(ctx context.Context) (domain.StewardLifecycleStatus, error) {
	policies, err := s.ListRetentionPolicies(ctx)
	if err != nil {
		return domain.StewardLifecycleStatus{}, err
	}
	status := domain.StewardLifecycleStatus{
		Profile: captureProfile(), RetentionPolicies: policies, LastRuns: map[string]*time.Time{},
		UpdatedAt: time.Now().UTC(),
	}
	_, status.LocalEncryptionReady, _ = localPayloadCipherFromEnv()
	_ = s.db.Pool.QueryRow(ctx, `select exists(select 1 from pg_extension where extname='vector')`).Scan(&status.VectorSearchEnabled)
	layerQueries := []struct {
		kind  string
		query string
	}{
		{kind: "raw_evidence", query: `
			select count(*), coalesce((select sum(size_bytes) from steward_encrypted_blobs),0),
			       count(*) filter (where expires_at is not null and expires_at <= now()), 0
			from steward_observations`},
		{kind: "activity_facts", query: `select count(*), 0, 0, 0 from steward_activity_sessions`},
		{kind: "inferences", query: `
			select (select count(*) from steward_habits) + (select count(*) from steward_insights), 0, 0,
			       (select count(*) from steward_habits where status='quarantined') +
			       (select count(*) from steward_insights where status='quarantined')`},
		{kind: "long_term_assets", query: `
			select (select count(*) from steward_memories where deleted_at is null) +
			       (select count(*) from steward_knowledge_items where deleted_at is null) +
			       (select count(*) from steward_tasks where deleted_at is null), 0, 0, 0`},
		{kind: "audit", query: `select count(*), 0, 0, 0 from steward_audit_logs`},
		{kind: "deletion_tombstones", query: `select count(*), 0, count(*) filter (where expires_at <= now()), 0 from steward_deletion_tombstones`},
	}
	for _, query := range layerQueries {
		layer := domain.StewardLifecycleLayerStatus{Kind: query.kind}
		if err := s.db.Pool.QueryRow(ctx, query.query).Scan(&layer.Count, &layer.Bytes, &layer.ExpiredCount, &layer.QuarantinedCount); err != nil {
			return domain.StewardLifecycleStatus{}, fmt.Errorf("read lifecycle layer %s: %w", query.kind, err)
		}
		status.Layers = append(status.Layers, layer)
	}
	_ = s.db.Pool.QueryRow(ctx, `select min(expires_at) from steward_observations where expires_at > now()`).Scan(&status.NextExpiringAt)
	rows, err := s.db.Pool.Query(ctx, `
		select distinct on (job_type) job_type, completed_at
		from steward_lifecycle_runs where status='success'
		order by job_type, completed_at desc
	`)
	if err != nil {
		return domain.StewardLifecycleStatus{}, err
	}
	for rows.Next() {
		var name string
		var completed *time.Time
		if err := rows.Scan(&name, &completed); err != nil {
			rows.Close()
			return domain.StewardLifecycleStatus{}, err
		}
		status.LastRuns[name] = completed
	}
	rows.Close()
	return status, nil
}

func captureProfile() string {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("STEWARD_CAPTURE_PROFILE"))); value == "deep" || value == "light" {
		return value
	}
	if runtime.GOOS == "linux" {
		return "light"
	}
	return "deep"
}

func (s *Service) EvaluateLifecycle(ctx context.Context, input EvaluateLifecycleInput) (domain.StewardLifecycleEvaluation, error) {
	limit := normalizeLimit(input.Limit, 1000, 5000)
	now := time.Now().UTC()
	evaluation := domain.StewardLifecycleEvaluation{
		ID: uuid.NewString(), DryRun: true, EvaluatedAt: now, Actions: []domain.StewardLifecycleAction{}, Counts: map[string]int{},
	}
	rows, err := s.db.Pool.Query(ctx, `
		select o.id::text, o.expires_at, coalesce(p.require_preview,false)
		from steward_observations o
		left join lateral (
			select require_preview from steward_retention_policies p
			where p.auto_purge=true and p.data_kind in (o.type,'observation')
			  and o.source like replace(p.source_pattern,'*','%')
			order by case when p.data_kind=o.type then 0 else 1 end,
			         case when p.source_pattern='*' then 1 else 0 end limit 1
		) p on true
		where o.expires_at is not null and o.expires_at <= $1 and o.retention_locked=false
		  and p.require_preview is not null
		order by o.expires_at limit $2
	`, now, limit)
	if err != nil {
		return evaluation, err
	}
	for rows.Next() {
		var id string
		var expiry *time.Time
		var preview bool
		if err := rows.Scan(&id, &expiry, &preview); err != nil {
			rows.Close()
			return evaluation, err
		}
		evaluation.Actions = append(evaluation.Actions, domain.StewardLifecycleAction{
			TargetType: "observation", TargetID: id, Action: "delete_observation",
			Reason: "原始证据 TTL 已到期", RequiresPreview: preview,
		})
	}
	rows.Close()
	if len(evaluation.Actions) < limit {
		remaining := limit - len(evaluation.Actions)
		if err := s.appendInferenceLifecycleActions(ctx, &evaluation, "habit", "steward_habits", remaining, now); err != nil {
			return evaluation, err
		}
	}
	if len(evaluation.Actions) < limit {
		remaining := limit - len(evaluation.Actions)
		if err := s.appendInferenceLifecycleActions(ctx, &evaluation, "insight", "steward_insights", remaining, now); err != nil {
			return evaluation, err
		}
	}
	if len(evaluation.Actions) < limit {
		rows, err := s.db.Pool.Query(ctx, `
			select b.id::text from steward_encrypted_blobs b
			where not exists (
				select 1 from steward_observations o
				where o.id=b.observation_id and o.occurred_at=b.observation_time
			) limit $1
		`, limit-len(evaluation.Actions))
		if err != nil {
			return evaluation, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return evaluation, err
			}
			evaluation.Actions = append(evaluation.Actions, domain.StewardLifecycleAction{
				TargetType: "encrypted_blob", TargetID: id, Action: "delete_orphan_blob", Reason: "媒体文件没有任何原始观察引用",
			})
		}
		rows.Close()
	}
	for _, action := range evaluation.Actions {
		evaluation.Counts[action.Action]++
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_lifecycle_runs (id, job_type, status, dry_run, action_counts, started_at, completed_at)
		values ($1,'evaluate','success',true,$2,$3,$3)
	`, evaluation.ID, marshalJSON(evaluation.Counts), now)
	if err != nil {
		return evaluation, err
	}
	return evaluation, nil
}

func (s *Service) appendInferenceLifecycleActions(ctx context.Context, evaluation *domain.StewardLifecycleEvaluation, targetType, table string, limit int, now time.Time) error {
	if limit <= 0 {
		return nil
	}
	var autoPurge, requirePreview, protectReferenced bool
	err := s.db.Pool.QueryRow(ctx, `
		select auto_purge,require_preview,protect_referenced
		from steward_retention_policies where data_kind='inference'
		order by case when source_pattern='*' then 0 else 1 end limit 1
	`).Scan(&autoPurge, &requirePreview, &protectReferenced)
	if err == pgx.ErrNoRows || !autoPurge {
		return nil
	}
	if err != nil {
		return err
	}
	lastEvidenceColumn := "last_evidence_at"
	if targetType == "insight" {
		lastEvidenceColumn = "updated_at"
	}
	query := fmt.Sprintf(`
		select id::text, value_score, status, quarantined_at, last_evidence_at
		from %s
		where user_confirmed=false and retention_locked=false and status not in ('confirmed','ignored','deleted')
		  and (value_score < 0.45 or (last_evidence_at is not null and last_evidence_at < $1))
		  and (not $3 or not exists(
			select 1 from steward_source_refs r where r.source_type=$4 and r.source_id=%s.id::text
		  ))
		order by value_score asc limit $2
	`, table, table)
	query = strings.ReplaceAll(query, "last_evidence_at", lastEvidenceColumn)
	rows, err := s.db.Pool.Query(ctx, query, now.AddDate(0, 0, -90), limit, protectReferenced, targetType)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var value float64
		var quarantinedAt, lastEvidenceAt *time.Time
		if err := rows.Scan(&id, &value, &status, &quarantinedAt, &lastEvidenceAt); err != nil {
			return err
		}
		action := domain.StewardLifecycleAction{TargetType: targetType, TargetID: id, ValueScore: value, RequiresPreview: requirePreview}
		switch {
		case value < 0.25 && status == "quarantined" && quarantinedAt != nil && quarantinedAt.Before(now.AddDate(0, 0, -30)):
			action.Action = "delete_inference"
			action.Reason = "低价值系统推断已完成 30 天隔离"
		case lastEvidenceAt != nil && lastEvidenceAt.Before(now.AddDate(0, 0, -180)) && status == "archived":
			action.Action = "delete_inference"
			action.Reason = "推断 180 天没有新证据且已经归档"
		case value < 0.25 && status != "quarantined":
			recoverable := now.AddDate(0, 0, 30)
			action.Action = "quarantine_inference"
			action.Reason = "价值分低于 0.25，进入 30 天隔离"
			action.RecoverableTo = &recoverable
		case value < 0.45 || (lastEvidenceAt != nil && lastEvidenceAt.Before(now.AddDate(0, 0, -90))):
			action.Action = "archive_inference"
			action.Reason = "价值分低于 0.45 或 90 天没有新证据"
		default:
			continue
		}
		evaluation.Actions = append(evaluation.Actions, action)
	}
	return rows.Err()
}

func (s *Service) PurgeLifecycle(ctx context.Context, input PurgeLifecycleInput) (domain.StewardPurgeResult, error) {
	result := domain.StewardPurgeResult{DryRun: !input.Execute, Actions: []domain.StewardLifecycleAction{}, CompletedAt: time.Now().UTC()}
	if !input.Execute {
		evaluation, err := s.EvaluateLifecycle(ctx, EvaluateLifecycleInput{})
		if err != nil {
			return result, err
		}
		result.Actions = evaluation.Actions
		return result, nil
	}
	if strings.TrimSpace(input.EvaluationID) == "" {
		return result, fmt.Errorf("evaluation_id from a recent dry run is required")
	}
	var evaluatedAt time.Time
	err := s.db.Pool.QueryRow(ctx, `
		select completed_at from steward_lifecycle_runs
		where id=$1 and job_type='evaluate' and dry_run=true and status='success'
	`, input.EvaluationID).Scan(&evaluatedAt)
	if err != nil {
		return result, fmt.Errorf("valid lifecycle evaluation not found: %w", err)
	}
	if evaluatedAt.Before(time.Now().UTC().Add(-time.Hour)) {
		return result, fmt.Errorf("lifecycle evaluation is older than 1 hour; run a new preview")
	}
	evaluation, err := s.EvaluateLifecycle(ctx, EvaluateLifecycleInput{})
	if err != nil {
		return result, err
	}
	result.DryRun = false
	result.Actions = evaluation.Actions
	for _, action := range evaluation.Actions {
		if input.Automatic && action.RequiresPreview {
			result.Skipped++
			continue
		}
		changed, err := s.executeLifecycleAction(ctx, action)
		if err != nil {
			return result, err
		}
		if !changed {
			result.Skipped++
			continue
		}
		switch action.Action {
		case "quarantine_inference", "archive_inference":
			result.Quarantined++
		default:
			result.Deleted++
		}
	}
	confirmed, syncable := !input.Automatic, false
	auditID, err := s.recordAudit(ctx, AuditInput{Actor: defaultString(map[bool]string{true: "system", false: "user"}[input.Automatic], "user"),
		Action: "lifecycle.purge", TargetType: "lifecycle", Source: "security:lifecycle", PermissionLevel: PermissionA3,
		DataLevel: DataD0, InputSummary: "authorized lifecycle policy execution",
		OutputSummary: fmt.Sprintf("deleted=%d quarantined=%d skipped=%d", result.Deleted, result.Quarantined, result.Skipped),
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK})
	if err != nil {
		return result, err
	}
	result.AuditID = auditID
	result.CompletedAt = time.Now().UTC()
	_, _ = s.db.Pool.Exec(ctx, `update steward_deletion_tombstones set audit_id=$1 where audit_id is null and deleted_at >= $2`, auditID, evaluatedAt)
	return result, nil
}

func (s *Service) executeLifecycleAction(ctx context.Context, action domain.StewardLifecycleAction) (bool, error) {
	switch action.Action {
	case "delete_observation":
		return s.deleteObservationEvidence(ctx, action.TargetID)
	case "delete_orphan_blob":
		return s.deleteOrphanBlob(ctx, action.TargetID)
	case "quarantine_inference", "archive_inference", "delete_inference":
		return s.applyInferenceLifecycleAction(ctx, action)
	default:
		return false, nil
	}
}

func (s *Service) deleteObservationEvidence(ctx context.Context, observationID string) (bool, error) {
	rows, err := s.db.Pool.Query(ctx, `select storage_path from steward_encrypted_blobs where observation_id=$1`, observationID)
	if err != nil {
		return false, err
	}
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return false, err
		}
		paths = append(paths, path)
	}
	rows.Close()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	relationRows, err := tx.Query(ctx, `delete from steward_relation_evidence where observation_id=$1 returning relation_id::text`, observationID)
	if err != nil {
		return false, err
	}
	relationIDs := []string{}
	for relationRows.Next() {
		var id string
		if err := relationRows.Scan(&id); err != nil {
			relationRows.Close()
			return false, err
		}
		relationIDs = append(relationIDs, id)
	}
	relationRows.Close()
	_, _ = tx.Exec(ctx, `delete from steward_source_refs where source_type='observation' and source_id=$1`, observationID)
	_, _ = tx.Exec(ctx, `delete from steward_encrypted_blobs where observation_id=$1`, observationID)
	result, err := tx.Exec(ctx, `delete from steward_observations where id=$1 and retention_locked=false`, observationID)
	if err != nil || result.RowsAffected() == 0 {
		return false, err
	}
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		insert into steward_deletion_tombstones (
			id, entity_type, entity_id, source_device_id, reason, deleted_at, expires_at
		) values ($1,'observation',$2,$3,'retention_ttl',$4,$5)
		on conflict (entity_type,entity_id) do update set deleted_at=excluded.deleted_at, expires_at=excluded.expires_at
	`, uuid.NewString(), observationID, s.agentIDValue(), now, now.AddDate(0, 0, 90))
	if err != nil {
		return false, err
	}
	for _, relationID := range relationIDs {
		_, _ = tx.Exec(ctx, `
			update steward_relations set evidence_count=(select count(*) from steward_relation_evidence where relation_id=$1),
			status=case when not exists(select 1 from steward_relation_evidence where relation_id=$1) then 'stale' else status end,
			updated_at=now() where id=$1
		`, relationID)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	for _, path := range paths {
		_ = os.Remove(path)
	}
	return true, nil
}

func (s *Service) deleteOrphanBlob(ctx context.Context, id string) (bool, error) {
	var path string
	err := s.db.Pool.QueryRow(ctx, `delete from steward_encrypted_blobs where id=$1 returning storage_path`, id).Scan(&path)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_ = os.Remove(path)
	return true, nil
}

func (s *Service) applyInferenceLifecycleAction(ctx context.Context, action domain.StewardLifecycleAction) (bool, error) {
	table := ""
	if action.TargetType == "habit" {
		table = "steward_habits"
	} else if action.TargetType == "insight" {
		table = "steward_insights"
	} else {
		return false, nil
	}
	status := ""
	switch action.Action {
	case "quarantine_inference":
		status = "quarantined"
	case "archive_inference":
		status = "archived"
	case "delete_inference":
		status = "deleted"
	}
	query := fmt.Sprintf(`
		update %s set status=$2,
		  quarantined_at=case when $2='quarantined' then coalesce(quarantined_at,now()) else quarantined_at end,
		  updated_at=now()
		where id=$1 and user_confirmed=false and retention_locked=false and status not in ('confirmed','ignored','deleted')
	`, table)
	result, err := s.db.Pool.Exec(ctx, query, action.TargetID, status)
	return err == nil && result.RowsAffected() > 0, err
}

func (s *Service) RunLifecycleMaintenance(ctx context.Context) error {
	now := time.Now().UTC()
	if err := s.ensureObservationPartitions(ctx, now); err != nil {
		return err
	}
	jobs := []struct {
		name     string
		interval time.Duration
		run      func(context.Context) (map[string]int, error)
	}{
		{name: "hourly_aggregation", interval: time.Hour, run: func(ctx context.Context) (map[string]int, error) {
			count, err := s.AggregateActivitySessions(ctx, 5000)
			return map[string]int{"sessions_created": count}, err
		}},
		{name: "daily_retention", interval: 24 * time.Hour, run: func(ctx context.Context) (map[string]int, error) {
			evaluation, err := s.EvaluateLifecycle(ctx, EvaluateLifecycleInput{Limit: 5000})
			if err != nil {
				return nil, err
			}
			purged, err := s.PurgeLifecycle(ctx, PurgeLifecycleInput{EvaluationID: evaluation.ID, Execute: true, Automatic: true})
			return map[string]int{"deleted": purged.Deleted, "quarantined": purged.Quarantined, "skipped": purged.Skipped}, err
		}},
		{name: "weekly_inference", interval: 7 * 24 * time.Hour, run: func(ctx context.Context) (map[string]int, error) {
			return s.EvaluateHabitsAndInsights(ctx, now)
		}},
		{name: "monthly_reflection", interval: 30 * 24 * time.Hour, run: s.runMemoryReflection},
		{name: "quarterly_archive", interval: 90 * 24 * time.Hour, run: s.runQuarterlyArchive},
	}
	for _, job := range jobs {
		if err := s.runScheduledLifecycleJob(ctx, job.name, job.interval, job.run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) runScheduledLifecycleJob(ctx context.Context, name string, interval time.Duration, run func(context.Context) (map[string]int, error)) error {
	var last *time.Time
	err := s.db.Pool.QueryRow(ctx, `select max(completed_at) from steward_lifecycle_runs where job_type=$1 and status='success'`, name).Scan(&last)
	if err != nil {
		return err
	}
	if last != nil && time.Since(*last) < interval {
		return nil
	}
	id, startedAt := uuid.NewString(), time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `insert into steward_lifecycle_runs (id,job_type,status,dry_run,started_at) values ($1,$2,'running',false,$3)`, id, name, startedAt)
	if err != nil {
		return err
	}
	counts, runErr := run(ctx)
	status := "success"
	var errorSummary *string
	if runErr != nil {
		status = "failed"
		value := truncateAdvisorText(runErr.Error(), 500)
		errorSummary = &value
	}
	_, updateErr := s.db.Pool.Exec(ctx, `
		update steward_lifecycle_runs set status=$2, action_counts=$3, error_summary=$4, completed_at=$5 where id=$1
	`, id, status, marshalJSON(counts), errorSummary, time.Now().UTC())
	if runErr != nil {
		return runErr
	}
	return updateErr
}

func (s *Service) runMemoryReflection(ctx context.Context) (map[string]int, error) {
	result, err := s.db.Pool.Exec(ctx, `
		update steward_memories m set confidence=greatest(0.1,confidence-0.1), inference_status='stale', updated_at=now()
		where m.user_confirmed=false and m.source='habit-engine' and m.updated_at < now()-interval '90 days'
		  and not exists(select 1 from steward_source_refs r where r.target_type='memory' and r.target_id=m.id and r.created_at >= now()-interval '90 days')
	`)
	if err != nil {
		return nil, err
	}
	return map[string]int{"memories_degraded": int(result.RowsAffected())}, nil
}

func (s *Service) runQuarterlyArchive(ctx context.Context) (map[string]int, error) {
	result, err := s.db.Pool.Exec(ctx, `
		update steward_entities set status='archived', updated_at=now()
		where status='active' and type in ('project','repository','topic') and last_seen_at < now()-interval '180 days'
	`)
	if err != nil {
		return nil, err
	}
	return map[string]int{"entities_archived": int(result.RowsAffected())}, nil
}
