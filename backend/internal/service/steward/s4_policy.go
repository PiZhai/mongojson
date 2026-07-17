package steward

import (
	"context"
	"fmt"
	"strings"

	"mongojson/backend/internal/domain"
)

func autonomyModeValue(value string, fallback string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		mode = fallback
	}
	switch mode {
	case AutonomyModeSuggestOnly, AutonomyModeControlled:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported autonomy mode %q", value)
	}
}

func autonomyPolicyValue(value string, fallback string) (string, error) {
	policy := strings.ToLower(strings.TrimSpace(value))
	if policy == "" {
		policy = fallback
	}
	switch policy {
	case AutonomyPolicyAuto, AutonomyPolicyConfirm, AutonomyPolicyNever:
		return policy, nil
	case AutonomyPolicySuggest:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported autonomy policy %q", value)
	}
}

func autonomyRiskValue(value string, fallback string) (string, error) {
	risk := strings.ToLower(strings.TrimSpace(value))
	if risk == "" {
		risk = fallback
	}
	switch risk {
	case "low", "medium", "high", "critical":
		return risk, nil
	default:
		return "", fmt.Errorf("unsupported autonomy risk level %q", value)
	}
}

func autonomyPermissionValue(value string, fallback string) (string, error) {
	permission := strings.ToUpper(strings.TrimSpace(value))
	if permission == "" {
		permission = fallback
	}
	switch permission {
	case PermissionA0, PermissionA1, PermissionA2, PermissionA3, PermissionA4,
		PermissionA5, PermissionA6, PermissionA7, PermissionA8, PermissionA9:
		return permission, nil
	default:
		return "", fmt.Errorf("unsupported autonomy permission level %q", value)
	}
}

func autonomyAutoPermissionValue(value string, fallback string) (string, error) {
	return autonomyPermissionValue(value, fallback)
}

func autonomyDataLevelValue(value string, fallback string) (string, error) {
	level := strings.ToUpper(strings.TrimSpace(value))
	if level == "" {
		level = fallback
	}
	switch level {
	case DataD0, DataD1, DataD2, DataD3, DataD4, DataD5, DataD6:
		return level, nil
	default:
		return "", fmt.Errorf("unsupported autonomy data level %q", value)
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
		if issue := autonomyProposalPolicyIssue(proposal); issue != "" {
			return fmt.Errorf("autonomy proposal policy contract is invalid: %s", issue)
		}
		if proposal.Policy == AutonomyPolicyNever {
			return fmt.Errorf("denied-policy proposals cannot be approved for autonomous execution")
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
	return autonomyProposalPolicyIssue(proposal) != "" || proposal.Policy == AutonomyPolicyNever
}

func autonomyProposalPolicyIssue(proposal domain.StewardAutonomyProposal) string {
	if _, err := autonomyPolicyValue(proposal.Policy, ""); err != nil {
		return err.Error()
	}
	if _, err := autonomyRiskValue(proposal.RiskLevel, ""); err != nil {
		return err.Error()
	}
	if _, err := autonomyPermissionValue(proposal.PermissionLevel, ""); err != nil {
		return err.Error()
	}
	if _, err := autonomyDataLevelValue(proposal.DataLevel, ""); err != nil {
		return err.Error()
	}
	return ""
}

func evaluateCurrentRuleExecutionPolicy(proposal domain.StewardAutonomyProposal, rule domain.StewardAutonomyRule) (bool, string) {
	if ownerModeEnabled() {
		return true, ""
	}
	if proposal.RuleID == nil {
		return proposal.Policy == AutonomyPolicyAuto, ""
	}
	if strings.TrimSpace(rule.ID) == "" || rule.ID != strings.TrimSpace(*proposal.RuleID) {
		return false, "proposal rule is missing or does not match"
	}
	if !rule.Enabled {
		return false, "proposal rule is disabled"
	}
	if _, err := autonomyPolicyValue(rule.Policy, ""); err != nil {
		return false, "current rule policy is invalid: " + err.Error()
	}
	if _, err := autonomyRiskValue(rule.RiskLevel, ""); err != nil {
		return false, "current rule risk is invalid: " + err.Error()
	}
	if _, err := autonomyPermissionValue(rule.MaxPermissionLevel, ""); err != nil {
		return false, "current rule permission is invalid: " + err.Error()
	}
	if strings.TrimSpace(rule.Action) != strings.TrimSpace(proposal.Action) {
		return false, "proposal action no longer matches its rule"
	}
	if rule.Policy == AutonomyPolicyNever {
		return false, "current rule policy is never"
	}
	if permissionRank(proposal.PermissionLevel) > permissionRank(rule.MaxPermissionLevel) {
		return false, "proposal permission exceeds the current rule ceiling"
	}
	return proposal.Policy == AutonomyPolicyAuto && rule.Policy == AutonomyPolicyAuto, ""
}

func (s *Service) currentRuleExecutionPolicy(ctx context.Context, proposal domain.StewardAutonomyProposal) (bool, string, error) {
	if proposal.RuleID == nil {
		return proposal.Policy == AutonomyPolicyAuto, "", nil
	}
	rule, err := s.getAutonomyRule(ctx, *proposal.RuleID)
	if err != nil {
		return false, "", err
	}
	automatic, issue := evaluateCurrentRuleExecutionPolicy(proposal, rule)
	return automatic, issue, nil
}

func isHighRisk(risk string, permission string) bool {
	if ownerModeEnabled() {
		return false
	}
	// Only explicitly classified low-risk work can enter the autonomous executor.
	// Unknown or future risk labels fail closed.
	if !strings.EqualFold(strings.TrimSpace(risk), "low") {
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
