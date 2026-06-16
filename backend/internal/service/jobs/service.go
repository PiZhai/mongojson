package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
)

type Service struct {
	db        *database.DB
	store     *storage.LocalStore
	retention time.Duration
	queue     chan string
	mu        sync.Mutex
}

func NewService(db *database.DB, store *storage.LocalStore, retention time.Duration) *Service {
	return &Service{
		db:        db,
		store:     store,
		retention: retention,
		queue:     make(chan string, 64),
	}
}

func (s *Service) Queue() <-chan string {
	return s.queue
}

func (s *Service) Enqueue(id string) {
	s.queue <- id
}

func (s *Service) Create(ctx context.Context, toolType string, inputFileID *string, params map[string]any) (domain.JobRecord, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(s.retention)
	record := domain.JobRecord{
		ID:          uuid.NewString(),
		ToolType:    toolType,
		Status:      "pending",
		InputFileID: inputFileID,
		Params:      params,
		CreatedAt:   now,
		ExpiresAt:   &expiresAt,
	}

	_, err := s.db.Pool.Exec(ctx, `
		insert into tool_jobs (id, tool_type, status, input_file_id, params, created_at, expires_at)
		values ($1,$2,$3,$4,$5,$6,$7)
	`, record.ID, record.ToolType, record.Status, record.InputFileID, record.Params, record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return domain.JobRecord{}, fmt.Errorf("insert job: %w", err)
	}

	s.Enqueue(record.ID)
	return record, nil
}

func (s *Service) Get(ctx context.Context, id string) (domain.JobRecord, error) {
	var record domain.JobRecord
	var params []byte
	err := s.db.Pool.QueryRow(ctx, `
		select id, tool_type, status, input_file_id, output_file_id, params, error_message, created_at, finished_at, expires_at
		from tool_jobs where id = $1
	`, id).Scan(
		&record.ID,
		&record.ToolType,
		&record.Status,
		&record.InputFileID,
		&record.OutputFileID,
		&params,
		&record.ErrorMessage,
		&record.CreatedAt,
		&record.FinishedAt,
		&record.ExpiresAt,
	)
	if err != nil {
		return domain.JobRecord{}, fmt.Errorf("get job: %w", err)
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &record.Params)
	}
	return record, nil
}

func (s *Service) markRunning(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `update tool_jobs set status = 'running' where id = $1`, id)
	return err
}

func (s *Service) markFailed(ctx context.Context, id string, message string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		update tool_jobs set status = 'failed', error_message = $2, finished_at = $3 where id = $1
	`, id, message, now)
	return err
}

func (s *Service) markSuccess(ctx context.Context, id string, outputFileID string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		update tool_jobs set status = 'success', output_file_id = $2, finished_at = $3 where id = $1
	`, id, outputFileID, now)
	return err
}

func (s *Service) Process(ctx context.Context, cfg config.Config, id string) {
	job, err := s.Get(ctx, id)
	if err != nil {
		return
	}
	_ = s.markRunning(ctx, id)

	output, mimeType, filename, processErr := s.runProcessor(ctx, cfg, job)
	if processErr != nil {
		_ = s.markFailed(ctx, id, processErr.Error())
		return
	}

	storedName, storagePath, size, err := s.store.SaveBytes(output, filename, "outputs")
	if err != nil {
		_ = s.markFailed(ctx, id, err.Error())
		return
	}

	expiresAt := time.Now().UTC().Add(s.retention)
	fileRecord := domain.FileRecord{
		ID:           uuid.NewString(),
		OriginalName: filename,
		StoredName:   storedName,
		StoragePath:  storagePath,
		MIMEType:     mimeType,
		SizeBytes:    size,
		Category:     "output",
		CreatedAt:    time.Now().UTC(),
		ExpiresAt:    &expiresAt,
	}

	_, err = s.db.Pool.Exec(ctx, `
		insert into tool_files (id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, fileRecord.ID, fileRecord.OriginalName, fileRecord.StoredName, fileRecord.StoragePath, fileRecord.MIMEType, fileRecord.SizeBytes, fileRecord.Category, fileRecord.ExpiresAt, fileRecord.CreatedAt)
	if err != nil {
		_ = s.markFailed(ctx, id, err.Error())
		return
	}

	_ = s.markSuccess(ctx, id, fileRecord.ID)
}

func (s *Service) runProcessor(ctx context.Context, cfg config.Config, job domain.JobRecord) ([]byte, string, string, error) {
	if job.InputFileID == nil {
		return nil, "", "", fmt.Errorf("job has no input file")
	}

	var input domain.FileRecord
	err := s.db.Pool.QueryRow(ctx, `
		select id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at
		from tool_files where id = $1
	`, *job.InputFileID).Scan(
		&input.ID,
		&input.OriginalName,
		&input.StoredName,
		&input.StoragePath,
		&input.MIMEType,
		&input.SizeBytes,
		&input.Category,
		&input.ExpiresAt,
		&input.CreatedAt,
	)
	if err != nil {
		return nil, "", "", fmt.Errorf("get input file: %w", err)
	}

	return nil, "", "", fmt.Errorf("document conversion is temporarily disabled in this build")
}
