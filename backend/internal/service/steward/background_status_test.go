package steward

import (
	"context"
	"encoding/json"
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

func TestClassifyBackgroundStatus(t *testing.T) {
	tests := []struct {
		name   string
		input  StewardBackgroundStatus
		state  string
		issues int
	}{
		{
			name:  "disabled is explicit",
			input: StewardBackgroundStatus{Enabled: false},
			state: "disabled", issues: 1,
		},
		{
			name: "healthy requires live source and model",
			input: StewardBackgroundStatus{
				Enabled: true, Agent: domain.StewardAgentStatus{Status: StatusRunning},
				Model:    domain.StewardAutonomyAdvisorStatus{Enabled: true},
				Pipeline: ActivityPipelineStatus{Sources: []ActivitySourceStatus{{CollectorName: "windows-activity", Fresh: true}}},
			},
			state: "healthy", issues: 0,
		},
		{
			name: "stale collection is degraded",
			input: StewardBackgroundStatus{
				Enabled: true, Agent: domain.StewardAgentStatus{Status: StatusRunning},
				Model:    domain.StewardAutonomyAdvisorStatus{Enabled: true},
				Pipeline: ActivityPipelineStatus{Sources: []ActivitySourceStatus{{CollectorName: "windows-activity", Fresh: false}}},
			},
			state: "degraded", issues: 1,
		},
		{
			name: "stopped agent is unhealthy",
			input: StewardBackgroundStatus{
				Enabled: true, Agent: domain.StewardAgentStatus{Status: StatusStopped},
				Model:    domain.StewardAutonomyAdvisorStatus{Enabled: true},
				Pipeline: ActivityPipelineStatus{Sources: []ActivitySourceStatus{{Fresh: true}}},
			},
			state: "unhealthy", issues: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, issues := classifyBackgroundStatus(test.input)
			if state != test.state || len(issues) != test.issues {
				t.Fatalf("classifyBackgroundStatus()=(%q,%v), want state=%q issues=%d", state, issues, test.state, test.issues)
			}
		})
	}
}

func TestBackgroundStatusStructuredIssuesKeepStringCompatibility(t *testing.T) {
	status := StewardBackgroundStatus{
		Enabled: true,
		Agent:   domain.StewardAgentStatus{Status: StatusRunning},
		Model:   domain.StewardAutonomyAdvisorStatus{Enabled: true, CircuitOpen: true},
		Pipeline: ActivityPipelineStatus{Sources: []ActivitySourceStatus{{
			CollectorName: "windows-activity", Fresh: false,
		}}},
	}
	state, details := classifyBackgroundStatusDetailed(status)
	if state != "degraded" || len(details) != 2 {
		t.Fatalf("classifyBackgroundStatusDetailed()=(%q,%+v)", state, details)
	}
	for _, detail := range details {
		if strings.TrimSpace(detail.Code) == "" || strings.TrimSpace(detail.Message) == "" || strings.TrimSpace(detail.Action) == "" {
			t.Fatalf("issue lacks structured recovery data: %+v", detail)
		}
	}
	_, compatible := classifyBackgroundStatus(status)
	if got, want := strings.Join(compatible, "|"), strings.Join(issueMessages(details), "|"); got != want {
		t.Fatalf("compatibility issues=%q, structured messages=%q", got, want)
	}
}

func TestBackgroundStatusExposesReportContractFailures(t *testing.T) {
	status := StewardBackgroundStatus{
		Enabled: true,
		Agent:   domain.StewardAgentStatus{Status: StatusRunning},
		Model:   domain.StewardAutonomyAdvisorStatus{Enabled: true},
		Pipeline: ActivityPipelineStatus{Sources: []ActivitySourceStatus{{
			CollectorName: "windows-activity", Fresh: true,
		}}},
		ReportHealth: StewardReportHealthStatus{
			RequiredCoverage:    reportEvidenceCoverageThreshold,
			MissingDailyPeriods: []string{"2026-07-18"},
			LowCoverageReports:  2,
		},
	}
	state, details := classifyBackgroundStatusDetailed(status)
	if state != "degraded" || len(details) != 2 {
		t.Fatalf("report health classification=(%q,%+v)", state, details)
	}
	codes := map[string]bool{}
	for _, detail := range details {
		codes[detail.Code] = true
	}
	if !codes["report_missing"] || !codes["report_low_coverage"] {
		t.Fatalf("report health issue codes = %#v", codes)
	}
}

