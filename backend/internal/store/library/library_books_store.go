package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type bookOrderQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) NextBookOrder(ctx context.Context, projectID string) (int, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return 0, library.ErrNotFound
	}
	return validatedNextBookOrder(ctx, s.Pool, projectUUID)
}

// validatedNextBookOrder is the stored order of a new book: after the live books of the shelf
// (specs/2026-11-11-ux7-upload.md UP2.3). The place a deleted book keeps for its return is not filled: the new book
// stands last, as the author sees it, and a book returned to a taken place moves the others on.
func validatedNextBookOrder(ctx context.Context, queryer bookOrderQueryer, projectID pgtype.UUID) (int, error) {
	var count, distinctCount, nextOrder int64
	if err := queryer.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT "order"), COALESCE(MAX("order"), 0) + 1
		FROM books
		WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID).Scan(&count, &distinctCount, &nextOrder); err != nil {
		return 0, err
	}
	if count != distinctCount {
		return 0, library.ErrImportSequenceConflict
	}
	return int(nextOrder), nil
}

func (s *Store) CreateBook(ctx context.Context, input library.CreateBookStoreInput) (library.Book, error) {
	bookID, err := storedb.ParseUUID(input.ID)
	if err != nil {
		return library.Book{}, err
	}
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	create := func(tx pgx.Tx, order int) (sqlc.CreateBookRow, error) {
		row, err := sqlc.New(tx).CreateBook(ctx, sqlc.CreateBookParams{
			ID:            bookID,
			Title:         input.Title,
			Subtitle:      textValue(input.Subtitle),
			CoverColor:    textValue(input.CoverColor),
			SortOrder:     int32(order),
			DisplayNumber: input.DisplayNumber,
			CreatedAt:     storedb.Timestamptz(input.CreatedAt),
			ProjectID:     projectUUID,
		})
		if err != nil {
			return sqlc.CreateBookRow{}, err
		}
		return row, nil
	}
	var row sqlc.CreateBookRow
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockBookOrderProject(ctx, tx, projectUUID); err != nil {
			return err
		}
		if err := ensureNoActiveImportQueueReservation(ctx, tx, projectUUID); err != nil {
			return err
		}
		if err := s.quota.CheckNewBooksInTx(ctx, tx, projectUUID.String(), 1, false); err != nil {
			return err
		}
		if input.AfterBookID.Present {
			order, err := bookInsertionOrder(ctx, tx, projectUUID, input.AfterBookID)
			if err != nil {
				return err
			}
			if err := shiftBooksForInsert(ctx, tx, projectUUID, order, input.CreatedAt); err != nil {
				return err
			}
			row, err = create(tx, order)
		} else {
			nextOrder, err := validatedNextBookOrder(ctx, tx, projectUUID)
			if err != nil {
				return err
			}
			if input.Order != nextOrder {
				return library.ErrConflict
			}
			row, err = create(tx, input.Order)
		}
		if err != nil {
			return err
		}
		vector, err := lockCurrentBookSource(ctx, tx, projectUUID, bookID)
		if err != nil {
			return err
		}
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
			SourceView: governance.SourceViewDraft, BookID: bookID.String(),
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
		return s.runDeletionFailpoint("create_book_after_catalog_mutation")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.Book{}, library.ErrNotFound
	}
	if isUniqueViolation(err) {
		return library.Book{}, library.ErrConflict
	}
	if isCheckViolation(err) {
		return library.Book{}, library.ErrValidation
	}
	if err != nil {
		return library.Book{}, err
	}
	// A new book has no update from a file to tell.
	return s.getBook(ctx, input.ProjectID, row.ID.String(), false)
}

func (s *Store) ListBooks(ctx context.Context, input library.ListBooksStoreInput) ([]library.Book, error) {
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	state := input.State
	if state == "" {
		state = library.SourceListActive
	}
	after, err := listPositionArgs(input.After)
	if err != nil {
		return nil, err
	}
	if state == library.SourceListDeleted {
		return s.listDeletedBooks(ctx, input, projectUUID, after)
	}
	if state != library.SourceListActive {
		return nil, library.ErrValidation
	}
	rows, err := sqlc.New(s.Pool).ListBookSummariesByProject(ctx, sqlc.ListBookSummariesByProjectParams{
		ProjectID:   projectUUID,
		HasCursor:   after.has,
		CursorOrder: after.order,
		CursorID:    after.id,
		LimitCount:  int32(input.Limit),
	})
	if err != nil {
		return nil, err
	}
	books := make([]library.Book, 0, len(rows))
	for _, row := range rows {
		books = append(books, libraryBookFromListSummarySQL(row))
	}
	if err := placeBooks(ctx, s.Pool, projectUUID, books); err != nil {
		return nil, err
	}
	if err := updateBooks(ctx, s.Pool, projectUUID, books); err != nil {
		return nil, err
	}
	return books, nil
}

