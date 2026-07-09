package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

func (s *Service) checkIncomingEntityVersion(ctx context.Context, change domain.StewardSyncChange) (domain.StewardSyncConflict, bool, error) {
	currentVersion, localSnapshot, found, err := s.localSyncEntitySnapshot(ctx, change.EntityType, change.EntityID)
	if err != nil || !found || currentVersion < change.Version {
		return domain.StewardSyncConflict{}, false, err
	}
	if currentVersion > change.Version {
		return s.registerIncomingSyncConflict(ctx, change, "incoming "+change.EntityType+" version is older than local entity")
	}
	incomingSnapshot, err := incomingSyncEntitySnapshot(change)
	if err != nil {
		return domain.StewardSyncConflict{}, false, err
	}
	localEncoded, err := json.Marshal(localSnapshot)
	if err != nil {
		return domain.StewardSyncConflict{}, false, err
	}
	incomingEncoded, err := json.Marshal(incomingSnapshot)
	if err != nil {
		return domain.StewardSyncConflict{}, false, err
	}
	if string(localEncoded) == string(incomingEncoded) {
		return domain.StewardSyncConflict{}, false, nil
	}
	return s.registerIncomingSyncConflict(ctx, change, "incoming "+change.EntityType+" has the same version but different content")
}

func (s *Service) registerIncomingSyncConflict(ctx context.Context, change domain.StewardSyncChange, reason string) (domain.StewardSyncConflict, bool, error) {
	conflict, err := s.createSyncConflict(ctx, change, reason)
	if err != nil {
		return domain.StewardSyncConflict{}, false, err
	}
	_ = s.markSyncChange(ctx, change.ID, SyncConflictStatus, nil, false)
	return conflict, true, nil
}

func (s *Service) localSyncEntitySnapshot(ctx context.Context, entityType string, id string) (int, map[string]any, bool, error) {
	switch entityType {
	case EntityTask:
		item, err := s.getTask(ctx, id)
		if err != nil {
			return syncSnapshotNotFound(err)
		}
		return item.Version, taskSyncSnapshot(item), true, nil
	case EntityEvent:
		item, err := s.getEvent(ctx, id)
		if err != nil {
			return syncSnapshotNotFound(err)
		}
		return item.Version, eventSyncSnapshot(item), true, nil
	case EntityIntent:
		item, err := s.getIntent(ctx, id)
		if err != nil {
			return syncSnapshotNotFound(err)
		}
		return item.Version, intentSyncSnapshot(item), true, nil
	case EntityMemory:
		item, err := s.getMemory(ctx, id)
		if err != nil {
			return syncSnapshotNotFound(err)
		}
		return item.Version, memorySyncSnapshot(item), true, nil
	case EntityKnowledgeItem:
		item, err := s.getKnowledgeItem(ctx, id)
		if err != nil {
			return syncSnapshotNotFound(err)
		}
		return item.Version, knowledgeSyncSnapshot(item), true, nil
	case EntityTimeline:
		item, err := s.getTimelineSegment(ctx, id)
		if err != nil {
			return syncSnapshotNotFound(err)
		}
		eventIDs, err := s.listTimelineSegmentEventIDs(ctx, id)
		if err != nil {
			return 0, nil, false, err
		}
		return item.Version, timelineSyncSnapshot(item, eventIDs), true, nil
	default:
		return 0, nil, false, fmt.Errorf("entity type %s does not support version conflict detection", entityType)
	}
}

func syncSnapshotNotFound(err error) (int, map[string]any, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	return 0, nil, false, err
}

