package steward

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCompanionSourceStateAndActivityBatchPersistence(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	service.registerRuntimeR2Tools()
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(-15 * time.Minute)
	ended := now.Add(-14 * time.Minute)
	sessionID := "windows-test-" + uuid.NewString()
	eventKey := "windows:foreground:" + sessionID

	input := CreateObservationInput{
		Source: "companion:windows-activity", Type: "foreground_window", Summary: "Code · activity batch test",
		SourceEventKey: eventKey, SourceRevision: 1, InteractiveSessionID: sessionID,
		SourceTimezone: "UTC",
		ContextKey:     "code|activity batch test", OccurredAt: &started, EndedAt: &ended,
		Metadata: map[string]any{"companion_outbox_backlog": 2, "capture_interval_seconds": 10},
	}
	observation, err := service.CreateObservation(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_collection_source_states where collector_name='companion:windows-activity' and source_key=$1`, sessionID+":foreground_window")
		_, _ = db.Pool.Exec(ctx, `delete from steward_observations where id=$1 and occurred_at=$2`, observation.ID, observation.OccurredAt)
	})

	newEnd := ended.Add(time.Minute)
	input.SourceRevision = 2
	input.EndedAt = &newEnd
	input.Metadata["companion_outbox_backlog"] = 0
	merged, err := service.CreateObservation(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if merged.ID != observation.ID || merged.EndedAt == nil || !merged.EndedAt.Equal(newEnd) {
		t.Fatalf("source revision was not merged: first=%#v merged=%#v", observation, merged)
	}
	var status, interactiveSessionID string
	var backlog int64
	var lastSourceAt, lastIngestedAt time.Time
	if err := db.Pool.QueryRow(ctx, `
		select status,interactive_session_id,backlog_count,last_source_event_at,last_ingested_at
		from steward_collection_source_states
		where collector_name='companion:windows-activity' and source_key=$1
	`, sessionID+":foreground_window").Scan(&status, &interactiveSessionID, &backlog, &lastSourceAt, &lastIngestedAt); err != nil {
		t.Fatal(err)
	}
	if status != "healthy" || interactiveSessionID != sessionID || backlog != 0 || !lastSourceAt.Equal(newEnd) || lastIngestedAt.IsZero() {
		t.Fatalf("unexpected Companion source state: status=%s session=%s backlog=%d source=%s ingested=%s",
			status, interactiveSessionID, backlog, lastSourceAt, lastIngestedAt)
	}

	batchDevice := "batch-test-" + uuid.NewString()
	sessionItem := activitySessionForBatch{
		ID: uuid.NewString(), DeviceID: batchDevice, Title: "Code", Summary: "activity batch test",
		Source: "companion:windows-activity", CanonicalContext: "code", ObservationCount: 2,
		Revision: 1, SourceCount: 1, ActiveSeconds: 60, StartedAt: started, EndedAt: newEnd, UpdatedAt: now,
	}
	batch, created, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{sessionItem}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first immutable activity batch was not created")
	}
	catalogGeneration := service.runtimeTools.generationValue()
	if catalogGeneration <= 0 || batch.CatalogGeneration != catalogGeneration {
		t.Fatalf("batch catalog generation=%d want live registry generation %d", batch.CatalogGeneration, catalogGeneration)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where device_id=$1`, batchDevice)
	})
	if _, duplicate, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{sessionItem}, now); err != nil || duplicate {
		t.Fatalf("idempotent batch rebuild duplicate=%v err=%v", duplicate, err)
	}
	batchContext, err := service.GetActivityBatchContext(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionEvidence, ok := activityBatchContextItem(batchContext, "activity_session", sessionItem.ID)
	if batchContext.Batch.ContextHash == "" || !ok ||
		sessionEvidence.SourceRevision != int64(sessionItem.Revision) || !sessionEvidence.SourceUpdatedAt.Equal(sessionItem.UpdatedAt) {
		t.Fatalf("unexpected immutable batch context: %#v", batchContext)
	}

	if _, err := db.Pool.Exec(ctx, `update steward_activity_batches set status='completed',completed_at=$2 where id=$1`, batch.ID, now); err != nil {
		t.Fatal(err)
	}
	revised := sessionItem
	revised.Revision = 2
	revised.ObservationCount = 3
	revised.UpdatedAt = now.Add(time.Second)
	second, created, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{revised}, now.Add(2*time.Second))
	if err != nil || !created {
		t.Fatalf("late session revision did not create a successor: created=%v err=%v", created, err)
	}
	if second.Revision != 2 || second.SupersedesID == nil || *second.SupersedesID != batch.ID {
		t.Fatalf("unexpected successor metadata: %+v", second)
	}
	wantSecondKey := fmt.Sprintf("activity-batch:%s:%s:%s:%d:2", batchDevice,
		started.UTC().Format(time.RFC3339Nano), newEnd.UTC().Format(time.RFC3339Nano), catalogGeneration)
	if second.IdempotencyKey != wantSecondKey {
		t.Fatalf("successor idempotency key=%q want %q", second.IdempotencyKey, wantSecondKey)
	}
	firstAfterRevision, err := service.GetActivityBatch(ctx, batch.ID)
	if err != nil || firstAfterRevision.Status != "superseded" {
		t.Fatalf("completed predecessor was not superseded: batch=%+v err=%v", firstAfterRevision, err)
	}
	if _, duplicate, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{revised}, now.Add(3*time.Second)); err != nil || duplicate {
		t.Fatalf("same revised snapshot duplicated: created=%v err=%v", duplicate, err)
	}

	if _, err := db.Pool.Exec(ctx, `update steward_activity_batches set status='processing',lease_owner='revision-worker' where id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	thirdSnapshot := revised
	thirdSnapshot.Revision = 3
	thirdSnapshot.UpdatedAt = now.Add(4 * time.Second)
	if current, created, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{thirdSnapshot}, now.Add(5*time.Second)); err != nil || created || current.ID != second.ID {
		t.Fatalf("processing batch was unsafely replaced: current=%+v created=%v err=%v", current, created, err)
	}
	var batchCount int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_activity_batches where device_id=$1`, batchDevice).Scan(&batchCount); err != nil || batchCount != 2 {
		t.Fatalf("processing deferral batch count=%d err=%v", batchCount, err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_activity_batches set status='executing' where id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if current, created, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{thirdSnapshot}, now.Add(6*time.Second)); err != nil || created || current.ID != second.ID {
		t.Fatalf("executing batch was unsafely replaced: current=%+v created=%v err=%v", current, created, err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_activity_batches set status='completed',completed_at=$2,lease_owner='' where id=$1`, second.ID, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	third, created, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{thirdSnapshot}, now.Add(7*time.Second))
	if err != nil || !created || third.Revision != 3 || third.SupersedesID == nil || *third.SupersedesID != second.ID {
		t.Fatalf("deferred successor was not created after completion: batch=%+v created=%v err=%v", third, created, err)
	}
	service.runtimeTools.register(newDispatchBarrierRuntimeTool())
	newGeneration := service.runtimeTools.generationValue()
	regenerated, created, err := service.persistActivityBatch(ctx, batchDevice, "interval", started, newEnd, []activitySessionForBatch{thirdSnapshot}, now.Add(8*time.Second))
	if err != nil || !created {
		t.Fatalf("catalog change did not materialize a new batch: batch=%+v created=%v err=%v", regenerated, created, err)
	}
	if regenerated.CatalogGeneration != newGeneration || regenerated.Revision != 1 || regenerated.ID == third.ID {
		t.Fatalf("catalog generation was not part of batch identity: old=%+v new=%+v", third, regenerated)
	}
}

func TestActivityBatchPersistsDeterministicMultiSourceContext(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	now := time.Now().UTC().Truncate(time.Second)
	windowStart, windowEnd := now.Add(-20*time.Minute), now.Add(-10*time.Minute)
	deviceID := "multi-source-batch-" + uuid.NewString()
	sessionID, eventID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	reminderID, notificationID := uuid.NewString(), uuid.NewString()
	previousProfileID, currentProfileID := uuid.NewString(), uuid.NewString()

	if _, err := db.Pool.Exec(ctx, `
		insert into steward_events(id,type,title,summary,source,data_level,status,device_id,created_at,updated_at,version)
		values($1,'calendar','专注时间','按计划完成','calendar','D0','confirmed',$2,$3,$3,3)
	`, eventID, deviceID, windowStart.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_tasks(id,type,title,description,status,priority,due_at,source,data_level,device_id,created_at,updated_at,version)
		values($1,'planned','整理日报','汇总今天的活动','open','normal',$2,'calendar','D0',$3,$4,$4,5)
	`, taskID, now.Add(time.Hour), deviceID, windowStart.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, notification := range []struct {
		id, category, status, title string
		scheduledAt                 time.Time
	}{{reminderID, "reminder", "queued", "喝水提醒", windowStart.Add(-time.Minute)},
		{notificationID, "general", "sent", "后台任务完成", windowStart.Add(4 * time.Minute)}} {
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_notifications(
				id,source_type,title,body,category,priority,status,scheduled_at,schedule_revision,created_at,updated_at
			) values($1,'test',$2,'待处理内容',$3,'normal',$4,$5,2,$6,$6)
		`, notification.id, notification.title, notification.category, notification.status,
			notification.scheduledAt, windowStart.Add(-2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var firstProfileRevision int64
	if err := db.Pool.QueryRow(ctx, `
		select coalesce(max(revision),0)+1 from steward_profile_snapshots
		where profile_scope='default' and view='recent'
	`).Scan(&firstProfileRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_profile_snapshots(
			id,profile_scope,view,revision,as_of,window_end,document,fact_count,content_hash,created_at
		) values($1,'default','recent',$2,$3,$3,$4::jsonb,1,$5,$3)
	`, previousProfileID, firstProfileRevision, windowStart.Add(-time.Minute), `{"focus":"reading"}`, "profile-previous-"+previousProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_profile_snapshots(
			id,profile_scope,view,revision,as_of,window_end,document,fact_count,content_hash,supersedes_id,created_at
		) values($1,'default','recent',$2,$3,$3,$4::jsonb,2,$5,$6,$3)
	`, currentProfileID, firstProfileRevision+1, windowStart.Add(5*time.Minute),
		`{"focus":"coding","break_preference":"short"}`, "profile-current-"+currentProfileID, previousProfileID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where device_id=$1`, deviceID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_events where id=$1`, eventID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_tasks where id=$1`, taskID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_notifications where id in ($1,$2)`, reminderID, notificationID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_profile_snapshots where id=$1`, currentProfileID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_profile_snapshots where id=$1`, previousProfileID)
	})

	session := activitySessionForBatch{
		ID: sessionID, DeviceID: deviceID, Title: "Editor", Summary: "multi-source context",
		Source: "test", CanonicalContext: "editor", ObservationCount: 2, Revision: 4,
		SourceCount: 1, ActiveSeconds: 60, StartedAt: windowStart.Add(time.Minute),
		EndedAt: windowStart.Add(2 * time.Minute), UpdatedAt: windowEnd,
	}
	batch, created, err := service.persistActivityBatch(ctx, deviceID, "interval", windowStart, windowEnd, []activitySessionForBatch{session}, now)
	if err != nil || !created {
		t.Fatalf("persist multi-source batch: created=%v err=%v", created, err)
	}
	batchContext, err := service.GetActivityBatchContext(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"activity_session": sessionID,
		"event_change":     eventID,
		"task_change":      taskID,
		"reminder":         reminderID,
		"notification":     notificationID,
		"profile_snapshot": currentProfileID,
		"profile_diff":     currentProfileID,
	}
	seen := map[string]bool{}
	for index, item := range batchContext.Items {
		if item.Ordinal != index {
			t.Fatalf("non-deterministic ordinal at %d: %+v", index, item)
		}
		if item.SourceType != item.ItemType || item.SourceID != item.ItemID || !item.SourceTime.Equal(item.ItemOccurredAt) {
			t.Fatalf("source aliases do not identify persisted evidence: %+v", item)
		}
		if item.SourceTime.IsZero() || item.SourceUpdatedAt.IsZero() || item.SnapshotHash == "" || strings.TrimSpace(item.EvidenceSummary) == "" {
			t.Fatalf("incomplete persisted evidence: %+v", item)
		}
		if expectedID, ok := expected[item.SourceType]; ok && expectedID == item.SourceID {
			seen[item.SourceType] = true
		}
		if index > 0 && activityBatchSourceLess(item, batchContext.Items[index-1]) {
			t.Fatalf("batch items are not deterministically ordered: previous=%+v current=%+v", batchContext.Items[index-1], item)
		}
	}
	for sourceType := range expected {
		if !seen[sourceType] {
			t.Fatalf("missing %s evidence in batch context: %+v", sourceType, batchContext.Items)
		}
	}
	if batch.ContextHash == "" || batch.Statistics["source_type_counts"] == nil {
		t.Fatalf("multi-source batch lacks deterministic statistics: %+v", batch)
	}
	unchanged, duplicate, err := service.persistActivityBatch(ctx, deviceID, "interval", windowStart, windowEnd, []activitySessionForBatch{session}, now.Add(time.Second))
	if err != nil || duplicate || unchanged.ID != batch.ID || unchanged.ContextHash != batch.ContextHash {
		t.Fatalf("same source snapshot was not idempotent: batch=%+v created=%v err=%v", unchanged, duplicate, err)
	}

	taskUpdatedAt := windowEnd.Add(-time.Second)
	if _, err := db.Pool.Exec(ctx, `
		update steward_tasks set title='整理日报（已调整）',version=version+1,updated_at=$2 where id=$1
	`, taskID, taskUpdatedAt); err != nil {
		t.Fatal(err)
	}
	revised, revisedCreated, err := service.persistActivityBatch(ctx, deviceID, "interval", windowStart, windowEnd, []activitySessionForBatch{session}, now.Add(2*time.Second))
	if err != nil || !revisedCreated || revised.Revision != batch.Revision+1 || revised.ContextHash == batch.ContextHash {
		t.Fatalf("task change did not revise multi-source snapshot: batch=%+v created=%v err=%v", revised, revisedCreated, err)
	}
	revisedContext, err := service.GetActivityBatchContext(ctx, revised.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskEvidence, ok := activityBatchContextItem(revisedContext, "task_change", taskID)
	if !ok || taskEvidence.SourceRevision != 6 || !strings.Contains(taskEvidence.EvidenceSummary, "已调整") {
		t.Fatalf("revised task evidence missing: %+v", taskEvidence)
	}
}

func activityBatchContextItem(value ActivityBatchContext, sourceType, sourceID string) (ActivityBatchItem, bool) {
	for _, item := range value.Items {
		if item.ItemType == sourceType && item.ItemID == sourceID {
			return item, true
		}
	}
	return ActivityBatchItem{}, false
}

func activityBatchContextTypeCount(value ActivityBatchContext, sourceType string) int {
	count := 0
	for _, item := range value.Items {
		if item.ItemType == sourceType {
			count++
		}
	}
	return count
}

func activityBatchSourceLess(left, right ActivityBatchItem) bool {
	if left.ItemOccurredAt.Equal(right.ItemOccurredAt) {
		if left.ItemType == right.ItemType {
			return left.ItemID < right.ItemID
		}
		return left.ItemType < right.ItemType
	}
	return left.ItemOccurredAt.Before(right.ItemOccurredAt)
}

func TestActivityBatchRevisionReplaceableStates(t *testing.T) {
	for _, status := range []string{"pending", "completed", "cancelled"} {
		if !activityBatchRevisionReplaceable(status) {
			t.Fatalf("status %s should be safely replaceable", status)
		}
	}
	for _, status := range []string{"processing", "executing", "waiting_model", "failed", "superseded"} {
		if activityBatchRevisionReplaceable(status) {
			t.Fatalf("status %s must retain its immutable snapshot", status)
		}
	}
}

func TestBuildDueActivityBatchesCreatesLateEventRevision(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db, WithAgentID("late-batch-builder"))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	windowStart := now.Truncate(30 * time.Minute).Add(-2 * time.Hour)
	started, ended := windowStart.Add(5*time.Minute), windowStart.Add(10*time.Minute)
	sessionID, unchangedSessionID, deviceID := uuid.NewString(), uuid.NewString(), "late-session-"+uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_sessions(
			id,type,title,summary,source,context_key,device_id,data_level,status,
			observation_count,confidence,value_score,started_at,ended_at,created_at,updated_at,
			revision,canonical_context,active_seconds,afk_seconds,source_count
		) values($1,'foreground_window','Editor','initial snapshot','companion:windows-activity',
			'editor',$2,'D2','closed',1,0.9,0.7,$3,$4,$5,$5,1,'editor',300,0,1)
	`, sessionID, deviceID, started, ended, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_sessions(
			id,type,title,summary,source,context_key,device_id,data_level,status,
			observation_count,confidence,value_score,started_at,ended_at,created_at,updated_at,
			revision,canonical_context,active_seconds,afk_seconds,source_count
		) values($1,'foreground_window','Browser','unchanged window evidence','companion:windows-activity',
			'browser',$2,'D2','closed',1,0.9,0.7,$3,$4,$5,$5,1,'browser',180,0,1)
	`, unchangedSessionID, deviceID, started.Add(time.Minute), ended.Add(-time.Minute), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where device_id=$1`, deviceID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_sessions where id in ($1,$2)`, sessionID, unchangedSessionID)
	})

	created, err := service.BuildDueActivityBatches(ctx, now)
	if err != nil || len(created) != 1 {
		t.Fatalf("initial build batches=%+v err=%v", created, err)
	}
	first := created[0]
	if _, err := db.Pool.Exec(ctx, `update steward_activity_batches set status='completed',completed_at=$2 where id=$1`, first.ID, now); err != nil {
		t.Fatal(err)
	}
	lateUpdateAt := now.Add(time.Second)
	if _, err := db.Pool.Exec(ctx, `
		update steward_activity_sessions set revision=2,observation_count=2,
			summary='late event merged',updated_at=$2 where id=$1
	`, sessionID, lateUpdateAt); err != nil {
		t.Fatal(err)
	}

	created, err = service.BuildDueActivityBatches(ctx, now.Add(2*time.Second))
	if err != nil || len(created) != 1 {
		t.Fatalf("late-event build batches=%+v err=%v", created, err)
	}
	second := created[0]
	if second.Revision != 2 || second.SupersedesID == nil || *second.SupersedesID != first.ID {
		t.Fatalf("late-event successor=%+v", second)
	}
	old, err := service.GetActivityBatch(ctx, first.ID)
	if err != nil || old.Status != "superseded" {
		t.Fatalf("old batch status=%s err=%v", old.Status, err)
	}
	context, err := service.GetActivityBatchContext(ctx, second.ID)
	if err != nil || activityBatchContextTypeCount(context, "activity_session") != 2 {
		t.Fatalf("late-event batch snapshot=%+v err=%v", context, err)
	}
	revisions := map[string]int64{}
	for _, item := range context.Items {
		revisions[item.ItemID] = item.SourceRevision
	}
	if revisions[sessionID] != 2 || revisions[unchangedSessionID] != 1 {
		t.Fatalf("successor did not retain the full logical window: revisions=%v", revisions)
	}
	for _, item := range context.Items {
		if item.ItemID == sessionID && !item.SourceUpdatedAt.Equal(lateUpdateAt) {
			t.Fatalf("late-event marker=%s want %s", item.SourceUpdatedAt, lateUpdateAt)
		}
	}
	if duplicate, err := service.BuildDueActivityBatches(ctx, now.Add(3*time.Second)); err != nil || len(duplicate) != 0 {
		t.Fatalf("unchanged late snapshot rebuilt: batches=%+v err=%v", duplicate, err)
	}
}

func TestActivityBatchMigrationBackfillsLegacySnapshotMarkers(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	batchID, itemID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_batches(
			id,device_id,window_start,window_end,status,due_at,next_attempt_at,idempotency_key
		) values($1,'legacy-batch',$2,$3,'completed',$3,$3,$4)
	`, batchID, now.Add(-time.Hour), now, "legacy:"+batchID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where id=$1`, batchID) })
	legacySnapshot := sessionID + ":7:" + now.Format(time.RFC3339Nano)
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_batch_items(
			id,batch_id,item_type,item_id,item_occurred_at,snapshot_hash
		) values($1,$2,'activity_session',$3,$4,$5)
	`, itemID, batchID, sessionID, now.Add(-time.Hour), legacySnapshot); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var revision int64
	var updatedAt time.Time
	if err := db.Pool.QueryRow(ctx, `
		select source_revision,source_updated_at from steward_activity_batch_items where id=$1
	`, itemID).Scan(&revision, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if revision != 7 || !updatedAt.Equal(now) {
		t.Fatalf("legacy snapshot backfill revision=%d updated_at=%s want 7/%s", revision, updatedAt, now)
	}
}

func TestReconcileActivityBatchEpisodesProjectsTerminalState(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	now := time.Now().UTC().Truncate(time.Second)
	conversationID := uuid.NewString()
	triggerID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
		values($1,'activity batch reconcile','active','D0',$2,$2)
	`, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_conversation_messages(id,conversation_id,role,content,data_level,model,created_at)
		values($1,$2,'system','background batch','D0','',$3)
	`, triggerID, conversationID, now); err != nil {
		t.Fatal(err)
	}
	devicePrefix := "reconcile-test-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where device_id like $1`, devicePrefix+"%")
		_, _ = db.Pool.Exec(ctx, `delete from steward_conversations where id=$1`, conversationID)
	})

	statuses := []string{agentEpisodeCompleted, agentEpisodeFailed, agentEpisodeBlocked, agentEpisodeCancelled}
	batchByEpisodeStatus := map[string]string{}
	for _, episodeStatus := range statuses {
		episodeID, turnID, batchID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_agent_episodes(
				id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,status,current_round,
				tool_call_count,max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,
				control_generation,last_result_summary,failure_summary,created_at,updated_at,completed_at
			) values($1,$2,$3,'proactive_activity_batch','summarize activity','D0',$4,1,0,64,256,7200,3,5,$5,$6,$7,$7,$7)
		`, episodeID, conversationID, triggerID, episodeStatus, "episode "+episodeStatus,
			"failure "+episodeStatus, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_agent_turns(id,episode_id,round_index,status,provider,model,provider_response_id,created_at,updated_at,completed_at)
			values($1,$2,1,'final','test-provider','test-model',$3,$4,$4,$4)
		`, turnID, episodeID, "response-"+episodeStatus, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_activity_batches(
				id,device_id,window_start,window_end,status,due_at,next_attempt_at,idempotency_key,
				control_generation,episode_id,attempt_count,lease_owner,lease_expires_at
			) values($1,$2,$3,$4,'executing',$5,$5,$6,7,$7,3,'test-worker',$8)
		`, batchID, devicePrefix+episodeStatus, now.Add(-time.Hour), now.Add(-30*time.Minute), now,
			"reconcile:"+batchID, episodeID, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		batchByEpisodeStatus[episodeStatus] = batchID
	}
	result, err := service.ReconcileActivityBatchEpisodes(ctx, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 || result.WaitingModel != 2 || result.Cancelled != 1 || result.Skipped != 0 {
		t.Fatalf("unexpected reconciliation result: %#v", result)
	}
	for episodeStatus, batchID := range batchByEpisodeStatus {
		var batchStatus, episodeID, provider, responseID string
		var nextAttempt time.Time
		if err := db.Pool.QueryRow(ctx, `
			select status,coalesce(episode_id::text,''),provider,provider_response_id,next_attempt_at
			from steward_activity_batches where id=$1
		`, batchID).Scan(&batchStatus, &episodeID, &provider, &responseID, &nextAttempt); err != nil {
			t.Fatal(err)
		}
		switch episodeStatus {
		case agentEpisodeCompleted:
			if batchStatus != "completed" || episodeID == "" || provider != "test-provider" || responseID != "response-completed" {
				t.Fatalf("completed projection status=%s episode=%s provider=%s response=%s", batchStatus, episodeID, provider, responseID)
			}
		case agentEpisodeFailed, agentEpisodeBlocked:
			if batchStatus != "waiting_model" || episodeID == "" || !nextAttempt.After(now) {
				t.Fatalf("retry projection for %s: status=%s episode=%s next=%s", episodeStatus, batchStatus, episodeID, nextAttempt)
			}
		case agentEpisodeCancelled:
			if batchStatus != "cancelled" || episodeID == "" {
				t.Fatalf("cancel projection status=%s episode=%s", batchStatus, episodeID)
			}
		}
	}
}

func TestClaimActivityBatchHonorsDevicePredecessor(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	now := time.Now().UTC().Truncate(time.Second)
	deviceID := "ordered-batch-" + uuid.NewString()
	olderID, newerID := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where device_id=$1`, deviceID)
	})
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_batches(
			id,device_id,window_start,window_end,status,due_at,next_attempt_at,idempotency_key,created_at,updated_at
		) values
			($1,$3,$4,$5,'waiting_model',$6,$7,$8,$4,$4),
			($2,$3,$5,$9,'pending',$6,$6,$10,$5,$5)
	`, olderID, newerID, deviceID, now.Add(-2*time.Hour), now.Add(-time.Hour), now.Add(-time.Minute),
		now.Add(time.Hour), "ordered:"+olderID, now, "ordered:"+newerID); err != nil {
		t.Fatal(err)
	}

	claimed, err := service.ClaimActivityBatch(ctx, "ordered-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Fatalf("newer batch bypassed a backoff predecessor: %+v", claimed)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_activity_batches set next_attempt_at=$2 where id=$1`, olderID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	claimed, err = service.ClaimActivityBatch(ctx, "ordered-worker", time.Minute)
	if err != nil || claimed == nil || claimed.ID != olderID {
		t.Fatalf("claim older batch=%+v err=%v", claimed, err)
	}
	if err := service.CompleteActivityBatch(ctx, claimed.ID, "ordered-worker", claimed.ControlGeneration, "done"); err != nil {
		t.Fatal(err)
	}
	claimed, err = service.ClaimActivityBatch(ctx, "ordered-worker", time.Minute)
	if err != nil || claimed == nil || claimed.ID != newerID {
		t.Fatalf("claim successor batch=%+v err=%v", claimed, err)
	}
}

