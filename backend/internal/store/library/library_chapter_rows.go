package librarystore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func chapterInsertionOrder(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, placement library.OrderPlacement) (int, error) {
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
SELECT c."order"
FROM chapters c
JOIN books b ON b.id = c.book_id
JOIN projects p ON p.id = b.project_id
WHERE c.project_id = $1
  AND c.book_id = $2
  AND c.id = $3
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
`, projectID, bookID, anchorID).Scan(&anchorOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, library.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return anchorOrder + 1, nil
}

func shiftChaptersForInsert(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, targetOrder int, updatedAt time.Time) error {
	if targetOrder < 1 {
		return library.ErrValidation
	}
	_, err := tx.Exec(ctx, `
UPDATE chapters c
SET "order" = c."order" + 1,
    updated_at = $4
FROM books b
JOIN projects p ON p.id = b.project_id
WHERE c.project_id = $1
  AND c.book_id = $2
  AND b.id = c.book_id
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND c."order" >= $3
`, projectID, bookID, targetOrder, storedb.Timestamptz(updatedAt))
	return err
}

func createLibraryParagraphs(
	ctx context.Context,
	queries *sqlc.Queries,
	projectID, bookID pgtype.UUID,
	projectGeneration, bookGeneration, chapterGeneration int64,
	paragraphs []library.Paragraph,
) error {
	for _, paragraph := range paragraphs {
		paragraphID, err := storedb.ParseUUID(paragraph.ID)
		if err != nil {
			return err
		}
		chapterID, err := storedb.ParseUUID(paragraph.ChapterID)
		if err != nil {
			return library.ErrNotFound
		}
		if _, err := queries.CreateChapterParagraph(ctx, sqlc.CreateChapterParagraphParams{
			ID:                paragraphID,
			Revision:          paragraph.Revision,
			Variant:           string(paragraph.Variant),
			Kind:              string(paragraph.Kind),
			SortOrder:         int32(paragraph.Order),
			Markdown:          paragraph.Markdown,
			PlainText:         paragraph.PlainText,
			WordCount:         int32(paragraph.WordCount),
			CreatedAt:         storedb.Timestamptz(paragraph.CreatedAt),
			ProjectID:         projectID,
			BookID:            bookID,
			ChapterID:         chapterID,
			ProjectGeneration: projectGeneration,
			BookGeneration:    bookGeneration,
			ChapterGeneration: chapterGeneration,
		}); err != nil {
			return err
		}
	}
	return nil
}

func mapLibraryChapterWriteResult(row sqlc.Chapter, err error) (library.Chapter, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return library.Chapter{}, library.ErrNotFound
	}
	if isUniqueViolation(err) {
		return library.Chapter{}, library.ErrConflict
	}
	if isForeignKeyViolation(err) {
		return library.Chapter{}, library.ErrNotFound
	}
	if isCheckViolation(err) {
		return library.Chapter{}, library.ErrValidation
	}
	if err != nil {
		return library.Chapter{}, err
	}
	return libraryChapterFromSQL(row), nil
}
func scanLibraryChapter(row pgx.Row) (sqlc.Chapter, error) {
	var chapter sqlc.Chapter
	err := row.Scan(
		&chapter.ID,
		&chapter.BookID,
		&chapter.Title,
		&chapter.Order,
		&chapter.Status,
		&chapter.Revision,
		&chapter.PublishedRevision,
		&chapter.PublishedAt,
		&chapter.PublishError,
		&chapter.DeletedAt,
		&chapter.PurgeAfter,
		&chapter.CreatedAt,
		&chapter.UpdatedAt,
	)
	return chapter, err
}
func libraryChapterFromSQL(row sqlc.Chapter) library.Chapter {
	return library.Chapter{
		ID:                row.ID.String(),
		BookID:            row.BookID.String(),
		Title:             row.Title,
		Order:             int(row.Order),
		Status:            library.ChapterStatus(row.Status),
		Revision:          row.Revision,
		PublishedRevision: int64Ptr(row.PublishedRevision),
		PublishedAt:       storedb.OptionalTime(row.PublishedAt),
		PublishError:      textPtr(row.PublishError),
		CreatedAt:         row.CreatedAt.Time,
		UpdatedAt:         row.UpdatedAt.Time,
	}
}

func libraryParagraphFromSQL(row sqlc.ChapterParagraph) library.Paragraph {
	return library.Paragraph{
		ID:        row.ID.String(),
		ChapterID: row.ChapterID.String(),
		Revision:  row.Revision,
		Variant:   library.ParagraphVariant(row.Variant),
		Kind:      library.ParagraphKind(row.Kind),
		Order:     int(row.Order),
		Markdown:  row.Markdown,
		PlainText: row.PlainText,
		WordCount: int(row.WordCount),
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}
}

func int64Ptr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
