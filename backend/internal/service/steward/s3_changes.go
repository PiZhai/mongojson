package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

func (s *Service) CreateSyncChange(ctx context.Context, input CreateSyncChangeInput) (domain.StewardSyncChange, error) {
	change, _, err := s.createSyncChange(ctx, input)
	return change, err
}

func (s *Service) createSyncChange(ctx context.Context, input CreateSyncChangeInput) (domain.StewardSyncChange, bool, error) {
	if strings.TrimSpace(input.ID) != "" {
		existing, err := s.getSyncChange(ctx, strings.TrimSpace(input.ID))
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardSyncChange{}, false, err
		}
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.Version <= 0 {
		input.Version = 1
	}
	origin := defaultString(input.OriginDeviceID, s.agentIDValue())
	id := defaultString(input.ID, uuid.NewString())
	operation := normalizeOperation(input.Operation)
	dataLevel := defaultString(input.DataLevel, DataD0)
	if _, err := s.getDevice(ctx, origin); err != nil {
		if _, registerErr := s.RegisterDevice(ctx, RegisterDeviceInput{ID: origin, DeviceName: origin, Platform: "unknown"}); registerErr != nil {
			return domain.StewardSyncChange{}, false, registerErr
		}
	}
	plainPayload, err := json.Marshal(input.Payload)
	if err != nil {
		return domain.StewardSyncChange{}, false, fmt.Errorf("marshal sync payload: %w", err)
	}
	hash := hashBytes(plainPayload)
	input.ID = id
	input.Operation = operation
	input.OriginDeviceID = origin
	input.DataLevel = dataLevel
	storagePayload, err := prepareSyncPayloadForStorage(input)
	if err != nil {
		return domain.StewardSyncChange{}, false, err
	}
	payload, err := json.Marshal(storagePayload)
	if err != nil {
		return domain.StewardSyncChange{}, false, fmt.Errorf("marshal sync payload for storage: %w", err)
	}
	var change domain.StewardSyncChange
	if err := s.db.Pool.QueryRow(ctx, `
		insert into steward_sync_changes (
			id, entity_type, entity_id, operation, origin_device_id, version, data_level,
			payload, payload_hash, sync_status, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11)
		on conflict (id) do nothing
		returning id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		          payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
	`, id, input.EntityType, input.EntityID, operation, origin, input.Version,
		dataLevel, string(payload), hash, SyncPending, time.Now().UTC()).Scan(
		&change.ID, &change.Sequence, &change.EntityType, &change.EntityID, &change.Operation,
		&change.OriginDeviceID, &change.Version, &change.DataLevel, newPayloadScanner(&change.Payload),
		&change.PayloadHash, &change.SyncStatus, &change.ErrorSummary, &change.CreatedAt, &change.AppliedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := s.getSyncChange(ctx, id)
		if getErr != nil {
			return domain.StewardSyncChange{}, false, fmt.Errorf("read concurrent steward sync change %s: %w", id, getErr)
		}
		return existing, false, nil
	} else if err != nil {
		return domain.StewardSyncChange{}, false, fmt.Errorf("create steward sync change: %w", err)
	}
	if change.Payload, err = decryptStoredSyncPayload(change); err != nil {
		return domain.StewardSyncChange{}, false, err
	}
	return change, true, nil
}

func (s *Service) ListSyncChanges(ctx context.Context, sinceSequence int64, limit int) ([]domain.StewardSyncChange, error) {
	return s.listSyncChanges(ctx, sinceSequence, limit, syncChangeListReplay)
}

func (s *Service) ListRecentSyncChanges(ctx context.Context, limit int) ([]domain.StewardSyncChange, error) {
	return s.listSyncChanges(ctx, 0, limit, syncChangeListRecent)
}