func incomingSyncEntitySnapshot(change domain.StewardSyncChange) (map[string]any, error) {
	deleted := change.Operation == SyncDelete || nullableTimePayload(change.Payload, "deleted_at") != nil
	status := func(fallback string) string {
		if deleted {
			return StatusDeleted
		}
		return stringPayload(change.Payload, "status", fallback)
	}
	switch change.EntityType {
	case EntityTask:
		return map[string]any{
			"type": stringPayload(change.Payload, "type", "remote"), "title": stringPayload(change.Payload, "title", "同步任务"),
			"description": stringPayload(change.Payload, "description", stringPayload(change.Payload, "summary", "")), "status": status(StatusOpen),
			"priority": stringPayload(change.Payload, "priority", "normal"), "due_at": payloadTimeText(change.Payload, "due_at"),
			"data_level": defaultString(change.DataLevel, DataD0), "permission_level": stringPayload(change.Payload, "permission_level", PermissionA3),
			"risk_level": stringPayload(change.Payload, "risk_level", "low"), "user_confirmed": boolPayload(change.Payload, "user_confirmed", true),
			"completed_at": payloadTimeText(change.Payload, "completed_at"), "canceled_at": payloadTimeText(change.Payload, "canceled_at"), "deleted": deleted,
		}, nil
	case EntityEvent:
		return map[string]any{
			"type": stringPayload(change.Payload, "type", "activity"), "title": stringPayload(change.Payload, "title", "同步事件"),
			"summary": stringPayload(change.Payload, "summary", ""), "status": status(StatusActive),
			"data_level": defaultString(change.DataLevel, DataD0), "permission_level": stringPayload(change.Payload, "permission_level", PermissionA3),
			"user_confirmed": boolPayload(change.Payload, "user_confirmed", true), "deleted": deleted,
		}, nil
	case EntityIntent:
		return map[string]any{
			"type": stringPayload(change.Payload, "type", "follow_up"), "title": stringPayload(change.Payload, "title", "同步意图"),
			"summary": stringPayload(change.Payload, "summary", ""), "reason": stringPayload(change.Payload, "reason", ""),
			"suggested_action": stringPayload(change.Payload, "suggested_action", ""), "risk_level": stringPayload(change.Payload, "risk_level", "low"),
			"status": status(StatusCandidate), "data_level": defaultString(change.DataLevel, DataD0),
			"permission_level": stringPayload(change.Payload, "permission_level", PermissionA3), "confidence": floatPayload(change.Payload, "confidence", 0.5),
			"user_confirmed": boolPayload(change.Payload, "user_confirmed", true), "deleted": deleted,
		}, nil
	case EntityMemory:
		return map[string]any{
			"type": stringPayload(change.Payload, "type", "project_fact"), "title": stringPayload(change.Payload, "title", "同步记忆"),
			"summary": stringPayload(change.Payload, "summary", ""), "content": stringPayload(change.Payload, "content", stringPayload(change.Payload, "summary", "")),
			"scope": stringPayload(change.Payload, "scope", "global"), "status": status(StatusActive),
			"data_level": defaultString(change.DataLevel, DataD0), "permission_level": stringPayload(change.Payload, "permission_level", PermissionA3),
			"confidence": floatPayload(change.Payload, "confidence", 1), "user_confirmed": boolPayload(change.Payload, "user_confirmed", true),
			"last_verified_at": payloadTimeText(change.Payload, "last_verified_at"), "deleted": deleted,
		}, nil
	case EntityKnowledgeItem:
		return map[string]any{
			"type": stringPayload(change.Payload, "type", "note"), "title": stringPayload(change.Payload, "title", "同步知识"),
			"summary": stringPayload(change.Payload, "summary", ""), "original_uri": stringPayload(change.Payload, "original_uri", ""),
			"import_method": stringPayload(change.Payload, "import_method", "sync"), "content_hash": stringPayload(change.Payload, "content_hash", ""),
			"status": status(StatusActive), "data_level": defaultString(change.DataLevel, DataD0),
			"permission_level": stringPayload(change.Payload, "permission_level", PermissionA3), "allow_index": boolPayload(change.Payload, "allow_index", true),
			"user_confirmed": boolPayload(change.Payload, "user_confirmed", true), "deleted": deleted,
		}, nil
	case EntityTimeline:
		eventIDs := stringSlicePayload(change.Payload, "event_ids")
		sort.Strings(eventIDs)
		return map[string]any{
			"type": stringPayload(change.Payload, "type", "remote_cluster"), "title": stringPayload(change.Payload, "title", "同步时间线片段"),
			"summary": stringPayload(change.Payload, "summary", ""), "status": status(StatusActive),
			"data_level": defaultString(change.DataLevel, DataD0), "permission_level": stringPayload(change.Payload, "permission_level", PermissionA3),
			"start_at": payloadTimeText(change.Payload, "start_at"), "end_at": payloadTimeText(change.Payload, "end_at"),
			"confidence": floatPayload(change.Payload, "confidence", 1), "user_confirmed": boolPayload(change.Payload, "user_confirmed", true),
			"event_ids": eventIDs, "deleted": deleted,
		}, nil
	default:
		return nil, fmt.Errorf("entity type %s does not support version conflict detection", change.EntityType)
	}
}

