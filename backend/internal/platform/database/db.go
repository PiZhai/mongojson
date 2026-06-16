package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func Connect(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

func (db *DB) Migrate(ctx context.Context) error {
	statements := []string{
		`create extension if not exists "pgcrypto";`,
		`create table if not exists tool_files (
			id uuid primary key,
			original_name text not null,
			stored_name text not null,
			storage_path text not null,
			mime_type text not null,
			size_bytes bigint not null,
			category text not null,
			expires_at timestamptz,
			created_at timestamptz not null default now()
		);`,
		`create table if not exists tool_jobs (
			id uuid primary key,
			tool_type text not null,
			status text not null,
			input_file_id uuid references tool_files(id) on delete set null,
			output_file_id uuid references tool_files(id) on delete set null,
			params jsonb not null default '{}'::jsonb,
			error_message text,
			created_at timestamptz not null default now(),
			finished_at timestamptz,
			expires_at timestamptz
		);`,
		`create table if not exists tool_runs (
			id uuid primary key,
			tool_type text not null,
			summary text not null,
			created_at timestamptz not null default now()
		);`,
		`create table if not exists tool_presets (
			id uuid primary key,
			tool_type text not null,
			name text not null,
			payload jsonb not null default '{}'::jsonb,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create index if not exists idx_tool_jobs_status on tool_jobs(status);`,
		`create index if not exists idx_tool_files_expires_at on tool_files(expires_at);`,
	}

	for _, statement := range statements {
		if _, err := db.Pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate statement: %w", err)
		}
	}

	return nil
}

func ScanNullableTime(row pgx.Row) (*string, error) {
	var value *string
	if err := row.Scan(&value); err != nil {
		return nil, err
	}
	return value, nil
}
