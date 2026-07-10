package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

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
