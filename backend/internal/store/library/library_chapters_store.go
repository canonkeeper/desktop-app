package librarystore

import (
	"context"
	"errors"
	"math"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateChapterWithDraft(ctx context.Context, input library.CreateChapterWithDraftStoreInput) (library.Chapter, error) {
	chapterID, err := storedb.ParseUUID(input.ID)
	if err != nil {
		return library.Chapter{}, err
	}
	bookUUID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	var chapter sqlc.Chapter
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var bookTitle string
		vector, err := lockCurrentBookSource(ctx, tx, projectUUID, bookUUID)
		if err != nil {
			return err
		}
		var order int
		if input.AfterChapterID.Present {
			order, err = chapterInsertionOrder(ctx, tx, projectUUID, bookUUID, input.AfterChapterID)
			if err != nil {
				return err
			}
			if err := shiftChaptersForInsert(ctx, tx, projectUUID, bookUUID, order, input.CreatedAt); err != nil {
				return err
			}
		} else if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX("order"), 0) + 1
			FROM chapters
			WHERE project_id = $1 AND book_id = $2 AND deleted_at IS NULL
		`, projectUUID, bookUUID).Scan(&order); err != nil {
			return err
		}
		chapter, err = scanLibraryChapter(tx.QueryRow(ctx, `
			INSERT INTO chapters (
				id, book_id, project_id, title, "order",
				lifecycle_generation, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, 1, $6, $6)
			RETURNING id, book_id, title, "order", status, revision,
			          published_revision, published_at, publish_error,
			          deleted_at, purge_after, created_at, updated_at
		`, chapterID, bookUUID, projectUUID, input.Title, order, input.CreatedAt))
		if err != nil {
			return err
		}
		if err := createLibraryParagraphs(ctx, tx, projectUUID, bookUUID, vector.Project, vector.Book, chapterParagraphs{generation: 1, paragraphs: input.Paragraphs}); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT title FROM books WHERE project_id = $1 AND id = $2`, projectUUID, bookUUID).Scan(&bookTitle); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE chapter_source_members AS member
			SET chapter_order = chapter."order"
			FROM chapters AS chapter
			WHERE member.project_id = $1
			  AND member.source_view = 'draft'
			  AND member.project_lifecycle_generation = $2
			  AND member.book_id = $3
			  AND member.book_lifecycle_generation = $4
			  AND chapter.id = member.chapter_id
			  AND chapter.project_id = member.project_id
			  AND chapter.book_id = member.book_id
			  AND chapter.lifecycle_generation = member.chapter_lifecycle_generation
			  AND chapter.deleted_at IS NULL
		`, projectUUID, vector.Project, bookUUID, vector.Book); err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("create_chapter_after_sibling_catalog_sync"); err != nil {
			return err
		}
		if err := upsertChapterSourceMember(ctx, tx, library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: projectUUID.String(), ProjectLifecycleGeneration: vector.Project,
				SourceView: governance.SourceViewDraft, BookID: bookUUID.String(),
				BookLifecycleGeneration: vector.Book, Title: bookTitle,
			},
			ChapterID: chapterID.String(), ChapterLifecycleGeneration: 1,
			SourceRevision: 1, Title: chapter.Title, Order: int(chapter.Order),
		}); err != nil {
			return err
		}
		revision, err := bumpProjectSourceView(ctx, tx, projectUUID, vector.Project, governance.SourceViewDraft)
		if err != nil {
			return err
		}
		if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, ensureTextIndexIntent); err != nil {
			return err
		}
		return s.runDeletionFailpoint("create_chapter_after_catalog_mutation")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.Chapter{}, library.ErrNotFound
	}
	if isUniqueViolation(err) {
		return library.Chapter{}, library.ErrConflict
	}
	if isForeignKeyViolation(err) {
		return library.Chapter{}, library.ErrNotFound
	}
	if isCheckViolation(err) {
		return library.Chapter{}, library.ErrValidation
	}
	if err != nil {
		return library.Chapter{}, err
	}
	return libraryChapterFromSQL(chapter), nil
}

func (s *Store) ListChapters(ctx context.Context, input library.ListChaptersStoreInput) ([]library.Chapter, error) {
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	state := input.State
	if state == "" {
		state = library.SourceListActive
	}
	after, err := listPositionArgs(input.After)
	if err != nil {
		return nil, err
	}
	if state == library.SourceListDeleted {
		return s.listDeletedChapters(ctx, input, bookUUID, after)
	}
	if state != library.SourceListActive {
		return nil, library.ErrValidation
	}
	rows, err := sqlc.New(s.Pool).ListChaptersByBook(ctx, sqlc.ListChaptersByBookParams{
		ProjectID:   projectUUID,
		BookID:      bookUUID,
		HasCursor:   after.has,
		CursorOrder: after.order,
		CursorID:    after.id,
		LimitCount:  int32(input.Limit),
	})
	if err != nil {
		return nil, err
	}
	chapters := make([]library.Chapter, 0, len(rows))
	for _, row := range rows {
		chapters = append(chapters, libraryChapterFromSQL(row))
	}
	return chapters, nil
}

func (s *Store) listDeletedChapters(ctx context.Context, input library.ListChaptersStoreInput, bookID pgtype.UUID, after listPositionArgsValue) ([]library.Chapter, error) {
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	var chapters []library.Chapter
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT chapter.id, chapter.book_id, chapter.title, chapter."order",
			       chapter.status, chapter.revision, chapter.published_revision,
			       chapter.published_at, chapter.publish_error,
			       chapter.deleted_at, chapter.purge_after,
			       chapter.created_at, chapter.updated_at
			FROM chapters AS chapter
			JOIN books AS book
			  ON book.id = chapter.book_id AND book.project_id = chapter.project_id
			JOIN projects AS project ON project.id = book.project_id
			JOIN project_memberships AS membership ON membership.project_id = project.id
			WHERE chapter.project_id = $2
			  AND chapter.book_id = $3
			  AND membership.user_id = $1
			  AND membership.removed_at IS NULL
			  AND membership.role IN ('editor', 'admin', 'owner')
			  AND project.deleted_at IS NULL
			  AND book.deleted_at IS NULL
			  AND chapter.deleted_at IS NOT NULL
			  AND chapter.governance_retention_started_at IS NOT NULL
			  AND chapter.purge_after IS NOT NULL
			  AND chapter.hard_purge_started_at IS NULL
			  AND clock_timestamp() < chapter.purge_after
			  AND (NOT $5::boolean OR (chapter.deleted_at, chapter.id) < ($6::timestamptz, $7::uuid))
			ORDER BY chapter.deleted_at DESC, chapter.id DESC
			LIMIT $4
		`, actorUserID, projectID, bookID, input.Limit, after.has, after.deletedAt, after.id)
		if err != nil {
			return err
		}
		defer rows.Close()
		chapters = make([]library.Chapter, 0)
		for rows.Next() {
			var row sqlc.Chapter
			if err := rows.Scan(
				&row.ID, &row.BookID, &row.Title, &row.Order,
				&row.Status, &row.Revision, &row.PublishedRevision,
				&row.PublishedAt, &row.PublishError,
				&row.DeletedAt, &row.PurgeAfter,
				&row.CreatedAt, &row.UpdatedAt,
			); err != nil {
				return err
			}
			chapter := libraryChapterFromSQL(row)
			deletedAt := row.DeletedAt.Time
			restoreUntil := row.PurgeAfter.Time
			chapter.DeletedAt = &deletedAt
			chapter.RestoreUntil = &restoreUntil
			chapters = append(chapters, chapter)
		}
		return rows.Err()
	})
	return chapters, err
}

