package steward

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func (s *Service) ensureS4Defaults(ctx context.Context, now time.Time) error {
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_autonomy_settings (id, paused, mode, max_auto_permission, updated_at)
		values ($1,false,$2,$3,$4)
		on conflict (id) do nothing
	`, AutonomySettingsID, AutonomyModeSuggestOnly, PermissionA3, now); err != nil {
		return fmt.Errorf("ensure autonomy settings: %w", err)
	}
	if ownerModeEnabled() {
		if _, err := s.db.Pool.Exec(ctx, `
			delete from steward_approval_requests;
			delete from steward_autonomous_runs;
			delete from steward_autonomy_proposals;
			delete from steward_autonomy_rules;
			delete from steward_tool_definitions;
		`); err != nil {
			return fmt.Errorf("remove legacy permission-gated autonomy state in owner mode: %w", err)
		}
		if _, err := s.db.Pool.Exec(ctx, `
			update steward_autonomy_settings
			set paused=false, mode=$1, max_auto_permission=$2, updated_at=$3
			where id=$4
		`, AutonomyModeControlled, "", now, AutonomySettingsID); err != nil {
			return fmt.Errorf("enable unrestricted owner autonomy: %w", err)
		}
		return nil
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
			Enabled:            false,
			ScopeSummary:       "R4.8 已停用：由模型主动决策替代固定事件跟进规则",
		},
		{
			Name:               "stale-open-task-review",
			TriggerType:        "task.stale",
			TargetType:         "task",
			Action:             "create_review_checklist",
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            false,
			ScopeSummary:       "R4.8 已停用：由模型结合完整上下文决定是否复盘",
		},
		{
			Name:               "event-knowledge-summary",
			TriggerType:        "event.created",
			TargetType:         "knowledge_item",
			Action:             AutonomyActionCreateKnowledgeSummary,
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            false,
			ScopeSummary:       "R4.8 已停用：由每日和每周模型归纳替代",
		},
		{
			Name:               "due-task-reminder",
			TriggerType:        "task.due",
			TargetType:         "task",
			Action:             AutonomyActionCreateReminderTask,
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            false,
			ScopeSummary:       "R4.8 已停用：由模型判断提醒价值和时机",
		},
		{
			Name:               "sync-conflict-diagnostics",
			TriggerType:        "sync.conflict",
			TargetType:         "knowledge_item",
			Action:             AutonomyActionRunReadOnlyDiagnostics,
			Policy:             AutonomyPolicySuggest,
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
			Enabled:            false,
			ScopeSummary:       "R4.8 已停用：由模型按需调用诊断工具",
		},
		{
			Name:               "high-risk-guardrail",
			TriggerType:        "risk.detected",
			TargetType:         "plan",
			Action:             "block_high_risk_execution",
			Policy:             AutonomyPolicyNever,
			RiskLevel:          "high",
			MaxPermissionLevel: PermissionA4,
			Enabled:            false,
			ScopeSummary:       "R4.8 已停用规则；高风险阻断由 Runtime/Broker 安全层强制执行",
		},
	}
	for _, rule := range defaults {
		if _, err := s.db.Pool.Exec(ctx, `
			insert into steward_autonomy_rules (
				id, name, trigger_type, target_type, action, policy, risk_level,
				max_permission_level, enabled, scope_summary, created_at, updated_at
			)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
			on conflict (name) do update set enabled=false,scope_summary=excluded.scope_summary,updated_at=excluded.updated_at
		`, uuid.NewString(), rule.Name, rule.TriggerType, rule.TargetType, rule.Action, rule.Policy,
			rule.RiskLevel, rule.MaxPermissionLevel, rule.Enabled, rule.ScopeSummary, now); err != nil {
			return fmt.Errorf("ensure autonomy rule: %w", err)
		}
	}
	return nil
}
