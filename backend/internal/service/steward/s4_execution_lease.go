package steward

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const autonomyProposalLockNamespace = "steward-autonomy-proposal:"

// autonomyExecutionLease uses a PostgreSQL session advisory lock so separate
// daemon/API processes sharing a database cannot execute or transition the same
// proposal concurrently.
type autonomyExecutionLease struct {
	conn *pgxpool.Conn
	key  string
}

func acquireAutonomyExecutionLease(ctx context.Context, pool *pgxpool.Pool, proposalID string) (*autonomyExecutionLease, error) {
	if pool == nil {
		return nil, fmt.Errorf("database pool is required for autonomy proposal lease")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire autonomy proposal lease connection: %w", err)
	}
	key := autonomyProposalLockNamespace + proposalID
	if _, err := conn.Exec(ctx, `select pg_advisory_lock(hashtextextended($1, 0))`, key); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire autonomy proposal lease: %w", err)
	}
	return &autonomyExecutionLease{conn: conn, key: key}, nil
}

func (l *autonomyExecutionLease) Release() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var unlocked bool
	if err := l.conn.QueryRow(ctx, `select pg_advisory_unlock(hashtextextended($1, 0))`, l.key).Scan(&unlocked); err != nil || !unlocked {
		if err == nil {
			err = fmt.Errorf("advisory lock was not held")
		}
		log.Printf("release autonomy proposal lease failed: %v", err)
		_ = l.conn.Conn().Close(ctx)
		l.conn.Release()
		l.conn = nil
		return
	}
	l.conn.Release()
	l.conn = nil
}
