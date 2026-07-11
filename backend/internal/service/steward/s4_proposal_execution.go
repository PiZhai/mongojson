package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func (s *Service) SimulateAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomousRun, error) {
	proposal, err := s.getAutonomyProposal(ctx, id)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	executor, err := s.resolveAutonomyProposalExecutor(proposal)
	if err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunBlocked,
			proposal.TriggerReason, err.Error(), "register a low-risk executor or revise the proposal action")
	}
	result, err := executor.Simulate(ctx, proposal)
	if err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunFailed,
			proposal.TriggerReason, "autonomy action simulation failed", err.Error())
	}
	if err := validateAutonomyExecutionResult(executor, result, false); err != nil {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunFailed,
			proposal.TriggerReason, "autonomy action simulation returned an invalid result", err.Error())
	}
	return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeSimulate, RunSuccess,
		proposal.TriggerReason, result.ImpactSummary, result.RecoveryHint)
}

func (s *Service) ExecuteAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomousRun, error) {
	return s.executeAutonomyProposal(ctx, id, false)
}

func (s *Service) RetryAutonomyProposal(ctx context.Context, id string) (domain.StewardAutonomousRun, error) {
	run, err := s.executeAutonomyProposal(ctx, id, true)
	status := ResultFailed
	output := "manual autonomy retry failed"
	var errorSummary *string
	if err == nil {
		status = mapRunStatusToAudit(run.Status)
		output = "manual autonomy retry completed with status " + run.Status
	} else {
		summary := strings.TrimSpace(err.Error())
		errorSummary = &summary
	}
	confirmed := true
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "autonomy.retry",
		TargetType:      "autonomy_proposal",
		TargetID:        stringPtr(id),
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   output,
		UserConfirmed:   &confirmed,
		ResultStatus:    status,
		ErrorSummary:    errorSummary,
	})
	return run, err
}

