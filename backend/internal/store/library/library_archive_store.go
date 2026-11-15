package librarystore

import (
	"context"
	"encoding/hex"
	"math"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The text of a saga file is written by the code of the import of a book, on the transaction of the open, with the
// paragraphs of the file instead of a parsed file (specs/2026-11-13-desk-sagafile.md SF3.3, SF3.4): new ids from the base,
// the orders, kinds, Markdown, titles and places as the file has them, the plain text the projection of the import.

// ArchiveBook is a book of a saga file.
type ArchiveBook struct {
	ProjectID         string
	ProjectGeneration int64
	KeyNumber         int
	Order             int
	Title             string
	Subtitle          *string
	CoverColor        *string
	DisplayNumber     string
	// Published says some chapter of the book has a published text: the book is a member of that text too.
	Published bool
	At        time.Time
}

// ArchiveBookRow is the book the open wrote.
type ArchiveBookRow struct {
	ProjectID         pgtype.UUID
	ProjectGeneration int64
	ID                pgtype.UUID
	Generation        int64
	Title             string
	At                pgtype.Timestamptz
}

// CreateArchiveBookInTx writes a book with the number, place, subtitle, colour and number of the file: the number is the
// book's own and the counter of the saga rises to it (migration 0135).
func (s *Store) CreateArchiveBookInTx(ctx context.Context, tx pgx.Tx, input ArchiveBook) (ArchiveBookRow, error) {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil || input.KeyNumber < 1 || input.Order < 1 || input.Order > math.MaxInt32 || strings.TrimSpace(input.Title) == "" {
		return ArchiveBookRow{}, library.ErrValidation
	}
	at := storedb.Timestamptz(input.At)
	row := ArchiveBookRow{ProjectID: projectID, ProjectGeneration: input.ProjectGeneration, Title: input.Title, At: at}
	if err := tx.QueryRow(ctx, `
		INSERT INTO books (id, project_id, title, subtitle, cover_color, "order", display_number, key_number, created_at, updated_at)
		SELECT gen_random_uuid(), project.id, $2, $3, $4, $5, $6, $7, $8, $8
		FROM projects AS project WHERE project.id = $1 AND project.deleted_at IS NULL AND project.lifecycle_generation = $9
		RETURNING id, lifecycle_generation
	`, projectID, input.Title, input.Subtitle, input.CoverColor, input.Order, input.DisplayNumber, input.KeyNumber, at,
		input.ProjectGeneration).Scan(&row.ID, &row.Generation); err != nil {
		return ArchiveBookRow{}, err
	}
	views := []governance.SourceView{governance.SourceViewDraft}
	if input.Published {
		views = append(views, governance.SourceViewPublished)
	}
	for _, view := range views {
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: input.ProjectID, ProjectLifecycleGeneration: input.ProjectGeneration, SourceView: view,
			BookID: row.ID.String(), BookLifecycleGeneration: row.Generation, Title: input.Title,
		}); err != nil {
			return ArchiveBookRow{}, err
		}
	}
	return row, nil
}

// ArchiveChapter is a chapter of a saga file: its paragraphs with the order, kind and Markdown of the file; PublishedOrder
// is its place in the published text, nil for a chapter only in the draft text.
type ArchiveChapter struct {
	Title          string
	Order          int
	PublishedOrder *int
	Paragraphs     []library.ParagraphInput
}

// ArchiveChapterRow is a chapter the open wrote: its id, the ids of its paragraphs in the order of the file and the digest
// of the rows it wrote — the one the text index finds (SF3.5).
type ArchiveChapterRow struct {
	ID          string
	Paragraphs  []string
	ContentHash string
}

