package steward

import (
	"context"
	"time"

	"mongojson/backend/internal/domain"
)

func (s *Service) applyEventSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}
	if change.Operation == SyncDelete {
		return s.applySoftDeleteSyncChange(ctx, "steward_events", change)
	}
	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_events (
			id, type, title, summary, source, data_level, permission_level, status, device_id,
			user_confirmed, version, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    summary = excluded.summary,
		    source = excluded.source,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    status = excluded.status,
		    device_id = excluded.device_id,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_events.version, excluded.version),
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID, stringPayload(change.Payload, "type", "remote_note"),
		stringPayload(change.Payload, "title", "同步事件"),
		stringPayload(change.Payload, "summary", ""),
		"sync", defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		stringPayload(change.Payload, "status", StatusActive),
		change.OriginDeviceID,
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version, createdAt, updatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply event sync change", err)
	}
	if err := s.resolvePendingTimelineEventLinks(ctx, change.EntityID); err != nil {
		return s.markApplyError(ctx, change, "resolve pending timeline event links", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) applyIntentSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}
	if change.Operation == SyncDelete {
		return s.applySoftDeleteSyncChange(ctx, "steward_intents", change)
	}
	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_intents (
			id, type, title, summary, reason, suggested_action, risk_level, status, source,
			data_level, permission_level, device_id, confidence, user_confirmed, version, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    summary = excluded.summary,
		    reason = excluded.reason,
		    suggested_action = excluded.suggested_action,
		    risk_level = excluded.risk_level,
		    status = excluded.status,
		    source = excluded.source,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    device_id = excluded.device_id,
		    confidence = excluded.confidence,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_intents.version, excluded.version),
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID, stringPayload(change.Payload, "type", "follow_up"),
		stringPayload(change.Payload, "title", "同步意图"),
		stringPayload(change.Payload, "summary", ""),
		stringPayload(change.Payload, "reason", ""),
		stringPayload(change.Payload, "suggested_action", ""),
		stringPayload(change.Payload, "risk_level", "low"),
		stringPayload(change.Payload, "status", StatusCandidate),
		"sync", defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		change.OriginDeviceID,
		floatPayload(change.Payload, "confidence", 0.5),
		boolPayload(change.Payload, "user_confirmed", false),
		change.Version, createdAt, updatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply intent sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) applyMemorySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}
	if change.Operation == SyncDelete {
		return s.applySoftDeleteSyncChange(ctx, "steward_memories", change)
	}
	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	lastVerifiedAt := nullableTimePayload(change.Payload, "last_verified_at")
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_memories (
			id, type, title, summary, content, scope, status, source, data_level, permission_level,
			device_id, confidence, user_confirmed, version, last_verified_at, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    summary = excluded.summary,
		    content = excluded.content,
		    scope = excluded.scope,
		    status = excluded.status,
		    source = excluded.source,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    device_id = excluded.device_id,
		    confidence = excluded.confidence,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_memories.version, excluded.version),
		    last_verified_at = excluded.last_verified_at,
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID, stringPayload(change.Payload, "type", "project_fact"),
		stringPayload(change.Payload, "title", "同步记忆"),
		stringPayload(change.Payload, "summary", ""),
		stringPayload(change.Payload, "content", stringPayload(change.Payload, "summary", "")),
		stringPayload(change.Payload, "scope", "global"),
		stringPayload(change.Payload, "status", StatusActive),
		"sync", defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		change.OriginDeviceID,
		floatPayload(change.Payload, "confidence", 1),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version, lastVerifiedAt, createdAt, updatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply memory sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) applyKnowledgeItemSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}
	if change.Operation == SyncDelete {
		return s.applySoftDeleteSyncChange(ctx, "steward_knowledge_items", change)
	}
	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_knowledge_items (
			id, type, title, summary, source, original_uri, import_method, content_hash, status,
			data_level, permission_level, device_id, allow_index, user_confirmed, version, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    summary = excluded.summary,
		    source = excluded.source,
		    original_uri = excluded.original_uri,
		    import_method = excluded.import_method,
		    content_hash = excluded.content_hash,
		    status = excluded.status,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    device_id = excluded.device_id,
		    allow_index = excluded.allow_index,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_knowledge_items.version, excluded.version),
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID, stringPayload(change.Payload, "type", "note"),
		stringPayload(change.Payload, "title", "同步知识"),
		stringPayload(change.Payload, "summary", ""),
		"sync",
		stringPayload(change.Payload, "original_uri", ""),
		stringPayload(change.Payload, "import_method", "sync"),
		stringPayload(change.Payload, "content_hash", ""),
		stringPayload(change.Payload, "status", StatusActive),
		defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		change.OriginDeviceID,
		boolPayload(change.Payload, "allow_index", true),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version, createdAt, updatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply knowledge item sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) recordEventSyncChange(ctx context.Context, event domain.StewardEvent, operation string) error {
	payload := map[string]any{
		"type":             event.Type,
		"title":            event.Title,
		"summary":          event.Summary,
		"source":           event.Source,
		"data_level":       event.DataLevel,
		"permission_level": event.PermissionLevel,
		"status":           event.Status,
		"user_confirmed":   event.UserConfirmed,
		"version":          event.Version,
		"created_at":       event.CreatedAt,
		"updated_at":       event.UpdatedAt,
	}
	if event.DeletedAt != nil {
		payload["deleted_at"] = event.DeletedAt
	}
	return s.recordEntitySyncChange(ctx, EntityEvent, event.ID, operation, event.Version, event.DataLevel, payload)
}

