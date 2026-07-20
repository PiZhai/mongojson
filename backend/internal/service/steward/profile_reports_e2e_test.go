package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

func TestProfileReportAndIntelligenceJobPostgresLifecycle(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the profile/report/job Postgres lifecycle test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("profile-report-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureRuntimeToolSpecs(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	job, created, err := service.EnqueueIntelligenceJob(ctx, EnqueueIntelligenceJobInput{
		Kind: intelligenceJobProfileConsolidation, PeriodKey: "2026-07-18", PeriodStart: start,
		PeriodEnd: start.Add(24 * time.Hour), DueAt: start.Add(21 * time.Hour), Input: map[string]any{"bootstrap": true},
	})
	if err != nil || !created {
		t.Fatalf("enqueue profile job: created=%v err=%v", created, err)
	}
	duplicate, created, err := service.EnqueueIntelligenceJob(ctx, EnqueueIntelligenceJobInput{
		Kind: intelligenceJobProfileConsolidation, PeriodKey: "2026-07-18", PeriodStart: start,
		PeriodEnd: start.Add(24 * time.Hour), DueAt: start.Add(21 * time.Hour), Input: map[string]any{"bootstrap": true},
	})
	if err != nil || created || duplicate.ID != job.ID {
		t.Fatalf("idempotent enqueue: duplicate=%+v created=%v err=%v", duplicate, created, err)
	}
	claimed, err := service.ClaimIntelligenceJobs(ctx, "profile-report-e2e-worker", start.Add(22*time.Hour), time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID || claimed[0].Attempts != 1 {
		t.Fatalf("claim profile job: claimed=%+v err=%v", claimed, err)
	}
	job = claimed[0]
	if err := service.DeferIntelligenceJob(ctx, job, fmt.Errorf("temporary model outage")); err != nil {
		t.Fatalf("defer profile job: %v", err)
	}
	deferred, err := service.GetIntelligenceJob(ctx, job.ID)
	if err != nil || deferred.Status != intelligenceJobWaitingModel || deferred.FailureSummary == "" {
		t.Fatalf("deferred profile job=%+v err=%v", deferred, err)
	}

	fact, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{
		Key: "preferred_editor", Value: map[string]any{"name": "VS Code"}, Summary: "用户明确选择 VS Code",
		Horizon: domain.StewardProfileHorizonExplicit, Confidence: 1, UserConfirmed: true,
		CreatedBy: "user", JobID: job.ID, ValidFrom: &start,
	})
	if err != nil {
		t.Fatalf("upsert profile fact: %v", err)
	}
	if fact.Version != 1 || fact.JobID == nil || *fact.JobID != job.ID {
		t.Fatalf("unexpected fact: %+v", fact)
	}
	view, err := service.GetProfileView(ctx)
	if err != nil || view.Merged == nil || len(view.Merged.Facts) != 1 {
		t.Fatalf("new profile fact was not immediately visible: view=%+v err=%v", view, err)
	}
	view, err = service.RebuildProfileSnapshots(ctx, start.Add(23*time.Hour), 14, job.ID)
	if err != nil {
		t.Fatalf("rebuild profile snapshots: %v", err)
	}
	if view.Merged == nil || len(view.Merged.Facts) != 1 {
		t.Fatalf("unexpected merged profile: %+v", view.Merged)
	}

	reportJob, _, err := service.EnqueueIntelligenceJob(ctx, EnqueueIntelligenceJobInput{
		Kind: intelligenceJobReportDaily, PeriodKey: "2026-07-18", PeriodStart: start,
		PeriodEnd: start.Add(21 * time.Hour), DueAt: start.Add(21 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.WriteReport(ctx, WriteReportInput{
		Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-18", PeriodStart: start,
		PeriodEnd: start.Add(21 * time.Hour), Status: reportStatusComplete, Title: "日报", Body: "完成了画像归纳。",
		Silent: true, JobID: reportJob.ID, Evidence: []ProfileEvidenceInput{{SourceType: "profile_fact", SourceID: fact.ID, EvidenceDay: start}},
	})
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	loadedReport, err := service.GetReport(ctx, report.ID)
	if err != nil || loadedReport.Revision != 1 || !loadedReport.Silent || loadedReport.EvidenceCount != 1 {
		t.Fatalf("loaded report=%+v err=%v", loadedReport, err)
	}

	for _, action := range []string{"steward.runtime_status", "steward.collection_status", "steward.activity.query", "steward.background_jobs.list"} {
		tool := runtimeIntelligenceTool{service: service, action: action}
		result, err := tool.Execute(ctx, map[string]any{})
		if err != nil {
			t.Fatalf("execute %s: %v", action, err)
		}
		if len(result.Output) == 0 {
			t.Fatalf("%s returned empty output", action)
		}
	}
}

func TestReportEvidenceCoverageReflectsPersistedSessions(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run report coverage persistence test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("report-coverage-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	coveredID, missingID := uuid.NewString(), uuid.NewString()
	for index, id := range []string{coveredID, missingID} {
		started := start.Add(time.Duration(index+1) * time.Hour)
		if _, err := db.Pool.Exec(ctx, `insert into steward_activity_sessions(
			id,type,title,summary,source,context_key,device_id,data_level,status,observation_count,
			confidence,value_score,started_at,ended_at,created_at,updated_at
		) values($1,'focused_work',$2,'','test','coverage','report-coverage-e2e','D2','closed',1,1,1,$3,$4,$3,$4)`,
			id, fmt.Sprintf("session-%d", index+1), started, started.Add(30*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	report, err := service.WriteReport(ctx, WriteReportInput{
		Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-19", PeriodStart: start,
		PeriodEnd: start.Add(24 * time.Hour), Status: reportStatusComplete, Title: "日报", Body: "覆盖率测试。", Silent: true,
		Evidence: []ProfileEvidenceInput{{SourceType: "activity_session", SourceID: coveredID, EvidenceDay: start.Add(time.Hour)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != reportStatusPartial || report.EvidenceCoverage != 0.5 || len(report.MissingEvidence) != 1 || report.MissingEvidence[0] != "activity_session:"+missingID {
		t.Fatalf("report coverage=%f missing=%v", report.EvidenceCoverage, report.MissingEvidence)
	}
	loaded, err := service.GetReport(ctx, report.ID)
	if err != nil || loaded.Status != reportStatusPartial || loaded.EvidenceCoverage != 0.5 || len(loaded.MissingEvidence) != 1 {
		t.Fatalf("persisted report coverage=%f missing=%v err=%v", loaded.EvidenceCoverage, loaded.MissingEvidence, err)
	}
	var checkpointJSON []byte
	if err := db.Pool.QueryRow(ctx, `select checkpoint from steward_reports where id=$1`, report.ID).Scan(&checkpointJSON); err != nil {
		t.Fatal(err)
	}
	var checkpoint map[string]any
	if err := json.Unmarshal(checkpointJSON, &checkpoint); err != nil {
		t.Fatal(err)
	}
	quality, _ := checkpoint["report_quality"].(map[string]any)
	if quality["status"] != "low_coverage" || intFromCheckpoint(quality["max_attempts"]) != reportQualityMaxAttempts {
		t.Fatalf("report quality checkpoint = %#v", quality)
	}
	retry, err := service.getIntelligenceJobByKey(ctx, "intelligence:report-quality:"+report.ID)
	if err != nil {
		t.Fatalf("load report quality retry: %v", err)
	}
	if retry.Kind != intelligenceJobReportDaily || retry.MaxAttempts != reportQualityMaxAttempts || retry.Input["regenerate_report_id"] != report.ID {
		t.Fatalf("report quality retry = %+v", retry)
	}
}

func TestProfileCorrectionPropagationPostgresLifecycle(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run profile correction propagation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("profile-correction-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}

	periodStart := time.Now().UTC().Add(-12 * time.Hour).Truncate(time.Second)
	periodEnd := periodStart.Add(24 * time.Hour)
	initialAt := periodStart.Add(time.Hour)
	initial, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{
		Key: "preference.focus_sound", Value: map[string]any{"value": "quiet"}, Summary: "initial explicit preference",
		Horizon: domain.StewardProfileHorizonExplicit, Confidence: 1, UserConfirmed: true, CreatedBy: "user", ValidFrom: &initialAt,
	})
	if err != nil {
		t.Fatalf("write initial profile fact: %v", err)
	}
	report, err := service.WriteReport(ctx, WriteReportInput{
		Cadence: domain.StewardReportDaily, PeriodKey: "profile-correction-e2e", PeriodStart: periodStart, PeriodEnd: periodEnd,
		Status: reportStatusComplete, Title: "Original report", Body: "Based on the original profile.", Silent: true,
		Evidence: []ProfileEvidenceInput{{SourceType: "profile_fact", SourceID: initial.ID, EvidenceDay: initialAt}},
	})
	if err != nil || report.Status != reportStatusComplete {
		t.Fatalf("write source report=%+v err=%v", report, err)
	}

	correctedAt := periodStart.Add(2 * time.Hour)
	correctionInput := UpsertProfileFactInput{
		Key: "preference.focus_sound", Value: map[string]any{"value": "music"}, Summary: "user corrected focus sound", ValidFrom: &correctedAt,
	}
	corrected, err := service.CorrectProfileFact(ctx, correctionInput)
	if err != nil {
		t.Fatalf("correct profile fact: %v", err)
	}
	if corrected.Horizon != domain.StewardProfileHorizonExplicit || !corrected.UserConfirmed {
		t.Fatalf("corrected fact contract = %+v", corrected)
	}
	foundCorrectionEvidence := false
	for _, evidence := range corrected.Evidence {
		if evidence.SourceType == "user_correction" && evidence.SourceID == corrected.ID {
			foundCorrectionEvidence = true
		}
	}
	if !foundCorrectionEvidence {
		t.Fatalf("durable correction evidence missing: %+v", corrected.Evidence)
	}

	review, err := service.getIntelligenceJobByKey(ctx, "intelligence:profile-correction-review:"+corrected.ID)
	if err != nil {
		t.Fatalf("load correction policy review: %v", err)
	}
	if review.Kind != intelligenceJobProfileCorrectionReview || review.MaxAttempts != reportQualityMaxAttempts || review.Input["correction_fact_id"] != corrected.ID {
		t.Fatalf("correction policy review = %+v", review)
	}
	regeneration, err := service.getIntelligenceJobByKey(ctx, "intelligence:profile-correction:"+corrected.ID+":report:"+report.ID)
	if err != nil {
		t.Fatalf("load correction report regeneration: %v", err)
	}
	if regeneration.Kind != intelligenceJobReportDaily || regeneration.MaxAttempts != reportQualityMaxAttempts ||
		regeneration.Input["regenerate_report_id"] != report.ID || regeneration.Input["correction_fact_id"] != corrected.ID {
		t.Fatalf("correction report regeneration = %+v", regeneration)
	}

	retried, err := service.CorrectProfileFact(ctx, correctionInput)
	if err != nil || retried.ID != corrected.ID {
		t.Fatalf("idempotent correction retry=%+v err=%v", retried, err)
	}
	var propagatedJobs int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_memory_consolidation_runs
		where idempotency_key=$1 or idempotency_key=$2`,
		"intelligence:profile-correction-review:"+corrected.ID,
		"intelligence:profile-correction:"+corrected.ID+":report:"+report.ID).Scan(&propagatedJobs); err != nil {
		t.Fatal(err)
	}
	if propagatedJobs != 2 {
		t.Fatalf("propagation retry created %d jobs, want 2", propagatedJobs)
	}
}

func intFromCheckpoint(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func TestProfileAndReportRejectFabricatedEvidenceAndRecoverBackgroundJobContext(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the evidence integrity Postgres test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("profile-evidence-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	service.registerIntelligenceTools()
	if err := service.ensureRuntimeToolSpecs(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	sessionIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for index, id := range sessionIDs {
		day := start.AddDate(0, 0, index)
		if _, err := db.Pool.Exec(ctx, `insert into steward_activity_sessions
			(id,type,title,summary,source,context_key,device_id,data_level,status,observation_count,confidence,value_score,started_at,ended_at,created_at,updated_at)
			values ($1,'foreground','work','verified activity','windows-activity','work','local','D0','closed',1,1,1,$2,$3,$2,$3)`,
			id, day.Add(9*time.Hour), day.Add(10*time.Hour)); err != nil {
			t.Fatalf("insert activity session %d: %v", index, err)
		}
	}

	baseFact := UpsertProfileFactInput{Key: "work.focus_window", Value: map[string]any{"value": "09:00-10:00"},
		Horizon: domain.StewardProfileHorizonStable, CreatedBy: "model", Confidence: 0.8}
	baseFact.Evidence = []ProfileEvidenceInput{
		{SourceType: "session", SourceID: sessionIDs[0], EvidenceDay: start},
		{SourceType: "session", SourceID: sessionIDs[1], EvidenceDay: start.AddDate(0, 0, 1)},
		{SourceType: "session", SourceID: uuid.NewString(), EvidenceDay: start.AddDate(0, 0, 2)},
	}
	if _, err := service.UpsertProfileFact(ctx, baseFact); !errors.Is(err, ErrEvidenceSourceNotFound) {
		t.Fatalf("fabricated source error = %v, want ErrEvidenceSourceNotFound", err)
	}
	baseFact.Evidence[2].SourceID = sessionIDs[2]
	baseFact.Evidence[2].EvidenceDay = start.AddDate(0, 0, 9)
	if _, err := service.UpsertProfileFact(ctx, baseFact); !errors.Is(err, ErrEvidenceDayMismatch) {
		t.Fatalf("fabricated evidence day error = %v, want ErrEvidenceDayMismatch", err)
	}
	baseFact.Evidence[2].EvidenceDay = start.AddDate(0, 0, 2)
	fact, err := service.UpsertProfileFact(ctx, baseFact)
	if err != nil || fact.EvidenceDays != 3 || fact.EvidenceCount != 3 {
		t.Fatalf("verified stable fact=%+v err=%v", fact, err)
	}

	if _, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{Key: "recent.project", Value: map[string]any{"value": "R5.3"},
		Horizon: domain.StewardProfileHorizonRecent, CreatedBy: "model"}); !errors.Is(err, ErrProfileEvidenceRequired) {
		t.Fatalf("model fact without evidence error = %v, want ErrProfileEvidenceRequired", err)
	}
	if _, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{Key: "identity.name", Value: map[string]any{"value": "Alice"},
		Horizon: domain.StewardProfileHorizonExplicit, CreatedBy: "user", UserConfirmed: true}); err != nil {
		t.Fatalf("explicit user correction should remain evidence-free: %v", err)
	}
	if _, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{Key: "identity.fake", Value: map[string]any{"value": "fabricated"},
		Horizon: domain.StewardProfileHorizonExplicit, CreatedBy: "model", UserConfirmed: true}); !errors.Is(err, ErrProfileEvidenceRequired) {
		t.Fatalf("model explicit fact without user source error = %v, want ErrProfileEvidenceRequired", err)
	}

	conversationID, messageID := uuid.NewString(), uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
		values ($1,'evidence','active','D0',$2,$2)`, conversationID, start); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,created_at)
		values ($1,$2,'user','我明确喜欢安静工作','D0',$3)`, messageID, conversationID, start); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{Key: "preference.quiet", Value: map[string]any{"value": true},
		Horizon: domain.StewardProfileHorizonExplicit, CreatedBy: "model", UserConfirmed: true,
		Evidence: []ProfileEvidenceInput{{SourceType: "user_message", SourceID: messageID, EvidenceDay: start}}}); err != nil {
		t.Fatalf("model fact backed by real user message: %v", err)
	}

	reportEnd := start.Add(24 * time.Hour)
	reportJob, _, err := service.EnqueueIntelligenceJob(ctx, EnqueueIntelligenceJobInput{Kind: intelligenceJobReportDaily,
		PeriodKey: "2026-07-10", PeriodStart: start, PeriodEnd: reportEnd, DueAt: start})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimIntelligenceJobs(ctx, "evidence-worker", start.Add(time.Hour), time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != reportJob.ID {
		t.Fatalf("claim report job=%+v err=%v", claimed, err)
	}
	reportJob = claimed[0]
	episodeID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_episodes
		(id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,status,created_at,updated_at,completed_at)
		values ($1,$2,$3,'report_daily','daily report','D0','completed',$4,$4,$4)`, episodeID, conversationID, messageID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := service.LinkIntelligenceJobEpisode(ctx, reportJob, episodeID); err != nil {
		t.Fatal(err)
	}
	runID, stepID, invocationID, executionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_runs(id,goal,status,plan_hash,created_at,updated_at,completed_at)
		values ($1,'write report','succeeded','report-plan',$2,$2,$2)`, runID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_run_steps
		(id,run_id,step_key,position,title,tool_name,tool_version,status,idempotency_key,created_at,updated_at,completed_at)
		values ($1,$2,'report',0,'write report','steward.report.write','5.3.0','succeeded','report-step',$3,$3,$3)`,
		stepID, runID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_tool_invocations
		(id,run_id,step_id,tool_name,tool_version,attempt,idempotency_key,status,started_at,finished_at)
		values ($1,$2,$3,'steward.report.write','5.3.0',1,'report-invocation','succeeded',$4,$4)`,
		invocationID, runID, stepID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_executions
		(id,conversation_id,message_id,request_message_id,instruction,kind,status,run_id,episode_id,created_at,updated_at,completed_at)
		values ($1,$2,$3,$3,'write report','run','succeeded',$4,$5,$6,$6,$6)`,
		executionID, conversationID, messageID, runID, episodeID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	resolvedEpisodeID, resolvedJobID, err := service.intelligenceInvocationContext(withRuntimeInvocationID(ctx, invocationID))
	if err != nil || resolvedEpisodeID != episodeID || resolvedJobID != reportJob.ID {
		t.Fatalf("invocation context episode=%s job=%s err=%v", resolvedEpisodeID, resolvedJobID, err)
	}

	outside := []ProfileEvidenceInput{{SourceType: "activity_session", SourceID: sessionIDs[2], EvidenceDay: start.AddDate(0, 0, 2)}}
	if _, err := service.WriteReport(ctx, WriteReportInput{Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-10",
		PeriodStart: start, PeriodEnd: reportEnd, Title: "日报", Body: "错误地引用未来证据", Evidence: outside}); !errors.Is(err, ErrEvidenceDayMismatch) {
		t.Fatalf("out-of-period report evidence error = %v, want ErrEvidenceDayMismatch", err)
	}
	report, err := service.WriteReport(ctx, WriteReportInput{Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-10",
		PeriodStart: start, PeriodEnd: reportEnd, Title: "日报", Body: "引用真实活动证据",
		Evidence: []ProfileEvidenceInput{{SourceType: "session", SourceID: sessionIDs[0], EvidenceDay: start}}})
	if err != nil {
		t.Fatalf("write report without explicit job context: %v", err)
	}
	if report.JobID == nil || *report.JobID != reportJob.ID || report.EpisodeID == nil || *report.EpisodeID != episodeID {
		t.Fatalf("report did not recover job/episode context: %+v", report)
	}
	if reconciled, err := service.ReconcileIntelligenceJobs(ctx, start.Add(2*time.Hour), 4); err != nil || reconciled != 1 {
		t.Fatalf("reconcile linked report: count=%d err=%v", reconciled, err)
	}
	loadedJob, err := service.GetIntelligenceJob(ctx, reportJob.ID)
	if err != nil || loadedJob.Status != intelligenceJobCompleted || loadedJob.ReportID == nil || *loadedJob.ReportID != report.ID {
		t.Fatalf("reconciled job=%+v err=%v", loadedJob, err)
	}
}

func TestProfileConflictPostgresLifecycle(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the profile conflict Postgres lifecycle test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("profile-conflict-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	location := service.evidenceTimezone(ctx)
	sessionIDs := []string{uuid.NewString(), uuid.NewString()}
	for index, id := range sessionIDs {
		startedAt := now.Add(time.Duration(index-2) * time.Hour)
		if _, err := db.Pool.Exec(ctx, `insert into steward_activity_sessions(
			id,type,title,summary,source,context_key,device_id,data_level,status,observation_count,
			confidence,value_score,started_at,ended_at,created_at,updated_at
		) values($1,'focused_work',$2,'conflicting profile evidence','test','profile-conflict','profile-conflict-e2e',
			'D2','closed',1,1,1,$3,$4,$3,$4)`, id, fmt.Sprintf("profile-conflict-%d", index), startedAt, startedAt.Add(30*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	writeInference := func(value string, confidence float64, sessionIndex int) domain.StewardProfileFact {
		t.Helper()
		evidenceAt := now.Add(time.Duration(sessionIndex-2) * time.Hour).In(location)
		fact, err := service.UpsertProfileFact(ctx, UpsertProfileFactInput{
			Key: "work.preferred_focus_window", Value: map[string]any{"value": value},
			Horizon: domain.StewardProfileHorizonRecent, Confidence: confidence, CreatedBy: "model",
			Evidence: []ProfileEvidenceInput{{SourceType: "activity_session", SourceID: sessionIDs[sessionIndex], EvidenceDay: evidenceAt}},
		})
		if err != nil {
			t.Fatalf("write inference %q: %v", value, err)
		}
		return fact
	}
	first := writeInference("09:00-11:00", 0.9, 0)
	second := writeInference("20:00-22:00", 0.8, 1)
	if second.SupersedesFactID != nil {
		t.Fatalf("contradictory inference silently superseded %s", *second.SupersedesFactID)
	}

	active, err := service.ListProfileFacts(ctx, ListProfileFactsInput{
		Horizon: domain.StewardProfileHorizonRecent, Status: domain.StewardProfileFactActive,
		Key: "work.preferred_focus_window", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ConflictGroup == "" || active[0].ConflictGroup != active[1].ConflictGroup {
		t.Fatalf("active conflicting evidence branches = %+v", active)
	}
	for _, fact := range active {
		if len(fact.Evidence) != 1 || fact.ValidTo != nil {
			t.Fatalf("conflicting branch lost audit evidence or validity: %+v", fact)
		}
	}
	view, err := service.GetProfileView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Recent == nil || len(view.Recent.Facts) != 1 || view.Recent.Facts[0].ID != first.ID {
		t.Fatalf("recent conflict projection = %+v", view.Recent)
	}
	if selected := view.Recent.Facts[0]; selected.EffectiveConfidence <= 0 || selected.EffectiveConfidence >= selected.Confidence {
		t.Fatalf("conflict confidence was not reduced in projection: %+v", selected)
	}

	firstCorrection, err := service.CorrectProfileFact(ctx, UpsertProfileFactInput{
		Key: "work.preferred_focus_window", Value: map[string]any{"value": "10:00-12:00"}, Summary: "user correction",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondCorrection, err := service.CorrectProfileFact(ctx, UpsertProfileFactInput{
		Key: "work.preferred_focus_window", Value: map[string]any{"value": "10:30-12:30"}, Summary: "new user correction",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondCorrection.SupersedesFactID == nil || *secondCorrection.SupersedesFactID != firstCorrection.ID {
		t.Fatalf("explicit correction chain = first %+v second %+v", firstCorrection, secondCorrection)
	}
	explicitFacts, err := service.ListProfileFacts(ctx, ListProfileFactsInput{
		Horizon: domain.StewardProfileHorizonExplicit, Key: "work.preferred_focus_window", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitStatuses := map[string]string{}
	for _, fact := range explicitFacts {
		explicitStatuses[fact.ID] = fact.Status
	}
	if explicitStatuses[firstCorrection.ID] != domain.StewardProfileFactSuperseded || explicitStatuses[secondCorrection.ID] != domain.StewardProfileFactActive {
		t.Fatalf("explicit correction statuses = %+v", explicitStatuses)
	}
	view, err = service.GetProfileView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Merged == nil || view.Merged.Profile["work.preferred_focus_window"].(map[string]any)["value"] != "10:30-12:30" {
		t.Fatalf("merged profile did not retain explicit precedence: %+v", view.Merged)
	}
	active, err = service.ListProfileFacts(ctx, ListProfileFactsInput{
		Horizon: domain.StewardProfileHorizonRecent, Status: domain.StewardProfileFactActive,
		Key: "work.preferred_focus_window", Limit: 10,
	})
	if err != nil || len(active) != 2 {
		t.Fatalf("explicit correction erased recent conflict history: count=%d err=%v", len(active), err)
	}
}

func TestReportNotificationDeliveryPostgresLifecycle(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the report notification delivery Postgres test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("report-notification-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureDefaultNotificationEndpoints(ctx); err != nil {
		t.Fatal(err)
	}

	// Keep one known system endpoint disabled so notification routing fails
	// after the report transaction has committed. This proves that report
	// persistence and notification creation are independent failure domains.
	endpointID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `insert into steward_notification_endpoints(id,channel,name,enabled,config)
		values($1,'system','report-notification-e2e',false,'{}'::jsonb)`, endpointID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_notification_endpoints set enabled=false`); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	input := WriteReportInput{Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-20", PeriodStart: start,
		PeriodEnd: start.Add(24 * time.Hour), Status: reportStatusComplete, Title: "日报", Summary: "完成了报告投递闭环。", Body: "完整报告正文。"}
	report, err := service.WriteReport(ctx, input)
	if err != nil {
		t.Fatalf("notification routing failure must not fail report persistence: %v", err)
	}
	var decision, retryStatus, retryError string
	if err := db.Pool.QueryRow(ctx, `select delivery_decision,
		coalesce(checkpoint#>>'{notification_delivery,status}',''),
		coalesce(checkpoint#>>'{notification_delivery,last_error}','') from steward_reports where id=$1`, report.ID).
		Scan(&decision, &retryStatus, &retryError); err != nil {
		t.Fatal(err)
	}
	if decision != "deliver" || retryStatus != "retrying" || retryError == "" {
		t.Fatalf("delivery retry state decision=%q status=%q error=%q", decision, retryStatus, retryError)
	}
	if report.NotificationID == nil || strings.TrimSpace(*report.NotificationID) == "" {
		t.Fatalf("persisted notification was not linked after routing failure: report=%+v retry_error=%q", report, retryError)
	}
	notificationID := *report.NotificationID
	var notificationCount, deliveryCount int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_notifications where source_type='report' and source_id=$1`, report.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_notification_deliveries where notification_id=$1`, notificationID).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || deliveryCount != 0 {
		t.Fatalf("failed route notification_count=%d delivery_count=%d", notificationCount, deliveryCount)
	}

	if _, err := db.Pool.Exec(ctx, `update steward_notification_endpoints set enabled=true where id=$1`, endpointID); err != nil {
		t.Fatal(err)
	}
	reconciled, err := service.ReconcileReportNotifications(ctx, time.Now().UTC().Add(2*time.Minute), 10)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconcile report notification: count=%d err=%v", reconciled, err)
	}
	loaded, err := service.GetReport(ctx, report.ID)
	if err != nil || loaded.NotificationID == nil || *loaded.NotificationID != notificationID {
		t.Fatalf("reconciled report=%+v err=%v", loaded, err)
	}
	var notificationStatus, deliveryStatus, checkpointStatus, checkpointError string
	if err := db.Pool.QueryRow(ctx, `select n.status,d.status,
		coalesce(r.checkpoint#>>'{notification_delivery,status}',''),
		coalesce(r.checkpoint#>>'{notification_delivery,last_error}','')
		from steward_reports r join steward_notifications n on n.id=r.notification_id
		join steward_notification_deliveries d on d.notification_id=n.id where r.id=$1`, report.ID).
		Scan(&notificationStatus, &deliveryStatus, &checkpointStatus, &checkpointError); err != nil {
		t.Fatal(err)
	}
	if notificationStatus != "queued" || deliveryStatus != "queued" || checkpointStatus != "queued" || checkpointError != "" {
		t.Fatalf("reconciled notification=%q delivery=%q checkpoint=%q error=%q", notificationStatus, deliveryStatus, checkpointStatus, checkpointError)
	}

	// Both the report write retry and a later reconciliation must converge on
	// the same deduplicated notification and delivery occurrence.
	retried, err := service.WriteReport(ctx, input)
	if err != nil || retried.ID != report.ID || retried.NotificationID == nil || *retried.NotificationID != notificationID {
		t.Fatalf("idempotent report retry=%+v err=%v", retried, err)
	}
	if reconciled, err = service.ReconcileReportNotifications(ctx, time.Now().UTC().Add(3*time.Minute), 10); err != nil || reconciled != 0 {
		t.Fatalf("settled report was reconciled again: count=%d err=%v", reconciled, err)
	}
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_notifications where source_type='report' and source_id=$1`, report.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_notification_deliveries where notification_id=$1`, notificationID).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || deliveryCount != 1 {
		t.Fatalf("retry duplicated side effects: notification_count=%d delivery_count=%d", notificationCount, deliveryCount)
	}

	silent, err := service.WriteReport(ctx, WriteReportInput{Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-21",
		PeriodStart: start.Add(24 * time.Hour), PeriodEnd: start.Add(48 * time.Hour), Status: reportStatusComplete,
		Title: "静默日报", Body: "只持久化，不提醒。", Silent: true})
	if err != nil || !silent.Silent || silent.NotificationID != nil {
		t.Fatalf("silent report=%+v err=%v", silent, err)
	}
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_notifications where source_type='report' and source_id=$1`, silent.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 0 {
		t.Fatalf("silent report created %d notifications", notificationCount)
	}

	partial, err := service.WriteReport(ctx, WriteReportInput{Cadence: domain.StewardReportDaily, PeriodKey: "2026-07-22",
		PeriodStart: start.Add(48 * time.Hour), PeriodEnd: start.Add(72 * time.Hour), Status: reportStatusPartial,
		Title: "部分日报", Body: "证据不完整，但仍应通知。"})
	if err != nil || partial.NotificationID == nil {
		t.Fatalf("partial report notification=%+v err=%v", partial, err)
	}
}

func newProfileReportTestDatabase(t *testing.T, ctx context.Context, baseDSN string) *database.DB {
	t.Helper()
	name := fmt.Sprintf("steward_profile_report_%d", time.Now().UnixNano())
	adminConfig, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "create database "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, `select pg_terminate_backend(pid) from pg_stat_activity where datname=$1 and pid<>pg_backend_pid()`, name)
		_, _ = admin.Exec(dropCtx, "drop database if exists "+quoted)
		admin.Close()
	})
	config, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	db := &database.DB{Pool: pool}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