// WriteArchiveChaptersInTx writes chapters of one book of the open: the chapters and their paragraphs of revision 1 in the
// draft text with the place of the file, and the published text of those that have one, at the place it has there —
// without the renumbering of a publication, so every key of a chapter is the key it had (SF3.4).
func (s *Store) WriteArchiveChaptersInTx(ctx context.Context, tx pgx.Tx, book ArchiveBookRow, chapters []ArchiveChapter) ([]ArchiveChapterRow, error) {
	if len(chapters) == 0 {
		return nil, nil
	}
	contents := make([][]importedParagraphContent, len(chapters))
	count := 0
	for index, chapter := range chapters {
		if strings.TrimSpace(chapter.Title) == "" || len(chapter.Paragraphs) == 0 || chapter.Order < 1 {
			return nil, library.ErrValidation
		}
		contents[index] = importedParagraphContents(chapter.Paragraphs)
		count += len(contents[index])
	}
	ids, err := newRowIDs(ctx, tx, len(chapters)+count)
	if err != nil {
		return nil, err
	}
	rows := make([]newChapterRow, len(chapters))
	for index, chapter := range chapters {
		rows[index] = newChapterRow{id: ids[index], title: chapter.Title, order: chapter.Order}
	}
	created, err := createLibraryChapters(ctx, tx, book.ProjectID, book.ID, rows, book.At)
	if err != nil {
		return nil, err
	}
	paragraphIDs := ids[len(chapters):]
	draft := make([]chapterParagraphs, len(created))
	var published []chapterParagraphs
	var draftMembers, publishedMembers []library.ChapterSourceMember
	var publishedIDs []pgtype.UUID
	var publishedGenerations, publishedRevisions []int64
	written := make([]ArchiveChapterRow, len(created))
	for index, chapter := range created {
		paragraphs := paragraphsWithIDs(paragraphIDs[:len(contents[index])], chapter.ID.String(), 1, contents[index], book.At.Time)
		paragraphIDs = paragraphIDs[len(contents[index]):]
		draft[index] = chapterParagraphs{generation: chapter.LifecycleGeneration, paragraphs: paragraphs}
		member := library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: book.ProjectID.String(), ProjectLifecycleGeneration: book.ProjectGeneration,
				SourceView: governance.SourceViewDraft, BookID: book.ID.String(), BookLifecycleGeneration: book.Generation, Title: book.Title,
			},
			ChapterID: chapter.ID.String(), ChapterLifecycleGeneration: chapter.LifecycleGeneration,
			SourceRevision: uint64(chapter.Revision), Title: chapter.Title, Order: int(chapter.Order),
		}
		draftMembers = append(draftMembers, member)
		if order := chapters[index].PublishedOrder; order != nil {
			published = append(published, chapterParagraphs{generation: chapter.LifecycleGeneration, paragraphs: publishedImportParagraphs(paragraphs)})
			member.SourceView, member.Order = governance.SourceViewPublished, *order
			publishedMembers = append(publishedMembers, member)
			publishedIDs = append(publishedIDs, chapter.ID)
			publishedGenerations = append(publishedGenerations, chapter.LifecycleGeneration)
			publishedRevisions = append(publishedRevisions, chapter.Revision)
		}
		canonical := make([]governance.CanonicalParagraphContent, len(paragraphs))
		row := ArchiveChapterRow{ID: chapter.ID.String(), Paragraphs: make([]string, len(paragraphs))}
		for position, paragraph := range paragraphs {
			row.Paragraphs[position] = paragraph.ID
			canonical[position] = governance.CanonicalParagraphContent{ParagraphID: paragraph.ID, Order: uint32(paragraph.Order), Content: paragraph.PlainText}
		}
		digest, err := governance.CanonicalChapterContentDigest(canonical)
		if err != nil {
			return nil, library.ErrValidation
		}
		row.ContentHash = hex.EncodeToString(digest[:])
		written[index] = row
	}
	if err := createLibraryParagraphs(ctx, tx, book.ProjectID, book.ID, book.ProjectGeneration, book.Generation, draft...); err != nil {
		return nil, err
	}
	if err := upsertChapterSourceMembers(ctx, tx, draftMembers); err != nil {
		return nil, err
	}
	if len(published) > 0 {
		if err := createLibraryParagraphs(ctx, tx, book.ProjectID, book.ID, book.ProjectGeneration, book.Generation, published...); err != nil {
			return nil, err
		}
		if err := markChaptersPublished(ctx, tx, book.ProjectID, book.ID, book.ProjectGeneration, book.Generation,
			publishedIDs, publishedGenerations, publishedRevisions, book.At); err != nil {
			return nil, err
		}
		if err := upsertChapterSourceMembers(ctx, tx, publishedMembers); err != nil {
			return nil, err
		}
	}
	return written, nil
}

// FinishArchiveTextInTx ends the text of an open: the written text is checked against the quotas of the owner as an
// admitted book is, the catalogs of the texts move and the text index of each is asked for (SF3.3). The wave «Сага
// готова» is not opened (SF3.11).
func (s *Store) FinishArchiveTextInTx(ctx context.Context, tx pgx.Tx, projectID string, projectGeneration int64, published bool) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.ErrValidation
	}
	if err := s.quota.CheckAdmittedSourcesInTx(ctx, tx, projectID, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters); err != nil {
		return err
	}
	revision, err := bumpProjectSourceView(ctx, tx, project, projectGeneration, governance.SourceViewDraft)
	if err != nil {
		return err
	}
	if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, ensureTextIndexIntent); err != nil {
		return err
	}
	if !published {
		return nil
	}
	if revision, err = createOrBumpPublishedSourceView(ctx, tx, project, projectGeneration); err != nil {
		return err
	}
	return s.upsertCanonTextIndexIntent(ctx, tx, revision, ensureTextIndexIntent)
}

// RaiseBookKeyNumbersInTx sets the counter of the numbers of the books of a new saga to the one of its file: a book added
// later gets the number after every number the saga ever gave (SF3.4).
func RaiseBookKeyNumbersInTx(ctx context.Context, tx pgx.Tx, projectID string, numbers int) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.ErrValidation
	}
	command, err := tx.Exec(ctx, `
		UPDATE projects SET book_key_numbers = greatest(book_key_numbers, $2) WHERE id = $1 AND deleted_at IS NULL
	`, project, numbers)
	if err == nil && command.RowsAffected() != 1 {
		return library.ErrNotFound
	}
	return err
}