func (s *Service) listSyncChanges(ctx context.Context, sinceSequence int64, limit int, mode syncChangeListMode) ([]domain.StewardSyncChange, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, syncChangeListQuery(mode), sinceSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward sync changes: %w", err)
	}
	defer rows.Close()

	changes := []domain.StewardSyncChange{}
	for rows.Next() {
		change, err := scanSyncChange(rows)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func syncChangeListQuery(mode syncChangeListMode) string {
	return fmt.Sprintf(`
		select id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		       payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
		from steward_sync_changes
		where sequence > $1
		order by sequence %s
		limit $2
	`, syncChangeListOrder(mode))
}

func syncChangeListOrder(mode syncChangeListMode) string {
	if mode == syncChangeListRecent {
		return "desc"
	}
	return "asc"
}

func (s *Service) recordTaskSyncChange(ctx context.Context, task domain.StewardTask, operation string) error {
	if task.ID == "" {
		return nil
	}
	payload := map[string]any{
		"type":             task.Type,
		"title":            task.Title,
		"description":      task.Description,
		"status":           task.Status,
		"priority":         task.Priority,
		"source":           task.Source,
		"data_level":       task.DataLevel,
		"permission_level": task.PermissionLevel,
		"risk_level":       task.RiskLevel,
		"user_confirmed":   task.UserConfirmed,
		"version":          task.Version,
		"updated_at":       task.UpdatedAt,
	}
	if task.DueAt != nil {
		payload["due_at"] = task.DueAt
	}
	if task.CompletedAt != nil {
		payload["completed_at"] = task.CompletedAt
	}
	if task.CanceledAt != nil {
		payload["canceled_at"] = task.CanceledAt
	}
	if task.DeletedAt != nil {
		payload["deleted_at"] = task.DeletedAt
	}
	_, err := s.CreateSyncChange(ctx, CreateSyncChangeInput{
		EntityType:     "task",
		EntityID:       task.ID,
		Operation:      operation,
		OriginDeviceID: s.agentIDValue(),
		Version:        task.Version,
		DataLevel:      task.DataLevel,
		Payload:        payload,
	})
	return err
}

func (s *Service) markSyncChange(ctx context.Context, id string, status string, errorSummary *string, applied bool) error {
	var appliedAt *time.Time
	if applied {
		now := time.Now().UTC()
		appliedAt = &now
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_sync_changes
		set sync_status = $1, error_summary = $2, applied_at = $3
		where id = $4
	`, status, errorSummary, appliedAt, id); err != nil {
		return fmt.Errorf("mark sync change: %w", err)
	}
	return nil
}

func (s *Service) getSyncChange(ctx context.Context, id string) (domain.StewardSyncChange, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		       payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
		from steward_sync_changes
		where id = $1
	`, id)
	return scanSyncChange(row)
}

func scanSyncChange(row scanner) (domain.StewardSyncChange, error) {
	var change domain.StewardSyncChange
	err := row.Scan(&change.ID, &change.Sequence, &change.EntityType, &change.EntityID,
		&change.Operation, &change.OriginDeviceID, &change.Version, &change.DataLevel,
		newPayloadScanner(&change.Payload), &change.PayloadHash, &change.SyncStatus,
		&change.ErrorSummary, &change.CreatedAt, &change.AppliedAt)
	if err != nil {
		return change, err
	}
	change.Payload, err = decryptStoredSyncPayload(change)
	return change, err
}

type payloadScanner struct {
	value *map[string]any
}

func newPayloadScanner(value *map[string]any) *payloadScanner {
	return &payloadScanner{value: value}
}

func (p *payloadScanner) Scan(src any) error {
	if p.value == nil {
		return nil
	}
	switch value := src.(type) {
	case string:
		return json.Unmarshal([]byte(value), p.value)
	case []byte:
		return json.Unmarshal(value, p.value)
	default:
		*p.value = map[string]any{}
		return nil
	}
}

func normalizeOperation(value string) string {
	switch strings.TrimSpace(value) {
	case SyncCreate, SyncUpdate, SyncDelete:
		return strings.TrimSpace(value)
	default:
		return SyncUpdate
	}
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func stringPayload(payload map[string]any, key string, fallback string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return fallback
	}
	text, ok := value.(string)
	if !ok {
		return fallback
	}
	return defaultString(text, fallback)
}

func stringPtrIfPresent(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
