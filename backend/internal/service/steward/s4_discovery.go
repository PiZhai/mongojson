package steward

import (
	"context"
	"fmt"
	"time"
)

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
