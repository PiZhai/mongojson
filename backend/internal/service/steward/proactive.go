package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	proactiveCadenceDaily  = "daily"
	proactiveCadenceWeekly = "weekly"
	proactiveSilentToken   = "[SILENT]"
	proactiveConversation  = "主动管家"
)

type RunProactiveInput struct {
	Force   bool   `json:"force"`
	Cadence string `json:"cadence"`
}

type proactivePeriod struct {
	Cadence string
	Key     string
	Start   time.Time
	End     time.Time
}

type proactiveContext struct {
	Cadence  string           `json:"cadence"`
	Start    string           `json:"period_start"`
	End      string           `json:"period_end"`
	Activity []map[string]any `json:"activity_sessions"`
	Habits   []map[string]any `json:"habits"`
	Insights []map[string]any `json:"insights"`
	Tasks    []map[string]any `json:"open_tasks"`
	Events   []map[string]any `json:"events"`
	Memories []map[string]any `json:"long_term_memories"`
}

// RunProactiveCycle performs model-led reflection. No fixed rule chooses an
// action: the model may remain silent, speak in the dedicated conversation, or
// call registered tools. Runtime policy remains the final execution authority.
func (s *Service) RunProactiveCycle(ctx context.Context, input RunProactiveInput) ([]domain.StewardProactiveRun, error) {
	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.Paused {
		return nil, nil
	}
	now := time.Now()
	periods := dueProactivePeriods(now, input)
	result := make([]domain.StewardProactiveRun, 0, len(periods))
	for _, period := range periods {
		run, created, err := s.claimProactiveRun(ctx, period)
		if err != nil {
			return result, err
		}
		if !created {
			continue
		}
		run = s.processProactiveRun(ctx, run)
		result = append(result, run)
	}
	return result, nil
}

func dueProactivePeriods(now time.Time, input RunProactiveInput) []proactivePeriod {
	location := now.Location()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	periods := []proactivePeriod{}
	wants := func(cadence string) bool {
		value := strings.ToLower(strings.TrimSpace(input.Cadence))
		return value == "" || value == "all" || value == cadence
	}
	if wants(proactiveCadenceDaily) && (input.Force || now.Hour() >= intEnv("STEWARD_PROACTIVE_DAILY_HOUR", 20)) {
		key := dayStart.Format("2006-01-02")
		if input.Force {
			key += "-manual-" + now.Format("150405")
		}
		periods = append(periods, proactivePeriod{Cadence: proactiveCadenceDaily, Key: key, Start: dayStart, End: now})
	}
	weekDay := intEnv("STEWARD_PROACTIVE_WEEKLY_DAY", int(time.Sunday))
	if wants(proactiveCadenceWeekly) && (input.Force || (int(now.Weekday()) == weekDay && now.Hour() >= intEnv("STEWARD_PROACTIVE_WEEKLY_HOUR", 20))) {
		year, week := now.ISOWeek()
		key := fmt.Sprintf("%04d-W%02d", year, week)
		if input.Force {
			key += "-manual-" + now.Format("150405")
		}
		periods = append(periods, proactivePeriod{Cadence: proactiveCadenceWeekly, Key: key, Start: dayStart.AddDate(0, 0, -7), End: now})
	}
	return periods
}

