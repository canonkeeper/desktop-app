package librarystore

import (
	"context"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// importedBookWrite is what an import wrote into the library: the book, the sources the import Job
// produced and, for an update, how the chapters of the file met the book.
type importedBookWrite struct {
	bookID         pgtype.UUID
	refs           []governance.ArtifactSourceRef
	paragraphCount int
	counts         library.FinalizedImport
}

// createImportedBookInTx creates the book of a new_book import at the next place of the saga.
func (s *Store) createImportedBookInTx(
	ctx context.Context,
	tx pgx.Tx,
	input library.FinalizeImportStoreInput,
	jobID, projectID pgtype.UUID,
	projectGeneration int64,
	finalizedAt pgtype.Timestamptz,
	initialPublication library.InitialPublication,
) (importedBookWrite, error) {
	var bookID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&bookID); err != nil {
		return importedBookWrite{}, err
	}
	bookOrder, err := validatedNextBookOrder(ctx, tx, projectID)
	if err != nil {
		return importedBookWrite{}, err
	}
	if input.ExpectedBookOrder > 0 && input.ExpectedBookOrder != bookOrder {
		return importedBookWrite{}, library.ErrImportExpectedOrderConflict
	}
	queries := sqlc.New(tx)
	book, err := queries.CreateBook(ctx, sqlc.CreateBookParams{
		ID:            bookID,
		Title:         input.BookTitle,
		SortOrder:     int32(bookOrder),
		DisplayNumber: strconv.Itoa(bookOrder),
		CreatedAt:     finalizedAt,
		ProjectID:     projectID,
	})
	if err != nil {
		return importedBookWrite{}, err
	}
	if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
		ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
		SourceView: governance.SourceViewDraft, BookID: bookID.String(),
		BookLifecycleGeneration: book.LifecycleGeneration, Title: book.Title,
	}); err != nil {
		return importedBookWrite{}, err
	}
	if err := s.hitImportFinalize("after_import_book_create"); err != nil {
		return importedBookWrite{}, err
	}
	linkedQueueItem, err := tx.Exec(ctx, `
		UPDATE import_queue_items
		SET book_id = $3, updated_at = $4
		WHERE project_id = $1 AND import_job_id = $2 AND book_id IS NULL
	`, projectID, jobID, bookID, finalizedAt)
	if err != nil {
		return importedBookWrite{}, err
	}
	if linkedQueueItem.RowsAffected() > 1 {
		return importedBookWrite{}, library.ErrConflict
	}
	selectedView := mustDraftSourceView()
	bookVector := governance.SourceGenerationVector{
		ProjectID: input.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		BookID: bookID.String(), BookGeneration: governance.LifecycleGeneration(book.LifecycleGeneration),
	}
	bookRef, err := governance.NewBookArtifactRef(bookVector, selectedView, governance.AbsentChatArm())
	if err != nil {
		return importedBookWrite{}, library.ErrValidation
	}
	producedRefs := []governance.ArtifactSourceRef{bookRef}
	paragraphCount := 0
	publishedChapters := make([]importedChapterPublication, 0, len(input.Chapters))
	for chapterIndex, chapterInput := range input.Chapters {
		if strings.TrimSpace(chapterInput.Title) == "" || len(chapterInput.Paragraphs) == 0 {
			return importedBookWrite{}, library.ErrValidation
		}
		var chapterID pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&chapterID); err != nil {
			return importedBookWrite{}, err
		}
		chapter, err := queries.CreateChapter(ctx, sqlc.CreateChapterParams{
			ID:        chapterID,
			Title:     chapterInput.Title,
			SortOrder: int32(chapterIndex + 1),
			CreatedAt: finalizedAt,
			ProjectID: projectID,
			BookID:    bookID,
		})
		if err != nil {
			return importedBookWrite{}, err
		}
		if err := s.hitImportFinalize("after_import_chapter_create"); err != nil {
			return importedBookWrite{}, err
		}
		chapterVector := governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			BookID: bookID.String(), BookGeneration: governance.LifecycleGeneration(book.LifecycleGeneration),
			ChapterID: chapterID.String(), ChapterGeneration: governance.LifecycleGeneration(chapter.LifecycleGeneration),
		}
		chapterRef, err := governance.NewChapterArtifactRef(chapterVector, selectedView, governance.AbsentChatArm())
		if err != nil {
			return importedBookWrite{}, library.ErrValidation
		}
		producedRefs = append(producedRefs, chapterRef)
		paragraphs, err := importedParagraphs(ctx, tx, chapterID.String(), 1, importedParagraphContents(chapterInput.Paragraphs), finalizedAt.Time)
		if err != nil {
			return importedBookWrite{}, err
		}
		if err := createLibraryParagraphs(
			ctx, queries, projectID, bookID,
			projectGeneration, book.LifecycleGeneration, chapter.LifecycleGeneration,
			paragraphs,
		); err != nil {
			return importedBookWrite{}, err
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
				SourceView: governance.SourceViewDraft, BookID: bookID.String(),
				BookLifecycleGeneration: book.LifecycleGeneration, Title: book.Title,
			},
			ChapterID: chapterID.String(), ChapterLifecycleGeneration: chapter.LifecycleGeneration,
			SourceRevision: 1, Title: chapter.Title, Order: int(chapter.Order),
		}); err != nil {
			return importedBookWrite{}, err
		}
		if err := s.hitImportFinalize("after_import_paragraphs_create"); err != nil {
			return importedBookWrite{}, err
		}
		publishedChapters = append(publishedChapters, importedChapterPublication{
			id: chapterID, lifecycleGeneration: chapter.LifecycleGeneration,
			title: chapter.Title, order: chapter.Order, revision: chapter.Revision,
			paragraphs: paragraphs,
		})
		paragraphCount += len(paragraphs)
	}
	draftRevision, err := bumpProjectSourceView(ctx, tx, projectID, projectGeneration, governance.SourceViewDraft)
	if err != nil {
		return importedBookWrite{}, err
	}
	if err := s.upsertCanonTextIndexIntent(ctx, tx, draftRevision, ensureTextIndexIntent); err != nil {
		return importedBookWrite{}, err
	}
	if err := s.hitImportFinalize("after_import_catalog_mutation"); err != nil {
		return importedBookWrite{}, err
	}
	if initialPublication == library.InitialPublicationPublishImported {
		if err := s.publishImportedBookInTx(
			ctx, tx, queries, projectID, bookID,
			projectGeneration, book.LifecycleGeneration, book.Title, finalizedAt, publishedChapters,
		); err != nil {
			return importedBookWrite{}, err
		}
	}
	return importedBookWrite{bookID: bookID, refs: producedRefs, paragraphCount: paragraphCount}, nil
}