func (s *Store) listDeletedBooks(ctx context.Context, input library.ListBooksStoreInput, projectID pgtype.UUID, after listPositionArgsValue) ([]library.Book, error) {
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	var books []library.Book
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT
			  book.id, book.project_id, book.title, book.subtitle, book.cover_color,
			  book."order", book.display_number, book.status, book.revision,
			  book.created_at, book.updated_at,
			  COALESCE(book_stats.chapter_count, 0)::integer,
			  COALESCE(book_stats.word_count, 0)::integer,
			  book.deleted_at, book.purge_after
			FROM books AS book
			JOIN projects AS project ON project.id = book.project_id
			JOIN project_memberships AS membership ON membership.project_id = project.id
			LEFT JOIN LATERAL (
			  SELECT COUNT(chapter.id)::integer AS chapter_count,
			         COALESCE(SUM(chapter_words.word_count), 0)::integer AS word_count
			  FROM chapters AS chapter
			  LEFT JOIN LATERAL (
			    SELECT COALESCE(SUM(paragraph.word_count), 0)::integer AS word_count
			    FROM chapter_paragraphs AS paragraph
			    WHERE paragraph.chapter_id = chapter.id
			      AND paragraph.revision = chapter.revision
			      AND paragraph.variant = 'draft'
			  ) AS chapter_words ON true
			  WHERE chapter.book_id = book.id AND chapter.deleted_at IS NULL
			) AS book_stats ON true
			WHERE book.project_id = $2
			  AND membership.user_id = $1
			  AND membership.removed_at IS NULL
			  AND membership.role IN ('editor', 'admin', 'owner')
			  AND project.deleted_at IS NULL
			  AND book.deleted_at IS NOT NULL
			  AND book.governance_retention_started_at IS NOT NULL
			  AND book.purge_after IS NOT NULL
			  AND book.hard_purge_started_at IS NULL
			  AND clock_timestamp() < book.purge_after
			  AND (NOT $4::boolean OR (book.deleted_at, book.id) < ($5::timestamptz, $6::uuid))
			ORDER BY book.deleted_at DESC, book.id DESC
			LIMIT $3
		`, actorUserID, projectID, input.Limit, after.has, after.deletedAt, after.id)
		if err != nil {
			return err
		}
		defer rows.Close()
		books = make([]library.Book, 0)
		for rows.Next() {
			var id, rowProjectID pgtype.UUID
			var title, displayNumber, status string
			var subtitle, coverColor pgtype.Text
			var order, chapterCount, wordCount int32
			var revision int64
			var createdAt, updatedAt, deletedAt, restoreUntil pgtype.Timestamptz
			if err := rows.Scan(
				&id, &rowProjectID, &title, &subtitle, &coverColor,
				&order, &displayNumber, &status, &revision,
				&createdAt, &updatedAt, &chapterCount, &wordCount,
				&deletedAt, &restoreUntil,
			); err != nil {
				return err
			}
			book := libraryBookFromSummaryColumns(
				id, rowProjectID, title, subtitle, coverColor, order, displayNumber,
				status, revision, createdAt, updatedAt, 0, chapterCount, wordCount,
			)
			deletedClock := deletedAt.Time
			restoreClock := restoreUntil.Time
			book.DeletedAt = &deletedClock
			book.RestoreUntil = &restoreClock
			books = append(books, book)
		}
		return rows.Err()
	})
	return books, err
}

// GetBook reads a live book of the project with its place on the shelf and its last update from a file.
func (s *Store) GetBook(ctx context.Context, projectID, bookID string) (library.Book, error) {
	return s.getBook(ctx, projectID, bookID, true)
}

func (s *Store) getBook(ctx context.Context, projectID, bookID string, withUpdate bool) (library.Book, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	row, err := sqlc.New(s.Pool).GetBookSummaryByID(ctx, sqlc.GetBookSummaryByIDParams{ProjectID: projectUUID, BookID: bookUUID})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.Book{}, library.ErrNotFound
	}
	if err != nil {
		return library.Book{}, err
	}
	book := []library.Book{libraryBookFromGetSummarySQL(row)}
	if err := placeBooks(ctx, s.Pool, projectUUID, book); err != nil {
		return library.Book{}, err
	}
	if withUpdate {
		if err := updateBooks(ctx, s.Pool, projectUUID, book); err != nil {
			return library.Book{}, err
		}
	}
	return book[0], nil
}
