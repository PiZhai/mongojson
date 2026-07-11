package steward

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
)

const autonomyPolicyGateKey = "steward-autonomy-policy-gate"

type autonomyPolicyGateContextKey struct{}

type autonomyPolicyGateLease struct {
	conn   *pgxpool.Conn
	shared bool
}

func autonomyPolicyGateStatus() domain.StewardAutonomyPolicyGateStatus {
	return domain.StewardAutonomyPolicyGateStatus{
		Enabled:                 true,
		Backend:                 "postgres_advisory_rw",
		CycleReadBarrier:        true,
		ExecutionReadBarrier:    true,
		SettingsWriteBarrier:    true,
		RuleWriteBarrier:        true,
		CurrentRuleRevalidation: true,
	}
}

func acquireAutonomyPolicyReadGate(ctx context.Context, pool *pgxpool.Pool) (context.Context, *autonomyPolicyGateLease, error) {
	if held, _ := ctx.Value(autonomyPolicyGateContextKey{}).(bool); held {
		return ctx, nil, nil
	}
	lease, err := acquireAutonomyPolicyGate(ctx, pool, true)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, autonomyPolicyGateContextKey{}, true), lease, nil
}

func acquireAutonomyPolicyWriteGate(ctx context.Context, pool *pgxpool.Pool) (*autonomyPolicyGateLease, error) {
	return acquireAutonomyPolicyGate(ctx, pool, false)
}

func acquireAutonomyPolicyGate(ctx context.Context, pool *pgxpool.Pool, shared bool) (*autonomyPolicyGateLease, error) {
	if pool == nil {
		return nil, fmt.Errorf("database pool is required for autonomy policy gate")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire autonomy policy gate connection: %w", err)
	}
	lockFunction := "pg_advisory_lock"
	if shared {
		lockFunction = "pg_advisory_lock_shared"
	}
	if _, err := conn.Exec(ctx, "select "+lockFunction+"(hashtextextended($1, 0))", autonomyPolicyGateKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire autonomy policy gate: %w", err)
	}
	return &autonomyPolicyGateLease{conn: conn, shared: shared}, nil
}

func (l *autonomyPolicyGateLease) Release() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	unlockFunction := "pg_advisory_unlock"
	if l.shared {
		unlockFunction = "pg_advisory_unlock_shared"
	}
	var unlocked bool
	if err := l.conn.QueryRow(ctx, "select "+unlockFunction+"(hashtextextended($1, 0))", autonomyPolicyGateKey).Scan(&unlocked); err != nil || !unlocked {
		if err == nil {
			err = fmt.Errorf("advisory lock was not held")
		}
		log.Printf("release autonomy policy gate failed: %v", err)
		_ = l.conn.Conn().Close(ctx)
		l.conn.Release()
		l.conn = nil
		return
	}
	l.conn.Release()
	l.conn = nil
}
