package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

const (
	AutonomySettingsID = "default"

	AutonomyModeSuggestOnly = "suggest_only"
	AutonomyModeControlled  = "controlled"

	AutonomyPolicySuggest = "suggest"
	AutonomyPolicyConfirm = "confirm"
	AutonomyPolicyAuto    = "auto"
	AutonomyPolicyNever   = "never"

	ProposalCandidate = "candidate"
	ProposalApproved  = "approved"
	ProposalDismissed = "dismissed"
	ProposalExecuted  = "executed"
	ProposalBlocked   = "blocked"

	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"

	RunModeSimulate = "simulate"
	RunModeExecute  = "execute"
	RunSuccess      = "success"
	RunBlocked      = "blocked"
	RunFailed       = "failed"
)

type UpdateAutonomySettingsInput struct {
	Paused            *bool  `json:"paused"`
	Mode              string `json:"mode"`
	MaxAutoPermission string `json:"max_auto_permission"`
}

type UpdateAutonomyRuleInput struct {
	Policy             *string `json:"policy"`
	Enabled            *bool   `json:"enabled"`
	MaxPermissionLevel *string `json:"max_permission_level"`
	ScopeSummary       *string `json:"scope_summary"`
}

type CreateAutonomyProposalInput struct {
	RuleID           *string `json:"rule_id"`
	SourceEntityType string  `json:"source_entity_type"`
	SourceEntityID   *string `json:"source_entity_id"`
	Action           string  `json:"action"`
	Title            string  `json:"title"`
	Summary          string  `json:"summary"`
	TriggerReason    string  `json:"trigger_reason"`
	SuggestedAction  string  `json:"suggested_action"`
	RiskLevel        string  `json:"risk_level"`
	PermissionLevel  string  `json:"permission_level"`
	DataLevel        string  `json:"data_level"`
	Policy           string  `json:"policy"`
	ImpactSummary    string  `json:"impact_summary"`
}

type DismissAutonomyProposalsInput struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Reason string `json:"reason"`
}

type DismissAutonomyProposalsResult struct {
	Dismissed int      `json:"dismissed"`
	Status    string   `json:"status"`
	IDs       []string `json:"ids"`
}

type DecideApprovalInput struct {
	DecisionReason string `json:"decision_reason"`
}

func (s *Service) ensureS4Defaults(ctx context.Context, now time.Time) error {
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_autonomy_settings (id, paused, mode, max_auto_permission, updated_at)
		values ($1,false,$2,$3,$4)
		on conflict (id) do nothing
	`, AutonomySettingsID, AutonomyModeSuggestOnly, PermissionA3, now); err != nil {
		return fmt.Errorf("ensure autonomy settings: %w", err)
	}

	defaults := []domain.StewardAutonomyRule{
		{
			Name:               "event-follow-up-candidate",
			TriggerType:        "event.created",
			TargetType:         "task",
			Action:             "create_follow_up_task",
			Policy:             AutonomyPolicyConfirm,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            true,
			ScopeSummary:       "从手动事件生成待确认的跟进任务建议",
		},
		{
			Name:               "stale-open-task-review",
			TriggerType:        "task.stale",
			TargetType:         "task",
			Action:             "create_review_checklist",
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            true,
			ScopeSummary:       "为长期未更新任务生成复盘或检查清单建议",
		},
		{
			Name:               "event-knowledge-summary",
			TriggerType:        "event.created",
			TargetType:         "knowledge_item",
			Action:             AutonomyActionCreateKnowledgeSummary,
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            true,
			ScopeSummary:       "把 D0/D1 事件整理为可索引的本地知识摘要",
		},
		{
			Name:               "due-task-reminder",
			TriggerType:        "task.due",
			TargetType:         "task",
			Action:             AutonomyActionCreateReminderTask,
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            true,
			ScopeSummary:       "为已到期或 24 小时内到期的本地任务生成提醒",
		},
		{
			Name:               "sync-conflict-diagnostics",
			TriggerType:        "sync.conflict",
			TargetType:         "knowledge_item",
			Action:             AutonomyActionRunReadOnlyDiagnostics,
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            true,
			ScopeSummary:       "同步冲突出现时建议运行只读诊断并保存本地报告",
		},
		{
			Name:               "high-risk-guardrail",
			TriggerType:        "risk.detected",
			TargetType:         "plan",
			Action:             "block_high_risk_execution",
			Policy:             AutonomyPolicyNever,
			RiskLevel:          "high",
			MaxPermissionLevel: PermissionA4,
			Enabled:            true,
			ScopeSummary:       "高风险操作只生成计划和审批请求，不直接执行",
		},
	}
	for _, rule := range defaults {
		if _, err := s.db.Pool.Exec(ctx, `
			insert into steward_autonomy_rules (
				id, name, trigger_type, target_type, action, policy, risk_level,
				max_permission_level, enabled, scope_summary, created_at, updated_at
			)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
			on conflict (name) do nothing
		`, uuid.NewString(), rule.Name, rule.TriggerType, rule.TargetType, rule.Action, rule.Policy,
			rule.RiskLevel, rule.MaxPermissionLevel, rule.Enabled, rule.ScopeSummary, now); err != nil {
			return fmt.Errorf("ensure autonomy rule: %w", err)
		}
	}
	return nil
}

func (s *Service) GetAutonomyOverview(ctx context.Context) (domain.StewardAutonomyOverview, error) {
	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	rules, err := s.ListAutonomyRules(ctx)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	proposals, err := s.ListAutonomyProposals(ctx, "", 20)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	approvals, err := s.ListApprovalRequests(ctx, ApprovalPending, 20)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	runs, err := s.ListAutonomousRuns(ctx, 20)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	return domain.StewardAutonomyOverview{
		Settings:  settings,
		Advisor:   s.autonomyAdvisor().Status(),
		Actions:   s.autonomyActionCapabilities(),
		Rules:     rules,
		Proposals: proposals,
		Approvals: approvals,
		Runs:      runs,
	}, nil
}

func (s *Service) GetAutonomySettings(ctx context.Context) (domain.StewardAutonomySettings, error) {
	var settings domain.StewardAutonomySettings
	if err := s.db.Pool.QueryRow(ctx, `
		select id, paused, mode, max_auto_permission, updated_at
		from steward_autonomy_settings
		where id = $1
	`, AutonomySettingsID).Scan(&settings.ID, &settings.Paused, &settings.Mode,
		&settings.MaxAutoPermission, &settings.UpdatedAt); err != nil {
		return domain.StewardAutonomySettings{}, fmt.Errorf("get autonomy settings: %w", err)
	}
	return settings, nil
}

func (s *Service) UpdateAutonomySettings(ctx context.Context, input UpdateAutonomySettingsInput) (domain.StewardAutonomySettings, error) {
	current, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomySettings{}, err
	}
	paused := current.Paused
	if input.Paused != nil {
		paused = *input.Paused
	}
	mode := current.Mode
	if strings.TrimSpace(input.Mode) != "" {
		mode = normalizeAutonomyMode(input.Mode)
	}
	maxPermission := current.MaxAutoPermission
	if strings.TrimSpace(input.MaxAutoPermission) != "" {
		maxPermission = strings.TrimSpace(input.MaxAutoPermission)
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_settings
		set paused = $1, mode = $2, max_auto_permission = $3, updated_at = $4
		where id = $5
	`, paused, mode, maxPermission, now, AutonomySettingsID); err != nil {
		return domain.StewardAutonomySettings{}, fmt.Errorf("update autonomy settings: %w", err)
	}
	action := "autonomy.settings.update"
	if current.Paused != paused {
		action = "autonomy.pause"
		if !paused {
			action = "autonomy.resume"
		}
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          action,
		TargetType:      "autonomy",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    fmt.Sprintf("paused=%t mode=%s max=%s", paused, mode, maxPermission),
		OutputSummary:   "autonomy settings updated",
		ResultStatus:    ResultOK,
	})
	return s.GetAutonomySettings(ctx)
}