func (s *Service) recordIntentSyncChange(ctx context.Context, intent domain.StewardIntent, operation string) error {
	payload := map[string]any{
		"type":             intent.Type,
		"title":            intent.Title,
		"summary":          intent.Summary,
		"reason":           intent.Reason,
		"suggested_action": intent.SuggestedAction,
		"risk_level":       intent.RiskLevel,
		"status":           intent.Status,
		"source":           intent.Source,
		"data_level":       intent.DataLevel,
		"permission_level": intent.PermissionLevel,
		"confidence":       intent.Confidence,
		"user_confirmed":   intent.UserConfirmed,
		"version":          intent.Version,
		"created_at":       intent.CreatedAt,
		"updated_at":       intent.UpdatedAt,
	}
	if intent.DeletedAt != nil {
		payload["deleted_at"] = intent.DeletedAt
	}
	return s.recordEntitySyncChange(ctx, EntityIntent, intent.ID, operation, intent.Version, intent.DataLevel, payload)
}

func (s *Service) recordMemorySyncChange(ctx context.Context, memory domain.StewardMemory, operation string) error {
	payload := map[string]any{
		"type":             memory.Type,
		"title":            memory.Title,
		"summary":          memory.Summary,
		"content":          memory.Content,
		"scope":            memory.Scope,
		"status":           memory.Status,
		"source":           memory.Source,
		"data_level":       memory.DataLevel,
		"permission_level": memory.PermissionLevel,
		"confidence":       memory.Confidence,
		"user_confirmed":   memory.UserConfirmed,
		"version":          memory.Version,
		"created_at":       memory.CreatedAt,
		"updated_at":       memory.UpdatedAt,
	}
	if memory.LastVerifiedAt != nil {
		payload["last_verified_at"] = memory.LastVerifiedAt
	}
	if memory.DeletedAt != nil {
		payload["deleted_at"] = memory.DeletedAt
	}
	return s.recordEntitySyncChange(ctx, EntityMemory, memory.ID, operation, memory.Version, memory.DataLevel, payload)
}

func (s *Service) recordKnowledgeItemSyncChange(ctx context.Context, item domain.StewardKnowledgeItem, operation string) error {
	payload := map[string]any{
		"type":             item.Type,
		"title":            item.Title,
		"summary":          item.Summary,
		"source":           item.Source,
		"original_uri":     item.OriginalURI,
		"import_method":    item.ImportMethod,
		"content_hash":     item.ContentHash,
		"status":           item.Status,
		"data_level":       item.DataLevel,
		"permission_level": item.PermissionLevel,
		"allow_index":      item.AllowIndex,
		"user_confirmed":   item.UserConfirmed,
		"version":          item.Version,
		"created_at":       item.CreatedAt,
		"updated_at":       item.UpdatedAt,
	}
	if item.DeletedAt != nil {
		payload["deleted_at"] = item.DeletedAt
	}
	return s.recordEntitySyncChange(ctx, EntityKnowledgeItem, item.ID, operation, item.Version, item.DataLevel, payload)
}
