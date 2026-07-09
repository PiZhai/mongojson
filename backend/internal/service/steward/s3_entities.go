package steward

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

func (s *Service) applyS2EntitySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	switch change.EntityType {
	case EntityEvent:
		return s.applyEventSyncChange(ctx, change)
	case EntityIntent:
		return s.applyIntentSyncChange(ctx, change)
	case EntityMemory:
		return s.applyMemorySyncChange(ctx, change)
	case EntityKnowledgeItem:
		return s.applyKnowledgeItemSyncChange(ctx, change)
	case EntitySourceRef:
		return s.applySourceRefSyncChange(ctx, change)
	case EntityDataTag:
		return s.applyDataTagSyncChange(ctx, change)
	case EntityEntityTag:
		return s.applyEntityTagSyncChange(ctx, change)
	case EntityTimeline:
		return s.applyTimelineSegmentSyncChange(ctx, change)
	case EntityAuditSummary:
		return s.applyAuditSummarySyncChange(ctx, change)
	case EntityDeviceRevoke:
		return s.applyDeviceRevocationSyncChange(ctx, change)
	case EntityDeviceCapability:
		return s.applyDeviceCapabilitySyncChange(ctx, change)
	default:
		if err := s.markSyncChange(ctx, change.ID, SyncStored, nil, false); err != nil {
			return false, domain.StewardSyncConflict{}, err
		}
		return false, domain.StewardSyncConflict{}, nil
	}
}

