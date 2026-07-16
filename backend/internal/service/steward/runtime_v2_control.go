package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

type SetRuntimeExecutionControlInput struct {
	Reason    string `json:"reason"`
	ChangedBy string `json:"changed_by"`
}

const executionControlGateKey = "steward-execution-control-gate"

func (s *Service) GetRuntimeExecutionControl(ctx context.Context) (domain.StewardRuntimeExecutionControl, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("execution control database is not configured")
	}
	var control domain.StewardRuntimeExecutionControl
	err := s.db.Pool.QueryRow(ctx, `
		select paused, generation, reason, changed_by, changed_at
		from steward_runtime_execution_control where id = 'global'
	`).Scan(&control.Paused, &control.Generation, &control.Reason, &control.ChangedBy, &control.ChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("runtime execution control is not initialized")
	}
	if err != nil {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("get runtime execution control: %w", err)
	}
	control.Stopped = control.Paused
	control.Scopes = []string{"runtime_v2", "s4_autonomy"}
	if s.runtimeR3 {
		control.Scopes = append(control.Scopes, "privilege_broker")
	}
	control.Watchdog = domain.StewardRuntimeWatchdogStatus{
		Enabled: true, LeaseTTLSeconds: int(s.runtimeLeaseTTL.Seconds()),
	}
	if control.Watchdog.LeaseTTLSeconds < 1 {
		control.Watchdog.LeaseTTLSeconds = 1
	}
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*)::int,
		       count(*) filter (where lease_expires_at is null or lease_expires_at <= now())::int
		from steward_tool_invocations where status = 'running'
	`).Scan(&control.Watchdog.ActiveInvocations, &control.Watchdog.StaleInvocations); err != nil {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("get runtime watchdog status: %w", err)
	}
	control.Broker = s.privilegeBrokerStatus(ctx)
	independentResumePrepared := control.Stopped && control.Broker.Configured && control.Broker.Reachable &&
		!control.Broker.Stopped && control.Broker.Generation == control.Generation+1
	if control.Broker.Configured && control.Broker.Reachable && !independentResumePrepared &&
		(control.Broker.Generation != control.Generation || control.Broker.Stopped != control.Stopped) {
		control.Broker.Error = fmt.Sprintf("broker state differs from unified control: stopped=%t generation=%d", control.Stopped, control.Generation)
	}
	control.Draining = control.Stopped && (control.Watchdog.ActiveInvocations > 0 || control.Broker.ActiveExecutions > 0)
	rows, err := s.db.Pool.Query(ctx, `
		select sequence, action, reason, changed_by, created_at
		from steward_runtime_control_events order by sequence desc limit 20
	`)
	if err != nil {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("list runtime control events: %w", err)
	}
	defer rows.Close()
	control.Events = []domain.StewardRuntimeControlEvent{}
	for rows.Next() {
		var event domain.StewardRuntimeControlEvent
		if err := rows.Scan(&event.Sequence, &event.Action, &event.Reason, &event.ChangedBy, &event.CreatedAt); err != nil {
			return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("scan runtime control event: %w", err)
		}
		control.Events = append(control.Events, event)
	}
	if err := rows.Err(); err != nil {
		return domain.StewardRuntimeExecutionControl{}, err
	}
	return control, nil
}

func (s *Service) PauseRuntimeExecution(ctx context.Context, input SetRuntimeExecutionControlInput) (domain.StewardRuntimeExecutionControl, error) {
	return s.setRuntimeExecutionPaused(ctx, true, input)
}

func (s *Service) ResumeRuntimeExecution(ctx context.Context, input SetRuntimeExecutionControlInput) (domain.StewardRuntimeExecutionControl, error) {
	return s.setRuntimeExecutionPaused(ctx, false, input)
}

func (s *Service) setRuntimeExecutionPaused(ctx context.Context, paused bool, input SetRuntimeExecutionControlInput) (domain.StewardRuntimeExecutionControl, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("execution control database is not configured")
	}
	input.ChangedBy = defaultString(strings.TrimSpace(input.ChangedBy), "local-user")
	input.Reason = strings.TrimSpace(input.Reason)
	if len([]rune(input.ChangedBy)) > 200 || len([]rune(input.Reason)) > 1000 {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("%w: runtime control changed_by or reason is too long", ErrAgentRunInvalid)
	}
	if input.Reason == "" {
		if paused {
			input.Reason = "user requested a system-wide execution emergency stop"
		} else {
			input.Reason = "user resumed system-wide execution"
		}
	}
	gate, err := acquireExecutionControlGate(ctx, s.db.Pool)
	if err != nil {
		return domain.StewardRuntimeExecutionControl{}, err
	}
	defer gate.Release()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardRuntimeExecutionControl{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current bool
	var currentGeneration int64
	if err := tx.QueryRow(ctx, `select paused, generation from steward_runtime_execution_control where id = 'global' for update`).Scan(&current, &currentGeneration); err != nil {
		return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("lock runtime execution control: %w", err)
	}
	changed := current != paused
	if changed && !paused {
		if err := s.requireIndependentlyResumedBroker(ctx, currentGeneration+1); err != nil {
			return domain.StewardRuntimeExecutionControl{}, err
		}
	}
	now := time.Now().UTC()
	if changed {
		if _, err := tx.Exec(ctx, `
		update steward_runtime_execution_control
		set paused = $1, generation = generation + 1, reason = $2, changed_by = $3, changed_at = $4
		where id = 'global'
	`, paused, input.Reason, input.ChangedBy, now); err != nil {
			return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("update execution control: %w", err)
		}
		action := "resumed"
		if paused {
			action = "stopped"
		}
		if _, err := tx.Exec(ctx, `
		insert into steward_runtime_control_events (action, reason, changed_by, created_at)
		values ($1,$2,$3,$4)
	`, action, input.Reason, input.ChangedBy, now); err != nil {
			return domain.StewardRuntimeExecutionControl{}, fmt.Errorf("append execution control event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardRuntimeExecutionControl{}, err
	}
	var brokerSyncErr error
	if s.runtimeR3 {
		if s.privilegeBroker == nil || s.privilegeBrokerError != nil {
			brokerSyncErr = fmt.Errorf("privilege Broker control synchronization is unavailable")
			if s.privilegeBrokerError != nil {
				brokerSyncErr = fmt.Errorf("privilege Broker control synchronization: %w", s.privilegeBrokerError)
			}
		} else {
			brokerSyncErr = s.syncPrivilegeBrokerControl(ctx, input.ChangedBy)
		}
	}
	if paused {
		// The durable state changes first so every process stops admitting new
		// work. Local contexts are cancelled immediately; the S4 write barrier
		// then waits until any in-flight autonomous action has left its read gate.
		s.cancelRuntimeContexts()
		if s.orchestrationRemote {
			_, _ = s.db.Pool.Exec(ctx, `
				update steward_remote_dispatches set cancel_requested=true,
				       last_error='origin device emergency stop requested remote cancellation',
				       available_at=now(), updated_at=now()
				where status in ('pending','sent','accepted','running')
			`)
		}
		policyGate, gateErr := acquireAutonomyPolicyWriteGate(ctx, s.db.Pool)
		if gateErr != nil {
			return domain.StewardRuntimeExecutionControl{}, gateErr
		}
		policyGate.Release()
		s.waitForRuntimeInvocationDrain(ctx, 3*time.Second)
	}
	control, controlErr := s.GetRuntimeExecutionControl(ctx)
	if controlErr != nil {
		return control, controlErr
	}
	if brokerSyncErr != nil {
		return control, fmt.Errorf("local execution is fail-closed but Broker control synchronization failed: %w", brokerSyncErr)
	}
	return control, nil
}

func (s *Service) requireIndependentlyResumedBroker(ctx context.Context, expectedGeneration int64) error {
	if s == nil || !s.runtimeR3 {
		return nil
	}
	if s.privilegeBrokerError != nil {
		return fmt.Errorf("independent Broker resume is unavailable: %w", s.privilegeBrokerError)
	}
	if s.privilegeBroker == nil {
		return fmt.Errorf("independent Broker resume is required before local execution can resume")
	}
	brokerCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := s.privilegeBroker.Status(brokerCtx)
	if err != nil {
		return fmt.Errorf("verify independent Broker resume: %w", err)
	}
	if status.Stopped || status.Generation != expectedGeneration {
		return fmt.Errorf("independent Broker resume required: resume Broker generation %d with the control authority before resuming Runtime V2/S4 (broker stopped=%t generation=%d)", expectedGeneration, status.Stopped, status.Generation)
	}
	return nil
}

func (s *Service) syncPrivilegeBrokerControl(ctx context.Context, changedBy string) error {
	if s == nil || !s.runtimeR3 || s.privilegeBroker == nil {
		return nil
	}
	stopped, generation, err := s.runtimeExecutionState(ctx)
	if err != nil {
		return err
	}
	var reason string
	if err := s.db.Pool.QueryRow(ctx, `select reason from steward_runtime_execution_control where id = 'global'`).Scan(&reason); err != nil {
		return err
	}
	brokerCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := s.privilegeBroker.Status(brokerCtx)
	if err != nil {
		return err
	}
	if status.Stopped == stopped && status.Generation == generation {
		return nil
	}
	if !stopped {
		return fmt.Errorf("Broker resume requires the independent control authority; expected resumed generation %d", generation)
	}
	_, err = s.privilegeBroker.SetControl(brokerCtx, stopped, privilegebroker.ControlRequest{
		Generation: generation, Reason: defaultString(strings.TrimSpace(reason), "synchronize unified execution control"),
		ChangedBy: defaultString(strings.TrimSpace(changedBy), "steward-control"),
	})
	return err
}

func (s *Service) runtimeExecutionPaused(ctx context.Context) (bool, error) {
	paused, _, err := s.runtimeExecutionState(ctx)
	return paused, err
}

func (s *Service) runtimeExecutionState(ctx context.Context) (bool, int64, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return false, 0, ErrRuntimeV2Disabled
	}
	var paused bool
	var generation int64
	if err := s.db.Pool.QueryRow(ctx, `select paused, generation from steward_runtime_execution_control where id = 'global'`).Scan(&paused, &generation); err != nil {
		return false, 0, fmt.Errorf("read execution emergency stop: %w", err)
	}
	if !paused && s.runtimeR3 {
		if s.privilegeBrokerError != nil {
			return true, generation, fmt.Errorf("privilege Broker is not safely configured: %w", s.privilegeBrokerError)
		}
		if s.privilegeBroker == nil {
			return true, generation, fmt.Errorf("privilege Broker status is unavailable")
		}
		brokerCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		status, err := s.privilegeBroker.Status(brokerCtx)
		if err != nil {
			return true, generation, fmt.Errorf("verify privilege Broker control state: %w", err)
		}
		if status.Stopped || status.Generation != generation {
			return true, generation, fmt.Errorf("privilege Broker control state is not synchronized: broker stopped=%t generation=%d, local generation=%d", status.Stopped, status.Generation, generation)
		}
	}
	return paused, generation, nil
}

type executionControlGate struct{ conn *pgxpool.Conn }

func acquireExecutionControlGate(ctx context.Context, pool *pgxpool.Pool) (*executionControlGate, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire execution control connection: %w", err)
	}
	if _, err := conn.Exec(ctx, `select pg_advisory_lock(hashtextextended($1, 0))`, executionControlGateKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire execution control gate: %w", err)
	}
	return &executionControlGate{conn: conn}, nil
}

func (g *executionControlGate) Release() {
	if g == nil || g.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var unlocked bool
	if err := g.conn.QueryRow(ctx, `select pg_advisory_unlock(hashtextextended($1, 0))`, executionControlGateKey).Scan(&unlocked); err != nil || !unlocked {
		_ = g.conn.Conn().Close(ctx)
	}
	g.conn.Release()
	g.conn = nil
}

func (s *Service) cancelRuntimeContexts() {
	s.runtimeCancelMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.runtimeCancels))
	for _, cancel := range s.runtimeCancels {
		cancels = append(cancels, cancel)
	}
	s.runtimeCancelMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Service) executionGuardContext(parent context.Context, key string, generation int64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	s.runtimeCancelMu.Lock()
	s.runtimeCancels[key] = cancel
	s.runtimeCancelMu.Unlock()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stopped, currentGeneration, err := s.runtimeExecutionState(ctx)
				if err != nil || stopped || currentGeneration != generation {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		s.runtimeCancelMu.Lock()
		delete(s.runtimeCancels, key)
		s.runtimeCancelMu.Unlock()
	}
}

func (s *Service) waitForRuntimeInvocationDrain(ctx context.Context, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active int
		if err := s.db.Pool.QueryRow(ctx, `select count(*)::int from steward_tool_invocations where status = 'running'`).Scan(&active); err != nil || active == 0 {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
