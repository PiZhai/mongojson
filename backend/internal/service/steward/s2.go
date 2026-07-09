package steward

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

type ConvertEventInput struct {
	TargetType string `json:"target_type"`
}

type CreateIntentInput struct {
	Type            string  `json:"type"`
	Title           string  `json:"title"`
	Summary         string  `json:"summary"`
	Reason          string  `json:"reason"`
	SuggestedAction string  `json:"suggested_action"`
	RiskLevel       string  `json:"risk_level"`
	Source          string  `json:"source"`
	DataLevel       string  `json:"data_level"`
	PermissionLevel string  `json:"permission_level"`
	Confidence      float64 `json:"confidence"`
	UserConfirmed   *bool   `json:"user_confirmed"`
}

type CreateMemoryInput struct {
	Type            string  `json:"type"`
	Title           string  `json:"title"`
	Summary         string  `json:"summary"`
	Content         string  `json:"content"`
	Scope           string  `json:"scope"`
	Source          string  `json:"source"`
	DataLevel       string  `json:"data_level"`
	PermissionLevel string  `json:"permission_level"`
	Confidence      float64 `json:"confidence"`
	UserConfirmed   *bool   `json:"user_confirmed"`
}

type CorrectMemoryInput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

type CreateKnowledgeInput struct {
	ID              string `json:"-"`
	Type            string `json:"type"`
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	Source          string `json:"source"`
	OriginalURI     string `json:"original_uri"`
	ImportMethod    string `json:"import_method"`
	DataLevel       string `json:"data_level"`
	PermissionLevel string `json:"permission_level"`
	AllowIndex      *bool  `json:"allow_index"`
	UserConfirmed   *bool  `json:"user_confirmed"`
}

type CreateSourceRefInput struct {
	TargetType  string  `json:"target_type"`
	TargetID    string  `json:"target_id"`
	SourceType  string  `json:"source_type"`
	SourceID    string  `json:"source_id"`
	Location    string  `json:"location"`
	Summary     string  `json:"summary"`
	Confidence  float64 `json:"confidence"`
	Sensitive   bool    `json:"sensitive"`
	Displayable *bool   `json:"displayable"`
}

type CreateDataTagInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type AssignTagInput struct {
	EntityType string  `json:"entity_type"`
	EntityID   string  `json:"entity_id"`
	TagID      string  `json:"tag_id"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

type SearchInput struct {
	Query      string
	EntityType string
	Status     string
	DataLevel  string
	Limit      int
}

func (s *Service) ConvertEvent(ctx context.Context, id string, input ConvertEventInput) (map[string]any, error) {
	event, err := s.getEvent(ctx, id)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(input.TargetType)
	switch target {
	case "task":
		task, err := s.CreateTask(ctx, CreateTaskInput{
			Type:            "from_event",
			Title:           event.Title,
			Description:     event.Summary,
			Priority:        "normal",
			Source:          "event",
			DataLevel:       event.DataLevel,
			PermissionLevel: PermissionA3,
			RiskLevel:       "low",
		})
		if err != nil {
			return nil, err
		}
		if _, err := s.CreateSourceRef(ctx, CreateSourceRefInput{
			TargetType: "task",
			TargetID:   task.ID,
			SourceType: "event",
			SourceID:   event.ID,
			Summary:    event.Title,
			Confidence: 1,
		}); err != nil {
			return nil, err
		}
		_, _ = s.recordEventConversion(ctx, event.ID, "task", task.ID)
		return map[string]any{"task": task}, nil
	case "intent":
		intent, err := s.CreateIntent(ctx, CreateIntentInput{
			Type:            "follow_up",
			Title:           event.Title,
			Summary:         event.Summary,
			Reason:          "由用户从事件转化",
			SuggestedAction: "确认后转为任务",
			RiskLevel:       "low",
			Source:          "event",
			DataLevel:       event.DataLevel,
			PermissionLevel: PermissionA3,
			Confidence:      0.8,
		})
		if err != nil {
			return nil, err
		}
		if _, err := s.CreateSourceRef(ctx, CreateSourceRefInput{
			TargetType: "intent",
			TargetID:   intent.ID,
			SourceType: "event",
			SourceID:   event.ID,
			Summary:    event.Title,
			Confidence: 1,
		}); err != nil {
			return nil, err
		}
		_, _ = s.recordEventConversion(ctx, event.ID, "intent", intent.ID)
		return map[string]any{"intent": intent}, nil
	case "memory":
		memory, err := s.CreateMemory(ctx, CreateMemoryInput{
			Type:            "project_fact",
			Title:           event.Title,
			Summary:         event.Summary,
			Content:         defaultString(event.Summary, event.Title),
			Scope:           "global",
			Source:          "event",
			DataLevel:       event.DataLevel,
			PermissionLevel: PermissionA3,
			Confidence:      1,
		})
		if err != nil {
			return nil, err
		}
		if _, err := s.CreateSourceRef(ctx, CreateSourceRefInput{
			TargetType: "memory",
			TargetID:   memory.ID,
			SourceType: "event",
			SourceID:   event.ID,
			Summary:    event.Title,
			Confidence: 1,
			Sensitive:  isSensitiveLevel(event.DataLevel),
		}); err != nil {
			return nil, err
		}
		_, _ = s.recordEventConversion(ctx, event.ID, "memory", memory.ID)
		return map[string]any{"memory": memory}, nil
	case "knowledge":
		knowledge, err := s.CreateKnowledgeItem(ctx, CreateKnowledgeInput{
			Type:            "note",
			Title:           event.Title,
			Summary:         event.Summary,
			Source:          "event",
			ImportMethod:    "event_conversion",
			DataLevel:       event.DataLevel,
			PermissionLevel: PermissionA3,
		})
		if err != nil {
			return nil, err
		}
		if _, err := s.CreateSourceRef(ctx, CreateSourceRefInput{
			TargetType: "knowledge_item",
			TargetID:   knowledge.ID,
			SourceType: "event",
			SourceID:   event.ID,
			Summary:    event.Title,
			Confidence: 1,
			Sensitive:  isSensitiveLevel(event.DataLevel),
		}); err != nil {
			return nil, err
		}
		_, _ = s.recordEventConversion(ctx, event.ID, "knowledge_item", knowledge.ID)
		return map[string]any{"knowledge_item": knowledge}, nil
	case "timeline":
		segment, err := s.CreateTimelineSegmentFromEvent(ctx, event)
		if err != nil {
			return nil, err
		}
		_, _ = s.recordEventConversion(ctx, event.ID, "timeline_segment", segment.ID)
		return map[string]any{"timeline_segment": segment}, nil
	default:
		return nil, fmt.Errorf("unsupported conversion target: %s", target)
	}
}

func (s *Service) ListTimelineSegments(ctx context.Context, limit int) ([]domain.StewardTimelineSegment, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select s.id, s.type, s.title, s.summary, s.status, s.source, s.data_level, s.permission_level,
		       s.device_id, s.start_at, s.end_at, s.confidence, s.user_confirmed, s.version, s.audit_id,
		       count(e.event_id)::int as event_count, s.created_at, s.updated_at, s.deleted_at
		from steward_timeline_segments s
		left join steward_timeline_segment_events e on e.segment_id = s.id
		where s.deleted_at is null and s.status <> $1
		group by s.id
		order by coalesce(s.start_at, s.created_at) desc
		limit $2
	`, StatusDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward timeline segments: %w", err)
	}
	defer rows.Close()

	items := []domain.StewardTimelineSegment{}
	for rows.Next() {
		var item domain.StewardTimelineSegment
		if err := rows.Scan(
			&item.ID,
			&item.Type,
			&item.Title,
			&item.Summary,
			&item.Status,
			&item.Source,
			&item.DataLevel,
			&item.PermissionLevel,
			&item.DeviceID,
			&item.StartAt,
			&item.EndAt,
			&item.Confidence,
			&item.UserConfirmed,
			&item.Version,
			&item.AuditID,
			&item.EventCount,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.DeletedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateTimelineSegmentFromEvent(ctx context.Context, event domain.StewardEvent) (domain.StewardTimelineSegment, error) {
	now := time.Now().UTC()
	record := domain.StewardTimelineSegment{
		ID:              uuid.NewString(),
		Type:            "manual_cluster",
		Title:           event.Title,
		Summary:         event.Summary,
		Status:          StatusActive,
		Source:          "event",
		DataLevel:       event.DataLevel,
		PermissionLevel: PermissionA3,
		DeviceID:        s.agentIDValue(),
		StartAt:         &event.CreatedAt,
		EndAt:           &event.CreatedAt,
		Confidence:      1,
		UserConfirmed:   true,
		Version:         1,
		EventCount:      1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "timeline_segment.create",
		TargetType:      "timeline_segment",
		TargetID:        &record.ID,
		Source:          "event",
		PermissionLevel: PermissionA3,
		DataLevel:       event.DataLevel,
		InputSummary:    event.Title,
		OutputSummary:   "timeline segment created from event",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardTimelineSegment{}, err
	}
	record.AuditID = &auditID
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_timeline_segments (
			id, type, title, summary, status, source, data_level, permission_level, device_id,
			start_at, end_at, confidence, user_confirmed, version, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
	`, record.ID, record.Type, record.Title, record.Summary, record.Status, record.Source,
		record.DataLevel, record.PermissionLevel, record.DeviceID, record.StartAt, record.EndAt,
		record.Confidence, record.UserConfirmed, record.Version, auditID, now); err != nil {
		return domain.StewardTimelineSegment{}, fmt.Errorf("create steward timeline segment: %w", err)
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_timeline_segment_events (segment_id, event_id)
		values ($1,$2)
		on conflict do nothing
	`, record.ID, event.ID); err != nil {
		return domain.StewardTimelineSegment{}, fmt.Errorf("link steward timeline event: %w", err)
	}
	_ = s.recordTimelineSegmentSyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) DeleteTimelineSegment(ctx context.Context, id string) error {
	if _, err := s.getTimelineSegment(ctx, id); err != nil {
		return err
	}
	if err := s.softDeleteEntity(ctx, "steward_timeline_segments", "timeline_segment", id, "timeline_segment.delete"); err != nil {
		return err
	}
	segment, err := s.getTimelineSegment(ctx, id)
	if err != nil {
		return err
	}
	_ = s.recordTimelineSegmentSyncChange(ctx, segment, SyncDelete)
	return nil
}

func (s *Service) CreateIntent(ctx context.Context, input CreateIntentInput) (domain.StewardIntent, error) {
	now := time.Now().UTC()
	confidence := input.Confidence
	if confidence <= 0 {
		confidence = 0.5
	}
	record := domain.StewardIntent{
		ID:              uuid.NewString(),
		Type:            defaultString(input.Type, "follow_up"),
		Title:           defaultString(input.Title, "候选意图"),
		Summary:         strings.TrimSpace(input.Summary),
		Reason:          strings.TrimSpace(input.Reason),
		SuggestedAction: strings.TrimSpace(input.SuggestedAction),
		RiskLevel:       defaultString(input.RiskLevel, "low"),
		Status:          StatusCandidate,
		Source:          defaultString(input.Source, "manual"),
		DataLevel:       defaultString(input.DataLevel, DataD0),
		PermissionLevel: defaultString(input.PermissionLevel, PermissionA3),
		DeviceID:        s.agentIDValue(),
		Confidence:      confidence,
		UserConfirmed:   defaultBool(input.UserConfirmed, false),
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "intent.create",
		TargetType:      "intent",
		TargetID:        &record.ID,
		Source:          record.Source,
		PermissionLevel: record.PermissionLevel,
		DataLevel:       record.DataLevel,
		InputSummary:    record.Title,
		OutputSummary:   "intent candidate created",
		UserConfirmed:   &record.UserConfirmed,
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardIntent{}, err
	}
	record.AuditID = &auditID
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_intents (
			id, type, title, summary, reason, suggested_action, risk_level, status, source,
			data_level, permission_level, device_id, confidence, user_confirmed, version, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
	`, record.ID, record.Type, record.Title, record.Summary, record.Reason, record.SuggestedAction,
		record.RiskLevel, record.Status, record.Source, record.DataLevel, record.PermissionLevel,
		record.DeviceID, record.Confidence, record.UserConfirmed, record.Version, auditID, now); err != nil {
		return domain.StewardIntent{}, fmt.Errorf("create steward intent: %w", err)
	}
	_ = s.recordIntentSyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) ListIntents(ctx context.Context, limit int) ([]domain.StewardIntent, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, type, title, summary, reason, suggested_action, risk_level, status, source,
		       data_level, permission_level, device_id, confidence, user_confirmed, version, audit_id,
		       created_at, updated_at, deleted_at
		from steward_intents
		where deleted_at is null and status <> $1
		order by
			case when status = 'candidate' then 0 when status = 'accepted' then 1 else 2 end,
			updated_at desc
		limit $2
	`, StatusDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward intents: %w", err)
	}
	defer rows.Close()

	items := []domain.StewardIntent{}
	for rows.Next() {
		item, err := scanIntent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) AcceptIntent(ctx context.Context, id string) (domain.StewardTask, error) {
	intent, err := s.getIntent(ctx, id)
	if err != nil {
		return domain.StewardTask{}, err
	}
	task, err := s.CreateTask(ctx, CreateTaskInput{
		Type:            "from_intent",
		Title:           intent.Title,
		Description:     defaultString(intent.Summary, intent.Reason),
		Priority:        "normal",
		Source:          "intent",
		DataLevel:       intent.DataLevel,
		PermissionLevel: intent.PermissionLevel,
		RiskLevel:       intent.RiskLevel,
	})
	if err != nil {
		return domain.StewardTask{}, err
	}
	if _, err := s.CreateSourceRef(ctx, CreateSourceRefInput{
		TargetType: "task",
		TargetID:   task.ID,
		SourceType: "intent",
		SourceID:   intent.ID,
		Summary:    intent.Title,
		Confidence: intent.Confidence,
		Sensitive:  isSensitiveLevel(intent.DataLevel),
	}); err != nil {
		return domain.StewardTask{}, err
	}
	if _, err := s.updateIntentStatus(ctx, intent, StatusAccepted, "intent.accept"); err != nil {
		return domain.StewardTask{}, err
	}
	return task, nil
}

func (s *Service) DismissIntent(ctx context.Context, id string) (domain.StewardIntent, error) {
	intent, err := s.getIntent(ctx, id)
	if err != nil {
		return domain.StewardIntent{}, err
	}
	return s.updateIntentStatus(ctx, intent, StatusDismissed, "intent.dismiss")
}

func (s *Service) MuteIntent(ctx context.Context, id string) (domain.StewardIntent, error) {
	intent, err := s.getIntent(ctx, id)
	if err != nil {
		return domain.StewardIntent{}, err
	}
	return s.updateIntentStatus(ctx, intent, StatusMuted, "intent.mute")
}

func (s *Service) DeleteIntent(ctx context.Context, id string) error {
	if err := s.softDeleteEntity(ctx, "steward_intents", "intent", id, "intent.delete"); err != nil {
		return err
	}
	intent, err := s.getIntent(ctx, id)
	if err == nil {
		_ = s.recordIntentSyncChange(ctx, intent, SyncDelete)
	}
	return nil
}

func (s *Service) CreateMemory(ctx context.Context, input CreateMemoryInput) (domain.StewardMemory, error) {
	now := time.Now().UTC()
	confidence := input.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	record := domain.StewardMemory{
		ID:              uuid.NewString(),
		Type:            defaultString(input.Type, "project_fact"),
		Title:           defaultString(input.Title, "记忆"),
		Summary:         strings.TrimSpace(input.Summary),
		Content:         defaultString(input.Content, input.Summary),
		Scope:           defaultString(input.Scope, "global"),
		Status:          StatusActive,
		Source:          defaultString(input.Source, "manual"),
		DataLevel:       defaultString(input.DataLevel, DataD0),
		PermissionLevel: defaultString(input.PermissionLevel, PermissionA3),
		DeviceID:        s.agentIDValue(),
		Confidence:      confidence,
		UserConfirmed:   defaultBool(input.UserConfirmed, true),
		Version:         1,
		LastVerifiedAt:  &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "memory.create",
		TargetType:      "memory",
		TargetID:        &record.ID,
		Source:          record.Source,
		PermissionLevel: record.PermissionLevel,
		DataLevel:       record.DataLevel,
		InputSummary:    record.Title,
		OutputSummary:   "memory created",
		UserConfirmed:   &record.UserConfirmed,
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardMemory{}, err
	}
	record.AuditID = &auditID
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_memories (
			id, type, title, summary, content, scope, status, source, data_level, permission_level,
			device_id, confidence, user_confirmed, version, last_verified_at, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
	`, record.ID, record.Type, record.Title, record.Summary, record.Content, record.Scope, record.Status,
		record.Source, record.DataLevel, record.PermissionLevel, record.DeviceID, record.Confidence,
		record.UserConfirmed, record.Version, record.LastVerifiedAt, auditID, now); err != nil {
		return domain.StewardMemory{}, fmt.Errorf("create steward memory: %w", err)
	}
	_ = s.recordMemorySyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) ListMemories(ctx context.Context, limit int) ([]domain.StewardMemory, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, type, title, summary, content, scope, status, source, data_level, permission_level,
		       device_id, confidence, user_confirmed, version, last_verified_at, audit_id, created_at, updated_at, deleted_at
		from steward_memories
		where deleted_at is null and status <> $1
		order by updated_at desc
		limit $2
	`, StatusDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward memories: %w", err)
	}
	defer rows.Close()

	items := []domain.StewardMemory{}
	for rows.Next() {
		item, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CorrectMemory(ctx context.Context, id string, input CorrectMemoryInput) (domain.StewardMemory, error) {
	current, err := s.getMemory(ctx, id)
	if err != nil {
		return domain.StewardMemory{}, err
	}
	now := time.Now().UTC()
	nextTitle := defaultString(input.Title, current.Title)
	nextSummary := defaultString(input.Summary, current.Summary)
	nextContent := defaultString(input.Content, current.Content)
	reason := defaultString(input.Reason, "用户纠正记忆")
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "memory.correct",
		TargetType:      "memory",
		TargetID:        &current.ID,
		Source:          "manual",
		PermissionLevel: current.PermissionLevel,
		DataLevel:       current.DataLevel,
		InputSummary:    current.Title,
		OutputSummary:   nextTitle,
		BeforeSummary:   current.Summary,
		AfterSummary:    nextSummary,
		Reason:          reason,
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardMemory{}, err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_memory_versions (id, memory_id, version, title, summary, content, reason, audit_id, created_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, uuid.NewString(), current.ID, current.Version, current.Title, current.Summary, current.Content, reason, auditID, now); err != nil {
		return domain.StewardMemory{}, fmt.Errorf("insert steward memory version: %w", err)
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_memories
		set title = $1, summary = $2, content = $3, status = $4, user_confirmed = true,
		    version = version + 1, last_verified_at = $5, audit_id = $6, updated_at = $5
		where id = $7 and deleted_at is null
	`, nextTitle, nextSummary, nextContent, StatusActive, now, auditID, current.ID); err != nil {
		return domain.StewardMemory{}, fmt.Errorf("correct steward memory: %w", err)
	}
	memory, err := s.getMemory(ctx, current.ID)
	if err != nil {
		return domain.StewardMemory{}, err
	}
	_ = s.recordMemorySyncChange(ctx, memory, SyncUpdate)
	return memory, nil
}

func (s *Service) ArchiveMemory(ctx context.Context, id string) (domain.StewardMemory, error) {
	memory, err := s.getMemory(ctx, id)
	if err != nil {
		return domain.StewardMemory{}, err
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "memory.archive",
		TargetType:      "memory",
		TargetID:        &memory.ID,
		Source:          "manual",
		PermissionLevel: memory.PermissionLevel,
		DataLevel:       memory.DataLevel,
		InputSummary:    memory.Title,
		OutputSummary:   "memory archived",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardMemory{}, err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_memories
		set status = $1, version = version + 1, audit_id = $2, updated_at = $3
		where id = $4 and deleted_at is null
	`, StatusArchived, auditID, time.Now().UTC(), memory.ID); err != nil {
		return domain.StewardMemory{}, fmt.Errorf("archive steward memory: %w", err)
	}
	memory, err = s.getMemory(ctx, id)
	if err != nil {
		return domain.StewardMemory{}, err
	}
	_ = s.recordMemorySyncChange(ctx, memory, SyncUpdate)
	return memory, nil
}

func (s *Service) DeleteMemory(ctx context.Context, id string) error {
	if err := s.softDeleteEntity(ctx, "steward_memories", "memory", id, "memory.delete"); err != nil {
		return err
	}
	memory, err := s.getMemory(ctx, id)
	if err == nil {
		_ = s.recordMemorySyncChange(ctx, memory, SyncDelete)
	}
	return nil
}

func (s *Service) ListMemoryVersions(ctx context.Context, memoryID string) ([]domain.StewardMemoryVersion, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, memory_id, version, title, summary, content, reason, audit_id, created_at
		from steward_memory_versions
		where memory_id = $1
		order by version desc, created_at desc
	`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("list steward memory versions: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardMemoryVersion{}
	for rows.Next() {
		var item domain.StewardMemoryVersion
		if err := rows.Scan(
			&item.ID,
			&item.MemoryID,
			&item.Version,
			&item.Title,
			&item.Summary,
			&item.Content,
			&item.Reason,
			&item.AuditID,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateKnowledgeItem(ctx context.Context, input CreateKnowledgeInput) (domain.StewardKnowledgeItem, error) {
	recordID := strings.TrimSpace(input.ID)
	if recordID != "" {
		if _, err := uuid.Parse(recordID); err != nil {
			return domain.StewardKnowledgeItem{}, fmt.Errorf("invalid internal knowledge item id: %w", err)
		}
		existing, err := s.getKnowledgeItem(ctx, recordID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardKnowledgeItem{}, err
		}
	} else {
		recordID = uuid.NewString()
	}
	now := time.Now().UTC()
	hashInput := strings.Join([]string{input.Title, input.Summary, input.OriginalURI}, "\n")
	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))
	record := domain.StewardKnowledgeItem{
		ID:              recordID,
		Type:            defaultString(input.Type, "note"),
		Title:           defaultString(input.Title, "知识条目"),
		Summary:         strings.TrimSpace(input.Summary),
		Source:          defaultString(input.Source, "manual"),
		OriginalURI:     strings.TrimSpace(input.OriginalURI),
		ImportMethod:    defaultString(input.ImportMethod, "manual"),
		ContentHash:     contentHash,
		Status:          StatusActive,
		DataLevel:       defaultString(input.DataLevel, DataD0),
		PermissionLevel: defaultString(input.PermissionLevel, PermissionA3),
		DeviceID:        s.agentIDValue(),
		AllowIndex:      defaultBool(input.AllowIndex, true),
		UserConfirmed:   defaultBool(input.UserConfirmed, true),
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           auditActorForSource(record.Source),
		Action:          "knowledge_item.create",
		TargetType:      "knowledge_item",
		TargetID:        &record.ID,
		Source:          record.Source,
		PermissionLevel: record.PermissionLevel,
		DataLevel:       record.DataLevel,
		InputSummary:    record.Title,
		OutputSummary:   "knowledge item imported",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardKnowledgeItem{}, err
	}
	record.AuditID = &auditID
	tag, err := s.db.Pool.Exec(ctx, `
		insert into steward_knowledge_items (
			id, type, title, summary, source, original_uri, import_method, content_hash, status,
			data_level, permission_level, device_id, allow_index, user_confirmed, version, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
		on conflict (id) do nothing
	`, record.ID, record.Type, record.Title, record.Summary, record.Source, record.OriginalURI,
		record.ImportMethod, record.ContentHash, record.Status, record.DataLevel, record.PermissionLevel,
		record.DeviceID, record.AllowIndex, record.UserConfirmed, record.Version, auditID, now)
	if err != nil {
		return domain.StewardKnowledgeItem{}, fmt.Errorf("create steward knowledge item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, err := s.getKnowledgeItem(ctx, record.ID)
		if err != nil {
			return domain.StewardKnowledgeItem{}, fmt.Errorf("load idempotent steward knowledge item: %w", err)
		}
		return existing, nil
	}
	_ = s.recordKnowledgeItemSyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) ListKnowledgeItems(ctx context.Context, limit int) ([]domain.StewardKnowledgeItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, type, title, summary, source, original_uri, import_method, content_hash, status,
		       data_level, permission_level, device_id, allow_index, user_confirmed, version, audit_id,
		       created_at, updated_at, deleted_at
		from steward_knowledge_items
		where deleted_at is null and status <> $1
		order by updated_at desc
		limit $2
	`, StatusDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward knowledge items: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardKnowledgeItem{}
	for rows.Next() {
		item, err := scanKnowledgeItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) DeleteKnowledgeItem(ctx context.Context, id string) error {
	if err := s.softDeleteEntity(ctx, "steward_knowledge_items", "knowledge_item", id, "knowledge_item.delete"); err != nil {
		return err
	}
	item, err := s.getKnowledgeItem(ctx, id)
	if err == nil {
		_ = s.recordKnowledgeItemSyncChange(ctx, item, SyncDelete)
	}
	return nil
}

func (s *Service) DeleteTask(ctx context.Context, id string) error {
	if _, err := s.getTask(ctx, id); err != nil {
		return err
	}
	if err := s.softDeleteEntity(ctx, "steward_tasks", "task", id, "task.delete"); err != nil {
		return err
	}
	task, err := s.getTask(ctx, id)
	if err != nil {
		return err
	}
	_ = s.recordTaskSyncChange(ctx, task, SyncDelete)
	return nil
}

func (s *Service) CreateSourceRef(ctx context.Context, input CreateSourceRefInput) (domain.StewardSourceRef, error) {
	now := time.Now().UTC()
	record := domain.StewardSourceRef{
		ID:          uuid.NewString(),
		TargetType:  defaultString(input.TargetType, "unknown"),
		TargetID:    strings.TrimSpace(input.TargetID),
		SourceType:  defaultString(input.SourceType, "manual_input"),
		SourceID:    strings.TrimSpace(input.SourceID),
		Location:    strings.TrimSpace(input.Location),
		Summary:     strings.TrimSpace(input.Summary),
		Confidence:  input.Confidence,
		Sensitive:   input.Sensitive,
		Displayable: defaultBool(input.Displayable, true),
		CreatedAt:   now,
	}
	if record.Confidence <= 0 {
		record.Confidence = 1
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "source_ref.create",
		TargetType:      record.TargetType,
		TargetID:        &record.TargetID,
		Source:          record.SourceType,
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    record.Summary,
		OutputSummary:   "source ref created",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardSourceRef{}, err
	}
	record.AuditID = &auditID
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_source_refs (
			id, target_type, target_id, source_type, source_id, location, summary,
			confidence, sensitive, displayable, audit_id, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, record.ID, record.TargetType, record.TargetID, record.SourceType, record.SourceID, record.Location,
		record.Summary, record.Confidence, record.Sensitive, record.Displayable, auditID, now); err != nil {
		return domain.StewardSourceRef{}, fmt.Errorf("create steward source ref: %w", err)
	}
	_ = s.recordSourceRefSyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) ListSourceRefs(ctx context.Context, targetType string, targetID string, limit int) ([]domain.StewardSourceRef, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
		select id, target_type, target_id::text, source_type, source_id, location, summary,
		       confidence, sensitive, displayable, audit_id, created_at
		from steward_source_refs
		where ($1 = '' or target_type = $1)
		  and ($2 = '' or target_id::text = $2)
		  and ($1 <> '' or displayable = true)
		order by created_at desc
		limit $3
	`
	rows, err := s.db.Pool.Query(ctx, query, strings.TrimSpace(targetType), strings.TrimSpace(targetID), limit)
	if err != nil {
		return nil, fmt.Errorf("list steward source refs: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardSourceRef{}
	for rows.Next() {
		var item domain.StewardSourceRef
		if err := rows.Scan(
			&item.ID,
			&item.TargetType,
			&item.TargetID,
			&item.SourceType,
			&item.SourceID,
			&item.Location,
			&item.Summary,
			&item.Confidence,
			&item.Sensitive,
			&item.Displayable,
			&item.AuditID,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateDataTag(ctx context.Context, input CreateDataTagInput) (domain.StewardDataTag, error) {
	now := time.Now().UTC()
	record := domain.StewardDataTag{
		ID:          uuid.NewString(),
		Name:        defaultString(input.Name, "未命名标签"),
		Type:        defaultString(input.Type, "normal"),
		Color:       strings.TrimSpace(input.Color),
		Description: strings.TrimSpace(input.Description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.Pool.QueryRow(ctx, `
		insert into steward_data_tags (id, name, type, color, description, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$6)
		on conflict (name) do update
		set type = excluded.type,
		    color = excluded.color,
		    description = excluded.description,
		    updated_at = excluded.updated_at
		returning id, name, type, color, description, created_at, updated_at
	`, record.ID, record.Name, record.Type, record.Color, record.Description, now).Scan(
		&record.ID,
		&record.Name,
		&record.Type,
		&record.Color,
		&record.Description,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return domain.StewardDataTag{}, fmt.Errorf("create steward data tag: %w", err)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "data_tag.upsert",
		TargetType:      "data_tag",
		TargetID:        &record.ID,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    record.Name,
		OutputSummary:   "data tag saved",
		ResultStatus:    ResultOK,
	})
	_ = s.recordDataTagSyncChange(ctx, record, SyncUpdate)
	return record, nil
}

func (s *Service) ListDataTags(ctx context.Context) ([]domain.StewardDataTag, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, name, type, color, description, created_at, updated_at
		from steward_data_tags
		order by type, name
	`)
	if err != nil {
		return nil, fmt.Errorf("list steward data tags: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardDataTag{}
	for rows.Next() {
		var item domain.StewardDataTag
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Type,
			&item.Color,
			&item.Description,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) AssignDataTag(ctx context.Context, input AssignTagInput) error {
	confidence := input.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_entity_tags (entity_type, entity_id, tag_id, source, confidence)
		values ($1,$2,$3,$4,$5)
		on conflict (entity_type, entity_id, tag_id) do update
		set source = excluded.source,
		    confidence = excluded.confidence,
		    created_at = now()
	`, input.EntityType, input.EntityID, input.TagID, defaultString(input.Source, "manual"), confidence); err != nil {
		return fmt.Errorf("assign steward data tag: %w", err)
	}
	tag, tagErr := s.getDataTag(ctx, input.TagID)
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "data_tag.assign",
		TargetType:      input.EntityType,
		TargetID:        &input.EntityID,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    input.TagID,
		OutputSummary:   "data tag assigned",
		ResultStatus:    ResultOK,
	})
	if tagErr == nil {
		_ = s.recordEntityTagSyncChange(ctx, input, tag, confidence, SyncUpdate)
	}
	return nil
}

func (s *Service) Search(ctx context.Context, input SearchInput) ([]domain.StewardSearchResult, error) {
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	results := []domain.StewardSearchResult{}
	queryText := "%" + strings.TrimSpace(input.Query) + "%"
	if strings.TrimSpace(input.Query) == "" {
		queryText = "%"
	}

	addResults := func(entityType string, query string, args ...any) error {
		if input.EntityType != "" && input.EntityType != entityType {
			return nil
		}
		rows, err := s.db.Pool.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item domain.StewardSearchResult
			item.EntityType = entityType
			if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.Summary, &item.Status, &item.DataLevel, &item.Source, &item.UpdatedAt); err != nil {
				return err
			}
			if (input.Status == "" || item.Status == input.Status) && (input.DataLevel == "" || item.DataLevel == input.DataLevel) {
				results = append(results, item)
			}
		}
		return rows.Err()
	}

	if err := addResults("event", `
		select id::text, type, title, summary, status, data_level, source, updated_at
		from steward_events
		where deleted_at is null and status <> 'deleted' and (title ilike $1 or summary ilike $1)
		order by updated_at desc
		limit $2
	`, queryText, limit); err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	if err := addResults("timeline_segment", `
		select id::text, type, title, summary, status, data_level, source, updated_at
		from steward_timeline_segments
		where deleted_at is null and status <> 'deleted' and (title ilike $1 or summary ilike $1)
		order by updated_at desc
		limit $2
	`, queryText, limit); err != nil {
		return nil, fmt.Errorf("search timeline segments: %w", err)
	}
	if err := addResults("task", `
		select id::text, type, title, description as summary, status, data_level, source, updated_at
		from steward_tasks
		where deleted_at is null and status <> 'deleted' and (title ilike $1 or description ilike $1)
		order by updated_at desc
		limit $2
	`, queryText, limit); err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	if err := addResults("intent", `
		select id::text, type, title, summary, status, data_level, source, updated_at
		from steward_intents
		where deleted_at is null and status <> 'deleted' and (title ilike $1 or summary ilike $1 or reason ilike $1)
		order by updated_at desc
		limit $2
	`, queryText, limit); err != nil {
		return nil, fmt.Errorf("search intents: %w", err)
	}
	if err := addResults("memory", `
		select id::text, type, title, summary, status, data_level, source, updated_at
		from steward_memories
		where deleted_at is null and status <> 'deleted' and (title ilike $1 or summary ilike $1 or content ilike $1)
		order by updated_at desc
		limit $2
	`, queryText, limit); err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	if err := addResults("knowledge_item", `
		select id::text, type, title, summary, status, data_level, source, updated_at
		from steward_knowledge_items
		where deleted_at is null and status <> 'deleted' and (title ilike $1 or summary ilike $1 or original_uri ilike $1)
		order by updated_at desc
		limit $2
	`, queryText, limit); err != nil {
		return nil, fmt.Errorf("search knowledge items: %w", err)
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (s *Service) ExportData(ctx context.Context, includeSensitive bool) (domain.StewardOverview, error) {
	overview, err := s.GetOverview(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	if !includeSensitive {
		overview.Events = filterSensitive(overview.Events, func(item domain.StewardEvent) string { return item.DataLevel })
		overview.TimelineSegments = filterSensitive(overview.TimelineSegments, func(item domain.StewardTimelineSegment) string { return item.DataLevel })
		overview.Tasks = filterSensitive(overview.Tasks, func(item domain.StewardTask) string { return item.DataLevel })
		overview.Intents = filterSensitive(overview.Intents, func(item domain.StewardIntent) string { return item.DataLevel })
		overview.Memories = filterSensitive(overview.Memories, func(item domain.StewardMemory) string { return item.DataLevel })
		overview.KnowledgeItems = filterSensitive(overview.KnowledgeItems, func(item domain.StewardKnowledgeItem) string { return item.DataLevel })
		filteredRefs := []domain.StewardSourceRef{}
		for _, ref := range overview.SourceRefs {
			if !ref.Sensitive {
				filteredRefs = append(filteredRefs, ref)
			}
		}
		overview.SourceRefs = filteredRefs
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "data.export",
		TargetType:      "export",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    fmt.Sprintf("include_sensitive=%t", includeSensitive),
		OutputSummary:   "steward data exported",
		ResultStatus:    ResultOK,
	})
	return overview, nil
}

func (s *Service) getIntent(ctx context.Context, id string) (domain.StewardIntent, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, type, title, summary, reason, suggested_action, risk_level, status, source,
		       data_level, permission_level, device_id, confidence, user_confirmed, version, audit_id,
		       created_at, updated_at, deleted_at
		from steward_intents
		where id = $1
	`, id)
	record, err := scanIntent(row)
	if err != nil {
		return domain.StewardIntent{}, fmt.Errorf("get steward intent: %w", err)
	}
	return record, nil
}

func (s *Service) getTimelineSegment(ctx context.Context, id string) (domain.StewardTimelineSegment, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select s.id, s.type, s.title, s.summary, s.status, s.source, s.data_level, s.permission_level,
		       s.device_id, s.start_at, s.end_at, s.confidence, s.user_confirmed, s.version, s.audit_id,
		       count(e.event_id)::int as event_count, s.created_at, s.updated_at, s.deleted_at
		from steward_timeline_segments s
		left join steward_timeline_segment_events e on e.segment_id = s.id
		where s.id = $1
		group by s.id
	`, id)
	var item domain.StewardTimelineSegment
	if err := row.Scan(
		&item.ID,
		&item.Type,
		&item.Title,
		&item.Summary,
		&item.Status,
		&item.Source,
		&item.DataLevel,
		&item.PermissionLevel,
		&item.DeviceID,
		&item.StartAt,
		&item.EndAt,
		&item.Confidence,
		&item.UserConfirmed,
		&item.Version,
		&item.AuditID,
		&item.EventCount,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.DeletedAt,
	); err != nil {
		return domain.StewardTimelineSegment{}, fmt.Errorf("get steward timeline segment: %w", err)
	}
	return item, nil
}

func (s *Service) updateIntentStatus(ctx context.Context, intent domain.StewardIntent, status string, action string) (domain.StewardIntent, error) {
	now := time.Now().UTC()
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          action,
		TargetType:      "intent",
		TargetID:        &intent.ID,
		Source:          "manual",
		PermissionLevel: intent.PermissionLevel,
		DataLevel:       intent.DataLevel,
		InputSummary:    intent.Title,
		OutputSummary:   status,
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardIntent{}, err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_intents
		set status = $1, user_confirmed = true, version = version + 1, audit_id = $2, updated_at = $3
		where id = $4 and deleted_at is null
	`, status, auditID, now, intent.ID); err != nil {
		return domain.StewardIntent{}, fmt.Errorf("update steward intent: %w", err)
	}
	updated, err := s.getIntent(ctx, intent.ID)
	if err != nil {
		return domain.StewardIntent{}, err
	}
	_ = s.recordIntentSyncChange(ctx, updated, SyncUpdate)
	return updated, nil
}

func (s *Service) getMemory(ctx context.Context, id string) (domain.StewardMemory, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, type, title, summary, content, scope, status, source, data_level, permission_level,
		       device_id, confidence, user_confirmed, version, last_verified_at, audit_id, created_at, updated_at, deleted_at
		from steward_memories
		where id = $1
	`, id)
	record, err := scanMemory(row)
	if err != nil {
		return domain.StewardMemory{}, fmt.Errorf("get steward memory: %w", err)
	}
	return record, nil
}

func (s *Service) getKnowledgeItem(ctx context.Context, id string) (domain.StewardKnowledgeItem, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, type, title, summary, source, original_uri, import_method, content_hash, status,
		       data_level, permission_level, device_id, allow_index, user_confirmed, version, audit_id,
		       created_at, updated_at, deleted_at
		from steward_knowledge_items
		where id = $1
	`, id)
	record, err := scanKnowledgeItem(row)
	if err != nil {
		return domain.StewardKnowledgeItem{}, fmt.Errorf("get steward knowledge item: %w", err)
	}
	return record, nil
}

func (s *Service) getDataTag(ctx context.Context, id string) (domain.StewardDataTag, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, name, type, color, description, created_at, updated_at
		from steward_data_tags
		where id = $1
	`, id)
	var item domain.StewardDataTag
	if err := row.Scan(
		&item.ID,
		&item.Name,
		&item.Type,
		&item.Color,
		&item.Description,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return domain.StewardDataTag{}, fmt.Errorf("get steward data tag: %w", err)
	}
	return item, nil
}

func (s *Service) softDeleteEntity(ctx context.Context, table string, targetType string, id string, action string) error {
	now := time.Now().UTC()
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          action,
		TargetType:      targetType,
		TargetID:        &id,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    id,
		OutputSummary:   targetType + " soft deleted",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		update %s
		set status = $1, deleted_at = $2, updated_at = $2, audit_id = $3, version = version + 1
		where id = $4 and deleted_at is null
	`, table)
	tag, err := s.db.Pool.Exec(ctx, query, StatusDeleted, now, auditID, id)
	if err != nil {
		return fmt.Errorf("delete steward %s: %w", targetType, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%s not found", targetType)
	}
	_, _ = s.db.Pool.Exec(ctx, `
		update steward_source_refs
		set displayable = false
		where target_type = $1 and target_id::text = $2
	`, targetType, id)
	return nil
}

func (s *Service) recordEventConversion(ctx context.Context, eventID string, targetType string, targetID string) (string, error) {
	return s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "event.convert",
		TargetType:      "event",
		TargetID:        &eventID,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    eventID,
		OutputSummary:   fmt.Sprintf("converted to %s %s", targetType, targetID),
		ResultStatus:    ResultOK,
	})
}

func scanIntent(row scanner) (domain.StewardIntent, error) {
	var record domain.StewardIntent
	err := row.Scan(
		&record.ID,
		&record.Type,
		&record.Title,
		&record.Summary,
		&record.Reason,
		&record.SuggestedAction,
		&record.RiskLevel,
		&record.Status,
		&record.Source,
		&record.DataLevel,
		&record.PermissionLevel,
		&record.DeviceID,
		&record.Confidence,
		&record.UserConfirmed,
		&record.Version,
		&record.AuditID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.DeletedAt,
	)
	return record, err
}

func scanMemory(row scanner) (domain.StewardMemory, error) {
	var record domain.StewardMemory
	err := row.Scan(
		&record.ID,
		&record.Type,
		&record.Title,
		&record.Summary,
		&record.Content,
		&record.Scope,
		&record.Status,
		&record.Source,
		&record.DataLevel,
		&record.PermissionLevel,
		&record.DeviceID,
		&record.Confidence,
		&record.UserConfirmed,
		&record.Version,
		&record.LastVerifiedAt,
		&record.AuditID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.DeletedAt,
	)
	return record, err
}

func scanKnowledgeItem(row scanner) (domain.StewardKnowledgeItem, error) {
	var record domain.StewardKnowledgeItem
	err := row.Scan(
		&record.ID,
		&record.Type,
		&record.Title,
		&record.Summary,
		&record.Source,
		&record.OriginalURI,
		&record.ImportMethod,
		&record.ContentHash,
		&record.Status,
		&record.DataLevel,
		&record.PermissionLevel,
		&record.DeviceID,
		&record.AllowIndex,
		&record.UserConfirmed,
		&record.Version,
		&record.AuditID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.DeletedAt,
	)
	return record, err
}

func filterSensitive[T any](items []T, level func(T) string) []T {
	filtered := []T{}
	for _, item := range items {
		if !isSensitiveLevel(level(item)) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func isSensitiveLevel(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "D4", "D5", "D6":
		return true
	default:
		return false
	}
}
