package steward

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

type runtimeApprovalProofMetadata struct {
	Required  bool
	JSON      []byte
	ProofID   string
	KeyID     string
	ExpiresAt time.Time
}

func (s *Service) validateRuntimeApprovalProof(ctx context.Context, runID, planHash, grantedBy, reason string, proof *privilegebroker.SignedApprovalProof) (runtimeApprovalProofMetadata, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select arguments from steward_run_steps
		where run_id = $1 and tool_name = 'privilege.execute'
		order by position
	`, runID)
	if err != nil {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("inspect privileged approval scope: %w", err)
	}
	defer rows.Close()
	capabilities := []string{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return runtimeApprovalProofMetadata{}, err
		}
		var arguments map[string]any
		if err := json.Unmarshal(payload, &arguments); err != nil {
			return runtimeApprovalProofMetadata{}, fmt.Errorf("decode privileged step arguments: %w", err)
		}
		capability, _ := arguments["capability"].(string)
		capability = strings.ToLower(strings.TrimSpace(capability))
		if !configuredToolActionPattern.MatchString(capability) {
			return runtimeApprovalProofMetadata{}, fmt.Errorf("privileged step has an invalid capability")
		}
		capabilities = append(capabilities, capability)
	}
	if err := rows.Err(); err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	if len(capabilities) == 0 {
		if proof != nil {
			return runtimeApprovalProofMetadata{}, fmt.Errorf("signed approval proof is only valid for privilege.execute runs")
		}
		return runtimeApprovalProofMetadata{}, nil
	}
	if len(capabilities) != 1 {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("R3.1 requires one signed approval proof per run; split multiple privileged steps into separate runs")
	}
	if proof == nil {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("R3.1 privileged execution requires a signed approval proof from an independent authority")
	}
	if proof.Claims.GrantedBy != strings.TrimSpace(grantedBy) {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("approval proof granted_by does not match the approval request")
	}
	stopped, generation, err := s.runtimeExecutionState(ctx)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	if stopped {
		return runtimeApprovalProofMetadata{}, ErrExecutionEmergencyStopped
	}
	if s.privilegeBroker == nil || s.privilegeBrokerError != nil {
		if s.privilegeBrokerError != nil {
			return runtimeApprovalProofMetadata{}, s.privilegeBrokerError
		}
		return runtimeApprovalProofMetadata{}, fmt.Errorf("privilege broker client is not configured")
	}
	statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := s.privilegeBroker.Status(statusCtx)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	if status.Stopped || status.Generation != generation {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("privilege broker control state is not synchronized")
	}
	if err := privilegebroker.VerifyApprovalProof(status.ApprovalAuthorities, *proof, privilegebroker.ApprovalProofExpectation{
		Subject: "runtime:" + runID, PlanHash: planHash, Capability: capabilities[0],
		ControlGeneration: generation, Reason: reason,
	}, time.Now().UTC()); err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	payload, err := json.Marshal(proof)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	return runtimeApprovalProofMetadata{
		Required: true, JSON: payload, ProofID: proof.Claims.ProofID, KeyID: proof.KeyID, ExpiresAt: proof.Claims.ExpiresAt,
	}, nil
}

func (s *Service) validateAutonomyApprovalProof(ctx context.Context, proposal domain.StewardAutonomyProposal, reason string, proof *privilegebroker.SignedApprovalProof) (runtimeApprovalProofMetadata, error) {
	tool, found, err := s.findToolDefinition(ctx, proposal.Action)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	if !found || !tool.Enabled || permissionRank(tool.PermissionLevel) < permissionRank(PermissionA4) {
		if proof != nil {
			return runtimeApprovalProofMetadata{}, fmt.Errorf("signed approval proof is only valid for configured Broker capabilities")
		}
		return runtimeApprovalProofMetadata{}, nil
	}
	if proof == nil {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("R3.1 configured capability approval requires an independently signed proof")
	}
	stopped, generation, err := s.runtimeExecutionState(ctx)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	if stopped {
		return runtimeApprovalProofMetadata{}, ErrExecutionEmergencyStopped
	}
	if s.privilegeBroker == nil || s.privilegeBrokerError != nil {
		if s.privilegeBrokerError != nil {
			return runtimeApprovalProofMetadata{}, s.privilegeBrokerError
		}
		return runtimeApprovalProofMetadata{}, fmt.Errorf("privilege broker client is not configured")
	}
	statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := s.privilegeBroker.Status(statusCtx)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	if status.Stopped || status.Generation != generation {
		return runtimeApprovalProofMetadata{}, fmt.Errorf("privilege broker control state is not synchronized")
	}
	if err := privilegebroker.VerifyApprovalProof(status.ApprovalAuthorities, *proof, privilegebroker.ApprovalProofExpectation{
		Subject: "s4:" + proposal.ID, PlanHash: autonomyBrokerPlanHash(proposal), Capability: proposal.Action,
		ControlGeneration: generation, Reason: reason,
	}, time.Now().UTC()); err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	payload, err := json.Marshal(proof)
	if err != nil {
		return runtimeApprovalProofMetadata{}, err
	}
	return runtimeApprovalProofMetadata{
		Required: true, JSON: payload, ProofID: proof.Claims.ProofID, KeyID: proof.KeyID, ExpiresAt: proof.Claims.ExpiresAt,
	}, nil
}

func (s *Service) approvedAutonomyApprovalProof(ctx context.Context, proposalID string) (privilegebroker.SignedApprovalProof, error) {
	var payload []byte
	err := s.db.Pool.QueryRow(ctx, `
		select approval_proof from steward_approval_requests
		where proposal_id = $1 and status = 'approved' and approval_proof_id <> ''
		  and approval_proof_expires_at > $2
		order by decided_at desc limit 1
	`, proposalID, time.Now().UTC()).Scan(&payload)
	if err != nil {
		return privilegebroker.SignedApprovalProof{}, fmt.Errorf("active signed autonomy approval proof is unavailable: %w", err)
	}
	var proof privilegebroker.SignedApprovalProof
	if err := json.Unmarshal(payload, &proof); err != nil || proof.Claims.ProofID == "" {
		return privilegebroker.SignedApprovalProof{}, fmt.Errorf("decode signed autonomy approval proof")
	}
	return proof, nil
}

func (s *Service) populateAutonomyApprovalExpectation(ctx context.Context, approval *domain.StewardApprovalRequest) error {
	if approval == nil || approval.ProposalID == nil {
		return nil
	}
	proposal, err := s.getAutonomyProposal(ctx, *approval.ProposalID)
	if err != nil {
		return err
	}
	tool, found, err := s.findToolDefinition(ctx, proposal.Action)
	if err != nil {
		return err
	}
	if !found || !tool.Enabled || permissionRank(tool.PermissionLevel) < permissionRank(PermissionA4) {
		return nil
	}
	_, generation, err := s.runtimeExecutionState(ctx)
	if err != nil {
		return err
	}
	approval.ApprovalProofRequired = true
	approval.ApprovalProofExpectation = &domain.StewardApprovalProofExpectation{
		Subject: "s4:" + proposal.ID, PlanHash: autonomyBrokerPlanHash(proposal), Capability: proposal.Action,
		ControlGeneration: generation,
	}
	return nil
}
