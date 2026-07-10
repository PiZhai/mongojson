package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

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
