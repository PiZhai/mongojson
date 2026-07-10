package steward

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	EntityTask             = "task"
	EntityEvent            = "event"
	EntityIntent           = "intent"
	EntityMemory           = "memory"
	EntityKnowledgeItem    = "knowledge_item"
	EntitySourceRef        = "source_ref"
	EntityDataTag          = "data_tag"
	EntityEntityTag        = "entity_tag"
	EntityTimeline         = "timeline_segment"
	EntityAuditSummary     = "audit_summary"
	EntityDeviceRevoke     = "device_revoke"
	EntityDeviceCapability = "device_capability"
)

func (s *Service) applySoftDeleteSyncChange(ctx context.Context, table string, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		update `+table+`
		set status = $1, deleted_at = $2, updated_at = $2, version = greatest(version, $3)
		where id = $4
	`, StatusDeleted, now, change.Version, change.EntityID)
	if err != nil {
		return s.markApplyError(ctx, change, "apply "+change.EntityType+" delete sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) finishAppliedSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
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
		Syncable:        boolPtr(false),
		ResultStatus:    ResultOK,
	})
	return true, domain.StewardSyncConflict{}, nil
}

func truncateSyncSummary(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func (s *Service) markApplyError(ctx context.Context, change domain.StewardSyncChange, label string, err error) (bool, domain.StewardSyncConflict, error) {
	msg := err.Error()
	_ = s.markSyncChange(ctx, change.ID, SyncPending, &msg, false)
	return false, domain.StewardSyncConflict{}, fmt.Errorf("%s: %w", label, err)
}

func (s *Service) recordEntitySyncChange(ctx context.Context, entityType string, entityID string, operation string, version int, dataLevel string, payload map[string]any) error {
	if strings.TrimSpace(entityID) == "" {
		return nil
	}
	_, err := s.CreateSyncChange(ctx, CreateSyncChangeInput{
		EntityType:     entityType,
		EntityID:       entityID,
		Operation:      operation,
		OriginDeviceID: s.agentIDValue(),
		Version:        version,
		DataLevel:      dataLevel,
		Payload:        payload,
	})
	return err
}

func boolPayload(payload map[string]any, key string, fallback bool) bool {
	value, ok := payload[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func floatPayload(payload map[string]any, key string, fallback float64) float64 {
	value, ok := payload[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func stringSlicePayload(payload map[string]any, key string) []string {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	items := []string{}
	appendString := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			items = append(items, value)
		}
	}
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			appendString(item)
		}
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				appendString(text)
			}
		}
	case string:
		for _, item := range strings.Split(typed, ",") {
			appendString(item)
		}
	}
	return items
}

func timePayload(payload map[string]any, key string, fallback time.Time) time.Time {
	if parsed := nullableTimePayload(payload, key); parsed != nil {
		return *parsed
	}
	return fallback
}

func nullableTimePayload(payload map[string]any, key string) *time.Time {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case time.Time:
		return &typed
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return &parsed
		}
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			return &parsed
		}
	}
	return nil
}
