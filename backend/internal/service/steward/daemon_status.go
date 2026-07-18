package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

func (s *Service) configureDaemonLoop(ctx context.Context, name string, interval time.Duration, running bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("daemon loop name is required")
	}
	enabled := interval > 0
	if !enabled {
		running = false
	}
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_daemon_loop_status (
			agent_id, name, enabled, running, interval_text, updated_at
		)
		values ($1,$2,$3,$4,$5,$6)
		on conflict (agent_id, name) do update
		set enabled = excluded.enabled,
		    running = excluded.running,
		    interval_text = excluded.interval_text,
		    last_error = case when excluded.enabled then steward_daemon_loop_status.last_error else null end,
		    consecutive_failures = case when excluded.enabled then steward_daemon_loop_status.consecutive_failures else 0 end,
		    updated_at = excluded.updated_at
	`, s.agentIDValue(), name, enabled, running, interval.String(), now)
	if err != nil {
		return fmt.Errorf("configure daemon loop %s: %w", name, err)
	}
	return nil
}

func (s *Service) recordDaemonLoopResult(ctx context.Context, name string, startedAt time.Time, runErr error) error {
	now := time.Now().UTC()
	var errorSummary *string
	if runErr != nil {
		value := sanitizeDaemonLoopError(runErr)
		errorSummary = &value
	}
	_, err := s.db.Pool.Exec(ctx, `
		update steward_daemon_loop_status
		set last_started_at = $1,
		    last_completed_at = $2,
		    last_success_at = case when $3::text is null then $2 else last_success_at end,
		    last_error = $3,
		    consecutive_failures = case when $3::text is null then 0 else consecutive_failures + 1 end,
		    updated_at = $2
		where agent_id = $4 and name = $5
	`, startedAt.UTC(), now, errorSummary, s.agentIDValue(), strings.TrimSpace(name))
	if err != nil {
		return fmt.Errorf("record daemon loop %s result: %w", name, err)
	}
	return nil
}

func (s *Service) daemonLoopInitialDelay(ctx context.Context, name string, interval time.Duration, now time.Time) (time.Duration, error) {
	var lastCompletedAt *time.Time
	err := s.db.Pool.QueryRow(ctx, `
		select last_completed_at
		from steward_daemon_loop_status
		where agent_id = $1 and name = $2
	`, s.agentIDValue(), strings.TrimSpace(name)).Scan(&lastCompletedAt)
	if err != nil {
		return 0, fmt.Errorf("read daemon loop %s schedule: %w", name, err)
	}
	return persistedLoopDelay(lastCompletedAt, now, interval), nil
}

func persistedLoopDelay(lastCompletedAt *time.Time, now time.Time, interval time.Duration) time.Duration {
	if lastCompletedAt == nil || interval <= 0 {
		return 0
	}
	elapsed := now.Sub(lastCompletedAt.UTC())
	if elapsed >= interval {
		return 0
	}
	if elapsed < 0 {
		// A clock correction must not postpone the loop for longer than one
		// configured interval.
		return interval
	}
	return interval - elapsed
}

func sanitizeDaemonLoopError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, strings.TrimSpace(err.Error()))
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 500 {
		return string(runes[:500])
	}
	return value
}

func (s *Service) stopDaemonLoops(ctx context.Context) error {
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_daemon_loop_status
		set running = false, updated_at = $1
		where agent_id = $2
	`, now, s.agentIDValue()); err != nil {
		return fmt.Errorf("mark daemon loops stopped: %w", err)
	}
	return nil
}

func (s *Service) listDaemonLoopStatuses(ctx context.Context) ([]domain.StewardBackgroundLoopStatus, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select name, enabled, running, interval_text, last_started_at, last_completed_at,
		       last_success_at, last_error, consecutive_failures, updated_at
		from steward_daemon_loop_status
		where agent_id = $1
		order by case name when 'heartbeat' then 1 when 'runtime-v2' then 2 when 'sync' then 3 when 'autonomy' then 4 else 5 end, name
	`, s.agentIDValue())
	if err != nil {
		return nil, fmt.Errorf("list daemon loop statuses: %w", err)
	}
	defer rows.Close()
	statuses := []domain.StewardBackgroundLoopStatus{}
	for rows.Next() {
		var status domain.StewardBackgroundLoopStatus
		if err := rows.Scan(&status.Name, &status.Enabled, &status.Running, &status.Interval,
			&status.LastStartedAt, &status.LastCompletedAt, &status.LastSuccessAt, &status.LastError,
			&status.ConsecutiveFailures, &status.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan daemon loop status: %w", err)
		}
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}
