package steward

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

func (s *Service) GetOrchestrationAgent(ctx context.Context, id string) (domain.StewardOrchestrationAgent, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardOrchestrationAgent{}, err
	}
	return s.getOrchestrationAgent(ctx, strings.ToLower(strings.TrimSpace(id)))
}

func (s *Service) RegisterAgentWorker(ctx context.Context, agentID, workerID string, processID int) (domain.StewardAgentWorkerStatus, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardAgentWorkerStatus{}, err
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	workerID = strings.TrimSpace(workerID)
	if !runtimeStepKeyPattern.MatchString(agentID) || workerID == "" || len(workerID) > 160 {
		return domain.StewardAgentWorkerStatus{}, fmt.Errorf("%w: invalid Agent or worker id", ErrOrchestrationInvalid)
	}
	agent, err := s.getOrchestrationAgent(ctx, agentID)
	if err != nil {
		return domain.StewardAgentWorkerStatus{}, err
	}
	if !agent.Enabled {
		return domain.StewardAgentWorkerStatus{}, fmt.Errorf("%w: Agent is disabled", ErrOrchestrationInvalid)
	}
	if processID <= 0 {
		processID = os.Getpid()
	}
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_agent_workers (
			worker_id, agent_id, status, process_id, started_at, heartbeat_at
		) values ($1,$2,'running',$3,$4,$4)
		on conflict (worker_id) do update set
			agent_id=excluded.agent_id, status='running', process_id=excluded.process_id,
			current_message_id=null, started_at=excluded.started_at,
			heartbeat_at=excluded.heartbeat_at, stopped_at=null
	`, workerID, agentID, processID, now)
	if err != nil {
		return domain.StewardAgentWorkerStatus{}, fmt.Errorf("register Agent worker: %w", err)
	}
	return s.getAgentWorkerStatus(ctx, workerID)
}

func (s *Service) HeartbeatAgentWorker(ctx context.Context, workerID string) error {
	command, err := s.db.Pool.Exec(ctx, `
		update steward_agent_workers set heartbeat_at=now()
		where worker_id=$1 and status='running'
	`, workerID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("Agent worker is not registered or running")
	}
	return nil
}

func (s *Service) StopAgentWorker(ctx context.Context, workerID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		update steward_agent_workers set status='stopped', current_message_id=null,
		       heartbeat_at=now(), stopped_at=now() where worker_id=$1
	`, workerID)
	return err
}

func (s *Service) getAgentWorkerStatus(ctx context.Context, workerID string) (domain.StewardAgentWorkerStatus, error) {
	var item domain.StewardAgentWorkerStatus
	err := s.db.Pool.QueryRow(ctx, `
		select worker_id, agent_id, status, process_id, coalesce(current_message_id::text,''),
		       started_at, heartbeat_at, stopped_at
		from steward_agent_workers where worker_id=$1
	`, workerID).Scan(&item.WorkerID, &item.AgentID, &item.Status, &item.ProcessID,
		&item.CurrentMessage, &item.StartedAt, &item.HeartbeatAt, &item.StoppedAt)
	return item, err
}

