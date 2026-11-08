package librarystore

import (
	"context"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) PublishChapter(ctx context.Context, input library.PublishChapterStoreInput) (library.Chapter, error) {
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	chapterUUID, err := storedb.ParseUUID(input.ChapterID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	var chapter sqlc.Chapter
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockCurrentBookSource(ctx, tx, projectUUID, bookUUID); err != nil {
			return err
		}
		vector, err := lockCurrentChapterSource(ctx, tx, projectUUID, bookUUID, chapterUUID)
		if err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("publish_chapter_after_source_lock"); err != nil {
			return err
		}
		var currentRevision int64
		var publishedRevision pgtype.Int8
		var chapterTitle, bookTitle string
		if err := tx.QueryRow(ctx, `
			SELECT chapter.revision, chapter.published_revision, chapter.title, book.title
			FROM chapters AS chapter
			JOIN books AS book ON book.project_id = chapter.project_id AND book.id = chapter.book_id
			WHERE chapter.project_id = $1 AND chapter.book_id = $2 AND chapter.id = $3
		`, projectUUID, bookUUID, chapterUUID).Scan(&currentRevision, &publishedRevision, &chapterTitle, &bookTitle); err != nil {
			return err
		}
		if currentRevision != input.Revision {
			return library.ErrConflict
		}
		queries := sqlc.New(tx)
		samePublishedRevision := publishedRevision.Valid && publishedRevision.Int64 == currentRevision
		if samePublishedRevision {
			var publishedBookTitle, publishedChapterTitle string
			var publishedSourceRevision int64
			if err := tx.QueryRow(ctx, `
				SELECT book_member.title, chapter_member.title, chapter_member.source_revision
				FROM book_source_members AS book_member
				JOIN chapter_source_members AS chapter_member
				  ON chapter_member.project_id = book_member.project_id
				 AND chapter_member.source_view = book_member.source_view
				 AND chapter_member.project_lifecycle_generation = book_member.project_lifecycle_generation
				 AND chapter_member.book_id = book_member.book_id
				 AND chapter_member.book_lifecycle_generation = book_member.book_lifecycle_generation
				WHERE book_member.project_id = $1
				  AND book_member.source_view = 'published'
				  AND book_member.project_lifecycle_generation = $2
				  AND book_member.book_id = $3
				  AND book_member.book_lifecycle_generation = $4
				  AND chapter_member.chapter_id = $5
				  AND chapter_member.chapter_lifecycle_generation = $6
			`, projectUUID, vector.Project, bookUUID, vector.Book, chapterUUID, vector.Chapter).Scan(
				&publishedBookTitle, &publishedChapterTitle, &publishedSourceRevision,
			); err != nil {
				return mapSourceCatalogNotFound(err)
			}
			if publishedBookTitle == bookTitle && publishedChapterTitle == chapterTitle && publishedSourceRevision == currentRevision {
				chapter, err = queries.GetChapterByID(ctx, sqlc.GetChapterByIDParams{
					ProjectID: projectUUID,
					BookID:    bookUUID,
					ChapterID: chapterUUID,
				})
				return err
			}
			chapter, err = queries.GetChapterByID(ctx, sqlc.GetChapterByIDParams{
				ProjectID: projectUUID,
				BookID:    bookUUID,
				ChapterID: chapterUUID,
			})
			if err != nil {
				return err
			}
		} else {
			paragraphs, err := loadChapterDraftSnapshot(ctx, tx, chapterUUID, currentRevision)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
DELETE FROM chapter_paragraphs AS paragraph
USING chapters AS chapter, books AS book, projects AS project
WHERE paragraph.chapter_id = $1
  AND paragraph.revision = $2
  AND paragraph.variant = 'published'
  AND chapter.project_id = $3
  AND chapter.book_id = $4
  AND chapter.id = $1
  AND chapter.lifecycle_generation = $5
  AND book.project_id = $3
  AND book.id = $4
  AND book.lifecycle_generation = $6
  AND project.id = $3
  AND project.lifecycle_generation = $7
  AND chapter.deleted_at IS NULL
  AND book.deleted_at IS NULL
  AND project.deleted_at IS NULL
			`, chapterUUID, currentRevision, projectUUID, bookUUID, vector.Chapter, vector.Book, vector.Project); err != nil {
				return err
			}
			for index := range paragraphs {
				paragraphs[index].Variant = library.ParagraphVariantPublished
			}
			if err := createLibraryParagraphs(ctx, queries, projectUUID, bookUUID, vector.Project, vector.Book, vector.Chapter, paragraphs); err != nil {
				return err
			}
			row := tx.QueryRow(ctx, `
UPDATE chapters c
SET status = 'published',
    published_revision = $1,
    published_at = $2,
    publish_error = NULL,
    updated_at = $2
FROM books b
JOIN projects p ON p.id = b.project_id
WHERE c.project_id = $3
  AND c.book_id = $4
  AND c.id = $5
  AND c.lifecycle_generation = $6
  AND b.project_id = $3
  AND b.id = $4
  AND b.lifecycle_generation = $7
  AND p.id = $3
  AND p.lifecycle_generation = $8
  AND c.revision = $1
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
RETURNING c.id, c.book_id, c.title, c."order", c.status, c.revision, c.published_revision, c.published_at, c.publish_error, c.deleted_at, c.purge_after, c.created_at, c.updated_at
		`, currentRevision, storedb.Timestamptz(input.PublishedAt), projectUUID, bookUUID, chapterUUID, vector.Chapter, vector.Book, vector.Project)
			chapter, err = scanLibraryChapter(row)
			if err != nil {
				return err
			}
		}
		if err := upsertBookSourceMember(ctx, tx, library.BookSourceMember{
			ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
			SourceView: governance.SourceViewPublished, BookID: bookUUID.String(),
			BookLifecycleGeneration: vector.Book, Title: bookTitle,
		}); err != nil {
			return err
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
				SourceView: governance.SourceViewPublished, BookID: bookUUID.String(),
				BookLifecycleGeneration: vector.Book, Title: bookTitle,
			},
			ChapterID: chapterUUID.String(), ChapterLifecycleGeneration: vector.Chapter,
			SourceRevision: uint64(currentRevision), Title: chapterTitle, Order: int(chapter.Order),
		}); err != nil {
			return err
		}
		if err := normalizePublishedChapterOrder(ctx, tx, projectUUID, vector.Project, bookUUID, vector.Book); err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("publish_chapter_after_graph_transition"); err != nil {
			return err
		}
		revision, err := createOrBumpPublishedSourceView(ctx, tx, projectUUID, vector.Project)
		if err != nil {
			return err
		}
		if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, ensureTextIndexIntent); err != nil {
			return err
		}
		return s.runDeletionFailpoint("publish_chapter_after_catalog_mutation")
	})
	return mapLibraryChapterWriteResult(chapter, err)
}

func loadChapterDraftSnapshot(ctx context.Context, tx pgx.Tx, chapterID pgtype.UUID, revision int64) ([]library.Paragraph, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, kind, "order", markdown, plain_text, word_count, created_at, updated_at
		FROM chapter_paragraphs
		WHERE chapter_id = $1 AND revision = $2 AND variant = 'draft'
		ORDER BY "order", id
	`, chapterID, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paragraphs := make([]library.Paragraph, 0)
	for rows.Next() {
		var paragraph library.Paragraph
		if err := rows.Scan(&paragraph.ID, &paragraph.Kind, &paragraph.Order, &paragraph.Markdown, &paragraph.PlainText, &paragraph.WordCount, &paragraph.CreatedAt, &paragraph.UpdatedAt); err != nil {
			return nil, err
		}
		paragraph.ChapterID = chapterID.String()
		paragraph.Revision = revision
		paragraph.Variant = library.ParagraphVariantDraft
		paragraphs = append(paragraphs, paragraph)
	}
	return paragraphs, rows.Err()
}