func (s *Service) claimProactiveRun(ctx context.Context, period proactivePeriod) (domain.StewardProactiveRun, bool, error) {
	id, now := uuid.NewString(), time.Now().UTC()
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_proactive_runs (id,cadence,period_key,period_start,period_end,status,created_at,updated_at)
		values ($1,$2,$3,$4,$5,'processing',$6,$6)
		on conflict (cadence,period_key) do nothing
		returning id,cadence,period_key,period_start,period_end,status,summary,analysis,decision,
		          conversation_id::text,message_id::text,execution_id::text,provider,model,error_summary,
		          audit_id::text,created_at,updated_at,completed_at
	`, id, period.Cadence, period.Key, period.Start.UTC(), period.End.UTC(), now)
	run, err := scanProactiveRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardProactiveRun{}, false, nil
	}
	return run, err == nil, err
}

func (s *Service) processProactiveRun(ctx context.Context, run domain.StewardProactiveRun) domain.StewardProactiveRun {
	if _, err := s.AggregateActivitySessions(ctx, 5000); err != nil {
		return s.finishProactiveRun(ctx, run, "failed", "", nil, err)
	}
	if run.Cadence == proactiveCadenceWeekly {
		if _, err := s.EvaluateHabitsAndInsights(ctx, run.PeriodEnd); err != nil {
			return s.finishProactiveRun(ctx, run, "failed", "", nil, err)
		}
	}
	activity, err := s.buildProactiveContext(ctx, run)
	if err != nil {
		return s.finishProactiveRun(ctx, run, "failed", "", nil, err)
	}
	encoded, err := json.Marshal(activity)
	if err != nil {
		return s.finishProactiveRun(ctx, run, "failed", "", nil, err)
	}
	policy, err := s.ResolveDataPolicy(ctx, DataD2, "proactive:"+run.Cadence)
	if err != nil || policy.ModelMode != PolicyModeAuto {
		if err == nil {
			err = fmt.Errorf("proactive model disclosure is not automatic")
		}
		return s.finishProactiveRun(ctx, run, "blocked", "", nil, err)
	}
	permission, err := s.ResolvePermissionPolicy(ctx, PermissionA6, "model:proactive")
	if err != nil || permission.ExecutionMode != PolicyModeAuto {
		if err == nil {
			err = fmt.Errorf("proactive model permission is not automatic")
		}
		return s.finishProactiveRun(ctx, run, "blocked", "", nil, err)
	}
	advisor := s.autonomyAdvisor()
	analyzer, ok := advisor.(ObservationModelAdvisor)
	if !ok || !advisor.Status().Enabled {
		return s.finishProactiveRun(ctx, run, "failed", "", nil, fmt.Errorf("configured model does not support proactive analysis"))
	}
	content := string(encoded)
	if !ownerModeEnabled() {
		content = redactCredentialText(content)
	}
	analysis, err := analyzer.AnalyzeObservation(ctx, ObservationModelInput{
		Source: "proactive:" + run.Cadence, Type: run.Cadence + "_reflection", DataLevel: DataD2,
		ContentMode: policy.ModelContentMode, Content: content,
	})
	if err != nil {
		return s.finishProactiveRun(ctx, run, "failed", "", nil, err)
	}
	analysisMap := map[string]any{"summary": analysis.Summary, "insights": analysis.Insights, "suggested_actions": analysis.SuggestedActions}
	converser, ok := advisor.(ConversationAdvisor)
	if !ok {
		return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, fmt.Errorf("configured model does not support proactive tool decisions"))
	}
	prompt := proactiveDecisionPrompt(run, analysis)
	decision, err := converser.Converse(ctx, ConversationAdvisorInput{
		Message: prompt, DataLevel: DataD2, Tools: s.runtimeTools.specs(),
		Devices: s.conversationAdvisorDevices(ctx), KnownFolders: runtimeKnownFolders(), CurrentTime: time.Now(),
	})
	if err != nil {
		return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, err)
	}
	status := advisor.Status()
	run.Provider, run.Model = status.Provider, status.Model
	if decision.Intent == "execution" && decision.ExecutionPlan != nil {
		conversation, err := s.ensureProactiveConversation(ctx)
		if err != nil {
			return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, err)
		}
		trigger, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleSystem, prompt, DataD2, status.Model, "proactive:"+run.Cadence+":"+run.PeriodKey)
		if err != nil {
			return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, err)
		}
		plan := *decision.ExecutionPlan
		instruction, summary := proactiveExecutionText(run, analysis, plan)
		plan.Summary = summary
		message, execution, err := s.createConversationExecutionFromModel(ctx, conversation, trigger, instruction, DataD2, decision.TargetDevice, plan)
		if err != nil {
			return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, err)
		}
		run.ConversationID, run.MessageID, run.ExecutionID = &conversation.ID, &message.ID, &execution.ID
		run.Decision = "execution"
		return s.finishProactiveRun(ctx, run, "execution", analysis.Summary, analysisMap, nil)
	}
	reply := strings.TrimSpace(decision.Reply)
	if reply == "" || strings.EqualFold(reply, proactiveSilentToken) {
		run.Decision = "silent"
		return s.finishProactiveRun(ctx, run, "silent", analysis.Summary, analysisMap, nil)
	}
	conversation, err := s.ensureProactiveConversation(ctx)
	if err != nil {
		return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, err)
	}
	message, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, reply, DataD2, status.Model, "proactive:"+run.Cadence+":"+run.PeriodKey)
	if err != nil {
		return s.finishProactiveRun(ctx, run, "failed", analysis.Summary, analysisMap, err)
	}
	run.ConversationID, run.MessageID, run.Decision = &conversation.ID, &message.ID, "message"
	return s.finishProactiveRun(ctx, run, "message", analysis.Summary, analysisMap, nil)
}

func proactiveExecutionText(run domain.StewardProactiveRun, analysis ObservationModelOutput, plan RuntimePlanDraft) (string, string) {
	labels := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		label := strings.TrimSpace(step.Title)
		if label == "" {
			label = strings.TrimSpace(step.ToolName)
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	cadence := "每日"
	if run.Cadence == proactiveCadenceWeekly {
		cadence = "每周"
	}
	activity := defaultString(strings.Join(labels, "；"), "执行模型选择的低风险帮助")
	summary := truncateAdvisorText(cadence+"主动帮助："+activity, 500)
	instruction := truncateAdvisorText(summary+"。依据本次归纳："+strings.TrimSpace(analysis.Summary), 2000)
	return instruction, summary
}

func proactiveDecisionPrompt(run domain.StewardProactiveRun, analysis ObservationModelOutput) string {
	actions := strings.Join(analysis.SuggestedActions, "；")
	executionRule := "安全层会重新计算权限和风险；高风险动作只能进入确认。"
	if ownerModeEnabled() {
		executionRule = "设备所有者已授权调用全部已提供工具；安全层只校验参数、真实能力、证据、签名和全局急停，不按 A/D 等级限制你的决定。"
	}
	return fmt.Sprintf(`这是一次%s主动审视，时间范围 %s 至 %s。
模型归纳：%s
可能的洞察：%s
分析阶段提出的可能动作：%s

请独立判断现在是否值得打扰用户或采取行动：
- 没有足够价值、证据不足、重复提醒或不紧急时，只回复 [SILENT]。
- 只需沟通时，用自然、简短的中文直接告诉用户，并说明依据；不要暴露内部分类。
- 值得采取动作时，直接使用提供的 tools。可以创建任务、保存长期记忆或执行低风险帮助；不要把动作伪装成文本。
- 不要根据单次活动推断稳定人格，不要重复现有任务，不要为了显得主动而制造提醒。
- %s`, run.Cadence,
		run.PeriodStart.Local().Format(time.RFC3339), run.PeriodEnd.Local().Format(time.RFC3339),
		analysis.Summary, strings.Join(analysis.Insights, "；"), actions, executionRule)
}

func (s *Service) buildProactiveContext(ctx context.Context, run domain.StewardProactiveRun) (proactiveContext, error) {
	result := proactiveContext{Cadence: run.Cadence, Start: run.PeriodStart.Format(time.RFC3339), End: run.PeriodEnd.Format(time.RFC3339)}
	sessions, err := s.ListActivitySessions(ctx, 500)
	if err != nil {
		return result, err
	}
	for _, item := range sessions {
		if item.EndedAt.Before(run.PeriodStart) || item.StartedAt.After(run.PeriodEnd) || (!ownerModeEnabled() && dataLevelRank(item.DataLevel) > dataLevelRank(DataD2)) {
			continue
		}
		result.Activity = append(result.Activity, map[string]any{"title": item.Title, "summary": item.Summary, "source": item.Source, "start": item.StartedAt, "end": item.EndedAt, "samples": item.ObservationCount})
	}
	habits, err := s.ListHabits(ctx, 100)
	if err != nil {
		return result, err
	}
	for _, item := range habits {
		if ownerModeEnabled() || dataLevelRank(item.DataLevel) <= dataLevelRank(DataD2) {
			result.Habits = append(result.Habits, map[string]any{"title": item.Title, "summary": item.Summary, "status": item.Status, "confidence": item.Confidence, "evidence": item.EvidenceCount})
		}
	}
	insights, err := s.ListInsights(ctx, 100)
	if err != nil {
		return result, err
	}
	for _, item := range insights {
		if ownerModeEnabled() || dataLevelRank(item.DataLevel) <= dataLevelRank(DataD2) {
			result.Insights = append(result.Insights, map[string]any{"title": item.Title, "summary": item.Summary, "suggested_action": item.SuggestedAction, "status": item.Status})
		}
	}
	tasks, err := s.ListTasks(ctx, 200)
	if err != nil {
		return result, err
	}
	for _, item := range tasks {
		if item.Status == StatusOpen && (ownerModeEnabled() || dataLevelRank(item.DataLevel) <= dataLevelRank(DataD2)) {
			result.Tasks = append(result.Tasks, map[string]any{"title": item.Title, "description": item.Description, "due_at": item.DueAt, "priority": item.Priority})
		}
	}
	events, err := s.ListEvents(ctx, 200)
	if err != nil {
		return result, err
	}
	for _, item := range events {
		if (ownerModeEnabled() || dataLevelRank(item.DataLevel) <= dataLevelRank(DataD2)) && (item.UpdatedAt.After(run.PeriodStart) || item.Status == StatusActive) {
			result.Events = append(result.Events, map[string]any{"title": item.Title, "summary": item.Summary, "status": item.Status, "updated_at": item.UpdatedAt})
		}
	}
	memories, err := s.ListMemories(ctx, 200)
	if err != nil {
		return result, err
	}
	for _, item := range memories {
		if (ownerModeEnabled() || dataLevelRank(item.DataLevel) <= dataLevelRank(DataD2)) && item.Status != StatusArchived {
			result.Memories = append(result.Memories, map[string]any{"title": item.Title, "summary": item.Summary, "scope": item.Scope, "confidence": item.Confidence})
		}
	}
	return result, nil
}

func (s *Service) ensureProactiveConversation(ctx context.Context) (domain.StewardConversation, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `select id::text from steward_conversations where title=$1 and status='active' order by created_at limit 1`, proactiveConversation).Scan(&id)
	if err == nil {
		return s.getConversation(ctx, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardConversation{}, err
	}
	return s.CreateConversation(ctx, CreateConversationInput{Title: proactiveConversation, DataLevel: DataD2})
}

func (s *Service) finishProactiveRun(ctx context.Context, run domain.StewardProactiveRun, status, summary string, analysis map[string]any, cause error) domain.StewardProactiveRun {
	now := time.Now().UTC()
	run.Status, run.Summary, run.Analysis, run.UpdatedAt, run.CompletedAt = status, truncateAdvisorText(summary, 8000), analysis, now, &now
	if run.Analysis == nil {
		run.Analysis = map[string]any{}
	}
	if cause != nil {
		run.ErrorSummary = sanitizeRuntimeError(cause)
	}
	confirmed, syncable := false, false
	resultStatus := ResultOK
	if status == "blocked" {
		resultStatus = ResultBlocked
	} else if status == "failed" {
		resultStatus = ResultFailed
	}
	auditID, _ := s.recordAudit(ctx, AuditInput{Actor: "proactive-model", Action: "proactive." + status, TargetType: "proactive_run", TargetID: &run.ID,
		Source: "proactive:" + run.Cadence, PermissionLevel: PermissionA6, DataLevel: DataD2,
		InputSummary: run.Cadence + " model reflection", OutputSummary: defaultString(run.Decision, status), Reason: run.ErrorSummary,
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: resultStatus})
	if auditID != "" {
		run.AuditID = &auditID
	}
	_, _ = s.db.Pool.Exec(ctx, `
		update steward_proactive_runs set status=$2,summary=$3,analysis=$4,decision=$5,
		conversation_id=$6,message_id=$7,execution_id=$8,provider=$9,model=$10,error_summary=$11,
		audit_id=$12,updated_at=$13,completed_at=$13 where id=$1
	`, run.ID, run.Status, run.Summary, run.Analysis, run.Decision, run.ConversationID, run.MessageID,
		run.ExecutionID, run.Provider, run.Model, run.ErrorSummary, run.AuditID, now)
	return run
}

func (s *Service) ListProactiveRuns(ctx context.Context, limit int) ([]domain.StewardProactiveRun, error) {
	limit = normalizeLimit(limit, 50, 200)
	rows, err := s.db.Pool.Query(ctx, `
		select id,cadence,period_key,period_start,period_end,status,summary,analysis,decision,
		       conversation_id::text,message_id::text,execution_id::text,provider,model,error_summary,
		       audit_id::text,created_at,updated_at,completed_at
		from steward_proactive_runs order by created_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardProactiveRun{}
	for rows.Next() {
		item, err := scanProactiveRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanProactiveRun(row rowScanner) (domain.StewardProactiveRun, error) {
	var item domain.StewardProactiveRun
	err := row.Scan(&item.ID, &item.Cadence, &item.PeriodKey, &item.PeriodStart, &item.PeriodEnd,
		&item.Status, &item.Summary, &item.Analysis, &item.Decision, &item.ConversationID,
		&item.MessageID, &item.ExecutionID, &item.Provider, &item.Model, &item.ErrorSummary,
		&item.AuditID, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	return item, err
}
