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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

const DefaultSlug = "inbox"
const defaultFloatingCardColor = "#fff7d6"

var (
	ErrDocumentNotFound     = errors.New("memo document not found")
	ErrRevisionConflict     = errors.New("memo revision conflict")
	ErrInvalidContentJSON   = errors.New("invalid memo content_json")
	ErrInvalidFloatingCards = errors.New("invalid floating_cards")
	ErrInvalidSideNote      = errors.New("invalid side note")
	ErrSideNoteNotFound     = errors.New("side note not found")
	ErrSlugConflict         = errors.New("memo slug already exists")
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

type DocumentSaveInput struct {
	Title         string
	ContentJSON   json.RawMessage
	ContentHTML   string
	ContentText   string
	SchemaVersion int
	Revision      int64
	EditorType    string
}

type SideNoteInput struct {
	ID            string
	AnchorBlockID *string
	BodyJSON      json.RawMessage
	Color         string
	SortOrder     int
	Collapsed     bool
	Status        string
	Revision      int64
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func (s *Service) GetOrCreate(ctx context.Context, slug string) (domain.MemoRecord, error) {
	slug = normalizeSlug(slug)
	record, err := s.getBySlug(ctx, slug)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.MemoRecord{}, err
	}

	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into tool_memos (
			id, slug, title, content_json, content_html, content_text,
			floating_cards, schema_version, revision, editor_type, created_at, updated_at
		)
		values ($1,$2,$3,'[]'::jsonb,$4,$5,'[]'::jsonb,0,1,'vditor',$6,$6)
		on conflict (slug) do nothing
	`, uuid.NewString(), slug, "随手记", defaultContentHTML(), "随手记", now)
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("create memo: %w", err)
	}
	return s.getBySlug(ctx, slug)
}

func (s *Service) CreateDocument(ctx context.Context, slug, title string) (domain.MemoRecord, error) {
	slug = normalizeSlug(slug)
	title = normalizeTitle(title)
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		insert into tool_memos (
			id, slug, title, content_json, content_html, content_text,
			floating_cards, schema_version, revision, editor_type, created_at, updated_at
		)
		values ($1,$2,$3,'[]'::jsonb,'','','[]'::jsonb,1,1,'blocknote',$4,$4)
	`, uuid.NewString(), slug, title, now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.MemoRecord{}, ErrSlugConflict
		}
		return domain.MemoRecord{}, fmt.Errorf("create memo document: %w", err)
	}
	return s.getBySlug(ctx, slug)
}

