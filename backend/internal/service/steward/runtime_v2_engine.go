package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

// RunAgentRuntimeCycle claims and executes queued runs. A database state claim
// prevents two daemon instances from executing the same run concurrently.
func (s *Service) RunAgentRuntimeCycle(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.runtimeV2 {
		return 0, nil
	}
	if err := s.runtimeEnabled(); err != nil {
		return 0, err
	}
	paused, err := s.runtimeExecutionPaused(ctx)
	if err != nil {
		return 0, err
	}
	if paused {
		return 0, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	processed := 0
	seenRunIDs := []string{}
	for processed < limit {
		runID, claimed, err := s.claimQueuedAgentRun(ctx, seenRunIDs)
		if err != nil {
			return processed, err
		}
		if !claimed {
			break
		}
		processed++
		seenRunIDs = append(seenRunIDs, runID)
		if err := s.executeAgentRun(ctx, runID); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func (s *Service) claimQueuedAgentRun(ctx context.Context, excludedRunIDs []string) (string, bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID string
	err = tx.QueryRow(ctx, `
		select id::text from steward_agent_runs
		where status = $1 and cancel_requested = false
		  and not exists (
			select 1 from steward_runtime_execution_control
			where id = 'global' and paused
		  )
		  and (cardinality($2::text[]) = 0 or id::text <> all($2::text[]))
		order by updated_at, created_at
		for update skip locked limit 1
	`, RuntimeRunQueued, excludedRunIDs).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claim queued agent run: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, started_at = coalesce(started_at, $3), updated_at = $3
		where id = $1
	`, runID, RuntimeRunRunning, now); err != nil {
		return "", false, fmt.Errorf("mark agent run running: %w", err)
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.running", RuntimeRunRunning, "execution worker claimed run", map[string]any{}); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return runID, true, nil
}

func (s *Service) executeAgentRun(ctx context.Context, runID string) error {
	for {
		paused, err := s.runtimeExecutionPaused(ctx)
		if err != nil {
			return err
		}
		if paused {
			return s.requeueAgentRunForGlobalPause(ctx, runID)
		}
		run, err := s.GetAgentRun(ctx, runID)
		if err != nil {
			return err
		}
		if run.CancelRequested {
			return s.finishAgentRunCancelled(ctx, runID, "cancellation observed between steps")
		}
		if run.Status != RuntimeRunRunning {
			return nil
		}
		allSucceeded := true
		statusByKey := make(map[string]string, len(run.Steps))
		for _, step := range run.Steps {
			statusByKey[step.Key] = step.Status
			if step.Status != RuntimeStepSucceeded {
				allSucceeded = false
			}
		}
		if allSucceeded {
			return s.finishAgentRunSucceeded(ctx, runID)
		}

		var ready *domain.StewardRunStep
		for index := range run.Steps {
			step := &run.Steps[index]
			if step.Status != RuntimeStepPending {
				continue
			}
			dependenciesReady := true
			for _, dependency := range step.DependsOn {
				if statusByKey[dependency] != RuntimeStepSucceeded {
					dependenciesReady = false
					break
				}
			}
			if dependenciesReady {
				ready = step
				break
			}
		}
		if ready == nil {
			return s.finishAgentRunBlocked(ctx, runID, "no executable step remains; dependency graph is blocked")
		}
		approvalRef := ""
		var approvalProof privilegebroker.SignedApprovalProof
		if ready.RequiresApproval {
			var approved bool
			approvalRef, approvalProof, approved, err = s.agentRunActiveApproval(ctx, runID, run.PlanHash, ready.ToolName)
			if err != nil {
				return err
			}
			if !approved {
				return s.pauseAgentRunForApproval(ctx, runID, ready.ID)
			}
		}
		tool, ok := s.runtimeTools.get(ready.ToolName)
		if !ok {
			return s.finishAgentRunBlocked(ctx, runID, fmt.Sprintf("runtime tool %q is no longer registered", ready.ToolName))
		}
		if tool.Spec().Version != ready.ToolVersion {
			return s.finishAgentRunBlocked(ctx, runID, fmt.Sprintf("runtime tool %q version %q is unavailable; plan requires %q", ready.ToolName, tool.Spec().Version, ready.ToolVersion))
		}
		claimed, invocation, err := s.claimAgentRunStep(ctx, *ready)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		paused, err = s.runtimeExecutionPaused(ctx)
		if err != nil {
			return err
		}
		if paused {
			return s.pauseAgentRunStepForGlobalControl(ctx, *ready, invocation, false)
		}
		cancelRequested, err := s.agentRunCancellationRequested(ctx, ready.RunID)
		if err != nil {
			return err
		}
		if cancelRequested {
			return s.cancelAgentRunStep(ctx, *ready, invocation, "tool invocation cancelled before execution")
		}
		toolCtx, cancel := s.runtimeToolContext(ctx, ready.RunID, invocation.ID, invocation.ControlGeneration, time.Duration(ready.TimeoutSeconds)*time.Second)
		toolCtx = withRuntimeExecutionAuthorization(toolCtx, runtimeExecutionAuthorization{
			RunID: ready.RunID, PlanHash: run.PlanHash, ApprovalRef: approvalRef,
			ApprovalProof: approvalProof,
			RequestedBy:   run.RequestedBy, ControlGeneration: invocation.ControlGeneration,
		})
		result, executeErr := executeRuntimeTool(toolCtx, tool, ready.Arguments)
		if executeErr == nil && toolCtx.Err() != nil {
			executeErr = toolCtx.Err()
		}
		cancel()
		paused, pauseErr := s.runtimeExecutionPaused(ctx)
		if pauseErr != nil {
			return pauseErr
		}
		if paused {
			return s.pauseAgentRunStepForGlobalControl(ctx, *ready, invocation, true)
		}
		if executeErr != nil {
			cancelRequested, cancelErr := s.agentRunCancellationRequested(ctx, ready.RunID)
			if cancelErr != nil {
				return cancelErr
			}
			if cancelRequested {
				return s.cancelAgentRunStep(ctx, *ready, invocation, "tool invocation cancelled by user request")
			}
			return s.failAgentRunStepWithResult(ctx, *ready, invocation, result, executeErr)
		}
		cancelRequested, err = s.agentRunCancellationRequested(ctx, ready.RunID)
		if err != nil {
			return err
		}
		if cancelRequested {
			return s.cancelAgentRunStep(ctx, *ready, invocation, "tool invocation cancelled before verification")
		}
		if err := s.markAgentRunStepVerifying(ctx, *ready); err != nil {
			return err
		}
		verifyCtx, verifyCancel := s.runtimeToolContext(ctx, ready.RunID, invocation.ID, invocation.ControlGeneration, time.Duration(ready.TimeoutSeconds)*time.Second)
		verifyErr := verifyRuntimeTool(verifyCtx, tool, ready.Arguments, result.Output, ready.ExpectedOutput)
		if verifyErr == nil && verifyCtx.Err() != nil {
			verifyErr = verifyCtx.Err()
		}
		verifyCancel()
		paused, pauseErr = s.runtimeExecutionPaused(ctx)
		if pauseErr != nil {
			return pauseErr
		}
		if paused {
			if verifyErr != nil {
				return s.pauseAgentRunStepForGlobalControl(ctx, *ready, invocation, true)
			}
			// The postcondition is already proven, so persist the successful
			// step before honoring the pause. This avoids replaying completed
			// non-idempotent work after execution resumes.
			if err := s.finishAgentRunStepSucceeded(ctx, *ready, invocation, result); err != nil {
				return err
			}
			return s.requeueAgentRunForGlobalPause(ctx, ready.RunID)
		}
		if verifyErr != nil {
			cancelRequested, cancelErr := s.agentRunCancellationRequested(ctx, ready.RunID)
			if cancelErr != nil {
				return cancelErr
			}
			if cancelRequested {
				return s.cancelAgentRunStep(ctx, *ready, invocation, "verification cancelled by user request")
			}
			return s.failAgentRunStepWithResult(ctx, *ready, invocation, result, fmt.Errorf("postcondition failed: %w", verifyErr))
		}
		cancelRequested, err = s.agentRunCancellationRequested(ctx, ready.RunID)
		if err != nil {
			return err
		}
		if cancelRequested {
			return s.cancelAgentRunStep(ctx, *ready, invocation, "tool invocation cancelled before commit")
		}
		if err := s.finishAgentRunStepSucceeded(ctx, *ready, invocation, result); err != nil {
			return err
		}
	}
}

func executeRuntimeTool(ctx context.Context, tool RuntimeTool, arguments map[string]any) (result RuntimeToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("runtime tool panicked: %v", recovered)
		}
	}()
	return tool.Execute(ctx, arguments)
}

func verifyRuntimeTool(ctx context.Context, tool RuntimeTool, arguments map[string]any, output map[string]any, expected map[string]any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("runtime tool verifier panicked: %v", recovered)
		}
	}()
	return tool.Verify(ctx, arguments, output, expected)
}

func (s *Service) runtimeToolContext(parent context.Context, runID, invocationID string, generation int64, timeout time.Duration) (context.Context, context.CancelFunc) {
	timeoutCtx, timeoutCancel := context.WithTimeout(parent, timeout)
	ctx, guardCancel := s.executionGuardContext(timeoutCtx, "runtime:"+invocationID, generation)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		heartbeatEvery := s.runtimeLeaseTTL / 3
		if heartbeatEvery < 200*time.Millisecond {
			heartbeatEvery = 200 * time.Millisecond
		}
		nextHeartbeat := time.Now().Add(heartbeatEvery)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				requested, err := s.agentRunCancellationRequested(ctx, runID)
				if err != nil || requested {
					guardCancel()
					return
				}
				if !time.Now().Before(nextHeartbeat) {
					expiresAt := time.Now().UTC().Add(s.runtimeLeaseTTL)
					commandTag, heartbeatErr := s.db.Pool.Exec(ctx, `
						update steward_tool_invocations
						set heartbeat_at = now(), lease_expires_at = $2
						where id = $1 and status = 'running' and lease_owner = $3
					`, invocationID, expiresAt, s.runtimeWorkerID)
					if heartbeatErr != nil || commandTag.RowsAffected() == 0 {
						guardCancel()
						return
					}
					nextHeartbeat = time.Now().Add(heartbeatEvery)
				}
			}
		}
	}()
	return ctx, func() {
		guardCancel()
		timeoutCancel()
	}
}

func (s *Service) requeueAgentRunForGlobalPause(ctx context.Context, runID string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	commandTag, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, failure_summary = '', updated_at = $3, completed_at = null
		where id = $1 and status in ($4,$5)
	`, runID, RuntimeRunQueued, now, RuntimeRunRunning, RuntimeRunVerifying)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.paused", RuntimeRunQueued,
		"global runtime pause returned the claimed run to the durable queue", map[string]any{"scope": "global"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) pauseAgentRunStepForGlobalControl(ctx context.Context, step domain.StewardRunStep, invocation domain.StewardToolInvocation, outcomeUnknown bool) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if outcomeUnknown && step.ToolIdempotency == RuntimeIdempotencyNonIdempotent {
		reason := "unified execution emergency stop interrupted a non-idempotent tool; outcome is unknown and automatic replay is forbidden"
		commandTag, err := tx.Exec(ctx, `
			update steward_tool_invocations
			set status = $2, error_summary = $3, finished_at = $4, lease_expires_at = null
			where id = $1 and status = 'running'
		`, invocation.ID, RuntimeStepFailed, reason, now)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() == 0 {
			return tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `
			update steward_run_steps
			set status = $2, last_error = $3, updated_at = $4, completed_at = $4 where id = $1
		`, step.ID, RuntimeStepBlocked, reason, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update steward_approval_grants set status = 'revoked', revoked_at = $2
			where run_id = $1 and status = 'active'
		`, step.RunID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update steward_agent_runs
			set status = $2, failure_summary = $3, updated_at = $4, completed_at = $4 where id = $1
		`, step.RunID, RuntimeRunBlocked, reason, now); err != nil {
			return err
		}
		stepID := step.ID
		if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, "run.pause_blocked", RuntimeRunBlocked, reason, map[string]any{
			"scope": "global", "tool_idempotency": step.ToolIdempotency, "requires_fresh_approval": true,
		}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	reason := "unified execution emergency stop interrupted replay-safe work; retry is held until execution resumes"
	if !outcomeUnknown {
		reason = "unified execution emergency stop was observed before tool execution; work returned to the queue"
	}
	commandTag, err := tx.Exec(ctx, `
		update steward_tool_invocations
		set status = $2, error_summary = $3, finished_at = $4, lease_expires_at = null
		where id = $1 and status = 'running'
	`, invocation.ID, RuntimeStepCancelled, reason, now)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		update steward_run_steps
		set status = $2, max_attempts = greatest(max_attempts, attempt + 1), last_error = $3,
		    updated_at = $4, completed_at = null where id = $1
	`, step.ID, RuntimeStepPending, reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, failure_summary = '', updated_at = $3, completed_at = null where id = $1
	`, step.RunID, RuntimeRunQueued, now); err != nil {
		return err
	}
	stepID := step.ID
	if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, "run.paused", RuntimeRunQueued, reason, map[string]any{
		"scope": "global", "tool_idempotency": step.ToolIdempotency,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) agentRunCancellationRequested(ctx context.Context, runID string) (bool, error) {
	var requested bool
	if err := s.db.Pool.QueryRow(ctx, `select cancel_requested from steward_agent_runs where id = $1`, runID).Scan(&requested); err != nil {
		return false, fmt.Errorf("read agent run cancellation: %w", err)
	}
	return requested, nil
}

func (s *Service) claimAgentRunStep(ctx context.Context, step domain.StewardRunStep) (bool, domain.StewardToolInvocation, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, domain.StewardToolInvocation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	var stopped bool
	var generation int64
	if err := tx.QueryRow(ctx, `select paused, generation from steward_runtime_execution_control where id = 'global'`).Scan(&stopped, &generation); err != nil {
		return false, domain.StewardToolInvocation{}, err
	}
	if stopped {
		return false, domain.StewardToolInvocation{}, nil
	}
	var attempt int
	err = tx.QueryRow(ctx, `
		update steward_run_steps
		set status = $2, attempt = attempt + 1, started_at = coalesce(started_at, $3),
		    completed_at = null, updated_at = $3
		where id = $1 and status = $4
		returning attempt
	`, step.ID, RuntimeStepRunning, now, RuntimeStepPending).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, domain.StewardToolInvocation{}, nil
	}
	if err != nil {
		return false, domain.StewardToolInvocation{}, fmt.Errorf("claim agent run step: %w", err)
	}
	if _, ok := s.runtimeTools.get(step.ToolName); !ok {
		return false, domain.StewardToolInvocation{}, fmt.Errorf("runtime tool %q is not registered", step.ToolName)
	}
	inputJSON, _ := json.Marshal(step.Arguments)
	invocation := domain.StewardToolInvocation{
		ID: uuid.NewString(), RunID: step.RunID, StepID: step.ID, ToolName: step.ToolName,
		ToolVersion: step.ToolVersion, Attempt: attempt,
		IdempotencyKey: fmt.Sprintf("%s:%d", step.IdempotencyKey, attempt),
		Status:         RuntimeStepRunning, Input: step.Arguments, LeaseOwner: s.runtimeWorkerID,
		ControlGeneration: generation, StartedAt: now,
	}
	leaseExpiresAt := now.Add(s.runtimeLeaseTTL)
	invocation.HeartbeatAt = &now
	invocation.LeaseExpiresAt = &leaseExpiresAt
	if _, err := tx.Exec(ctx, `
		insert into steward_tool_invocations (
			id, run_id, step_id, tool_name, tool_version, attempt, idempotency_key,
			status, input, output, lease_owner, control_generation, heartbeat_at,
			lease_expires_at, started_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,'{}'::jsonb,$10,$11,$12,$13,$12)
	`, invocation.ID, invocation.RunID, invocation.StepID, invocation.ToolName, invocation.ToolVersion,
		invocation.Attempt, invocation.IdempotencyKey, invocation.Status, string(inputJSON), invocation.LeaseOwner,
		invocation.ControlGeneration, now, leaseExpiresAt); err != nil {
		return false, domain.StewardToolInvocation{}, fmt.Errorf("insert tool invocation: %w", err)
	}
	stepID := step.ID
	if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, "step.running", RuntimeStepRunning,
		"tool invocation started", map[string]any{"attempt": attempt, "tool_name": step.ToolName}); err != nil {
		return false, domain.StewardToolInvocation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, domain.StewardToolInvocation{}, err
	}
	step.Attempt = attempt
	return true, invocation, nil
}

func (s *Service) markAgentRunStepVerifying(ctx context.Context, step domain.StewardRunStep) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `update steward_run_steps set status = $2, updated_at = $3 where id = $1`, step.ID, RuntimeStepVerifying, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update steward_agent_runs set status = $2, updated_at = $3 where id = $1`, step.RunID, RuntimeRunVerifying, now); err != nil {
		return err
	}
	stepID := step.ID
	if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, "step.verifying", RuntimeStepVerifying, "tool output is being verified", map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) finishAgentRunStepSucceeded(ctx context.Context, step domain.StewardRunStep, invocation domain.StewardToolInvocation, result RuntimeToolResult) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	var dataLevel string
	if err := tx.QueryRow(ctx, `select data_level from steward_agent_runs where id = $1`, step.RunID).Scan(&dataLevel); err != nil {
		return err
	}
	canonicalID := uuid.NewString()
	canonical, err := s.storeRuntimeEvidence(ctx, tx, canonicalID, step.RunID, step.ID, "tool_result",
		"governed canonical tool result", dataLevel, result.Output)
	if err != nil {
		return err
	}
	governance := map[string]any{
		"evidence_id": canonicalID, "payload_state": canonical.State,
		"payload_available": canonical.Available, "size_bytes": canonical.SizeBytes,
		"sha256": canonical.SHA256, "redacted": canonical.Redacted,
	}
	invocationOutput := canonical.Preview
	invocationOutput["_governance"] = governance
	outputJSON, _ := json.Marshal(invocationOutput)
	commandTag, err := tx.Exec(ctx, `
		update steward_tool_invocations
		set status = $2, output = $3::jsonb, finished_at = $4, lease_expires_at = null
		where id = $1 and status = $5
	`, invocation.ID, RuntimeStepSucceeded, string(outputJSON), now, RuntimeStepRunning)
	if err != nil {
		return fmt.Errorf("complete tool invocation: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("tool invocation lease was already fenced before completion")
	}
	if _, err := tx.Exec(ctx, `
		update steward_run_steps
		set status = $2, last_error = '', updated_at = $3, completed_at = $3 where id = $1
	`, step.ID, RuntimeStepSucceeded, now); err != nil {
		return fmt.Errorf("complete agent run step: %w", err)
	}
	for _, evidence := range result.Evidence {
		if _, err := s.storeRuntimeEvidence(ctx, tx, uuid.NewString(), step.RunID, step.ID,
			evidence.Kind, evidence.Summary, dataLevel, evidence.Payload); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update steward_agent_runs set status = $2, updated_at = $3 where id = $1`, step.RunID, RuntimeRunRunning, now); err != nil {
		return err
	}
	stepID := step.ID
	if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, "step.succeeded", RuntimeStepSucceeded,
		"tool output satisfied the postcondition", map[string]any{"attempt": invocation.Attempt, "evidence_count": len(result.Evidence) + 1}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) failAgentRunStep(ctx context.Context, step domain.StewardRunStep, invocation domain.StewardToolInvocation, cause error) error {
	return s.failAgentRunStepWithResult(ctx, step, invocation, RuntimeToolResult{}, cause)
}

