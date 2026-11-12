package jobstore

import (
	"context"
	"slices"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// DeletedSourceWork names what a book or chapter deletion fenced: the project at its generation, the text versions
// whose catalog revision the deletion raised and, for a book, the book itself.
type DeletedSourceWork struct {
	ProjectID         string
	ProjectGeneration int64
	Views             []governance.SourceView
	// BookID is set by a book deletion only: the import of that book stops with it.
	BookID string
	At     time.Time
}

// StopDeletedSourceWorkInTx stops, in the deletion's fence transaction, the work the fence would reject anyway
// (specs/2026-11-08-deletion-stops-processing.md UC1 «забор источника, одна транзакция» and «преемник через
// владельца»): the running cards and thread layer Jobs of the versions turn canceling, the import of a deleted book
// stops (queued → canceled, running → canceling), and every version that lost such a Job and still has indexed
// chapters of live books gets its cards Job queued again, which queues the layer when it succeeds. Waiting Jobs build
// the current text when they are claimed and stay; the text index supersedes its own work (R1.1.2). The caller runs
// it before the derived data of the source is scrubbed, so a card written meanwhile is scrubbed with the rest.
func (s *Store) StopDeletedSourceWorkInTx(ctx context.Context, tx pgx.Tx, work DeletedSourceWork) error {
	if tx == nil || work.ProjectGeneration < 1 || work.At.IsZero() {
		return jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(work.ProjectID)
	if err != nil {
		return jobs.ErrValidation
	}
	views := make([]string, 0, len(work.Views))
	for _, view := range work.Views {
		if _, err := governance.PresentSourceView(view); err != nil {
			return jobs.ErrValidation
		}
		views = append(views, string(view))
	}
	rows, err := tx.Query(ctx, `
		UPDATE jobs AS job
		SET status = CASE WHEN job.status = 'queued' THEN 'canceled' ELSE 'canceling' END,
		    cancel_requested_at = $4,
		    claimed_at = CASE WHEN job.status = 'queued' THEN NULL ELSE job.claimed_at END,
		    completed_at = CASE WHEN job.status = 'queued' THEN $4 ELSE job.completed_at END,
		    updated_at = $4
		WHERE job.project_id = $1
		  AND job.can_cancel
		  AND job.cancel_requested_at IS NULL
		  AND job.subject_json->>'projectId' = job.project_id::text
		  AND (
		    (job.kind IN ('chapter_cards', 'saga_layer') AND job.status = 'running'
		      AND job.subject_json->>'sourceView' = ANY($2::text[]))
		    OR (job.kind = 'import' AND job.status IN ('queued', 'running')
		      AND $3::text <> '' AND job.subject_json->>'bookId' = $3::text)
		  )
		  AND ck_artifact_source_owner_is_readable(
		    job.artifact_source_owner_id, job.project_id, 'job', job.id
		  )
		RETURNING job.kind, COALESCE(job.subject_json->>'sourceView', '')
	`, projectID, views, work.BookID, work.At)
	if err != nil {
		return err
	}
	var stopped []governance.SourceView
	for rows.Next() {
		var kind, view string
		if err := rows.Scan(&kind, &view); err != nil {
			rows.Close()
			return err
		}
		if jobs.Kind(kind) != jobs.KindImport && !slices.Contains(stopped, governance.SourceView(view)) {
			stopped = append(stopped, governance.SourceView(view))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, view := range stopped {
		if err := s.queueCardsAfterDeletionInTx(ctx, tx, work, view); err != nil {
			return err
		}
	}
	return nil
}

// queueCardsAfterDeletionInTx queues the cards Job of a version that still has indexed chapters of live books
// (R1.3); a waiting one is reused.
func (s *Store) queueCardsAfterDeletionInTx(ctx context.Context, tx pgx.Tx, work DeletedSourceWork, view governance.SourceView) error {
	var chaptersLeft bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM chapter_text_index_state AS state
		  JOIN chapters AS chapter
		    ON chapter.id = state.chapter_id AND chapter.project_id = state.project_id AND chapter.deleted_at IS NULL
		  JOIN books AS book
		    ON book.id = chapter.book_id AND book.project_id = chapter.project_id AND book.deleted_at IS NULL
		  WHERE state.project_id = $1 AND state.source_view = $2
		)
	`, work.ProjectID, string(view)).Scan(&chaptersLeft); err != nil {
		return err
	}
	if !chaptersLeft {
		return nil
	}
	arm, err := governance.PresentSourceView(view)
	if err != nil {
		return jobs.ErrValidation
	}
	projectRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID:         work.ProjectID,
		ProjectGeneration: governance.LifecycleGeneration(work.ProjectGeneration),
	}, arm, governance.AbsentChatArm())
	if err != nil {
		return jobs.ErrValidation
	}
	_, err = s.UpsertChapterCardsIntent(ctx, tx, jobs.ChapterCardsIntentInput{
		Subject:   jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: work.ProjectID, SourceView: view},
		Source:    jobs.JobSourceManifest{SourceView: arm, Refs: []governance.ArtifactSourceRef{projectRef}},
		CreatedAt: work.At,
	})
	return err
}