func (s *Service) GetDocument(ctx context.Context, slug string) (domain.MemoRecord, error) {
	record, err := s.getBySlug(ctx, normalizeSlug(slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MemoRecord{}, ErrDocumentNotFound
	}
	return record, err
}

func (s *Service) SaveDocument(ctx context.Context, id string, input DocumentSaveInput) (domain.MemoRecord, error) {
	contentJSON, blockIDs, err := normalizeDocumentJSON(input.ContentJSON)
	if err != nil {
		return domain.MemoRecord{}, err
	}
	if input.Revision < 1 {
		return domain.MemoRecord{}, fmt.Errorf("%w: revision must be positive", ErrRevisionConflict)
	}
	if input.SchemaVersion < 1 {
		input.SchemaVersion = 1
	}
	if strings.TrimSpace(input.EditorType) == "" {
		input.EditorType = "blocknote"
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("begin memo save: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentRevision int64
	var currentEditor, currentHTML, currentText, currentCards string
	err = tx.QueryRow(ctx, `
		select revision, editor_type, content_html, content_text, floating_cards::text
		from tool_memos where id = $1 for update
	`, id).Scan(&currentRevision, &currentEditor, &currentHTML, &currentText, &currentCards)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MemoRecord{}, ErrDocumentNotFound
	}
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("load memo for save: %w", err)
	}
	if currentRevision != input.Revision {
		return domain.MemoRecord{}, ErrRevisionConflict
	}

	if currentEditor != "blocknote" {
		if _, err = tx.Exec(ctx, `
			insert into memo_migration_backups (
				document_id, source_revision, content_html, content_text, floating_cards
			) values ($1,$2,$3,$4,$5::jsonb)
			on conflict (document_id, source_revision) do nothing
		`, id, currentRevision, currentHTML, currentText, currentCards); err != nil {
			return domain.MemoRecord{}, fmt.Errorf("backup legacy memo: %w", err)
		}
	}

	now := time.Now().UTC()
	commandTag, err := tx.Exec(ctx, `
		update tool_memos
		set title = $2,
		    content_json = $3::jsonb,
		    content_html = $4,
		    content_text = $5,
		    schema_version = $6,
		    editor_type = $7,
		    revision = revision + 1,
		    updated_at = $8
		where id = $1 and revision = $9
	`, id, normalizeTitle(input.Title), string(contentJSON), input.ContentHTML, input.ContentText,
		input.SchemaVersion, input.EditorType, now, input.Revision)
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("save memo document: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return domain.MemoRecord{}, ErrRevisionConflict
	}

	if err = reconcileSideNoteAnchors(ctx, tx, id, blockIDs, now); err != nil {
		return domain.MemoRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MemoRecord{}, fmt.Errorf("commit memo save: %w", err)
	}
	return s.getByID(ctx, id)
}

func (s *Service) DeleteDocument(ctx context.Context, id string) error {
	tag, err := s.db.Pool.Exec(ctx, `delete from tool_memos where id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete memo document: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrDocumentNotFound
	}
	return nil
}

func (s *Service) ListSideNotes(ctx context.Context, documentID string) ([]domain.MemoSideNoteRecord, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, document_id, anchor_block_id, body_json::text, color, sort_order,
		       collapsed, status, revision, created_at, updated_at
		from memo_side_notes
		where document_id = $1
		order by case when anchor_block_id is null then 1 else 0 end, sort_order, created_at, id
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("list memo side notes: %w", err)
	}
	defer rows.Close()
	items := make([]domain.MemoSideNoteRecord, 0)
	for rows.Next() {
		record, scanErr := scanSideNote(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, record)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memo side notes: %w", err)
	}
	return items, nil
}

func (s *Service) CreateSideNote(ctx context.Context, documentID string, input SideNoteInput) (domain.MemoSideNoteRecord, error) {
	input, err := normalizeSideNoteInput(input)
	if err != nil {
		return domain.MemoSideNoteRecord{}, err
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	status, err := s.resolveAnchorStatus(ctx, documentID, input.AnchorBlockID)
	if err != nil {
		return domain.MemoSideNoteRecord{}, err
	}
	if input.Status == "archived" {
		status = "archived"
	}
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into memo_side_notes (
			id, document_id, anchor_block_id, body_json, color, sort_order,
			collapsed, status, revision, created_at, updated_at
		) values ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,1,$9,$9)
	`, input.ID, documentID, input.AnchorBlockID, string(input.BodyJSON), input.Color,
		input.SortOrder, input.Collapsed, status, now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return domain.MemoSideNoteRecord{}, ErrDocumentNotFound
		}
		return domain.MemoSideNoteRecord{}, fmt.Errorf("create memo side note: %w", err)
	}
	return s.getSideNote(ctx, input.ID)
}

func (s *Service) SaveSideNote(ctx context.Context, id string, input SideNoteInput) (domain.MemoSideNoteRecord, error) {
	input, err := normalizeSideNoteInput(input)
	if err != nil {
		return domain.MemoSideNoteRecord{}, err
	}
	if input.Revision < 1 {
		return domain.MemoSideNoteRecord{}, fmt.Errorf("%w: revision must be positive", ErrRevisionConflict)
	}
	var documentID string
	err = s.db.Pool.QueryRow(ctx, `select document_id from memo_side_notes where id = $1`, id).Scan(&documentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MemoSideNoteRecord{}, ErrSideNoteNotFound
	}
	if err != nil {
		return domain.MemoSideNoteRecord{}, fmt.Errorf("load memo side note: %w", err)
	}
	status, err := s.resolveAnchorStatus(ctx, documentID, input.AnchorBlockID)
	if err != nil {
		return domain.MemoSideNoteRecord{}, err
	}
	if input.Status == "archived" {
		status = "archived"
	}
	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update memo_side_notes
		set anchor_block_id = $2, body_json = $3::jsonb, color = $4, sort_order = $5,
		    collapsed = $6, status = $7, revision = revision + 1, updated_at = $8
		where id = $1 and revision = $9
	`, id, input.AnchorBlockID, string(input.BodyJSON), input.Color, input.SortOrder,
		input.Collapsed, status, now, input.Revision)
	if err != nil {
		return domain.MemoSideNoteRecord{}, fmt.Errorf("save memo side note: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.MemoSideNoteRecord{}, ErrRevisionConflict
	}
	return s.getSideNote(ctx, id)
}

func (s *Service) DeleteSideNote(ctx context.Context, id string) error {
	tag, err := s.db.Pool.Exec(ctx, `delete from memo_side_notes where id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete memo side note: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrSideNoteNotFound
	}
	return nil
}

