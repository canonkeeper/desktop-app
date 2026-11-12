// Package trash reads «Недавно удалённые» from PostgreSQL (specs/2026-11-11-ux7-trash.md TB1.1): the sagas, books and
// chapters an author may return, by the same rules their returns check.
package trash

import (
	"context"
	"strconv"

	"canonkeeper/backend/internal/domain/shelf"
	"canonkeeper/backend/internal/domain/trash"
	service "canonkeeper/backend/internal/services/trash"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

var _ service.Sources = (*Repository)(nil)

type Repository struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Repository {
	if db == nil {
		panic("trash repository: database is required")
	}
	return &Repository{db: db}
}

// restorable is the window a return of a source still opens: deleted under the governed window, before its term and
// before its hard purge; the alias names the row of projects, books or chapters.
func restorable(alias string) string {
	return alias + `.deleted_at IS NOT NULL AND ` + alias + `.governance_retention_started_at IS NOT NULL AND ` +
		alias + `.purge_after IS NOT NULL AND ` + alias + `.hard_purge_started_at IS NULL AND clock_timestamp() < ` + alias + `.purge_after`
}

// ListDeletedSources reads the groups in one snapshot: the deleted sagas the author owns or administers
// (restoreProject), the deleted books of his live sagas and the deleted chapters of their live books while he is an
// editor and above (restoreBook, restoreChapter), and the books of those sagas whose update can be returned
// (revertBookUpdate).
func (repository *Repository) ListDeletedSources(ctx context.Context, actorUserID string, limit int) (trash.Sources, error) {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return trash.Sources{}, service.ErrValidation
	}
	sources := trash.Sources{
		Sagas: trash.Group[trash.Saga]{Items: []trash.Saga{}}, Books: trash.Group[trash.Book]{Items: []trash.Book{}},
		Chapters: trash.Group[trash.Chapter]{Items: []trash.Chapter{}}, BookUpdates: trash.Group[trash.BookUpdate]{Items: []trash.BookUpdate{}},
	}
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT project.id::text, project.name,
			       (SELECT count(*) FROM books AS book WHERE book.project_id = project.id AND book.deleted_at IS NULL)::integer,
			       project.deleted_at, project.purge_after, count(*) OVER ()::integer
			FROM projects AS project
			JOIN project_memberships AS membership ON membership.project_id = project.id
			WHERE membership.user_id = $1 AND membership.removed_at IS NULL AND membership.role IN ('admin', 'owner')
			  AND `+restorable("project")+`
			ORDER BY project.deleted_at DESC, project.id DESC
			LIMIT $2
		`, actor, limit)
		if err != nil {
			return err
		}
		if err := collect(rows, &sources.Sagas, func(row pgx.Rows, saga *trash.Saga, total *int) error {
			return row.Scan(&saga.ID, &saga.Title, &saga.Books, &saga.DeletedAt, &saga.RestoreUntil, total)
		}); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `
			SELECT book.id::text, project.id::text, project.name, book.title,
			       book.deleted_at, book.purge_after, count(*) OVER ()::integer
			FROM books AS book
			JOIN projects AS project ON project.id = book.project_id AND project.deleted_at IS NULL
			JOIN project_memberships AS membership ON membership.project_id = project.id
			WHERE membership.user_id = $1 AND membership.removed_at IS NULL AND membership.role IN ('editor', 'admin', 'owner')
			  AND `+restorable("book")+`
			ORDER BY book.deleted_at DESC, book.id DESC
			LIMIT $2
		`, actor, limit)
		if err != nil {
			return err
		}
		if err := collect(rows, &sources.Books, func(row pgx.Rows, book *trash.Book, total *int) error {
			return row.Scan(&book.ID, &book.ProjectID, &book.SagaTitle, &book.Title, &book.DeletedAt, &book.RestoreUntil, total)
		}); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `
			SELECT chapter.id::text, project.id::text, project.name, book.id::text, book.title,
			       chapter.title, chapter."order", chapter.deleted_at, chapter.purge_after, count(*) OVER ()::integer
			FROM chapters AS chapter
			JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id AND book.deleted_at IS NULL
			JOIN projects AS project ON project.id = book.project_id AND project.deleted_at IS NULL
			JOIN project_memberships AS membership ON membership.project_id = project.id
			WHERE membership.user_id = $1 AND membership.removed_at IS NULL AND membership.role IN ('editor', 'admin', 'owner')
			  AND `+restorable("chapter")+`
			ORDER BY chapter.deleted_at DESC, chapter.id DESC
			LIMIT $2
		`, actor, limit)
		if err != nil {
			return err
		}
		if err := collect(rows, &sources.Chapters, func(row pgx.Rows, chapter *trash.Chapter, total *int) error {
			return row.Scan(&chapter.ID, &chapter.ProjectID, &chapter.SagaTitle, &chapter.BookID, &chapter.BookTitle,
				&chapter.Title, &chapter.Order, &chapter.DeletedAt, &chapter.RestoreUntil, total)
		}); err != nil {
			return err
		}
		// The updates of books from a file whose previous version can still be returned, by the rule of revertBookUpdate
		// (specs/2026-11-12-ux10-book-revert.md BR3.3): a live book of a live saga the author edits, its snapshot in its term.
		rows, err = tx.Query(ctx, `
			SELECT book.id::text, project.id::text, project.name, book.title,
			       snapshot.updated_at, snapshot.purge_after, count(*) OVER ()::integer
			FROM book_update_snapshots AS snapshot
			JOIN books AS book ON book.id = snapshot.book_id AND book.project_id = snapshot.project_id AND book.deleted_at IS NULL
			JOIN projects AS project ON project.id = book.project_id AND project.deleted_at IS NULL
			JOIN project_memberships AS membership ON membership.project_id = project.id
			WHERE membership.user_id = $1 AND membership.removed_at IS NULL AND membership.role IN ('editor', 'admin', 'owner')
			  AND NOT project.demo AND clock_timestamp() < snapshot.purge_after
			ORDER BY snapshot.updated_at DESC, book.id DESC
			LIMIT $2
		`, actor, limit)
		if err != nil {
			return err
		}
		if err := collect(rows, &sources.BookUpdates, func(row pgx.Rows, update *trash.BookUpdate, total *int) error {
			return row.Scan(&update.BookID, &update.ProjectID, &update.SagaTitle, &update.Title, &update.UpdatedAt, &update.RevertUntil, total)
		}); err != nil {
			return err
		}
		return placeChapterBooks(ctx, tx, sources.Chapters.Items)
	})
	if err != nil {
		return trash.Sources{}, err
	}
	return sources, nil
}

// collect reads the rows of a group with the count of all of them each row carries.
func collect[T any](rows pgx.Rows, group *trash.Group[T], scan func(pgx.Rows, *T, *int) error) error {
	defer rows.Close()
	for rows.Next() {
		var item T
		if err := scan(rows, &item, &group.Total); err != nil {
			return err
		}
		group.Items = append(group.Items, item)
	}
	return rows.Err()
}

// placeChapterBooks names the living book of each deleted chapter by its place on the shelf of its saga, «Книга N» — the
// one rule of the number (shelf.Places, specs/2026-11-11-ux7-reorder.md RK1.6).
func placeChapterBooks(ctx context.Context, tx pgx.Tx, chapters []trash.Chapter) error {
	if len(chapters) == 0 {
		return nil
	}
	projects := make([]string, 0, len(chapters))
	for _, chapter := range chapters {
		projects = append(projects, chapter.ProjectID)
	}
	rows, err := tx.Query(ctx, `
		SELECT project_id::text, id::text, "order" FROM books WHERE project_id = ANY($1::uuid[]) AND deleted_at IS NULL
	`, projects)
	if err != nil {
		return err
	}
	shelves := map[string][]shelf.Book{}
	for rows.Next() {
		var project string
		var book shelf.Book
		if err := rows.Scan(&project, &book.ID, &book.Order); err != nil {
			rows.Close()
			return err
		}
		shelves[project] = append(shelves[project], book)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	places := map[string]map[string]int{}
	for index := range chapters {
		project := chapters[index].ProjectID
		if places[project] == nil {
			places[project] = shelf.Places(shelves[project])
		}
		chapters[index].BookDisplayNumber = strconv.Itoa(places[project][chapters[index].BookID])
	}
	return nil
}
