package librarystore

import (
	"context"
	"slices"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type importedChapterPublication struct {
	id                  pgtype.UUID
	lifecycleGeneration int64
	title               string
	order               int32
	revision            int64
	paragraphs          []library.Paragraph
}

func publishedImportParagraphs(draft []library.Paragraph) []library.Paragraph {
	published := slices.Clone(draft)
	for index := range published {
		published[index].Variant = library.ParagraphVariantPublished
	}
	return published
}

// publishImportedBookInTx publishes the chapters an import wrote by batches: their published paragraphs, their rows and
// their members of the published catalog, a few statements for the whole book (specs/2026-11-11-import-speed.md IS1).
func (s *Store) publishImportedBookInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectID, bookID pgtype.UUID,
	projectGeneration, bookGeneration int64,
	bookTitle string,
	publishedAt pgtype.Timestamptz,
	chapters []importedChapterPublication,
) error {
	if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
		ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
		SourceView: governance.SourceViewPublished, BookID: bookID.String(),
		BookLifecycleGeneration: bookGeneration, Title: bookTitle,
	}); err != nil {
		return err
	}
	published := make([]chapterParagraphs, len(chapters))
	members := make([]library.ChapterSourceMember, len(chapters))
	ids := make([]pgtype.UUID, len(chapters))
	generations := make([]int64, len(chapters))
	revisions := make([]int64, len(chapters))
	for index, chapter := range chapters {
		published[index] = chapterParagraphs{generation: chapter.lifecycleGeneration, paragraphs: publishedImportParagraphs(chapter.paragraphs)}
		members[index] = library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
				SourceView: governance.SourceViewPublished, BookID: bookID.String(),
				BookLifecycleGeneration: bookGeneration, Title: bookTitle,
			},
			ChapterID: chapter.id.String(), ChapterLifecycleGeneration: chapter.lifecycleGeneration,
			SourceRevision: uint64(chapter.revision), Title: chapter.title, Order: int(chapter.order),
		}
		ids[index], generations[index], revisions[index] = chapter.id, chapter.lifecycleGeneration, chapter.revision
	}
	if err := createLibraryParagraphs(ctx, tx, projectID, bookID, projectGeneration, bookGeneration, published...); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE chapters AS chapter
		SET status = 'published',
		    published_revision = given.revision,
		    published_at = $1,
		    publish_error = NULL,
		    updated_at = $1
		FROM unnest($2::uuid[], $3::bigint[], $4::bigint[]) AS given(id, lifecycle_generation, revision),
		     books AS book
		JOIN projects AS project ON project.id = book.project_id
		WHERE chapter.project_id = $5
		  AND chapter.book_id = $6
		  AND chapter.id = given.id
		  AND chapter.lifecycle_generation = given.lifecycle_generation
		  AND book.project_id = $5
		  AND book.id = $6
		  AND book.lifecycle_generation = $7
		  AND project.id = $5
		  AND project.lifecycle_generation = $8
		  AND chapter.revision = given.revision
		  AND chapter.deleted_at IS NULL
		  AND book.deleted_at IS NULL
		  AND project.deleted_at IS NULL
	`, publishedAt, ids, generations, revisions, projectID, bookID, bookGeneration, projectGeneration)
	if err != nil {
		return err
	}
	if command.RowsAffected() != int64(len(chapters)) {
		return library.ErrConflict
	}
	if err := upsertChapterSourceMembers(ctx, tx, members); err != nil {
		return err
	}
	if err := normalizePublishedChapterOrder(ctx, tx, projectID, projectGeneration, bookID, bookGeneration); err != nil {
		return err
	}
	revision, err := createOrBumpPublishedSourceView(ctx, tx, projectID, projectGeneration)
	if err != nil {
		return err
	}
	if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, ensureTextIndexIntent); err != nil {
		return err
	}
	return s.hitImportFinalize("after_import_published_catalog_mutation")
}
