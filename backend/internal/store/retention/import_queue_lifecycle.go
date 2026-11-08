package retentionstore

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ReconcileImportUploads preserves upload bytes only according to authoritative
// Import Queue state. It never derives queue or Job terminal outcomes.
func (store *Store) ReconcileImportUploads(ctx context.Context) error {
	return store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE import_uploads AS upload
			SET content = NULL
			WHERE upload.content IS NOT NULL
			  AND EXISTS (
			    SELECT 1
			    FROM import_queue_items AS item
			    JOIN import_queues AS queue
			      ON queue.id = item.queue_id AND queue.project_id = item.project_id
			    LEFT JOIN jobs AS job
			      ON job.id = item.import_job_id AND job.project_id = item.project_id
			    WHERE item.project_id = upload.project_id
			      AND item.storage_key = upload.storage_key
			      AND queue.status IN ('succeeded', 'canceled', 'expired')
			      AND NOT COALESCE(job.status IN ('running', 'canceling'), false)
			  )
		`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE import_uploads AS upload
			SET expires_at = clock_timestamp() + interval '24 hours'
			WHERE upload.content IS NOT NULL
			  AND EXISTS (
			    SELECT 1
			    FROM import_queue_items AS item
			    JOIN import_queues AS queue
			      ON queue.id = item.queue_id AND queue.project_id = item.project_id
			    WHERE item.project_id = upload.project_id
			      AND item.storage_key = upload.storage_key
			      AND queue.status IN ('running', 'failed')
			  )
		`)
		return err
	})
}