func (s *Service) failAgentRunStepWithResult(ctx context.Context, step domain.StewardRunStep, invocation domain.StewardToolInvocation, result RuntimeToolResult, cause error) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	summary := sanitizeRuntimeError(cause)
	invocationOutput := map[string]any{}
	evidenceCount := 0
	if len(result.Output) > 0 || len(result.Evidence) > 0 {
		var dataLevel string
		if err := tx.QueryRow(ctx, `select data_level from steward_agent_runs where id = $1`, step.RunID).Scan(&dataLevel); err != nil {
			return err
		}
		if len(result.Output) > 0 {
			canonicalID := uuid.NewString()
			canonical, err := s.storeRuntimeEvidence(ctx, tx, canonicalID, step.RunID, step.ID, "tool_result_failed",
				"governed tool result captured before invocation failure", dataLevel, result.Output)
			if err != nil {
				return err
			}
			invocationOutput = canonical.Preview
			invocationOutput["_governance"] = map[string]any{
				"evidence_id": canonicalID, "payload_state": canonical.State,
				"payload_available": canonical.Available, "size_bytes": canonical.SizeBytes,
				"sha256": canonical.SHA256, "redacted": canonical.Redacted,
			}
			evidenceCount++
		}
		for _, evidence := range result.Evidence {
			if _, err := s.storeRuntimeEvidence(ctx, tx, uuid.NewString(), step.RunID, step.ID,
				evidence.Kind, evidence.Summary, dataLevel, evidence.Payload); err != nil {
				return err
			}
			evidenceCount++
		}
	}
	outputJSON, _ := json.Marshal(invocationOutput)
	commandTag, err := tx.Exec(ctx, `
		update steward_tool_invocations
		set status = $2, error_summary = $3, finished_at = $4, output = $5::jsonb, lease_expires_at = null
		where id = $1 and status = 'running'
	`, invocation.ID, RuntimeStepFailed, summary, now, string(outputJSON))
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	var currentAttempt, maxAttempts int
	if err := tx.QueryRow(ctx, `select attempt, max_attempts from steward_run_steps where id = $1 for update`, step.ID).Scan(&currentAttempt, &maxAttempts); err != nil {
		return err
	}
	stepStatus := RuntimeStepFailed
	runStatus := RuntimeRunFailed
	eventType := "step.failed"
	message := "step exhausted its retry budget"
	completedAt := any(now)
	if currentAttempt < maxAttempts {
		stepStatus = RuntimeStepPending
		runStatus = RuntimeRunQueued
		eventType = "step.retry_scheduled"
		message = "step returned to the queue for a durable retry"
		completedAt = nil
	}
	if _, err := tx.Exec(ctx, `
		update steward_run_steps set status = $2, last_error = $3, updated_at = $4, completed_at = $5 where id = $1
	`, step.ID, stepStatus, summary, now, completedAt); err != nil {
		return err
	}
	runCompletedAt := any(nil)
	if runStatus == RuntimeRunFailed {
		runCompletedAt = now
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, failure_summary = $3, updated_at = $4, completed_at = $5 where id = $1
	`, step.RunID, runStatus, summary, now, runCompletedAt); err != nil {
		return err
	}
	stepID := step.ID
	if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, eventType, stepStatus, message,
		map[string]any{"attempt": currentAttempt, "max_attempts": maxAttempts, "error": summary, "evidence_count": evidenceCount}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) cancelAgentRunStep(ctx context.Context, step domain.StewardRunStep, invocation domain.StewardToolInvocation, reason string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	commandTag, err := tx.Exec(ctx, `
		update steward_tool_invocations
		set status = $2, error_summary = $3, finished_at = $4, lease_expires_at = null
		where id = $1 and status = 'running'
	`, invocation.ID, RuntimeStepCancelled, reason, now)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		update steward_run_steps set status = $2, last_error = $3, updated_at = $4, completed_at = $4
		where run_id = $1 and status in ('pending','running','verifying','blocked')
	`, step.RunID, RuntimeStepCancelled, reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, failure_summary = '', cancel_requested = false, updated_at = $3, completed_at = $3
		where id = $1
	`, step.RunID, RuntimeRunCancelled, now); err != nil {
		return err
	}
	stepID := step.ID
	if err := appendRuntimeEvent(ctx, tx, step.RunID, &stepID, "run.cancelled", RuntimeRunCancelled, reason, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) finishAgentRunSucceeded(ctx context.Context, runID string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `update steward_agent_runs set status = $2, updated_at = $3 where id = $1`, runID, RuntimeRunVerifying, now); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.verifying", RuntimeRunVerifying, "all step postconditions passed; verifying run invariant", map[string]any{}); err != nil {
		return err
	}
	var incomplete int
	if err := tx.QueryRow(ctx, `select count(*) from steward_run_steps where run_id = $1 and status <> $2`, runID, RuntimeStepSucceeded).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete != 0 {
		return fmt.Errorf("run invariant failed: %d steps are not succeeded", incomplete)
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, failure_summary = '', cancel_requested = false, updated_at = $3, completed_at = $3
		where id = $1
	`, runID, RuntimeRunSucceeded, now); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.succeeded", RuntimeRunSucceeded, "run completed with verified evidence", map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) finishAgentRunCancelled(ctx context.Context, runID string, reason string) error {
	return s.finishAgentRunWithStatus(ctx, runID, RuntimeRunCancelled, RuntimeStepCancelled, "run.cancelled", reason)
}

