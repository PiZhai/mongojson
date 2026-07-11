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
		`create index if not exists idx_tool_jobs_status on tool_jobs(status);`,
		`create index if not exists idx_tool_files_expires_at on tool_files(expires_at);`,
		`create table if not exists steward_agent_status (
			agent_id text primary key,
			device_name text not null,
			platform text not null,
			status text not null,
			version text not null,
			enabled_collectors jsonb not null default '[]'::jsonb,
			started_at timestamptz,
			last_heartbeat_at timestamptz,
			last_error text,
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_audit_logs (
			id uuid primary key,
			occurred_at timestamptz not null default now(),
			actor text not null,
			action text not null,
			target_type text not null,
			target_id uuid,
			source text not null,
			permission_level text not null,
			data_level text not null,
			input_summary text not null default '',
			output_summary text not null default '',
			result_status text not null,
			error_summary text
		);`,
		`create table if not exists steward_collector_configs (
			id uuid primary key,
			name text not null unique,
			enabled boolean not null default false,
			scope_summary text not null default '',
			last_run_at timestamptz,
			last_error text,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			audit_id uuid references steward_audit_logs(id) on delete set null
		);`,
		`create table if not exists steward_events (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			source text not null,
			data_level text not null,
			status text not null,
			device_id text not null,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			deleted_at timestamptz
		);`,
		`create table if not exists steward_tasks (
			id uuid primary key,
			title text not null,
			description text not null default '',
			status text not null,
			priority text not null,
			due_at timestamptz,
			source text not null,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			completed_at timestamptz,
			canceled_at timestamptz
		);`,
		`alter table steward_audit_logs
			add column if not exists before_summary text not null default '',
			add column if not exists after_summary text not null default '',
			add column if not exists reason text not null default '',
			add column if not exists user_confirmed boolean not null default true,
			add column if not exists syncable boolean not null default true,
			add column if not exists version integer not null default 1,
			add column if not exists device_id text not null default 'local-s1';`,
		`alter table steward_events
			add column if not exists permission_level text not null default 'A3',
			add column if not exists user_confirmed boolean not null default true,
			add column if not exists version integer not null default 1;`,
		`alter table steward_tasks
			add column if not exists type text not null default 'manual',
			add column if not exists data_level text not null default 'D0',
			add column if not exists permission_level text not null default 'A3',
			add column if not exists device_id text not null default 'local-s1',
			add column if not exists risk_level text not null default 'low',
			add column if not exists user_confirmed boolean not null default true,
			add column if not exists version integer not null default 1,
			add column if not exists deleted_at timestamptz;`,
		`create table if not exists steward_timeline_segments (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			status text not null,
			source text not null,
			data_level text not null,
			permission_level text not null,
			device_id text not null,
			start_at timestamptz,
			end_at timestamptz,
			confidence double precision not null default 1,
			user_confirmed boolean not null default true,
			version integer not null default 1,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			deleted_at timestamptz
		);`,
		`create table if not exists steward_timeline_segment_events (
			segment_id uuid not null references steward_timeline_segments(id) on delete cascade,
			event_id uuid not null references steward_events(id) on delete cascade,
			created_at timestamptz not null default now(),
			primary key (segment_id, event_id)
		);`,
		`create table if not exists steward_timeline_pending_events (
			segment_id uuid not null references steward_timeline_segments(id) on delete cascade,
			event_id uuid not null,
			origin_device_id text not null default '',
			created_at timestamptz not null default now(),
			primary key (segment_id, event_id)
		);`,
		`create table if not exists steward_intents (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			reason text not null default '',
			suggested_action text not null default '',
			risk_level text not null default 'low',
			status text not null,
			source text not null,
			data_level text not null,
			permission_level text not null,
			device_id text not null,
			confidence double precision not null default 0.5,
			user_confirmed boolean not null default false,
			version integer not null default 1,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			deleted_at timestamptz
		);`,
		`create table if not exists steward_memories (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			content text not null default '',
			scope text not null default 'global',
			status text not null,
			source text not null,
			data_level text not null,
			permission_level text not null,
			device_id text not null,
			confidence double precision not null default 1,
			user_confirmed boolean not null default true,
			version integer not null default 1,
			last_verified_at timestamptz,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			deleted_at timestamptz
		);`,
		`create table if not exists steward_memory_versions (
			id uuid primary key,
			memory_id uuid not null references steward_memories(id) on delete cascade,
			version integer not null,
			title text not null,
			summary text not null default '',
			content text not null default '',
			reason text not null default '',
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now()
		);`,
		`create table if not exists steward_knowledge_items (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			source text not null,
			original_uri text not null default '',
			import_method text not null default 'manual',
			content_hash text not null default '',
			status text not null,
			data_level text not null,
			permission_level text not null,
			device_id text not null,
			allow_index boolean not null default true,
			user_confirmed boolean not null default true,
			version integer not null default 1,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			deleted_at timestamptz
		);`,
		`create table if not exists steward_source_refs (
			id uuid primary key,
			target_type text not null,
			target_id uuid not null,
			source_type text not null,
			source_id text not null default '',
			location text not null default '',
			summary text not null default '',
			confidence double precision not null default 1,
			sensitive boolean not null default false,
			displayable boolean not null default true,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now()
		);`,
		`create table if not exists steward_data_tags (
			id uuid primary key,
			name text not null unique,
			type text not null,
			color text not null default '',
			description text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_data_tag_aliases (
			alias_id uuid primary key,
			tag_id uuid not null references steward_data_tags(id) on delete cascade,
			origin_device_id text not null default '',
			created_at timestamptz not null default now()
		);`,
		`create table if not exists steward_entity_tags (
			entity_type text not null,
			entity_id uuid not null,
			tag_id uuid not null references steward_data_tags(id) on delete cascade,
			source text not null,
			confidence double precision not null default 1,
			created_at timestamptz not null default now(),
			primary key (entity_type, entity_id, tag_id)
		);`,
		`create table if not exists steward_devices (
			id text primary key,
			device_name text not null,
			platform text not null,
			role text not null default 'peer',
			trust_status text not null default 'trusted',
			sync_enabled boolean not null default true,
			permission_level text not null default 'A3',
			public_key text not null default '',
			api_base_url text not null default '',
			last_sync_sequence bigint not null default 0,
			last_sent_sequence bigint not null default 0,
			last_seen_at timestamptz,
			last_sync_at timestamptz,
			last_sync_error text,
			revoked_at timestamptz,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`alter table steward_devices
			add column if not exists api_base_url text not null default '',
			add column if not exists last_sync_sequence bigint not null default 0,
			add column if not exists last_sent_sequence bigint not null default 0,
			add column if not exists last_sync_at timestamptz,
			add column if not exists last_sync_error text;`,
		`create table if not exists steward_device_permissions (
			id uuid primary key,
			device_id text not null references steward_devices(id) on delete cascade,
			capability text not null,
			policy text not null default 'confirm',
			max_permission_level text not null default 'A3',
			scope_summary text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			unique(device_id, capability)
		);`,
		`create table if not exists steward_device_capabilities (
			device_id text not null references steward_devices(id) on delete cascade,
			capability text not null,
			description text not null default '',
			target_type text not null default '',
			risk_level text not null default 'low',
			max_permission_level text not null default 'A3',
			version integer not null default 1,
			updated_at timestamptz not null default now(),
			primary key (device_id, capability)
		);`,
		`create table if not exists steward_sync_changes (
			id uuid primary key,
			sequence bigint generated always as identity,
			entity_type text not null,
			entity_id uuid not null,
			operation text not null,
			origin_device_id text not null references steward_devices(id) on delete restrict,
			version integer not null default 1,
			data_level text not null default 'D0',
			payload jsonb not null default '{}'::jsonb,
			payload_hash text not null default '',
			sync_status text not null default 'pending',
			error_summary text,
			created_at timestamptz not null default now(),
			applied_at timestamptz
		);`,
		`create table if not exists steward_sync_conflicts (
			id uuid primary key,
			entity_type text not null,
			entity_id uuid not null,
			local_change_id uuid references steward_sync_changes(id) on delete set null,
			remote_change_id uuid references steward_sync_changes(id) on delete set null,
			reason text not null default '',
			status text not null default 'open',
			resolution text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			resolved_at timestamptz
		);`,
		`create table if not exists steward_autonomy_settings (
			id text primary key,
			paused boolean not null default false,
			mode text not null default 'suggest_only',
			max_auto_permission text not null default 'A3',
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_autonomy_rules (
			id uuid primary key,
			name text not null unique,
			trigger_type text not null,
			target_type text not null,
			action text not null,
			policy text not null default 'confirm',
			risk_level text not null default 'low',
			max_permission_level text not null default 'A3',
			enabled boolean not null default true,
			scope_summary text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_autonomy_proposals (
			id uuid primary key,
			rule_id uuid references steward_autonomy_rules(id) on delete set null,
			source_entity_type text not null default '',
			source_entity_id uuid,
			action text not null default 'create_local_task',
			title text not null,
			summary text not null default '',
			trigger_reason text not null default '',
			suggested_action text not null default '',
			risk_level text not null default 'low',
			permission_level text not null default 'A3',
			data_level text not null default 'D0',
			status text not null default 'candidate',
			policy text not null default 'confirm',
			impact_summary text not null default '',
			score double precision not null default 0.25,
			score_reason text not null default '',
			created_task_id uuid references steward_tasks(id) on delete set null,
			execution_target_type text not null default '',
			execution_target_id text not null default '',
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			unique(rule_id, source_entity_type, source_entity_id)
		);`,
		`alter table steward_autonomy_proposals
			add column if not exists action text not null default 'create_local_task',
			add column if not exists score double precision not null default 0.25,
			add column if not exists score_reason text not null default '',
			add column if not exists execution_target_type text not null default '',
			add column if not exists execution_target_id text not null default '';`,
		`create table if not exists steward_approval_requests (
			id uuid primary key,
			proposal_id uuid references steward_autonomy_proposals(id) on delete set null,
			requested_action text not null,
			risk_summary text not null default '',
			plan_summary text not null default '',
			status text not null default 'pending',
			decided_by text not null default '',
			decision_reason text not null default '',
			created_at timestamptz not null default now(),
			decided_at timestamptz
		);`,
		`create table if not exists steward_autonomous_runs (
			id uuid primary key,
			proposal_id uuid references steward_autonomy_proposals(id) on delete set null,
			rule_id uuid references steward_autonomy_rules(id) on delete set null,
			mode text not null,
			status text not null,
			trigger_reason text not null default '',
			impact_summary text not null default '',
			recovery_hint text not null default '',
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now()
		);`,
		`create table if not exists steward_daemon_loop_status (
			agent_id text not null,
			name text not null,
			enabled boolean not null default false,
			running boolean not null default false,
			interval_text text not null default '',
			last_started_at timestamptz,
			last_completed_at timestamptz,
			last_success_at timestamptz,
			last_error text,
			consecutive_failures integer not null default 0,
			updated_at timestamptz not null default now(),
			primary key (agent_id, name)
		);`,
		`create index if not exists idx_steward_events_created_at on steward_events(created_at desc);`,
		`create index if not exists idx_steward_events_status on steward_events(status);`,
		`create index if not exists idx_steward_tasks_updated_at on steward_tasks(updated_at desc);`,
		`create index if not exists idx_steward_tasks_status on steward_tasks(status);`,
		`create index if not exists idx_steward_audit_logs_occurred_at on steward_audit_logs(occurred_at desc);`,
		`create index if not exists idx_steward_timeline_segments_updated_at on steward_timeline_segments(updated_at desc);`,
		`create index if not exists idx_steward_timeline_pending_events_event on steward_timeline_pending_events(event_id);`,
		`create index if not exists idx_steward_intents_updated_at on steward_intents(updated_at desc);`,
		`create index if not exists idx_steward_intents_status on steward_intents(status);`,
		`create index if not exists idx_steward_memories_updated_at on steward_memories(updated_at desc);`,
		`create index if not exists idx_steward_memories_status on steward_memories(status);`,
		`create index if not exists idx_steward_knowledge_items_updated_at on steward_knowledge_items(updated_at desc);`,
		`create index if not exists idx_steward_source_refs_target on steward_source_refs(target_type, target_id);`,
		`create index if not exists idx_steward_entity_tags_entity on steward_entity_tags(entity_type, entity_id);`,
		`create index if not exists idx_steward_data_tag_aliases_tag on steward_data_tag_aliases(tag_id);`,
		`create index if not exists idx_steward_devices_trust on steward_devices(trust_status, sync_enabled);`,
		`create index if not exists idx_steward_sync_changes_sequence on steward_sync_changes(sequence);`,
		`create index if not exists idx_steward_sync_changes_entity on steward_sync_changes(entity_type, entity_id);`,
		`create index if not exists idx_steward_sync_conflicts_status on steward_sync_conflicts(status, updated_at desc);`,
		`create index if not exists idx_steward_autonomy_proposals_status on steward_autonomy_proposals(status, updated_at desc);`,
		`create index if not exists idx_steward_autonomy_proposals_score on steward_autonomy_proposals(status, score desc, updated_at desc);`,
		`create index if not exists idx_steward_autonomy_proposals_action on steward_autonomy_proposals(action, status);`,
		`create index if not exists idx_steward_approval_requests_status on steward_approval_requests(status, created_at desc);`,
		`delete from steward_approval_requests duplicate
		using steward_approval_requests canonical
		where duplicate.proposal_id = canonical.proposal_id
		  and duplicate.requested_action = canonical.requested_action
		  and duplicate.status = 'pending'
		  and canonical.status = 'pending'
		  and duplicate.id::text > canonical.id::text;`,
		`create unique index if not exists idx_steward_approval_requests_pending_unique
		on steward_approval_requests(proposal_id, requested_action)
		where status = 'pending' and proposal_id is not null;`,
		`create index if not exists idx_steward_autonomous_runs_created_at on steward_autonomous_runs(created_at desc);`,
		`create index if not exists idx_steward_daemon_loop_status_agent on steward_daemon_loop_status(agent_id, name);`,
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
