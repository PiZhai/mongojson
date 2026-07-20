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
	"github.com/jackc/pgx/v5/pgconn"
)

type ActivityBatch struct {
	ID                 string         `json:"id"`
	DeviceID           string         `json:"device_id"`
	WindowStart        time.Time      `json:"window_start"`
	WindowEnd          time.Time      `json:"window_end"`
	TriggerKind        string         `json:"trigger_kind"`
	Revision           int            `json:"revision"`
	SupersedesID       *string        `json:"supersedes_id,omitempty"`
	CatalogGeneration  int64          `json:"catalog_generation"`
	ContextHash        string         `json:"context_hash"`
	Statistics         map[string]any `json:"statistics"`
	Checkpoint         map[string]any `json:"checkpoint"`
	Status             string         `json:"status"`
	DueAt              time.Time      `json:"due_at"`
	LastStartedAt      *time.Time     `json:"last_started_at,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	AttemptCount       int            `json:"attempt_count"`
	NextAttemptAt      time.Time      `json:"next_attempt_at"`
	LeaseOwner         string         `json:"lease_owner"`
	LeaseExpiresAt     *time.Time     `json:"lease_expires_at,omitempty"`
	IdempotencyKey     string         `json:"idempotency_key"`
	ControlGeneration  int64          `json:"control_generation"`
	EpisodeID          *string        `json:"episode_id,omitempty"`
	Provider           string         `json:"provider,omitempty"`
	Model              string         `json:"model,omitempty"`
	ProviderResponseID string         `json:"provider_response_id,omitempty"`
	ResponseSummary    string         `json:"response_summary,omitempty"`
	ErrorCode          string         `json:"error_code,omitempty"`
	ErrorSummary       string         `json:"error_summary,omitempty"`
	MissedRunCount     int            `json:"missed_run_count"`
	CatchUpPolicy      string         `json:"catch_up_policy"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type ActivityBatchItem struct {
	ID              string    `json:"id"`
	BatchID         string    `json:"batch_id"`
	ItemType        string    `json:"item_type"`
	ItemID          string    `json:"item_id"`
	ItemOccurredAt  time.Time `json:"item_occurred_at"`
	SourceType      string    `json:"source_type"`
	SourceID        string    `json:"source_id"`
	SourceTime      time.Time `json:"source_time"`
	Role            string    `json:"role"`
	Ordinal         int       `json:"ordinal"`
	SnapshotHash    string    `json:"snapshot_hash"`
	SourceRevision  int64     `json:"source_revision"`
	SourceUpdatedAt time.Time `json:"source_updated_at"`
	EvidenceSummary string    `json:"evidence_summary"`
	CreatedAt       time.Time `json:"created_at"`
}

type ActivityBatchContext struct {
	Batch ActivityBatch       `json:"batch"`
	Items []ActivityBatchItem `json:"items"`
}

type ActivityBatchReconcileResult struct {
	Scanned      int `json:"scanned"`
	Completed    int `json:"completed"`
	WaitingModel int `json:"waiting_model"`
	Cancelled    int `json:"cancelled"`
	Skipped      int `json:"skipped"`
}

type ActivitySourceStatus struct {
	DeviceID              string         `json:"device_id"`
	CollectorName         string         `json:"collector_name"`
	SourceKey             string         `json:"source_key"`
	ExecutionTarget       string         `json:"execution_target"`
	Watcher               string         `json:"watcher,omitempty"`
	Host                  string         `json:"host,omitempty"`
	EventType             string         `json:"event_type,omitempty"`
	InteractiveSessionID  string         `json:"interactive_session_id,omitempty"`
	Status                string         `json:"status"`
	Cursor                map[string]any `json:"cursor"`
	Capabilities          map[string]any `json:"capabilities"`
	APIVersion            string         `json:"api_version,omitempty"`
	LastPollAt            *time.Time     `json:"last_poll_at,omitempty"`
	LastSourceEventAt     *time.Time     `json:"last_source_event_at,omitempty"`
	LastIngestedAt        *time.Time     `json:"last_ingested_at,omitempty"`
	BacklogCount          int64          `json:"backlog_count"`
	MaxExpectedLagSeconds int            `json:"max_expected_lag_seconds"`
	LastError             string         `json:"last_error,omitempty"`
	Reachable             bool           `json:"reachable"`
	SourceFresh           bool           `json:"source_fresh"`
	IngestionFresh        bool           `json:"ingestion_fresh"`
	Fresh                 bool           `json:"fresh"`
}

type ActivityPipelineStatus struct {
	Enabled           bool                   `json:"enabled"`
	Mode              string                 `json:"mode"`
	Sources           []ActivitySourceStatus `json:"sources"`
	PendingBatches    int                    `json:"pending_batches"`
	ProcessingBatches int                    `json:"processing_batches"`
	WaitingModel      int                    `json:"waiting_model"`
	FailedBatches     int                    `json:"failed_batches"`
	LastBatchAt       *time.Time             `json:"last_batch_at,omitempty"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

const activityBatchColumns = `id::text,device_id,window_start,window_end,trigger_kind,revision,supersedes_id::text,
	catalog_generation,context_hash,statistics,checkpoint,status,due_at,last_started_at,
	completed_at,attempt_count,next_attempt_at,lease_owner,lease_expires_at,idempotency_key,
	control_generation,episode_id::text,provider,model,provider_response_id,response_summary,
	error_code,error_summary,missed_run_count,catch_up_policy,created_at,updated_at`

type activitySessionForBatch struct {
	ID, DeviceID, Title, Summary, Source, CanonicalContext string
	ObservationCount, Revision, SourceCount                int
	ActiveSeconds, AFKSeconds                              float64
	StartedAt, EndedAt, UpdatedAt                          time.Time
}

// activityBatchSourceItem is the normalized, immutable representation used by
// every source in a batch. Sessions, entity changes, outstanding notifications
// and profile snapshots all pass through the same deterministic ordering,
// hashing and persistence path.
type activityBatchSourceItem struct {
	SourceType      string
	SourceID        string
	SourceTime      time.Time
	Role            string
	SourceRevision  int64
	SourceUpdatedAt time.Time
	SnapshotHash    string
	EvidenceSummary string
}

type recentProfileSnapshotForBatch struct {
	ID          string
	Revision    int64
	AsOf        time.Time
	Document    map[string]any
	FactCount   int
	ContentHash string
	CreatedAt   time.Time
}

// BuildDueActivityBatches materializes immutable evidence batches. A persisted
// session revision/update timestamp is the snapshot boundary: unchanged
// snapshots are skipped, while a late update makes the whole logical window
// dirty so its successor contains both changed and unchanged sessions.
func (s *Service) BuildDueActivityBatches(ctx context.Context, now time.Time) ([]ActivityBatch, error) {
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled || settings.Mode != "batch" {
		return []ActivityBatch{}, nil
	}
	interval := time.Duration(settings.BatchIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	grace := time.Duration(settings.BoundaryGraceSeconds) * time.Second
	if grace < 0 {
		grace = 0
	}
	cutoff := now.UTC().Add(-grace)
	catchupDays := settings.ReportCatchupDays
	if catchupDays < 1 {
		catchupDays = 7
	}
	catalogGeneration := s.runtimeTools.generationValue()
	rows, err := s.db.Pool.Query(ctx, `
		with eligible as (
			select s.id,s.title,s.summary,s.source,s.canonical_context,
			       s.observation_count,s.revision,s.source_count,s.active_seconds,s.afk_seconds,
			       s.started_at,s.ended_at,s.updated_at,
			       coalesce(nullif(trim(s.device_id),''),$4) as effective_device_id,
			       date_bin($3::interval,s.started_at,timestamptz '1970-01-01 00:00:00+00') as logical_window_start,
			       case when date_bin($3::interval,s.started_at,timestamptz '1970-01-01 00:00:00+00')+$3::interval > $1
			            then 'activity_boundary' else 'interval' end as logical_trigger
			from steward_activity_sessions s
			where s.status='closed' and s.ended_at <= $1 and s.started_at >= $2
		), dirty_windows as (
			select distinct e.effective_device_id,e.logical_window_start,e.logical_trigger
			from eligible e
			where not exists (
				select 1
				from steward_activity_batch_items i
				join steward_activity_batches b on b.id=i.batch_id
				where i.item_type='activity_session'
				  and i.item_id=e.id::text
				  and i.source_revision=e.revision
				  and i.source_updated_at=e.updated_at
				  and b.status<>'superseded'
				  and b.catalog_generation=$5
			)
		)
		select e.id::text,e.effective_device_id,e.title,e.summary,e.source,e.canonical_context,
		       e.observation_count,e.revision,e.source_count,e.active_seconds,e.afk_seconds,
		       e.started_at,e.ended_at,e.updated_at
		from eligible e
		join dirty_windows d
		  on d.effective_device_id=e.effective_device_id
		 and d.logical_window_start=e.logical_window_start
		 and d.logical_trigger=e.logical_trigger
		order by e.started_at,e.id
	`, cutoff, now.UTC().AddDate(0, 0, -catchupDays), fmt.Sprintf("%f seconds", interval.Seconds()), s.agentIDValue(), catalogGeneration)
	if err != nil {
		return nil, fmt.Errorf("list unbatched activity sessions: %w", err)
	}
	sessions := []activitySessionForBatch{}
	for rows.Next() {
		var item activitySessionForBatch
		if err := rows.Scan(&item.ID, &item.DeviceID, &item.Title, &item.Summary, &item.Source,
			&item.CanonicalContext, &item.ObservationCount, &item.Revision, &item.SourceCount,
			&item.ActiveSeconds, &item.AFKSeconds, &item.StartedAt, &item.EndedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		sessions = append(sessions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	type batchGroup struct {
		deviceID, trigger string
		start, end        time.Time
		items             []activitySessionForBatch
	}
	groups := map[string]*batchGroup{}
	for _, session := range sessions {
		windowStart := session.StartedAt.UTC().Truncate(interval)
		windowEnd := windowStart.Add(interval)
		trigger := "interval"
		if windowEnd.After(cutoff) {
			windowEnd = session.EndedAt.UTC()
			trigger = "activity_boundary"
		}
		deviceID := defaultString(strings.TrimSpace(session.DeviceID), s.agentIDValue())
		key := deviceID + "|" + windowStart.Format(time.RFC3339Nano) + "|" + trigger
		group := groups[key]
		if group == nil {
			group = &batchGroup{deviceID: deviceID, trigger: trigger, start: windowStart, end: windowEnd}
			groups[key] = group
		}
		if trigger == "activity_boundary" && session.EndedAt.After(group.end) {
			group.end = session.EndedAt.UTC()
		}
		group.items = append(group.items, session)
	}
	ordered := make([]*batchGroup, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].start.Equal(ordered[j].start) {
			return ordered[i].deviceID < ordered[j].deviceID
		}
		return ordered[i].start.Before(ordered[j].start)
	})
	created := make([]ActivityBatch, 0, len(ordered))
	for _, group := range ordered {
		batch, wasCreated, err := s.persistActivityBatch(ctx, group.deviceID, group.trigger, group.start, group.end, group.items, now.UTC())
		if err != nil {
			return created, err
		}
		if wasCreated {
			created = append(created, batch)
		}
	}
	return created, nil
}

func (s *Service) persistActivityBatch(ctx context.Context, deviceID, trigger string, start, end time.Time, items []activitySessionForBatch, now time.Time) (ActivityBatch, bool, error) {
	if len(items) == 0 {
		return ActivityBatch{}, false, nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return ActivityBatch{}, false, err
	}
	defer tx.Rollback(ctx)
	catalogGeneration := s.runtimeTools.generationValue()
	logicalKey := fmt.Sprintf("activity-batch-window:%s:%s:%s:%d", deviceID,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), catalogGeneration)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, logicalKey); err != nil {
		return ActivityBatch{}, false, err
	}

	sourceItems := make([]activityBatchSourceItem, 0, len(items)+8)
	statistics := map[string]any{
		"session_count": len(items), "observation_count": 0, "active_seconds": 0.0,
		"afk_seconds": 0.0, "source_count": 0,
	}
	sources := map[string]bool{}
	for _, item := range items {
		statistics["observation_count"] = statistics["observation_count"].(int) + item.ObservationCount
		statistics["active_seconds"] = statistics["active_seconds"].(float64) + item.ActiveSeconds
		statistics["afk_seconds"] = statistics["afk_seconds"].(float64) + item.AFKSeconds
		sources[item.Source] = true
		summary := strings.TrimSpace(strings.Join(nonEmptyStrings(item.Title, item.Summary), " · "))
		sourceItems = append(sourceItems, activityBatchSourceItem{
			SourceType:      "activity_session",
			SourceID:        item.ID,
			SourceTime:      item.StartedAt.UTC(),
			Role:            "evidence",
			SourceRevision:  int64(item.Revision),
			SourceUpdatedAt: item.UpdatedAt.UTC(),
			SnapshotHash:    activityBatchSessionSnapshot(item),
			EvidenceSummary: truncateAdvisorText(summary, 500),
		})
	}
	statistics["source_count"] = len(sources)

	supplemental, err := s.loadActivityBatchSupplementalItems(ctx, tx, start.UTC(), end.UTC(), now.UTC())
	if err != nil {
		return ActivityBatch{}, false, err
	}
	sourceItems = append(sourceItems, supplemental...)
	sort.SliceStable(sourceItems, func(i, j int) bool {
		if sourceItems[i].SourceTime.Equal(sourceItems[j].SourceTime) {
			if sourceItems[i].SourceType == sourceItems[j].SourceType {
				return sourceItems[i].SourceID < sourceItems[j].SourceID
			}
			return sourceItems[i].SourceType < sourceItems[j].SourceType
		}
		return sourceItems[i].SourceTime.Before(sourceItems[j].SourceTime)
	})
	sourceTypeCounts := map[string]int{}
	hasher := sha256.New()
	for _, item := range sourceItems {
		sourceTypeCounts[item.SourceType]++
		fmt.Fprintln(hasher, activityBatchSourceSnapshot(item))
	}
	statistics["item_count"] = len(sourceItems)
	statistics["supplemental_item_count"] = len(supplemental)
	statistics["source_type_counts"] = sourceTypeCounts
	contextHash := hex.EncodeToString(hasher.Sum(nil))
	statisticsJSON, err := json.Marshal(statistics)
	if err != nil {
		return ActivityBatch{}, false, fmt.Errorf("encode activity batch statistics: %w", err)
	}

	var previous *ActivityBatch
	latest, latestErr := scanActivityBatch(tx.QueryRow(ctx, `
		select `+activityBatchColumns+` from steward_activity_batches
		where device_id=$1 and window_start=$2 and window_end=$3 and catalog_generation=$4
		  and status<>'superseded'
		order by revision desc,created_at desc limit 1 for update
	`, deviceID, start, end, catalogGeneration))
	if latestErr == nil {
		previous = &latest
		if latest.ContextHash == contextHash {
			if err := tx.Commit(ctx); err != nil {
				return ActivityBatch{}, false, err
			}
			return latest, false, nil
		}
		// processing/executing and every retryable state retain ownership of
		// their immutable snapshot. The changed session remains dirty and a
		// successor is materialized after that batch reaches a replaceable
		// terminal state. Pending is safe to replace because it has never been
		// claimed and the row lock serializes this decision with ClaimActivityBatch.
		if !activityBatchRevisionReplaceable(latest.Status) {
			if err := tx.Commit(ctx); err != nil {
				return ActivityBatch{}, false, err
			}
			return latest, false, nil
		}
	} else if !errors.Is(latestErr, pgx.ErrNoRows) {
		return ActivityBatch{}, false, latestErr
	}

	var revision int
	if err := tx.QueryRow(ctx, `
		select coalesce(max(revision),0)+1 from steward_activity_batches
		where device_id=$1 and window_start=$2 and window_end=$3 and catalog_generation=$4
	`, deviceID, start, end, catalogGeneration).Scan(&revision); err != nil {
		return ActivityBatch{}, false, err
	}
	// BATCH-002: the immutable batch identity is the logical device/window,
	// catalog generation and monotonically increasing revision. ContextHash is
	// retained as the content fingerprint, not overloaded as the identity.
	idempotencyKey := fmt.Sprintf("activity-batch:%s:%s:%s:%d:%d", deviceID,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), catalogGeneration, revision)
	batchID := uuid.NewString()
	var supersedesID any
	if previous != nil {
		supersedesID = previous.ID
		tag, updateErr := tx.Exec(ctx, `
			update steward_activity_batches set status='superseded',completed_at=coalesce(completed_at,$2),
				lease_owner='',lease_expires_at=null,control_generation=control_generation+1,
				checkpoint=checkpoint || jsonb_build_object('superseded_by_batch_id',$3::text,'superseded_at',$2),
				updated_at=$2
			where id=$1 and status=$4
		`, previous.ID, now, batchID, previous.Status)
		if updateErr != nil {
			return ActivityBatch{}, false, updateErr
		}
		if tag.RowsAffected() != 1 {
			return ActivityBatch{}, false, errors.New("activity batch changed while creating a revision")
		}
	}
	_, err = tx.Exec(ctx, `
		insert into steward_activity_batches (
			id,device_id,window_start,window_end,trigger_kind,revision,supersedes_id,
			catalog_generation,context_hash,statistics,status,due_at,next_attempt_at,idempotency_key,created_at,updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11,$11,$12,$11,$11)
	`, batchID, deviceID, start, end, trigger, revision, supersedesID, catalogGeneration, contextHash, statisticsJSON, now, idempotencyKey)
	if err != nil {
		return ActivityBatch{}, false, fmt.Errorf("create activity batch: %w", err)
	}
	for ordinal, item := range sourceItems {
		_, err = tx.Exec(ctx, `
			insert into steward_activity_batch_items (
				id,batch_id,item_type,item_id,item_occurred_at,role,ordinal,snapshot_hash,
				source_revision,source_updated_at,evidence_summary
			) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			on conflict(batch_id,item_type,item_id,item_occurred_at) do nothing
		`, uuid.NewString(), batchID, item.SourceType, item.SourceID, item.SourceTime,
			item.Role, ordinal, item.SnapshotHash, item.SourceRevision, item.SourceUpdatedAt,
			item.EvidenceSummary)
		if err != nil {
			return ActivityBatch{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ActivityBatch{}, false, err
	}
	batch, err := s.GetActivityBatch(ctx, batchID)
	return batch, true, err
}

func activityBatchSessionSnapshot(item activitySessionForBatch) string {
	return fmt.Sprintf("%s:%d:%s", item.ID, item.Revision, item.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

func activityBatchSourceSnapshot(item activityBatchSourceItem) string {
	return strings.Join([]string{
		item.SourceType,
		item.SourceID,
		item.SourceTime.UTC().Format(time.RFC3339Nano),
		item.Role,
		fmt.Sprintf("%d", item.SourceRevision),
		item.SourceUpdatedAt.UTC().Format(time.RFC3339Nano),
		item.SnapshotHash,
	}, "|")
}

func (s *Service) loadActivityBatchSupplementalItems(ctx context.Context, tx pgx.Tx, start, end, generatedAt time.Time) ([]activityBatchSourceItem, error) {
	items := []activityBatchSourceItem{}

	eventRows, err := tx.Query(ctx, `
		select id::text,type,title,summary,source,status,version,updated_at
		from steward_events
		where deleted_at is null and updated_at >= $1 and updated_at < $2
		order by updated_at,id
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("load activity batch event changes: %w", err)
	}
	for eventRows.Next() {
		var id, eventType, title, summary, source, status string
		var revision int64
		var updatedAt time.Time
		if err := eventRows.Scan(&id, &eventType, &title, &summary, &source, &status, &revision, &updatedAt); err != nil {
			eventRows.Close()
			return nil, fmt.Errorf("scan activity batch event change: %w", err)
		}
		items = append(items, activityBatchSourceItem{
			SourceType:      "event_change",
			SourceID:        id,
			SourceTime:      updatedAt.UTC(),
			Role:            "context",
			SourceRevision:  revision,
			SourceUpdatedAt: updatedAt.UTC(),
			SnapshotHash:    activityBatchRecordSnapshot("event_change", id, revision, updatedAt),
			EvidenceSummary: truncateAdvisorText(strings.Join(nonEmptyStrings(
				fmt.Sprintf("日程/事件变更 [%s/%s]", eventType, status), title, summary, "来源 "+source,
			), " · "), 500),
		})
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return nil, fmt.Errorf("iterate activity batch event changes: %w", err)
	}
	eventRows.Close()

	taskRows, err := tx.Query(ctx, `
		select id::text,type,title,description,status,priority,due_at,source,version,updated_at
		from steward_tasks
		where deleted_at is null and updated_at >= $1 and updated_at < $2
		order by updated_at,id
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("load activity batch task changes: %w", err)
	}
	for taskRows.Next() {
		var id, taskType, title, description, status, priority, source string
		var dueAt *time.Time
		var revision int64
		var updatedAt time.Time
		if err := taskRows.Scan(&id, &taskType, &title, &description, &status, &priority, &dueAt, &source, &revision, &updatedAt); err != nil {
			taskRows.Close()
			return nil, fmt.Errorf("scan activity batch task change: %w", err)
		}
		dueSummary := ""
		if dueAt != nil {
			dueSummary = "截止 " + dueAt.UTC().Format(time.RFC3339)
		}
		items = append(items, activityBatchSourceItem{
			SourceType:      "task_change",
			SourceID:        id,
			SourceTime:      updatedAt.UTC(),
			Role:            "context",
			SourceRevision:  revision,
			SourceUpdatedAt: updatedAt.UTC(),
			SnapshotHash:    activityBatchRecordSnapshot("task_change", id, revision, updatedAt),
			EvidenceSummary: truncateAdvisorText(strings.Join(nonEmptyStrings(
				fmt.Sprintf("任务变更 [%s/%s/%s]", taskType, status, priority), title, description, dueSummary, "来源 "+source,
			), " · "), 500),
		})
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return nil, fmt.Errorf("iterate activity batch task changes: %w", err)
	}
	taskRows.Close()

	notificationRows, err := tx.Query(ctx, `
		select id::text,source_type,title,body,category,priority,status,schedule_revision,scheduled_at,updated_at
		from steward_notifications
		where created_at <= $1
		  and status not in ('acknowledged','dismissed','auto_resolved','cancelled')
		  and (expires_at is null or expires_at > $1)
		order by scheduled_at,id
	`, generatedAt)
	if err != nil {
		return nil, fmt.Errorf("load activity batch outstanding notifications: %w", err)
	}
	for notificationRows.Next() {
		var id, origin, title, body, category, priority, status string
		var revision int64
		var scheduledAt, updatedAt time.Time
		if err := notificationRows.Scan(&id, &origin, &title, &body, &category, &priority, &status,
			&revision, &scheduledAt, &updatedAt); err != nil {
			notificationRows.Close()
			return nil, fmt.Errorf("scan activity batch outstanding notification: %w", err)
		}
		sourceType := "notification"
		if strings.EqualFold(strings.TrimSpace(category), "reminder") {
			sourceType = "reminder"
		}
		items = append(items, activityBatchSourceItem{
			SourceType:      sourceType,
			SourceID:        id,
			SourceTime:      scheduledAt.UTC(),
			Role:            "pending_attention",
			SourceRevision:  revision,
			SourceUpdatedAt: updatedAt.UTC(),
			SnapshotHash:    activityBatchRecordSnapshot(sourceType, id, revision, updatedAt),
			EvidenceSummary: truncateAdvisorText(strings.Join(nonEmptyStrings(
				fmt.Sprintf("未处理%s [%s/%s]", map[bool]string{true: "提醒", false: "通知"}[sourceType == "reminder"], status, priority),
				title, body, "来源 "+origin, "计划 "+scheduledAt.UTC().Format(time.RFC3339),
			), " · "), 500),
		})
	}
	if err := notificationRows.Err(); err != nil {
		notificationRows.Close()
		return nil, fmt.Errorf("iterate activity batch outstanding notifications: %w", err)
	}
	notificationRows.Close()

	profileRows, err := tx.Query(ctx, `
		select id::text,revision,as_of,document,fact_count,content_hash,created_at
		from steward_profile_snapshots
		where profile_scope='default' and view='recent' and as_of <= $1
		order by revision desc,id desc limit 2
	`, generatedAt)
	if err != nil {
		return nil, fmt.Errorf("load activity batch recent profile snapshots: %w", err)
	}
	profiles := []recentProfileSnapshotForBatch{}
	for profileRows.Next() {
		var snapshot recentProfileSnapshotForBatch
		if err := profileRows.Scan(&snapshot.ID, &snapshot.Revision, &snapshot.AsOf, &snapshot.Document,
			&snapshot.FactCount, &snapshot.ContentHash, &snapshot.CreatedAt); err != nil {
			profileRows.Close()
			return nil, fmt.Errorf("scan activity batch recent profile snapshot: %w", err)
		}
		snapshot.ContentHash = activityBatchProfileContentHash(snapshot)
		profiles = append(profiles, snapshot)
	}
	if err := profileRows.Err(); err != nil {
		profileRows.Close()
		return nil, fmt.Errorf("iterate activity batch recent profile snapshots: %w", err)
	}
	profileRows.Close()
	if len(profiles) > 0 {
		current := profiles[0]
		items = append(items, activityBatchSourceItem{
			SourceType:      "profile_snapshot",
			SourceID:        current.ID,
			SourceTime:      current.AsOf.UTC(),
			Role:            "profile_context",
			SourceRevision:  current.Revision,
			SourceUpdatedAt: current.CreatedAt.UTC(),
			SnapshotHash:    current.ContentHash,
			EvidenceSummary: fmt.Sprintf("近期画像快照 revision=%d facts=%d as_of=%s", current.Revision, current.FactCount, current.AsOf.UTC().Format(time.RFC3339)),
		})
		if len(profiles) > 1 {
			previous := profiles[1]
			items = append(items, activityBatchSourceItem{
				SourceType:      "profile_diff",
				SourceID:        current.ID,
				SourceTime:      current.AsOf.UTC(),
				Role:            "profile_context",
				SourceRevision:  current.Revision,
				SourceUpdatedAt: current.CreatedAt.UTC(),
				SnapshotHash:    activityBatchDigest("profile-diff|" + previous.ContentHash + "|" + current.ContentHash),
				EvidenceSummary: truncateAdvisorText(activityBatchProfileDiffSummary(previous, current), 500),
			})
		}
	}

	return items, nil
}

func activityBatchRecordSnapshot(sourceType, sourceID string, revision int64, updatedAt time.Time) string {
	return strings.Join([]string{sourceType, sourceID, fmt.Sprintf("%d", revision), updatedAt.UTC().Format(time.RFC3339Nano)}, ":")
}

func activityBatchProfileContentHash(snapshot recentProfileSnapshotForBatch) string {
	if value := strings.TrimSpace(snapshot.ContentHash); value != "" {
		return value
	}
	raw, _ := json.Marshal(snapshot.Document)
	return activityBatchDigest(string(raw))
}

func activityBatchDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func activityBatchProfileDiffSummary(previous, current recentProfileSnapshotForBatch) string {
	added, changed, removed := []string{}, []string{}, []string{}
	for key, currentValue := range current.Document {
		previousValue, exists := previous.Document[key]
		if !exists {
			added = append(added, key)
			continue
		}
		currentJSON, _ := json.Marshal(currentValue)
		previousJSON, _ := json.Marshal(previousValue)
		if string(currentJSON) != string(previousJSON) {
			changed = append(changed, key)
		}
	}
	for key := range previous.Document {
		if _, exists := current.Document[key]; !exists {
			removed = append(removed, key)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	parts := []string{fmt.Sprintf("近期画像差异 revision=%d→%d previous=%s current=%s", previous.Revision, current.Revision, previous.ID, current.ID)}
	if len(added) > 0 {
		parts = append(parts, "新增="+strings.Join(added, ","))
	}
	if len(changed) > 0 {
		parts = append(parts, "变更="+strings.Join(changed, ","))
	}
	if len(removed) > 0 {
		parts = append(parts, "移除="+strings.Join(removed, ","))
	}
	if len(parts) == 1 {
		parts = append(parts, "内容无变化")
	}
	return strings.Join(parts, " · ")
}

func activityBatchRevisionReplaceable(status string) bool {
	switch status {
	case "pending", "completed", "cancelled":
		return true
	default:
		return false
	}
}

func (s *Service) GetActivityBatch(ctx context.Context, id string) (ActivityBatch, error) {
	return scanActivityBatch(s.db.Pool.QueryRow(ctx, `select `+activityBatchColumns+` from steward_activity_batches where id=$1`, id))
}

// ListActivityBatches exposes the durable batch state through the service
// layer so HTTP handlers and model tools do not need their own partial SQL
// projections. An empty status lists every state.
func (s *Service) ListActivityBatches(ctx context.Context, status string, limit int) ([]ActivityBatch, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && !validActivityBatchStatus(status) {
		return nil, fmt.Errorf("unsupported activity batch status %q", status)
	}
	limit = normalizeLimit(limit, 50, 200)
	rows, err := s.db.Pool.Query(ctx, `select `+activityBatchColumns+` from steward_activity_batches
		where ($1='' or status=$1) order by window_start desc,created_at desc limit $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ActivityBatch, 0, limit)
	for rows.Next() {
		item, scanErr := scanActivityBatch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validActivityBatchStatus(status string) bool {
	switch status {
	case "pending", "processing", "executing", "waiting_model", "failed", "completed", "cancelled", "superseded":
		return true
	default:
		return false
	}
}

func scanActivityBatch(row rowScanner) (ActivityBatch, error) {
	var out ActivityBatch
	err := row.Scan(&out.ID, &out.DeviceID, &out.WindowStart, &out.WindowEnd, &out.TriggerKind,
		&out.Revision, &out.SupersedesID, &out.CatalogGeneration, &out.ContextHash,
		&out.Statistics, &out.Checkpoint, &out.Status, &out.DueAt, &out.LastStartedAt,
		&out.CompletedAt, &out.AttemptCount, &out.NextAttemptAt, &out.LeaseOwner,
		&out.LeaseExpiresAt, &out.IdempotencyKey, &out.ControlGeneration, &out.EpisodeID,
		&out.Provider, &out.Model, &out.ProviderResponseID, &out.ResponseSummary,
		&out.ErrorCode, &out.ErrorSummary, &out.MissedRunCount, &out.CatchUpPolicy,
		&out.CreatedAt, &out.UpdatedAt)
	if out.Statistics == nil {
		out.Statistics = map[string]any{}
	}
	if out.Checkpoint == nil {
		out.Checkpoint = map[string]any{}
	}
	return out, err
}

func (s *Service) GetActivityBatchContext(ctx context.Context, id string) (ActivityBatchContext, error) {
	batch, err := s.GetActivityBatch(ctx, id)
	if err != nil {
		return ActivityBatchContext{}, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,batch_id::text,item_type,item_id,item_occurred_at,role,ordinal,
		       snapshot_hash,source_revision,source_updated_at,evidence_summary,created_at
		from steward_activity_batch_items where batch_id=$1 order by ordinal,id
	`, id)
	if err != nil {
		return ActivityBatchContext{}, err
	}
	defer rows.Close()
	items := []ActivityBatchItem{}
	for rows.Next() {
		var item ActivityBatchItem
		if err := rows.Scan(&item.ID, &item.BatchID, &item.ItemType, &item.ItemID,
			&item.ItemOccurredAt, &item.Role, &item.Ordinal, &item.SnapshotHash,
			&item.SourceRevision, &item.SourceUpdatedAt, &item.EvidenceSummary, &item.CreatedAt); err != nil {
			return ActivityBatchContext{}, err
		}
		item.SourceType = item.ItemType
		item.SourceID = item.ItemID
		item.SourceTime = item.ItemOccurredAt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ActivityBatchContext{}, err
	}
	return ActivityBatchContext{Batch: batch, Items: items}, nil
}

func (s *Service) ClaimActivityBatch(ctx context.Context, worker string, lease time.Duration) (*ActivityBatch, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return nil, errors.New("activity batch worker is required")
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `
		select b.id::text from steward_activity_batches b
		where (b.status in ('pending','waiting_model','failed') or (b.status='processing' and b.episode_id is null))
		  and b.next_attempt_at <= now()
		  and (b.lease_expires_at is null or b.lease_expires_at < now())
		  and not exists (
			select 1 from steward_activity_batches predecessor
			where predecessor.device_id=b.device_id
			  and predecessor.status not in ('completed','cancelled','superseded')
			  and (predecessor.window_start < b.window_start
			    or (predecessor.window_start=b.window_start and predecessor.created_at < b.created_at)
			    or (predecessor.window_start=b.window_start and predecessor.created_at=b.created_at and predecessor.id < b.id))
		  )
		order by b.window_start,b.created_at,b.id for update of b skip locked limit 1
	`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		update steward_activity_batches set status='processing',lease_owner=$2,
		lease_expires_at=now()+$3::interval,last_started_at=now(),
		attempt_count=attempt_count+case when status='processing' and episode_id is null then 0 else 1 end,
		control_generation=control_generation+1,updated_at=now()
		where id=$1
	`, id, worker, fmt.Sprintf("%f seconds", lease.Seconds()))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	item, err := s.GetActivityBatch(ctx, id)
	return &item, err
}

func (s *Service) CompleteActivityBatch(ctx context.Context, id, worker string, expectedGeneration int64, responseSummary string) error {
	worker = strings.TrimSpace(worker)
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_activity_batches set status='completed',completed_at=now(),lease_owner='',
		lease_expires_at=null,response_summary=$3,error_code='',error_summary='',
		checkpoint=checkpoint || jsonb_build_object('completed_by_worker',$2),updated_at=now()
		where id=$1 and status='processing' and episode_id is null and lease_owner=$2 and control_generation=$4
	`, id, worker, truncateAdvisorText(responseSummary, 1000), expectedGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	batch, getErr := s.GetActivityBatch(ctx, id)
	if getErr == nil && batch.Status == "completed" && batch.EpisodeID == nil && batch.ControlGeneration == expectedGeneration && activityBatchCheckpointString(batch.Checkpoint, "completed_by_worker") == worker {
		return nil
	}
	return errors.New("activity batch lease was lost before completion")
}

func activityBatchCheckpointString(checkpoint map[string]any, key string) string {
	value, _ := checkpoint[key].(string)
	return strings.TrimSpace(value)
}

// AttachActivityBatchEpisode fences the background Episode to the worker that
// owns the current lease. Repeating the same attachment after a restart is
// idempotent, while a second Episode cannot silently take over the batch.
func (s *Service) AttachActivityBatchEpisode(ctx context.Context, id, worker, episodeID string, expectedGeneration int64) error {
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_activity_batches set episode_id=$3,status='executing',updated_at=now()
		where id=$1 and status in ('processing','executing') and lease_owner=$2 and control_generation=$4
		  and (episode_id is null or episode_id=$3)
	`, id, strings.TrimSpace(worker), episodeID, expectedGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("activity batch lease or Episode attachment was lost")
	}
	return nil
}

func (s *Service) CompleteActivityBatchEpisode(ctx context.Context, id, episodeID string, expectedGeneration int64, provider, model, responseID, responseSummary string) error {
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_activity_batches set status='completed',completed_at=now(),lease_owner='',
		lease_expires_at=null,provider=$3,model=$4,provider_response_id=$5,response_summary=$6,
		error_code='',error_summary='',updated_at=now()
		where id=$1 and status='executing' and episode_id=$2 and control_generation=$7
	`, id, episodeID, provider, model, responseID, truncateAdvisorText(responseSummary, 1000), expectedGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	batch, getErr := s.GetActivityBatch(ctx, id)
	if getErr == nil && batch.Status == "completed" && batch.ControlGeneration == expectedGeneration && batch.EpisodeID != nil && *batch.EpisodeID == episodeID {
		return nil
	}
	return errors.New("activity batch Episode no longer owns the batch")
}

// ReconcileActivityBatchEpisodes projects terminal Agent Episode state back
// into its immutable activity batch. Both batch and Episode generations are
// compared by the update so a stale scan cannot overwrite a pause, retry or
// cancellation that won a concurrent race.
func (s *Service) ReconcileActivityBatchEpisodes(ctx context.Context, now time.Time, limit int) (ActivityBatchReconcileResult, error) {
	limit = normalizeLimit(limit, 100, 1000)
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return ActivityBatchReconcileResult{}, err
	}
	defer tx.Rollback(ctx)
	type candidate struct {
		batchID, episodeID, episodeStatus, summary, failure string
		provider, model, responseID                         string
		batchGeneration, episodeGeneration                  int64
		attemptCount                                        int
	}
	rows, err := tx.Query(ctx, `
		select b.id::text,e.id::text,b.control_generation,e.control_generation,e.status,
		       coalesce(nullif(e.last_result_summary,''),nullif(m.content,''),''),e.failure_summary,
		       coalesce(t.provider,''),coalesce(t.model,''),coalesce(t.provider_response_id,''),b.attempt_count
		from steward_activity_batches b
		join steward_agent_episodes e on e.id=b.episode_id
		left join steward_conversation_messages m on m.id=e.final_message_id
		left join lateral (
			select provider,model,provider_response_id from steward_agent_turns
			where episode_id=e.id order by round_index desc limit 1
		) t on true
		where b.status='executing' and e.status in ('completed','failed','blocked','cancelled')
		order by b.updated_at,b.id for update of b skip locked limit $1
	`, limit)
	if err != nil {
		return ActivityBatchReconcileResult{}, err
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.batchID, &item.episodeID, &item.batchGeneration,
			&item.episodeGeneration, &item.episodeStatus, &item.summary, &item.failure,
			&item.provider, &item.model, &item.responseID, &item.attemptCount); err != nil {
			rows.Close()
			return ActivityBatchReconcileResult{}, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ActivityBatchReconcileResult{}, err
	}
	rows.Close()
	result := ActivityBatchReconcileResult{Scanned: len(candidates)}
	for _, item := range candidates {
		var tag pgconn.CommandTag
		switch item.episodeStatus {
		case agentEpisodeCompleted:
			tag, err = tx.Exec(ctx, `
				update steward_activity_batches set status='completed',completed_at=$6,lease_owner='',
				lease_expires_at=null,provider=$7,model=$8,provider_response_id=$9,response_summary=$10,
				error_code='',error_summary='',updated_at=$6
				where id=$1 and status='executing' and episode_id=$2 and control_generation=$3
				  and exists(select 1 from steward_agent_episodes where id=$2 and status=$4 and control_generation=$5)
			`, item.batchID, item.episodeID, item.batchGeneration, item.episodeStatus,
				item.episodeGeneration, now, item.provider, item.model, item.responseID,
				truncateAdvisorText(item.summary, 1000))
		case agentEpisodeFailed, agentEpisodeBlocked:
			nextAttempt := now.Add(activityBatchRetryDelay(item.attemptCount))
			tag, err = tx.Exec(ctx, `
				update steward_activity_batches set status='waiting_model',next_attempt_at=$6,lease_owner='',
				lease_expires_at=null,checkpoint=checkpoint || jsonb_build_object(
				  'last_episode_id',$2::text,'last_episode_status',$4,'last_episode_generation',$5),
				error_code=$7,error_summary=$8,updated_at=$9
				where id=$1 and status='executing' and episode_id=$2 and control_generation=$3
				  and exists(select 1 from steward_agent_episodes where id=$2 and status=$4 and control_generation=$5)
			`, item.batchID, item.episodeID, item.batchGeneration, item.episodeStatus,
				item.episodeGeneration, nextAttempt, "MODEL_EPISODE_"+strings.ToUpper(item.episodeStatus),
				truncateAdvisorText(defaultString(item.failure, item.summary), 1000), now)
		case agentEpisodeCancelled:
			tag, err = tx.Exec(ctx, `
				update steward_activity_batches set status='cancelled',completed_at=$6,lease_owner='',
				lease_expires_at=null,error_code='EPISODE_CANCELLED',error_summary=$7,updated_at=$6
				where id=$1 and status='executing' and episode_id=$2 and control_generation=$3
				  and exists(select 1 from steward_agent_episodes where id=$2 and status=$4 and control_generation=$5)
			`, item.batchID, item.episodeID, item.batchGeneration, item.episodeStatus,
				item.episodeGeneration, now, truncateAdvisorText(defaultString(item.failure, item.summary), 1000))
		}
		if err != nil {
			return result, err
		}
		if tag.RowsAffected() != 1 {
			result.Skipped++
			continue
		}
		switch item.episodeStatus {
		case agentEpisodeCompleted:
			result.Completed++
		case agentEpisodeFailed, agentEpisodeBlocked:
			result.WaitingModel++
		case agentEpisodeCancelled:
			result.Cancelled++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func activityBatchRetryDelay(attemptCount int) time.Duration {
	if attemptCount < 1 {
		attemptCount = 1
	}
	shift := attemptCount - 1
	if shift > 8 {
		shift = 8
	}
	delay := 15 * time.Second * time.Duration(1<<shift)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func (s *Service) RetryActivityBatch(ctx context.Context, id, worker string, expectedGeneration int64, code string, cause error, next time.Time) error {
	message := ""
	if cause != nil {
		message = truncateAdvisorText(cause.Error(), 1000)
	}
	status := "failed"
	if strings.HasPrefix(code, "MODEL_") {
		status = "waiting_model"
	}
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_activity_batches set status=$3,next_attempt_at=$4,lease_owner='',lease_expires_at=null,
		       error_code=$5,error_summary=$6,updated_at=now()
		where id=$1 and status='processing' and lease_owner=$2 and control_generation=$7
	`, id, worker, status, next.UTC(), code, message, expectedGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("activity batch lease was lost before retry")
	}
	return nil
}

