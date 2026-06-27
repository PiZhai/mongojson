package memo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

const DefaultSlug = "inbox"
const defaultFloatingCardColor = "#fff7d6"

var (
	ErrInvalidFloatingCards = errors.New("invalid floating_cards")
	hexColorPattern         = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type Service struct {
	db *database.DB
}

type SaveInput struct {
	Slug          string
	Title         string
	ContentHTML   string
	ContentText   string
	FloatingCards *json.RawMessage
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
		ID:            uuid.NewString(),
		Slug:          slug,
		Title:         "随手记",
		ContentHTML:   defaultContentHTML(),
		ContentText:   "随手记",
		FloatingCards: []domain.MemoFloatingCard{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into tool_memos (id, slug, title, content_html, content_text, floating_cards, created_at, updated_at)
		values ($1,$2,$3,$4,$5,'[]'::jsonb,$6,$7)
		on conflict (slug) do nothing
	`, record.ID, record.Slug, record.Title, record.ContentHTML, record.ContentText, record.CreatedAt, record.UpdatedAt); err != nil {
		return domain.MemoRecord{}, fmt.Errorf("create memo: %w", err)
	}

	return s.getBySlug(ctx, slug)
}

func (s *Service) Save(ctx context.Context, slug string, title string, contentHTML string, contentText string) (domain.MemoRecord, error) {
	return s.SaveMemo(ctx, SaveInput{
		Slug:        slug,
		Title:       title,
		ContentHTML: contentHTML,
		ContentText: contentText,
	})
}

func (s *Service) SaveMemo(ctx context.Context, input SaveInput) (domain.MemoRecord, error) {
	slug := input.Slug
	if slug == "" {
		slug = DefaultSlug
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "随手记"
	}

	now := time.Now().UTC()
	newID := uuid.NewString()
	if input.FloatingCards == nil {
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
	`, slug, newID, title, input.ContentHTML, input.ContentText, now)
		if err != nil {
			return domain.MemoRecord{}, fmt.Errorf("save memo: %w", err)
		}
		return s.getBySlug(ctx, slug)
	}

	_, cardsJSON, err := NormalizeFloatingCardsJSON(*input.FloatingCards, now)
	if err != nil {
		return domain.MemoRecord{}, err
	}

	_, err = s.db.Pool.Exec(ctx, `
		insert into tool_memos (id, slug, title, content_html, content_text, floating_cards, created_at, updated_at)
		values (
			coalesce((select id from tool_memos where slug = $1), $2::uuid),
			$1,$3,$4,$5,$6::jsonb,
			coalesce((select created_at from tool_memos where slug = $1), $7),
			$7
		)
		on conflict (slug) do update
		set title = excluded.title,
		    content_html = excluded.content_html,
		    content_text = excluded.content_text,
		    floating_cards = excluded.floating_cards,
		    updated_at = excluded.updated_at
	`, slug, newID, title, input.ContentHTML, input.ContentText, string(cardsJSON), now)
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("save memo: %w", err)
	}
	return s.getBySlug(ctx, slug)
}

func (s *Service) getBySlug(ctx context.Context, slug string) (domain.MemoRecord, error) {
	var record domain.MemoRecord
	var cardsJSON string
	if err := s.db.Pool.QueryRow(ctx, `
		select id, slug, title, content_html, content_text, coalesce(floating_cards, '[]'::jsonb)::text, created_at, updated_at
		from tool_memos
		where slug = $1
	`, slug).Scan(
		&record.ID,
		&record.Slug,
		&record.Title,
		&record.ContentHTML,
		&record.ContentText,
		&cardsJSON,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return domain.MemoRecord{}, fmt.Errorf("get memo by slug: %w", err)
	}
	cards, _, err := NormalizeFloatingCardsJSON(json.RawMessage(cardsJSON), time.Now().UTC())
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("decode memo floating cards: %w", err)
	}
	record.FloatingCards = cards
	return record, nil
}

func NormalizeFloatingCardsJSON(raw json.RawMessage, now time.Time) ([]domain.MemoFloatingCard, []byte, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("[]")
	}
	if strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return nil, nil, fmt.Errorf("%w: must be an array of card objects", ErrInvalidFloatingCards)
	}

	var payloads []struct {
		ID        string `json:"id"`
		Content   string `json:"content"`
		Color     string `json:"color"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &payloads); err != nil {
		return nil, nil, fmt.Errorf("%w: must be an array of card objects", ErrInvalidFloatingCards)
	}

	cards := make([]domain.MemoFloatingCard, 0, len(payloads))
	for _, payload := range payloads {
		cardID := strings.TrimSpace(payload.ID)
		if cardID == "" {
			cardID = uuid.NewString()
		}
		createdAt := parseCardTime(payload.CreatedAt, now)
		updatedAt := parseCardTime(payload.UpdatedAt, createdAt)
		cards = append(cards, domain.MemoFloatingCard{
			ID:        cardID,
			Content:   payload.Content,
			Color:     normalizeFloatingCardColor(payload.Color),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	normalizedJSON, err := json.Marshal(cards)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: marshal normalized cards", ErrInvalidFloatingCards)
	}
	return cards, normalizedJSON, nil
}

func normalizeFloatingCardColor(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if hexColorPattern.MatchString(value) {
		return value
	}
	return defaultFloatingCardColor
}

func parseCardTime(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	return fallback.UTC()
}

func defaultContentHTML() string {
	return `<h1>随手记</h1><p>把想法、链接、图片和临时决策先放进这里，内容会自动保存到云端。</p>`
}
