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
	var row sqlc.Book
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
			_, currentOrder, err := bookProjectAndOrder(ctx, tx, projectUUID, bookUUID)
			if err != nil {
				return err
			}
			targetOrder, err := bookMoveTargetOrder(ctx, tx, projectUUID, bookUUID, currentOrder, input.AfterBookID)
			if err != nil {
				return err
			}
			if err := shiftBooksForMove(ctx, tx, projectUUID, bookUUID, currentOrder, targetOrder, input.UpdatedAt); err != nil {
				return err
			}
			row, err = scanLibraryBook(tx.QueryRow(ctx, `
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
		} else {
			row, err = sqlc.New(tx).UpdateBook(ctx, sqlc.UpdateBookParams{
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
		}
		if previousTitle == input.Title {
			return nil
		}
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
			SourceView: governance.SourceViewDraft, BookID: bookUUID.String(),
			BookLifecycleGeneration: vector.Book, Title: row.Title,
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
	return s.GetBook(ctx, input.ProjectID, row.ID.String())
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

func bookProjectAndOrder(ctx context.Context, tx pgx.Tx, expectedProjectID, bookID pgtype.UUID) (pgtype.UUID, int, error) {
	var projectID pgtype.UUID
	var order int
	err := tx.QueryRow(ctx, `
SELECT b.project_id, b."order"
FROM books b
JOIN projects p ON p.id = b.project_id
WHERE b.project_id = $1
  AND b.id = $2
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
`, expectedProjectID, bookID).Scan(&projectID, &order)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, 0, library.ErrNotFound
	}
	return projectID, order, err
}

func bookMoveTargetOrder(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, currentOrder int, placement library.OrderPlacement) (int, error) {
	if !placement.Present {
		return currentOrder, nil
	}
	if placement.AfterID == nil {
		return 1, nil
	}
	anchorID, err := storedb.ParseUUID(*placement.AfterID)
	if err != nil {
		return 0, library.ErrNotFound
	}
	if anchorID == bookID {
		return 0, library.ErrValidation
	}
	rawTarget, err := bookInsertionOrder(ctx, tx, projectID, placement)
	if err != nil {
		return 0, err
	}
	if currentOrder < rawTarget {
		return rawTarget - 1, nil
	}
	return rawTarget, nil
}

func shiftBooksForMove(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, currentOrder, targetOrder int, updatedAt time.Time) error {
	if targetOrder < 1 || currentOrder < 1 {
		return library.ErrValidation
	}
	if targetOrder == currentOrder {
		return nil
	}
	if targetOrder < currentOrder {
		_, err := tx.Exec(ctx, `
UPDATE books b
SET "order" = b."order" + 1,
    updated_at = $5
FROM projects p
WHERE b.project_id = $1
  AND p.id = b.project_id
  AND b.id <> $2
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND b."order" >= $3
  AND b."order" < $4
`, projectID, bookID, targetOrder, currentOrder, storedb.Timestamptz(updatedAt))
		return err
	}
	_, err := tx.Exec(ctx, `
UPDATE books b
SET "order" = b."order" - 1,
    updated_at = $5
FROM projects p
WHERE b.project_id = $1
  AND p.id = b.project_id
  AND b.id <> $2
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND b."order" > $3
  AND b."order" <= $4
`, projectID, bookID, currentOrder, targetOrder, storedb.Timestamptz(updatedAt))
	return err
}
