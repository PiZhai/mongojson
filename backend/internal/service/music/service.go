package music

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
)

var ErrUnsupportedAudio = errors.New("unsupported audio file")
var ErrUnsupportedLyric = errors.New("unsupported lyric file")
var ErrUnsupportedArtwork = errors.New("unsupported artwork file")
var ErrArtworkTooLarge = errors.New("artwork file exceeds 8 MiB")
var ErrInvalidCursor = errors.New("invalid cursor")
var ErrTrackNotFound = errors.New("music track not found")
var ErrLyricsNotFound = errors.New("music lyrics not found")
var ErrArtworkNotFound = errors.New("music artwork not found")

const maxArtworkBytes int64 = 8 << 20

var supportedExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".ogg": true,
	".m4a": true, ".aac": true, ".opus": true, ".webm": true,
}

const trackSelectColumns = `mt.id, mt.file_id, mt.lyric_file_id, mt.artwork_file_id, mt.title, mt.artist, mt.note,
	tf.original_name, tf.mime_type, tf.size_bytes, mt.duration_seconds, mt.audio_quality,
	coalesce(mt.content_sha256, ''), tf.storage_path, coalesce(lf.original_name, ''), coalesce(lf.mime_type, ''),
	coalesce(lf.storage_path, ''), coalesce(af.original_name, ''), coalesce(af.mime_type, ''),
	coalesce(af.storage_path, ''), coalesce(af.sha256, ''), mt.created_at`

type Service struct {
	db    *database.DB
	store *storage.LocalStore
}

type UploadInput struct {
	File          multipart.File
	Header        *multipart.FileHeader
	Lyric         multipart.File
	LyricHeader   *multipart.FileHeader
	Artwork       multipart.File
	ArtworkHeader *multipart.FileHeader
	Title         string
	Artist        string
	Note          string
	Duration      *float64
	AudioQuality  json.RawMessage
}

type UploadResult struct {
	Track     domain.MusicTrackRecord `json:"track"`
	Duplicate bool                    `json:"duplicate"`
}

