package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

type StewardBackgroundQueueStatus struct {
	Pending       int        `json:"pending"`
	Processing    int        `json:"processing"`
	WaitingModel  int        `json:"waiting_model"`
	Failed        int        `json:"failed"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}

type StewardNotificationQueueStatus struct {
	Queued     int        `json:"queued"`
	Sending    int        `json:"sending"`
	Retrying   int        `json:"retrying"`
	Failed     int        `json:"failed"`
	Accepted   int        `json:"accepted"`
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
}

type StewardBackgroundOutcome struct {
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Summary string    `json:"summary,omitempty"`
	At      time.Time `json:"at"`
}

// StewardReportHealthStatus makes report completeness independently visible
// from queue liveness. A running worker is not healthy when a due report is
// absent or its durable evidence coverage is below the completion contract.
type StewardReportHealthStatus struct {
	RequiredCoverage    float64  `json:"required_coverage"`
	MissingDailyPeriods []string `json:"missing_daily_periods"`
	LowCoverageReports  int      `json:"low_coverage_reports"`
}

// StewardBackgroundStatus is intentionally a lightweight, read-only health
// projection. It separates scheduler/loop liveness, collection freshness,
// queue progress, model availability and the latest durable outcome so the UI
// never infers "running" from a single green process badge.
type StewardBackgroundStatus struct {
	State               string                               `json:"state"`
	Enabled             bool                                 `json:"enabled"`
	Mode                string                               `json:"mode"`
	CheckedAt           time.Time                            `json:"checked_at"`
	Agent               domain.StewardAgentStatus            `json:"agent"`
	Loops               []domain.StewardBackgroundLoopStatus `json:"loops"`
	Pipeline            ActivityPipelineStatus               `json:"pipeline"`
	IntelligenceQueue   StewardBackgroundQueueStatus         `json:"intelligence_queue"`
	Notifications       StewardNotificationQueueStatus       `json:"notifications"`
	ReportHealth        StewardReportHealthStatus            `json:"report_health"`
	Model               domain.StewardAutonomyAdvisorStatus  `json:"model"`
	LatestOutcome       *StewardBackgroundOutcome            `json:"latest_outcome,omitempty"`
	LatestReportAt      *time.Time                           `json:"latest_report_at,omitempty"`
	ProfileUpdatedAt    *time.Time                           `json:"profile_updated_at,omitempty"`
	NextConsolidationAt *time.Time                           `json:"next_consolidation_at,omitempty"`
	NextDailyReportAt   *time.Time                           `json:"next_daily_report_at,omitempty"`
	Issues              []string                             `json:"issues"`
	IssueDetails        []domain.StewardHealthIssue          `json:"issue_details"`
	Metrics             domain.StewardBackgroundMetrics      `json:"metrics"`
}

func (s *Service) GetBackgroundStatus(ctx context.Context) (StewardBackgroundStatus, error) {
	now := time.Now().UTC()
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get intelligence settings: %w", err)
	}
	agent, err := s.GetAgentStatus(ctx)
	if err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get agent status: %w", err)
	}
	pipeline, err := s.ActivityPipelineStatus(ctx, now)
	if err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get activity pipeline status: %w", err)
	}
	out := StewardBackgroundStatus{
		Enabled: settings.Enabled, Mode: settings.Mode, CheckedAt: now, Agent: agent,
		Loops: agent.BackgroundLoops, Pipeline: pipeline, Model: s.autonomyAdvisor().Status(),
		Issues: []string{}, IssueDetails: []domain.StewardHealthIssue{},
	}
	out.NextConsolidationAt, out.NextDailyReportAt = nextContinuousIntelligenceDeadlines(now, settings)
	out.Metrics, err = s.loadBackgroundMetrics(ctx, now)
	if err != nil {
		return StewardBackgroundStatus{}, err
	}
	out.ReportHealth, err = s.loadReportHealth(ctx, now, settings)
	if err != nil {
		return StewardBackgroundStatus{}, err
	}
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*) filter(where status='pending'),
		       count(*) filter(where status in ('processing','executing')),
		       count(*) filter(where status='waiting_model'),
		       count(*) filter(where status='failed'),
		       max(completed_at) filter(where status in ('completed','partial'))
		from steward_memory_consolidation_runs
	`).Scan(&out.IntelligenceQueue.Pending, &out.IntelligenceQueue.Processing,
		&out.IntelligenceQueue.WaitingModel, &out.IntelligenceQueue.Failed,
		&out.IntelligenceQueue.LastSuccessAt); err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get intelligence queue status: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*) filter(where status='queued'),count(*) filter(where status='sending'),
		       count(*) filter(where status='retrying'),count(*) filter(where status='failed'),
		       count(*) filter(where status='accepted'),max(accepted_at)
		from steward_notification_deliveries
	`).Scan(&out.Notifications.Queued, &out.Notifications.Sending, &out.Notifications.Retrying,
		&out.Notifications.Failed, &out.Notifications.Accepted, &out.Notifications.LastSentAt); err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get notification queue status: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `select max(completed_at) from steward_reports where status in ('complete','partial')`).Scan(&out.LatestReportAt); err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get latest report status: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `select max(updated_at) from steward_profile_facts where status='active'`).Scan(&out.ProfileUpdatedAt); err != nil {
		return StewardBackgroundStatus{}, fmt.Errorf("get profile status: %w", err)
	}
	latest, err := s.latestBackgroundOutcome(ctx)
	if err != nil {
		return StewardBackgroundStatus{}, err
	}
	out.LatestOutcome = latest
	out.State, out.IssueDetails = classifyBackgroundStatusDetailed(out)
	out.Issues = issueMessages(out.IssueDetails)
	return out, nil
}

func (s *Service) loadReportHealth(ctx context.Context, now time.Time, settings IntelligenceSettings) (StewardReportHealthStatus, error) {
	out := StewardReportHealthStatus{
		RequiredCoverage:    reportEvidenceCoverageThreshold,
		MissingDailyPeriods: []string{},
	}
	if !settings.Enabled {
		return out, nil
	}
	periods, err := dueIntelligencePeriodsForSettings(now, settings)
	if err != nil {
		return out, fmt.Errorf("resolve due report periods: %w", err)
	}
	for _, period := range periods {
		if period.Kind != intelligenceJobReportDaily {
			continue
		}
		var exists bool
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_reports
			where profile_scope='default' and cadence='daily' and period_key=$1
			and status in ('complete','partial'))`, period.Key).Scan(&exists); err != nil {
			return out, fmt.Errorf("check due daily report %s: %w", period.Key, err)
		}
		if !exists {
			out.MissingDailyPeriods = append(out.MissingDailyPeriods, period.Key)
		}
	}
	if err := s.db.Pool.QueryRow(ctx, `select count(*) from steward_reports
		where profile_scope='default' and status in ('complete','partial') and evidence_coverage<$1`,
		reportEvidenceCoverageThreshold).Scan(&out.LowCoverageReports); err != nil {
		return out, fmt.Errorf("count low-coverage reports: %w", err)
	}
	return out, nil
}

