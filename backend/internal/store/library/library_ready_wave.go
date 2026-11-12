package librarystore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// openReadyWaveInTx opens the wave of the preparation of the saga for the import Job of a new book, before the book and
// its text index are written (specs/2026-11-09-first-session.md, FS1.2). A saga with no wave gets one of the Job's
// author, started when the Job was created: the Jobs of one import queue are created together, so the wave of a queue
// starts when the queue did. A closed wave starts again. An open wave is joined — its start and its author stay — while
// anything else of the saga is still imported, indexed or carded; an open wave nothing works on (its books were
// deleted, its cards failed) starts again. The other imports of the Job's own queue wait for their turn and are not
// work on the saga yet. The caller holds the lock of the project row, which CloseWave takes too. The demo saga opens no
// wave: its seed is not a book the author waits for (specs/2026-11-09-demo-saga.md DS3).
func openReadyWaveInTx(ctx context.Context, tx pgx.Tx, projectID, jobID, actorUserID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO saga_ready_waves AS wave (project_id, started_at, started_by_user_id)
		SELECT $1, job.created_at, $3
		FROM jobs AS job
		WHERE job.id = $2 AND job.project_id = $1
		  AND NOT EXISTS (SELECT 1 FROM projects AS saga WHERE saga.id = $1 AND saga.demo)
		ON CONFLICT (project_id) DO UPDATE
		SET started_at = EXCLUDED.started_at,
		    started_by_user_id = EXCLUDED.started_by_user_id,
		    ready_at = NULL, ready_books = NULL, ready_chapters = NULL
		WHERE wave.ready_at IS NOT NULL
		   OR NOT EXISTS (
		     SELECT 1
		     FROM jobs AS other
		     WHERE other.project_id = $1 AND other.id <> $2
		       AND other.kind IN ('import', 'canon_text_index', 'chapter_cards')
		       AND other.status IN ('queued', 'running', 'canceling')
		       AND NOT EXISTS (
		         SELECT 1
		         FROM import_queue_items AS mine
		         JOIN import_queue_items AS sibling ON sibling.queue_id = mine.queue_id
		         WHERE mine.import_job_id = $2 AND sibling.import_job_id = other.id
		       )
		   )
	`, projectID, jobID, actorUserID)
	return err
}
