package steward

import "mongojson/backend/internal/privilegebroker"

const (
	AutonomySettingsID = "default"

	AutonomyModeSuggestOnly = "suggest_only"
	AutonomyModeControlled  = "controlled"

	AutonomyPolicySuggest = "suggest"
	AutonomyPolicyConfirm = "confirm"
	AutonomyPolicyAuto    = "auto"
	AutonomyPolicyNever   = "never"

	ProposalCandidate = "candidate"
	ProposalApproved  = "approved"
	ProposalDismissed = "dismissed"
	ProposalExecuted  = "executed"
	ProposalBlocked   = "blocked"

	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"

	RunModeSimulate = "simulate"
	RunModeExecute  = "execute"
	RunSuccess      = "success"
	RunBlocked      = "blocked"
	RunFailed       = "failed"
)

type UpdateAutonomySettingsInput struct {
	Paused            *bool  `json:"paused"`
	Mode              string `json:"mode"`
	MaxAutoPermission string `json:"max_auto_permission"`
}

type UpdateAutonomyRuleInput struct {
	Policy             *string `json:"policy"`
	Enabled            *bool   `json:"enabled"`
	MaxPermissionLevel *string `json:"max_permission_level"`
	ScopeSummary       *string `json:"scope_summary"`
}

type CreateAutonomyProposalInput struct {
	RuleID           *string `json:"rule_id"`
	SourceEntityType string  `json:"source_entity_type"`
	SourceEntityID   *string `json:"source_entity_id"`
	Action           string  `json:"action"`
	Title            string  `json:"title"`
	Summary          string  `json:"summary"`
	TriggerReason    string  `json:"trigger_reason"`
	SuggestedAction  string  `json:"suggested_action"`
	RiskLevel        string  `json:"risk_level"`
	PermissionLevel  string  `json:"permission_level"`
	DataLevel        string  `json:"data_level"`
	Policy           string  `json:"policy"`
	ImpactSummary    string  `json:"impact_summary"`
}

type DismissAutonomyProposalsInput struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Reason string `json:"reason"`
}

type DismissAutonomyProposalsResult struct {
	Dismissed int      `json:"dismissed"`
	Status    string   `json:"status"`
	IDs       []string `json:"ids"`
}

type DecideApprovalInput struct {
	DecisionReason string                               `json:"decision_reason"`
	ApprovalProof  *privilegebroker.SignedApprovalProof `json:"approval_proof,omitempty"`
}
