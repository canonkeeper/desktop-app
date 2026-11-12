package librarystore

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EditedChapterInTx reads a live chapter of a live book with its text at a revision, the current one when revision is
// 0 (specs/2026-11-05-voice-full-access.md A8.7). A chapter or a book deleted is library.ErrNotFound.
func (s *Store) EditedChapterInTx(ctx context.Context, tx pgx.Tx, projectID, chapterID string, revision int64) (library.EditedChapter, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.EditedChapter{}, library.ErrNotFound
	}
	chapter, err := storedb.ParseUUID(chapterID)
	if err != nil {
		return library.EditedChapter{}, library.ErrNotFound
	}
	var edited library.EditedChapter
	var book, previous pgtype.UUID
	var current int64
	if err := tx.QueryRow(ctx, `
		SELECT chapter.book_id, chapter.title, COALESCE(chapter.published_revision, 0),
		       (SELECT before.id FROM chapters AS before
		        WHERE before.project_id = chapter.project_id AND before.book_id = chapter.book_id AND before.deleted_at IS NULL
		          AND (before."order", before.id) < (chapter."order", chapter.id)
		        ORDER BY before."order" DESC, before.id DESC LIMIT 1)
		FROM chapters AS chapter
		JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id AND book.deleted_at IS NULL
		WHERE chapter.project_id = $1 AND chapter.id = $2 AND chapter.deleted_at IS NULL
	`, project, chapter).Scan(&book, &edited.Title, &current, &previous); errors.Is(err, pgx.ErrNoRows) {
		return library.EditedChapter{}, library.ErrNotFound
	} else if err != nil {
		return library.EditedChapter{}, err
	}
	if current < 1 {
		return library.EditedChapter{}, library.ErrNotFound
	}
	if revision == 0 {
		revision = current
	}
	rows, err := sqlc.New(tx).ListChapterParagraphsByChapterRevisionVariant(ctx, sqlc.ListChapterParagraphsByChapterRevisionVariantParams{
		ProjectID: project, BookID: book, ChapterID: chapter, Revision: revision, Variant: string(library.ParagraphVariantPublished),
	})
	if err != nil {
		return library.EditedChapter{}, err
	}
	for _, row := range rows {
		if strings.TrimSpace(row.PlainText) != "" {
			edited.Paragraphs = append(edited.Paragraphs, strings.TrimSpace(row.Markdown))
		}
	}
	if len(edited.Paragraphs) == 0 {
		return library.EditedChapter{}, library.ErrNotFound
	}
	edited.BookID, edited.ChapterID, edited.Revision = book.String(), chapter.String(), revision
	if previous.Valid {
		edited.PreviousChapterID = previous.String()
	}
	return edited, nil
}

// ReplaceChapterTextInTx gives a chapter of the book a new text at the next revision, in both text versions, on its
// place and under its id; the text index of both versions is asked for again, as after «Обновить из файла». A chapter
// not at ExpectedRevision is library.ErrConflict: someone changed it after the author opened it.
func (s *Store) ReplaceChapterTextInTx(ctx context.Context, tx pgx.Tx, input library.ReplaceChapterTextInput) (int64, error) {
	title := strings.TrimSpace(input.Title)
	if tx == nil || title == "" || len(input.Paragraphs) == 0 || input.ExpectedRevision < 1 {
		return 0, library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return 0, library.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return 0, library.ErrNotFound
	}
	chapterID, err := storedb.ParseUUID(input.ChapterID)
	if err != nil {
		return 0, library.ErrNotFound
	}
	var projectGeneration, bookGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
		return 0, library.ErrNotFound
	} else if err != nil {
		return 0, err
	}
	var bookTitle string
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation, title FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL FOR UPDATE
	`, bookID, projectID).Scan(&bookGeneration, &bookTitle); errors.Is(err, pgx.ErrNoRows) {
		return 0, library.ErrNotFound
	} else if err != nil {
		return 0, err
	}
	chapters, err := lockBookChapterRows(ctx, tx, projectID, bookID)
	if err != nil {
		return 0, err
	}
	var chapter *importedBookChapter
	for index := range chapters {
		if chapters[index].id == chapterID {
			chapter = &chapters[index]
		}
	}
	if chapter == nil {
		return 0, library.ErrNotFound
	}
	if chapter.revision != input.ExpectedRevision || chapter.publishedRevision != chapter.revision {
		return 0, library.ErrConflict
	}
	var at pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&at); err != nil {
		return 0, err
	}
	inputs := make([]library.ParagraphInput, len(input.Paragraphs))
	for index, paragraph := range input.Paragraphs {
		inputs[index] = library.ParagraphInput{Markdown: paragraph}
	}
	revision := chapter.revision + 1
	if _, err := writeImportedChapterText(ctx, tx, projectID, bookID, chapterID, projectGeneration, bookGeneration,
		chapter.generation, chapter.revision, revision, title, chapter.order, importedParagraphContents(inputs), at); err != nil {
		return 0, err
	}
	if err := s.publishBookTextInTx(ctx, tx, projectID, bookID, projectGeneration, bookGeneration, bookTitle); err != nil {
		return 0, err
	}
	if err := s.quota.CheckOwnerSourcesInTx(ctx, tx, input.ProjectID, quota.MeasureCharacters); err != nil {
		return 0, err
	}
	return revision, nil
}