type importedParagraphContent struct {
	kind      library.ParagraphKind
	order     int
	markdown  string
	plainText string
}

// importedParagraphContents is the text of a file chapter as the library stores it.
func importedParagraphContents(inputs []library.ParagraphInput) []importedParagraphContent {
	contents := make([]importedParagraphContent, 0, len(inputs))
	for index, input := range inputs {
		content := importedParagraphContent{kind: input.Kind, order: input.Order, markdown: input.Markdown, plainText: strings.TrimSpace(input.Markdown)}
		if content.order == 0 {
			content.order = index + 1
		}
		if input.Text != nil {
			content.plainText = strings.TrimSpace(*input.Text)
		}
		if content.kind == "" {
			content.kind = library.ParagraphKindParagraph
		}
		contents = append(contents, content)
	}
	return contents
}

// importedParagraphs gives the paragraphs of one chapter revision their ids.
func importedParagraphs(ctx context.Context, tx pgx.Tx, chapterID string, revision int64, contents []importedParagraphContent, at time.Time) ([]library.Paragraph, error) {
	paragraphs := make([]library.Paragraph, 0, len(contents))
	for _, content := range contents {
		var paragraphID pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&paragraphID); err != nil {
			return nil, err
		}
		paragraphs = append(paragraphs, library.Paragraph{
			ID: paragraphID.String(), ChapterID: chapterID, Revision: revision,
			Variant: library.ParagraphVariantDraft, Kind: content.kind, Order: content.order,
			Markdown: content.markdown, PlainText: content.plainText,
			WordCount: len(strings.Fields(content.plainText)), CreatedAt: at, UpdatedAt: at,
		})
	}
	return paragraphs, nil
}