func TestBackgroundStatusMetricsJSONMarksUnavailableUsage(t *testing.T) {
	status := StewardBackgroundStatus{
		Issues:       []string{},
		IssueDetails: []domain.StewardHealthIssue{},
		Metrics: domain.StewardBackgroundMetrics{
			BatchStatusCounts:  map[string]int{},
			ReminderFeedback1H: domain.StewardReminderFeedbackMetrics{ByAction: map[string]int{}},
			ModelUsage: domain.StewardModelUsageMetrics{
				Available: false,
				Reason:    "provider token and cost usage is not persisted",
			},
		},
	}
	payload, err := json.Marshal(map[string]any{"status": status})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status struct {
			Issues       []string                    `json:"issues"`
			IssueDetails []domain.StewardHealthIssue `json:"issue_details"`
			Metrics      struct {
				BatchStatusCounts  map[string]int `json:"batch_status_counts"`
				ReminderFeedback1H struct {
					ByAction map[string]int `json:"by_action"`
				} `json:"reminder_feedback_1h"`
				ModelUsage map[string]any `json:"model_usage"`
			} `json:"metrics"`
		} `json:"status"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status.Issues == nil || decoded.Status.IssueDetails == nil || decoded.Status.Metrics.BatchStatusCounts == nil || decoded.Status.Metrics.ReminderFeedback1H.ByAction == nil {
		t.Fatalf("empty compatibility and metric collections must serialize as arrays/objects: %s", payload)
	}
	if available, ok := decoded.Status.Metrics.ModelUsage["available"].(bool); !ok || available {
		t.Fatalf("model usage availability was not explicit: %s", payload)
	}
	for _, forbidden := range []string{"input_tokens", "output_tokens", "total_tokens", "cost"} {
		if _, exists := decoded.Status.Metrics.ModelUsage[forbidden]; exists {
			t.Fatalf("unavailable model usage fabricated %q: %s", forbidden, payload)
		}
	}
}

func TestNextContinuousIntelligenceDeadlinesReflectSettings(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 7, 30, 0, time.UTC)
	settings := IntelligenceSettings{
		Enabled: true, Mode: "batch", Timezone: "UTC",
		BatchIntervalSeconds: 1800, BoundaryGraceSeconds: 300,
		DailyReportFallbackLocal: "21:30",
	}
	consolidation, report := nextContinuousIntelligenceDeadlines(now, settings)
	if consolidation == nil || !consolidation.Equal(time.Date(2026, 7, 20, 10, 35, 0, 0, time.UTC)) {
		t.Fatalf("next consolidation = %v", consolidation)
	}
	if report == nil || !report.Equal(time.Date(2026, 7, 20, 21, 30, 0, 0, time.UTC)) {
		t.Fatalf("next daily report = %v", report)
	}

	afterReport := time.Date(2026, 7, 20, 22, 0, 0, 0, time.UTC)
	_, report = nextContinuousIntelligenceDeadlines(afterReport, settings)
	if report == nil || !report.Equal(time.Date(2026, 7, 21, 21, 30, 0, 0, time.UTC)) {
		t.Fatalf("next daily report after cutoff = %v", report)
	}
}

func TestNextContinuousIntelligenceDeadlinesAreAbsentWhenDisabled(t *testing.T) {
	consolidation, report := nextContinuousIntelligenceDeadlines(time.Now(), IntelligenceSettings{Enabled: false})
	if consolidation != nil || report != nil {
		t.Fatalf("disabled deadlines = (%v,%v)", consolidation, report)
	}
}

func TestLoadBackgroundMetricsPostgres(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the background observability metrics test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newBackgroundStatusTestDatabase(t, ctx, baseDSN)
	service := NewService(db)
	now := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	recent := now.Add(-20 * time.Minute)
	old := now.Add(-2 * time.Hour)

	for index, occurredAt := range []time.Time{recent, recent.Add(time.Minute), old} {
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_observations(id,source,type,summary,data_level,permission_level,device_id,occurred_at)
			values($1,'test','window','metrics','D0','A0','test-device',$2)
		`, uuid.New(), occurredAt); err != nil {
			t.Fatalf("insert observation %d: %v", index, err)
		}
	}
	for index, startedAt := range []time.Time{recent, old} {
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_activity_sessions(id,type,title,source,device_id,data_level,started_at,ended_at)
			values($1,'work','metrics','test','test-device','D0',$2,$3)
		`, uuid.New(), startedAt, startedAt.Add(time.Minute)); err != nil {
			t.Fatalf("insert activity session %d: %v", index, err)
		}
	}
	for index, item := range []struct {
		status    string
		createdAt time.Time
	}{{"pending", recent}, {"failed", recent.Add(time.Minute)}, {"completed", old}} {
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_activity_batches(id,window_start,window_end,status,idempotency_key,created_at,updated_at)
			values($1,$2,$3,$4,$5,$6,$6)
		`, uuid.New(), item.createdAt.Add(-time.Minute), item.createdAt, item.status, uuid.NewString(), item.createdAt); err != nil {
			t.Fatalf("insert activity batch %d: %v", index, err)
		}
	}

	conversationID := uuid.New()
	messageID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title) values($1,'metrics')`, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_conversation_messages(id,conversation_id,role,content,data_level)
		values($1,$2,'user','metrics','D0')
	`, messageID, conversationID); err != nil {
		t.Fatal(err)
	}
	for index, item := range []struct {
		status      string
		completedAt time.Time
	}{{"completed", recent}, {"failed", recent.Add(time.Minute)}, {"completed", old}} {
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_agent_episodes(id,conversation_id,trigger_message_id,goal,status,created_at,updated_at,completed_at)
			values($1,$2,$3,'metrics',$4,$5,$5,$5)
		`, uuid.New(), conversationID, messageID, item.status, item.completedAt); err != nil {
			t.Fatalf("insert agent episode %d: %v", index, err)
		}
	}

	for index, coverage := range []float64{0.5, 1} {
		periodStart := now.AddDate(0, 0, -index-1)
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_reports(id,cadence,period_key,period_start,period_end,status,evidence_coverage,idempotency_key)
			values($1,'daily',$2,$3,$4,'complete',$5,$6)
		`, uuid.New(), fmt.Sprintf("metrics-%d", index), periodStart, periodStart.Add(24*time.Hour), coverage, uuid.NewString()); err != nil {
			t.Fatalf("insert report %d: %v", index, err)
		}
	}

	notificationID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `insert into steward_notifications(id,title,body,created_at,updated_at) values($1,'metrics','metrics',$2,$2)`, notificationID, recent); err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"opened", "dismissed"} {
		if _, err := db.Pool.Exec(ctx, `
			insert into steward_reminder_feedback(id,notification_id,action,idempotency_key,created_at)
			values($1,$2,$3,$4,$5)
		`, uuid.New(), notificationID, action, uuid.NewString(), recent.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatalf("insert reminder feedback %d: %v", index, err)
		}
	}

	metrics, err := service.loadBackgroundMetrics(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Observations1H != 2 || metrics.Sessions1H != 1 || !metrics.SessionCompressionRatio.Available || metrics.SessionCompressionRatio.Value != 2 {
		t.Fatalf("unexpected collection metrics: %+v", metrics)
	}
	if metrics.BatchStatusCounts["pending"] != 1 || metrics.BatchStatusCounts["failed"] != 1 || metrics.BatchStatusCounts["completed"] != 0 {
		t.Fatalf("unexpected batch metrics: %+v", metrics.BatchStatusCounts)
	}
	if metrics.ModelEpisodes1H.Completed != 1 || metrics.ModelEpisodes1H.Failed != 1 {
		t.Fatalf("unexpected episode metrics: %+v", metrics.ModelEpisodes1H)
	}
	if !metrics.ReportCoverage.Available || metrics.ReportCoverage.ReportCount != 2 || metrics.ReportCoverage.Average != 0.75 {
		t.Fatalf("unexpected report coverage: %+v", metrics.ReportCoverage)
	}
	if metrics.ReminderFeedback1H.Total != 2 || metrics.ReminderFeedback1H.ByAction["opened"] != 1 || metrics.ReminderFeedback1H.ByAction["dismissed"] != 1 {
		t.Fatalf("unexpected reminder feedback: %+v", metrics.ReminderFeedback1H)
	}
	if metrics.ModelUsage.Available || metrics.ModelUsage.Reason == "" || metrics.ModelUsage.TotalTokens != nil || metrics.ModelUsage.Cost != nil {
		t.Fatalf("unavailable usage must not be estimated: %+v", metrics.ModelUsage)
	}
}

