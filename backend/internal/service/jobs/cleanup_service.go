package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *Service) Expire(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		select id, storage_path from tool_files
		where expires_at is not null and expires_at < $1
	`, time.Now().UTC())
	if err != nil {
		return err
	}
	defer rows.Close()

	type expiredFile struct {
		id   string
		path string
	}

	var expired []expiredFile
	for rows.Next() {
		var item expiredFile
		if err := rows.Scan(&item.id, &item.path); err != nil {
			return fmt.Errorf("scan expired file: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate expired files: %w", err)
	}

	var cleanupErrs []error
	for _, item := range expired {
		if err := s.store.Delete(item.path); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete expired file %s: %w", item.path, err))
			continue
		}
		if _, err := s.db.Pool.Exec(ctx, `delete from tool_files where id = $1`, item.id); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete expired file metadata %s: %w", item.id, err))
		}
	}

	if _, err := s.db.Pool.Exec(ctx, `
		update tool_jobs set status = 'expired'
		where expires_at is not null and expires_at < $1 and status in ('pending','running','success')
	`, time.Now().UTC()); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("expire old jobs: %w", err))
	}

	return errors.Join(cleanupErrs...)
}
