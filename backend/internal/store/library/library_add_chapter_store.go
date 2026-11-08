package librarystore

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// AddChapterInTx adds one chapter to a live book in the caller's transaction
// (specs/2026-11-01-writer-assistant-index.md, R7.5): after the chapter named, or at the start of the book
// without one. It takes the path of a new chapter of «Обновить из файла»: the text goes to both text
// versions at once and both get their intent of the text index, from which cards and threads follow. The
// chapters after it only move. It counts against the limits of the text and spends no import.
func (s *Store) AddChapterInTx(ctx context.Context, tx pgx.Tx, input library.AddChapterInput) (string, error) {
	title := strings.TrimSpace(input.Title)
	if tx == nil || title == "" || len(input.Paragraphs) == 0 || input.At.IsZero() {
		return "", library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return "", library.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return "", library.ErrNotFound
	}
	var projectGeneration, bookGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
		return "", library.ErrNotFound
	} else if err != nil {
		return "", err
	}
	var bookTitle string
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation, title FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL FOR UPDATE
	`, bookID, projectID).Scan(&bookGeneration, &bookTitle); errors.Is(err, pgx.ErrNoRows) {
		return "", library.ErrNotFound
	} else if err != nil {
		return "", err
	}
	chapters, err := lockBookChapterRows(ctx, tx, projectID, bookID)
	if err != nil {
		return "", err
	}
	position := 0
	if input.AfterChapterID != "" {
		position = -1
		for index, chapter := range chapters {
			if chapter.id.String() == input.AfterChapterID {
				position = index + 1
				break
			}
		}
		if position < 0 {
			return "", library.ErrNotFound
		}
	}
	var at pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&at); err != nil {
		return "", err
	}
	// A book made by hand may not stand in the published version yet.
	if _, err := createOrBumpPublishedSourceView(ctx, tx, projectID, projectGeneration); err != nil {
		return "", err
	}
	if err := upsertImportedBookMembers(ctx, tx, projectID, bookID, projectGeneration, bookGeneration, bookTitle); err != nil {
		return "", err
	}
	for index := position; index < len(chapters); index++ {
		chapter := chapters[index]
		if order := index + 2; chapter.order != order {
			if err := placeImportedChapter(ctx, tx, projectID, bookID, chapter.id, projectGeneration, bookGeneration,
				chapter.generation, chapter.revision, chapter.title, order, at); err != nil {
				return "", err
			}
		}
	}
	var chapterID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&chapterID); err != nil {
		return "", err
	}
	order := position + 1
	queries := sqlc.New(tx)
	created, err := queries.CreateChapter(ctx, sqlc.CreateChapterParams{
		ID: chapterID, Title: title, SortOrder: int32(order), CreatedAt: at, ProjectID: projectID, BookID: bookID,
	})
	if err != nil {
		return "", err
	}
	inputs := make([]library.ParagraphInput, len(input.Paragraphs))
	for index, paragraph := range input.Paragraphs {
		inputs[index] = library.ParagraphInput{Markdown: paragraph}
	}
	if _, err := writeImportedChapterText(ctx, tx, queries, projectID, bookID, chapterID, projectGeneration, bookGeneration,
		created.LifecycleGeneration, created.Revision, created.Revision, title, order, importedParagraphContents(inputs), at); err != nil {
		return "", err
	}
	if err := s.publishBookTextInTx(ctx, tx, projectID, bookID, projectGeneration, bookGeneration, bookTitle); err != nil {
		return "", err
	}
	if err := quotastore.CheckOwnerSourcesInTx(ctx, tx, input.ProjectID); err != nil {
		return "", err
	}
	return chapterID.String(), nil
}

// publishBookTextInTx closes a change of the text of a book in both text versions: the members of the book,
// the published order, and the intent of the text index of each version.
func (s *Store) publishBookTextInTx(
	ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, projectGeneration, bookGeneration int64, bookTitle string,
) error {
	if err := upsertImportedBookMembers(ctx, tx, projectID, bookID, projectGeneration, bookGeneration, bookTitle); err != nil {
		return err
	}
	if err := normalizePublishedChapterOrder(ctx, tx, projectID, projectGeneration, bookID, bookGeneration); err != nil {
		return err
	}
	draftRevision, err := bumpProjectSourceView(ctx, tx, projectID, projectGeneration, governance.SourceViewDraft)
	if err != nil {
		return err
	}
	if err := s.upsertCanonTextIndexIntent(ctx, tx, draftRevision, ensureTextIndexIntent); err != nil {
		return err
	}
	publishedRevision, err := createOrBumpPublishedSourceView(ctx, tx, projectID, projectGeneration)
	if err != nil {
		return err
	}
	return s.upsertCanonTextIndexIntent(ctx, tx, publishedRevision, ensureTextIndexIntent)
}
