package librarystore

import (
	"context"
	"errors"
	"strings"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// importedBookChapter is a live chapter of the book an update rewrites, with its current text and the ids of
// its paragraphs, which the fingerprint of the text has.
type importedBookChapter struct {
	id                pgtype.UUID
	title             string
	order             int
	revision          int64
	publishedRevision int64
	generation        int64
	contents          []importedParagraphContent
	paragraphIDs      []string
}

// importedBookUpdate is a book locked for an update with its plan (specs/2026-10-27-update-book-from-file.md,
// UC3, UC4): the chapters of the book, the text of the file and which chapter of the book each chapter of the
// file is.
type importedBookUpdate struct {
	bookGeneration int64
	bookTitle      string
	chapters       []importedBookChapter
	fileContents   [][]importedParagraphContent
	plan           domainlibrary.BookUpdatePlan
}

// lockImportedBookUpdate locks a live book of the project and its chapters and plans the update by the
// chapters of the file. The update writes by the plan and the preview tells it, so both read it here.
func lockImportedBookUpdate(
	ctx context.Context, tx pgx.Tx, input library.FinalizeImportStoreInput, projectID, bookID pgtype.UUID,
) (importedBookUpdate, error) {
	var update importedBookUpdate
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation, title FROM books
		WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, bookID, projectID).Scan(&update.bookGeneration, &update.bookTitle); errors.Is(err, pgx.ErrNoRows) {
		return importedBookUpdate{}, library.ErrNotFound
	} else if err != nil {
		return importedBookUpdate{}, err
	}
	chapters, err := lockImportedBookChapters(ctx, tx, projectID, bookID)
	if err != nil {
		return importedBookUpdate{}, err
	}
	update.chapters = chapters
	update.fileContents = make([][]importedParagraphContent, len(input.Chapters))
	book := make([]domainlibrary.BookChapterText, len(chapters))
	file := make([]domainlibrary.BookChapterText, len(input.Chapters))
	for index, chapter := range input.Chapters {
		if strings.TrimSpace(chapter.Title) == "" || len(chapter.Paragraphs) == 0 {
			return importedBookUpdate{}, library.ErrValidation
		}
		update.fileContents[index] = importedParagraphContents(chapter.Paragraphs)
		file[index] = domainlibrary.BookChapterText{Title: chapter.Title, Paragraphs: plainTexts(update.fileContents[index])}
	}
	for index, chapter := range chapters {
		book[index] = domainlibrary.BookChapterText{Title: chapter.title, Paragraphs: plainTexts(chapter.contents)}
	}
	update.plan = domainlibrary.PlanBookUpdate(book, file)
	for index, pairing := range update.plan.Chapters {
		// A chapter whose published text lags behind its draft is written anew, as before the plan.
		if pairing.Book >= 0 && chapters[pairing.Book].publishedRevision != chapters[pairing.Book].revision {
			update.plan.Chapters[index].Change = domainlibrary.TextWords
		}
	}
	return update, nil
}

func plainTexts(contents []importedParagraphContent) []string {
	texts := make([]string, len(contents))
	for index, content := range contents {
		texts[index] = content.plainText
	}
	return texts
}