func (s *Service) ListAutonomyRules(ctx context.Context) ([]domain.StewardAutonomyRule, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, name, trigger_type, target_type, action, policy, risk_level,
		       max_permission_level, enabled, scope_summary, created_at, updated_at
		from steward_autonomy_rules
		order by enabled desc, name
	`)
	if err != nil {
		return nil, fmt.Errorf("list autonomy rules: %w", err)
	}
	defer rows.Close()

	rules := []domain.StewardAutonomyRule{}
	for rows.Next() {
		rule, err := scanAutonomyRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Service) UpdateAutonomyRule(ctx context.Context, id string, input UpdateAutonomyRuleInput) (domain.StewardAutonomyRule, error) {
	current, err := s.getAutonomyRule(ctx, id)
	if err != nil {
		return domain.StewardAutonomyRule{}, err
	}
	policy := current.Policy
	if input.Policy != nil {
		policy = normalizeAutonomyPolicy(*input.Policy)
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	maxPermission := current.MaxPermissionLevel
	if input.MaxPermissionLevel != nil {
		maxPermission = defaultString(*input.MaxPermissionLevel, current.MaxPermissionLevel)
	}
	scope := current.ScopeSummary
	if input.ScopeSummary != nil {
		scope = strings.TrimSpace(*input.ScopeSummary)
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_rules
		set policy = $1, enabled = $2, max_permission_level = $3, scope_summary = $4, updated_at = $5
		where id = $6
	`, policy, enabled, maxPermission, scope, now, id); err != nil {
		return domain.StewardAutonomyRule{}, fmt.Errorf("update autonomy rule: %w", err)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "autonomy.rule.update",
		TargetType:      "autonomy_rule",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    current.Name,
		OutputSummary:   fmt.Sprintf("enabled=%t policy=%s", enabled, policy),
		ResultStatus:    ResultOK,
	})
	return s.getAutonomyRule(ctx, id)
}

func (s *Service) RunAutonomyCycle(ctx context.Context, limit int) (domain.StewardAutonomyOverview, error) {
	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if settings.Paused {
		_, _ = s.recordAutonomousRun(ctx, nil, nil, RunModeSimulate, RunBlocked, "autonomy paused", "no proposals created", "resume autonomy to scan")
		return s.GetAutonomyOverview(ctx)
	}
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	if err := s.createEventFollowUpProposals(ctx, limit); err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if err := s.createStaleTaskProposals(ctx, limit); err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if err := s.createEventKnowledgeSummaryProposals(ctx, limit); err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if err := s.createDueTaskReminderProposals(ctx, limit); err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if err := s.createSyncConflictDiagnosticProposals(ctx, limit); err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if settings.Mode == AutonomyModeControlled {
		if err := s.executeControlledAutoProposals(ctx, limit); err != nil {
			return domain.StewardAutonomyOverview{}, err
		}
	}
	return s.GetAutonomyOverview(ctx)
}