type Page struct {
	Tracks     []domain.MusicTrackRecord `json:"tracks"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type cursorValue struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

type rowScanner interface {
	Scan(...any) error
}

func NewService(db *database.DB, store *storage.LocalStore) *Service {
	return &Service{db: db, store: store}
}

func (s *Service) SaveUpload(ctx context.Context, input UploadInput) (UploadResult, error) {
	if input.File == nil || input.Header == nil {
		return UploadResult{}, fmt.Errorf("file is required")
	}
	ext := strings.ToLower(filepath.Ext(input.Header.Filename))
	mimeType := strings.TrimSpace(input.Header.Header.Get("Content-Type"))
	if !strings.HasPrefix(mimeType, "audio/") && !supportedExtensions[ext] {
		return UploadResult{}, ErrUnsupportedAudio
	}
	if input.LyricHeader != nil && strings.ToLower(filepath.Ext(input.LyricHeader.Filename)) != ".lrc" {
		return UploadResult{}, ErrUnsupportedLyric
	}
	artworkMIME := ""
	if input.Artwork != nil || input.ArtworkHeader != nil {
		if input.Artwork == nil || input.ArtworkHeader == nil {
			return UploadResult{}, ErrUnsupportedArtwork
		}
		var artworkErr error
		artworkMIME, artworkErr = validateArtwork(input.Artwork, input.ArtworkHeader)
		if artworkErr != nil {
			return UploadResult{}, artworkErr
		}
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

	storedName, storagePath, size, digest, err := s.store.SaveUploadedFileWithSHA256(input.File, input.Header.Filename, "music")
	if err != nil {
		return UploadResult{}, fmt.Errorf("store music file: %w", err)
	}
	cleanupAudio := func() { _ = s.store.Delete(storagePath) }

	if existing, found, err := s.findByHash(ctx, digest); err != nil {
		cleanupAudio()
		return UploadResult{}, err
	} else if found {
		cleanupAudio()
		if input.Lyric != nil && input.LyricHeader != nil && existing.LyricFileID == nil {
			existing, err = s.attachLyric(ctx, existing, input.Lyric, input.LyricHeader)
			if err != nil {
				return UploadResult{}, err
			}
		} else if input.Lyric != nil {
			_ = input.Lyric.Close()
		}
		if input.Artwork != nil && input.ArtworkHeader != nil && existing.ArtworkFileID == nil {
			existing, err = s.attachArtwork(ctx, existing, input.Artwork, input.ArtworkHeader, artworkMIME)
			if err != nil {
				return UploadResult{}, err
			}
		} else if input.Artwork != nil {
			_ = input.Artwork.Close()
		}
		return UploadResult{Track: validateTrackFiles(existing), Duplicate: true}, nil
	}

	now := time.Now().UTC()
	fileID := uuid.NewString()
	record := domain.MusicTrackRecord{
		ID: uuid.NewString(), FileID: fileID, Title: title, Artist: strings.TrimSpace(input.Artist), Note: strings.TrimSpace(input.Note),
		OriginalName: input.Header.Filename, MIMEType: mimeType, SizeBytes: size, Duration: input.Duration,
		AudioQuality: input.AudioQuality, ContentSHA256: digest, StoragePath: storagePath, CreatedAt: now,
	}

	var lyricStoredName string
	var lyricSize int64
	if input.Lyric != nil && input.LyricHeader != nil {
		lyricID := uuid.NewString()
		lyricMIME := strings.TrimSpace(input.LyricHeader.Header.Get("Content-Type"))
		if lyricMIME == "" {
			lyricMIME = "text/plain; charset=utf-8"
		}
		lyricStoredName, record.LyricStoragePath, lyricSize, err = s.store.SaveUploadedFile(input.Lyric, input.LyricHeader.Filename, "music-lyrics")
		if err != nil {
			cleanupAudio()
			return UploadResult{}, fmt.Errorf("store lyric file: %w", err)
		}
		record.LyricFileID = &lyricID
		record.LyricFileName = input.LyricHeader.Filename
		record.LyricMIMEType = lyricMIME
	}
	var artworkStoredName string
	var artworkSize int64
	if input.Artwork != nil && input.ArtworkHeader != nil {
		artworkID := uuid.NewString()
		artworkStoredName, record.ArtworkStoragePath, artworkSize, record.ArtworkContentSHA256, err = s.store.SaveUploadedFileWithSHA256(input.Artwork, input.ArtworkHeader.Filename, "music-artwork")
		if err != nil {
			cleanupAudio()
			_ = s.store.Delete(record.LyricStoragePath)
			return UploadResult{}, fmt.Errorf("store artwork file: %w", err)
		}
		if artworkSize > maxArtworkBytes {
			cleanupAudio()
			_ = s.store.Delete(record.LyricStoragePath)
			_ = s.store.Delete(record.ArtworkStoragePath)
			return UploadResult{}, ErrArtworkTooLarge
		}
		record.ArtworkFileID = &artworkID
		record.ArtworkFileName = input.ArtworkHeader.Filename
		record.ArtworkMIMEType = artworkMIME
		record.ArtworkAvailable = true
	}
	cleanupFiles := func() {
		cleanupAudio()
		_ = s.store.Delete(record.LyricStoragePath)
		_ = s.store.Delete(record.ArtworkStoragePath)
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		cleanupFiles()
		return UploadResult{}, fmt.Errorf("begin music upload: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `insert into tool_files
		(id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,'music',null,$7)`, fileID, record.OriginalName, storedName, storagePath, record.MIMEType, size, now)
	if err == nil && record.LyricFileID != nil {
		_, err = tx.Exec(ctx, `insert into tool_files
			(id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
			values ($1,$2,$3,$4,$5,$6,'music-lyrics',null,$7)`, *record.LyricFileID, record.LyricFileName,
			lyricStoredName, record.LyricStoragePath, record.LyricMIMEType, lyricSize, now)
	}
	if err == nil && record.ArtworkFileID != nil {
		_, err = tx.Exec(ctx, `insert into tool_files
			(id, original_name, stored_name, storage_path, mime_type, size_bytes, category, sha256, expires_at, created_at)
			values ($1,$2,$3,$4,$5,$6,'music-artwork',$7,null,$8)`, *record.ArtworkFileID, record.ArtworkFileName,
			artworkStoredName, record.ArtworkStoragePath, record.ArtworkMIMEType, artworkSize, record.ArtworkContentSHA256, now)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `insert into music_tracks
			(id, file_id, lyric_file_id, artwork_file_id, content_sha256, title, artist, note, duration_seconds, audio_quality, created_at)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, record.ID, fileID, record.LyricFileID, record.ArtworkFileID, digest,
			record.Title, record.Artist, record.Note, record.Duration, record.AudioQuality, now)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		cleanupFiles()
		if isDigestConflict(err) {
			if existing, found, lookupErr := s.findByHash(ctx, digest); lookupErr == nil && found {
				return UploadResult{Track: validateTrackFiles(existing), Duplicate: true}, nil
			}
		}
		return UploadResult{}, fmt.Errorf("insert music track: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		cleanupFiles()
		return UploadResult{}, fmt.Errorf("commit music upload: %w", err)
	}
	return UploadResult{Track: validateTrackFiles(record)}, nil
}

