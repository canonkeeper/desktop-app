package librarystore

import (
	"context"
	"errors"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// «Вернуть прежнюю версию» of a book after its update from a file (specs/2026-11-12-ux10-book-revert.md BR2): the
// preview reads the snapshot and the book and tells the plan; the return reads them under the locks of a write, checks
// that the book is the one the preview saw and writes the plan in one transaction. A return is not an import: no Job,
// no upload of the day, no limit of books — only the text it brings back is held against the limits of the tier.

// bookRevert is a book with its snapshot and its plan.
type bookRevert struct {
	projectID, bookID                 pgtype.UUID
	projectGeneration, bookGeneration int64
	bookTitle                         string
	updatedAt, purgeAfter             time.Time
	snapshot                          map[string]snapshotChapterRow
	now                               map[string]domainlibrary.ChapterNow
	plan                              domainlibrary.BookRevertPlan
}

// PreviewBookUpdateRevert tells what a return of the book would do now (BR2.1); it writes nothing.
func (s *Store) PreviewBookUpdateRevert(ctx context.Context, actor domainlibrary.Actor, bookID string) (domainlibrary.BookRevertPreview, error) {
	var preview domainlibrary.BookRevertPreview
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`); err != nil {
			return err
		}
		revert, err := readBookRevertInTx(ctx, tx, actor, bookID, "")
		if err != nil {
			return err
		}
		preview = revert.plan.PreviewOf(revert.bookID.String(), revert.bookTitle, revert.updatedAt, revert.purgeAfter)
		return nil
	})
	return preview, revertError(err)
}

// RevertBookUpdate returns the book to the version before its last update from a file (BR2.2) if the book is still the
// one whose preview had the fingerprint; it reports the project of the book.
func (s *Store) RevertBookUpdate(ctx context.Context, actor domainlibrary.Actor, bookID, fingerprint string) (string, error) {
	var projectID string
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		revert, err := readBookRevertInTx(ctx, tx, actor, bookID, "FOR UPDATE")
		if err != nil {
			return err
		}
		if revert.plan.Fingerprint != fingerprint {
			return domainlibrary.ErrUpdateChanged
		}
		projectID = revert.projectID.String()
		return s.writeBookRevertInTx(ctx, tx, actor, revert)
	})
	return projectID, revertError(err)
}

// readBookRevertInTx reads a book of a live saga the actor edits, its live snapshot and its chapters, and plans the
// return; lock is the row lock of a write, empty for the preview. Not a member, a deleted book or saga — not found; a
// reader — forbidden; the demo saga — its refusal; a book an update from a file writes now — busy; no live snapshot —
// gone.
func readBookRevertInTx(ctx context.Context, tx pgx.Tx, actor domainlibrary.Actor, rawBookID, lock string) (bookRevert, error) {
	var revert bookRevert
	bookID, err := storedb.ParseUUID(rawBookID)
	if err != nil {
		return bookRevert{}, domainlibrary.ErrNotFound
	}
	actorID, err := storedb.ParseUUID(actor.UserID)
	if err != nil {
		return bookRevert{}, domainlibrary.ErrNotFound
	}
	revert.bookID = bookID
	if err := tx.QueryRow(ctx, `SELECT project_id FROM books WHERE id = $1`, bookID).Scan(&revert.projectID); errors.Is(err, pgx.ErrNoRows) {
		return bookRevert{}, domainlibrary.ErrNotFound
	} else if err != nil {
		return bookRevert{}, err
	}
	var demo bool
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation, demo FROM projects WHERE id = $1 AND deleted_at IS NULL `+lock,
		revert.projectID).Scan(&revert.projectGeneration, &demo); errors.Is(err, pgx.ErrNoRows) {
		return bookRevert{}, domainlibrary.ErrNotFound
	} else if err != nil {
		return bookRevert{}, err
	}
	var role string
	shareLock := ""
	if lock != "" {
		shareLock = "FOR SHARE"
	}
	if err := tx.QueryRow(ctx, `
		SELECT role FROM project_memberships WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL `+shareLock,
		revert.projectID, actorID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return bookRevert{}, domainlibrary.ErrNotFound
	} else if err != nil {
		return bookRevert{}, err
	}
	if !projects.CanMutateProject(projects.Role(role)) {
		return bookRevert{}, domainlibrary.ErrForbidden
	}
	// The demo saga takes no text of the author (specs/2026-11-09-demo-saga.md DS3.3).
	if demo {
		return bookRevert{}, quota.ErrDemoSaga
	}
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation, title FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL `+lock,
		bookID, revert.projectID).Scan(&revert.bookGeneration, &revert.bookTitle); errors.Is(err, pgx.ErrNoRows) {
		return bookRevert{}, domainlibrary.ErrNotFound
	} else if err != nil {
		return bookRevert{}, err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM import_job_requests AS request
		  JOIN jobs AS job ON job.id = request.job_id AND job.project_id = request.project_id
		  WHERE request.project_id = $1 AND request.target_book_id = $2 AND request.import_mode = 'update_book'
		    AND job.status IN ('queued', 'running', 'canceling'))
	`, revert.projectID, bookID).Scan(&busy); err != nil {
		return bookRevert{}, err
	}
	if busy {
		return bookRevert{}, domainlibrary.ErrBookBusy
	}
	if err := tx.QueryRow(ctx, `
		SELECT updated_at, purge_after FROM book_update_snapshots
		WHERE book_id = $1 AND project_id = $2 AND clock_timestamp() < purge_after `+lock,
		bookID, revert.projectID).Scan(&revert.updatedAt, &revert.purgeAfter); errors.Is(err, pgx.ErrNoRows) {
		return bookRevert{}, domainlibrary.ErrUpdateGone
	} else if err != nil {
		return bookRevert{}, err
	}
	revert.updatedAt, revert.purgeAfter = revert.updatedAt.UTC(), revert.purgeAfter.UTC()
	rows, err := tx.Query(ctx, `
		SELECT chapter_id, change, revision, generation, COALESCE(previous_revision, 0), COALESCE(previous_title, ''),
		       COALESCE(previous_order, 0), previous_markdown
		FROM book_update_snapshot_chapters WHERE book_id = $1
		ORDER BY chapter_id
	`, bookID)
	if err != nil {
		return bookRevert{}, err
	}
	snapshot, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (snapshotChapterRow, error) {
		var chapter snapshotChapterRow
		var id pgtype.UUID
		var change string
		err := row.Scan(&id, &change, &chapter.chapter.Revision, &chapter.chapter.Generation, &chapter.chapter.PreviousRevision,
			&chapter.chapter.PreviousTitle, &chapter.chapter.PreviousOrder, &chapter.previousMarkdown)
		chapter.chapter.ID, chapter.chapter.Change = id.String(), domainlibrary.UpdateChange(change)
		return chapter, err
	})
	if err != nil {
		return bookRevert{}, err
	}
	chapterLock := ""
	if lock != "" {
		chapterLock = "FOR UPDATE OF chapter"
	}
	rows, err = tx.Query(ctx, `
		SELECT chapter.id, chapter.title, chapter."order", chapter.revision, chapter.lifecycle_generation,
		       chapter.deleted_at IS NULL,
		       chapter.deleted_at IS NOT NULL AND chapter.governance_retention_started_at IS NOT NULL
		         AND chapter.purge_after IS NOT NULL AND chapter.hard_purge_started_at IS NULL
		         AND clock_timestamp() < chapter.purge_after
		FROM chapters AS chapter
		WHERE chapter.project_id = $1 AND chapter.book_id = $2
		  AND (chapter.deleted_at IS NULL
		       OR chapter.id IN (SELECT chapter_id FROM book_update_snapshot_chapters WHERE book_id = $2))
		ORDER BY chapter."order", chapter.id
		`+chapterLock, revert.projectID, bookID)
	if err != nil {
		return bookRevert{}, err
	}
	book, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domainlibrary.ChapterNow, error) {
		var chapter domainlibrary.ChapterNow
		var id pgtype.UUID
		err := row.Scan(&id, &chapter.Title, &chapter.Order, &chapter.Revision, &chapter.Generation, &chapter.Live, &chapter.Restorable)
		chapter.ID = id.String()
		return chapter, err
	})
	if err != nil {
		return bookRevert{}, err
	}
	chapters := make([]domainlibrary.SnapshotChapter, len(snapshot))
	revert.snapshot = make(map[string]snapshotChapterRow, len(snapshot))
	for index, row := range snapshot {
		chapters[index] = row.chapter
		revert.snapshot[row.chapter.ID] = row
	}
	revert.now = make(map[string]domainlibrary.ChapterNow, len(book))
	for _, chapter := range book {
		revert.now[chapter.ID] = chapter
	}
	revert.plan = domainlibrary.PlanBookRevert(revert.updatedAt, chapters, book)
	return revert, nil
}

