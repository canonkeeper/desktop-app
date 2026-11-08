package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/library"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ReconcileTerminalImportQueueJobs(ctx context.Context) (int64, error) {
	if s == nil || s.Pool == nil {
		return 0, library.ErrValidation
	}
	var reconciled int64
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		var projectID, queueID pgtype.UUID
		err := tx.QueryRow(ctx, `
			SELECT queue.project_id, queue.id
			FROM import_queues AS queue
			WHERE queue.status IN ('failed', 'canceled', 'expired')
			  AND EXISTS (
			    SELECT 1
			    FROM import_queue_items AS item
			    JOIN jobs AS job
			      ON job.id=item.import_job_id AND job.project_id=item.project_id
			    WHERE item.queue_id=queue.id AND item.project_id=queue.project_id
			      AND job.kind='import' AND job.status='queued'
			  )
			ORDER BY queue.updated_at, queue.id
			LIMIT 1
			FOR UPDATE OF queue
		`).Scan(&projectID, &queueID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		reconciled, err = jobstore.FinalizeQueuedImportQueueSuffixInTx(ctx, tx, jobstore.FinalizeQueuedImportQueueSuffixInTxInput{
			ProjectID: projectID, QueueID: queueID, AfterPosition: 0,
		})
		return err
	})
	return reconciled, err
}
