package indexing

import (
	"context"
	"errors"

	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CloseWave closes the open wave of the preparation of a saga (specs/2026-11-09-first-session.md, FS1.3) and reports it
// once: the cards Job that found every chapter carded asks, and the wave closes when the text the site reads — the
// published one — has the index and the card of every chapter, by the counters the status shows, and no new book is
// still being imported. Until then, and for a saga without an open wave, it reports false. A wave whose new books are
// gone — deleted while they were prepared — is forgotten without a word (FS1.5). The lock of the project row orders it
// with the import that opens a wave.
func (store *Store) CloseWave(ctx context.Context, projectID string) (indexingdomain.ReadyWave, bool, error) {
	if store == nil || store.db == nil || store.db.Pool == nil || ctx == nil {
		return indexingdomain.ReadyWave{}, false, ErrInvalid
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return indexingdomain.ReadyWave{}, false, ErrInvalid
	}
	tx, err := store.db.Pool.Begin(ctx)
	if err != nil {
		return indexingdomain.ReadyWave{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	wave, closed, err := closeWaveInTx(ctx, tx, project)
	if err != nil {
		return indexingdomain.ReadyWave{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return indexingdomain.ReadyWave{}, false, err
	}
	return wave, closed, nil
}

func closeWaveInTx(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) (indexingdomain.ReadyWave, bool, error) {
	wave := indexingdomain.ReadyWave{ProjectID: storedb.UUIDString(projectID)}
	err := tx.QueryRow(ctx, `
		SELECT name FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, projectID).Scan(&wave.ProjectName)
	if errors.Is(err, pgx.ErrNoRows) {
		return wave, false, nil
	}
	if err != nil {
		return wave, false, err
	}
	err = tx.QueryRow(ctx, `
		SELECT started_at FROM saga_ready_waves WHERE project_id = $1 AND ready_at IS NULL
	`, projectID).Scan(&wave.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wave, false, nil
	}
	if err != nil {
		return wave, false, err
	}
	// Between the volumes of an import queue every chapter there is has its card, yet the saga is not read: the import
	// Job of the next new book waits for its turn, and its text is in no counter yet. Text that is already in the saga
	// — the other view, «Обновить из файла» — is in the counters below, so its Jobs do not hold the wave.
	if busy, err := newBookImporting(ctx, tx, projectID); err != nil || busy {
		return wave, false, err
	}
	views, err := loadStatusViews(ctx, tx, projectID)
	if err != nil {
		return wave, false, err
	}
	for _, book := range views[governance.SourceViewPublished].books {
		if !book.ready() {
			return wave, false, nil
		}
		if book.chapters > 0 {
			wave.Books++
			wave.Chapters += book.chapters
		}
	}
	// The wave is of the books loaded since it started. Without one in the text of the site there is nothing to tell.
	var fresh bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM book_source_members AS member
		  JOIN books AS book
		    ON book.id = member.book_id AND book.project_id = member.project_id
		   AND book.lifecycle_generation = member.book_lifecycle_generation AND book.deleted_at IS NULL
		  WHERE member.project_id = $1 AND member.source_view = 'published' AND book.created_at >= $2
		)
	`, projectID, wave.StartedAt).Scan(&fresh); err != nil {
		return wave, false, err
	}
	if !fresh || wave.Chapters == 0 {
		_, err := tx.Exec(ctx, `DELETE FROM saga_ready_waves WHERE project_id = $1 AND ready_at IS NULL`, projectID)
		return wave, false, err
	}
	if err := tx.QueryRow(ctx, `
		UPDATE saga_ready_waves
		SET ready_at = date_trunc('microseconds', clock_timestamp()), ready_books = $2, ready_chapters = $3
		WHERE project_id = $1 AND ready_at IS NULL
		RETURNING ready_at
	`, projectID, wave.Books, wave.Chapters).Scan(&wave.ReadyAt); err != nil {
		return wave, false, err
	}
	var recipient pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT `+waveRecipientSQL+`
		FROM saga_ready_waves AS wave
		JOIN projects AS project ON project.id = wave.project_id
		WHERE wave.project_id = $1
	`, projectID).Scan(&recipient); err != nil {
		return wave, false, err
	}
	wave.RecipientUserID = storedb.UUIDString(recipient)
	return wave, true, nil
}

// waveRecipientSQL is whom the wave of a saga is told to (FS1.6, FS2.2): the author who loaded the books, while he is
// in the saga and his account lives, else the owner of the saga. It reads the rows wave and project.
const waveRecipientSQL = `COALESCE(
		  (SELECT member.user_id
		   FROM project_memberships AS member
		   JOIN users AS account ON account.id = member.user_id
		   WHERE member.project_id = wave.project_id AND member.user_id = wave.started_by_user_id
		     AND member.removed_at IS NULL AND account.deleted_at IS NULL),
		  project.owner_user_id)`

// newBookImporting reports whether the import Job of a new book of the project still waits or runs: its chapters are in
// no counter yet.
func newBookImporting(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) (bool, error) {
	var importing bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM jobs
		  WHERE project_id = $1 AND kind = 'import'
		    AND status IN ('queued', 'running', 'canceling')
		    AND COALESCE(subject_json->>'bookId', '') = ''
		)
	`, projectID).Scan(&importing)
	return importing, err
}

// loadStatusWave reads the wave of the saga for the status of one reader (FS4.1, FS5): nil without one. Of an open wave
// it also reads whether a new book is still to be imported and how many cards the saga got over the stretch its pace is
// measured on.
func loadStatusWave(ctx context.Context, tx pgx.Tx, projectID, actorID pgtype.UUID) (*statusWave, error) {
	var wave statusWave
	var readyAt pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT wave.started_at, wave.ready_at, now(), `+waveRecipientSQL+` = $2
		FROM saga_ready_waves AS wave
		JOIN projects AS project ON project.id = wave.project_id
		WHERE wave.project_id = $1
	`, projectID, actorID).Scan(&wave.startedAt, &readyAt, &wave.now, &wave.recipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if readyAt.Valid {
		wave.readyAt = &readyAt.Time
		return &wave, nil
	}
	if wave.importing, err = newBookImporting(ctx, tx, projectID); err != nil {
		return nil, err
	}
	since := wave.now.Add(-indexingdomain.PaceSpan(wave.startedAt, wave.now))
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::integer FROM chapter_cards WHERE project_id = $1 AND created_at >= $2
	`, projectID, since).Scan(&wave.recentCards); err != nil {
		return nil, err
	}
	return &wave, nil
}
