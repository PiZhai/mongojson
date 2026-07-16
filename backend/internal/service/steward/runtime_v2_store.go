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
	"github.com/jackc/pgx/v5/pgconn"

	"mongojson/backend/internal/domain"
)

func (s *Service) ensureRuntimeToolSpecs(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil || s.db.Pool == nil || s.runtimeTools == nil {
		return nil
	}
	for _, spec := range s.runtimeTools.specs() {
		inputSchema, err := json.Marshal(spec.InputSchema)
		if err != nil {
			return fmt.Errorf("encode runtime tool %s input schema: %w", spec.Name, err)
		}
		outputSchema, err := json.Marshal(spec.OutputSchema)
		if err != nil {
			return fmt.Errorf("encode runtime tool %s output schema: %w", spec.Name, err)
		}
		if _, err := s.db.Pool.Exec(ctx, `
			insert into steward_runtime_tool_specs (
				name, version, description, input_schema, output_schema, permission_level,
				risk_level, side_effect, approval_mode, idempotency_mode,
				deterministic, supports_cancel, default_timeout_seconds, updated_at
			) values ($1,$2,$3,$4::jsonb,$5::jsonb,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			on conflict (name) do update
			set version = excluded.version,
			    description = excluded.description,
			    input_schema = excluded.input_schema,
			    output_schema = excluded.output_schema,
			    permission_level = excluded.permission_level,
			    risk_level = excluded.risk_level,
			    side_effect = excluded.side_effect,
			    approval_mode = excluded.approval_mode,
			    idempotency_mode = excluded.idempotency_mode,
			    deterministic = excluded.deterministic,
			    supports_cancel = excluded.supports_cancel,
			    default_timeout_seconds = excluded.default_timeout_seconds,
			    updated_at = excluded.updated_at
		`, spec.Name, spec.Version, spec.Description, string(inputSchema), string(outputSchema),
			spec.PermissionLevel, spec.RiskLevel, spec.SideEffect, spec.ApprovalMode, spec.IdempotencyMode,
			spec.Deterministic, spec.SupportsCancel,
			spec.DefaultTimeoutSec, now); err != nil {
			return fmt.Errorf("ensure runtime tool spec %s: %w", spec.Name, err)
		}
	}
	return nil
}

