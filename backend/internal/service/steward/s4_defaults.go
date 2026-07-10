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