// SaveMemo keeps the legacy endpoint writable during the BlockNote migration.
func (s *Service) SaveMemo(ctx context.Context, input SaveInput) (domain.MemoRecord, error) {
	slug := normalizeSlug(input.Slug)
	title := normalizeTitle(input.Title)
	now := time.Now().UTC()
	newID := uuid.NewString()
	cardsJSON := json.RawMessage(nil)
	if input.FloatingCards != nil {
		_, normalized, err := NormalizeFloatingCardsJSON(*input.FloatingCards, now)
		if err != nil {
			return domain.MemoRecord{}, err
		}
		cardsJSON = normalized
	}

	if cardsJSON == nil {
		_, err := s.db.Pool.Exec(ctx, `
			insert into tool_memos (id, slug, title, content_html, content_text, created_at, updated_at)
			values (
				coalesce((select id from tool_memos where slug = $1), $2::uuid),
				$1,$3,$4,$5,coalesce((select created_at from tool_memos where slug = $1), $6),$6
			)
			on conflict (slug) do update
			set title = excluded.title, content_html = excluded.content_html,
			    content_text = excluded.content_text, revision = tool_memos.revision + 1,
			    updated_at = excluded.updated_at
		`, slug, newID, title, input.ContentHTML, input.ContentText, now)
		if err != nil {
			return domain.MemoRecord{}, fmt.Errorf("save legacy memo: %w", err)
		}
		return s.getBySlug(ctx, slug)
	}

	_, err := s.db.Pool.Exec(ctx, `
		insert into tool_memos (id, slug, title, content_html, content_text, floating_cards, created_at, updated_at)
		values (
			coalesce((select id from tool_memos where slug = $1), $2::uuid),
			$1,$3,$4,$5,$6::jsonb,coalesce((select created_at from tool_memos where slug = $1), $7),$7
		)
		on conflict (slug) do update
		set title = excluded.title, content_html = excluded.content_html,
		    content_text = excluded.content_text, floating_cards = excluded.floating_cards,
		    revision = tool_memos.revision + 1, updated_at = excluded.updated_at
	`, slug, newID, title, input.ContentHTML, input.ContentText, string(cardsJSON), now)
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("save legacy memo: %w", err)
	}
	return s.getBySlug(ctx, slug)
}

func (s *Service) Save(ctx context.Context, slug, title, contentHTML, contentText string) (domain.MemoRecord, error) {
	return s.SaveMemo(ctx, SaveInput{Slug: slug, Title: title, ContentHTML: contentHTML, ContentText: contentText})
}

func (s *Service) getBySlug(ctx context.Context, slug string) (domain.MemoRecord, error) {
	return scanMemo(s.db.Pool.QueryRow(ctx, memoSelect+` where slug = $1`, slug))
}

func (s *Service) getByID(ctx context.Context, id string) (domain.MemoRecord, error) {
	record, err := scanMemo(s.db.Pool.QueryRow(ctx, memoSelect+` where id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MemoRecord{}, ErrDocumentNotFound
	}
	return record, err
}

const memoSelect = `
	select id, slug, title, coalesce(content_json, '[]'::jsonb)::text,
	       content_html, content_text, coalesce(floating_cards, '[]'::jsonb)::text,
	       schema_version, revision, editor_type, created_at, updated_at
	from tool_memos`

func scanMemo(row pgx.Row) (domain.MemoRecord, error) {
	var record domain.MemoRecord
	var contentJSON, cardsJSON string
	err := row.Scan(&record.ID, &record.Slug, &record.Title, &contentJSON,
		&record.ContentHTML, &record.ContentText, &cardsJSON, &record.SchemaVersion,
		&record.Revision, &record.EditorType, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return domain.MemoRecord{}, err
	}
	record.ContentJSON = json.RawMessage(contentJSON)
	cards, _, err := NormalizeFloatingCardsJSON(json.RawMessage(cardsJSON), time.Now().UTC())
	if err != nil {
		return domain.MemoRecord{}, fmt.Errorf("decode memo floating cards: %w", err)
	}
	record.FloatingCards = cards
	return record, nil
}

func (s *Service) getSideNote(ctx context.Context, id string) (domain.MemoSideNoteRecord, error) {
	record, err := scanSideNote(s.db.Pool.QueryRow(ctx, `
		select id, document_id, anchor_block_id, body_json::text, color, sort_order,
		       collapsed, status, revision, created_at, updated_at
		from memo_side_notes where id = $1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MemoSideNoteRecord{}, ErrSideNoteNotFound
	}
	return record, err
}

type rowScanner interface {
	Scan(...any) error
}

func scanSideNote(row rowScanner) (domain.MemoSideNoteRecord, error) {
	var record domain.MemoSideNoteRecord
	var bodyJSON string
	if err := row.Scan(&record.ID, &record.DocumentID, &record.AnchorBlockID, &bodyJSON,
		&record.Color, &record.SortOrder, &record.Collapsed, &record.Status,
		&record.Revision, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return domain.MemoSideNoteRecord{}, fmt.Errorf("scan memo side note: %w", err)
	}
	record.BodyJSON = json.RawMessage(bodyJSON)
	return record, nil
}

func normalizeDocumentJSON(raw json.RawMessage) ([]byte, []string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("[]")
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, fmt.Errorf("%w: document must be an array of blocks", ErrInvalidContentJSON)
	}
	normalized, err := json.Marshal(blocks)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: cannot normalize document", ErrInvalidContentJSON)
	}
	blockIDs := make([]string, 0)
	collectBlockIDs(blocks, &blockIDs)
	return normalized, blockIDs, nil
}