func (s *Service) CreateAutonomyProposal(ctx context.Context, input CreateAutonomyProposalInput) (domain.StewardAutonomyProposal, error) {
	now := time.Now().UTC()
	policy := normalizeAutonomyPolicy(input.Policy)
	risk := defaultString(input.RiskLevel, "low")
	permission := defaultString(input.PermissionLevel, PermissionA3)
	action := defaultString(input.Action, AutonomyActionCreateLocalTask)
	score := s.autonomyProposalScorer().Score(input)
	status := ProposalCandidate
	blockedReason := ""
	executor, executorFound := s.autonomyActionExecutor(action)
	if policy == AutonomyPolicyNever || isHighRisk(risk, permission) {
		status = ProposalBlocked
		blockedReason = "high risk or denied policy"
	} else if !executorFound {
		status = ProposalBlocked
		blockedReason = "no registered executor for action " + action
	} else if maxPermission := defaultString(executor.Capability().MaxPermissionLevel, PermissionA3); permissionRank(permission) > permissionRank(maxPermission) {
		status = ProposalBlocked
		blockedReason = fmt.Sprintf("action %s allows up to %s", action, maxPermission)
	}
	auditResult := ResultOK
	if status == ProposalBlocked {
		auditResult = ResultBlocked
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.proposal.create",
		TargetType:      "autonomy_proposal",
		Source:          defaultString(input.SourceEntityType, "system"),
		PermissionLevel: permission,
		DataLevel:       defaultString(input.DataLevel, DataD0),
		InputSummary:    input.TriggerReason,
		OutputSummary:   input.Title,
		Reason:          blockedReason,
		ResultStatus:    auditResult,
	})
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_autonomy_proposals (
			id, rule_id, source_entity_type, source_entity_id, action, title, summary, trigger_reason,
			suggested_action, risk_level, permission_level, data_level, status, policy,
			impact_summary, score, score_reason, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19)
		on conflict (rule_id, source_entity_type, source_entity_id) do update
		set summary = excluded.summary,
		    trigger_reason = excluded.trigger_reason,
		    suggested_action = excluded.suggested_action,
		    impact_summary = excluded.impact_summary,
		    score = excluded.score,
		    score_reason = excluded.score_reason,
		    updated_at = excluded.updated_at
		returning id, coalesce(rule_id::text, ''), source_entity_type, coalesce(source_entity_id::text, ''), action, title, summary,
		          trigger_reason, suggested_action, risk_level, permission_level, data_level, status,
		          policy, impact_summary, score, score_reason, coalesce(created_task_id::text, ''), execution_target_type,
		          execution_target_id, coalesce(audit_id::text, ''), created_at, updated_at
	`, uuid.NewString(), input.RuleID, defaultString(input.SourceEntityType, "manual"),
		input.SourceEntityID, action, defaultString(input.Title, "自主建议"), strings.TrimSpace(input.Summary),
		strings.TrimSpace(input.TriggerReason), strings.TrimSpace(input.SuggestedAction), risk,
		permission, defaultString(input.DataLevel, DataD0), status, policy,
		strings.TrimSpace(input.ImpactSummary), score.Value, score.Reason, auditID, now)
	proposal, err := scanAutonomyProposal(row)
	if err != nil {
		return domain.StewardAutonomyProposal{}, fmt.Errorf("create autonomy proposal: %w", err)
	}
	if status == ProposalBlocked {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "review blocked autonomous proposal", blockedReason, proposal.ImpactSummary)
	}
	return proposal, nil
}

func (s *Service) ListAutonomyProposals(ctx context.Context, status string, limit int) ([]domain.StewardAutonomyProposal, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, coalesce(rule_id::text, ''), source_entity_type, coalesce(source_entity_id::text, ''), action, title, summary,
		       trigger_reason, suggested_action, risk_level, permission_level, data_level, status,
		       policy, impact_summary, score, score_reason, coalesce(created_task_id::text, ''), execution_target_type,
		       execution_target_id, coalesce(audit_id::text, ''), created_at, updated_at
		from steward_autonomy_proposals
		where ($1 = '' or status = $1)
		order by score desc, updated_at desc
		limit $2
	`, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, fmt.Errorf("list autonomy proposals: %w", err)
	}
	defer rows.Close()

	proposals := []domain.StewardAutonomyProposal{}
	for rows.Next() {
		proposal, err := scanAutonomyProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, proposal)
	}
	return proposals, rows.Err()
}

func (s *Service) ApproveAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomyProposal, error) {
	return s.updateProposalStatus(ctx, id, ProposalApproved, "autonomy.proposal.approve")
}

func (s *Service) DismissAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomyProposal, error) {
	return s.updateProposalStatus(ctx, id, ProposalDismissed, "autonomy.proposal.dismiss")
}

func (s *Service) DismissAutonomyProposals(ctx context.Context, input DismissAutonomyProposalsInput) (DismissAutonomyProposalsResult, error) {
	status, err := normalizeBulkDismissProposalStatus(input.Status)
	if err != nil {
		return DismissAutonomyProposalsResult{}, err
	}
	limit := normalizeBulkDismissLimit(input.Limit)
	result := DismissAutonomyProposalsResult{
		Status: status,
		IDs:    []string{},
	}

	rows, err := s.db.Pool.Query(ctx, `
		select id::text
		from steward_autonomy_proposals
		where status = $1
		order by updated_at desc
		limit $2
	`, status, limit)
	if err != nil {
		return result, fmt.Errorf("list autonomy proposals for bulk dismiss: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return result, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	for _, id := range ids {
		if _, err := s.updateProposalStatus(ctx, id, ProposalDismissed, "autonomy.proposal.bulk_dismiss.item"); err != nil {
			return result, err
		}
		result.Dismissed++
		result.IDs = append(result.IDs, id)
	}

	reason := defaultString(input.Reason, "bulk dismiss autonomy proposals")
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "autonomy.proposal.bulk_dismiss",
		TargetType:      "autonomy_proposal",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    fmt.Sprintf("status=%s limit=%d reason=%s", status, limit, reason),
		OutputSummary:   fmt.Sprintf("dismissed=%d", result.Dismissed),
		ResultStatus:    ResultOK,
	})
	return result, nil
}

func (s *Service) SimulateAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomousRun, error) {
	proposal, err := s.getAutonomyProposal(ctx, id)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	executor, err := s.resolveAutonomyProposalExecutor(proposal)
	if err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunBlocked,
			proposal.TriggerReason, err.Error(), "register a low-risk executor or revise the proposal action")
	}
	result, err := executor.Simulate(ctx, proposal)
	if err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunFailed,
			proposal.TriggerReason, "autonomy action simulation failed", err.Error())
	}
	if err := validateAutonomyExecutionResult(executor, result, false); err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunFailed,
			proposal.TriggerReason, "autonomy action simulation returned an invalid result", err.Error())
	}
	return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunSuccess,
		proposal.TriggerReason, result.ImpactSummary, result.RecoveryHint)
}

