package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func (s *Service) CreateAutonomyProposal(ctx context.Context, input CreateAutonomyProposalInput) (domain.StewardAutonomyProposal, error) {
	policy, err := autonomyPolicyValue(input.Policy, AutonomyPolicySuggest)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	risk, err := autonomyRiskValue(input.RiskLevel, "low")
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	permission, err := autonomyPermissionValue(input.PermissionLevel, PermissionA3)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	dataLevel, err := autonomyDataLevelValue(input.DataLevel, DataD0)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	now := time.Now().UTC()
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
		DataLevel:       dataLevel,
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
		permission, dataLevel, status, policy,
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
	proposals := []domain.StewardAutonomyProposal{}
	for rows.Next() {
		proposal, err := scanAutonomyProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := s.populateAutonomyRetryStates(ctx, proposals); err != nil {
		return nil, err
	}
	return proposals, nil
}

func (s *Service) ApproveAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomyProposal, error) {
	lease, err := acquireAutonomyExecutionLease(ctx, s.db.Pool, id)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	defer lease.Release()
	gatedCtx, policyGate, err := acquireAutonomyPolicyReadGate(ctx, s.db.Pool)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	defer policyGate.Release()
	proposal, err := s.getAutonomyProposal(gatedCtx, id)
	if err != nil {
		return domain.StewardAutonomyProposal{}, err
	}
	if _, issue, err := s.currentRuleExecutionPolicy(gatedCtx, proposal); err != nil {
		return domain.StewardAutonomyProposal{}, err
	} else if issue != "" {
		s.recordAutonomyApprovalPolicyDenied(gatedCtx, "autonomy_proposal", proposal.ID, issue)
		return domain.StewardAutonomyProposal{}, fmt.Errorf("current autonomy rule blocks approval: %s", issue)
	}
	return s.updateProposalStatusLocked(gatedCtx, id, ProposalApproved, "autonomy.proposal.approve")
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