func (s *Service) ActivityPipelineStatus(ctx context.Context, now time.Time) (ActivityPipelineStatus, error) {
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return ActivityPipelineStatus{}, err
	}
	out := ActivityPipelineStatus{Enabled: settings.Enabled, Mode: settings.Mode, Sources: []ActivitySourceStatus{}, UpdatedAt: now.UTC()}
	rows, err := s.db.Pool.Query(ctx, `
		select device_id,collector_name,source_key,execution_target,watcher,host,event_type,
		       interactive_session_id,status,cursor,capabilities,api_version,
		       last_poll_at,last_source_event_at,last_ingested_at,backlog_count,max_expected_lag_seconds,last_error
		from steward_collection_source_states order by collector_name,source_key
	`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item ActivitySourceStatus
		if err := rows.Scan(&item.DeviceID, &item.CollectorName, &item.SourceKey, &item.ExecutionTarget,
			&item.Watcher, &item.Host, &item.EventType, &item.InteractiveSessionID,
			&item.Status, &item.Cursor, &item.Capabilities, &item.APIVersion, &item.LastPollAt, &item.LastSourceEventAt,
			&item.LastIngestedAt, &item.BacklogCount, &item.MaxExpectedLagSeconds, &item.LastError); err != nil {
			rows.Close()
			return out, err
		}
		classifyActivitySourceHealth(&item, now)
		out.Sources = append(out.Sources, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*) filter(where status='pending'),count(*) filter(where status in ('processing','executing')),
		       count(*) filter(where status='waiting_model'),count(*) filter(where status='failed'),max(completed_at)
		from steward_activity_batches
	`).Scan(&out.PendingBatches, &out.ProcessingBatches, &out.WaitingModel, &out.FailedBatches, &out.LastBatchAt); err != nil {
		return out, err
	}
	return out, nil
}

func classifyActivitySourceHealth(item *ActivitySourceStatus, now time.Time) {
	if item == nil {
		return
	}
	maxLag := time.Duration(item.MaxExpectedLagSeconds) * time.Second
	if maxLag <= 0 {
		maxLag = 5 * time.Minute
	}
	statusHealthy := strings.EqualFold(strings.TrimSpace(item.Status), "healthy") && strings.TrimSpace(item.LastError) == ""
	item.Reachable = statusHealthy && activitySourceTimestampFresh(item.LastPollAt, now, maxLag)
	item.SourceFresh = activitySourceTimestampFresh(item.LastSourceEventAt, now, maxLag)
	item.IngestionFresh = activitySourceTimestampFresh(item.LastIngestedAt, now, maxLag)
	// Fresh is the end-to-end signal retained for existing callers. A successful
	// poll alone only proves reachability; it cannot stand in for bucket data.
	// The synthetic server row is a connectivity probe rather than an event
	// stream, so reachability is its complete health contract.
	if item.SourceKey == "server" && strings.TrimSpace(item.EventType) == "" {
		item.Fresh = item.Reachable
	} else {
		item.Fresh = item.Reachable && item.SourceFresh
	}
}

func activitySourceTimestampFresh(value *time.Time, now time.Time, maxLag time.Duration) bool {
	if value == nil || maxLag <= 0 {
		return false
	}
	age := now.UTC().Sub(value.UTC())
	return age >= -maxLag && age <= maxLag
}
