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
		`alter table steward_collector_configs
			add column if not exists settings jsonb not null default '{}'::jsonb;`,
		`create table if not exists steward_data_policies (
			id uuid primary key,
			data_level text not null,
			source_pattern text not null default '*',
			collect_mode text not null default 'deny',
			model_mode text not null default 'deny',
			model_content_mode text not null default 'summary',
			allow_local_persistence boolean not null default true,
			allow_sync boolean not null default false,
			require_encryption boolean not null default false,
			consent_expires_at timestamptz,
			description text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			unique (data_level, source_pattern)
		);`,
		`create table if not exists steward_permission_policies (
			id uuid primary key,
			permission_level text not null,
			action_pattern text not null default '*',
			execution_mode text not null default 'deny',
			require_simulation boolean not null default true,
			require_rollback boolean not null default false,
			max_batch_size integer not null default 1,
			cooldown_seconds integer not null default 0,
			description text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			unique (permission_level, action_pattern)
		);`,
		`create table if not exists steward_model_dispatches (
			id uuid primary key,
			observation_id uuid not null,
			observation_time timestamptz not null,
			source text not null,
			data_level text not null,
			content_mode text not null,
			status text not null default 'pending',
			attempts integer not null default 0,
			request_summary text not null default '',
			response_summary text not null default '',
			last_error text not null default '',
			next_attempt_at timestamptz,
			provider text not null default '',
			model text not null default '',
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			completed_at timestamptz,
			unique (observation_id, observation_time)
		);`,
		`create table if not exists steward_tool_definitions (
			id uuid primary key,
			action text not null unique,
			name text not null,
			description text not null default '',
			executable text not null,
			arguments jsonb not null default '[]'::jsonb,
			working_directory text not null default '',
			permission_level text not null,
			risk_level text not null default 'high',
			enabled boolean not null default false,
			timeout_seconds integer not null default 60,
			rollback_executable text not null default '',
			rollback_arguments jsonb not null default '[]'::jsonb,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_conversations (
			id uuid primary key,
			title text not null,
			status text not null default 'active',
			data_level text not null default 'D0',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_conversation_messages (
			id uuid primary key,
			conversation_id uuid not null references steward_conversations(id) on delete cascade,
			role text not null,
			content text not null,
			data_level text not null,
			model text not null default '',
			context_summary text not null default '',
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now()
		);`,
		`alter table steward_conversation_messages
			add column if not exists payload_encrypted boolean not null default false,
			add column if not exists encrypted_payload jsonb not null default '{}'::jsonb;`,
		`create table if not exists steward_conversation_suggestions (
			id uuid primary key,
			message_id uuid not null references steward_conversation_messages(id) on delete cascade,
			kind text not null,
			title text not null,
			summary text not null default '',
			content text not null default '',
			suggested_action text not null default '',
			data_level text not null,
			permission_level text not null default 'A3',
			risk_level text not null default 'low',
			status text not null default 'candidate',
			target_id uuid,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`alter table steward_conversation_suggestions
			add column if not exists payload_encrypted boolean not null default false,
			add column if not exists encrypted_payload jsonb not null default '{}'::jsonb;`,
		`create table if not exists steward_collector_observations (
			collector_name text not null,
			observation_key text not null,
			fingerprint text not null,
			last_seen_at timestamptz not null default now(),
			primary key (collector_name, observation_key)
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
		`create table if not exists steward_observations (
			id uuid not null,
			source text not null,
			type text not null,
			summary text not null default '',
			data_level text not null,
			permission_level text not null,
			device_id text not null,
			context_key text not null default '',
			fingerprint text not null default '',
			payload jsonb not null default '{}'::jsonb,
			payload_encrypted boolean not null default false,
			metadata jsonb not null default '{}'::jsonb,
			status text not null default 'active',
			system_generated boolean not null default true,
			retention_locked boolean not null default false,
			duplicate_count integer not null default 1,
			session_id uuid,
			occurred_at timestamptz not null,
			ended_at timestamptz,
			expires_at timestamptz,
			created_at timestamptz not null default now(),
			search_vector tsvector generated always as (to_tsvector('simple', coalesce(summary, ''))) stored,
			primary key (id, occurred_at)
		) partition by range (occurred_at);`,
		`do $$
		declare
			month_start date;
			month_end date;
			partition_name text;
		begin
			for offset_month in -1..1 loop
				month_start := (date_trunc('month', current_date) + (offset_month || ' month')::interval)::date;
				month_end := (month_start + interval '1 month')::date;
				partition_name := 'steward_observations_' || to_char(month_start, 'YYYY_MM');
				execute format('create table if not exists %I partition of steward_observations for values from (%L) to (%L)', partition_name, month_start, month_end);
			end loop;
		end $$;`,
		`create table if not exists steward_observations_default partition of steward_observations default;`,
		`create table if not exists steward_encrypted_blobs (
			id uuid primary key,
			observation_id uuid not null,
			observation_time timestamptz not null,
			storage_path text not null unique,
			mime_type text not null,
			size_bytes bigint not null,
			key_id text not null,
			ciphertext_hash text not null,
			expires_at timestamptz,
			created_at timestamptz not null default now()
		);`,
		`create table if not exists steward_activity_sessions (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			source text not null,
			context_key text not null default '',
			device_id text not null,
			data_level text not null,
			status text not null default 'closed',
			observation_count integer not null default 0,
			confidence double precision not null default 0.5,
			value_score double precision not null default 0.5,
			started_at timestamptz not null,
			ended_at timestamptz not null,
			timeline_id uuid references steward_timeline_segments(id) on delete set null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			search_vector tsvector generated always as (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(summary, ''))) stored
		);`,
		`create table if not exists steward_entities (
			id uuid primary key,
			type text not null,
			canonical_key text not null,
			display_name text not null,
			summary text not null default '',
			data_level text not null,
			status text not null default 'active',
			confidence double precision not null default 0.5,
			evidence_count integer not null default 0,
			first_seen_at timestamptz not null,
			last_seen_at timestamptz not null,
			last_verified_at timestamptz,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			search_vector tsvector generated always as (to_tsvector('simple', coalesce(display_name, '') || ' ' || coalesce(summary, ''))) stored,
			unique (type, canonical_key)
		);`,
		`create table if not exists steward_relations (
			id uuid primary key,
			source_entity_id uuid not null references steward_entities(id) on delete cascade,
			target_entity_id uuid not null references steward_entities(id) on delete cascade,
			relation_type text not null,
			confidence double precision not null default 0.5,
			evidence_count integer not null default 0,
			first_seen_at timestamptz not null,
			last_seen_at timestamptz not null,
			valid_from timestamptz,
			valid_to timestamptz,
			data_level text not null,
			status text not null default 'candidate',
			inference_state text not null default 'candidate',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			unique (source_entity_id, target_entity_id, relation_type)
		);`,
		`create table if not exists steward_relation_evidence (
			id uuid primary key,
			relation_id uuid not null references steward_relations(id) on delete cascade,
			source_ref_id uuid references steward_source_refs(id) on delete set null,
			observation_id uuid,
			observation_time timestamptz,
			evidence_type text not null,
			summary text not null default '',
			confidence double precision not null default 0.5,
			created_at timestamptz not null default now(),
			check (source_ref_id is not null or (observation_id is not null and observation_time is not null))
		);`,
		`create table if not exists steward_habits (
			id uuid primary key,
			entity_id uuid references steward_entities(id) on delete set null,
			type text not null,
			title text not null,
			summary text not null default '',
			pattern text not null default '',
			status text not null default 'candidate',
			data_level text not null,
			confidence double precision not null default 0.5,
			evidence_count integer not null default 0,
			value_score double precision not null default 0.5,
			user_confirmed boolean not null default false,
			retention_locked boolean not null default false,
			last_evidence_at timestamptz,
			quarantined_at timestamptz,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_insights (
			id uuid primary key,
			type text not null,
			title text not null,
			summary text not null default '',
			suggested_action text not null default '',
			status text not null default 'candidate',
			data_level text not null,
			confidence double precision not null default 0.5,
			evidence_count integer not null default 0,
			value_score double precision not null default 0.5,
			user_confirmed boolean not null default false,
			retention_locked boolean not null default false,
			quarantined_at timestamptz,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		);`,
		`create table if not exists steward_retention_policies (
			id uuid primary key,
			source_pattern text not null,
			data_kind text not null,
			data_level text not null default '*',
			ttl_days double precision not null,
			quarantine_days integer not null default 30,
			auto_purge boolean not null default false,
			require_preview boolean not null default true,
			protect_user_confirmed boolean not null default true,
			protect_referenced boolean not null default true,
			deletion_tombstone_days integer not null default 90,
			description text not null default '',
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now(),
			unique (source_pattern, data_kind, data_level)
		);`,
		`create table if not exists steward_lifecycle_runs (
			id uuid primary key,
			job_type text not null,
			status text not null,
			dry_run boolean not null default true,
			action_counts jsonb not null default '{}'::jsonb,
			error_summary text,
			started_at timestamptz not null,
			completed_at timestamptz
		);`,
		`create table if not exists steward_deletion_tombstones (
			id uuid primary key,
			entity_type text not null,
			entity_id text not null,
			source_device_id text not null,
			reason text not null,
			deleted_at timestamptz not null,
			expires_at timestamptz not null,
			audit_id uuid references steward_audit_logs(id) on delete set null,
			unique (entity_type, entity_id)
		);`,
		`alter table steward_events
			add column if not exists valid_from timestamptz,
			add column if not exists valid_to timestamptz,
			add column if not exists inference_status text not null default 'confirmed',
			add column if not exists evidence_count integer not null default 0,
			add column if not exists last_verified_at timestamptz;`,
		`alter table steward_timeline_segments
			add column if not exists valid_from timestamptz,
			add column if not exists valid_to timestamptz,
			add column if not exists inference_status text not null default 'confirmed',
			add column if not exists evidence_count integer not null default 0,
			add column if not exists last_verified_at timestamptz;`,
		`alter table steward_intents
			add column if not exists valid_from timestamptz,
			add column if not exists valid_to timestamptz,
			add column if not exists inference_status text not null default 'candidate',
			add column if not exists evidence_count integer not null default 0,
			add column if not exists last_verified_at timestamptz;`,
		`alter table steward_memories
			add column if not exists valid_from timestamptz,
			add column if not exists valid_to timestamptz,
			add column if not exists inference_status text not null default 'confirmed',
			add column if not exists evidence_count integer not null default 0;`,
		`alter table steward_knowledge_items
			add column if not exists valid_from timestamptz,
			add column if not exists valid_to timestamptz,
			add column if not exists inference_status text not null default 'confirmed',
			add column if not exists evidence_count integer not null default 0,
			add column if not exists last_verified_at timestamptz;`,
		`create index if not exists idx_steward_events_created_at on steward_events(created_at desc);`,
		`create index if not exists idx_steward_events_status on steward_events(status);`,
		`create index if not exists idx_steward_tasks_updated_at on steward_tasks(updated_at desc);`,
		`create index if not exists idx_steward_tasks_status on steward_tasks(status);`,
		`create index if not exists idx_steward_audit_logs_occurred_at on steward_audit_logs(occurred_at desc);`,
		`create index if not exists idx_steward_data_policies_level on steward_data_policies(data_level, source_pattern);`,
		`create index if not exists idx_steward_permission_policies_level on steward_permission_policies(permission_level, action_pattern);`,
		`create index if not exists idx_steward_model_dispatches_pending on steward_model_dispatches(status, next_attempt_at, created_at);`,
		`create index if not exists idx_steward_tool_definitions_enabled on steward_tool_definitions(enabled, permission_level, action);`,
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
		`create index if not exists idx_steward_conversations_updated_at on steward_conversations(updated_at desc);`,
		`create index if not exists idx_steward_conversation_messages_conversation on steward_conversation_messages(conversation_id, created_at);`,
		`create index if not exists idx_steward_conversation_suggestions_message on steward_conversation_suggestions(message_id, status);`,
		`create index if not exists idx_steward_observations_time on steward_observations(occurred_at desc);`,
		`create index if not exists idx_steward_observations_pending on steward_observations(status, session_id, occurred_at);`,
		`create index if not exists idx_steward_observations_expiry on steward_observations(expires_at) where expires_at is not null;`,
		`create index if not exists idx_steward_observations_fingerprint on steward_observations(source, type, context_key, fingerprint, occurred_at desc);`,
		`create index if not exists idx_steward_observations_search on steward_observations using gin(search_vector);`,
		`create index if not exists idx_steward_encrypted_blobs_observation on steward_encrypted_blobs(observation_id, observation_time);`,
		`create index if not exists idx_steward_activity_sessions_time on steward_activity_sessions(started_at desc);`,
		`create index if not exists idx_steward_activity_sessions_search on steward_activity_sessions using gin(search_vector);`,
		`create index if not exists idx_steward_entities_search on steward_entities using gin(search_vector);`,
		`create index if not exists idx_steward_relations_source on steward_relations(source_entity_id, status);`,
		`create index if not exists idx_steward_relations_target on steward_relations(target_entity_id, status);`,
		`create index if not exists idx_steward_relation_evidence_relation on steward_relation_evidence(relation_id, created_at desc);`,
		`create index if not exists idx_steward_habits_value on steward_habits(status, value_score, updated_at);`,
		`create index if not exists idx_steward_insights_value on steward_insights(status, value_score, updated_at);`,
		`create index if not exists idx_steward_lifecycle_runs_job on steward_lifecycle_runs(job_type, started_at desc);`,
		`create index if not exists idx_steward_tombstones_expiry on steward_deletion_tombstones(expires_at);`,
	}

	for _, statement := range statements {
		if _, err := db.Pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate statement: %w", err)
		}
	}

	// pgvector is optional so a PostgreSQL installation without the extension
	// still supports the required full-text and relationship retrieval path.
	if _, err := db.Pool.Exec(ctx, `create extension if not exists vector;`); err == nil {
		vectorStatements := []string{
			`alter table steward_observations add column if not exists embedding vector(768);`,
			`alter table steward_activity_sessions add column if not exists embedding vector(768);`,
			`create index if not exists idx_steward_observations_embedding on steward_observations using hnsw (embedding vector_cosine_ops) where embedding is not null;`,
			`create index if not exists idx_steward_activity_sessions_embedding on steward_activity_sessions using hnsw (embedding vector_cosine_ops) where embedding is not null;`,
		}
		for _, statement := range vectorStatements {
			_, _ = db.Pool.Exec(ctx, statement)
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