// updateImportedBookInTx rewrites a live book from the chapters of a new file of the same volume
// (specs/2026-10-27-update-book-from-file.md, UC1, UC3, UC4). A chapter whose text differs only in spaces keeps
// its revision and its card; one with other signs gets the next revision and keeps its card; one with other
// words gets the next revision and a new card later; a new one is created, and a chapter the file no longer
// has is deleted as by hand. The book keeps its id, place and title.
func (s *Store) updateImportedBookInTx(
	ctx context.Context,
	tx pgx.Tx,
	input library.FinalizeImportStoreInput,
	projectID, bookID pgtype.UUID,
	projectGeneration int64,
	finalizedAt pgtype.Timestamptz,
) (importedBookWrite, error) {
	update, err := lockImportedBookUpdate(ctx, tx, input, projectID, bookID)
	if err != nil {
		return importedBookWrite{}, err
	}
	bookGeneration, chapters, plan := update.bookGeneration, update.chapters, update.plan
	write := importedBookWrite{bookID: bookID, counts: importedUpdateCounts(plan)}
	for _, removed := range plan.Removed {
		chapter := chapters[removed]
		var tombstoneID pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&tombstoneID); err != nil {
			return importedBookWrite{}, err
		}
		request := governedChapterDeleteRequest(tombstoneID.String(), input.ActorUserID, input.ProjectID, chapter.id.String(), finalizedAt.Time)
		if _, err := s.fenceLockedChapterInTx(
			ctx, tx, request, projectID, bookID, chapter.id, tombstoneID,
			projectGeneration, bookGeneration, chapter.generation,
		); err != nil {
			return importedBookWrite{}, err
		}
	}

	bookVector := governance.SourceGenerationVector{
		ProjectID: input.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		BookID: bookID.String(), BookGeneration: governance.LifecycleGeneration(bookGeneration),
	}
	bookRef, err := governance.NewBookArtifactRef(bookVector, mustDraftSourceView(), governance.AbsentChatArm())
	if err != nil {
		return importedBookWrite{}, library.ErrValidation
	}
	write.refs = []governance.ArtifactSourceRef{bookRef}
	queries := sqlc.New(tx)
	for index, fileChapter := range input.Chapters {
		order := index + 1
		pairing := plan.Chapters[index]
		contents := update.fileContents[index]
		write.paragraphCount += len(contents)
		var chapterID pgtype.UUID
		var generation, revision int64
		switch {
		case pairing.Book < 0:
			if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&chapterID); err != nil {
				return importedBookWrite{}, err
			}
			created, err := queries.CreateChapter(ctx, sqlc.CreateChapterParams{
				ID: chapterID, Title: fileChapter.Title, SortOrder: int32(order),
				CreatedAt: finalizedAt, ProjectID: projectID, BookID: bookID,
			})
			if err != nil {
				return importedBookWrite{}, err
			}
			generation, revision = created.LifecycleGeneration, created.Revision
			if _, err := writeImportedChapterText(ctx, tx, queries, projectID, bookID, chapterID, projectGeneration, bookGeneration,
				generation, revision, revision, fileChapter.Title, order, contents, finalizedAt); err != nil {
				return importedBookWrite{}, err
			}
		case pairing.Change == domainlibrary.TextSame:
			chapter := chapters[pairing.Book]
			chapterID, generation, revision = chapter.id, chapter.generation, chapter.revision
			if chapter.title != fileChapter.Title || chapter.order != order {
				if err := placeImportedChapter(ctx, tx, projectID, bookID, chapterID, projectGeneration, bookGeneration,
					generation, revision, fileChapter.Title, order, finalizedAt); err != nil {
					return importedBookWrite{}, err
				}
			}
		default:
			chapter := chapters[pairing.Book]
			chapterID, generation, revision = chapter.id, chapter.generation, chapter.revision+1
			written, err := writeImportedChapterText(ctx, tx, queries, projectID, bookID, chapterID, projectGeneration, bookGeneration,
				generation, chapter.revision, revision, fileChapter.Title, order, contents, finalizedAt)
			if err != nil {
				return importedBookWrite{}, err
			}
			if pairing.Change == domainlibrary.TextPunctuation {
				if err := carryChapterCard(ctx, tx, projectID, chapter, written); err != nil {
					return importedBookWrite{}, err
				}
			}
		}
		chapterVector := bookVector
		chapterVector.ChapterID, chapterVector.ChapterGeneration = chapterID.String(), governance.LifecycleGeneration(generation)
		chapterRef, err := governance.NewChapterArtifactRef(chapterVector, mustDraftSourceView(), governance.AbsentChatArm())
		if err != nil {
			return importedBookWrite{}, library.ErrValidation
		}
		write.refs = append(write.refs, chapterRef)
	}
	if err := s.publishBookTextInTx(ctx, tx, projectID, bookID, projectGeneration, bookGeneration, update.bookTitle); err != nil {
		return importedBookWrite{}, err
	}
	return write, s.hitImportFinalize("after_import_update_catalog_mutation")
}

