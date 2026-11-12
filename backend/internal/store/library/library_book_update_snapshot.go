package librarystore

import (
	"context"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The snapshot of the last update of a book from a file (specs/2026-11-12-ux10-book-revert.md BR1): written by the
// update in its transaction, read by the books of the shelf and by the return, removed by the return, by a new update,
// by the fence of its book or saga and after its term.

// snapshotChapterRow is a chapter of the snapshot as the update writes it; previousMarkdown is the italic and bold of the
// rows of a chapter whose words stayed, by their order, when the update changed them.
type snapshotChapterRow struct {
	chapter          domainlibrary.SnapshotChapter
	previousMarkdown []string
}

// snapshotOfKept is the row of a chapter of the book the update kept under its id.
func snapshotOfKept(chapter importedBookChapter, change domainlibrary.UpdateChange, revision int64) snapshotChapterRow {
	return snapshotChapterRow{chapter: domainlibrary.SnapshotChapter{
		ID: chapter.id.String(), Change: change, Revision: revision, Generation: chapter.generation,
		PreviousRevision: chapter.revision, PreviousTitle: chapter.title, PreviousOrder: chapter.order,
	}}
}

// writeBookUpdateSnapshotInTx replaces the snapshot of the book by the one of this update: one live snapshot a book
// (BR1.2), for the 7 days a chapter it removed waits in «Недавно удалённые».
func writeBookUpdateSnapshotInTx(
	ctx context.Context, tx pgx.Tx, projectID, bookID, jobID pgtype.UUID, at pgtype.Timestamptz, chapters []snapshotChapterRow,
) error {
	if _, err := tx.Exec(ctx, `DELETE FROM book_update_snapshots WHERE book_id = $1`, bookID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO book_update_snapshots (book_id, project_id, job_id, updated_at, purge_after)
		VALUES ($1, $2, $3, $4, $5)
	`, bookID, projectID, jobID, at, at.Time.Add(deletion.SourceRestoreWindow)); err != nil {
		return err
	}
	for _, row := range chapters {
		chapter := row.chapter
		var previousRevision, previousOrder any
		var previousTitle any
		if chapter.Change != domainlibrary.UpdateAdded {
			previousRevision, previousTitle, previousOrder = chapter.PreviousRevision, chapter.PreviousTitle, chapter.PreviousOrder
		}
		var markdown any
		if row.previousMarkdown != nil {
			markdown = row.previousMarkdown
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO book_update_snapshot_chapters (
			  book_id, chapter_id, change, revision, generation, previous_revision, previous_title, previous_order, previous_markdown
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, bookID, chapter.ID, string(chapter.Change), chapter.Revision, chapter.Generation,
			previousRevision, previousTitle, previousOrder, markdown); err != nil {
			return err
		}
	}
	return nil
}

type bookUpdatesQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// updateBooks gives the living books of a page the last update of each from a file while its snapshot lives (BR1.3).
func updateBooks(ctx context.Context, queryer bookUpdatesQueryer, projectID pgtype.UUID, books []library.Book) error {
	if len(books) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(books))
	for _, book := range books {
		id, err := storedb.ParseUUID(book.ID)
		if err != nil {
			return library.ErrNotFound
		}
		ids = append(ids, id)
	}
	rows, err := queryer.Query(ctx, `
		SELECT snapshot.book_id, snapshot.updated_at, snapshot.purge_after, snapshot.job_id,
		       count(*) FILTER (WHERE chapter.change IN ('words', 'punctuation', 'placed'))::integer,
		       count(*) FILTER (WHERE chapter.change = 'added')::integer,
		       count(*) FILTER (WHERE chapter.change = 'removed')::integer
		FROM book_update_snapshots AS snapshot
		LEFT JOIN book_update_snapshot_chapters AS chapter ON chapter.book_id = snapshot.book_id
		WHERE snapshot.project_id = $1 AND snapshot.book_id = ANY($2::uuid[]) AND clock_timestamp() < snapshot.purge_after
		GROUP BY snapshot.book_id
	`, projectID, ids)
	if err != nil {
		return err
	}
	updates := map[string]*library.BookUpdate{}
	for rows.Next() {
		var bookID, jobID pgtype.UUID
		var at, until time.Time
		var update library.BookUpdate
		if err := rows.Scan(&bookID, &at, &until, &jobID, &update.Changed, &update.Added, &update.Removed); err != nil {
			rows.Close()
			return err
		}
		update.At, update.RevertUntil = at.UTC(), until.UTC()
		if jobID.Valid {
			update.JobID = jobID.String()
		}
		updates[bookID.String()] = &update
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range books {
		books[index].Update = updates[books[index].ID]
	}
	return nil
}
