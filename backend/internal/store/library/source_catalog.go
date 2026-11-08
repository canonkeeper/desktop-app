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

type retainedSourceProjection struct {
	Revision library.SourceCatalogRevision
	Books    []library.BookSourceMember
	Chapters []library.ChapterSourceMember
}

// CreateDraftProjectSourceView создаёт начальную draft-проекцию каталога
// внутри уже открытой транзакции создания проекта.
func (s *Store) CreateDraftProjectSourceView(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
) (library.SourceCatalogRevision, error) {
	return createDraftProjectSourceView(ctx, tx, projectID, projectGeneration)
}

// RestoreProjectSourceCatalog переносит сохранённую проекцию источников на
// восстановленное поколение проекта внутри текущей транзакции.
func (s *Store) RestoreProjectSourceCatalog(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
) error {
	retained, err := loadRetainedSourceViewsForRestore(ctx, tx, projectID)
	if err != nil {
		return err
	}
	revisions, err := replaceRestoredSourceProjection(ctx, tx, projectID, projectGeneration, retained)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, ensureTextIndexIntent); err != nil {
			return err
		}
	}
	return nil
}

func createDraftProjectSourceView(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
) (library.SourceCatalogRevision, error) {
	if err := lockCurrentCatalogProject(ctx, tx, projectID, projectGeneration); err != nil {
		return library.SourceCatalogRevision{}, err
	}
	row, err := sqlc.New(tx).CreateDraftProjectSourceView(ctx, sqlc.CreateDraftProjectSourceViewParams{
		ProjectID: projectID, ProjectLifecycleGeneration: projectGeneration,
	})
	if err != nil {
		return library.SourceCatalogRevision{}, err
	}
	return sourceCatalogRevision(row.ProjectID, row.ProjectLifecycleGeneration, row.SourceView, row.CatalogRevision)
}

func bumpProjectSourceView(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	view governance.SourceView,
) (library.SourceCatalogRevision, error) {
	if !validSourceCatalogView(view) {
		return library.SourceCatalogRevision{}, library.ErrValidation
	}
	if err := lockCurrentCatalogProject(ctx, tx, projectID, projectGeneration); err != nil {
		return library.SourceCatalogRevision{}, err
	}
	row, err := sqlc.New(tx).BumpProjectSourceView(ctx, sqlc.BumpProjectSourceViewParams{
		ProjectID: projectID, SourceView: string(view), ProjectLifecycleGeneration: projectGeneration,
	})
	if err != nil {
		return library.SourceCatalogRevision{}, err
	}
	return sourceCatalogRevision(row.ProjectID, row.ProjectLifecycleGeneration, row.SourceView, row.CatalogRevision)
}

func createOrBumpPublishedSourceView(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
) (library.SourceCatalogRevision, error) {
	if err := lockCurrentCatalogProject(ctx, tx, projectID, projectGeneration); err != nil {
		return library.SourceCatalogRevision{}, err
	}
	row, err := sqlc.New(tx).CreateOrBumpPublishedProjectSourceView(ctx, sqlc.CreateOrBumpPublishedProjectSourceViewParams{
		ProjectID: projectID, ProjectLifecycleGeneration: projectGeneration,
	})
	if err != nil {
		return library.SourceCatalogRevision{}, err
	}
	return sourceCatalogRevision(row.ProjectID, row.ProjectLifecycleGeneration, row.SourceView, row.CatalogRevision)
}

func bumpSourceViewsForBook(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, projectGeneration int64, bookID pgtype.UUID, bookGeneration int64) ([]library.SourceCatalogRevision, error) {
	return bumpSourceViewsForMember(ctx, tx, projectID, projectGeneration, bookID, bookGeneration, pgtype.UUID{})
}

func bumpSourceViewsForChapter(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, projectGeneration int64, bookID pgtype.UUID, bookGeneration int64, chapterID pgtype.UUID) ([]library.SourceCatalogRevision, error) {
	return bumpSourceViewsForMember(ctx, tx, projectID, projectGeneration, bookID, bookGeneration, chapterID)
}