func (s *Service) attachLyric(ctx context.Context, track domain.MusicTrackRecord, file multipart.File, header *multipart.FileHeader) (domain.MusicTrackRecord, error) {
	storedName, path, size, err := s.store.SaveUploadedFile(file, header.Filename, "music-lyrics")
	if err != nil {
		return domain.MusicTrackRecord{}, fmt.Errorf("store lyric file: %w", err)
	}
	lyricID := uuid.NewString()
	mimeType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = "text/plain; charset=utf-8"
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, fmt.Errorf("begin lyric attach: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `insert into tool_files
		(id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,'music-lyrics',null,now())`, lyricID, header.Filename, storedName, path, mimeType, size); err == nil {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `update music_tracks set lyric_file_id=$1 where id=$2 and lyric_file_id is null`, lyricID, track.ID)
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("lyrics already attached")
		}
	}
	if err != nil {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, fmt.Errorf("attach lyric: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, fmt.Errorf("commit lyric attach: %w", err)
	}
	return s.GetByID(ctx, track.ID)
}

func (s *Service) attachArtwork(ctx context.Context, track domain.MusicTrackRecord, file multipart.File, header *multipart.FileHeader, mimeType string) (domain.MusicTrackRecord, error) {
	storedName, path, size, digest, err := s.store.SaveUploadedFileWithSHA256(file, header.Filename, "music-artwork")
	if err != nil {
		return domain.MusicTrackRecord{}, fmt.Errorf("store artwork file: %w", err)
	}
	if size > maxArtworkBytes {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, ErrArtworkTooLarge
	}
	artworkID := uuid.NewString()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, fmt.Errorf("begin artwork attach: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `insert into tool_files
		(id, original_name, stored_name, storage_path, mime_type, size_bytes, category, sha256, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,'music-artwork',$7,null,now())`, artworkID, header.Filename, storedName, path, mimeType, size, digest); err == nil {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `update music_tracks set artwork_file_id=$1 where id=$2 and artwork_file_id is null`, artworkID, track.ID)
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("artwork already attached")
		}
	}
	if err != nil {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, fmt.Errorf("attach artwork: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		_ = s.store.Delete(path)
		return domain.MusicTrackRecord{}, fmt.Errorf("commit artwork attach: %w", err)
	}
	return s.GetByID(ctx, track.ID)
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
	rows, err := s.db.Pool.Query(ctx, fmt.Sprintf(`select %s
		from music_tracks mt join tool_files tf on tf.id = mt.file_id
		left join tool_files lf on lf.id = mt.lyric_file_id
		left join tool_files af on af.id = mt.artwork_file_id %s
		order by mt.created_at desc, mt.id desc limit $1`, trackSelectColumns, where), args...)
	if err != nil {
		return Page{}, fmt.Errorf("list music tracks: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MusicTrackRecord, 0, limit)
	for rows.Next() {
		item, err := scanTrack(rows)
		if err != nil {
			return Page{}, fmt.Errorf("scan music track: %w", err)
		}
		items = append(items, validateTrackFiles(item))
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
	row := s.db.Pool.QueryRow(ctx, fmt.Sprintf(`select %s from music_tracks mt
		join tool_files tf on tf.id = mt.file_id left join tool_files lf on lf.id = mt.lyric_file_id
		left join tool_files af on af.id = mt.artwork_file_id
		where mt.id = $1`, trackSelectColumns), id)
	item, err := scanTrack(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.MusicTrackRecord{}, ErrTrackNotFound
		}
		return domain.MusicTrackRecord{}, fmt.Errorf("get music track: %w", err)
	}
	return validateTrackFiles(item), nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	record, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin music delete: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `delete from music_tracks where id=$1`, id); err == nil {
		_, err = tx.Exec(ctx, `delete from tool_files where id=$1 or id=$2 or id=$3`, record.FileID, record.LyricFileID, record.ArtworkFileID)
	}
	if err != nil {
		return fmt.Errorf("delete music metadata: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit music delete: %w", err)
	}
	if err := errors.Join(s.store.Delete(record.StoragePath), s.store.Delete(record.LyricStoragePath), s.store.Delete(record.ArtworkStoragePath)); err != nil {
		return fmt.Errorf("delete music files: %w", err)
	}
	return nil
}

func (s *Service) findByHash(ctx context.Context, digest string) (domain.MusicTrackRecord, bool, error) {
	item, found, err := s.findIndexedByHash(ctx, digest)
	if err != nil || found {
		return item, found, err
	}
	return s.findLegacyByHash(ctx, digest)
}

func (s *Service) findIndexedByHash(ctx context.Context, digest string) (domain.MusicTrackRecord, bool, error) {
	row := s.db.Pool.QueryRow(ctx, fmt.Sprintf(`select %s from music_tracks mt
		join tool_files tf on tf.id = mt.file_id left join tool_files lf on lf.id = mt.lyric_file_id
		left join tool_files af on af.id = mt.artwork_file_id
		where mt.content_sha256 = $1`, trackSelectColumns), digest)
	item, err := scanTrack(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MusicTrackRecord{}, false, nil
	}
	if err != nil {
		return domain.MusicTrackRecord{}, false, fmt.Errorf("find duplicate music: %w", err)
	}
	return item, true, nil
}

func (s *Service) findLegacyByHash(ctx context.Context, targetDigest string) (domain.MusicTrackRecord, bool, error) {
	rows, err := s.db.Pool.Query(ctx, `select mt.id, tf.storage_path from music_tracks mt
		join tool_files tf on tf.id=mt.file_id where mt.content_sha256 is null`)
	if err != nil {
		return domain.MusicTrackRecord{}, false, fmt.Errorf("list legacy music hashes: %w", err)
	}
	defer rows.Close()
	type legacyTrack struct{ id, path string }
	var legacy []legacyTrack
	for rows.Next() {
		var item legacyTrack
		if err := rows.Scan(&item.id, &item.path); err != nil {
			return domain.MusicTrackRecord{}, false, fmt.Errorf("scan legacy music hash: %w", err)
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		return domain.MusicTrackRecord{}, false, fmt.Errorf("iterate legacy music hashes: %w", err)
	}

	for _, item := range legacy {
		digest, err := hashFile(item.path)
		if err != nil {
			continue
		}
		if _, err := s.db.Pool.Exec(ctx, `update music_tracks set content_sha256=$1 where id=$2 and content_sha256 is null`, digest, item.id); err != nil && !isDigestConflict(err) {
			return domain.MusicTrackRecord{}, false, fmt.Errorf("backfill music hash: %w", err)
		}
		if digest == targetDigest {
			track, err := s.GetByID(ctx, item.id)
			return track, err == nil, err
		}
	}
	return domain.MusicTrackRecord{}, false, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func scanTrack(row rowScanner) (domain.MusicTrackRecord, error) {
	var item domain.MusicTrackRecord
	err := row.Scan(&item.ID, &item.FileID, &item.LyricFileID, &item.ArtworkFileID, &item.Title, &item.Artist, &item.Note,
		&item.OriginalName, &item.MIMEType, &item.SizeBytes, &item.Duration, &item.AudioQuality,
		&item.ContentSHA256, &item.StoragePath, &item.LyricFileName, &item.LyricMIMEType,
		&item.LyricStoragePath, &item.ArtworkFileName, &item.ArtworkMIMEType, &item.ArtworkStoragePath,
		&item.ArtworkContentSHA256, &item.CreatedAt)
	return item, err
}

func validateTrackFiles(item domain.MusicTrackRecord) domain.MusicTrackRecord {
	item.FileAvailable = true
	if _, err := os.Stat(item.StoragePath); err != nil {
		item.FileAvailable = false
		item.RecordIssue = "音频文件缺失，建议删除此歌曲记录"
		return item
	}
	if item.LyricStoragePath != "" {
		if _, err := os.Stat(item.LyricStoragePath); err != nil {
			item.RecordIssue = "歌词文件缺失，歌曲仍可播放"
		}
	}
	item.ArtworkAvailable = false
	if item.ArtworkStoragePath != "" {
		if _, err := os.Stat(item.ArtworkStoragePath); err == nil {
			item.ArtworkAvailable = true
		}
	}
	return item
}

func validateArtwork(file multipart.File, header *multipart.FileHeader) (string, error) {
	if header.Size > maxArtworkBytes {
		return "", ErrArtworkTooLarge
	}
	buffer := make([]byte, 512)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", ErrUnsupportedArtwork
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", ErrUnsupportedArtwork
	}
	detected := http.DetectContentType(buffer[:n])
	if detected != "image/jpeg" && detected != "image/png" && detected != "image/webp" {
		return "", ErrUnsupportedArtwork
	}
	return detected, nil
}

func isDigestConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_music_tracks_content_sha256"
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