// importedUpdateCounts are the counters of the result of an update or its preview.
func importedUpdateCounts(plan domainlibrary.BookUpdatePlan) library.FinalizedImport {
	counts := plan.Counts()
	return library.FinalizedImport{
		UnchangedChapters: counts.Unchanged, ChangedChapters: counts.Words, AddedChapters: counts.Added,
		RemovedChapters: counts.Removed, PunctuationChapters: counts.Punctuation, RenamedChapters: counts.Renamed,
	}
}

// carryChapterCard moves the card of the text a chapter had to the text of its new revision after a change of
// signs (R3.2): the same row under the fingerprint of the new text, built_from naming the text the model read
// and the same created_at, as it is the same card. Without a card of the old text there is nothing to carry,
// and the cards Job builds one as for any text.
func carryChapterCard(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, before importedBookChapter, after []library.Paragraph) error {
	if len(before.paragraphIDs) != len(before.contents) {
		return library.ErrConflict
	}
	old := make([]governance.CanonicalParagraphContent, len(before.contents))
	for index, content := range before.contents {
		old[index] = governance.CanonicalParagraphContent{ParagraphID: before.paragraphIDs[index], Order: uint32(content.order), Content: content.plainText}
	}
	next := make([]governance.CanonicalParagraphContent, len(after))
	for index, paragraph := range after {
		next[index] = governance.CanonicalParagraphContent{ParagraphID: paragraph.ID, Order: uint32(paragraph.Order), Content: paragraph.PlainText}
	}
	oldHash, err := governance.CanonicalChapterContentDigest(old)
	if err != nil {
		return library.ErrConflict
	}
	newHash, err := governance.CanonicalChapterContentDigest(next)
	if err != nil {
		return library.ErrConflict
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO chapter_cards (project_id, chapter_id, content_hash, card_json, model, version, built_from, created_at)
		SELECT card.project_id, card.chapter_id, $4, card.card_json, card.model, card.version,
		       coalesce(card.built_from, card.content_hash), card.created_at
		FROM chapter_cards AS card
		WHERE card.project_id = $1 AND card.chapter_id = $2 AND card.content_hash = $3
		ON CONFLICT (project_id, chapter_id, content_hash) DO NOTHING
	`, projectID, before.id, oldHash[:], newHash[:])
	return err
}

// lockBookChapterRows locks the live chapters of a book in their order, without their text.
func lockBookChapterRows(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID) ([]importedBookChapter, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, title, "order", revision, COALESCE(published_revision, 0), lifecycle_generation
		FROM chapters
		WHERE project_id = $1 AND book_id = $2 AND deleted_at IS NULL
		ORDER BY "order", id
		FOR UPDATE
	`, projectID, bookID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (importedBookChapter, error) {
		var chapter importedBookChapter
		err := row.Scan(&chapter.id, &chapter.title, &chapter.order, &chapter.revision, &chapter.publishedRevision, &chapter.generation)
		return chapter, err
	})
}

func lockImportedBookChapters(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID) ([]importedBookChapter, error) {
	chapters, err := lockBookChapterRows(ctx, tx, projectID, bookID)
	if err != nil {
		return nil, err
	}
	byID := make(map[pgtype.UUID]*importedBookChapter, len(chapters))
	for index := range chapters {
		byID[chapters[index].id] = &chapters[index]
	}
	paragraphs, err := tx.Query(ctx, `
		SELECT paragraph.chapter_id, paragraph.id, paragraph.kind, paragraph."order", paragraph.markdown, paragraph.plain_text
		FROM chapter_paragraphs AS paragraph
		JOIN chapters AS chapter
		  ON chapter.id = paragraph.chapter_id AND chapter.revision = paragraph.revision
		WHERE chapter.project_id = $1 AND chapter.book_id = $2 AND chapter.deleted_at IS NULL
		  AND paragraph.variant = 'draft'
		ORDER BY paragraph.chapter_id, paragraph."order", paragraph.id
	`, projectID, bookID)
	if err != nil {
		return nil, err
	}
	defer paragraphs.Close()
	for paragraphs.Next() {
		var chapterID, paragraphID pgtype.UUID
		var content importedParagraphContent
		if err := paragraphs.Scan(&chapterID, &paragraphID, &content.kind, &content.order, &content.markdown, &content.plainText); err != nil {
			return nil, err
		}
		if chapter := byID[chapterID]; chapter != nil {
			chapter.contents = append(chapter.contents, content)
			chapter.paragraphIDs = append(chapter.paragraphIDs, paragraphID.String())
		}
	}
	return chapters, paragraphs.Err()
}