// nextContinuousIntelligenceDeadlines exposes the configured scheduler's
// next deterministic deadlines. These timestamps are promises about when the
// controller will next be eligible to act, not claims that activity or a model
// response will exist at that instant.
func nextContinuousIntelligenceDeadlines(now time.Time, settings IntelligenceSettings) (*time.Time, *time.Time) {
	if !settings.Enabled {
		return nil, nil
	}
	var nextConsolidation *time.Time
	if settings.Mode == "batch" {
		interval := time.Duration(settings.BatchIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 30 * time.Minute
		}
		grace := time.Duration(settings.BoundaryGraceSeconds) * time.Second
		if grace < 0 {
			grace = 0
		}
		value := now.UTC().Truncate(interval).Add(interval).Add(grace)
		nextConsolidation = &value
	}
	location, err := intelligenceScheduleLocation(settings.Timezone, time.Local)
	if err != nil {
		return nextConsolidation, nil
	}
	hour, minute, err := parseLocalClock(settings.DailyReportFallbackLocal)
	if err != nil {
		return nextConsolidation, nil
	}
	localNow := now.In(location)
	nextLocal := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if !nextLocal.After(localNow) {
		nextLocal = nextLocal.AddDate(0, 0, 1)
	}
	nextDaily := nextLocal.UTC()
	return nextConsolidation, &nextDaily
}