func (s *Service) applyAuditSummarySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	var targetID *string
	if rawTargetID := stringPayload(change.Payload, "target_id", ""); rawTargetID != "" {
		if _, err := uuid.Parse(rawTargetID); err == nil {
			targetID = &rawTargetID
		}
	}
	var errorSummary *string
	if rawError := stringPayload(change.Payload, "error_summary", ""); rawError != "" {
		errorSummary = &rawError
	}
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_audit_logs (
			id, occurred_at, actor, action, target_type, target_id, source, permission_level, data_level,
			input_summary, output_summary, before_summary, after_summary, reason, user_confirmed, syncable,
			version, device_id, result_status, error_summary
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,'',$10,'','',$11,$12,false,$13,$14,$15,$16)
		on conflict (id) do nothing
	`, change.EntityID,
		timePayload(change.Payload, "occurred_at", change.CreatedAt),
		stringPayload(change.Payload, "actor", "remote"),
		stringPayload(change.Payload, "action", "remote.audit"),
		stringPayload(change.Payload, "target_type", "unknown"),
		targetID,
		stringPayload(change.Payload, "source", "sync"),
		stringPayload(change.Payload, "permission_level", PermissionA1),
		defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "output_summary", ""),
		stringPayload(change.Payload, "reason", ""),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version,
		change.OriginDeviceID,
		stringPayload(change.Payload, "result_status", ResultOK),
		errorSummary)
	if err != nil {
		return s.markApplyError(ctx, change, "apply audit summary sync change", err)
	}
	if err := s.markSyncChange(ctx, change.ID, SyncApplied, nil, true); err != nil {
		return false, domain.StewardSyncConflict{}, err
	}
	return true, domain.StewardSyncConflict{}, nil
}

func (s *Service) applyDeviceRevocationSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	deviceID := strings.TrimSpace(stringPayload(change.Payload, "device_id", ""))
	if deviceID == "" {
		deviceID = strings.TrimSpace(stringPayload(change.Payload, "revoked_device_id", ""))
	}
	if deviceID == "" {
		return s.markApplyError(ctx, change, "apply device revocation sync change", fmt.Errorf("device_id is required"))
	}
	if deviceID == s.agentIDValue() {
		return s.markApplyError(ctx, change, "apply device revocation sync change", fmt.Errorf("remote revocation cannot disable local device"))
	}
	if trustStatus := strings.TrimSpace(stringPayload(change.Payload, "trust_status", DeviceRevoked)); trustStatus != DeviceRevoked {
		return s.markApplyError(ctx, change, "apply device revocation sync change", fmt.Errorf("device revocation sync only accepts revoked status"))
	}

	now := time.Now().UTC()
	revokedAt := timePayload(change.Payload, "revoked_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_devices (
			id, device_name, platform, role, trust_status, sync_enabled, permission_level, public_key, api_base_url,
			last_seen_at, revoked_at, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,false,$6,$7,$8,$9,$10,$11,$11)
		on conflict (id) do update
		set trust_status = $5,
		    sync_enabled = false,
		    revoked_at = coalesce(steward_devices.revoked_at, excluded.revoked_at),
		    updated_at = excluded.updated_at
	`, deviceID,
		defaultString(stringPayload(change.Payload, "device_name", ""), deviceID),
		defaultString(stringPayload(change.Payload, "platform", ""), "unknown"),
		DeviceRolePeer,
		DeviceRevoked,
		defaultString(stringPayload(change.Payload, "permission_level", ""), PermissionA3),
		stringPayload(change.Payload, "public_key", ""),
		strings.TrimRight(strings.TrimSpace(stringPayload(change.Payload, "api_base_url", "")), "/"),
		nullableTimePayload(change.Payload, "last_seen_at"),
		revokedAt,
		now)
	if err != nil {
		return s.markApplyError(ctx, change, "apply device revocation sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

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

func (s *Service) applySourceRefSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if change.Operation == SyncDelete {
		if _, err := s.db.Pool.Exec(ctx, `delete from steward_source_refs where id = $1`, change.EntityID); err != nil {
			return s.markApplyError(ctx, change, "apply source ref delete sync change", err)
		}
		return s.finishAppliedSyncChange(ctx, change)
	}
	createdAt := timePayload(change.Payload, "created_at", time.Now().UTC())
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_source_refs (
			id, target_type, target_id, source_type, source_id, location, summary,
			confidence, sensitive, displayable, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (id) do update
		set target_type = excluded.target_type,
		    target_id = excluded.target_id,
		    source_type = excluded.source_type,
		    source_id = excluded.source_id,
		    location = excluded.location,
		    summary = excluded.summary,
		    confidence = excluded.confidence,
		    sensitive = excluded.sensitive,
		    displayable = excluded.displayable
	`, change.EntityID,
		stringPayload(change.Payload, "target_type", "unknown"),
		stringPayload(change.Payload, "target_id", stringPayload(change.Payload, "entity_id", change.EntityID)),
		stringPayload(change.Payload, "source_type", "sync"),
		stringPayload(change.Payload, "source_id", ""),
		stringPayload(change.Payload, "location", ""),
		stringPayload(change.Payload, "summary", ""),
		floatPayload(change.Payload, "confidence", 1),
		boolPayload(change.Payload, "sensitive", false),
		boolPayload(change.Payload, "displayable", true),
		createdAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply source ref sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) applyDataTagSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if change.Operation == SyncDelete {
		var canonicalID string
		err := s.db.Pool.QueryRow(ctx, `select tag_id::text from steward_data_tag_aliases where alias_id = $1`, change.EntityID).Scan(&canonicalID)
		if err == nil {
			if _, err := s.db.Pool.Exec(ctx, `delete from steward_data_tag_aliases where alias_id = $1`, change.EntityID); err != nil {
				return s.markApplyError(ctx, change, "delete data tag alias sync change", err)
			}
			return s.finishAppliedSyncChange(ctx, change)
		}
		if err != pgx.ErrNoRows {
			return s.markApplyError(ctx, change, "resolve data tag alias delete", err)
		}
		if _, err := s.db.Pool.Exec(ctx, `delete from steward_data_tags where id = $1`, change.EntityID); err != nil {
			return s.markApplyError(ctx, change, "apply data tag delete sync change", err)
		}
		return s.finishAppliedSyncChange(ctx, change)
	}
	now := time.Now().UTC()
	incoming := domain.StewardDataTag{
		ID:          change.EntityID,
		Name:        stringPayload(change.Payload, "name", "同步标签"),
		Type:        stringPayload(change.Payload, "type", "normal"),
		Color:       stringPayload(change.Payload, "color", ""),
		Description: stringPayload(change.Payload, "description", ""),
		CreatedAt:   timePayload(change.Payload, "created_at", now),
		UpdatedAt:   timePayload(change.Payload, "updated_at", now),
	}
	existing, found, err := s.findDataTagByIDOrName(ctx, incoming.ID, incoming.Name)
	if err != nil {
		return s.markApplyError(ctx, change, "find canonical data tag", err)
	}
	if found {
		return s.finishMergedDataTagSyncChange(ctx, change, incoming, existing)
	}
	tag, err := s.db.Pool.Exec(ctx, `
		insert into steward_data_tags (id, name, type, color, description, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7)
		on conflict do nothing
	`, incoming.ID, incoming.Name, incoming.Type, incoming.Color, incoming.Description, incoming.CreatedAt, incoming.UpdatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply data tag sync change", err)
	}
	if tag.RowsAffected() == 0 {
		existing, found, err = s.findDataTagByIDOrName(ctx, incoming.ID, incoming.Name)
		if err != nil || !found {
			return s.markApplyError(ctx, change, "resolve concurrent canonical data tag", defaultError(err, "canonical data tag not found"))
		}
		return s.finishMergedDataTagSyncChange(ctx, change, incoming, existing)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) applyEntityTagSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if change.Operation == SyncDelete {
		tagID := stringPayload(change.Payload, "tag_id", "")
		if canonicalID, found, err := s.findCanonicalTagID(ctx, tagID, stringPayload(change.Payload, "tag_name", "")); err != nil {
			return s.markApplyError(ctx, change, "resolve entity tag delete alias", err)
		} else if found {
			tagID = canonicalID
		}
		if _, err := s.db.Pool.Exec(ctx, `
			delete from steward_entity_tags
			where entity_type = $1 and entity_id::text = $2 and tag_id::text = $3
		`, stringPayload(change.Payload, "entity_type", ""), stringPayload(change.Payload, "entity_id", ""), tagID); err != nil {
			return s.markApplyError(ctx, change, "apply entity tag delete sync change", err)
		}
		return s.finishAppliedSyncChange(ctx, change)
	}
	tagID, err := s.resolveIncomingTagID(ctx, change.Payload)
	if err != nil {
		return s.markApplyError(ctx, change, "resolve entity tag sync change", err)
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_entity_tags (entity_type, entity_id, tag_id, source, confidence, created_at)
		values ($1,$2,$3,$4,$5,$6)
		on conflict (entity_type, entity_id, tag_id) do update
		set source = excluded.source,
		    confidence = excluded.confidence,
		    created_at = excluded.created_at
	`, stringPayload(change.Payload, "entity_type", ""),
		stringPayload(change.Payload, "entity_id", ""),
		tagID,
		stringPayload(change.Payload, "source", "sync"),
		floatPayload(change.Payload, "confidence", 1),
		timePayload(change.Payload, "created_at", time.Now().UTC()))
	if err != nil {
		return s.markApplyError(ctx, change, "apply entity tag sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) applyTimelineSegmentSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}
	if change.Operation == SyncDelete {
		applied, conflict, err := s.applySoftDeleteSyncChange(ctx, "steward_timeline_segments", change)
		if err == nil {
			_, _ = s.db.Pool.Exec(ctx, `delete from steward_timeline_pending_events where segment_id = $1`, change.EntityID)
		}
		return applied, conflict, err
	}
	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_timeline_segments (
			id, type, title, summary, status, source, data_level, permission_level, device_id,
			start_at, end_at, confidence, user_confirmed, version, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    summary = excluded.summary,
		    status = excluded.status,
		    source = excluded.source,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    device_id = excluded.device_id,
		    start_at = excluded.start_at,
		    end_at = excluded.end_at,
		    confidence = excluded.confidence,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_timeline_segments.version, excluded.version),
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID,
		stringPayload(change.Payload, "type", "remote_cluster"),
		stringPayload(change.Payload, "title", "同步时间线片段"),
		stringPayload(change.Payload, "summary", ""),
		stringPayload(change.Payload, "status", StatusActive),
		"sync",
		defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		change.OriginDeviceID,
		nullableTimePayload(change.Payload, "start_at"),
		nullableTimePayload(change.Payload, "end_at"),
		floatPayload(change.Payload, "confidence", 1),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version, createdAt, updatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply timeline segment sync change", err)
	}
	for _, eventID := range stringSlicePayload(change.Payload, "event_ids") {
		if err := s.linkOrQueueTimelineEvent(ctx, change.EntityID, eventID, change.OriginDeviceID); err != nil {
			return s.markApplyError(ctx, change, "link timeline segment sync change", err)
		}
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) linkOrQueueTimelineEvent(ctx context.Context, segmentID string, eventID string, originDeviceID string) error {
	tag, err := s.db.Pool.Exec(ctx, `
		insert into steward_timeline_segment_events (segment_id, event_id)
		select $1, $2
		where exists (select 1 from steward_events where id = $2)
		on conflict do nothing
	`, segmentID, eventID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		_, err = s.db.Pool.Exec(ctx, `delete from steward_timeline_pending_events where segment_id = $1 and event_id = $2`, segmentID, eventID)
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_timeline_pending_events (segment_id, event_id, origin_device_id, created_at)
		select $1,$2,$3,$4
		where not exists (select 1 from steward_events where id = $2)
		on conflict (segment_id, event_id) do update
		set origin_device_id = excluded.origin_device_id
	`, segmentID, eventID, originDeviceID, time.Now().UTC())
	return err
}

func (s *Service) resolvePendingTimelineEventLinks(ctx context.Context, eventID string) error {
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_timeline_segment_events (segment_id, event_id)
		select pending.segment_id, pending.event_id
		from steward_timeline_pending_events pending
		join steward_timeline_segments segment on segment.id = pending.segment_id and segment.deleted_at is null
		join steward_events event on event.id = pending.event_id and event.deleted_at is null
		where pending.event_id = $1
		on conflict do nothing
	`, eventID); err != nil {
		return err
	}
	_, err := s.db.Pool.Exec(ctx, `
		delete from steward_timeline_pending_events pending
		where pending.event_id = $1
		  and exists (
		    select 1
		    from steward_timeline_segment_events linked
		    where linked.segment_id = pending.segment_id and linked.event_id = pending.event_id
		  )
	`, eventID)
	return err
}

func (s *Service) resolveIncomingTagID(ctx context.Context, payload map[string]any) (string, error) {
	tagID := stringPayload(payload, "tag_id", "")
	tagName := stringPayload(payload, "tag_name", "")
	if canonicalID, found, err := s.findCanonicalTagID(ctx, tagID, tagName); err != nil {
		return "", err
	} else if found {
		canonical, err := s.getDataTag(ctx, canonicalID)
		if err != nil {
			return "", err
		}
		incoming := domain.StewardDataTag{
			ID:          tagID,
			Name:        optionalStringPayload(payload, "tag_name", canonical.Name),
			Type:        optionalStringPayload(payload, "tag_type", canonical.Type),
			Color:       optionalStringPayload(payload, "tag_color", canonical.Color),
			Description: optionalStringPayload(payload, "tag_description", canonical.Description),
		}
		if !dataTagMetadataEqual(canonical, incoming) {
			return "", fmt.Errorf("incoming tag metadata conflicts with canonical tag %s", canonical.ID)
		}
		if tagID != "" && tagID != canonical.ID {
			if err := s.ensureDataTagAlias(ctx, tagID, canonical.ID, "sync"); err != nil {
				return "", err
			}
		}
		return canonical.ID, nil
	}
	if tagID == "" {
		tagID = uuid.NewString()
	}
	incoming := domain.StewardDataTag{
		ID:          tagID,
		Name:        defaultString(tagName, "同步标签"),
		Type:        stringPayload(payload, "tag_type", "normal"),
		Color:       stringPayload(payload, "tag_color", ""),
		Description: stringPayload(payload, "tag_description", ""),
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_data_tags (id, name, type, color, description, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$6)
		on conflict do nothing
	`, incoming.ID, incoming.Name, incoming.Type, incoming.Color, incoming.Description,
		time.Now().UTC()); err != nil {
		return "", err
	}
	canonicalID, found, err := s.findCanonicalTagID(ctx, incoming.ID, incoming.Name)
	if err != nil || !found {
		return "", defaultError(err, "canonical tag not found after insert")
	}
	canonical, err := s.getDataTag(ctx, canonicalID)
	if err != nil {
		return "", err
	}
	if !dataTagMetadataEqual(canonical, incoming) {
		return "", fmt.Errorf("incoming tag metadata conflicts with canonical tag %s", canonical.ID)
	}
	if incoming.ID != canonical.ID {
		if err := s.ensureDataTagAlias(ctx, incoming.ID, canonical.ID, "sync"); err != nil {
			return "", err
		}
	}
	return canonical.ID, nil
}

func (s *Service) findDataTagByIDOrName(ctx context.Context, id string, name string) (domain.StewardDataTag, bool, error) {
	if strings.TrimSpace(id) != "" {
		item, err := s.getDataTag(ctx, id)
		if err == nil {
			return item, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardDataTag{}, false, err
		}
	}
	if strings.TrimSpace(name) == "" {
		return domain.StewardDataTag{}, false, nil
	}
	var item domain.StewardDataTag
	err := s.db.Pool.QueryRow(ctx, `
		select id, name, type, color, description, created_at, updated_at
		from steward_data_tags where name = $1
	`, name).Scan(&item.ID, &item.Name, &item.Type, &item.Color, &item.Description, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		return domain.StewardDataTag{}, false, nil
	}
	return item, err == nil, err
}

func (s *Service) findCanonicalTagID(ctx context.Context, id string, name string) (string, bool, error) {
	if strings.TrimSpace(id) != "" {
		var canonicalID string
		err := s.db.Pool.QueryRow(ctx, `select id::text from steward_data_tags where id = $1`, id).Scan(&canonicalID)
		if err == nil {
			return canonicalID, true, nil
		}
		if err != pgx.ErrNoRows {
			return "", false, err
		}
		err = s.db.Pool.QueryRow(ctx, `select tag_id::text from steward_data_tag_aliases where alias_id = $1`, id).Scan(&canonicalID)
		if err == nil {
			return canonicalID, true, nil
		}
		if err != pgx.ErrNoRows {
			return "", false, err
		}
	}
	if strings.TrimSpace(name) != "" {
		var canonicalID string
		err := s.db.Pool.QueryRow(ctx, `select id::text from steward_data_tags where name = $1`, name).Scan(&canonicalID)
		if err == nil {
			return canonicalID, true, nil
		}
		if err != pgx.ErrNoRows {
			return "", false, err
		}
	}
	return "", false, nil
}

func (s *Service) finishMergedDataTagSyncChange(ctx context.Context, change domain.StewardSyncChange, incoming domain.StewardDataTag, canonical domain.StewardDataTag) (bool, domain.StewardSyncConflict, error) {
	if !dataTagMetadataEqual(canonical, incoming) {
		conflict, _, err := s.registerIncomingSyncConflict(ctx, change, "incoming data_tag matches an existing id or name but metadata differs")
		return false, conflict, err
	}
	if incoming.ID != canonical.ID {
		if err := s.ensureDataTagAlias(ctx, incoming.ID, canonical.ID, change.OriginDeviceID); err != nil {
			return s.markApplyError(ctx, change, "store data tag alias", err)
		}
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) ensureDataTagAlias(ctx context.Context, aliasID string, canonicalID string, originDeviceID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_data_tag_aliases (alias_id, tag_id, origin_device_id, created_at)
		values ($1,$2,$3,$4)
		on conflict (alias_id) do update
		set tag_id = excluded.tag_id, origin_device_id = excluded.origin_device_id
	`, aliasID, canonicalID, originDeviceID, time.Now().UTC())
	return err
}

func dataTagMetadataEqual(left domain.StewardDataTag, right domain.StewardDataTag) bool {
	return strings.TrimSpace(left.Name) == strings.TrimSpace(right.Name) &&
		strings.TrimSpace(left.Type) == strings.TrimSpace(right.Type) &&
		strings.TrimSpace(left.Color) == strings.TrimSpace(right.Color) &&
		strings.TrimSpace(left.Description) == strings.TrimSpace(right.Description)
}

func defaultError(err error, message string) error {
	if err != nil {
		return err
	}
	return errors.New(message)
}

func optionalStringPayload(payload map[string]any, key string, fallback string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return fallback
	}
	text, ok := value.(string)
	if !ok {
		return fallback
	}
	return strings.TrimSpace(text)
}

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

func (s *Service) recordAuditSummarySyncChange(ctx context.Context, audit domain.StewardAuditLog) error {
	if !audit.Syncable || strings.EqualFold(audit.Actor, "sync") || dataLevelRank(audit.DataLevel) > dataLevelRank(DataD1) {
		return nil
	}
	targetID := ""
	if audit.TargetID != nil {
		targetID = *audit.TargetID
	}
	errorSummary := ""
	if audit.ErrorSummary != nil {
		errorSummary = truncateSyncSummary(*audit.ErrorSummary, 500)
	}
	payload := map[string]any{
		"occurred_at":      audit.OccurredAt,
		"actor":            audit.Actor,
		"action":           audit.Action,
		"target_type":      audit.TargetType,
		"target_id":        targetID,
		"source":           audit.Source,
		"permission_level": audit.PermissionLevel,
		"data_level":       audit.DataLevel,
		"output_summary":   truncateSyncSummary(audit.OutputSummary, 500),
		"reason":           truncateSyncSummary(audit.Reason, 500),
		"user_confirmed":   audit.UserConfirmed,
		"result_status":    audit.ResultStatus,
		"error_summary":    errorSummary,
	}
	return s.recordEntitySyncChange(ctx, EntityAuditSummary, audit.ID, SyncCreate, audit.Version, audit.DataLevel, payload)
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

func (s *Service) recordTimelineSegmentSyncChange(ctx context.Context, item domain.StewardTimelineSegment, operation string) error {
	payload := map[string]any{
		"type":             item.Type,
		"title":            item.Title,
		"summary":          item.Summary,
		"status":           item.Status,
		"source":           item.Source,
		"data_level":       item.DataLevel,
		"permission_level": item.PermissionLevel,
		"confidence":       item.Confidence,
		"user_confirmed":   item.UserConfirmed,
		"version":          item.Version,
		"created_at":       item.CreatedAt,
		"updated_at":       item.UpdatedAt,
	}
	if item.StartAt != nil {
		payload["start_at"] = item.StartAt
	}
	if item.EndAt != nil {
		payload["end_at"] = item.EndAt
	}
	if item.DeletedAt != nil {
		payload["deleted_at"] = item.DeletedAt
	}
	if eventIDs, err := s.listTimelineSegmentEventIDs(ctx, item.ID); err == nil && len(eventIDs) > 0 {
		payload["event_ids"] = eventIDs
	}
	return s.recordEntitySyncChange(ctx, EntityTimeline, item.ID, operation, item.Version, item.DataLevel, payload)
}

func (s *Service) recordSourceRefSyncChange(ctx context.Context, item domain.StewardSourceRef, operation string) error {
	payload := map[string]any{
		"target_type": item.TargetType,
		"target_id":   item.TargetID,
		"source_type": item.SourceType,
		"source_id":   item.SourceID,
		"location":    item.Location,
		"summary":     item.Summary,
		"confidence":  item.Confidence,
		"sensitive":   item.Sensitive,
		"displayable": item.Displayable,
		"created_at":  item.CreatedAt,
	}
	return s.recordEntitySyncChange(ctx, EntitySourceRef, item.ID, operation, 1, DataD0, payload)
}

func (s *Service) recordDataTagSyncChange(ctx context.Context, item domain.StewardDataTag, operation string) error {
	payload := map[string]any{
		"name":        item.Name,
		"type":        item.Type,
		"color":       item.Color,
		"description": item.Description,
		"created_at":  item.CreatedAt,
		"updated_at":  item.UpdatedAt,
	}
	return s.recordEntitySyncChange(ctx, EntityDataTag, item.ID, operation, 1, DataD0, payload)
}

func (s *Service) recordEntityTagSyncChange(ctx context.Context, input AssignTagInput, tag domain.StewardDataTag, confidence float64, operation string) error {
	entityID := entityTagSyncEntityID(input.EntityType, input.EntityID, input.TagID)
	payload := map[string]any{
		"entity_type":     input.EntityType,
		"entity_id":       input.EntityID,
		"tag_id":          input.TagID,
		"tag_name":        tag.Name,
		"tag_type":        tag.Type,
		"tag_color":       tag.Color,
		"tag_description": tag.Description,
		"source":          defaultString(input.Source, "manual"),
		"confidence":      confidence,
		"created_at":      time.Now().UTC(),
	}
	return s.recordEntitySyncChange(ctx, EntityEntityTag, entityID, operation, 1, DataD0, payload)
}

func entityTagSyncEntityID(entityType string, entityID string, tagID string) string {
	key := strings.Join([]string{
		"steward_entity_tag",
		strings.TrimSpace(entityType),
		strings.TrimSpace(entityID),
		strings.TrimSpace(tagID),
	}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func deviceRevocationSyncEntityID(deviceID string) string {
	key := strings.Join([]string{
		"steward_device_revoke",
		strings.TrimSpace(deviceID),
	}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func (s *Service) listTimelineSegmentEventIDs(ctx context.Context, segmentID string) ([]string, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select event_id::text
		from steward_timeline_segment_events
		where segment_id = $1
		order by event_id
	`, segmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
