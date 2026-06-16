package presets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

type Service struct {
	db *database.DB
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func (s *Service) List(ctx context.Context, toolType string) ([]domain.PresetRecord, error) {
	query := `select id, tool_type, name, payload, created_at, updated_at from tool_presets`
	args := []any{}
	if toolType != "" {
		query += ` where tool_type = $1`
		args = append(args, toolType)
	}
	query += ` order by updated_at desc`

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer rows.Close()

	var presets []domain.PresetRecord
	for rows.Next() {
		var preset domain.PresetRecord
		if err := rows.Scan(&preset.ID, &preset.ToolType, &preset.Name, &preset.Payload, &preset.CreatedAt, &preset.UpdatedAt); err != nil {
			return nil, err
		}
		presets = append(presets, preset)
	}
	return presets, rows.Err()
}

func (s *Service) Save(ctx context.Context, toolType string, name string, payload map[string]any) (domain.PresetRecord, error) {
	now := time.Now().UTC()
	record := domain.PresetRecord{
		ID:        uuid.NewString(),
		ToolType:  toolType,
		Name:      name,
		Payload:   payload,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := s.db.Pool.Exec(ctx, `
		insert into tool_presets (id, tool_type, name, payload, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6)
	`, record.ID, record.ToolType, record.Name, record.Payload, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return domain.PresetRecord{}, fmt.Errorf("save preset: %w", err)
	}
	return record, nil
}
