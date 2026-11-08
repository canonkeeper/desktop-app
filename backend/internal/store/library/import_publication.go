package librarystore

import (
	"context"
	"slices"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/sqlc"

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

func (s *Store) publishImportedBookInTx(
	ctx context.Context,
	tx pgx.Tx,
	queries *sqlc.Queries,
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
	for _, chapter := range chapters {
		if err := createLibraryParagraphs(
			ctx, queries, projectID, bookID,
			projectGeneration, bookGeneration, chapter.lifecycleGeneration,
			publishedImportParagraphs(chapter.paragraphs),
		); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `
			UPDATE chapters AS chapter
			SET status = 'published',
			    published_revision = $1,
			    published_at = $2,
			    publish_error = NULL,
			    updated_at = $2
			FROM books AS book
			JOIN projects AS project ON project.id = book.project_id
			WHERE chapter.project_id = $3
			  AND chapter.book_id = $4
			  AND chapter.id = $5
			  AND chapter.lifecycle_generation = $6
			  AND book.project_id = $3
			  AND book.id = $4
			  AND book.lifecycle_generation = $7
			  AND project.id = $3
			  AND project.lifecycle_generation = $8
			  AND chapter.revision = $1
			  AND chapter.deleted_at IS NULL
			  AND book.deleted_at IS NULL
			  AND project.deleted_at IS NULL
		`, chapter.revision, publishedAt, projectID, bookID, chapter.id,
			chapter.lifecycleGeneration, bookGeneration, projectGeneration)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return library.ErrConflict
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectID.String(), ProjectLifecycleGeneration: projectGeneration,
				SourceView: governance.SourceViewPublished, BookID: bookID.String(),
				BookLifecycleGeneration: bookGeneration, Title: bookTitle,
			},
			ChapterID: chapter.id.String(), ChapterLifecycleGeneration: chapter.lifecycleGeneration,
			SourceRevision: uint64(chapter.revision), Title: chapter.title, Order: int(chapter.order),
		}); err != nil {
			return err
		}
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
