package librarystore

import (
	"context"

	"canonkeeper/backend/internal/library"
)

func (s *Store) ExpireStaleImportQueues(ctx context.Context) (int64, error) {
	if s == nil || s.Pool == nil {
		return 0, library.ErrValidation
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE import_queues
		SET status='expired', version=version+1,
		    updated_at=clock_timestamp(), completed_at=clock_timestamp()
		WHERE status IN ('uploading', 'draft')
		  AND created_at <= clock_timestamp() - interval '24 hours'
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