func (s *Service) listOrchestrationWorkers(ctx context.Context, orchestrationID string) ([]domain.StewardAgentWorkerStatus, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select distinct worker.worker_id, worker.agent_id, worker.status, worker.process_id,
		       coalesce(worker.current_message_id::text,''), worker.started_at,
		       worker.heartbeat_at, worker.stopped_at
		from steward_agent_workers worker
		join steward_orchestration_nodes node on node.agent_id=worker.agent_id
		where node.orchestration_id=$1 order by worker.agent_id, worker.worker_id
	`, orchestrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardAgentWorkerStatus{}
	for rows.Next() {
		var item domain.StewardAgentWorkerStatus
		if err := rows.Scan(&item.WorkerID, &item.AgentID, &item.Status, &item.ProcessID,
			&item.CurrentMessage, &item.StartedAt, &item.HeartbeatAt, &item.StoppedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listOrchestrationMessages(ctx context.Context, orchestrationID string) ([]domain.StewardAgentMessage, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, agent_id, orchestration_id::text, node_id::text, runtime_run_id::text,
		       type, status, payload, attempt, max_attempts, lease_owner, lease_expires_at,
		       available_at, last_error, created_at, updated_at, acknowledged_at
		from steward_agent_messages where orchestration_id=$1 order by created_at, id
	`, orchestrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardAgentMessage{}
	for rows.Next() {
		var item domain.StewardAgentMessage
		var payload []byte
		if err := rows.Scan(&item.ID, &item.AgentID, &item.OrchestrationID, &item.NodeID,
			&item.RuntimeRunID, &item.Type, &item.Status, &payload, &item.Attempt,
			&item.MaxAttempts, &item.LeaseOwner, &item.LeaseExpiresAt, &item.AvailableAt,
			&item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.AcknowledgedAt); err != nil {
			return nil, err
		}
		item.Payload = decodeRuntimeMap(payload)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ClaimAgentMessage(ctx context.Context, agentID, workerID string) (domain.StewardAgentMessage, bool, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	workerID = strings.TrimSpace(workerID)
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	// Expired mailbox leases are safe to redeliver. Invocation-level replay
	// safety remains the Runtime V2 watchdog's responsibility.
	if _, err := tx.Exec(ctx, `
		update steward_agent_workers worker
		set status='stopped', current_message_id=null, heartbeat_at=$2, stopped_at=$2
		from steward_agent_messages message
		where worker.current_message_id=message.id and worker.agent_id=$1
		  and worker.status='running' and message.status='leased'
		  and message.lease_expires_at <= $2
	`, agentID, now); err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_messages set status='pending', lease_owner='', lease_expires_at=null,
		       available_at=$2, last_error='worker lease expired; message redelivered', updated_at=$2
		where agent_id=$1 and status='leased' and lease_expires_at <= $2 and attempt < max_attempts
	`, agentID, now); err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	deadRows, err := tx.Query(ctx, `
		select message.runtime_run_id::text
		from steward_agent_messages message
		join steward_orchestrations parent on parent.id=message.orchestration_id
		where message.agent_id=$1 and message.status='dead'
		  and message.last_error='worker lease retry budget exhausted'
		  and parent.status in ('queued','running','compensating')
		for update of message
	`, agentID)
	if err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	deadRunIDs := []string{}
	for deadRows.Next() {
		var runID string
		if err := deadRows.Scan(&runID); err != nil {
			deadRows.Close()
			return domain.StewardAgentMessage{}, false, err
		}
		deadRunIDs = append(deadRunIDs, runID)
	}
	deadRows.Close()
	for _, runID := range deadRunIDs {
		if err := s.blockInvalidDelegatedRunTx(ctx, tx, runID, fmt.Errorf("Agent mailbox retry budget exhausted"), now); err != nil {
			return domain.StewardAgentMessage{}, false, err
		}
	}
	if _, err := tx.Exec(ctx, `
		update steward_agent_messages set status='dead', lease_owner='', lease_expires_at=null,
		       last_error='worker lease retry budget exhausted', updated_at=$2
		where agent_id=$1 and status='leased' and lease_expires_at <= $2 and attempt >= max_attempts
	`, agentID, now); err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	var maxConcurrency, active int
	if err := tx.QueryRow(ctx, `
		select agent.max_concurrency,
		       count(message.id) filter (where message.status='leased' and message.lease_expires_at > $2)::int
		from steward_orchestration_agents agent
		left join steward_agent_messages message on message.agent_id=agent.id
		where agent.id=$1 and agent.enabled group by agent.id
	`, agentID, now).Scan(&maxConcurrency, &active); errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentMessage{}, false, ErrOrchestrationAgentNotFound
	} else if err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	if active >= maxConcurrency {
		return domain.StewardAgentMessage{}, false, tx.Commit(ctx)
	}
	var message domain.StewardAgentMessage
	var payload []byte
	err = tx.QueryRow(ctx, `
		select message.id::text, message.agent_id, message.orchestration_id::text,
		       message.node_id::text, message.runtime_run_id::text, message.type,
		       message.status, message.payload, message.attempt, message.max_attempts,
		       message.available_at, message.last_error, message.created_at, message.updated_at
		from steward_agent_messages message
		join steward_orchestrations parent on parent.id=message.orchestration_id
		join steward_orchestration_nodes node on node.id=message.node_id
		join steward_agent_runs run on run.id=message.runtime_run_id
		join steward_runtime_execution_control control on control.id='global'
		where message.agent_id=$1 and message.status='pending' and message.available_at <= $2
		  and parent.status in ('queued','running','compensating') and node.status in ('dispatched','running')
		  and run.status in ('draft','queued','running','verifying') and control.paused=false
		order by message.available_at, message.created_at
		for update of message skip locked limit 1
	`, agentID, now).Scan(&message.ID, &message.AgentID, &message.OrchestrationID,
		&message.NodeID, &message.RuntimeRunID, &message.Type, &message.Status, &payload,
		&message.Attempt, &message.MaxAttempts, &message.AvailableAt, &message.LastError,
		&message.CreatedAt, &message.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentMessage{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	message.Payload = decodeRuntimeMap(payload)
	message.Status = "leased"
	message.Attempt++
	expires := now.Add(s.orchestrationMessageLease)
	message.LeaseOwner = workerID
	message.LeaseExpiresAt = &expires
	message.UpdatedAt = now
	if _, err := tx.Exec(ctx, `
		update steward_agent_messages set status='leased', attempt=attempt+1,
		       lease_owner=$2, lease_expires_at=$3, updated_at=$4 where id=$1
	`, message.ID, workerID, expires, now); err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	command, err := tx.Exec(ctx, `
		update steward_agent_workers set current_message_id=$2, heartbeat_at=$3
		where worker_id=$1 and agent_id=$4 and status='running'
	`, workerID, message.ID, now, agentID)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("worker is not registered for Agent %s", agentID)
		}
		return domain.StewardAgentMessage{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardAgentMessage{}, false, err
	}
	return message, true, nil
}

func (s *Service) HeartbeatAgentMessage(ctx context.Context, messageID, workerID string) error {
	expires := time.Now().UTC().Add(s.orchestrationMessageLease)
	command, err := s.db.Pool.Exec(ctx, `
		update steward_agent_messages set lease_expires_at=$3, updated_at=now()
		where id=$1 and status='leased' and lease_owner=$2 and lease_expires_at > now()
	`, messageID, workerID, expires)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("Agent message lease was lost")
	}
	return s.HeartbeatAgentWorker(ctx, workerID)
}

func (s *Service) ExecuteAgentMessage(ctx context.Context, message domain.StewardAgentMessage, workerID string) error {
	if message.Type != "execute" || message.LeaseOwner != workerID {
		return fmt.Errorf("invalid or unowned Agent message")
	}
	_, _ = s.RunAgentRuntimeWatchdog(ctx, 20)
	if err := s.recoverWorkerRunBetweenSteps(ctx, message.RuntimeRunID); err != nil {
		return s.nackAgentMessage(ctx, message, workerID, err)
	}
	if err := s.claimDelegatedRunForWorker(ctx, message, workerID); err != nil {
		return s.nackAgentMessage(ctx, message, workerID, err)
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		interval := s.orchestrationMessageLease / 3
		if interval < 200*time.Millisecond {
			interval = 200 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-execCtx.Done():
				return
			case <-ticker.C:
				if err := s.HeartbeatAgentMessage(execCtx, message.ID, workerID); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		if err := s.executeAgentRun(execCtx, message.RuntimeRunID); err != nil {
			cancel()
			<-heartbeatDone
			return s.nackAgentMessage(ctx, message, workerID, err)
		}
		run, err := s.GetAgentRun(ctx, message.RuntimeRunID)
		if err != nil {
			cancel()
			<-heartbeatDone
			return s.nackAgentMessage(ctx, message, workerID, err)
		}
		if run.Status == RuntimeRunQueued {
			if err := s.claimDelegatedRunForWorker(ctx, message, workerID); err != nil {
				cancel()
				<-heartbeatDone
				return s.nackAgentMessage(ctx, message, workerID, err)
			}
			continue
		}
		cancel()
		<-heartbeatDone
		return s.ackAgentMessage(ctx, message.ID, workerID, run.Status, run.FailureSummary)
	}
}

func (s *Service) recoverWorkerRunBetweenSteps(ctx context.Context, runID string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `select status from steward_agent_runs where id=$1 for update`, runID).Scan(&status); err != nil {
		return err
	}
	if status != RuntimeRunRunning && status != RuntimeRunVerifying {
		return tx.Commit(ctx)
	}
	var active int
	if err := tx.QueryRow(ctx, `
		select count(*)::int from steward_tool_invocations
		where run_id=$1 and status='running' and lease_expires_at > now()
	`, runID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return fmt.Errorf("previous worker invocation lease is still active")
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		update steward_agent_runs set status=$2, updated_at=$3 where id=$1
	`, runID, RuntimeRunQueued, now); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.worker_recovered", RuntimeRunQueued,
		"replacement Agent worker recovered a run between Runtime V2 steps", map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) claimDelegatedRunForWorker(ctx context.Context, message domain.StewardAgentMessage, workerID string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, leaseOwner string
	var leaseExpires time.Time
	err = tx.QueryRow(ctx, `
		select run.status, message.lease_owner, message.lease_expires_at
		from steward_agent_runs run
		join steward_agent_messages message on message.runtime_run_id=run.id
		where run.id=$1 and message.id=$2 for update of run, message
	`, message.RuntimeRunID, message.ID).Scan(&status, &leaseOwner, &leaseExpires)
	if err != nil {
		return err
	}
	if leaseOwner != workerID || !leaseExpires.After(time.Now().UTC()) {
		return fmt.Errorf("Agent message lease is not active")
	}
	if runtimeRunTerminal(status) {
		return tx.Commit(ctx)
	}
	if status != RuntimeRunDraft && status != RuntimeRunQueued {
		return fmt.Errorf("child run is not claimable after crash recovery: %s", status)
	}
	now := time.Now().UTC()
	if err := s.verifyRuntimeOrchestrationDelegationTx(ctx, tx, message.RuntimeRunID, now); err != nil {
		if blockErr := s.blockInvalidDelegatedRunTx(ctx, tx, message.RuntimeRunID, err, now); blockErr != nil {
			return blockErr
		}
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `
		update steward_agent_runs set status=$2, started_at=coalesce(started_at,$3), updated_at=$3
		where id=$1
	`, message.RuntimeRunID, RuntimeRunRunning, now)
	if err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, message.RuntimeRunID, nil, "run.worker_claimed", RuntimeRunRunning,
		"independent Agent worker claimed its task-bound message", map[string]any{"worker_id": workerID, "message_id": message.ID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ackAgentMessage(ctx context.Context, messageID, workerID, runStatus, summary string) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		update steward_agent_messages set status='acknowledged', lease_owner='', lease_expires_at=null,
		       last_error=$3, updated_at=$4, acknowledged_at=$4
		where id=$1 and status='leased' and lease_owner=$2
	`, messageID, workerID, defaultString(summary, runStatus), now)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("Agent message ACK lost its lease")
		}
		return err
	}
	_, err = tx.Exec(ctx, `update steward_agent_workers set current_message_id=null, heartbeat_at=$2 where worker_id=$1`, workerID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) nackAgentMessage(ctx context.Context, message domain.StewardAgentMessage, workerID string, cause error) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	summary := sanitizeRuntimeError(cause)
	if message.Attempt >= message.MaxAttempts {
		command, err := tx.Exec(ctx, `
			update steward_agent_messages set status='dead', lease_owner='', lease_expires_at=null,
			       last_error=$3, updated_at=$4
			where id=$1 and status='leased' and lease_owner=$2
		`, message.ID, workerID, summary, now)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return fmt.Errorf("Agent message NACK lost its lease")
		}
		if err := s.blockInvalidDelegatedRunTx(ctx, tx, message.RuntimeRunID, fmt.Errorf("Agent message delivery exhausted: %s", summary), now); err != nil {
			return err
		}
	} else {
		command, err := tx.Exec(ctx, `
			update steward_agent_messages set status='pending', lease_owner='', lease_expires_at=null,
			       available_at=$3, last_error=$4, updated_at=$2
			where id=$1 and status='leased' and lease_owner=$5
		`, message.ID, now, now.Add(time.Duration(message.Attempt)*time.Second), summary, workerID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return fmt.Errorf("Agent message NACK lost its lease")
		}
	}
	_, err = tx.Exec(ctx, `update steward_agent_workers set current_message_id=null, heartbeat_at=$2 where worker_id=$1`, workerID, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return cause
}
