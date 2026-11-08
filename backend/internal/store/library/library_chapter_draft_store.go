package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) SaveChapterDraft(ctx context.Context, input library.SaveChapterDraftStoreInput) (library.Chapter, error) {
	var chapter library.Chapter
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		chapter, err = s.saveChapterDraftTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return library.Chapter{}, err
	}
	return chapter, nil
}

func (s *Store) saveChapterDraftTx(
	ctx context.Context,
	tx pgx.Tx,
	input library.SaveChapterDraftStoreInput,
) (library.Chapter, error) {
	if input.ExpectedRevision < 1 {
		return library.Chapter{}, library.ErrValidation
	}
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
	vector, err := lockCurrentChapterSource(ctx, tx, projectUUID, bookUUID, chapterUUID)
	if err != nil {
		return library.Chapter{}, err
	}
	if err := s.runDeletionFailpoint("save_chapter_after_source_lock"); err != nil {
		return library.Chapter{}, err
	}
	var sourceRevision int64
	var sourceTitle string
	err = tx.QueryRow(ctx, `
			SELECT source_revision, title
			FROM chapter_source_members
			WHERE project_id = $1 AND source_view = 'draft'
			  AND project_lifecycle_generation = $2
			  AND book_id = $3 AND book_lifecycle_generation = $4
			  AND chapter_id = $5 AND chapter_lifecycle_generation = $6
			FOR UPDATE
	`, projectUUID, vector.Project, bookUUID, vector.Book, chapterUUID, vector.Chapter).Scan(&sourceRevision, &sourceTitle)
	if err != nil {
		return library.Chapter{}, mapSourceCatalogNotFound(err)
	}
	return s.saveChapterDraftAfterSourceLock(
		ctx, tx, input, projectUUID, bookUUID, chapterUUID, vector, sourceRevision, sourceTitle,
	)
}

// saveChapterDraftAfterSourceLock переиспользует ровно прежнюю запись главы,
// когда вызывающий код уже удерживает source locks в обязательном порядке.
func (s *Store) saveChapterDraftAfterSourceLock(
	ctx context.Context,
	tx pgx.Tx,
	input library.SaveChapterDraftStoreInput,
	projectUUID, bookUUID, chapterUUID pgtype.UUID,
	vector lockedLibrarySourceVector,
	sourceRevision int64,
	sourceTitle string,
) (library.Chapter, error) {
	unchanged, err := draftParagraphSnapshotMatches(ctx, tx, chapterUUID, sourceRevision, input.Paragraphs)
	if err != nil {
		return library.Chapter{}, err
	}
	paragraphsChanged := !unchanged
	var chapter sqlc.Chapter
	if _, err := tx.Exec(ctx, `
DELETE FROM chapter_paragraphs AS paragraph
USING chapters AS chapter, books AS book, projects AS project
WHERE paragraph.chapter_id = $1
  AND paragraph.revision = $2
  AND paragraph.variant = 'draft'
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
`, chapterUUID, input.Revision, projectUUID, bookUUID, vector.Chapter, vector.Book, vector.Project); err != nil {
		return library.Chapter{}, err
	}
	queries := sqlc.New(tx)
	if err := createLibraryParagraphs(ctx, queries, projectUUID, bookUUID, vector.Project, vector.Book, vector.Chapter, input.Paragraphs); err != nil {
		return library.Chapter{}, err
	}
	row := tx.QueryRow(ctx, `
UPDATE chapters c
SET title = $1,
    revision = $2,
    status = $3,
    updated_at = $4
FROM books b
JOIN projects p ON p.id = b.project_id
WHERE c.project_id = $5
  AND c.book_id = $6
  AND c.id = $7
  AND c.lifecycle_generation = $8
  AND b.project_id = $5
  AND b.id = $6
  AND b.lifecycle_generation = $9
  AND p.id = $5
  AND p.lifecycle_generation = $10
  AND c.revision = $11
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
RETURNING c.id, c.book_id, c.title, c."order", c.status, c.revision, c.published_revision, c.published_at, c.publish_error, c.deleted_at, c.purge_after, c.created_at, c.updated_at
`, input.Title, input.Revision, string(input.Status), storedb.Timestamptz(input.UpdatedAt), projectUUID, bookUUID, chapterUUID, vector.Chapter, vector.Book, vector.Project, input.ExpectedRevision)
	chapter, err = scanLibraryChapter(row)
	if errors.Is(err, pgx.ErrNoRows) {
		exists, existsErr := libraryChapterExistsForDraftSave(ctx, tx, projectUUID, bookUUID, chapterUUID)
		if existsErr != nil {
			return library.Chapter{}, existsErr
		}
		if exists {
			return library.Chapter{}, library.ErrConflict
		}
	}
	if err != nil || (!paragraphsChanged && sourceTitle == input.Title) {
		return mapLibraryChapterWriteResult(chapter, err)
	}
	var bookTitle string
	if err := tx.QueryRow(ctx, `SELECT title FROM books WHERE project_id = $1 AND id = $2`, projectUUID, bookUUID).Scan(&bookTitle); err != nil {
		return library.Chapter{}, err
	}
	if paragraphsChanged {
		sourceRevision = input.Revision
	}
	if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
		BookSourceMember: library.BookSourceMember{
			ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
			SourceView: governance.SourceViewDraft, BookID: bookUUID.String(),
			BookLifecycleGeneration: vector.Book, Title: bookTitle,
		},
		ChapterID: chapterUUID.String(), ChapterLifecycleGeneration: vector.Chapter,
		SourceRevision: uint64(sourceRevision), Title: input.Title, Order: int(chapter.Order),
	}); err != nil {
		return library.Chapter{}, err
	}
	if err := s.runDeletionFailpoint("save_chapter_after_graph_transition"); err != nil {
		return library.Chapter{}, err
	}
	revision, err := bumpProjectSourceView(ctx, tx, projectUUID, vector.Project, governance.SourceViewDraft)
	if err != nil {
		return library.Chapter{}, err
	}
	mode := refreshTextIndexIntent
	if paragraphsChanged {
		mode = ensureTextIndexIntent
	}
	if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, mode); err != nil {
		return library.Chapter{}, err
	}
	if err := s.runDeletionFailpoint("save_chapter_after_catalog_mutation"); err != nil {
		return library.Chapter{}, err
	}
	return mapLibraryChapterWriteResult(chapter, err)
}