// writeBookRevertInTx writes the plan of a return under the locks readBookRevertInTx took: the chapters of the file go
// to «Недавно удалённые», the removed ones come back from there, a changed chapter gets the text of its revision before
// the update as a new revision, the others get back their title, place and italic and bold; one publication of the text
// of the book, the snapshot goes, and the text is held against the limits of the tier.
func (s *Store) writeBookRevertInTx(ctx context.Context, tx pgx.Tx, actor domainlibrary.Actor, revert bookRevert) error {
	var at pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&at); err != nil {
		return err
	}
	projectID, bookID := revert.projectID, revert.bookID
	returned := false
	for _, step := range revert.plan.Steps {
		chapterID, err := storedb.ParseUUID(step.Chapter.ID)
		if err != nil {
			return domainlibrary.ErrConflict
		}
		switch step.Action {
		case domainlibrary.RevertRemove:
			var tombstoneID pgtype.UUID
			if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&tombstoneID); err != nil {
				return err
			}
			request := governedChapterDeleteRequest(tombstoneID.String(), actor.UserID, projectID.String(), step.Chapter.ID, at.Time)
			if _, err := s.fenceLockedChapterInTx(ctx, tx, request, projectID, bookID, chapterID, tombstoneID,
				revert.projectGeneration, revert.bookGeneration, step.Now.Generation); err != nil {
				return err
			}
		case domainlibrary.RevertReturn:
			if err := restoreLockedChapterInTx(ctx, tx, projectID, bookID, chapterID, step.Now.Generation); err != nil {
				return err
			}
			returned = true
		}
	}
	if returned {
		if err := s.RestoreProjectSourceCatalog(ctx, tx, projectID, revert.projectGeneration); err != nil {
			return err
		}
	}
	order := make(map[string]int, len(revert.plan.Order))
	for index, id := range revert.plan.Order {
		order[id] = index + 1
	}
	written := map[string]bool{}
	for _, step := range revert.plan.Steps {
		chapterID, err := storedb.ParseUUID(step.Chapter.ID)
		if err != nil {
			return domainlibrary.ErrConflict
		}
		switch step.Action {
		case domainlibrary.RevertText:
			if err := s.revertChapterTextInTx(ctx, tx, revert, step, chapterID, order[step.Chapter.ID], at); err != nil {
				return err
			}
			written[step.Chapter.ID] = true
		case domainlibrary.RevertPlace:
			if err := placeImportedChapter(ctx, tx, projectID, bookID, chapterID, revert.projectGeneration, revert.bookGeneration,
				step.Now.Generation, step.Now.Revision, step.Chapter.PreviousTitle, order[step.Chapter.ID], at); err != nil {
				return err
			}
			if err := restyleRevertedChapter(ctx, tx, chapterID, step.Now.Revision, revert.snapshot[step.Chapter.ID].previousMarkdown, at); err != nil {
				return err
			}
			written[step.Chapter.ID] = true
		}
	}
	// The chapters that keep their words and title stand at their place in the book after the return; a returned
	// chapter takes its place in the text versions again.
	returnedNow := map[string]bool{}
	for _, step := range revert.plan.Steps {
		returnedNow[step.Chapter.ID] = step.Action == domainlibrary.RevertReturn
	}
	for _, id := range revert.plan.Order {
		chapter := revert.now[id]
		if written[id] || (chapter.Order == order[id] && !returnedNow[id]) {
			continue
		}
		chapterID, err := storedb.ParseUUID(id)
		if err != nil {
			return domainlibrary.ErrConflict
		}
		if err := placeImportedChapter(ctx, tx, projectID, bookID, chapterID, revert.projectGeneration, revert.bookGeneration,
			chapter.Generation, chapter.Revision, chapter.Title, order[id], at); err != nil {
			return err
		}
	}
	if err := s.publishBookTextInTx(ctx, tx, projectID, bookID, revert.projectGeneration, revert.bookGeneration, revert.bookTitle); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM book_update_snapshots WHERE book_id = $1`, bookID); err != nil {
		return err
	}
	// The chapters and the text the return brings back are held against the limits of the tier (BR2.3).
	return s.quota.CheckOwnerSourcesInTx(ctx, tx, projectID.String(), quota.MeasureChapters|quota.MeasureCharacters)
}

// revertChapterTextInTx writes the words of the revision of a chapter before the update, with their italic and bold, as
// its next revision in both text versions, under the title and at the place it had: the path of
// ReplaceChapterTextInTx. A chapter whose update changed only signs keeps its card, as the update kept it (R3.2).
func (s *Store) revertChapterTextInTx(
	ctx context.Context, tx pgx.Tx, revert bookRevert, step domainlibrary.RevertStep, chapterID pgtype.UUID, order int, at pgtype.Timestamptz,
) error {
	previous, err := chapterContentsInTx(ctx, tx, chapterID, step.Chapter.PreviousRevision)
	if err != nil {
		return err
	}
	current, err := chapterContentsInTx(ctx, tx, chapterID, step.Now.Revision)
	if err != nil {
		return err
	}
	if len(previous.contents) == 0 {
		return domainlibrary.ErrConflict
	}
	written, err := writeImportedChapterText(ctx, tx, revert.projectID, revert.bookID, chapterID, revert.projectGeneration,
		revert.bookGeneration, step.Now.Generation, step.Now.Revision, step.Now.Revision+1, step.Chapter.PreviousTitle, order,
		previous.contents, at)
	if err != nil {
		return err
	}
	if step.Chapter.Change != domainlibrary.UpdatePunctuation {
		return nil
	}
	current.id = chapterID
	return carryChapterCard(ctx, tx, revert.projectID, current, written)
}

// chapterContentsInTx reads the draft rows of a revision of a chapter, with their ids, in their order.
func chapterContentsInTx(ctx context.Context, tx pgx.Tx, chapterID pgtype.UUID, revision int64) (importedBookChapter, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, kind, "order", markdown, plain_text FROM chapter_paragraphs
		WHERE chapter_id = $1 AND revision = $2 AND variant = 'draft'
		ORDER BY "order", id
	`, chapterID, revision)
	if err != nil {
		return importedBookChapter{}, err
	}
	defer rows.Close()
	chapter := importedBookChapter{id: chapterID, revision: revision}
	for rows.Next() {
		var id pgtype.UUID
		var content importedParagraphContent
		if err := rows.Scan(&id, &content.kind, &content.order, &content.markdown, &content.plainText); err != nil {
			return importedBookChapter{}, err
		}
		chapter.contents = append(chapter.contents, content)
		chapter.paragraphIDs = append(chapter.paragraphIDs, id.String())
	}
	return chapter, rows.Err()
}

