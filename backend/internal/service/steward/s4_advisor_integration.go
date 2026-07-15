package steward

import "context"

func (s *Service) enhanceAutonomyProposal(ctx context.Context, input CreateAutonomyProposalInput, advisorInput AutonomyAdvisorInput) CreateAutonomyProposalInput {
	advisor := s.autonomyAdvisor()
	if !advisor.Status().Enabled {
		return input
	}
	if s != nil && s.db != nil && s.db.Pool != nil {
		dataPolicy, err := s.ResolveDataPolicy(ctx, defaultString(advisorInput.DataLevel, DataD0), "autonomy:"+defaultString(advisorInput.SourceEntityType, "unknown"))
		if err != nil || dataPolicy.ModelMode != PolicyModeAuto {
			if err == nil {
				err = ErrDataPolicyDenied
			}
			s.recordAdvisorSuggestionFallback(ctx, advisorInput, err)
			return input
		}
		permissionPolicy, err := s.ResolvePermissionPolicy(ctx, PermissionA6, "model:autonomy-advisor")
		if err != nil || permissionPolicy.ExecutionMode != PolicyModeAuto {
			if err == nil {
				err = ErrDataPolicyDenied
			}
			s.recordAdvisorSuggestionFallback(ctx, advisorInput, err)
			return input
		}
	}
	suggestion, err := advisor.Suggest(ctx, advisorInput)
	if err != nil {
		s.recordAdvisorSuggestionFallback(ctx, advisorInput, err)
		return input
	}
	if violation := advisorSuggestionSafetyViolation(suggestion); violation != "" {
		s.recordAdvisorSuggestionBlocked(ctx, advisorInput, violation)
		return input
	}
	enhanced := applyAdvisorSuggestion(input, suggestion)
	enhanced.RuleID = input.RuleID
	enhanced.SourceEntityType = input.SourceEntityType
	enhanced.SourceEntityID = input.SourceEntityID
	enhanced.Action = input.Action
	enhanced.RiskLevel = input.RiskLevel
	enhanced.PermissionLevel = input.PermissionLevel
	enhanced.DataLevel = input.DataLevel
	enhanced.Policy = input.Policy
	return enhanced
}

func (s *Service) recordAdvisorSuggestionBlocked(ctx context.Context, input AutonomyAdvisorInput, violation string) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return
	}
	userConfirmed := true
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.advisor.suggestion.block",
		TargetType:      "autonomy_advisor",
		Source:          "guardrail",
		PermissionLevel: PermissionA3,
		DataLevel:       defaultString(input.DataLevel, DataD0),
		InputSummary:    defaultString(input.RuleName, "advisor suggestion"),
		OutputSummary:   "advisor suggestion rejected by output guardrail",
		Reason:          violation,
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    ResultBlocked,
		ErrorSummary:    stringPtr(violation),
	})
}