func taskSyncSnapshot(item domain.StewardTask) map[string]any {
	return map[string]any{"type": item.Type, "title": item.Title, "description": item.Description, "status": item.Status, "priority": item.Priority,
		"due_at": timeText(item.DueAt), "data_level": item.DataLevel, "permission_level": item.PermissionLevel, "risk_level": item.RiskLevel,
		"user_confirmed": item.UserConfirmed, "completed_at": timeText(item.CompletedAt), "canceled_at": timeText(item.CanceledAt), "deleted": item.DeletedAt != nil}
}

func eventSyncSnapshot(item domain.StewardEvent) map[string]any {
	return map[string]any{"type": item.Type, "title": item.Title, "summary": item.Summary, "status": item.Status, "data_level": item.DataLevel,
		"permission_level": item.PermissionLevel, "user_confirmed": item.UserConfirmed, "deleted": item.DeletedAt != nil}
}

func intentSyncSnapshot(item domain.StewardIntent) map[string]any {
	return map[string]any{"type": item.Type, "title": item.Title, "summary": item.Summary, "reason": item.Reason, "suggested_action": item.SuggestedAction,
		"risk_level": item.RiskLevel, "status": item.Status, "data_level": item.DataLevel, "permission_level": item.PermissionLevel,
		"confidence": item.Confidence, "user_confirmed": item.UserConfirmed, "deleted": item.DeletedAt != nil}
}

func memorySyncSnapshot(item domain.StewardMemory) map[string]any {
	return map[string]any{"type": item.Type, "title": item.Title, "summary": item.Summary, "content": item.Content, "scope": item.Scope, "status": item.Status,
		"data_level": item.DataLevel, "permission_level": item.PermissionLevel, "confidence": item.Confidence, "user_confirmed": item.UserConfirmed,
		"last_verified_at": timeText(item.LastVerifiedAt), "deleted": item.DeletedAt != nil}
}

func knowledgeSyncSnapshot(item domain.StewardKnowledgeItem) map[string]any {
	return map[string]any{"type": item.Type, "title": item.Title, "summary": item.Summary, "original_uri": item.OriginalURI, "import_method": item.ImportMethod,
		"content_hash": item.ContentHash, "status": item.Status, "data_level": item.DataLevel, "permission_level": item.PermissionLevel,
		"allow_index": item.AllowIndex, "user_confirmed": item.UserConfirmed, "deleted": item.DeletedAt != nil}
}

func timelineSyncSnapshot(item domain.StewardTimelineSegment, eventIDs []string) map[string]any {
	sort.Strings(eventIDs)
	return map[string]any{"type": item.Type, "title": item.Title, "summary": item.Summary, "status": item.Status, "data_level": item.DataLevel,
		"permission_level": item.PermissionLevel, "start_at": timeText(item.StartAt), "end_at": timeText(item.EndAt), "confidence": item.Confidence,
		"user_confirmed": item.UserConfirmed, "event_ids": eventIDs, "deleted": item.DeletedAt != nil}
}

func payloadTimeText(payload map[string]any, key string) string {
	return timeText(nullableTimePayload(payload, key))
}

func timeText(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
