package music

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
)

var ErrUnsupportedAudio = errors.New("unsupported audio file")
var ErrInvalidCursor = errors.New("invalid cursor")

var supportedExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".ogg": true,
	".m4a": true, ".aac": true, ".opus": true, ".webm": true,
}

type Service struct {
	db    *database.DB
	store *storage.LocalStore
}

type UploadInput struct {
	File         multipart.File
	Header       *multipart.FileHeader
	Title        string
	Artist       string
	Note         string
	Duration     *float64
	AudioQuality json.RawMessage
}

type Page struct {
	Tracks     []domain.MusicTrackRecord `json:"tracks"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type cursorValue struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func NewService(db *database.DB, store *storage.LocalStore) *Service {
	return &Service{db: db, store: store}
}

func (s *Service) SaveUpload(ctx context.Context, input UploadInput) (domain.MusicTrackRecord, error) {
	if input.File == nil || input.Header == nil {
		return domain.MusicTrackRecord{}, fmt.Errorf("file is required")
	}
	ext := strings.ToLower(filepath.Ext(input.Header.Filename))
	mimeType := strings.TrimSpace(input.Header.Header.Get("Content-Type"))
	if !strings.HasPrefix(mimeType, "audio/") && !supportedExtensions[ext] {
		return domain.MusicTrackRecord{}, ErrUnsupportedAudio
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(ext)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(input.Header.Filename), filepath.Ext(input.Header.Filename))
	}
	if len(input.AudioQuality) == 0 || !json.Valid(input.AudioQuality) {
		input.AudioQuality = json.RawMessage(`{}`)
	}

	storedName, storagePath, size, err := s.store.SaveUploadedFile(input.File, input.Header.Filename, "music")
	if err != nil {
		return domain.MusicTrackRecord{}, fmt.Errorf("store music file: %w", err)
	}

	now := time.Now().UTC()
	fileID := uuid.NewString()
	record := domain.MusicTrackRecord{
		ID: uuid.NewString(), Title: title, Artist: strings.TrimSpace(input.Artist), Note: strings.TrimSpace(input.Note),
		OriginalName: input.Header.Filename, MIMEType: mimeType, SizeBytes: size, Duration: input.Duration,
		AudioQuality: input.AudioQuality, StoragePath: storagePath, CreatedAt: now,
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		_ = s.store.Delete(storagePath)
		return domain.MusicTrackRecord{}, fmt.Errorf("begin music upload: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `insert into tool_files
		(id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,'music',null,$7)`, fileID, record.OriginalName, storedName, storagePath, record.MIMEType, size, now); err == nil {
		_, err = tx.Exec(ctx, `insert into music_tracks
			(id, file_id, title, artist, note, duration_seconds, audio_quality, created_at)
			values ($1,$2,$3,$4,$5,$6,$7,$8)`, record.ID, fileID, record.Title, record.Artist, record.Note, record.Duration, record.AudioQuality, now)
	}
	if err != nil {
		_ = s.store.Delete(storagePath)
		return domain.MusicTrackRecord{}, fmt.Errorf("insert music track: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		_ = s.store.Delete(storagePath)
		return domain.MusicTrackRecord{}, fmt.Errorf("commit music upload: %w", err)
	}
	return record, nil
}

func (s *Service) List(ctx context.Context, cursor string, limit int) (Page, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	args := []any{limit + 1}
	where := ""
	if cursor != "" {
		value, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		where = "where (mt.created_at, mt.id) < ($2, $3::uuid)"
		args = append(args, value.CreatedAt, value.ID)
	}
	rows, err := s.db.Pool.Query(ctx, fmt.Sprintf(`select mt.id, mt.title, mt.artist, mt.note,
		tf.original_name, tf.mime_type, tf.size_bytes, mt.duration_seconds, mt.audio_quality, tf.storage_path, mt.created_at
		from music_tracks mt join tool_files tf on tf.id = mt.file_id %s
		order by mt.created_at desc, mt.id desc limit $1`, where), args...)
	if err != nil {
		return Page{}, fmt.Errorf("list music tracks: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MusicTrackRecord, 0, limit)
	for rows.Next() {
		var item domain.MusicTrackRecord
		if err := rows.Scan(&item.ID, &item.Title, &item.Artist, &item.Note, &item.OriginalName, &item.MIMEType,
			&item.SizeBytes, &item.Duration, &item.AudioQuality, &item.StoragePath, &item.CreatedAt); err != nil {
			return Page{}, fmt.Errorf("scan music track: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate music tracks: %w", err)
	}
	page := Page{Tracks: items}
	if len(items) > limit {
		page.Tracks = items[:limit]
		last := page.Tracks[len(page.Tracks)-1]
		page.NextCursor = encodeCursor(cursorValue{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Service) GetByID(ctx context.Context, id string) (domain.MusicTrackRecord, error) {
	var item domain.MusicTrackRecord
	err := s.db.Pool.QueryRow(ctx, `select mt.id, mt.title, mt.artist, mt.note,
		tf.original_name, tf.mime_type, tf.size_bytes, mt.duration_seconds, mt.audio_quality, tf.storage_path, mt.created_at
		from music_tracks mt join tool_files tf on tf.id = mt.file_id where mt.id = $1`, id).Scan(
		&item.ID, &item.Title, &item.Artist, &item.Note, &item.OriginalName, &item.MIMEType,
		&item.SizeBytes, &item.Duration, &item.AudioQuality, &item.StoragePath, &item.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.MusicTrackRecord{}, fmt.Errorf("music track not found")
		}
		return domain.MusicTrackRecord{}, fmt.Errorf("get music track: %w", err)
	}
	return item, nil
}

func encodeCursor(value cursorValue) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (cursorValue, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursorValue{}, ErrInvalidCursor
	}
	var cursor cursorValue
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.CreatedAt.IsZero() || uuid.Validate(cursor.ID) != nil {
		return cursorValue{}, ErrInvalidCursor
	}
	return cursor, nil
}
