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
		       coalesce(decided_by, ''), coalesce(decision_reason, ''), created_at, decided_at,
		       approval_proof_id, approval_key_id, approval_proof_expires_at
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
		if err := s.populateAutonomyApprovalExpectation(ctx, &item); err != nil {
			return nil, err
		}
		approvals = append(approvals, item)
	}
	return approvals, rows.Err()
}

func (s *Service) ApproveRequest(ctx context.Context, id string, input DecideApprovalInput) (domain.StewardApprovalRequest, error) {
	return s.decideApproval(ctx, id, ApprovalApproved, input)
}

func (s *Service) RejectRequest(ctx context.Context, id string, input DecideApprovalInput) (domain.StewardApprovalRequest, error) {
	return s.decideApproval(ctx, id, ApprovalRejected, input)
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
		          coalesce(decided_by, ''), coalesce(decision_reason, ''), created_at, decided_at,
		          approval_proof_id, approval_key_id, approval_proof_expires_at
	`, uuid.NewString(), proposalID, requestedAction, riskSummary, planSummary, ApprovalPending, now)
	item, err := scanApprovalRequest(row)
	if err != nil {
		return domain.StewardApprovalRequest{}, fmt.Errorf("create approval request: %w", err)
	}
	if err := s.populateAutonomyApprovalExpectation(ctx, &item); err != nil {
		return domain.StewardApprovalRequest{}, err
	}
	return item, nil
}

func (s *Service) decideApproval(ctx context.Context, id string, status string, input DecideApprovalInput) (domain.StewardApprovalRequest, error) {
	reason := strings.TrimSpace(input.DecisionReason)
	current, err := s.getApprovalRequest(ctx, id)
	if err != nil {
		return domain.StewardApprovalRequest{}, err
	}
	var proposalTransition string
	var lease *autonomyExecutionLease
	var proofMetadata runtimeApprovalProofMetadata
	decidedBy := "user"
	if current.ProposalID != nil {
		lease, err = acquireAutonomyExecutionLease(ctx, s.db.Pool, *current.ProposalID)
		if err != nil {
			return domain.StewardApprovalRequest{}, err
		}
		defer lease.Release()
		gatedCtx, policyGate, gateErr := acquireAutonomyPolicyReadGate(ctx, s.db.Pool)
		if gateErr != nil {
			return domain.StewardApprovalRequest{}, gateErr
		}
		defer policyGate.Release()
		ctx = gatedCtx
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
		if proposalTransition == ProposalApproved {
			if _, issue, policyErr := s.currentRuleExecutionPolicy(ctx, proposal); policyErr != nil {
				return domain.StewardApprovalRequest{}, policyErr
			} else if issue != "" {
				s.recordAutonomyApprovalPolicyDenied(ctx, "approval_request", current.ID, issue)
				return domain.StewardApprovalRequest{}, fmt.Errorf("current autonomy rule blocks approval: %s", issue)
			}
			permissionPolicy, permissionErr := s.ResolvePermissionPolicy(ctx, proposal.PermissionLevel, proposal.Action)
			if permissionErr != nil || permissionPolicy.ExecutionMode == PolicyModeDeny {
				issue := "permission policy denies approval"
				if permissionErr != nil {
					issue = permissionErr.Error()
				}
				s.recordAutonomyApprovalPolicyDenied(ctx, "approval_request", current.ID, issue)
				return domain.StewardApprovalRequest{}, fmt.Errorf("permission policy blocks approval: %s", issue)
			}
			proofMetadata, err = s.validateAutonomyApprovalProof(ctx, proposal, reason, input.ApprovalProof)
			if err != nil {
				return domain.StewardApprovalRequest{}, err
			}
			if proofMetadata.Required {
				decidedBy = input.ApprovalProof.Claims.GrantedBy
			}
		}
	}

	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_approval_requests
		set status = $1, decided_by = $2, decision_reason = $3, decided_at = $4,
		    approval_proof = $5::jsonb, approval_proof_id = $6, approval_key_id = $7, approval_proof_expires_at = $8
		where id = $9 and status = $10
	`, status, decidedBy, reason, now, string(defaultJSON(proofMetadata.JSON)), proofMetadata.ProofID,
		proofMetadata.KeyID, nullableApprovalProofExpiry(proofMetadata), id, ApprovalPending)
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

func (s *Service) recordAutonomyApprovalPolicyDenied(ctx context.Context, targetType string, targetID string, issue string) {
	confirmed := true
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "autonomy.approval.current_rule_denied",
		TargetType:      targetType,
		TargetID:        stringPtr(targetID),
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    "approval rejected by current rule",
		OutputSummary:   "approval was not applied",
		Reason:          issue,
		UserConfirmed:   &confirmed,
		Syncable:        &syncable,
		ResultStatus:    ResultBlocked,
		ErrorSummary:    stringPtr(issue),
	})
}