func draftParagraphSnapshotMatches(ctx context.Context, tx pgx.Tx, chapterID pgtype.UUID, revision int64, paragraphs []library.Paragraph) (bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, kind, "order", markdown, plain_text
		FROM chapter_paragraphs
		WHERE chapter_id = $1 AND revision = $2 AND variant = 'draft'
		ORDER BY "order", id
	`, chapterID, revision)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(paragraphs) {
			return false, nil
		}
		var id, kind, markdown, plainText string
		var order int
		if err := rows.Scan(&id, &kind, &order, &markdown, &plainText); err != nil {
			return false, err
		}
		paragraph := paragraphs[index]
		if id != paragraph.ID || kind != string(paragraph.Kind) || order != paragraph.Order || markdown != paragraph.Markdown || plainText != paragraph.PlainText {
			return false, nil
		}
		index++
	}
	return index == len(paragraphs), rows.Err()
}

func libraryChapterExistsForDraftSave(ctx context.Context, tx pgx.Tx, projectID, bookID, chapterID pgtype.UUID) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM chapters c
  JOIN books b ON b.id = c.book_id
  JOIN projects p ON p.id = b.project_id
  WHERE c.project_id = $1
    AND c.book_id = $2
    AND c.id = $3
    AND c.deleted_at IS NULL
    AND b.deleted_at IS NULL
    AND p.deleted_at IS NULL
)
`, projectID, bookID, chapterID).Scan(&exists)
	return exists, err
}

type lockedLibrarySourceVector struct {
	Project int64
	Book    int64
	Chapter int64
}

func lockCurrentChapterSource(
	ctx context.Context,
	tx pgx.Tx,
	projectID, bookID, chapterID pgtype.UUID,
) (lockedLibrarySourceVector, error) {
	var vector lockedLibrarySourceVector
	if err := tx.QueryRow(ctx, `
SELECT lifecycle_generation
FROM projects
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE
`, projectID).Scan(&vector.Project); err != nil {
		return lockedLibrarySourceVector{}, mapLibrarySourceLockError(err)
	}
	if err := tx.QueryRow(ctx, `
SELECT lifecycle_generation
FROM books
WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL
FOR UPDATE
`, projectID, bookID).Scan(&vector.Book); err != nil {
		return lockedLibrarySourceVector{}, mapLibrarySourceLockError(err)
	}
	if err := tx.QueryRow(ctx, `
SELECT lifecycle_generation
FROM chapters
WHERE project_id = $1 AND book_id = $2 AND id = $3 AND deleted_at IS NULL
FOR UPDATE
`, projectID, bookID, chapterID).Scan(&vector.Chapter); err != nil {
		return lockedLibrarySourceVector{}, mapLibrarySourceLockError(err)
	}
	return vector, nil
}

func mapLibrarySourceLockError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ErrNotFound
	}
	return err
}
