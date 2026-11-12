package librarystore

import (
	"context"
	"unicode/utf8"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// previewPlacedChapters bounds the chapters whose places «было → стало» a preview keeps.
const previewPlacedChapters = 200

// previewImportedBookInTx tells what an update of the book by the file would do and writes nothing into the book
// (specs/2026-10-27-update-book-from-file.md, UC6). It plans under the same locks and by the same code as the
// update. Its refs name the book and every live chapter it compared, so deleting either takes the preview away
// with the Job, as for an update (R6.3).
func (s *Store) previewImportedBookInTx(
	ctx context.Context,
	tx pgx.Tx,
	input library.FinalizeImportStoreInput,
	projectID, bookID pgtype.UUID,
	projectGeneration int64,
) (importedBookWrite, error) {
	update, err := lockImportedBookUpdate(ctx, tx, input, projectID, bookID)
	if err != nil {
		return importedBookWrite{}, err
	}
	bookVector := governance.SourceGenerationVector{
		ProjectID: input.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		BookID: bookID.String(), BookGeneration: governance.LifecycleGeneration(update.bookGeneration),
	}
	bookRef, err := governance.NewBookArtifactRef(bookVector, mustDraftSourceView(), governance.AbsentChatArm())
	if err != nil {
		return importedBookWrite{}, library.ErrValidation
	}
	write := importedBookWrite{bookID: bookID, refs: []governance.ArtifactSourceRef{bookRef}, counts: importedUpdateCounts(update.plan)}
	for _, chapter := range update.chapters {
		chapterVector := bookVector
		chapterVector.ChapterID, chapterVector.ChapterGeneration = chapter.id.String(), governance.LifecycleGeneration(chapter.generation)
		chapterRef, err := governance.NewChapterArtifactRef(chapterVector, mustDraftSourceView(), governance.AbsentChatArm())
		if err != nil {
			return importedBookWrite{}, library.ErrValidation
		}
		write.refs = append(write.refs, chapterRef)
	}
	for _, contents := range update.fileContents {
		write.paragraphCount += len(contents)
	}
	preview, err := updatePreviewInTx(ctx, tx, update, input.Chapters, projectID, write.counts)
	if err != nil {
		return importedBookWrite{}, err
	}
	write.counts.Preview = preview
	return write, nil
}

// updatePreviewInTx is what an update of a book does, chapter by chapter, read before it writes: the preview tells it
// before the update (UC6), and the update keeps it in its result for «Что изменилось» (specs/2026-11-11-ux7-trash.md
// TB4.1). R6.2: whether the threads are rebuilt is told by the rule of the layer on the chapters of the saga after the
// update; the chapters that get a new card are those with other words and the new ones. A removed chapter takes the
// built threads away with it (specs/2026-11-01-index-foundation.md, R2.8): they are built anew.
func updatePreviewInTx(
	ctx context.Context, tx pgx.Tx, update importedBookUpdate, file []library.FinalizeImportChapterInput, projectID pgtype.UUID,
	counts library.FinalizedImport,
) (*jobs.ImportPreview, error) {
	var chapters int
	var built bool
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM chapters WHERE project_id = $1 AND deleted_at IS NULL),
		       EXISTS (SELECT 1 FROM saga_threads WHERE project_id = $1 AND digest IS NOT NULL)
	`, projectID).Scan(&chapters, &built); err != nil {
		return nil, err
	}
	newCards := counts.ChangedChapters + counts.AddedChapters
	edited, err := chaptersEditedOnSiteInTx(ctx, tx, projectID, update.chapters)
	if err != nil {
		return nil, err
	}
	preview := importedBookPreview(update, file, edited)
	preview.NewCards = newCards
	preview.Threads = built && counts.RemovedChapters > 0 ||
		sagalayer.DueAfter(newCards, chapters-counts.RemovedChapters+counts.AddedChapters, built)
	return preview, nil
}

// importedBookPreview lists the chapters the update would change in the order of the book after it, then the
// removed ones in their order now; the places of at most previewPlacedChapters chapters, and at most
// jobs.MaxImportPreviewChapters chapters, the rest counted.
func importedBookPreview(update importedBookUpdate, file []library.FinalizeImportChapterInput, edited map[pgtype.UUID]bool) *jobs.ImportPreview {
	preview := &jobs.ImportPreview{Chapters: []jobs.ImportPreviewChapter{}}
	placed := 0
	add := func(chapter jobs.ImportPreviewChapter) {
		if len(preview.Chapters) == jobs.MaxImportPreviewChapters {
			preview.MoreChapters++
			return
		}
		preview.Chapters = append(preview.Chapters, chapter)
	}
	for index, pairing := range update.plan.Chapters {
		entry := jobs.ImportPreviewChapter{Order: index + 1, Title: previewTitle(file[index].Title)}
		if pairing.Book < 0 {
			entry.Change = jobs.ImportPreviewAdded
			add(entry)
			continue
		}
		chapter := update.chapters[pairing.Book]
		if pairing.Renamed {
			entry.PreviousTitle = previewTitle(chapter.title)
		}
		switch {
		case pairing.Change == domainlibrary.TextWords:
			entry.Change, entry.EditedOnSite = jobs.ImportPreviewWords, edited[chapter.id]
		case pairing.Change == domainlibrary.TextPunctuation:
			entry.Change, entry.EditedOnSite = jobs.ImportPreviewPunctuation, edited[chapter.id]
		case pairing.Renamed:
			entry.Change = jobs.ImportPreviewRenamed
		default:
			continue
		}
		if entry.Change != jobs.ImportPreviewRenamed && placed < previewPlacedChapters {
			placed++
			for _, place := range domainlibrary.ChangePlaces(plainTexts(chapter.contents), plainTexts(update.fileContents[index]), 2) {
				entry.Places = append(entry.Places, jobs.ImportPreviewPlace{Before: place.Before, After: place.After})
			}
		}
		add(entry)
	}
	for _, removed := range update.plan.Removed {
		chapter := update.chapters[removed]
		add(jobs.ImportPreviewChapter{
			Order: max(chapter.order, 1), Title: previewTitle(chapter.title), Change: jobs.ImportPreviewRemoved, EditedOnSite: edited[chapter.id],
		})
	}
	return preview
}

// chaptersEditedOnSiteInTx names the chapters of the book whose text in the book now was written on the site
// (specs/2026-11-11-ux9-shelf-chat.md SC1.4): a chapter «Письмо» added that no file has rewritten since — still its first
// revision — or one whose revision is the one its edit put into the book. A file that changes or removes such a chapter
// replaces that text, and the preview says so.
func chaptersEditedOnSiteInTx(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, chapters []importedBookChapter) (map[pgtype.UUID]bool, error) {
	ids := make([]pgtype.UUID, len(chapters))
	for index, chapter := range chapters {
		ids[index] = chapter.id
	}
	rows, err := tx.Query(ctx, `
		SELECT chapter.id
		FROM chapters AS chapter
		WHERE chapter.project_id = $1 AND chapter.id = ANY($2::uuid[]) AND EXISTS (
		  SELECT 1 FROM chapter_drafts AS draft
		  WHERE draft.project_id = chapter.project_id AND (
		    (draft.added_chapter_id = chapter.id AND chapter.revision = 1)
		    OR (draft.edits_chapter_id = chapter.id AND draft.edits_revision = chapter.revision AND draft.edits_previous_revision IS NOT NULL)))
	`, projectID, ids)
	if err != nil {
		return nil, err
	}
	edited, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return nil, err
	}
	byID := make(map[pgtype.UUID]bool, len(edited))
	for _, id := range edited {
		byID[id] = true
	}
	return byID, nil
}

// previewTitle keeps a title within the bound of a preview.
func previewTitle(title string) string {
	if utf8.RuneCountInString(title) <= jobs.MaxImportPreviewTitleLength {
		return title
	}
	return string([]rune(title)[:jobs.MaxImportPreviewTitleLength-1]) + "…"
}
