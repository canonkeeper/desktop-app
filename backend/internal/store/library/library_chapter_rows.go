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

// chapterParagraphs are paragraphs a write gives one chapter of its lifecycle generation.
type chapterParagraphs struct {
	generation int64
	paragraphs []library.Paragraph
}

// paragraphStatementBytes is how much text one statement of paragraphs carries at most: a book of ten million
// characters goes in a few statements, not in one of tens of megabytes.
const paragraphStatementBytes = 4 << 20

// createLibraryParagraphs writes paragraphs of chapters of one live book by batches, a statement for up to
// paragraphStatementBytes of their text, not a statement per paragraph (specs/2026-11-11-import-speed.md IS1): each
// paragraph only into a live chapter of its generation in the live book and project of theirs, and a paragraph that
// finds none fails the write as pgx.ErrNoRows, as the write by rows did.
func createLibraryParagraphs(
	ctx context.Context,
	tx pgx.Tx,
	projectID, bookID pgtype.UUID,
	projectGeneration, bookGeneration int64,
	chapters ...chapterParagraphs,
) error {
	var batch paragraphBatch
	for _, chapter := range chapters {
		for _, paragraph := range chapter.paragraphs {
			paragraphID, err := storedb.ParseUUID(paragraph.ID)
			if err != nil {
				return err
			}
			chapterID, err := storedb.ParseUUID(paragraph.ChapterID)
			if err != nil {
				return library.ErrNotFound
			}
			if batch.bytes > 0 && batch.bytes+len(paragraph.Markdown)+len(paragraph.PlainText) > paragraphStatementBytes {
				if err := batch.write(ctx, tx, projectID, bookID, projectGeneration, bookGeneration); err != nil {
					return err
				}
				batch = paragraphBatch{}
			}
			batch.add(paragraphID, chapterID, chapter.generation, paragraph)
		}
	}
	return batch.write(ctx, tx, projectID, bookID, projectGeneration, bookGeneration)
}

// paragraphBatch is the columns of paragraphs one statement writes.
type paragraphBatch struct {
	ids, chapterIDs               []pgtype.UUID
	chapterGenerations, revisions []int64
	variants, kinds               []string
	orders, wordCounts            []int32
	markdowns, plainTexts         []string
	createdAt                     []pgtype.Timestamptz
	bytes                         int
}

func (batch *paragraphBatch) add(id, chapterID pgtype.UUID, chapterGeneration int64, paragraph library.Paragraph) {
	batch.ids = append(batch.ids, id)
	batch.chapterIDs = append(batch.chapterIDs, chapterID)
	batch.chapterGenerations = append(batch.chapterGenerations, chapterGeneration)
	batch.revisions = append(batch.revisions, paragraph.Revision)
	batch.variants = append(batch.variants, string(paragraph.Variant))
	batch.kinds = append(batch.kinds, string(paragraph.Kind))
	batch.orders = append(batch.orders, int32(paragraph.Order))
	batch.markdowns = append(batch.markdowns, paragraph.Markdown)
	batch.plainTexts = append(batch.plainTexts, paragraph.PlainText)
	batch.wordCounts = append(batch.wordCounts, int32(paragraph.WordCount))
	batch.createdAt = append(batch.createdAt, storedb.Timestamptz(paragraph.CreatedAt))
	batch.bytes += len(paragraph.Markdown) + len(paragraph.PlainText)
}