func (s *Service) finishAgentRunBlocked(ctx context.Context, runID string, reason string) error {
	return s.finishAgentRunWithStatus(ctx, runID, RuntimeRunBlocked, RuntimeStepBlocked, "run.blocked", reason)
}

func (s *Service) finishAgentRunWithStatus(ctx context.Context, runID string, runStatus string, stepStatus string, eventType string, reason string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		update steward_run_steps set status = $2, last_error = $3, updated_at = $4, completed_at = $4
		where run_id = $1 and status in ('pending','running','verifying','blocked')
	`, runID, stepStatus, reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs
		set status = $2, failure_summary = $3, cancel_requested = false, updated_at = $4, completed_at = $4
		where id = $1
	`, runID, runStatus, reason, now); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, eventType, runStatus, reason, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) pauseAgentRunForApproval(ctx context.Context, runID string, stepID string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `update steward_agent_runs set status = $2, updated_at = $3 where id = $1`, runID, RuntimeRunAwaitingApproval, now); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, runID, &stepID, "run.awaiting_approval", RuntimeRunAwaitingApproval, "active approval is missing or expired", map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) agentRunHasActiveApproval(ctx context.Context, runID string, planHash string) (bool, error) {
	_, _, approved, err := s.agentRunActiveApproval(ctx, runID, planHash, "")
	return approved, err
}