func (s *Store) GetChapter(ctx context.Context, projectID, bookID, chapterID string) (library.ChapterRecord, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.ChapterRecord{}, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return library.ChapterRecord{}, library.ErrNotFound
	}
	chapterUUID, err := storedb.ParseUUID(chapterID)
	if err != nil {
		return library.ChapterRecord{}, library.ErrNotFound
	}
	row, err := sqlc.New(s.Pool).GetChapterWithNavigationByID(ctx, sqlc.GetChapterWithNavigationByIDParams{
		ProjectID: projectUUID,
		BookID:    bookUUID,
		ChapterID: chapterUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ChapterRecord{}, library.ErrNotFound
	}
	if err != nil {
		return library.ChapterRecord{}, err
	}
	navigation := library.ChapterNavigation{Position: int(row.Position), Total: int(row.Total)}
	if row.PreviousID.Valid {
		navigation.Previous = &library.ChapterNavigationItem{
			ID:            storedb.UUIDString(row.PreviousID),
			Title:         row.PreviousTitle,
			DisplayNumber: library.ChapterDisplayNumber(int(row.PreviousOrder), row.PreviousTitle),
		}
	}
	if row.NextID.Valid {
		navigation.Next = &library.ChapterNavigationItem{
			ID:            storedb.UUIDString(row.NextID),
			Title:         row.NextTitle,
			DisplayNumber: library.ChapterDisplayNumber(int(row.NextOrder), row.NextTitle),
		}
	}
	return library.ChapterRecord{
		Chapter:    libraryChapterFromSQL(row.Chapter),
		ProjectID:  storedb.UUIDString(row.Chapter.ProjectID),
		Navigation: navigation,
	}, nil
}

func (s *Store) ListChapterParagraphs(ctx context.Context, input library.ListChapterParagraphsStoreInput) ([]library.Paragraph, error) {
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	chapterUUID, err := storedb.ParseUUID(input.ChapterID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	rows, err := sqlc.New(s.Pool).ListChapterParagraphsByChapterRevisionVariant(ctx, sqlc.ListChapterParagraphsByChapterRevisionVariantParams{
		ProjectID: projectUUID,
		BookID:    bookUUID,
		ChapterID: chapterUUID,
		Revision:  input.Revision,
		Variant:   string(input.Variant),
	})
	if err != nil {
		return nil, err
	}
	paragraphs := make([]library.Paragraph, 0, len(rows))
	for _, row := range rows {
		paragraphs = append(paragraphs, libraryParagraphFromSQL(row))
	}
	return paragraphs, nil
}

// listPositionArgsValue is a page position as query arguments: no position reads from the start.
type listPositionArgsValue struct {
	has       bool
	order     int32
	deletedAt pgtype.Timestamptz
	id        pgtype.UUID
}

func listPositionArgs(position *library.ListPosition) (listPositionArgsValue, error) {
	if position == nil {
		return listPositionArgsValue{}, nil
	}
	id, err := storedb.ParseUUID(position.ID)
	if err != nil || position.Order < math.MinInt32 || position.Order > math.MaxInt32 {
		return listPositionArgsValue{}, library.ErrValidation
	}
	return listPositionArgsValue{
		has:       true,
		order:     int32(position.Order),
		deletedAt: pgtype.Timestamptz{Time: position.DeletedAt, Valid: true},
		id:        id,
	}, nil
}
