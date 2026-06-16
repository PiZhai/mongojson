package filemeta

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
)

type Service struct {
	db        *database.DB
	store     *storage.LocalStore
	retention time.Duration
}

func NewService(db *database.DB, store *storage.LocalStore, retention time.Duration) *Service {
	return &Service{db: db, store: store, retention: retention}
}

func (s *Service) SaveUpload(ctx context.Context, file multipart.File, header *multipart.FileHeader) (domain.FileRecord, error) {
	storedName, storagePath, size, err := s.store.SaveUploadedFile(file, header.Filename, "uploads")
	if err != nil {
		return domain.FileRecord{}, err
	}

	record := domain.FileRecord{
		ID:           uuid.NewString(),
		OriginalName: header.Filename,
		StoredName:   storedName,
		StoragePath:  storagePath,
		MIMEType:     header.Header.Get("Content-Type"),
		SizeBytes:    size,
		Category:     "input",
		CreatedAt:    time.Now().UTC(),
	}
	expiresAt := time.Now().UTC().Add(s.retention)
	record.ExpiresAt = &expiresAt

	_, err = s.db.Pool.Exec(ctx, `
		insert into tool_files (id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, record.ID, record.OriginalName, record.StoredName, record.StoragePath, record.MIMEType, record.SizeBytes, record.Category, record.ExpiresAt, record.CreatedAt)
	if err != nil {
		return domain.FileRecord{}, err
	}

	return record, nil
}

func (s *Service) SaveGenerated(ctx context.Context, originalName string, mimeType string, content []byte) (domain.FileRecord, error) {
	storedName, storagePath, size, err := s.store.SaveBytes(content, originalName, "outputs")
	if err != nil {
		return domain.FileRecord{}, err
	}

	record := domain.FileRecord{
		ID:           uuid.NewString(),
		OriginalName: originalName,
		StoredName:   storedName,
		StoragePath:  storagePath,
		MIMEType:     mimeType,
		SizeBytes:    size,
		Category:     "output",
		CreatedAt:    time.Now().UTC(),
	}
	expiresAt := time.Now().UTC().Add(s.retention)
	record.ExpiresAt = &expiresAt

	_, err = s.db.Pool.Exec(ctx, `
		insert into tool_files (id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, record.ID, record.OriginalName, record.StoredName, record.StoragePath, record.MIMEType, record.SizeBytes, record.Category, record.ExpiresAt, record.CreatedAt)
	if err != nil {
		return domain.FileRecord{}, err
	}
	return record, nil
}

func (s *Service) GetByID(ctx context.Context, id string) (domain.FileRecord, error) {
	var record domain.FileRecord
	err := s.db.Pool.QueryRow(ctx, `
		select id, original_name, stored_name, storage_path, mime_type, size_bytes, category, expires_at, created_at
		from tool_files where id = $1
	`, id).Scan(
		&record.ID,
		&record.OriginalName,
		&record.StoredName,
		&record.StoragePath,
		&record.MIMEType,
		&record.SizeBytes,
		&record.Category,
		&record.ExpiresAt,
		&record.CreatedAt,
	)
	if err != nil {
		return domain.FileRecord{}, fmt.Errorf("get file by id: %w", err)
	}
	return record, nil
}

func DetectContentType(fileHeader *multipart.FileHeader) string {
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType([]byte(fileHeader.Filename))
	}
	return contentType
}