// restyleRevertedChapter gives the rows of a chapter whose words stayed the italic and bold they had before the update.
func restyleRevertedChapter(ctx context.Context, tx pgx.Tx, chapterID pgtype.UUID, revision int64, markdown []string, at pgtype.Timestamptz) error {
	if markdown == nil {
		return nil
	}
	current, err := chapterContentsInTx(ctx, tx, chapterID, revision)
	if err != nil {
		return err
	}
	if len(current.contents) != len(markdown) {
		return domainlibrary.ErrConflict
	}
	for index, content := range current.contents {
		if content.markdown == markdown[index] {
			continue
		}
		if err := setParagraphMarkdown(ctx, tx, chapterID, revision, content.order, markdown[index], at); err != nil {
			return err
		}
	}
	return nil
}

// revertError names the outcome of a return in the words of the domain; a refusal of a limit stays as it is.
func revertError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, library.ErrNotFound):
		return domainlibrary.ErrNotFound
	case errors.Is(err, library.ErrForbidden):
		return domainlibrary.ErrForbidden
	case errors.Is(err, library.ErrConflict), isUniqueViolation(err):
		return domainlibrary.ErrConflict
	case errors.Is(err, library.ErrValidation), isCheckViolation(err):
		return domainlibrary.ErrValidation
	default:
		return err
	}
}
