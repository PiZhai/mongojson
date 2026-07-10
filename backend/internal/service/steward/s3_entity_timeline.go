package steward

import (
	"context"
	"time"

	"mongojson/backend/internal/domain"
)

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