func bumpSourceViewsForMember(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, projectGeneration int64, bookID pgtype.UUID, bookGeneration int64, chapterID pgtype.UUID) ([]library.SourceCatalogRevision, error) {
	query := `
		SELECT DISTINCT source_view
		FROM chapter_source_members
		WHERE project_id = $1 AND project_lifecycle_generation = $2
		  AND book_id = $3 AND book_lifecycle_generation = $4
		  AND ($5::uuid IS NULL OR chapter_id = $5)
		UNION
		SELECT source_view
		FROM book_source_members
		WHERE $5::uuid IS NULL
		  AND project_id = $1 AND project_lifecycle_generation = $2
		  AND book_id = $3 AND book_lifecycle_generation = $4
		ORDER BY source_view
	`
	var chapter any
	if chapterID.Valid {
		chapter = chapterID
	}
	rows, err := tx.Query(ctx, query, projectID, projectGeneration, bookID, bookGeneration, chapter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []governance.SourceView
	for rows.Next() {
		var view governance.SourceView
		if err := rows.Scan(&view); err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	revisions := make([]library.SourceCatalogRevision, 0, len(views))
	for _, view := range views {
		revision, err := bumpProjectSourceView(ctx, tx, projectID, projectGeneration, view)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, nil
}

func upsertBookSourceMember(ctx context.Context, tx pgx.Tx, member library.BookSourceMember) error {
	if tx == nil || member.Validate() != nil {
		return library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(member.ProjectID)
	if err != nil {
		return library.ErrValidation
	}
	bookID, err := storedb.ParseUUID(member.BookID)
	if err != nil {
		return library.ErrValidation
	}
	queries := sqlc.New(tx)
	if err := lockCurrentCatalogProject(ctx, tx, projectID, member.ProjectLifecycleGeneration); err != nil {
		return err
	}
	if _, err := queries.LockCurrentSourceBook(ctx, sqlc.LockCurrentSourceBookParams{
		ProjectID: projectID, BookID: bookID,
		BookLifecycleGeneration: member.BookLifecycleGeneration,
	}); err != nil {
		return mapSourceCatalogNotFound(err)
	}
	return queries.UpsertBookSourceMember(ctx, sqlc.UpsertBookSourceMemberParams{
		ProjectID: projectID, SourceView: string(member.SourceView),
		ProjectLifecycleGeneration: member.ProjectLifecycleGeneration,
		BookID:                     bookID, BookLifecycleGeneration: member.BookLifecycleGeneration, Title: member.Title,
	})
}

func upsertChapterSourceMember(ctx context.Context, tx pgx.Tx, member library.ChapterSourceMember) error {
	if tx == nil || member.Validate() != nil || member.SourceRevision > math.MaxInt64 || member.Order > math.MaxInt32 {
		return library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(member.ProjectID)
	if err != nil {
		return library.ErrValidation
	}
	bookID, err := storedb.ParseUUID(member.BookID)
	if err != nil {
		return library.ErrValidation
	}
	chapterID, err := storedb.ParseUUID(member.ChapterID)
	if err != nil {
		return library.ErrValidation
	}
	queries := sqlc.New(tx)
	if err := lockCurrentCatalogProject(ctx, tx, projectID, member.ProjectLifecycleGeneration); err != nil {
		return err
	}
	if _, err := queries.LockCurrentSourceBook(ctx, sqlc.LockCurrentSourceBookParams{
		ProjectID: projectID, BookID: bookID,
		BookLifecycleGeneration: member.BookLifecycleGeneration,
	}); err != nil {
		return mapSourceCatalogNotFound(err)
	}
	if _, err := queries.LockCurrentSourceChapter(ctx, sqlc.LockCurrentSourceChapterParams{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID,
		ChapterLifecycleGeneration: member.ChapterLifecycleGeneration,
	}); err != nil {
		return mapSourceCatalogNotFound(err)
	}
	if _, err := queries.LockBookSourceMember(ctx, sqlc.LockBookSourceMemberParams{
		ProjectID: projectID, SourceView: string(member.SourceView),
		ProjectLifecycleGeneration: member.ProjectLifecycleGeneration,
		BookID:                     bookID, BookLifecycleGeneration: member.BookLifecycleGeneration,
	}); err != nil {
		return mapSourceCatalogNotFound(err)
	}
	return queries.UpsertChapterSourceMember(ctx, sqlc.UpsertChapterSourceMemberParams{
		ProjectID: projectID, SourceView: string(member.SourceView),
		ProjectLifecycleGeneration: member.ProjectLifecycleGeneration,
		BookID:                     bookID, BookLifecycleGeneration: member.BookLifecycleGeneration,
		ChapterID: chapterID, ChapterLifecycleGeneration: member.ChapterLifecycleGeneration,
		SourceRevision: int64(member.SourceRevision), Title: member.Title, ChapterOrder: int32(member.Order),
	})
}

func normalizePublishedChapterOrder(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	bookID pgtype.UUID,
	bookGeneration int64,
) error {
	if tx == nil || projectGeneration < 1 || bookGeneration < 1 {
		return library.ErrValidation
	}
	queries := sqlc.New(tx)
	if err := lockCurrentCatalogProject(ctx, tx, projectID, projectGeneration); err != nil {
		return err
	}
	if _, err := queries.LockCurrentSourceBook(ctx, sqlc.LockCurrentSourceBookParams{
		ProjectID: projectID, BookID: bookID,
		BookLifecycleGeneration: bookGeneration,
	}); err != nil {
		return mapSourceCatalogNotFound(err)
	}
	if _, err := queries.LockCurrentSourceChaptersForBook(ctx, sqlc.LockCurrentSourceChaptersForBookParams{
		ProjectID: projectID, BookID: bookID,
	}); err != nil {
		return err
	}
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if _, err := queries.LockBookSourceMember(ctx, sqlc.LockBookSourceMemberParams{
			ProjectID: projectID, SourceView: string(view), ProjectLifecycleGeneration: projectGeneration,
			BookID: bookID, BookLifecycleGeneration: bookGeneration,
		}); err != nil {
			return mapSourceCatalogNotFound(err)
		}
	}
	return queries.NormalizePublishedChapterOrder(ctx, sqlc.NormalizePublishedChapterOrderParams{
		ProjectID: projectID, ProjectLifecycleGeneration: projectGeneration,
		BookID: bookID, BookLifecycleGeneration: bookGeneration,
	})
}

func loadRetainedSourceViewsForRestore(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) ([]retainedSourceProjection, error) {
	if tx == nil || !projectID.Valid {
		return nil, library.ErrValidation
	}
	queries := sqlc.New(tx)
	viewRows, err := queries.ListRetainedProjectSourceViews(ctx, projectID)
	if err != nil {
		return nil, err
	}
	bookRows, err := queries.ListRetainedBookSourceMembers(ctx, projectID)
	if err != nil {
		return nil, err
	}
	chapterRows, err := queries.ListRetainedChapterSourceMembers(ctx, projectID)
	if err != nil {
		return nil, err
	}
	projections := make([]retainedSourceProjection, 0, len(viewRows))
	byView := make(map[governance.SourceView]*retainedSourceProjection, len(viewRows))
	for _, row := range viewRows {
		revision, err := sourceCatalogRevision(row.ProjectID, row.ProjectLifecycleGeneration, row.SourceView, row.CatalogRevision)
		if err != nil {
			return nil, err
		}
		projections = append(projections, retainedSourceProjection{Revision: revision})
		byView[revision.SourceView] = &projections[len(projections)-1]
	}
	for _, row := range bookRows {
		view := governance.SourceView(row.SourceView)
		projection := byView[view]
		if projection == nil {
			return nil, library.ErrValidation
		}
		member := library.BookSourceMember{
			ProjectID: storedb.UUIDString(row.ProjectID), ProjectLifecycleGeneration: row.ProjectLifecycleGeneration,
			SourceView: view, BookID: storedb.UUIDString(row.BookID),
			BookLifecycleGeneration: row.BookLifecycleGeneration, Title: row.Title,
		}
		if member.Validate() != nil {
			return nil, library.ErrValidation
		}
		projection.Books = append(projection.Books, member)
	}
	for _, row := range chapterRows {
		view := governance.SourceView(row.SourceView)
		projection := byView[view]
		if projection == nil || row.SourceRevision < 1 {
			return nil, library.ErrValidation
		}
		member := library.ChapterSourceMember{
			BookSourceMember: library.BookSourceMember{
				ProjectID: storedb.UUIDString(row.ProjectID), ProjectLifecycleGeneration: row.ProjectLifecycleGeneration,
				SourceView: view, BookID: storedb.UUIDString(row.BookID),
				BookLifecycleGeneration: row.BookLifecycleGeneration,
			},
			ChapterID: storedb.UUIDString(row.ChapterID), ChapterLifecycleGeneration: row.ChapterLifecycleGeneration,
			SourceRevision: uint64(row.SourceRevision), Title: row.Title, Order: int(row.ChapterOrder),
		}
		for _, book := range projection.Books {
			if book.BookID == member.BookID {
				member.BookSourceMember.Title = book.Title
				break
			}
		}
		if member.Validate() != nil {
			return nil, library.ErrValidation
		}
		projection.Chapters = append(projection.Chapters, member)
	}
	return projections, nil
}

func replaceRestoredSourceProjection(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	retained []retainedSourceProjection,
) ([]library.SourceCatalogRevision, error) {
	if tx == nil || projectGeneration < 1 {
		return nil, library.ErrValidation
	}
	queries := sqlc.New(tx)
	if err := lockCurrentCatalogProject(ctx, tx, projectID, projectGeneration); err != nil {
		return nil, err
	}
	books, err := queries.LockLiveSourceBooksForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	chapters, err := queries.LockLiveSourceChaptersForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	retainedByView := make(map[governance.SourceView]retainedSourceProjection, len(retained))
	for _, projection := range retained {
		if projection.Revision.Validate() != nil {
			return nil, library.ErrValidation
		}
		retainedByView[projection.Revision.SourceView] = projection
	}
	if err := queries.DeleteRetainedProjectSourceViews(ctx, projectID); err != nil {
		return nil, err
	}

	views := []governance.SourceView{governance.SourceViewDraft}
	_, retainedPublished := retainedByView[governance.SourceViewPublished]
	if !retainedPublished {
		for _, chapter := range chapters {
			if chapter.PublishedRevision.Valid {
				retainedPublished = true
				break
			}
		}
	}
	if retainedPublished {
		views = append(views, governance.SourceViewPublished)
	}

	revisions := make([]library.SourceCatalogRevision, 0, len(views))
	for _, view := range views {
		previousSnapshot := retainedByView[view]
		catalogRevision := uint64(1)
		if previousSnapshot.Revision.CatalogRevision > 0 {
			catalogRevision = previousSnapshot.Revision.CatalogRevision + 1
		}
		if catalogRevision > math.MaxInt64 {
			return nil, library.ErrValidation
		}
		row, err := queries.CreateRestoredProjectSourceView(ctx, sqlc.CreateRestoredProjectSourceViewParams{
			ProjectID: projectID, SourceView: string(view), ProjectLifecycleGeneration: projectGeneration,
			CatalogRevision: int64(catalogRevision),
		})
		if err != nil {
			return nil, err
		}
		revision, err := sourceCatalogRevision(row.ProjectID, row.ProjectLifecycleGeneration, row.SourceView, row.CatalogRevision)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
		if err := restoreSourceProjectionMembers(ctx, queries, revision, previousSnapshot, books, chapters); err != nil {
			return nil, err
		}
	}
	return revisions, nil
}

func restoreSourceProjectionMembers(
	ctx context.Context,
	queries *sqlc.Queries,
	revision library.SourceCatalogRevision,
	retained retainedSourceProjection,
	books []sqlc.LockLiveSourceBooksForProjectRow,
	chapters []sqlc.LockLiveSourceChaptersForProjectRow,
) error {
	projectID, err := storedb.ParseUUID(revision.ProjectID)
	if err != nil {
		return library.ErrValidation
	}
	retainedBooks := make(map[string]library.BookSourceMember, len(retained.Books))
	for _, book := range retained.Books {
		retainedBooks[book.BookID] = book
	}
	retainedChapters := make(map[string]library.ChapterSourceMember, len(retained.Chapters))
	for _, chapter := range retained.Chapters {
		retainedChapters[chapter.ChapterID] = chapter
	}
	currentBooks := make(map[string]sqlc.LockLiveSourceBooksForProjectRow, len(books))
	for _, book := range books {
		currentBooks[book.ID.String()] = book
	}

	selectedChapters := make([]library.ChapterSourceMember, 0, len(chapters)+len(retained.Chapters))
	selectedChapterIDs := make(map[string]struct{}, len(chapters))
	publishedBookIDs := make(map[string]struct{})
	for _, source := range chapters {
		book, ok := currentBooks[source.BookID.String()]
		if !ok {
			continue
		}
		member, retainedMember := retainedChapters[source.ID.String()]
		if revision.SourceView == governance.SourceViewPublished && !retainedMember && !source.PublishedRevision.Valid {
			continue
		}
		if !retainedMember {
			sourceRevision := source.Revision
			if revision.SourceView == governance.SourceViewPublished {
				sourceRevision = source.PublishedRevision.Int64
			}
			member = library.ChapterSourceMember{
				ChapterID: source.ID.String(), SourceRevision: uint64(sourceRevision),
				Title: source.Title, Order: int(source.ChapterOrder),
			}
		}
		member.ProjectID = revision.ProjectID
		member.ProjectLifecycleGeneration = revision.ProjectLifecycleGeneration
		member.SourceView = revision.SourceView
		member.BookID = source.BookID.String()
		member.BookLifecycleGeneration = book.LifecycleGeneration
		member.ChapterLifecycleGeneration = source.LifecycleGeneration
		selectedChapters = append(selectedChapters, member)
		selectedChapterIDs[member.ChapterID] = struct{}{}
		publishedBookIDs[member.BookID] = struct{}{}
	}
	for _, member := range retained.Chapters {
		if _, selected := selectedChapterIDs[member.ChapterID]; selected {
			continue
		}
		if _, ok := retainedBooks[member.BookID]; !ok {
			continue
		}
		member.ProjectID = revision.ProjectID
		member.ProjectLifecycleGeneration = revision.ProjectLifecycleGeneration
		member.SourceView = revision.SourceView
		if book, ok := currentBooks[member.BookID]; ok {
			member.BookLifecycleGeneration = book.LifecycleGeneration
		}
		selectedChapters = append(selectedChapters, member)
		publishedBookIDs[member.BookID] = struct{}{}
	}

	selectedBookIDs := make(map[string]struct{}, len(books))
	for _, source := range books {
		if revision.SourceView == governance.SourceViewPublished {
			if _, ok := publishedBookIDs[source.ID.String()]; !ok {
				continue
			}
		}
		book := retainedBooks[source.ID.String()]
		title := source.Title
		if book.Title != "" {
			title = book.Title
		}
		if err := queries.UpsertBookSourceMember(ctx, sqlc.UpsertBookSourceMemberParams{
			ProjectID: projectID, SourceView: string(revision.SourceView),
			ProjectLifecycleGeneration: revision.ProjectLifecycleGeneration,
			BookID:                     source.ID, BookLifecycleGeneration: source.LifecycleGeneration, Title: title,
		}); err != nil {
			return err
		}
		selectedBookIDs[source.ID.String()] = struct{}{}
	}
	for _, member := range retained.Books {
		if _, selected := selectedBookIDs[member.BookID]; selected {
			continue
		}
		if revision.SourceView == governance.SourceViewPublished {
			if _, ok := publishedBookIDs[member.BookID]; !ok {
				continue
			}
		}
		member.ProjectID = revision.ProjectID
		member.ProjectLifecycleGeneration = revision.ProjectLifecycleGeneration
		member.SourceView = revision.SourceView
		if member.Validate() != nil {
			return library.ErrValidation
		}
		bookID, err := storedb.ParseUUID(member.BookID)
		if err != nil {
			return library.ErrValidation
		}
		if err := queries.UpsertBookSourceMember(ctx, sqlc.UpsertBookSourceMemberParams{
			ProjectID: projectID, SourceView: string(revision.SourceView),
			ProjectLifecycleGeneration: revision.ProjectLifecycleGeneration,
			BookID:                     bookID, BookLifecycleGeneration: member.BookLifecycleGeneration, Title: member.Title,
		}); err != nil {
			return err
		}
	}
	for _, member := range selectedChapters {
		if member.Validate() != nil || member.SourceRevision > math.MaxInt64 {
			return library.ErrValidation
		}
		bookID, err := storedb.ParseUUID(member.BookID)
		if err != nil {
			return library.ErrValidation
		}
		chapterID, err := storedb.ParseUUID(member.ChapterID)
		if err != nil {
			return library.ErrValidation
		}
		if err := queries.UpsertChapterSourceMember(ctx, sqlc.UpsertChapterSourceMemberParams{
			ProjectID: projectID, SourceView: string(member.SourceView),
			ProjectLifecycleGeneration: member.ProjectLifecycleGeneration,
			BookID:                     bookID, BookLifecycleGeneration: member.BookLifecycleGeneration,
			ChapterID: chapterID, ChapterLifecycleGeneration: member.ChapterLifecycleGeneration,
			SourceRevision: int64(member.SourceRevision), Title: member.Title, ChapterOrder: int32(member.Order),
		}); err != nil {
			return err
		}
	}
	return nil
}

func lockCurrentCatalogProject(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, projectGeneration int64) error {
	if tx == nil || !projectID.Valid || projectGeneration < 1 {
		return library.ErrValidation
	}
	row, err := sqlc.New(tx).LockCurrentSourceProject(ctx, sqlc.LockCurrentSourceProjectParams{
		ProjectID:                  projectID,
		ProjectLifecycleGeneration: projectGeneration,
	})
	if err != nil {
		return mapSourceCatalogNotFound(err)
	}
	if row.LifecycleGeneration != projectGeneration {
		return library.ErrNotFound
	}
	return nil
}

func sourceCatalogRevision(projectID pgtype.UUID, projectGeneration int64, view string, catalogRevision int64) (library.SourceCatalogRevision, error) {
	if catalogRevision < 1 {
		return library.SourceCatalogRevision{}, library.ErrValidation
	}
	revision := library.SourceCatalogRevision{
		ProjectID: storedb.UUIDString(projectID), ProjectLifecycleGeneration: projectGeneration,
		SourceView: governance.SourceView(view), CatalogRevision: uint64(catalogRevision),
	}
	if revision.Validate() != nil {
		return library.SourceCatalogRevision{}, library.ErrValidation
	}
	return revision, nil
}

func mapSourceCatalogNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ErrNotFound
	}
	return err
}

func validSourceCatalogView(view governance.SourceView) bool {
	return view == governance.SourceViewDraft || view == governance.SourceViewPublished
}