// writeImportedChapterText stores the file text as the given revision of the chapter in both text
// versions and publishes it; expectedRevision is the revision the chapter row holds now. It returns the
// paragraphs it wrote.
func writeImportedChapterText(
	ctx context.Context,
	tx pgx.Tx,
	queries *sqlc.Queries,
	projectID, bookID, chapterID pgtype.UUID,
	projectGeneration, bookGeneration, chapterGeneration, expectedRevision, revision int64,
	title string,
	order int,
	contents []importedParagraphContent,
	at pgtype.Timestamptz,
) ([]library.Paragraph, error) {
	paragraphs, err := importedParagraphs(ctx, tx, chapterID.String(), revision, contents, at.Time)
	if err != nil {
		return nil, err
	}
	for _, variant := range [][]library.Paragraph{paragraphs, publishedImportParagraphs(paragraphs)} {
		if err := createLibraryParagraphs(ctx, queries, projectID, bookID, projectGeneration, bookGeneration, chapterGeneration, variant); err != nil {
			return nil, err
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE chapters
		SET title = $1, "order" = $2, revision = $3,
		    status = 'published', published_revision = $3, published_at = $4, publish_error = NULL, updated_at = $4
		WHERE project_id = $5 AND book_id = $6 AND id = $7
		  AND lifecycle_generation = $8 AND revision = $9 AND deleted_at IS NULL
	`, title, order, revision, at, projectID, bookID, chapterID, chapterGeneration, expectedRevision)
	if err != nil {
		return nil, err
	}
	if command.RowsAffected() != 1 {
		return nil, library.ErrConflict
	}
	return paragraphs, upsertImportedChapterMembers(ctx, tx, projectID, bookID, chapterID, projectGeneration, bookGeneration, chapterGeneration, revision, title, order)
}

// placeImportedChapter gives a chapter whose text did not change the title and place of the file.
func placeImportedChapter(
	ctx context.Context,
	tx pgx.Tx,
	projectID, bookID, chapterID pgtype.UUID,
	projectGeneration, bookGeneration, chapterGeneration, revision int64,
	title string,
	order int,
	at pgtype.Timestamptz,
) error {
	command, err := tx.Exec(ctx, `
		UPDATE chapters SET title = $1, "order" = $2, updated_at = $3
		WHERE project_id = $4 AND book_id = $5 AND id = $6
		  AND lifecycle_generation = $7 AND revision = $8 AND deleted_at IS NULL
	`, title, order, at, projectID, bookID, chapterID, chapterGeneration, revision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return library.ErrConflict
	}
	return upsertImportedChapterMembers(ctx, tx, projectID, bookID, chapterID, projectGeneration, bookGeneration, chapterGeneration, revision, title, order)
}

func upsertImportedBookMembers(ctx context.Context, tx pgx.Tx, projectID, bookID pgtype.UUID, projectGeneration, bookGeneration int64, title string) error {
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
			SourceView: view, BookID: bookID.String(), BookLifecycleGeneration: bookGeneration, Title: title,
		}); err != nil {
			return err
		}
	}
	return nil
}

func upsertImportedChapterMembers(
	ctx context.Context,
	tx pgx.Tx,
	projectID, bookID, chapterID pgtype.UUID,
	projectGeneration, bookGeneration, chapterGeneration, revision int64,
	title string,
	order int,
) error {
	var bookTitle string
	if err := tx.QueryRow(ctx, `SELECT title FROM books WHERE id = $1 AND project_id = $2`, bookID, projectID).Scan(&bookTitle); err != nil {
		return err
	}
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
				SourceView: view, BookID: bookID.String(), BookLifecycleGeneration: bookGeneration, Title: bookTitle,
			},
			ChapterID: chapterID.String(), ChapterLifecycleGeneration: chapterGeneration,
			SourceRevision: uint64(revision), Title: title, Order: order,
		}); err != nil {
			return err
		}
	}
	return nil
}
