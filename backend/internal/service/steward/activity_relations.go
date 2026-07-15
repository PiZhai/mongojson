package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

var allowedStewardEntityTypes = map[string]bool{
	"user": true, "person": true, "project": true, "application": true, "device": true,
	"file": true, "repository": true, "website": true, "topic": true, "location": true,
	"meeting": true, "goal": true, "activity": true,
}

var allowedStewardRelationTypes = map[string]bool{
	"derived_from": true, "belongs_to": true, "occurred_in": true, "mentions": true,
	"supports": true, "contradicts": true, "precedes": true, "follows": true,
	"repeats": true, "supersedes": true,
}

func normalizeEntityKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func (s *Service) upsertEntity(ctx context.Context, entityType, canonicalKey, displayName, summary, dataLevel string, confidence float64, seenAt time.Time) (domain.StewardEntity, error) {
	entityType = strings.ToLower(strings.TrimSpace(entityType))
	canonicalKey = normalizeEntityKey(canonicalKey)
	if !allowedStewardEntityTypes[entityType] {
		return domain.StewardEntity{}, fmt.Errorf("unsupported steward entity type %q", entityType)
	}
	if canonicalKey == "" {
		return domain.StewardEntity{}, fmt.Errorf("entity canonical key is required")
	}
	if confidence <= 0 {
		confidence = 0.5
	}
	displayName = defaultString(strings.TrimSpace(displayName), canonicalKey)
	dataLevel = defaultString(dataLevel, DataD2)
	var item domain.StewardEntity
	err := s.db.Pool.QueryRow(ctx, `
		insert into steward_entities (
			id, type, canonical_key, display_name, summary, data_level, status, confidence,
			evidence_count, first_seen_at, last_seen_at, created_at, updated_at
		) values ($1,$2,$3,$4,$5,$6,'active',$7,1,$8,$8,$8,$8)
		on conflict (type, canonical_key) do update set
			display_name = case when excluded.display_name <> '' then excluded.display_name else steward_entities.display_name end,
			summary = case when excluded.summary <> '' then excluded.summary else steward_entities.summary end,
			data_level = case when excluded.data_level > steward_entities.data_level then excluded.data_level else steward_entities.data_level end,
			confidence = greatest(steward_entities.confidence, excluded.confidence),
			evidence_count = steward_entities.evidence_count + 1,
			last_seen_at = greatest(steward_entities.last_seen_at, excluded.last_seen_at),
			updated_at = now()
		returning id::text, type, canonical_key, display_name, summary, data_level, status,
		          confidence, evidence_count, first_seen_at, last_seen_at, last_verified_at,
		          created_at, updated_at
	`, uuid.NewString(), entityType, canonicalKey, displayName, strings.TrimSpace(summary), dataLevel,
		clamp01(confidence), seenAt).Scan(&item.ID, &item.Type, &item.CanonicalKey, &item.DisplayName,
		&item.Summary, &item.DataLevel, &item.Status, &item.Confidence, &item.EvidenceCount,
		&item.FirstSeenAt, &item.LastSeenAt, &item.LastVerifiedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.StewardEntity{}, fmt.Errorf("upsert steward entity: %w", err)
	}
	return item, nil
}

