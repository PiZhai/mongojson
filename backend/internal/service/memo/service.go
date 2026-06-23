package memo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

const DefaultSlug = "inbox"

type Service struct {
	db *database.DB
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func (s *Service) GetOrCreate(ctx context.Context, slug string) (domain.MemoRecord, error) {
	if slug == "" {
		slug = DefaultSlug
	}

	record, err := s.getBySlug(ctx, slug)
	if err == nil {
		return record, nil
	}

	now := time.Now().UTC()
	record = domain.MemoRecord{
		ID:          uuid.NewString(),
		Slug:        slug,
		Title:       "随手记",
		ContentHTML: defaultContentHTML(),
		ContentText: "随手记",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into tool_memos (id, slug, title, content_html, content_text, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7)
		on conflict (slug) do nothing
	`, record.ID, record.Slug, record.Title, record.ContentHTML, record.ContentText, record.CreatedAt, record.UpdatedAt); err != nil {
		return domain.MemoRecord{}, fmt.Errorf("create memo: %w", err)
	}

	return s.getBySlug(ctx, slug)
}

func (s *Service) Save(ctx context.Context, slug string, title string, contentHTML string, contentText string) (domain.MemoRecord, error) {
	if slug == "" {
		slug = DefaultSlug
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "随手记"
	}

	now := time.Now().UTC()
	newID := uuid.NewString()
	_, err := s.db.Pool.Exec(ctx, `
		insert into tool_memos (id, slug, title, content_html, content_text, created_at, updated_at)
		values (
			coalesce((select id from tool_memos where slug = $1), $2::uuid),
			$1,$3,$4,$5,
			coalesce((select created_at from tool_memos where slug = $1), $6),
			$6
		)
		on conflict (slug) do update
		set title = excluded.title,
		    content_html = excluded.content_html,
		    content_text = excluded.content_text,
		    updated_at = excluded.updated_at
	`, slug, newID, title, contentHTML, contentText, now)
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("save memo: %w", err)
	}
	return s.getBySlug(ctx, slug)
}

func (s *Service) getBySlug(ctx context.Context, slug string) (domain.MemoRecord, error) {
	var record domain.MemoRecord
	if err := s.db.Pool.QueryRow(ctx, `
		select id, slug, title, content_html, content_text, created_at, updated_at
		from tool_memos
		where slug = $1
	`, slug).Scan(
		&record.ID,
		&record.Slug,
		&record.Title,
		&record.ContentHTML,
		&record.ContentText,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return domain.MemoRecord{}, fmt.Errorf("get memo by slug: %w", err)
	}
	return record, nil
}

func defaultContentHTML() string {
	return `<h1>随手记</h1><p>把想法、链接、图片和临时决策先放进这里，内容会自动保存到云端。</p>`
}
