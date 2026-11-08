package librarystore

import (
	"context"

	"canonkeeper/backend/internal/library"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func ensureNoActiveImportQueueReservation(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) error {
	var reserved bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM import_queues AS queue
			JOIN import_queue_items AS item
			  ON item.queue_id = queue.id AND item.project_id = queue.project_id
			WHERE queue.project_id = $1
			  AND queue.status = 'running'
			  AND item.import_job_id IS NOT NULL
		)
	`, projectID).Scan(&reserved); err != nil {
		return err
	}
	if reserved {
		return library.ErrImportExpectedOrderConflict
	}
	return nil
}