func (s *Service) linkObservationEntities(ctx context.Context, observationID string, observationTime time.Time, dataLevel string, hints []ObservationEntityHint) error {
	device, err := s.upsertEntity(ctx, "device", s.agentIDValue(), s.agentIDValue(), "本机采集设备", DataD2, 1, observationTime)
	if err != nil {
		return err
	}
	for _, hint := range hints {
		confidence := 1.0
		if hint.Inferred {
			confidence = 0.45
		}
		entity, err := s.upsertEntity(ctx, hint.Type, hint.CanonicalKey, hint.DisplayName, hint.Summary, dataLevel, confidence, observationTime)
		if err != nil {
			return err
		}
		if _, err := s.upsertRelationWithObservation(ctx, entity.ID, device.ID, "occurred_in", dataLevel, hint.Inferred, observationID, observationTime, "采集设备证据", confidence); err != nil {
			return err
		}
		if strings.TrimSpace(hint.RelationType) == "" {
			continue
		}
		target, err := s.upsertEntity(ctx, hint.TargetType, hint.TargetCanonicalKey, hint.TargetDisplayName, "", dataLevel, confidence, observationTime)
		if err != nil {
			return err
		}
		if _, err := s.upsertRelationWithObservation(ctx, entity.ID, target.ID, hint.RelationType, dataLevel, hint.Inferred, observationID, observationTime, "观察中显式关联", confidence); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertRelationWithObservation(ctx context.Context, sourceID, targetID, relationType, dataLevel string, inferred bool, observationID string, observationTime time.Time, summary string, confidence float64) (domain.StewardRelation, error) {
	relationType = strings.TrimSpace(relationType)
	if !allowedStewardRelationTypes[relationType] {
		return domain.StewardRelation{}, fmt.Errorf("unsupported steward relation type %q", relationType)
	}
	status, inferenceState := "active", "confirmed"
	if inferred {
		status, inferenceState = "candidate", "candidate"
	}
	var relation domain.StewardRelation
	err := s.db.Pool.QueryRow(ctx, `
		insert into steward_relations (
			id, source_entity_id, target_entity_id, relation_type, confidence, evidence_count,
			first_seen_at, last_seen_at, valid_from, data_level, status, inference_state, created_at, updated_at
		) values ($1,$2,$3,$4,$5,0,$6,$6,$6,$7,$8,$9,$6,$6)
		on conflict (source_entity_id, target_entity_id, relation_type) do update set
			confidence = greatest(steward_relations.confidence, excluded.confidence),
			last_seen_at = greatest(steward_relations.last_seen_at, excluded.last_seen_at),
			data_level = case when excluded.data_level > steward_relations.data_level then excluded.data_level else steward_relations.data_level end,
			updated_at = now()
		returning id::text, source_entity_id::text, target_entity_id::text, relation_type,
		          confidence, evidence_count, first_seen_at, last_seen_at, valid_from, valid_to,
		          data_level, status, inference_state, created_at, updated_at
	`, uuid.NewString(), sourceID, targetID, relationType, clamp01(confidence), observationTime,
		dataLevel, status, inferenceState).Scan(&relation.ID, &relation.SourceEntityID,
		&relation.TargetEntityID, &relation.RelationType, &relation.Confidence, &relation.EvidenceCount,
		&relation.FirstSeenAt, &relation.LastSeenAt, &relation.ValidFrom, &relation.ValidTo,
		&relation.DataLevel, &relation.Status, &relation.InferenceState, &relation.CreatedAt, &relation.UpdatedAt)
	if err != nil {
		return domain.StewardRelation{}, fmt.Errorf("upsert steward relation: %w", err)
	}
	evidenceID := uuid.NewString()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_relation_evidence (
			id, relation_id, observation_id, observation_time, evidence_type, summary, confidence, created_at
		) values ($1,$2,$3,$4,'observation',$5,$6,$4)
	`, evidenceID, relation.ID, observationID, observationTime, strings.TrimSpace(summary), clamp01(confidence))
	if err != nil {
		return domain.StewardRelation{}, fmt.Errorf("record relation evidence: %w", err)
	}
	_, err = s.db.Pool.Exec(ctx, `
		update steward_relations set evidence_count = (
			select count(*) from steward_relation_evidence where relation_id = $1
		), updated_at = now() where id = $1
	`, relation.ID)
	if err != nil {
		return domain.StewardRelation{}, err
	}
	relation.EvidenceCount++
	return relation, nil
}

func (s *Service) ListEntities(ctx context.Context, limit int) ([]domain.StewardEntity, error) {
	limit = normalizeLimit(limit, 100, 500)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, type, canonical_key, display_name, summary, data_level, status,
		       confidence, evidence_count, first_seen_at, last_seen_at, last_verified_at,
		       created_at, updated_at
		from steward_entities where status <> 'deleted'
		order by last_seen_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardEntity{}
	for rows.Next() {
		item, err := scanStewardEntity(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanStewardEntity(row scanner) (domain.StewardEntity, error) {
	var item domain.StewardEntity
	err := row.Scan(&item.ID, &item.Type, &item.CanonicalKey, &item.DisplayName, &item.Summary,
		&item.DataLevel, &item.Status, &item.Confidence, &item.EvidenceCount, &item.FirstSeenAt,
		&item.LastSeenAt, &item.LastVerifiedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *Service) ListEntityRelations(ctx context.Context, entityID string, limit int) ([]domain.StewardRelation, error) {
	limit = normalizeLimit(limit, 100, 300)
	rows, err := s.db.Pool.Query(ctx, `
		select r.id::text, r.source_entity_id::text, r.target_entity_id::text, r.relation_type,
		       r.confidence, r.evidence_count, r.first_seen_at, r.last_seen_at, r.valid_from,
		       r.valid_to, r.data_level, r.status, r.inference_state, r.created_at, r.updated_at,
		       se.id::text, se.type, se.canonical_key, se.display_name, se.summary, se.data_level,
		       se.status, se.confidence, se.evidence_count, se.first_seen_at, se.last_seen_at,
		       se.last_verified_at, se.created_at, se.updated_at,
		       te.id::text, te.type, te.canonical_key, te.display_name, te.summary, te.data_level,
		       te.status, te.confidence, te.evidence_count, te.first_seen_at, te.last_seen_at,
		       te.last_verified_at, te.created_at, te.updated_at
		from steward_relations r
		join steward_entities se on se.id = r.source_entity_id
		join steward_entities te on te.id = r.target_entity_id
		where r.source_entity_id::text = $1 or r.target_entity_id::text = $1
		order by r.last_seen_at desc limit $2
	`, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardRelation{}
	for rows.Next() {
		var item domain.StewardRelation
		var source, target domain.StewardEntity
		if err := rows.Scan(&item.ID, &item.SourceEntityID, &item.TargetEntityID, &item.RelationType,
			&item.Confidence, &item.EvidenceCount, &item.FirstSeenAt, &item.LastSeenAt,
			&item.ValidFrom, &item.ValidTo, &item.DataLevel, &item.Status, &item.InferenceState,
			&item.CreatedAt, &item.UpdatedAt,
			&source.ID, &source.Type, &source.CanonicalKey, &source.DisplayName, &source.Summary,
			&source.DataLevel, &source.Status, &source.Confidence, &source.EvidenceCount,
			&source.FirstSeenAt, &source.LastSeenAt, &source.LastVerifiedAt, &source.CreatedAt, &source.UpdatedAt,
			&target.ID, &target.Type, &target.CanonicalKey, &target.DisplayName, &target.Summary,
			&target.DataLevel, &target.Status, &target.Confidence, &target.EvidenceCount,
			&target.FirstSeenAt, &target.LastSeenAt, &target.LastVerifiedAt, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return nil, err
		}
		item.SourceEntity, item.TargetEntity = &source, &target
		evidence, err := s.listRelationEvidence(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Evidence = evidence
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listRelationEvidence(ctx context.Context, relationID string) ([]domain.StewardRelationEvidence, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, relation_id::text, source_ref_id::text, observation_id::text,
		       observation_time, evidence_type, summary, confidence, created_at
		from steward_relation_evidence where relation_id = $1
		order by created_at desc limit 50
	`, relationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardRelationEvidence{}
	for rows.Next() {
		var item domain.StewardRelationEvidence
		if err := rows.Scan(&item.ID, &item.RelationID, &item.SourceRefID, &item.ObservationID,
			&item.ObservationTime, &item.EvidenceType, &item.Summary, &item.Confidence, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
