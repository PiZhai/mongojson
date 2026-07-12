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

func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}
	return db.Pool.Ping(ctx)
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
		`create table if not exists tool_memos (
			id uuid primary key,
			slug text not null unique,
			title text not null,
			content_html text not null,
			content_text text not null,
			floating_cards jsonb not null default '[]'::jsonb,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`alter table tool_memos
			add column if not exists floating_cards jsonb not null default '[]'::jsonb;`,
		`create table if not exists music_tracks (
			id uuid primary key,
			file_id uuid not null unique references tool_files(id) on delete cascade,
			lyric_file_id uuid unique references tool_files(id) on delete set null,
			content_sha256 text,
			title text not null,
			artist text not null default '',
			note text not null default '',
			duration_seconds double precision,
			audio_quality jsonb not null default '{}'::jsonb,
			created_at timestamptz not null default now()
		);`,
		`alter table music_tracks add column if not exists lyric_file_id uuid unique references tool_files(id) on delete set null;`,
		`alter table music_tracks add column if not exists content_sha256 text;`,
		`create table if not exists canvas_boards (
			id uuid primary key,
			title text not null,
			scene_json jsonb not null default '{"elements":[],"appState":{},"files":{}}'::jsonb,
			revision bigint not null default 1,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists canvas_assets (
			id uuid primary key,
			board_id uuid not null references canvas_boards(id) on delete cascade,
			file_id uuid not null unique references tool_files(id) on delete cascade,
			canvas_file_id text not null,
			created_at timestamptz not null default now(),
			unique(board_id, canvas_file_id)
		);`,
		`create index if not exists idx_tool_jobs_status on tool_jobs(status);`,
		`create index if not exists idx_tool_files_expires_at on tool_files(expires_at);`,
		`create index if not exists idx_music_tracks_created_at_id on music_tracks(created_at desc, id desc);`,
		`create unique index if not exists idx_music_tracks_content_sha256 on music_tracks(content_sha256) where content_sha256 is not null;`,
		`create index if not exists idx_canvas_boards_updated_at on canvas_boards(updated_at desc, id desc);`,
		`create index if not exists idx_canvas_assets_board_id on canvas_assets(board_id);`,
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