func TestLoadReportHealthPostgres(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run report health test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newBackgroundStatusTestDatabase(t, ctx, baseDSN)
	service := NewService(db)
	now := time.Date(2031, 2, 3, 22, 0, 0, 0, time.UTC)
	settings := IntelligenceSettings{
		Enabled: true, Timezone: "UTC", DailyReportFallbackLocal: "21:30",
		WeeklyReportDay: int(time.Sunday), WeeklyReportLocal: "21:45", MonthlyReportLocal: "22:00",
		ProfileBootstrapDays: 1, ReportCatchupDays: 2,
	}
	periods, err := dueIntelligencePeriodsForSettings(now, settings)
	if err != nil {
		t.Fatal(err)
	}
	daily := []dueIntelligencePeriod{}
	for _, period := range periods {
		if period.Kind == intelligenceJobReportDaily {
			daily = append(daily, period)
		}
	}
	if len(daily) != 2 {
		t.Fatalf("daily periods = %+v", daily)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_reports
		(id,cadence,period_key,period_start,period_end,status,evidence_coverage,idempotency_key)
		values($1,'daily',$2,$3,$4,'partial',0.5,$5)`, uuid.New(), daily[0].Key, daily[0].Start, daily[0].End, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	health, err := service.loadReportHealth(ctx, now, settings)
	if err != nil {
		t.Fatal(err)
	}
	if health.RequiredCoverage != reportEvidenceCoverageThreshold || health.LowCoverageReports != 1 ||
		len(health.MissingDailyPeriods) != 1 || health.MissingDailyPeriods[0] != daily[1].Key {
		t.Fatalf("report health = %+v", health)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_reports
		(id,cadence,period_key,period_start,period_end,status,evidence_coverage,idempotency_key)
		values($1,'daily',$2,$3,$4,'complete',1,$5)`, uuid.New(), daily[1].Key, daily[1].Start, daily[1].End, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	health, err = service.loadReportHealth(ctx, now, settings)
	if err != nil || len(health.MissingDailyPeriods) != 0 || health.LowCoverageReports != 1 {
		t.Fatalf("report health after filling missing period = %+v err=%v", health, err)
	}
}

func newBackgroundStatusTestDatabase(t *testing.T, ctx context.Context, baseDSN string) *database.DB {
	t.Helper()
	name := fmt.Sprintf("steward_background_status_%d", time.Now().UnixNano())
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
