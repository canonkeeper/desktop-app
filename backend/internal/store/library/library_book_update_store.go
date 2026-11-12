package librarystore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) UpdateBook(ctx context.Context, input library.UpdateBookStoreInput) (library.Book, error) {
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	// The id and the title of the book as written.
	var written struct {
		id    pgtype.UUID
		title string
	}
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if input.AfterBookID.Present {
			if err := lockBookOrderProject(ctx, tx, projectUUID); err != nil {
				return err
			}
			if err := ensureNoActiveImportQueueReservation(ctx, tx, projectUUID); err != nil {
				return err
			}
		}
		vector, err := lockCurrentBookSource(ctx, tx, projectUUID, bookUUID)
		if err != nil {
			return err
		}
		var previousTitle string
		if err := tx.QueryRow(ctx, `SELECT title FROM books WHERE project_id = $1 AND id = $2`, projectUUID, bookUUID).Scan(&previousTitle); err != nil {
			return err
		}
		if input.AfterBookID.Present {
			targetOrder, err := placeBooksForMove(ctx, tx, projectUUID, bookUUID, input.AfterBookID, input.UpdatedAt)
			if err != nil {
				return err
			}
			row, err := scanLibraryBook(tx.QueryRow(ctx, `
UPDATE books b
SET title = $1,
    subtitle = $2,
    cover_color = $3,
    "order" = $4,
    revision = b.revision + 1,
    updated_at = $5
FROM projects p
WHERE b.project_id = $6
  AND b.id = $7
  AND p.id = $6
  AND p.lifecycle_generation = $8
  AND b.lifecycle_generation = $9
  AND b.project_id = p.id
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
RETURNING b.id, b.project_id, b.title, b.subtitle, b.cover_color, b."order", b.display_number, b.status, b.revision, b.deleted_at, b.purge_after, b.created_at, b.updated_at
`, input.Title, textValue(input.Subtitle), textValue(input.CoverColor), targetOrder, storedb.Timestamptz(input.UpdatedAt), projectUUID, bookUUID, vector.Project, vector.Book))
			if err != nil {
				return err
			}
			written.id, written.title = row.ID, row.Title
		} else {
			row, err := sqlc.New(tx).UpdateBook(ctx, sqlc.UpdateBookParams{
				Title:             input.Title,
				Subtitle:          textValue(input.Subtitle),
				CoverColor:        textValue(input.CoverColor),
				UpdatedAt:         storedb.Timestamptz(input.UpdatedAt),
				ProjectID:         projectUUID,
				BookID:            bookUUID,
				ProjectGeneration: vector.Project,
				BookGeneration:    vector.Book,
			})
			if err != nil {
				return err
			}
			written.id, written.title = row.ID, row.Title
		}
		if previousTitle == input.Title {
			return nil
		}
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
			SourceView: governance.SourceViewDraft, BookID: bookUUID.String(),
			BookLifecycleGeneration: vector.Book, Title: written.title,
		}); err != nil {
			return err
		}
		revision, err := bumpProjectSourceView(ctx, tx, projectUUID, vector.Project, governance.SourceViewDraft)
		if err != nil {
			return err
		}
		if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, refreshTextIndexIntent); err != nil {
			return err
		}
		return s.runDeletionFailpoint("update_book_after_catalog_mutation")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.Book{}, library.ErrNotFound
	}
	if isCheckViolation(err) {
		return library.Book{}, library.ErrValidation
	}
	if err != nil {
		return library.Book{}, err
	}
	return s.GetBook(ctx, input.ProjectID, written.id.String())
}
func bookInsertionOrder(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, placement library.OrderPlacement) (int, error) {
	if !placement.Present {
		return 0, library.ErrValidation
	}
	if placement.AfterID == nil {
		return 1, nil
	}
	anchorID, err := storedb.ParseUUID(*placement.AfterID)
	if err != nil {
		return 0, library.ErrNotFound
	}
	var anchorOrder int
	err = tx.QueryRow(ctx, `
SELECT b."order"
FROM books b
JOIN projects p ON p.id = b.project_id
WHERE b.id = $1
  AND b.project_id = $2
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
`, anchorID, projectID).Scan(&anchorOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, library.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return anchorOrder + 1, nil
}

func shiftBooksForInsert(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, targetOrder int, updatedAt time.Time) error {
	if targetOrder < 1 {
		return library.ErrValidation
	}
	_, err := tx.Exec(ctx, `
UPDATE books b
SET "order" = b."order" + 1,
    updated_at = $3
FROM projects p
WHERE b.project_id = $1
  AND p.id = b.project_id
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND b."order" >= $2
`, projectID, targetOrder, storedb.Timestamptz(updatedAt))
	return err
}

type lockedBookSourceVector struct {
	Project int64
	Book    int64
}

func lockBookOrderProject(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) error {
	var lockedProjectID pgtype.UUID
	if err := tx.QueryRow(ctx, `
SELECT id
FROM projects
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE
`, projectID).Scan(&lockedProjectID); err != nil {
		return mapLibrarySourceLockError(err)
	}
	return nil
}

func lockCurrentBookSource(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID) (lockedBookSourceVector, error) {
	var vector lockedBookSourceVector
	if err := tx.QueryRow(ctx, `
SELECT lifecycle_generation
FROM projects
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE
`, projectID).Scan(&vector.Project); err != nil {
		return lockedBookSourceVector{}, mapLibrarySourceLockError(err)
	}
	if err := tx.QueryRow(ctx, `
SELECT lifecycle_generation
FROM books
WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL
FOR UPDATE
`, projectID, bookID).Scan(&vector.Book); err != nil {
		return lockedBookSourceVector{}, mapLibrarySourceLockError(err)
	}
	return vector, nil
}

// placeBooksForMove puts the living books of the saga at places 1…n in the new order — the book after its anchor, at
// the start without one — and returns the place of the book; the row of the book itself is written by the caller. The
// keys of the chapters do not depend on the places (specs/2026-11-11-ux7-reorder.md RK1.1), so a move may close the
// places a deleted book left: «Книга N» is the place on the shelf. The same move twice leaves the same shelf.
func placeBooksForMove(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, placement library.OrderPlacement, updatedAt time.Time) (int, error) {
	var anchorID pgtype.UUID
	if placement.AfterID != nil {
		parsed, err := storedb.ParseUUID(*placement.AfterID)
		if err != nil {
			return 0, library.ErrNotFound
		}
		if parsed == bookID {
			return 0, library.ErrValidation
		}
		anchorID = parsed
	}
	rows, err := tx.Query(ctx, `
SELECT b.id
FROM books b
JOIN projects p ON p.id = b.project_id
WHERE b.project_id = $1
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
ORDER BY b."order", b.id
`, projectID)
	if err != nil {
		return 0, err
	}
	shelf, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return 0, err
	}
	placed := make([]pgtype.UUID, 0, len(shelf))
	found := !anchorID.Valid
	if found {
		placed = append(placed, bookID)
	}
	for _, id := range shelf {
		if id == bookID {
			continue
		}
		placed = append(placed, id)
		if anchorID.Valid && id == anchorID {
			placed = append(placed, bookID)
			found = true
		}
	}
	if !found || len(placed) != len(shelf) {
		return 0, library.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
UPDATE books b
SET "order" = placed.place,
    updated_at = $4
FROM unnest($2::uuid[]) WITH ORDINALITY AS placed(id, place)
WHERE b.project_id = $1
  AND b.id = placed.id
  AND b.id <> $3
  AND b.deleted_at IS NULL
  AND b."order" <> placed.place
`, projectID, placed, bookID, storedb.Timestamptz(updatedAt)); err != nil {
		return 0, err
	}
	for i, id := range placed {
		if id == bookID {
			return i + 1, nil
		}
	}
	return 0, library.ErrNotFound
}
