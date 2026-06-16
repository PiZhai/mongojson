package jobs

import (
	"context"
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
			return err
		}
		expired = append(expired, item)
	}

	for _, item := range expired {
		_ = s.store.Delete(item.path)
		_, _ = s.db.Pool.Exec(ctx, `delete from tool_files where id = $1`, item.id)
	}

	_, _ = s.db.Pool.Exec(ctx, `
		update tool_jobs set status = 'expired'
		where expires_at is not null and expires_at < $1 and status in ('pending','running','success')
	`, time.Now().UTC())
	return nil
}
