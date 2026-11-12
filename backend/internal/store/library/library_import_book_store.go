package librarystore

import (
	"context"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/textface"
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
	bookOrder, err := importedBookOrder(ctx, tx, projectID, input.ExpectedBookOrder)
	if err != nil {
		return importedBookWrite{}, err
	}
	book, err := sqlc.New(tx).CreateBook(ctx, sqlc.CreateBookParams{
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
	// The chapters, their paragraphs and their members of the catalog go by batches, a few statements for the whole
	// book (specs/2026-11-11-import-speed.md IS1): a statement per chapter or paragraph is a round trip each.
	contents := make([][]importedParagraphContent, len(input.Chapters))
	paragraphCount := 0
	for index, chapterInput := range input.Chapters {
		if strings.TrimSpace(chapterInput.Title) == "" || len(chapterInput.Paragraphs) == 0 {
			return importedBookWrite{}, library.ErrValidation
		}
		contents[index] = importedParagraphContents(chapterInput.Paragraphs)
		paragraphCount += len(contents[index])
	}
	ids, err := newRowIDs(ctx, tx, len(input.Chapters)+paragraphCount)
	if err != nil {
		return importedBookWrite{}, err
	}
	newChapters := make([]newChapterRow, len(input.Chapters))
	for index, chapterInput := range input.Chapters {
		newChapters[index] = newChapterRow{id: ids[index], title: chapterInput.Title, order: index + 1}
	}
	chapters, err := createLibraryChapters(ctx, tx, projectID, bookID, newChapters, finalizedAt)
	if err != nil {
		return importedBookWrite{}, err
	}
	if err := s.hitImportFinalize("after_import_chapter_create"); err != nil {
		return importedBookWrite{}, err
	}
	producedRefs := []governance.ArtifactSourceRef{bookRef}
	publishedChapters := make([]importedChapterPublication, len(chapters))
	draft := make([]chapterParagraphs, len(chapters))
	members := make([]library.ChapterSourceMember, len(chapters))
	paragraphIDs := ids[len(chapters):]
	for index, chapter := range chapters {
		chapterVector := governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			BookID: bookID.String(), BookGeneration: governance.LifecycleGeneration(book.LifecycleGeneration),
			ChapterID: chapter.ID.String(), ChapterGeneration: governance.LifecycleGeneration(chapter.LifecycleGeneration),
		}
		chapterRef, err := governance.NewChapterArtifactRef(chapterVector, selectedView, governance.AbsentChatArm())
		if err != nil {
			return importedBookWrite{}, library.ErrValidation
		}
		producedRefs = append(producedRefs, chapterRef)
		paragraphs := paragraphsWithIDs(paragraphIDs[:len(contents[index])], chapter.ID.String(), 1, contents[index], finalizedAt.Time)
		paragraphIDs = paragraphIDs[len(contents[index]):]
		draft[index] = chapterParagraphs{generation: chapter.LifecycleGeneration, paragraphs: paragraphs}
		members[index] = library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
				SourceView: governance.SourceViewDraft, BookID: bookID.String(),
				BookLifecycleGeneration: book.LifecycleGeneration, Title: book.Title,
			},
			ChapterID: chapter.ID.String(), ChapterLifecycleGeneration: chapter.LifecycleGeneration,
			SourceRevision: 1, Title: chapter.Title, Order: int(chapter.Order),
		}
		publishedChapters[index] = importedChapterPublication{
			id: chapter.ID, lifecycleGeneration: chapter.LifecycleGeneration,
			title: chapter.Title, order: chapter.Order, revision: chapter.Revision,
			paragraphs: paragraphs,
		}
	}
	if err := createLibraryParagraphs(ctx, tx, projectID, bookID, projectGeneration, book.LifecycleGeneration, draft...); err != nil {
		return importedBookWrite{}, err
	}
	if err := upsertChapterSourceMembers(ctx, tx, members); err != nil {
		return importedBookWrite{}, err
	}
	if err := s.hitImportFinalize("after_import_paragraphs_create"); err != nil {
		return importedBookWrite{}, err
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
			ctx, tx, projectID, bookID,
			projectGeneration, book.LifecycleGeneration, book.Title, finalizedAt, publishedChapters,
		); err != nil {
			return importedBookWrite{}, err
		}
	}
	return importedBookWrite{bookID: bookID, refs: producedRefs, paragraphCount: paragraphCount}, nil
}

// importedBookOrder is the place a new book of an import takes: the one its import was given — the next place at its
// staging, the place of its volume at the start of the queue — while no live book stands there. A volume of a queue
// keeps its place when a volume before it waits for the author or is skipped (specs/2026-11-11-ux7-upload.md UP3.2):
// the place is the queue's since its start, as the queue fences every other book move.
func importedBookOrder(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, expected int) (int, error) {
	next, err := validatedNextBookOrder(ctx, tx, projectID)
	if err != nil || expected <= 0 {
		return next, err
	}
	var taken bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM books WHERE project_id = $1 AND deleted_at IS NULL AND "order" = $2)
	`, projectID, expected).Scan(&taken); err != nil {
		return 0, err
	}
	if taken {
		return 0, library.ErrImportExpectedOrderConflict
	}
	return expected, nil
}

type importedParagraphContent struct {
	kind      library.ParagraphKind
	order     int
	markdown  string
	plainText string
}

// importedParagraphContents is the text of a file chapter as the library stores it: markdown in the Markdown subset with
// its italic and bold, plain_text its projection unless the input names it (specs/2026-11-08-italic-bold-in-text.md TF1.2).
func importedParagraphContents(inputs []library.ParagraphInput) []importedParagraphContent {
	contents := make([]importedParagraphContent, 0, len(inputs))
	for index, input := range inputs {
		content := importedParagraphContent{kind: input.Kind, order: input.Order, markdown: input.Markdown, plainText: strings.TrimSpace(textface.MarkdownPlain(input.Markdown))}
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

// importedParagraphs gives the paragraphs of one chapter revision their ids, all in one statement.
func importedParagraphs(ctx context.Context, tx pgx.Tx, chapterID string, revision int64, contents []importedParagraphContent, at time.Time) ([]library.Paragraph, error) {
	ids, err := newRowIDs(ctx, tx, len(contents))
	if err != nil {
		return nil, err
	}
	return paragraphsWithIDs(ids, chapterID, revision, contents, at), nil
}

// paragraphsWithIDs are the draft paragraphs of one chapter revision with the given ids, one a paragraph.
func paragraphsWithIDs(ids []pgtype.UUID, chapterID string, revision int64, contents []importedParagraphContent, at time.Time) []library.Paragraph {
	paragraphs := make([]library.Paragraph, 0, len(contents))
	for index, content := range contents {
		paragraphs = append(paragraphs, library.Paragraph{
			ID: ids[index].String(), ChapterID: chapterID, Revision: revision,
			Variant: library.ParagraphVariantDraft, Kind: content.kind, Order: content.order,
			Markdown: content.markdown, PlainText: content.plainText,
			WordCount: len(strings.Fields(content.plainText)), CreatedAt: at, UpdatedAt: at,
		})
	}
	return paragraphs
}