func (s *Service) loadBackgroundMetrics(ctx context.Context, now time.Time) (domain.StewardBackgroundMetrics, error) {
	windowEnd := now.UTC()
	windowStart := windowEnd.Add(-time.Hour)
	out := domain.StewardBackgroundMetrics{
		WindowStart:       windowStart,
		WindowEnd:         windowEnd,
		BatchStatusCounts: map[string]int{},
		ReminderFeedback1H: domain.StewardReminderFeedbackMetrics{
			ByAction: map[string]int{},
		},
		ModelUsage: domain.StewardModelUsageMetrics{
			Available: false,
			Reason:    "provider token and cost usage is not persisted",
		},
	}

	if err := s.db.Pool.QueryRow(ctx, `
		select
			(select count(*) from steward_observations where occurred_at >= $1 and occurred_at <= $2),
			(select count(*) from steward_activity_sessions where started_at >= $1 and started_at <= $2),
			(select count(*) from steward_agent_episodes
				where status='completed' and coalesce(completed_at,updated_at) >= $1 and coalesce(completed_at,updated_at) <= $2),
			(select count(*) from steward_agent_episodes
				where status='failed' and coalesce(completed_at,updated_at) >= $1 and coalesce(completed_at,updated_at) <= $2)
	`, windowStart, windowEnd).Scan(
		&out.Observations1H,
		&out.Sessions1H,
		&out.ModelEpisodes1H.Completed,
		&out.ModelEpisodes1H.Failed,
	); err != nil {
		return domain.StewardBackgroundMetrics{}, fmt.Errorf("get one-hour background metrics: %w", err)
	}

	out.SessionCompressionRatio = domain.StewardRatioMetric{
		Numerator:   out.Observations1H,
		Denominator: out.Sessions1H,
	}
	if out.Sessions1H > 0 {
		out.SessionCompressionRatio.Available = true
		out.SessionCompressionRatio.Value = float64(out.Observations1H) / float64(out.Sessions1H)
	} else {
		out.SessionCompressionRatio.Reason = "no activity sessions were created in the observation window"
	}

	batchRows, err := s.db.Pool.Query(ctx, `
		select status,count(*)
		from steward_activity_batches
		where created_at >= $1 and created_at <= $2
		group by status
		order by status
	`, windowStart, windowEnd)
	if err != nil {
		return domain.StewardBackgroundMetrics{}, fmt.Errorf("get activity batch status metrics: %w", err)
	}
	for batchRows.Next() {
		var status string
		var count int
		if err := batchRows.Scan(&status, &count); err != nil {
			batchRows.Close()
			return domain.StewardBackgroundMetrics{}, fmt.Errorf("scan activity batch status metrics: %w", err)
		}
		out.BatchStatusCounts[status] = count
	}
	if err := batchRows.Err(); err != nil {
		batchRows.Close()
		return domain.StewardBackgroundMetrics{}, fmt.Errorf("iterate activity batch status metrics: %w", err)
	}
	batchRows.Close()

	var coverageCount int
	var averageCoverage float64
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*),coalesce(avg(evidence_coverage),0)
		from steward_reports
		where status in ('complete','partial')
	`).Scan(&coverageCount, &averageCoverage); err != nil {
		return domain.StewardBackgroundMetrics{}, fmt.Errorf("get report coverage metrics: %w", err)
	}
	out.ReportCoverage.ReportCount = coverageCount
	if coverageCount > 0 {
		out.ReportCoverage.Available = true
		out.ReportCoverage.Average = averageCoverage
	} else {
		out.ReportCoverage.Reason = "no completed or partial reports are available"
	}

	feedbackRows, err := s.db.Pool.Query(ctx, `
		select action,count(*)
		from steward_reminder_feedback
		where created_at >= $1 and created_at <= $2
		group by action
		order by action
	`, windowStart, windowEnd)
	if err != nil {
		return domain.StewardBackgroundMetrics{}, fmt.Errorf("get reminder feedback metrics: %w", err)
	}
	for feedbackRows.Next() {
		var action string
		var count int
		if err := feedbackRows.Scan(&action, &count); err != nil {
			feedbackRows.Close()
			return domain.StewardBackgroundMetrics{}, fmt.Errorf("scan reminder feedback metrics: %w", err)
		}
		out.ReminderFeedback1H.ByAction[action] = count
		out.ReminderFeedback1H.Total += count
	}
	if err := feedbackRows.Err(); err != nil {
		feedbackRows.Close()
		return domain.StewardBackgroundMetrics{}, fmt.Errorf("iterate reminder feedback metrics: %w", err)
	}
	feedbackRows.Close()

	return out, nil
}

func (s *Service) latestBackgroundOutcome(ctx context.Context) (*StewardBackgroundOutcome, error) {
	var item StewardBackgroundOutcome
	err := s.db.Pool.QueryRow(ctx, `
		select kind,status,summary,updated_at from (
			select 'activity_batch'::text as kind,status,coalesce(nullif(response_summary,''),error_summary) as summary,updated_at
			from steward_activity_batches
			union all
			select kind,status,error_summary as summary,updated_at from steward_memory_consolidation_runs
			union all
			select 'report:'||cadence as kind,status,coalesce(nullif(summary,''),error_summary) as summary,updated_at
			from steward_reports
		) outcomes order by updated_at desc limit 1
	`).Scan(&item.Kind, &item.Status, &item.Summary, &item.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest background outcome: %w", err)
	}
	return &item, nil
}

func classifyBackgroundStatus(status StewardBackgroundStatus) (string, []string) {
	state, details := classifyBackgroundStatusDetailed(status)
	return state, issueMessages(details)
}

func classifyBackgroundStatusDetailed(status StewardBackgroundStatus) (string, []domain.StewardHealthIssue) {
	if !status.Enabled {
		return "disabled", []domain.StewardHealthIssue{{
			Code: "intelligence_disabled", Message: "持续智能已关闭", Action: "在持续智能设置中启用后台智能",
		}}
	}
	issues := []domain.StewardHealthIssue{}
	if strings.TrimSpace(status.Agent.Status) != StatusRunning {
		issues = append(issues, domain.StewardHealthIssue{
			Code: "agent_not_running", Message: "Agent 未运行", Action: "启动或重启 Steward Agent",
		})
	}
	for _, loop := range status.Loops {
		if loop.Enabled && loop.ConsecutiveFailures > 0 {
			issues = append(issues, domain.StewardHealthIssue{
				Code:    "background_loop_failing",
				Message: fmt.Sprintf("%s 连续失败 %d 次", loop.Name, loop.ConsecutiveFailures),
				Action:  "查看该后台循环的最近错误并重试",
			})
		}
	}
	if len(status.Pipeline.Sources) == 0 {
		issues = append(issues, domain.StewardHealthIssue{
			Code: "activity_source_missing", Message: "尚未收到活动采集源心跳", Action: "检查并重新连接 Session Companion 或 ActivityWatch",
		})
	}
	for _, source := range status.Pipeline.Sources {
		if !source.Fresh {
			label := defaultString(source.CollectorName, source.SourceKey)
			issues = append(issues, domain.StewardHealthIssue{
				Code: "activity_source_stale", Message: label + " 数据已过期或采集异常", Action: "检查采集器连接、权限和最近心跳",
			})
		}
	}
	if !status.Model.Enabled {
		issues = append(issues, domain.StewardHealthIssue{
			Code: "model_unconfigured", Message: "模型未配置", Action: "配置并测试 AI 模型连接",
		})
	} else if status.Model.CircuitOpen {
		issues = append(issues, domain.StewardHealthIssue{
			Code: "model_circuit_open", Message: "模型熔断器暂时开启", Action: "检查模型服务错误，等待熔断恢复或运行连接检查",
		})
	}
	if status.Pipeline.FailedBatches > 0 || status.IntelligenceQueue.Failed > 0 {
		issues = append(issues, domain.StewardHealthIssue{
			Code: "background_batch_failed", Message: "存在失败的后台批次", Action: "查看失败批次的错误详情并补跑",
		})
	}
	if len(status.ReportHealth.MissingDailyPeriods) > 0 {
		issues = append(issues, domain.StewardHealthIssue{
			Code:    "report_missing",
			Message: fmt.Sprintf("缺少 %d 个应生成的日报：%s", len(status.ReportHealth.MissingDailyPeriods), strings.Join(status.ReportHealth.MissingDailyPeriods, "、")),
			Action:  "检查日报后台任务并补跑缺失周期",
		})
	}
	if status.ReportHealth.LowCoverageReports > 0 {
		issues = append(issues, domain.StewardHealthIssue{
			Code:    "report_low_coverage",
			Message: fmt.Sprintf("存在 %d 份证据覆盖率低于 %.0f%% 的报告", status.ReportHealth.LowCoverageReports, status.ReportHealth.RequiredCoverage*100),
			Action:  "检查报告缺失证据与有界重试任务",
		})
	}
	if strings.TrimSpace(status.Agent.Status) != StatusRunning {
		return "unhealthy", issues
	}
	if len(issues) > 0 {
		return "degraded", issues
	}
	return "healthy", issues
}

func issueMessages(details []domain.StewardHealthIssue) []string {
	messages := make([]string, 0, len(details))
	for _, detail := range details {
		messages = append(messages, detail.Message)
	}
	return messages
}