// The first tool execution commits its business write while its invocation is
// intentionally left in "running", which models a crash before the runtime
// persisted the receipt. Executing a retry invocation must return the original
// notification/policy instead of duplicating either side effect.
func TestActivityBatchSideEffectsSurviveReceiptCrash(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	service.registerRuntimeR2Tools()
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	conversationID, triggerID := uuid.NewString(), uuid.NewString()
	episodeID, turnID, batchID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	runID, executionID := uuid.NewString(), uuid.NewString()
	notifyStepID, policyStepID := uuid.NewString(), uuid.NewString()
	taskStepID, memoryStepID := uuid.NewString(), uuid.NewString()
	notifyCallID, policyCallID := "notify-call-"+uuid.NewString(), "policy-call-"+uuid.NewString()
	taskCallID, memoryCallID := "task-call-"+uuid.NewString(), "memory-call-"+uuid.NewString()
	category := "receipt-crash-" + uuid.NewString()
	notifyEffectKey := fmt.Sprintf("batch:%s:tool:%s", batchID, notifyCallID)
	policyEffectKey := fmt.Sprintf("batch:%s:tool:%s", batchID, policyCallID)
	taskEffectKey := fmt.Sprintf("batch:%s:tool:%s", batchID, taskCallID)
	memoryEffectKey := fmt.Sprintf("batch:%s:tool:%s", batchID, memoryCallID)
	taskRecordID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(taskEffectKey)).String()
	memoryRecordID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(memoryEffectKey)).String()

	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_notifications where dedupe_key=$1`, notifyEffectKey)
		_, _ = db.Pool.Exec(ctx, `delete from steward_reminder_policies where idempotency_key=$1`, policyEffectKey)
		_, _ = db.Pool.Exec(ctx, `delete from steward_tasks where id=$1`, taskRecordID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_memories where id=$1`, memoryRecordID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_batches where id=$1`, batchID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_conversations where id=$1`, conversationID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_agent_runs where id=$1`, runID)
	})
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
		values($1,'batch receipt crash','active','D0',$2,$2)`, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,created_at)
		values($1,$2,'system','activity batch','D0',$3)`, triggerID, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_episodes(
		id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,visibility,context_ref_type,
		context_ref_id,result_sink,idempotency_key,status,current_round,created_at,updated_at)
		values($1,$2,$3,'activity_batch','test replay-safe side effects','D0','background','activity_batch',
		$4,'database',$5,'executing',1,$6,$6)`, episodeID, conversationID, triggerID, batchID, "episode:"+batchID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_activity_batches(
		id,device_id,window_start,window_end,status,due_at,next_attempt_at,idempotency_key,episode_id,created_at,updated_at)
		values($1,$2,$3,$4,'executing',$5,$5,$6,$7,$5,$5)`, batchID, "receipt-device-"+batchID,
		now.Add(-time.Hour), now.Add(-30*time.Minute), now, "batch:"+batchID, episodeID); err != nil {
		t.Fatal(err)
	}
	toolCalls, _ := json.Marshal([]map[string]any{
		{"id": notifyCallID, "tool_name": "notify.send", "arguments": map[string]any{"title": "test", "body": "test"}},
		{"id": policyCallID, "tool_name": "steward.reminder_policy.update", "arguments": map[string]any{"policy": map[string]any{"cooldown_seconds": 600}}},
		{"id": taskCallID, "tool_name": "steward.create_task", "arguments": map[string]any{"title": "batch task"}},
		{"id": memoryCallID, "tool_name": "steward.save_memory", "arguments": map[string]any{"title": "batch memory", "content": "remember once"}},
	})
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_turns(id,episode_id,round_index,status,tool_calls,created_at,updated_at)
		values($1,$2,1,'tools_running',$3::jsonb,$4,$4)`, turnID, episodeID, string(toolCalls), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_runs(id,goal,status,plan_hash,idempotency_key,created_at,updated_at,started_at)
		values($1,'batch side effects','running',$2,$3,$4,$4,$4)`, runID, "plan:"+runID, "run:"+runID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_run_steps(
		id,run_id,step_key,position,title,tool_name,tool_version,status,idempotency_key,tool_idempotency,created_at,updated_at,started_at)
		values($1,$3,'tool_1',0,'notify','notify.send','5.2.0','running',$4,'keyed',$5,$5,$5),
		      ($2,$3,'tool_2',1,'policy','steward.reminder_policy.update','5.3.0','running',$6,'keyed',$5,$5,$5),
		      ($7,$3,'tool_3',2,'task','steward.create_task','4.7.0','running',$8,'keyed',$5,$5,$5),
		      ($9,$3,'tool_4',3,'memory','steward.save_memory','4.7.0','running',$10,'keyed',$5,$5,$5)
	`, notifyStepID, policyStepID, runID, "step:"+notifyStepID, now, "step:"+policyStepID,
		taskStepID, "step:"+taskStepID, memoryStepID, "step:"+memoryStepID); err != nil {
		t.Fatal(err)
	}
	invocations := map[string][2]string{
		"notify": {uuid.NewString(), uuid.NewString()},
		"policy": {uuid.NewString(), uuid.NewString()},
		"task":   {uuid.NewString(), uuid.NewString()},
		"memory": {uuid.NewString(), uuid.NewString()},
	}
	for kind, ids := range invocations {
		stepID, toolName, toolVersion := notifyStepID, "notify.send", "5.2.0"
		switch kind {
		case "policy":
			stepID, toolName, toolVersion = policyStepID, "steward.reminder_policy.update", "5.3.0"
		case "task":
			stepID, toolName, toolVersion = taskStepID, "steward.create_task", "4.7.0"
		case "memory":
			stepID, toolName, toolVersion = memoryStepID, "steward.save_memory", "4.7.0"
		}
		for index, invocationID := range ids {
			if _, err := db.Pool.Exec(ctx, `insert into steward_tool_invocations(
				id,run_id,step_id,tool_name,tool_version,attempt,idempotency_key,status,started_at)
				values($1,$2,$3,$4,$5,$6,$7,'running',$8)`, invocationID, runID, stepID, toolName,
				toolVersion, index+1, fmt.Sprintf("invocation:%s:%d", kind, index+1), now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_executions(
		id,conversation_id,message_id,request_message_id,instruction,kind,status,run_id,episode_id,turn_id,round_index,created_at,updated_at)
		values($1,$2,$3,$3,'batch side effects','run','running',$4,$5,$6,1,$7,$7)`, executionID,
		conversationID, triggerID, runID, episodeID, turnID, now); err != nil {
		t.Fatal(err)
	}

	policyTool, ok := service.runtimeTools.get("steward.reminder_policy.update")
	if !ok {
		t.Fatal("reminder policy runtime tool is missing")
	}
	policyInput := map[string]any{"profile_scope": "default", "category": category,
		"policy": map[string]any{"cooldown_seconds": 600}, "rationale": "receipt crash replay"}
	firstPolicy, err := policyTool.Execute(withRuntimeInvocationID(ctx, invocations["policy"][0]), policyInput)
	if err != nil {
		t.Fatal(err)
	}
	secondPolicy, err := policyTool.Execute(withRuntimeInvocationID(ctx, invocations["policy"][1]), policyInput)
	if err != nil {
		t.Fatal(err)
	}
	if firstPolicy.Output["id"] == "" || firstPolicy.Output["id"] != secondPolicy.Output["id"] {
		t.Fatalf("policy replay created a different result: first=%v second=%v", firstPolicy.Output, secondPolicy.Output)
	}
	var policyCount int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_reminder_policies where idempotency_key=$1`, policyEffectKey).Scan(&policyCount); err != nil || policyCount != 1 {
		t.Fatalf("policy side-effect count=%d err=%v", policyCount, err)
	}

	notifyTool, ok := service.runtimeTools.get("notify.send")
	if !ok {
		t.Fatal("notification runtime tool is missing")
	}
	notifyInput := map[string]any{"title": "receipt crash", "body": "must be delivered once", "category": category,
		"dedupe_key": "model-selected-key", "decision_context": map[string]any{"reason": "test"}}
	firstNotification, err := notifyTool.Execute(withRuntimeInvocationID(ctx, invocations["notify"][0]), notifyInput)
	if err != nil {
		t.Fatal(err)
	}
	secondNotification, err := notifyTool.Execute(withRuntimeInvocationID(ctx, invocations["notify"][1]), notifyInput)
	if err != nil {
		t.Fatal(err)
	}
	if firstNotification.Output["id"] == "" || firstNotification.Output["id"] != secondNotification.Output["id"] {
		t.Fatalf("notification replay created a different result: first=%v second=%v", firstNotification.Output, secondNotification.Output)
	}
	var notificationCount int
	var requestedKey, storedEffectKey string
	if err := db.Pool.QueryRow(ctx, `select count(*),coalesce(max(decision_context->>'requested_dedupe_key'),''),
		coalesce(max(decision_context->>'activity_batch_effect_key'),'') from steward_notifications where dedupe_key=$1`, notifyEffectKey).
		Scan(&notificationCount, &requestedKey, &storedEffectKey); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || requestedKey != "model-selected-key" || storedEffectKey != notifyEffectKey {
		t.Fatalf("notification effect count=%d requested=%q stored=%q", notificationCount, requestedKey, storedEffectKey)
	}

	taskTool, ok := service.runtimeTools.get("steward.create_task")
	if !ok {
		t.Fatal("task runtime tool is missing")
	}
	firstTask, err := taskTool.Execute(withRuntimeInvocationID(ctx, invocations["task"][0]), map[string]any{"title": "batch task"})
	if err != nil {
		t.Fatal(err)
	}
	secondTask, err := taskTool.Execute(withRuntimeInvocationID(ctx, invocations["task"][1]), map[string]any{"title": "batch task"})
	if err != nil {
		t.Fatal(err)
	}
	if firstTask.Output["id"] != taskRecordID || secondTask.Output["id"] != taskRecordID {
		t.Fatalf("task replay was not stable: first=%v second=%v want=%s", firstTask.Output, secondTask.Output, taskRecordID)
	}
	var taskCount int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_tasks where id=$1`, taskRecordID).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("task side-effect count=%d err=%v", taskCount, err)
	}

	memoryTool, ok := service.runtimeTools.get("steward.save_memory")
	if !ok {
		t.Fatal("memory runtime tool is missing")
	}
	memoryInput := map[string]any{"title": "batch memory", "content": "remember once"}
	firstMemory, err := memoryTool.Execute(withRuntimeInvocationID(ctx, invocations["memory"][0]), memoryInput)
	if err != nil {
		t.Fatal(err)
	}
	secondMemory, err := memoryTool.Execute(withRuntimeInvocationID(ctx, invocations["memory"][1]), memoryInput)
	if err != nil {
		t.Fatal(err)
	}
	if firstMemory.Output["id"] != memoryRecordID || secondMemory.Output["id"] != memoryRecordID {
		t.Fatalf("memory replay was not stable: first=%v second=%v want=%s", firstMemory.Output, secondMemory.Output, memoryRecordID)
	}
	var memoryCount int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_memories where id=$1`, memoryRecordID).Scan(&memoryCount); err != nil || memoryCount != 1 {
		t.Fatalf("memory side-effect count=%d err=%v", memoryCount, err)
	}
}