func (batch *paragraphBatch) write(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, projectGeneration, bookGeneration int64) error {
	if len(batch.ids) == 0 {
		return nil
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO chapter_paragraphs (
		  id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at
		)
		SELECT given.id, chapter.id, given.revision, given.variant, given.kind, given.sort_order,
		       given.markdown, given.plain_text, given.word_count, given.created_at, given.created_at
		FROM unnest(
		  $1::uuid[], $2::uuid[], $3::bigint[], $4::bigint[], $5::text[], $6::text[],
		  $7::integer[], $8::text[], $9::text[], $10::integer[], $11::timestamptz[]
		) AS given(id, chapter_id, chapter_generation, revision, variant, kind, sort_order, markdown, plain_text, word_count, created_at)
		JOIN chapters AS chapter ON chapter.id = given.chapter_id AND chapter.lifecycle_generation = given.chapter_generation
		JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id
		JOIN projects AS project ON project.id = chapter.project_id
		WHERE chapter.project_id = $12
		  AND chapter.book_id = $13
		  AND project.lifecycle_generation = $14
		  AND book.lifecycle_generation = $15
		  AND chapter.deleted_at IS NULL
		  AND book.deleted_at IS NULL
		  AND project.deleted_at IS NULL
	`, batch.ids, batch.chapterIDs, batch.chapterGenerations, batch.revisions, batch.variants, batch.kinds,
		batch.orders, batch.markdowns, batch.plainTexts, batch.wordCounts, batch.createdAt,
		projectID, bookID, projectGeneration, bookGeneration)
	if err != nil {
		return err
	}
	if command.RowsAffected() != int64(len(batch.ids)) {
		return pgx.ErrNoRows
	}
	return nil
}

// newChapterRow is a chapter a write creates in a book: its id, title and place.
type newChapterRow struct {
	id    pgtype.UUID
	title string
	order int
}

// createLibraryChapters creates chapters of one live book of a live project in one statement, of lifecycle
// generation 1 and of one time, and returns their rows in the order given. A gone book or project creates none and
// fails the write as pgx.ErrNoRows, as the write of one chapter did.
func createLibraryChapters(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, chapters []newChapterRow, at pgtype.Timestamptz) ([]sqlc.Chapter, error) {
	ids := make([]pgtype.UUID, len(chapters))
	titles := make([]string, len(chapters))
	orders := make([]int32, len(chapters))
	for index, chapter := range chapters {
		ids[index], titles[index], orders[index] = chapter.id, chapter.title, int32(chapter.order)
	}
	rows, err := tx.Query(ctx, `
		INSERT INTO chapters (id, project_id, book_id, title, "order", lifecycle_generation, created_at, updated_at)
		SELECT given.id, project.id, book.id, given.title, given.sort_order, 1, $4, $4
		FROM unnest($1::uuid[], $2::text[], $3::integer[]) WITH ORDINALITY AS given(id, title, sort_order, at)
		CROSS JOIN books AS book
		JOIN projects AS project ON project.id = book.project_id
		WHERE book.project_id = $5
		  AND book.id = $6
		  AND book.deleted_at IS NULL
		  AND project.deleted_at IS NULL
		ORDER BY given.at
		RETURNING id, book_id, title, "order", status, revision,
		          published_revision, published_at, publish_error, deleted_at,
		          purge_after, created_at, updated_at, lifecycle_generation,
		          hard_purge_started_at, governance_retention_started_at,
		          purge_deadline, project_id
	`, ids, titles, orders, at, projectID, bookID)
	if err != nil {
		return nil, err
	}
	created, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sqlc.Chapter])
	if err != nil {
		return nil, err
	}
	byID := make(map[pgtype.UUID]sqlc.Chapter, len(created))
	for _, chapter := range created {
		byID[chapter.ID] = chapter
	}
	ordered := make([]sqlc.Chapter, len(chapters))
	for index, chapter := range chapters {
		row, ok := byID[chapter.id]
		if !ok {
			return nil, pgx.ErrNoRows
		}
		ordered[index] = row
	}
	return ordered, nil
}

// newRowIDs gives count new ids of rows in one statement.
func newRowIDs(ctx context.Context, tx pgx.Tx, count int) ([]pgtype.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT gen_random_uuid() FROM generate_series(1, $1::integer)`, count)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return nil, err
	}
	if len(ids) != count {
		return nil, library.ErrConflict
	}
	return ids, nil
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
