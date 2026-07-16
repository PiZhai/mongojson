package steward

import (
	"context"
	"fmt"
	"time"
)

// RunAgentRuntimeWatchdog is the only automatic recovery path in R2.6. It
// takes over work only after the invocation lease has expired; a second live
// daemon therefore cannot mistake another worker's active invocation for a
// crash merely because it has just started.
func (s *Service) RunAgentRuntimeWatchdog(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.runtimeV2 {
		return 0, nil
	}
	if err := s.runtimeEnabled(); err != nil {
		return 0, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select i.id::text, i.run_id::text, i.step_id::text, st.tool_idempotency
		from steward_tool_invocations i
		join steward_run_steps st on st.id = i.step_id
		join steward_agent_runs r on r.id = i.run_id
		where i.status = 'running'
		  and (i.lease_expires_at is null or i.lease_expires_at <= now())
		  and st.status in ('running','verifying')
		  and r.status in ('running','verifying')
		order by coalesce(i.lease_expires_at, i.started_at), i.started_at
		for update of i, st, r skip locked limit $1
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("scan expired runtime leases: %w", err)
	}
	type expiredInvocation struct {
		invocationID string
		runID        string
		stepID       string
		idempotency  string
	}
	expired := []expiredInvocation{}
	for rows.Next() {
		var item expiredInvocation
		if err := rows.Scan(&item.invocationID, &item.runID, &item.stepID, &item.idempotency); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	processed := 0
	for _, item := range expired {
		reason := "execution watchdog detected an expired worker lease"
		if item.idempotency == RuntimeIdempotencyNonIdempotent {
			reason = "execution watchdog detected an expired non-idempotent invocation; outcome is unknown and automatic replay is forbidden"
			if _, err := tx.Exec(ctx, `
				update steward_tool_invocations
				set status = $2, error_summary = $3, finished_at = $4, lease_expires_at = null
				where id = $1 and status = 'running'
			`, item.invocationID, RuntimeStepFailed, reason, now); err != nil {
				return processed, err
			}
			if _, err := tx.Exec(ctx, `
				update steward_run_steps set status = $2, last_error = $3, updated_at = $4, completed_at = $4
				where id = $1
			`, item.stepID, RuntimeStepBlocked, reason, now); err != nil {
				return processed, err
			}
			if _, err := tx.Exec(ctx, `
				update steward_approval_grants set status = 'revoked', revoked_at = $2
				where run_id = $1 and status = 'active'
			`, item.runID, now); err != nil {
				return processed, err
			}
			if _, err := tx.Exec(ctx, `
				update steward_agent_runs set status = $2, failure_summary = $3, updated_at = $4, completed_at = $4
				where id = $1
			`, item.runID, RuntimeRunBlocked, reason, now); err != nil {
				return processed, err
			}
			if err := appendRuntimeEvent(ctx, tx, item.runID, &item.stepID, "run.watchdog_blocked", RuntimeRunBlocked, reason, map[string]any{
				"tool_idempotency": item.idempotency, "requires_fresh_approval": true,
			}); err != nil {
				return processed, err
			}
			processed++
			continue
		}
		if _, err := tx.Exec(ctx, `
			update steward_tool_invocations
			set status = $2, error_summary = $3, finished_at = $4, lease_expires_at = null
			where id = $1 and status = 'running'
		`, item.invocationID, RuntimeStepFailed, reason+"; replay-safe work returned to the queue", now); err != nil {
			return processed, err
		}
		if _, err := tx.Exec(ctx, `
			update steward_run_steps
			set status = $2, max_attempts = greatest(max_attempts, attempt + 1),
			    last_error = $3, updated_at = $4, completed_at = null
			where id = $1
		`, item.stepID, RuntimeStepPending, reason, now); err != nil {
			return processed, err
		}
		if _, err := tx.Exec(ctx, `
			update steward_agent_runs
			set status = $2, failure_summary = '', updated_at = $3, completed_at = null
			where id = $1
		`, item.runID, RuntimeRunQueued, now); err != nil {
			return processed, err
		}
		if err := appendRuntimeEvent(ctx, tx, item.runID, &item.stepID, "run.watchdog_recovered", RuntimeRunQueued,
			"expired replay-safe invocation lease recovered to the durable queue", map[string]any{"tool_idempotency": item.idempotency}); err != nil {
			return processed, err
		}
		processed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return processed, nil
}
