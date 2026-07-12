package canvas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
)

var (
	ErrBoardNotFound    = errors.New("canvas board not found")
	ErrAssetNotFound    = errors.New("canvas asset not found")
	ErrRevisionConflict = errors.New("canvas board was changed by another session")
	ErrInvalidScene     = errors.New("canvas scene must be a JSON object")
)

var emptyScene = json.RawMessage(`{"elements":[],"appState":{},"files":{}}`)

type Service struct {
	db    *database.DB
	store *storage.LocalStore
}

type SaveInput struct {
	Title    string
	Scene    json.RawMessage
	Revision int64
}

func NewService(db *database.DB, store *storage.LocalStore) *Service {
	return &Service{db: db, store: store}
}

func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "未命名画板"
	}
	if len([]rune(title)) > 120 {
		return string([]rune(title)[:120])
	}
	return title
}

func validateScene(scene json.RawMessage) error {
	if len(scene) == 0 || !json.Valid(scene) {
		return ErrInvalidScene
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(scene, &value); err != nil || value == nil {
		return ErrInvalidScene
	}
	return nil
}

func (s *Service) Create(ctx context.Context, title string) (domain.CanvasBoardRecord, error) {
	now := time.Now().UTC()
	record := domain.CanvasBoardRecord{
		ID: uuid.NewString(), Title: normalizeTitle(title), Scene: emptyScene, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.db.Pool.Exec(ctx, `insert into canvas_boards (id,title,scene_json,revision,created_at,updated_at)
		values ($1,$2,$3,1,$4,$4)`, record.ID, record.Title, record.Scene, now)
	if err != nil {
		return domain.CanvasBoardRecord{}, fmt.Errorf("create canvas board: %w", err)
	}
	return record, nil
}

func (s *Service) List(ctx context.Context) ([]domain.CanvasBoardRecord, error) {
	rows, err := s.db.Pool.Query(ctx, `select id,title,revision,created_at,updated_at
		from canvas_boards order by updated_at desc, id desc`)
	if err != nil {
		return nil, fmt.Errorf("list canvas boards: %w", err)
	}
	defer rows.Close()
	items := make([]domain.CanvasBoardRecord, 0)
	for rows.Next() {
		var item domain.CanvasBoardRecord
		if err := rows.Scan(&item.ID, &item.Title, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan canvas board: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canvas boards: %w", err)
	}
	return items, nil
}

func (s *Service) Get(ctx context.Context, id string) (domain.CanvasBoardRecord, error) {
	var item domain.CanvasBoardRecord
	err := s.db.Pool.QueryRow(ctx, `select id,title,scene_json,revision,created_at,updated_at
		from canvas_boards where id=$1`, id).Scan(&item.ID, &item.Title, &item.Scene, &item.Revision, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CanvasBoardRecord{}, ErrBoardNotFound
	}
	if err != nil {
		return domain.CanvasBoardRecord{}, fmt.Errorf("get canvas board: %w", err)
	}
	return item, nil
}

func (s *Service) Save(ctx context.Context, id string, input SaveInput) (domain.CanvasBoardRecord, error) {
	if err := validateScene(input.Scene); err != nil {
		return domain.CanvasBoardRecord{}, err
	}
	var item domain.CanvasBoardRecord
	err := s.db.Pool.QueryRow(ctx, `update canvas_boards set title=$2,scene_json=$3,revision=revision+1,updated_at=now()
		where id=$1 and revision=$4 returning id,title,scene_json,revision,created_at,updated_at`,
		id, normalizeTitle(input.Title), input.Scene, input.Revision,
	).Scan(&item.ID, &item.Title, &item.Scene, &item.Revision, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if lookupErr := s.db.Pool.QueryRow(ctx, `select exists(select 1 from canvas_boards where id=$1)`, id).Scan(&exists); lookupErr != nil {
			return domain.CanvasBoardRecord{}, fmt.Errorf("check canvas board revision: %w", lookupErr)
		}
		if exists {
			return domain.CanvasBoardRecord{}, ErrRevisionConflict
		}
		return domain.CanvasBoardRecord{}, ErrBoardNotFound
	}
	if err != nil {
		return domain.CanvasBoardRecord{}, fmt.Errorf("save canvas board: %w", err)
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	rows, err := s.db.Pool.Query(ctx, `select tf.id,tf.storage_path from canvas_assets ca join tool_files tf on tf.id=ca.file_id where ca.board_id=$1`, id)
	if err != nil {
		return fmt.Errorf("list canvas assets for deletion: %w", err)
	}
	type storedFile struct{ id, path string }
	files := make([]storedFile, 0)
	for rows.Next() {
		var file storedFile
		if err := rows.Scan(&file.id, &file.path); err != nil {
			rows.Close()
			return fmt.Errorf("scan canvas asset for deletion: %w", err)
		}
		files = append(files, file)
	}
	rows.Close()

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin canvas board deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `delete from canvas_boards where id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete canvas board: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBoardNotFound
	}
	for _, file := range files {
		if _, err := tx.Exec(ctx, `delete from tool_files where id=$1`, file.id); err != nil {
			return fmt.Errorf("delete canvas asset metadata: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit canvas board deletion: %w", err)
	}
	for _, file := range files {
		_ = s.store.Delete(file.path)
	}
	return nil
}

func (s *Service) UploadAsset(ctx context.Context, boardID, canvasFileID string, file multipart.File, header *multipart.FileHeader) (domain.CanvasAssetRecord, error) {
	if _, err := s.Get(ctx, boardID); err != nil {
		return domain.CanvasAssetRecord{}, err
	}
	storedName, path, size, err := s.store.SaveUploadedFile(file, header.Filename, "canvas-assets")
	if err != nil {
		return domain.CanvasAssetRecord{}, fmt.Errorf("store canvas asset: %w", err)
	}
	now := time.Now().UTC()
	fileID := uuid.NewString()
	record := domain.CanvasAssetRecord{
		ID: uuid.NewString(), BoardID: boardID, FileID: fileID, CanvasFileID: strings.TrimSpace(canvasFileID),
		OriginalName: header.Filename, MIMEType: header.Header.Get("Content-Type"), SizeBytes: size, StoragePath: path, CreatedAt: now,
	}
	if record.CanvasFileID == "" {
		record.CanvasFileID = uuid.NewString()
	}
	if record.MIMEType == "" {
		record.MIMEType = "application/octet-stream"
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		_ = s.store.Delete(path)
		return domain.CanvasAssetRecord{}, fmt.Errorf("begin canvas asset upload: %w", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `insert into tool_files (id,original_name,stored_name,storage_path,mime_type,size_bytes,category,expires_at,created_at)
		values ($1,$2,$3,$4,$5,$6,'canvas-asset',null,$7)`, fileID, record.OriginalName, storedName, path, record.MIMEType, size, now)
	if err == nil {
		_, err = tx.Exec(ctx, `insert into canvas_assets (id,board_id,file_id,canvas_file_id,created_at) values ($1,$2,$3,$4,$5)`,
			record.ID, boardID, fileID, record.CanvasFileID, now)
	}
	if err != nil {
		_ = s.store.Delete(path)
		return domain.CanvasAssetRecord{}, fmt.Errorf("insert canvas asset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		_ = s.store.Delete(path)
		return domain.CanvasAssetRecord{}, fmt.Errorf("commit canvas asset upload: %w", err)
	}
	return record, nil
}

func (s *Service) GetAsset(ctx context.Context, id string) (domain.CanvasAssetRecord, error) {
	var item domain.CanvasAssetRecord
	err := s.db.Pool.QueryRow(ctx, `select ca.id,ca.board_id,ca.file_id,ca.canvas_file_id,tf.original_name,tf.mime_type,tf.size_bytes,tf.storage_path,ca.created_at
		from canvas_assets ca join tool_files tf on tf.id=ca.file_id where ca.id=$1`, id).Scan(
		&item.ID, &item.BoardID, &item.FileID, &item.CanvasFileID, &item.OriginalName, &item.MIMEType, &item.SizeBytes, &item.StoragePath, &item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CanvasAssetRecord{}, ErrAssetNotFound
	}
	if err != nil {
		return domain.CanvasAssetRecord{}, fmt.Errorf("get canvas asset: %w", err)
	}
	return item, nil
}
