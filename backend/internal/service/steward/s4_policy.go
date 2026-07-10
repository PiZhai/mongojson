package steward

import (
	"fmt"
	"strings"

	"mongojson/backend/internal/domain"
)

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
