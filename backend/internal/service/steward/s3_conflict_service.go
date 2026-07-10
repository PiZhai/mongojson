package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func (s *Service) ListSyncConflicts(ctx context.Context, status string, limit int) ([]domain.StewardSyncConflict, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, entity_type, entity_id::text,
		       coalesce(local_change_id::text, ''), coalesce(remote_change_id::text, ''),
		       reason, status, coalesce(resolution, ''), created_at, updated_at, resolved_at
		from steward_sync_conflicts
		where ($1 = '' or status = $1)
		order by updated_at desc
		limit $2
	`, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, fmt.Errorf("list steward sync conflicts: %w", err)
	}
	defer rows.Close()

	conflicts := []domain.StewardSyncConflict{}
	for rows.Next() {
		conflict, err := scanSyncConflict(rows)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func (s *Service) ResolveSyncConflict(ctx context.Context, id string, input ResolveSyncConflictInput) (domain.StewardSyncConflict, error) {
	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_sync_conflicts
		set status = $1, resolution = $2, resolved_at = $3, updated_at = $3
		where id = $4 and status = $5
	`, StatusResolved, defaultString(input.Resolution, "manual resolution"), now, id, StatusOpen)
	if err != nil {
		return domain.StewardSyncConflict{}, fmt.Errorf("resolve sync conflict: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.StewardSyncConflict{}, fmt.Errorf("sync conflict not found")
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "sync.conflict.resolve",
		TargetType:      "sync_conflict",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   defaultString(input.Resolution, "manual resolution"),
		ResultStatus:    ResultOK,
	})
	return s.getSyncConflict(ctx, id)
}

func (s *Service) applyTaskSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}

	if change.Operation == SyncDelete {
		now := time.Now().UTC()
		_, err := s.db.Pool.Exec(ctx, `
			update steward_tasks
			set status = $1, deleted_at = $2, updated_at = $2, version = greatest(version, $3)
			where id = $4
		`, StatusDeleted, now, change.Version, change.EntityID)
		if err != nil {
			msg := err.Error()
			_ = s.markSyncChange(ctx, change.ID, SyncPending, &msg, false)
			return false, domain.StewardSyncConflict{}, fmt.Errorf("apply task delete sync change: %w", err)
		}
		if err := s.markSyncChange(ctx, change.ID, SyncApplied, nil, true); err != nil {
			return false, domain.StewardSyncConflict{}, err
		}
		return true, domain.StewardSyncConflict{}, nil
	}

	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_tasks (
			id, type, title, description, status, priority, due_at, source, data_level, permission_level,
			device_id, risk_level, user_confirmed, version, created_at, updated_at, completed_at, canceled_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    description = excluded.description,
		    status = excluded.status,
		    priority = excluded.priority,
		    due_at = excluded.due_at,
		    source = excluded.source,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    device_id = excluded.device_id,
		    risk_level = excluded.risk_level,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_tasks.version, excluded.version),
		    completed_at = excluded.completed_at,
		    canceled_at = excluded.canceled_at,
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID, stringPayload(change.Payload, "type", "remote"),
		stringPayload(change.Payload, "title", "同步任务"),
		stringPayload(change.Payload, "description", stringPayload(change.Payload, "summary", "")),
		stringPayload(change.Payload, "status", StatusOpen),
		stringPayload(change.Payload, "priority", "normal"),
		nullableTimePayload(change.Payload, "due_at"),
		"sync", defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		change.OriginDeviceID,
		stringPayload(change.Payload, "risk_level", "low"),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version, createdAt, updatedAt,
		nullableTimePayload(change.Payload, "completed_at"),
		nullableTimePayload(change.Payload, "canceled_at"))
	if err != nil {
		msg := err.Error()
		_ = s.markSyncChange(ctx, change.ID, SyncPending, &msg, false)
		return false, domain.StewardSyncConflict{}, fmt.Errorf("apply task sync change: %w", err)
	}
	if err := s.markSyncChange(ctx, change.ID, SyncApplied, nil, true); err != nil {
		return false, domain.StewardSyncConflict{}, err
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "sync",
		Action:          "sync.change.apply",
		TargetType:      change.EntityType,
		TargetID:        &change.EntityID,
		Source:          change.OriginDeviceID,
		PermissionLevel: PermissionA3,
		DataLevel:       change.DataLevel,
		InputSummary:    change.Operation + " " + change.EntityID,
		OutputSummary:   "sync change applied",
		ResultStatus:    ResultOK,
	})
	return true, domain.StewardSyncConflict{}, nil
}

func (s *Service) createSyncConflict(ctx context.Context, change domain.StewardSyncChange, reason string) (domain.StewardSyncConflict, error) {
	now := time.Now().UTC()
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_sync_conflicts (
			id, entity_type, entity_id, remote_change_id, reason, status, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$7)
		returning id, entity_type, entity_id::text,
		          coalesce(local_change_id::text, ''), coalesce(remote_change_id::text, ''),
		          reason, status, coalesce(resolution, ''), created_at, updated_at, resolved_at
	`, uuid.NewString(), change.EntityType, change.EntityID, change.ID, reason, StatusOpen, now)
	conflict, err := scanSyncConflict(row)
	if err != nil {
		return domain.StewardSyncConflict{}, fmt.Errorf("create sync conflict: %w", err)
	}
	return conflict, nil
}

func (s *Service) getSyncConflict(ctx context.Context, id string) (domain.StewardSyncConflict, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, entity_type, entity_id::text,
		       coalesce(local_change_id::text, ''), coalesce(remote_change_id::text, ''),
		       reason, status, coalesce(resolution, ''), created_at, updated_at, resolved_at
		from steward_sync_conflicts
		where id = $1
	`, id)
	return scanSyncConflict(row)
}

func scanSyncConflict(row scanner) (domain.StewardSyncConflict, error) {
	var conflict domain.StewardSyncConflict
	var localChangeID string
	var remoteChangeID string
	err := row.Scan(&conflict.ID, &conflict.EntityType, &conflict.EntityID, &localChangeID,
		&remoteChangeID, &conflict.Reason, &conflict.Status, &conflict.Resolution,
		&conflict.CreatedAt, &conflict.UpdatedAt, &conflict.ResolvedAt)
	conflict.LocalChangeID = stringPtrIfPresent(localChangeID)
	conflict.RemoteChangeID = stringPtrIfPresent(remoteChangeID)
	return conflict, err
}