func (s *Service) ExecuteAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomousRun, error) {
	lease, err := acquireAutonomyExecutionLease(ctx, s.db.Pool, id)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	defer lease.Release()

	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	proposal, err := s.getAutonomyProposal(ctx, id)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	if proposal.Status == ProposalExecuted {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "proposal already executed; duplicate execution skipped", "inspect the created task or create a new proposal")
	}
	if proposal.Status == ProposalDismissed {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "proposal was dismissed; execution skipped", "create a new proposal if this action is still needed")
	}
	if proposal.Status == ProposalBlocked {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "manual high-risk review", proposal.RiskLevel, proposal.ImpactSummary)
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "proposal is blocked from autonomous execution", "manual review is required outside autonomy")
	}
	if settings.Paused {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "autonomy paused; execution skipped", "resume autonomy before executing")
	}
	if proposal.Status != ProposalApproved && proposal.Policy != AutonomyPolicyAuto {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "approve autonomous execution", "proposal needs explicit approval", proposal.ImpactSummary)
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "approval required before execution", "approve proposal or change rule policy")
	}
	if proposal.Policy == AutonomyPolicyNever || isHighRisk(proposal.RiskLevel, proposal.PermissionLevel) ||
		permissionRank(proposal.PermissionLevel) > permissionRank(settings.MaxAutoPermission) {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "manual high-risk review", proposal.RiskLevel, proposal.ImpactSummary)
		run, runErr := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "high-risk proposal blocked; plan only", "manually review and execute outside autonomy")
		if runErr != nil {
			return domain.StewardAutonomousRun{}, runErr
		}
		_, _ = s.updateProposalStatusLocked(ctx, id, ProposalBlocked, "autonomy.proposal.block")
		return run, nil
	}

	executor, err := s.resolveAutonomyProposalExecutor(proposal)
	if err != nil {
		run, runErr := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, err.Error(), "register a low-risk executor or revise the proposal action")
		if runErr != nil {
			return domain.StewardAutonomousRun{}, runErr
		}
		_, _ = s.updateProposalStatusLocked(ctx, id, ProposalBlocked, "autonomy.proposal.block")
		return run, nil
	}
	execution, err := executor.Execute(ctx, proposal)
	if err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunFailed,
			proposal.TriggerReason, "autonomy action execution failed", err.Error())
	}
	if err := validateAutonomyExecutionResult(executor, execution, true); err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunFailed,
			proposal.TriggerReason, "autonomy action execution returned an invalid result", err.Error())
	}
	var createdTaskID *string
	if execution.TargetType == "task" && strings.TrimSpace(execution.TargetID) != "" {
		createdTaskID = stringPtr(execution.TargetID)
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_proposals
		set status = $1, created_task_id = $2, execution_target_type = $3,
		    execution_target_id = $4, updated_at = $5
		where id = $6
	`, ProposalExecuted, createdTaskID, execution.TargetType, execution.TargetID, now, id); err != nil {
		return domain.StewardAutonomousRun{}, fmt.Errorf("mark autonomy proposal executed: %w", err)
	}
	run, err := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunSuccess,
		proposal.TriggerReason, execution.ImpactSummary, execution.RecoveryHint)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	var auditTargetID *string
	if _, parseErr := uuid.Parse(execution.TargetID); parseErr == nil {
		auditTargetID = stringPtr(execution.TargetID)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.execute",
		TargetType:      defaultString(execution.TargetType, "autonomy_action"),
		TargetID:        auditTargetID,
		Source:          "autonomy",
		PermissionLevel: proposal.PermissionLevel,
		DataLevel:       proposal.DataLevel,
		InputSummary:    "action=" + proposal.Action + "; " + proposal.TriggerReason,
		OutputSummary:   execution.ImpactSummary,
		ResultStatus:    ResultOK,
	})
	return run, nil
}

func (s *Service) resolveAutonomyProposalExecutor(proposal domain.StewardAutonomyProposal) (AutonomyActionExecutor, error) {
	executor, ok := s.autonomyActionExecutor(proposal.Action)
	if !ok {
		return nil, fmt.Errorf("no registered executor for autonomy action %s", proposal.Action)
	}
	if err := executorAllowsProposal(executor, proposal); err != nil {
		return nil, err
	}
	return executor, nil
}

func (s *Service) ListApprovalRequests(ctx context.Context, status string, limit int) ([]domain.StewardApprovalRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, coalesce(proposal_id::text, ''), requested_action, risk_summary, plan_summary, status,
		       coalesce(decided_by, ''), coalesce(decision_reason, ''), created_at, decided_at
		from steward_approval_requests
		where ($1 = '' or status = $1)
		order by created_at desc
		limit $2
	`, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, fmt.Errorf("list approval requests: %w", err)
	}
	defer rows.Close()
	approvals := []domain.StewardApprovalRequest{}
	for rows.Next() {
		item, err := scanApprovalRequest(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, item)
	}
	return approvals, rows.Err()
}

func (s *Service) ApproveRequest(ctx context.Context, id string, input DecideApprovalInput) (domain.StewardApprovalRequest, error) {
	return s.decideApproval(ctx, id, ApprovalApproved, input.DecisionReason)
}

func (s *Service) RejectRequest(ctx context.Context, id string, input DecideApprovalInput) (domain.StewardApprovalRequest, error) {
	return s.decideApproval(ctx, id, ApprovalRejected, input.DecisionReason)
}

