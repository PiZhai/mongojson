package steward

import (
	"context"

	"mongojson/backend/internal/domain"
)

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
	proposal, err := scanAutonomyProposal(row)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	if err := s.populateAutonomyRetryState(ctx, &proposal); err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	return proposal, nil
}

func (s *Service) getApprovalRequest(ctx context.Context, id string) (domain.StewardApprovalRequest, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, coalesce(proposal_id::text, ''), requested_action, risk_summary, plan_summary, status,
		       coalesce(decided_by, ''), coalesce(decision_reason, ''), created_at, decided_at,
		       approval_proof_id, approval_key_id, approval_proof_expires_at
		from steward_approval_requests
		where id = $1
	`, id)
	item, err := scanApprovalRequest(row)
	if err != nil {
		return item, err
	}
	if err := s.populateAutonomyApprovalExpectation(ctx, &item); err != nil {
		return item, err
	}
	return item, nil
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
		&item.CreatedAt, &item.DecidedAt, &item.ApprovalProofID, &item.ApprovalKeyID, &item.ApprovalProofExpiresAt)
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