func (s *Service) ListRuntimeToolSpecs(ctx context.Context) ([]domain.StewardToolSpec, error) {
	if err := s.runtimeEnabled(); err != nil {
		return nil, err
	}
	if err := s.ensureRuntimeToolSpecs(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		select name, version, description, input_schema, output_schema, permission_level,
		       risk_level, side_effect, approval_mode, idempotency_mode,
		       deterministic, supports_cancel, default_timeout_seconds, updated_at
		from steward_runtime_tool_specs order by name
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtime tool specs: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardToolSpec{}
	for rows.Next() {
		var item domain.StewardToolSpec
		var inputJSON, outputJSON []byte
		if err := rows.Scan(&item.Name, &item.Version, &item.Description, &inputJSON, &outputJSON,
			&item.PermissionLevel, &item.RiskLevel, &item.SideEffect, &item.ApprovalMode, &item.IdempotencyMode,
			&item.Deterministic, &item.SupportsCancel,
			&item.DefaultTimeoutSec, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan runtime tool spec: %w", err)
		}
		item.InputSchema = decodeRuntimeMap(inputJSON)
		item.OutputSchema = decodeRuntimeMap(outputJSON)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListAgentRuns(ctx context.Context, status string, limit int) ([]domain.StewardAgentRunSummary, error) {
	if err := s.runtimeEnabled(); err != nil {
		return nil, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && !validRuntimeRunStatus(status) {
		return nil, fmt.Errorf("%w: invalid run status filter", ErrAgentRunInvalid)
	}
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	rows, err := s.db.Pool.Query(ctx, `
		select run.id::text, run.goal, run.status, run.mode, run.plan_hash, run.planner,
		       run.permission_ceiling, run.data_level, count(step.id)::int,
		       count(step.id) filter (where step.status = $3)::int,
		       coalesce(bool_or(step.requires_approval), false), run.failure_summary,
		       run.created_at, run.updated_at, run.completed_at
		from steward_agent_runs run
		left join steward_run_steps step on step.run_id = run.id
		where ($1 = '' or run.status = $1)
		group by run.id
		order by run.updated_at desc, run.created_at desc
		limit $2
	`, status, limit, RuntimeStepSucceeded)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardAgentRunSummary{}
	for rows.Next() {
		var item domain.StewardAgentRunSummary
		if err := rows.Scan(&item.ID, &item.Goal, &item.Status, &item.Mode, &item.PlanHash, &item.Planner,
			&item.PermissionCeiling, &item.DataLevel, &item.StepCount, &item.CompletedSteps,
			&item.RequiresApproval, &item.FailureSummary, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan agent run summary: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateAgentRun(ctx context.Context, input CreateAgentRunInput) (domain.StewardAgentRun, error) {
	if err := s.runtimeEnabled(); err != nil {
		return domain.StewardAgentRun{}, err
	}
	if err := s.ensureRuntimeToolSpecs(ctx, time.Now().UTC()); err != nil {
		return domain.StewardAgentRun{}, err
	}
	normalized, planHash, err := s.normalizeAgentRunInput(input)
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	if normalized.IdempotencyKey != "" {
		var existingID, existingHash string
		err := s.db.Pool.QueryRow(ctx, `
			select id::text, plan_hash from steward_agent_runs where idempotency_key = $1
		`, normalized.IdempotencyKey).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != planHash {
				return domain.StewardAgentRun{}, ErrAgentRunConflict
			}
			return s.GetAgentRun(ctx, existingID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardAgentRun{}, fmt.Errorf("check agent run idempotency: %w", err)
		}
	}

	now := time.Now().UTC()
	runID := uuid.NewString()
	status := RuntimeRunDraft
	if normalized.AutoStart {
		status = RuntimeRunQueued
		for _, step := range normalized.Steps {
			if step.RequiresApproval {
				status = RuntimeRunAwaitingApproval
				break
			}
		}
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("begin create agent run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	policySummary, _ := json.Marshal(normalized.PolicySummary)
	_, err = tx.Exec(ctx, `
		insert into steward_agent_runs (
			id, goal, status, mode, plan_version, plan_hash, idempotency_key, requested_by,
			target_device, data_level, permission_ceiling, planner, planner_version,
			source_instruction, plan_summary, policy_summary, created_at, updated_at
		) values ($1,$2,$3,$4,1,$5,nullif($6,''),$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16,$16)
	`, runID, normalized.Goal, status, normalized.Mode, planHash, normalized.IdempotencyKey,
		normalized.RequestedBy, normalized.TargetDevice, normalized.DataLevel, normalized.PermissionCeiling,
		normalized.Planner, normalized.PlannerVersion, normalized.SourceInstruction, normalized.PlanSummary,
		string(policySummary), now)
	if err != nil {
		var pgErr *pgconn.PgError
		if normalized.IdempotencyKey != "" && errors.As(err, &pgErr) && pgErr.Code == "23505" {
			_ = tx.Rollback(ctx)
			var existingID, existingHash string
			if queryErr := s.db.Pool.QueryRow(ctx, `select id::text, plan_hash from steward_agent_runs where idempotency_key = $1`, normalized.IdempotencyKey).Scan(&existingID, &existingHash); queryErr != nil {
				return domain.StewardAgentRun{}, fmt.Errorf("resolve agent run idempotency race: %w", queryErr)
			}
			if existingHash != planHash {
				return domain.StewardAgentRun{}, ErrAgentRunConflict
			}
			return s.GetAgentRun(ctx, existingID)
		}
		return domain.StewardAgentRun{}, fmt.Errorf("insert agent run: %w", err)
	}
	for index, step := range normalized.Steps {
		arguments, _ := json.Marshal(step.Arguments)
		expected, _ := json.Marshal(step.ExpectedOutput)
		dependencies, _ := json.Marshal(step.DependsOn)
		stepID := uuid.NewString()
		stepIdempotency := fmt.Sprintf("%s:%s:%s", runID, step.Key, planHash[:16])
		if _, err := tx.Exec(ctx, `
			insert into steward_run_steps (
				id, run_id, step_key, position, title, tool_name, tool_version, arguments, expected_output,
				depends_on, status, max_attempts, timeout_seconds, idempotency_key,
				tool_idempotency, policy_decision, policy_reason, requires_approval, created_at, updated_at
			) values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19)
		`, stepID, runID, step.Key, index+1, step.Title, step.ToolName, step.ToolVersion, string(arguments), string(expected),
			string(dependencies), RuntimeStepPending, step.MaxAttempts, step.TimeoutSeconds,
			stepIdempotency, step.ToolIdempotency, step.PolicyDecision, step.PolicyReason, step.RequiresApproval, now); err != nil {
			return domain.StewardAgentRun{}, fmt.Errorf("insert agent run step %s: %w", step.Key, err)
		}
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.created", status, "execution plan persisted after policy evaluation", map[string]any{
		"plan_hash": planHash, "step_count": len(normalized.Steps), "planner": normalized.Planner,
	}); err != nil {
		return domain.StewardAgentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("commit create agent run: %w", err)
	}
	return s.GetAgentRun(ctx, runID)
}

func (s *Service) GetAgentRun(ctx context.Context, runID string) (domain.StewardAgentRun, error) {
	if err := s.runtimeEnabled(); err != nil {
		return domain.StewardAgentRun{}, err
	}
	runID = strings.TrimSpace(runID)
	if _, err := uuid.Parse(runID); err != nil {
		return domain.StewardAgentRun{}, ErrAgentRunNotFound
	}
	var run domain.StewardAgentRun
	var policySummary []byte
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, goal, status, mode, plan_version, plan_hash, coalesce(idempotency_key,''),
		       requested_by, target_device, data_level, permission_ceiling, planner, planner_version,
		       source_instruction, plan_summary, policy_summary, cancel_requested,
		       failure_summary, created_at, updated_at, started_at, completed_at
		from steward_agent_runs where id = $1
	`, runID).Scan(&run.ID, &run.Goal, &run.Status, &run.Mode, &run.PlanVersion, &run.PlanHash,
		&run.IdempotencyKey, &run.RequestedBy, &run.TargetDevice, &run.DataLevel, &run.PermissionCeiling,
		&run.Planner, &run.PlannerVersion, &run.SourceInstruction, &run.PlanSummary, &policySummary,
		&run.CancelRequested, &run.FailureSummary, &run.CreatedAt, &run.UpdatedAt, &run.StartedAt, &run.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentRun{}, ErrAgentRunNotFound
	}
	if err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("get agent run: %w", err)
	}
	run.PolicySummary = decodeRuntimeMap(policySummary)
	steps, err := s.listAgentRunSteps(ctx, run.ID)
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	run.Steps = steps
	approvals, err := s.listAgentRunApprovals(ctx, run.ID)
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	run.Approvals = approvals
	return run, nil
}

func (s *Service) listAgentRunSteps(ctx context.Context, runID string) ([]domain.StewardRunStep, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, run_id::text, step_key, position, title, tool_name, tool_version, arguments,
		       expected_output, depends_on, status, attempt, max_attempts, timeout_seconds,
		       idempotency_key, tool_idempotency, policy_decision, policy_reason,
		       requires_approval, last_error, created_at, updated_at,
		       started_at, completed_at
		from steward_run_steps where run_id = $1 order by position
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list agent run steps: %w", err)
	}
	defer rows.Close()
	steps := []domain.StewardRunStep{}
	for rows.Next() {
		var step domain.StewardRunStep
		var argumentsJSON, expectedJSON, dependenciesJSON []byte
		if err := rows.Scan(&step.ID, &step.RunID, &step.Key, &step.Position, &step.Title, &step.ToolName, &step.ToolVersion,
			&argumentsJSON, &expectedJSON, &dependenciesJSON, &step.Status, &step.Attempt, &step.MaxAttempts,
			&step.TimeoutSeconds, &step.IdempotencyKey, &step.ToolIdempotency, &step.PolicyDecision, &step.PolicyReason,
			&step.RequiresApproval, &step.LastError,
			&step.CreatedAt, &step.UpdatedAt, &step.StartedAt, &step.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan agent run step: %w", err)
		}
		step.Arguments = decodeRuntimeMap(argumentsJSON)
		step.ExpectedOutput = decodeRuntimeMap(expectedJSON)
		_ = json.Unmarshal(dependenciesJSON, &step.DependsOn)
		steps = append(steps, step)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range steps {
		steps[index].Invocations, err = s.listToolInvocations(ctx, steps[index].ID)
		if err != nil {
			return nil, err
		}
		steps[index].Evidence, err = s.listEvidenceArtifacts(ctx, steps[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return steps, nil
}

func (s *Service) listToolInvocations(ctx context.Context, stepID string) ([]domain.StewardToolInvocation, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, run_id::text, step_id::text, tool_name, tool_version, attempt,
		       idempotency_key, status, input, output, error_summary, lease_owner,
		       control_generation, started_at, finished_at, heartbeat_at, lease_expires_at
		from steward_tool_invocations where step_id = $1 order by attempt
	`, stepID)
	if err != nil {
		return nil, fmt.Errorf("list tool invocations: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardToolInvocation{}
	for rows.Next() {
		var item domain.StewardToolInvocation
		var inputJSON, outputJSON []byte
		if err := rows.Scan(&item.ID, &item.RunID, &item.StepID, &item.ToolName, &item.ToolVersion,
			&item.Attempt, &item.IdempotencyKey, &item.Status, &inputJSON, &outputJSON,
			&item.ErrorSummary, &item.LeaseOwner, &item.ControlGeneration, &item.StartedAt,
			&item.FinishedAt, &item.HeartbeatAt, &item.LeaseExpiresAt); err != nil {
			return nil, fmt.Errorf("scan tool invocation: %w", err)
		}
		item.Input = decodeRuntimeMap(inputJSON)
		item.Output = decodeRuntimeMap(outputJSON)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listEvidenceArtifacts(ctx context.Context, stepID string) ([]domain.StewardEvidenceArtifact, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, run_id::text, step_id::text, kind, summary, data_level,
		       content_type, payload_state, payload_available, size_bytes, sha256,
		       redacted, created_at
		from steward_evidence_artifacts where step_id = $1 order by created_at, id
	`, stepID)
	if err != nil {
		return nil, fmt.Errorf("list evidence artifacts: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardEvidenceArtifact{}
	for rows.Next() {
		var item domain.StewardEvidenceArtifact
		if err := rows.Scan(&item.ID, &item.RunID, &item.StepID, &item.Kind, &item.Summary,
			&item.DataLevel, &item.ContentType, &item.PayloadState, &item.PayloadAvailable,
			&item.SizeBytes, &item.SHA256, &item.Redacted, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan evidence artifact: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listAgentRunApprovals(ctx context.Context, runID string) ([]domain.StewardApprovalGrant, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, run_id::text, plan_hash, scope, granted_by,
		       case when status = 'active' and expires_at is not null and expires_at <= now() then 'expired' else status end,
		       reason,
		       created_at, expires_at, revoked_at, approval_proof_id, approval_key_id, approval_proof_expires_at
		from steward_approval_grants where run_id = $1 order by created_at
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list agent run approvals: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardApprovalGrant{}
	for rows.Next() {
		var item domain.StewardApprovalGrant
		if err := rows.Scan(&item.ID, &item.RunID, &item.PlanHash, &item.Scope, &item.GrantedBy,
			&item.Status, &item.Reason, &item.CreatedAt, &item.ExpiresAt, &item.RevokedAt,
			&item.ApprovalProofID, &item.ApprovalKeyID, &item.ApprovalProofExpiresAt); err != nil {
			return nil, fmt.Errorf("scan agent run approval: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListAgentRunEvents(ctx context.Context, runID string, after int64, limit int) ([]domain.StewardRunEvent, error) {
	if err := s.runtimeEnabled(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `
		select sequence, id::text, run_id::text, step_id::text, type, status, message, payload, created_at
		from steward_run_events where run_id = $1 and sequence > $2 order by sequence limit $3
	`, runID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent run events: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardRunEvent{}
	for rows.Next() {
		var item domain.StewardRunEvent
		var payload []byte
		if err := rows.Scan(&item.Sequence, &item.ID, &item.RunID, &item.StepID, &item.Type,
			&item.Status, &item.Message, &payload, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan agent run event: %w", err)
		}
		item.Payload = decodeRuntimeMap(payload)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) StartAgentRun(ctx context.Context, runID string) (domain.StewardAgentRun, error) {
	return s.transitionAgentRun(ctx, runID, "start")
}

func (s *Service) CancelAgentRun(ctx context.Context, runID string) (domain.StewardAgentRun, error) {
	return s.transitionAgentRun(ctx, runID, "cancel")
}

func (s *Service) ResumeAgentRun(ctx context.Context, runID string) (domain.StewardAgentRun, error) {
	return s.transitionAgentRun(ctx, runID, "resume")
}

func (s *Service) transitionAgentRun(ctx context.Context, runID string, action string) (domain.StewardAgentRun, error) {
	if err := s.runtimeEnabled(); err != nil {
		return domain.StewardAgentRun{}, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, planHash string
	var cancelRequested bool
	if err := tx.QueryRow(ctx, `select status, plan_hash, cancel_requested from steward_agent_runs where id = $1 for update`, runID).
		Scan(&status, &planHash, &cancelRequested); errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentRun{}, ErrAgentRunNotFound
	} else if err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("lock agent run: %w", err)
	}
	now := time.Now().UTC()
	nextStatus := status
	message := ""
	switch action {
	case "start":
		if status == RuntimeRunQueued || status == RuntimeRunAwaitingApproval {
			return s.commitAndGetAgentRun(ctx, tx, runID)
		}
		if status != RuntimeRunDraft && status != RuntimeRunPlanning {
			return domain.StewardAgentRun{}, ErrAgentRunInvalidTransition
		}
		approved, err := runtimeRunApproved(ctx, tx, runID, planHash, now)
		if err != nil {
			return domain.StewardAgentRun{}, err
		}
		needsApproval, err := runtimeRunNeedsApproval(ctx, tx, runID)
		if err != nil {
			return domain.StewardAgentRun{}, err
		}
		if needsApproval && !approved {
			nextStatus = RuntimeRunAwaitingApproval
			message = "run requires approval bound to the current plan hash"
		} else {
			nextStatus = RuntimeRunQueued
			message = "run queued"
		}
	case "cancel":
		if status == RuntimeRunCancelled {
			return s.commitAndGetAgentRun(ctx, tx, runID)
		}
		if status == RuntimeRunSucceeded || status == RuntimeRunFailed {
			return domain.StewardAgentRun{}, ErrAgentRunInvalidTransition
		}
		if status == RuntimeRunRunning || status == RuntimeRunVerifying || status == RuntimeRunCompensating {
			if _, err := tx.Exec(ctx, `update steward_agent_runs set cancel_requested = true, updated_at = $2 where id = $1`, runID, now); err != nil {
				return domain.StewardAgentRun{}, err
			}
			message = "cooperative cancellation requested"
		} else {
			nextStatus = RuntimeRunCancelled
			if _, err := tx.Exec(ctx, `
				update steward_run_steps set status = $2, updated_at = $3, completed_at = $3
				where run_id = $1 and status in ('pending','running','verifying','blocked')
			`, runID, RuntimeStepCancelled, now); err != nil {
				return domain.StewardAgentRun{}, err
			}
			message = "run cancelled before execution"
		}
	case "resume":
		if status != RuntimeRunFailed && status != RuntimeRunCancelled && status != RuntimeRunBlocked {
			return domain.StewardAgentRun{}, ErrAgentRunInvalidTransition
		}
		if _, err := tx.Exec(ctx, `
			update steward_run_steps
			set status = $2, max_attempts = greatest(max_attempts, attempt + 1), last_error = '',
			    updated_at = $3, completed_at = null
			where run_id = $1 and status in ('failed','cancelled','blocked','running','verifying')
		`, runID, RuntimeStepPending, now); err != nil {
			return domain.StewardAgentRun{}, err
		}
		approved, err := runtimeRunApproved(ctx, tx, runID, planHash, now)
		if err != nil {
			return domain.StewardAgentRun{}, err
		}
		needsApproval, err := runtimeRunNeedsApproval(ctx, tx, runID)
		if err != nil {
			return domain.StewardAgentRun{}, err
		}
		if needsApproval && !approved {
			nextStatus = RuntimeRunAwaitingApproval
			message = "run resumed and awaits fresh approval"
		} else {
			nextStatus = RuntimeRunQueued
			message = "run resumed and queued"
		}
	default:
		return domain.StewardAgentRun{}, ErrAgentRunInvalidTransition
	}
	if nextStatus != status || action == "resume" {
		completedAt := any(nil)
		if nextStatus == RuntimeRunCancelled {
			completedAt = now
		}
		if _, err := tx.Exec(ctx, `
			update steward_agent_runs set status = $2, cancel_requested = false, failure_summary = '',
			       updated_at = $3, completed_at = $4 where id = $1
		`, runID, nextStatus, now, completedAt); err != nil {
			return domain.StewardAgentRun{}, err
		}
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run."+action, nextStatus, message, map[string]any{}); err != nil {
		return domain.StewardAgentRun{}, err
	}
	return s.commitAndGetAgentRun(ctx, tx, runID)
}

func (s *Service) ApproveAgentRun(ctx context.Context, runID string, input ApproveAgentRunInput) (domain.StewardAgentRun, error) {
	if err := s.runtimeEnabled(); err != nil {
		return domain.StewardAgentRun{}, err
	}
	input.PlanHash = strings.TrimSpace(input.PlanHash)
	input.GrantedBy = defaultString(strings.TrimSpace(input.GrantedBy), "local-user")
	input.Scope = defaultString(strings.TrimSpace(input.Scope), "run")
	if input.Scope != "run" {
		return domain.StewardAgentRun{}, fmt.Errorf("%w: R1 only supports approval scope=run", ErrAgentRunInvalid)
	}
	now := time.Now().UTC()
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return domain.StewardAgentRun{}, fmt.Errorf("%w: expires_at must be in the future", ErrAgentRunInvalid)
	}
	proofMetadata, err := s.validateRuntimeApprovalProof(ctx, runID, input.PlanHash, input.GrantedBy, strings.TrimSpace(input.Reason), input.ApprovalProof)
	if err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("%w: %v", ErrAgentRunInvalid, err)
	}
	if proofMetadata.Required && (input.ExpiresAt == nil || input.ExpiresAt.After(proofMetadata.ExpiresAt)) {
		expiresAt := proofMetadata.ExpiresAt
		input.ExpiresAt = &expiresAt
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardAgentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, planHash string
	if err := tx.QueryRow(ctx, `select status, plan_hash from steward_agent_runs where id = $1 for update`, runID).Scan(&status, &planHash); errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentRun{}, ErrAgentRunNotFound
	} else if err != nil {
		return domain.StewardAgentRun{}, err
	}
	if input.PlanHash == "" || input.PlanHash != planHash {
		return domain.StewardAgentRun{}, ErrAgentRunPlanHashMismatch
	}
	if runtimeRunTerminal(status) {
		return domain.StewardAgentRun{}, ErrAgentRunInvalidTransition
	}
	if _, err := tx.Exec(ctx, `
		insert into steward_approval_grants (
			id, run_id, plan_hash, scope, granted_by, status, reason, created_at, expires_at,
			approval_proof, approval_proof_id, approval_key_id, approval_proof_expires_at
		) values ($1,$2,$3,$4,$5,'active',$6,$7,$8,$9::jsonb,$10,$11,$12)
	`, uuid.NewString(), runID, planHash, input.Scope, input.GrantedBy, strings.TrimSpace(input.Reason), now, input.ExpiresAt,
		string(defaultJSON(proofMetadata.JSON)), proofMetadata.ProofID, proofMetadata.KeyID, nullableApprovalProofExpiry(proofMetadata)); err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("insert agent run approval: %w", err)
	}
	nextStatus := status
	if status == RuntimeRunAwaitingApproval {
		nextStatus = RuntimeRunQueued
		if _, err := tx.Exec(ctx, `update steward_agent_runs set status = $2, updated_at = $3 where id = $1`, runID, nextStatus, now); err != nil {
			return domain.StewardAgentRun{}, err
		}
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.approved", nextStatus, "approval granted for immutable plan hash", map[string]any{"plan_hash": planHash, "granted_by": input.GrantedBy}); err != nil {
		return domain.StewardAgentRun{}, err
	}
	return s.commitAndGetAgentRun(ctx, tx, runID)
}

func defaultJSON(payload []byte) []byte {
	if len(payload) == 0 {
		return []byte("{}")
	}
	return payload
}

func nullableApprovalProofExpiry(metadata runtimeApprovalProofMetadata) any {
	if !metadata.Required {
		return nil
	}
	return metadata.ExpiresAt
}

func (s *Service) commitAndGetAgentRun(ctx context.Context, tx pgx.Tx, runID string) (domain.StewardAgentRun, error) {
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardAgentRun{}, fmt.Errorf("commit agent run transition: %w", err)
	}
	return s.GetAgentRun(ctx, runID)
}

func runtimeRunNeedsApproval(ctx context.Context, tx pgx.Tx, runID string) (bool, error) {
	var result bool
	err := tx.QueryRow(ctx, `select exists(select 1 from steward_run_steps where run_id = $1 and requires_approval)`, runID).Scan(&result)
	return result, err
}

func runtimeRunApproved(ctx context.Context, tx pgx.Tx, runID string, planHash string, now time.Time) (bool, error) {
	var result bool
	err := tx.QueryRow(ctx, `
		select exists(
			select 1 from steward_approval_grants
			where run_id = $1 and plan_hash = $2 and status = 'active'
			  and (expires_at is null or expires_at > $3)
		)
	`, runID, planHash, now).Scan(&result)
	return result, err
}

func appendRuntimeEvent(ctx context.Context, tx pgx.Tx, runID string, stepID *string, eventType string, status string, message string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode runtime event payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into steward_run_events (id, run_id, step_id, type, status, message, payload, created_at)
		values ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)
	`, uuid.NewString(), runID, stepID, eventType, status, message, string(payloadJSON), time.Now().UTC()); err != nil {
		return fmt.Errorf("append runtime event: %w", err)
	}
	return nil
}

func decodeRuntimeMap(payload []byte) map[string]any {
	result := map[string]any{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &result)
	}
	return result
}

func validRuntimeRunStatus(status string) bool {
	switch status {
	case RuntimeRunDraft, RuntimeRunPlanning, RuntimeRunAwaitingApproval, RuntimeRunQueued,
		RuntimeRunRunning, RuntimeRunVerifying, RuntimeRunSucceeded, RuntimeRunFailed,
		RuntimeRunCancelled, RuntimeRunCompensating, RuntimeRunBlocked:
		return true
	default:
		return false
	}
}