func collectBlockIDs(blocks []json.RawMessage, result *[]string) {
	for _, raw := range blocks {
		var block struct {
			ID       string            `json:"id"`
			Children []json.RawMessage `json:"children"`
		}
		if json.Unmarshal(raw, &block) != nil {
			continue
		}
		if id := strings.TrimSpace(block.ID); id != "" {
			*result = append(*result, id)
		}
		collectBlockIDs(block.Children, result)
	}
}

func reconcileSideNoteAnchors(ctx context.Context, tx pgx.Tx, documentID string, blockIDs []string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		update memo_side_notes
		set status = case when anchor_block_id = any($2::text[]) then 'active' else 'orphaned' end,
		    updated_at = case
		      when status <> case when anchor_block_id = any($2::text[]) then 'active' else 'orphaned' end then $3
		      else updated_at
		    end
		where document_id = $1 and anchor_block_id is not null and status <> 'archived'
	`, documentID, blockIDs, now)
	if err != nil {
		return fmt.Errorf("reconcile memo side note anchors: %w", err)
	}
	return nil
}

func (s *Service) resolveAnchorStatus(ctx context.Context, documentID string, anchorID *string) (string, error) {
	if anchorID == nil || strings.TrimSpace(*anchorID) == "" {
		return "active", nil
	}
	var contentJSON string
	err := s.db.Pool.QueryRow(ctx, `select content_json::text from tool_memos where id = $1`, documentID).Scan(&contentJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDocumentNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load memo document anchors: %w", err)
	}
	_, blockIDs, err := normalizeDocumentJSON(json.RawMessage(contentJSON))
	if err != nil {
		return "", err
	}
	for _, blockID := range blockIDs {
		if blockID == *anchorID {
			return "active", nil
		}
	}
	return "orphaned", nil
}

func normalizeSideNoteInput(input SideNoteInput) (SideNoteInput, error) {
	if len(input.BodyJSON) == 0 {
		input.BodyJSON = json.RawMessage(`{"text":""}`)
	}
	var body any
	if json.Unmarshal(input.BodyJSON, &body) != nil {
		return SideNoteInput{}, fmt.Errorf("%w: body_json must be valid JSON", ErrInvalidSideNote)
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return SideNoteInput{}, fmt.Errorf("%w: cannot normalize body_json", ErrInvalidSideNote)
	}
	input.BodyJSON = normalized
	input.Color = normalizeFloatingCardColor(input.Color)
	if input.SortOrder < 0 {
		input.SortOrder = 0
	}
	if input.AnchorBlockID != nil {
		trimmed := strings.TrimSpace(*input.AnchorBlockID)
		if trimmed == "" {
			input.AnchorBlockID = nil
		} else {
			input.AnchorBlockID = &trimmed
		}
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "orphaned" && input.Status != "archived" {
		return SideNoteInput{}, fmt.Errorf("%w: unsupported status", ErrInvalidSideNote)
	}
	return input, nil
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
			ID: cardID, Content: payload.Content, Color: normalizeFloatingCardColor(payload.Color),
			CreatedAt: createdAt, UpdatedAt: updatedAt,
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

func normalizeSlug(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultSlug
	}
	return value
}

func normalizeTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "随手记"
	}
	return value
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