func (s *Service) executeAutonomyProposal(ctx context.Context, id string, manualRetry bool) (domain.StewardAutonomousRun, error) {
	lease, err := acquireAutonomyExecutionLease(ctx, s.db.Pool, id)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	defer lease.Release()
	gatedCtx, policyGate, err := acquireAutonomyPolicyReadGate(ctx, s.db.Pool)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	defer policyGate.Release()
	ctx = gatedCtx

	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	proposal, err := s.getAutonomyProposal(ctx, id)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	if issue := autonomyProposalPolicyIssue(proposal); issue != "" {
		run, runErr := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "invalid persisted proposal policy blocked execution", issue)
		if runErr != nil {
			return domain.StewardAutonomousRun{}, runErr
		}
		if proposal.Status != ProposalDismissed && proposal.Status != ProposalExecuted {
			_, _ = s.updateProposalStatusLocked(ctx, id, ProposalBlocked, "autonomy.proposal.invalid_policy")
		}
		return run, nil
	}
	if manualRetry {
		latestFailed, retryErr := s.latestAutonomyExecutionFailed(ctx, proposal.ID)
		if retryErr != nil {
			return domain.StewardAutonomousRun{}, retryErr
		}
		if !latestFailed || !proposal.RetryEligible {
			return domain.StewardAutonomousRun{}, fmt.Errorf("autonomy proposal has no failed execution eligible for retry")
		}
	}
	if proposal.Status == ProposalExecuted {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "proposal already executed; duplicate execution skipped", "inspect the created task or create a new proposal")
	}
	if proposal.Status == ProposalDismissed {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "proposal was dismissed; execution skipped", "create a new proposal if this action is still needed")
	}
	if proposal.Status == ProposalBlocked && !manualRetry {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "manual high-risk review", proposal.RiskLevel, proposal.ImpactSummary)
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "proposal is blocked from autonomous execution", "manual review is required outside autonomy")
	}
	if settings.Paused {
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "autonomy paused; execution skipped", "resume autonomy before executing")
	}
	automaticAllowed := proposal.Policy == AutonomyPolicyAuto
	if proposal.RuleID != nil {
		var currentRuleIssue string
		automaticAllowed, currentRuleIssue, err = s.currentRuleExecutionPolicy(ctx, proposal)
		if err != nil {
			return domain.StewardAutonomousRun{}, err
		}
		if currentRuleIssue != "" {
			return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
				proposal.TriggerReason, "current autonomy rule blocked execution", currentRuleIssue)
		}
	}
	if proposal.Status != ProposalApproved && !automaticAllowed {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "approve autonomous execution", "proposal needs explicit approval", proposal.ImpactSummary)
		return s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "approval required before execution", "approve proposal or change rule policy")
	}
	if proposal.Policy == AutonomyPolicyNever || isHighRisk(proposal.RiskLevel, proposal.PermissionLevel) ||
		permissionRank(proposal.PermissionLevel) > permissionRank(settings.MaxAutoPermission) {
		_, _ = s.createApprovalRequest(ctx, &proposal.ID, "manual high-risk review", proposal.RiskLevel, proposal.ImpactSummary)
		run, runErr := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, "high-risk proposal blocked; plan only", "manually review and execute outside autonomy")
		if runErr != nil {
			return domain.StewardAutonomousRun{}, runErr
		}
		_, _ = s.updateProposalStatusLocked(ctx, id, ProposalBlocked, "autonomy.proposal.block")
		return run, nil
	}

	executor, err := s.resolveAutonomyProposalExecutor(proposal)
	if err != nil {
		run, runErr := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunBlocked,
			proposal.TriggerReason, err.Error(), "register a low-risk executor or revise the proposal action")
		if runErr != nil {
			return domain.StewardAutonomousRun{}, runErr
		}
		_, _ = s.updateProposalStatusLocked(ctx, id, ProposalBlocked, "autonomy.proposal.block")
		return run, nil
	}
	execution, err := executor.Execute(ctx, proposal)
	if err != nil {
		return s.recordAutonomyExecutionFailure(ctx, proposal, "autonomy action execution failed", err)
	}
	if err := validateAutonomyExecutionResult(executor, execution, true); err != nil {
		return s.recordAutonomyExecutionFailure(ctx, proposal, "autonomy action execution returned an invalid result", err)
	}
	var createdTaskID *string
	if execution.TargetType == "task" && strings.TrimSpace(execution.TargetID) != "" {
		createdTaskID = stringPtr(execution.TargetID)
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_proposals
		set status = $1, created_task_id = $2, execution_target_type = $3,
		    execution_target_id = $4, updated_at = $5
		where id = $6
	`, ProposalExecuted, createdTaskID, execution.TargetType, execution.TargetID, now, id); err != nil {
		return domain.StewardAutonomousRun{}, fmt.Errorf("mark autonomy proposal executed: %w", err)
	}
	run, err := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunSuccess,
		proposal.TriggerReason, execution.ImpactSummary, execution.RecoveryHint)
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	var auditTargetID *string
	if _, parseErr := uuid.Parse(execution.TargetID); parseErr == nil {
		auditTargetID = stringPtr(execution.TargetID)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.execute",
		TargetType:      defaultString(execution.TargetType, "autonomy_action"),
		TargetID:        auditTargetID,
		Source:          "autonomy",
		PermissionLevel: proposal.PermissionLevel,
		DataLevel:       proposal.DataLevel,
		InputSummary:    "action=" + proposal.Action + "; " + proposal.TriggerReason,
		OutputSummary:   execution.ImpactSummary,
		ResultStatus:    ResultOK,
	})
	return run, nil
}

func (s *Service) recordAutonomyExecutionFailure(ctx context.Context, proposal domain.StewardAutonomyProposal, impact string, cause error) (domain.StewardAutonomousRun, error) {
	run, err := s.recordAutonomousRun(ctx, &proposal.ID, proposal.RuleID, RunModeExecute, RunFailed,
		proposal.TriggerReason, impact, cause.Error())
	if err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	if proposal.Policy != AutonomyPolicyAuto {
		return run, nil
	}
	if err := s.populateAutonomyRetryState(ctx, &proposal); err != nil {
		return domain.StewardAutonomousRun{}, err
	}
	if proposal.RetryExhausted {
		if _, err := s.updateProposalStatusLocked(ctx, proposal.ID, ProposalBlocked, "autonomy.retry.exhausted"); err != nil {
			return domain.StewardAutonomousRun{}, err
		}
	}
	return run, nil
}

func (s *Service) resolveAutonomyProposalExecutor(proposal domain.StewardAutonomyProposal) (AutonomyActionExecutor, error) {
	executor, ok := s.autonomyActionExecutor(proposal.Action)
	if !ok {
		return nil, fmt.Errorf("no registered executor for autonomy action %s", proposal.Action)
	}
	if err := executorAllowsProposal(executor, proposal); err != nil {
		return nil, err
	}
	return executor, nil
}