func (s *Service) agentRunActiveApproval(ctx context.Context, runID string, planHash string, toolName string) (string, privilegebroker.SignedApprovalProof, bool, error) {
	var approvalID string
	var proofPayload []byte
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, approval_proof from steward_approval_grants
		where run_id = $1 and plan_hash = $2 and status = 'active'
		  and (expires_at is null or expires_at > $3)
		  and ($4 <> 'privilege.execute' or (approval_proof_id <> '' and approval_proof_expires_at > $3))
		order by created_at desc limit 1
	`, runID, planHash, time.Now().UTC(), toolName).Scan(&approvalID, &proofPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", privilegebroker.SignedApprovalProof{}, false, nil
	}
	if err != nil {
		return "", privilegebroker.SignedApprovalProof{}, false, err
	}
	if toolName != "privilege.execute" {
		return approvalID, privilegebroker.SignedApprovalProof{}, true, nil
	}
	var proof privilegebroker.SignedApprovalProof
	if err := json.Unmarshal(proofPayload, &proof); err != nil {
		return "", privilegebroker.SignedApprovalProof{}, false, fmt.Errorf("decode active signed approval proof: %w", err)
	}
	if proof.Claims.ProofID == "" {
		return "", privilegebroker.SignedApprovalProof{}, false, fmt.Errorf("active signed approval proof is empty")
	}
	return proof.Claims.ProofID, proof, true, nil
}

// RecoverAgentRuntime converts interrupted replay-safe work back into the
// durable queue. An interrupted non-idempotent tool has an unknown outcome, so
// it is blocked and its approval is revoked instead of being replayed.
func (s *Service) RecoverAgentRuntime(ctx context.Context) (int, error) {
	if s == nil || !s.runtimeV2 {
		return 0, nil
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select id::text from steward_agent_runs where status in ($1,$2) for update
	`, RuntimeRunRunning, RuntimeRunVerifying)
	if err != nil {
		return 0, err
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			return 0, err
		}
		runIDs = append(runIDs, runID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, runID := range runIDs {
		var activeStepID, idempotencyMode string
		err := tx.QueryRow(ctx, `
			select id::text, tool_idempotency
			from steward_run_steps
			where run_id = $1 and status in ($2,$3)
			order by position limit 1
		`, runID, RuntimeStepRunning, RuntimeStepVerifying).Scan(&activeStepID, &idempotencyMode)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if err == nil && idempotencyMode == RuntimeIdempotencyNonIdempotent {
			reason := "worker interrupted during a non-idempotent tool; outcome is unknown and automatic replay is forbidden"
			if _, err := tx.Exec(ctx, `
				update steward_tool_invocations
				set status = $2, error_summary = $3, finished_at = $4, lease_expires_at = null
				where run_id = $1 and status = $5
			`, runID, RuntimeStepFailed, reason, now, RuntimeStepRunning); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(ctx, `
				update steward_run_steps
				set status = $2, last_error = $3, updated_at = $4, completed_at = $4
				where id = $1
			`, activeStepID, RuntimeStepBlocked, reason, now); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(ctx, `
				update steward_approval_grants
				set status = 'revoked', revoked_at = $2
				where run_id = $1 and status = 'active'
			`, runID, now); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(ctx, `
				update steward_agent_runs
				set status = $2, failure_summary = $3, updated_at = $4, completed_at = $4
				where id = $1
			`, runID, RuntimeRunBlocked, reason, now); err != nil {
				return 0, err
			}
			if err := appendRuntimeEvent(ctx, tx, runID, &activeStepID, "run.recovery_blocked", RuntimeRunBlocked, reason, map[string]any{
				"tool_idempotency":        idempotencyMode,
				"requires_fresh_approval": true,
			}); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			update steward_tool_invocations
			set status = $2, error_summary = 'worker interrupted; invocation will be retried', finished_at = $3, lease_expires_at = null
			where run_id = $1 and status = $4
		`, runID, RuntimeStepFailed, now, RuntimeStepRunning); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			update steward_run_steps
			set status = $2, max_attempts = greatest(max_attempts, attempt + 1),
			    last_error = 'worker interrupted; retry queued', updated_at = $3
			where run_id = $1 and status in ($4,$5)
		`, runID, RuntimeStepPending, now, RuntimeStepRunning, RuntimeStepVerifying); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			update steward_agent_runs
			set status = $2, failure_summary = '', updated_at = $3 where id = $1
		`, runID, RuntimeRunQueued, now); err != nil {
			return 0, err
		}
		if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.recovered", RuntimeRunQueued, "interrupted run recovered to queue", map[string]any{}); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(runIDs), nil
}

func sanitizeRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.Join(strings.Fields(strings.TrimSpace(err.Error())), " ")
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return value
}