func (s *Service) ListAutonomousRuns(ctx context.Context, limit int) ([]domain.StewardAutonomousRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, coalesce(proposal_id::text, ''), coalesce(rule_id::text, ''), mode, status, trigger_reason, impact_summary,
		       recovery_hint, coalesce(audit_id::text, ''), created_at
		from steward_autonomous_runs
		order by created_at desc
		limit $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list autonomous runs: %w", err)
	}
	defer rows.Close()
	runs := []domain.StewardAutonomousRun{}
	for rows.Next() {
		run, err := scanAutonomousRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Service) createEventFollowUpProposals(ctx context.Context, limit int) error {
	rule, err := s.getAutonomyRuleByName(ctx, "event-follow-up-candidate")
	if err != nil || !rule.Enabled || rule.Policy == AutonomyPolicyNever {
		return nil
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, title, summary, data_level
		from steward_events
		where deleted_at is null
		  and status = $1
		  and not exists (
		    select 1 from steward_autonomy_proposals p
		    where p.rule_id = $2 and p.source_entity_type = 'event' and p.source_entity_id = steward_events.id
		  )
		order by created_at desc
		limit $3
	`, StatusActive, rule.ID, limit)
	if err != nil {
		return fmt.Errorf("scan event follow-up proposals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, summary, dataLevel string
		if err := rows.Scan(&id, &title, &summary, &dataLevel); err != nil {
			return err
		}
		input := CreateAutonomyProposalInput{
			RuleID:           &rule.ID,
			SourceEntityType: "event",
			SourceEntityID:   &id,
			Action:           rule.Action,
			Title:            "跟进：" + title,
			Summary:          summary,
			TriggerReason:    "事件可能需要后续处理：" + title,
			SuggestedAction:  "确认后创建一个低风险本地任务",
			RiskLevel:        rule.RiskLevel,
			PermissionLevel:  rule.MaxPermissionLevel,
			DataLevel:        dataLevel,
			Policy:           rule.Policy,
			ImpactSummary:    "只会在本地任务列表中创建待办，不会对外发送或修改系统",
		}
		input = s.enhanceAutonomyProposal(ctx, input, AutonomyAdvisorInput{
			Kind:             "event_follow_up",
			SourceEntityType: "event",
			Title:            title,
			Summary:          summary,
			DataLevel:        dataLevel,
			RuleName:         rule.Name,
			RuleScope:        rule.ScopeSummary,
		})
		_, err := s.CreateAutonomyProposal(ctx, input)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) createStaleTaskProposals(ctx context.Context, limit int) error {
	rule, err := s.getAutonomyRuleByName(ctx, "stale-open-task-review")
	if err != nil || !rule.Enabled || rule.Policy == AutonomyPolicyNever {
		return nil
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, title, description, data_level
		from steward_tasks
		where deleted_at is null
		  and status in ('open','in_progress','waiting')
		  and updated_at < $1
		  and not exists (
		    select 1 from steward_autonomy_proposals p
		    where p.rule_id = $2 and p.source_entity_type = 'task' and p.source_entity_id = steward_tasks.id
		  )
		order by updated_at asc
		limit $3
	`, cutoff, rule.ID, limit)
	if err != nil {
		return fmt.Errorf("scan stale task proposals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, description, dataLevel string
		if err := rows.Scan(&id, &title, &description, &dataLevel); err != nil {
			return err
		}
		input := CreateAutonomyProposalInput{
			RuleID:           &rule.ID,
			SourceEntityType: "task",
			SourceEntityID:   &id,
			Action:           rule.Action,
			Title:            "复盘：" + title,
			Summary:          description,
			TriggerReason:    "任务超过 24 小时未更新，可能需要复盘或拆解",
			SuggestedAction:  "生成检查清单或确认是否继续推进",
			RiskLevel:        rule.RiskLevel,
			PermissionLevel:  rule.MaxPermissionLevel,
			DataLevel:        dataLevel,
			Policy:           rule.Policy,
			ImpactSummary:    "只生成建议，不改变外部系统状态",
		}
		input = s.enhanceAutonomyProposal(ctx, input, AutonomyAdvisorInput{
			Kind:             "stale_task_review",
			SourceEntityType: "task",
			Title:            title,
			Summary:          description,
			DataLevel:        dataLevel,
			RuleName:         rule.Name,
			RuleScope:        rule.ScopeSummary,
		})
		_, err := s.CreateAutonomyProposal(ctx, input)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) createEventKnowledgeSummaryProposals(ctx context.Context, limit int) error {
	rule, err := s.getAutonomyRuleByName(ctx, "event-knowledge-summary")
	if err != nil || !rule.Enabled || rule.Policy == AutonomyPolicyNever {
		return nil
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, title, summary, data_level
		from steward_events
		where deleted_at is null
		  and status = $1
		  and data_level in ($2, $3)
		  and not exists (
		    select 1 from steward_autonomy_proposals p
		    where p.rule_id = $4 and p.source_entity_type = 'event' and p.source_entity_id = steward_events.id
		  )
		order by created_at desc
		limit $5
	`, StatusActive, DataD0, DataD1, rule.ID, limit)
	if err != nil {
		return fmt.Errorf("scan event knowledge summary proposals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, summary, dataLevel string
		if err := rows.Scan(&id, &title, &summary, &dataLevel); err != nil {
			return err
		}
		input := CreateAutonomyProposalInput{
			RuleID:           &rule.ID,
			SourceEntityType: "event",
			SourceEntityID:   &id,
			Action:           rule.Action,
			Title:            "摘要：" + title,
			Summary:          defaultString(summary, title),
			TriggerReason:    "低敏事件可整理为本地知识摘要",
			SuggestedAction:  "创建可检索的本地知识条目",
			RiskLevel:        rule.RiskLevel,
			PermissionLevel:  rule.MaxPermissionLevel,
			DataLevel:        dataLevel,
			Policy:           rule.Policy,
			ImpactSummary:    "只新增本地知识摘要，不修改或删除原事件",
		}
		input = s.enhanceAutonomyProposal(ctx, input, AutonomyAdvisorInput{
			Kind:             "event_knowledge_summary",
			SourceEntityType: "event",
			Title:            title,
			Summary:          summary,
			DataLevel:        dataLevel,
			RuleName:         rule.Name,
			RuleScope:        rule.ScopeSummary,
		})
		if _, err := s.CreateAutonomyProposal(ctx, input); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) createDueTaskReminderProposals(ctx context.Context, limit int) error {
	rule, err := s.getAutonomyRuleByName(ctx, "due-task-reminder")
	if err != nil || !rule.Enabled || rule.Policy == AutonomyPolicyNever {
		return nil
	}
	deadline := time.Now().UTC().Add(24 * time.Hour)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, title, description, data_level, due_at
		from steward_tasks
		where deleted_at is null
		  and status in ('open','in_progress','waiting')
		  and due_at is not null
		  and due_at <= $1
		  and not exists (
		    select 1 from steward_autonomy_proposals p
		    where p.rule_id = $2 and p.source_entity_type = 'task' and p.source_entity_id = steward_tasks.id
		  )
		order by due_at asc
		limit $3
	`, deadline, rule.ID, limit)
	if err != nil {
		return fmt.Errorf("scan due task reminder proposals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, description, dataLevel string
		var dueAt time.Time
		if err := rows.Scan(&id, &title, &description, &dataLevel, &dueAt); err != nil {
			return err
		}
		if _, err := s.CreateAutonomyProposal(ctx, CreateAutonomyProposalInput{
			RuleID:           &rule.ID,
			SourceEntityType: "task",
			SourceEntityID:   &id,
			Action:           rule.Action,
			Title:            "提醒：" + title,
			Summary:          defaultString(description, title),
			TriggerReason:    "任务已到期或将在 24 小时内到期：" + dueAt.UTC().Format(time.RFC3339),
			SuggestedAction:  "创建一个 24 小时内处理的本地提醒任务",
			RiskLevel:        rule.RiskLevel,
			PermissionLevel:  rule.MaxPermissionLevel,
			DataLevel:        dataLevel,
			Policy:           rule.Policy,
			ImpactSummary:    "只新增一个带截止时间的本地提醒任务",
		}); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) createSyncConflictDiagnosticProposals(ctx context.Context, limit int) error {
	rule, err := s.getAutonomyRuleByName(ctx, "sync-conflict-diagnostics")
	if err != nil || !rule.Enabled || rule.Policy == AutonomyPolicyNever {
		return nil
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, entity_type, reason
		from steward_sync_conflicts
		where status = $1
		  and not exists (
		    select 1 from steward_autonomy_proposals p
		    where p.rule_id = $2 and p.source_entity_type = 'sync_conflict' and p.source_entity_id = steward_sync_conflicts.id
		  )
		order by updated_at desc
		limit $3
	`, StatusOpen, rule.ID, limit)
	if err != nil {
		return fmt.Errorf("scan sync conflict diagnostic proposals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, entityType, reason string
		if err := rows.Scan(&id, &entityType, &reason); err != nil {
			return err
		}
		if _, err := s.CreateAutonomyProposal(ctx, CreateAutonomyProposalInput{
			RuleID:           &rule.ID,
			SourceEntityType: "sync_conflict",
			SourceEntityID:   &id,
			Action:           rule.Action,
			Title:            "诊断同步冲突：" + entityType,
			Summary:          reason,
			TriggerReason:    "发现未处理的同步冲突",
			SuggestedAction:  "运行只读状态检查并保存本地诊断报告",
			RiskLevel:        rule.RiskLevel,
			PermissionLevel:  rule.MaxPermissionLevel,
			DataLevel:        DataD0,
			Policy:           rule.Policy,
			ImpactSummary:    "只读取本地状态计数并新增一份诊断报告，不修改冲突",
		}); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) executeControlledAutoProposals(ctx context.Context, limit int) error {
	rows, err := s.db.Pool.Query(ctx, `
		select p.id::text
		from steward_autonomy_proposals p
		join steward_autonomy_rules r on r.id = p.rule_id
		where p.status = $1
		  and p.policy = $2
		  and r.enabled = true
		  and r.policy = $2
		  and r.action = p.action
		order by p.score desc, p.updated_at asc
		limit $3
	`, ProposalCandidate, AutonomyPolicyAuto, limit)
	if err != nil {
		return fmt.Errorf("list controlled auto proposals: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.ExecuteAutonomyProposal(ctx, id); err != nil {
			return fmt.Errorf("execute controlled auto proposal %s: %w", id, err)
		}
	}
	return nil
}

func (s *Service) enhanceAutonomyProposal(ctx context.Context, input CreateAutonomyProposalInput, advisorInput AutonomyAdvisorInput) CreateAutonomyProposalInput {
	advisor := s.autonomyAdvisor()
	if !advisor.Status().Enabled {
		return input
	}
	suggestion, err := advisor.Suggest(ctx, advisorInput)
	if err != nil {
		s.recordAdvisorSuggestionFallback(ctx, advisorInput, err)
		return input
	}
	if violation := advisorSuggestionSafetyViolation(suggestion); violation != "" {
		s.recordAdvisorSuggestionBlocked(ctx, advisorInput, violation)
		return input
	}
	enhanced := applyAdvisorSuggestion(input, suggestion)
	enhanced.RuleID = input.RuleID
	enhanced.SourceEntityType = input.SourceEntityType
	enhanced.SourceEntityID = input.SourceEntityID
	enhanced.Action = input.Action
	enhanced.RiskLevel = input.RiskLevel
	enhanced.PermissionLevel = input.PermissionLevel
	enhanced.DataLevel = input.DataLevel
	enhanced.Policy = input.Policy
	return enhanced
}

func (s *Service) recordAdvisorSuggestionBlocked(ctx context.Context, input AutonomyAdvisorInput, violation string) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return
	}
	userConfirmed := true
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.advisor.suggestion.block",
		TargetType:      "autonomy_advisor",
		Source:          "guardrail",
		PermissionLevel: PermissionA3,
		DataLevel:       defaultString(input.DataLevel, DataD0),
		InputSummary:    defaultString(input.RuleName, "advisor suggestion"),
		OutputSummary:   "advisor suggestion rejected by output guardrail",
		Reason:          violation,
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    ResultBlocked,
		ErrorSummary:    stringPtr(violation),
	})
}

func (s *Service) updateProposalStatus(ctx context.Context, id string, status string, action string) (domain.StewardAutonomyProposal, error) {
	lease, err := acquireAutonomyExecutionLease(ctx, s.db.Pool, id)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	defer lease.Release()
	return s.updateProposalStatusLocked(ctx, id, status, action)
}

func (s *Service) updateProposalStatusLocked(ctx context.Context, id string, status string, action string) (domain.StewardAutonomyProposal, error) {
	current, err := s.getAutonomyProposal(ctx, id)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	if err := validateProposalTransition(current, status); err != nil {
		_, _ = s.recordAudit(ctx, AuditInput{
			Actor:           "user",
			Action:          action,
			TargetType:      "autonomy_proposal",
			Source:          "manual",
			PermissionLevel: PermissionA3,
			DataLevel:       DataD2,
			InputSummary:    id,
			OutputSummary:   status,
			ResultStatus:    ResultBlocked,
			ErrorSummary:    stringPtr(err.Error()),
		})
		return current, err
	}
	if current.Status == strings.TrimSpace(status) {
		return current, nil
	}
	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_proposals
		set status = $1, updated_at = $2
		where id = $3
	`, status, now, id)
	if err != nil {
		return domain.StewardAutonomyProposal{}, fmt.Errorf("update autonomy proposal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.StewardAutonomyProposal{}, fmt.Errorf("autonomy proposal not found")
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          action,
		TargetType:      "autonomy_proposal",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   status,
		ResultStatus:    ResultOK,
	})
	return s.getAutonomyProposal(ctx, id)
}

func (s *Service) createApprovalRequest(ctx context.Context, proposalID *string, requestedAction string, riskSummary string, planSummary string) (domain.StewardApprovalRequest, error) {
	now := time.Now().UTC()
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_approval_requests (
			id, proposal_id, requested_action, risk_summary, plan_summary, status, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7)
		on conflict (proposal_id, requested_action) where status = 'pending' and proposal_id is not null
		do update set
			risk_summary = excluded.risk_summary,
			plan_summary = excluded.plan_summary
		returning id, coalesce(proposal_id::text, ''), requested_action, risk_summary, plan_summary, status,
		          coalesce(decided_by, ''), coalesce(decision_reason, ''), created_at, decided_at
	`, uuid.NewString(), proposalID, requestedAction, riskSummary, planSummary, ApprovalPending, now)
	item, err := scanApprovalRequest(row)
	if err != nil {
		return domain.StewardApprovalRequest{}, fmt.Errorf("create approval request: %w", err)
	}
	return item, nil
}

func (s *Service) decideApproval(ctx context.Context, id string, status string, reason string) (domain.StewardApprovalRequest, error) {
	current, err := s.getApprovalRequest(ctx, id)
	if err != nil {
		return domain.StewardApprovalRequest{}, err
	}
	var proposalTransition string
	var lease *autonomyExecutionLease
	if current.ProposalID != nil {
		lease, err = acquireAutonomyExecutionLease(ctx, s.db.Pool, *current.ProposalID)
		if err != nil {
			return domain.StewardApprovalRequest{}, err
		}
		defer lease.Release()
		proposal, err := s.getAutonomyProposal(ctx, *current.ProposalID)
		if err != nil {
			return domain.StewardApprovalRequest{}, err
		}
		var shouldTransition bool
		proposalTransition, shouldTransition, err = approvalProposalTransition(current, proposal, status)
		if err != nil {
			return domain.StewardApprovalRequest{}, err
		}
		if !shouldTransition {
			proposalTransition = ""
		}
	}

	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_approval_requests
		set status = $1, decided_by = 'user', decision_reason = $2, decided_at = $3
		where id = $4 and status = $5
	`, status, strings.TrimSpace(reason), now, id, ApprovalPending)
	if err != nil {
		return domain.StewardApprovalRequest{}, fmt.Errorf("decide approval request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.StewardApprovalRequest{}, fmt.Errorf("approval request not found")
	}
	if proposalTransition != "" && current.ProposalID != nil {
		action := "autonomy.approval.proposal_" + status
		if _, err := s.updateProposalStatusLocked(ctx, *current.ProposalID, proposalTransition, action); err != nil {
			return domain.StewardApprovalRequest{}, err
		}
	}
	return s.getApprovalRequest(ctx, id)
}

func (s *Service) recordAutonomousRun(ctx context.Context, proposalID *string, ruleID *string, mode string, status string, triggerReason string, impactSummary string, recoveryHint string) (domain.StewardAutonomousRun, error) {
	var errorSummary *string
	if status == RunFailed {
		summary := strings.TrimSpace(recoveryHint)
		if summary == "" {
			summary = strings.TrimSpace(impactSummary)
		}
		errorSummary = stringPtr(summary)
	}
	auditID, _ := s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.run." + status,
		TargetType:      "autonomous_run",
		Source:          mode,
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    triggerReason,
		OutputSummary:   impactSummary,
		ResultStatus:    mapRunStatusToAudit(status),
		ErrorSummary:    errorSummary,
	})
	now := time.Now().UTC()
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_autonomous_runs (
			id, proposal_id, rule_id, mode, status, trigger_reason, impact_summary,
			recovery_hint, audit_id, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		returning id, coalesce(proposal_id::text, ''), coalesce(rule_id::text, ''), mode, status, trigger_reason, impact_summary,
		          recovery_hint, coalesce(audit_id::text, ''), created_at
	`, uuid.NewString(), proposalID, ruleID, mode, status, triggerReason, impactSummary,
		recoveryHint, auditID, now)
	run, err := scanAutonomousRun(row)
	if err != nil {
		return domain.StewardAutonomousRun{}, fmt.Errorf("record autonomous run: %w", err)
	}
	return run, nil
}

func (s *Service) getAutonomyRule(ctx context.Context, id string) (domain.StewardAutonomyRule, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, name, trigger_type, target_type, action, policy, risk_level,
		       max_permission_level, enabled, scope_summary, created_at, updated_at
		from steward_autonomy_rules
		where id = $1
	`, id)
	return scanAutonomyRule(row)
}

func (s *Service) getAutonomyRuleByName(ctx context.Context, name string) (domain.StewardAutonomyRule, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, name, trigger_type, target_type, action, policy, risk_level,
		       max_permission_level, enabled, scope_summary, created_at, updated_at
		from steward_autonomy_rules
		where name = $1
	`, name)
	return scanAutonomyRule(row)
}

func (s *Service) getAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomyProposal, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, coalesce(rule_id::text, ''), source_entity_type, coalesce(source_entity_id::text, ''), action, title, summary,
		       trigger_reason, suggested_action, risk_level, permission_level, data_level, status,
		       policy, impact_summary, score, score_reason, coalesce(created_task_id::text, ''), execution_target_type,
		       execution_target_id, coalesce(audit_id::text, ''), created_at, updated_at
		from steward_autonomy_proposals
		where id = $1
	`, id)
	return scanAutonomyProposal(row)
}

func (s *Service) getApprovalRequest(ctx context.Context, id string) (domain.StewardApprovalRequest, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, coalesce(proposal_id::text, ''), requested_action, risk_summary, plan_summary, status,
		       coalesce(decided_by, ''), coalesce(decision_reason, ''), created_at, decided_at
		from steward_approval_requests
		where id = $1
	`, id)
	return scanApprovalRequest(row)
}

func scanAutonomyRule(row scanner) (domain.StewardAutonomyRule, error) {
	var rule domain.StewardAutonomyRule
	err := row.Scan(&rule.ID, &rule.Name, &rule.TriggerType, &rule.TargetType, &rule.Action,
		&rule.Policy, &rule.RiskLevel, &rule.MaxPermissionLevel, &rule.Enabled,
		&rule.ScopeSummary, &rule.CreatedAt, &rule.UpdatedAt)
	return rule, err
}

func scanAutonomyProposal(row scanner) (domain.StewardAutonomyProposal, error) {
	var proposal domain.StewardAutonomyProposal
	var ruleID string
	var sourceEntityID string
	var createdTaskID string
	var executionTargetType string
	var executionTargetID string
	var auditID string
	err := row.Scan(&proposal.ID, &ruleID, &proposal.SourceEntityType, &sourceEntityID, &proposal.Action,
		&proposal.Title, &proposal.Summary, &proposal.TriggerReason, &proposal.SuggestedAction,
		&proposal.RiskLevel, &proposal.PermissionLevel, &proposal.DataLevel, &proposal.Status,
		&proposal.Policy, &proposal.ImpactSummary, &proposal.Score, &proposal.ScoreReason, &createdTaskID,
		&executionTargetType, &executionTargetID, &auditID,
		&proposal.CreatedAt, &proposal.UpdatedAt)
	proposal.RuleID = stringPtrIfPresent(ruleID)
	proposal.SourceEntityID = stringPtrIfPresent(sourceEntityID)
	proposal.CreatedTaskID = stringPtrIfPresent(createdTaskID)
	proposal.ExecutionTargetType = executionTargetType
	proposal.ExecutionTargetID = executionTargetID
	proposal.AuditID = stringPtrIfPresent(auditID)
	return proposal, err
}

func scanApprovalRequest(row scanner) (domain.StewardApprovalRequest, error) {
	var item domain.StewardApprovalRequest
	var proposalID string
	err := row.Scan(&item.ID, &proposalID, &item.RequestedAction, &item.RiskSummary,
		&item.PlanSummary, &item.Status, &item.DecidedBy, &item.DecisionReason,
		&item.CreatedAt, &item.DecidedAt)
	item.ProposalID = stringPtrIfPresent(proposalID)
	return item, err
}

func scanAutonomousRun(row scanner) (domain.StewardAutonomousRun, error) {
	var run domain.StewardAutonomousRun
	var proposalID string
	var ruleID string
	var auditID string
	err := row.Scan(&run.ID, &proposalID, &ruleID, &run.Mode, &run.Status,
		&run.TriggerReason, &run.ImpactSummary, &run.RecoveryHint, &auditID, &run.CreatedAt)
	run.ProposalID = stringPtrIfPresent(proposalID)
	run.RuleID = stringPtrIfPresent(ruleID)
	run.AuditID = stringPtrIfPresent(auditID)
	return run, err
}

func normalizeAutonomyMode(value string) string {
	switch strings.TrimSpace(value) {
	case AutonomyModeControlled:
		return AutonomyModeControlled
	default:
		return AutonomyModeSuggestOnly
	}
}

func normalizeAutonomyPolicy(value string) string {
	switch strings.TrimSpace(value) {
	case AutonomyPolicyAuto, AutonomyPolicyConfirm, AutonomyPolicyNever:
		return strings.TrimSpace(value)
	default:
		return AutonomyPolicySuggest
	}
}

func normalizeBulkDismissProposalStatus(value string) (string, error) {
	status := strings.TrimSpace(value)
	if status == "" {
		status = ProposalCandidate
	}
	switch status {
	case ProposalCandidate, ProposalApproved, ProposalBlocked:
		return status, nil
	case ProposalDismissed, ProposalExecuted:
		return "", fmt.Errorf("bulk dismiss does not accept closed proposal status %q", status)
	default:
		return "", fmt.Errorf("unsupported bulk dismiss proposal status %q", status)
	}
}

func normalizeBulkDismissLimit(value int) int {
	if value <= 0 {
		return 50
	}
	if value > 200 {
		return 200
	}
	return value
}

func validateProposalTransition(proposal domain.StewardAutonomyProposal, targetStatus string) error {
	current := strings.TrimSpace(proposal.Status)
	target := strings.TrimSpace(targetStatus)
	if target == "" {
		return fmt.Errorf("target proposal status is required")
	}
	if current == target {
		return nil
	}
	switch current {
	case ProposalDismissed, ProposalExecuted:
		return fmt.Errorf("closed autonomy proposal status %q cannot transition to %q", current, target)
	}
	switch target {
	case ProposalApproved:
		if current != ProposalCandidate {
			return fmt.Errorf("only candidate autonomy proposals can be approved, got %q", current)
		}
		if proposalRequiresManualReview(proposal) {
			return fmt.Errorf("high-risk or denied-policy proposals cannot be approved for autonomous execution")
		}
		return nil
	case ProposalDismissed:
		switch current {
		case ProposalCandidate, ProposalApproved, ProposalBlocked:
			return nil
		default:
			return fmt.Errorf("cannot dismiss autonomy proposal from status %q", current)
		}
	case ProposalBlocked:
		switch current {
		case ProposalCandidate, ProposalApproved:
			return nil
		default:
			return fmt.Errorf("cannot block autonomy proposal from status %q", current)
		}
	case ProposalCandidate:
		return fmt.Errorf("autonomy proposal status cannot be reset to candidate")
	case ProposalExecuted:
		return fmt.Errorf("autonomy proposals must be executed through ExecuteAutonomyProposal")
	default:
		return fmt.Errorf("unsupported autonomy proposal status %q", target)
	}
}

func approvalProposalTransition(approval domain.StewardApprovalRequest, proposal domain.StewardAutonomyProposal, decisionStatus string) (string, bool, error) {
	if strings.TrimSpace(approval.RequestedAction) != "approve autonomous execution" {
		return "", false, nil
	}
	var target string
	switch strings.TrimSpace(decisionStatus) {
	case ApprovalApproved:
		target = ProposalApproved
	case ApprovalRejected:
		target = ProposalDismissed
	default:
		return "", false, fmt.Errorf("unsupported approval decision status %q", decisionStatus)
	}
	if err := validateProposalTransition(proposal, target); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func proposalRequiresManualReview(proposal domain.StewardAutonomyProposal) bool {
	return proposal.Policy == AutonomyPolicyNever || isHighRisk(proposal.RiskLevel, proposal.PermissionLevel)
}

func isHighRisk(risk string, permission string) bool {
	switch strings.TrimSpace(risk) {
	case "high", "critical":
		return true
	}
	return permissionRank(permission) >= permissionRank(PermissionA4)
}

func permissionRank(permission string) int {
	switch strings.ToUpper(strings.TrimSpace(permission)) {
	case "A0":
		return 0
	case "A1":
		return 1
	case "A2":
		return 2
	case "A3":
		return 3
	case "A4":
		return 4
	case "A5":
		return 5
	case "A6":
		return 6
	case "A7":
		return 7
	case "A8":
		return 8
	case "A9":
		return 9
	default:
		return 9
	}
}

func mapRunStatusToAudit(status string) string {
	switch status {
	case RunBlocked:
		return ResultBlocked
	case RunFailed:
		return ResultFailed
	default:
		return ResultOK
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
