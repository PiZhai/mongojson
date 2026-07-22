package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (db *DB) SaveManagementSession(ctx context.Context, tokenHash []byte, expiresAt time.Time) error {
	_, err := db.Pool.Exec(ctx, `
		insert into management_sessions (token_hash, expires_at)
		values ($1, $2)
		on conflict (token_hash) do update set expires_at = excluded.expires_at`, tokenHash, expiresAt.UTC())
	return err
}

func (db *DB) ManagementSessionExpiry(ctx context.Context, tokenHash []byte, now time.Time) (time.Time, bool, error) {
	var expiresAt time.Time
	err := db.Pool.QueryRow(ctx, `
		select expires_at from management_sessions
		where token_hash = $1 and expires_at > $2`, tokenHash, now.UTC()).Scan(&expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return expiresAt.UTC(), err == nil, err
}

func (db *DB) DeleteManagementSession(ctx context.Context, tokenHash []byte) error {
	_, err := db.Pool.Exec(ctx, `delete from management_sessions where token_hash = $1`, tokenHash)
	return err
}

func (db *DB) DeleteExpiredManagementSessions(ctx context.Context, now time.Time) error {
	_, err := db.Pool.Exec(ctx, `delete from management_sessions where expires_at <= $1`, now.UTC())
	return err
}
