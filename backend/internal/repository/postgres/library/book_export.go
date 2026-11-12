package library

import (
	"context"
	"errors"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// BookHead reads a book for a member of its project: anyone else does not see the book at all.
func (repository *Repository) BookHead(ctx context.Context, actor domainlibrary.Actor, bookID string) (domainlibrary.BookHead, error) {
	actorID, err := storedb.ParseUUID(actor.UserID)
	if err != nil {
		return domainlibrary.BookHead{}, domainlibrary.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return domainlibrary.BookHead{}, domainlibrary.ErrNotFound
	}
	row, err := sqlc.New(repository.db.Pool).GetBookHeadForActor(ctx, sqlc.GetBookHeadForActorParams{ActorUserID: actorID, BookID: bookUUID})
	if errors.Is(err, pgx.ErrNoRows) {
		return domainlibrary.BookHead{}, domainlibrary.ErrNotFound
	}
	if err != nil {
		return domainlibrary.BookHead{}, err
	}
	return domainlibrary.BookHead{
		ProjectID: row.ProjectID.String(), Title: row.Title, AuthorName: row.OwnerName,
		CanEdit: canEdit(row.Role), CoverVersion: row.CoverVersion,
	}, nil
}

// BookExportChapters lists the chapters with text in the published version, in the order of the book.
func (repository *Repository) BookExportChapters(ctx context.Context, projectID, bookID string) ([]domainlibrary.ExportChapter, error) {
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	return repository.exportChapters(ctx, projectID, bookUUID)
}

// ProjectExportChapters lists the chapters with text in the published version of the living books of a saga, in the
// order of the shelf (specs/2026-11-12-ux10-book-pictures.md BP3.2).
func (repository *Repository) ProjectExportChapters(ctx context.Context, projectID string) ([]domainlibrary.ExportChapter, error) {
	return repository.exportChapters(ctx, projectID, pgtype.UUID{})
}

func (repository *Repository) exportChapters(ctx context.Context, projectID string, book pgtype.UUID) ([]domainlibrary.ExportChapter, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	rows, err := sqlc.New(repository.db.Pool).ListBookExportChapters(ctx, sqlc.ListBookExportChaptersParams{ProjectID: projectUUID, BookID: book})
	if err != nil {
		return nil, err
	}
	chapters := make([]domainlibrary.ExportChapter, len(rows))
	for index, row := range rows {
		chapters[index] = domainlibrary.ExportChapter{
			ID: row.ID.String(), BookID: row.BookID.String(), Title: row.Title, Revision: row.PublishedRevision, Paragraphs: int(row.Paragraphs),
		}
	}
	return chapters, nil
}

// ProjectCanEdit is the right of an actor to take the books of a saga away as files: a saga he is not in is not found.
func (repository *Repository) ProjectCanEdit(ctx context.Context, actor domainlibrary.Actor, projectID string) (bool, error) {
	actorID, err := storedb.ParseUUID(actor.UserID)
	if err != nil {
		return false, domainlibrary.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, domainlibrary.ErrNotFound
	}
	role, err := sqlc.New(repository.db.Pool).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actorID, ProjectID: projectUUID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, domainlibrary.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return canEdit(role), nil
}

// BookExportParagraphs reads the published text of one chapter, its paragraphs in the Markdown subset with their italic
// and bold (specs/2026-11-08-italic-bold-in-text.md TF7.1). A chapter deleted or replaced since the list has none.
func (repository *Repository) BookExportParagraphs(ctx context.Context, projectID, bookID string, chapter domainlibrary.ExportChapter) ([]string, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	chapterUUID, err := storedb.ParseUUID(chapter.ID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	rows, err := sqlc.New(repository.db.Pool).ListChapterParagraphsByChapterRevisionVariant(ctx, sqlc.ListChapterParagraphsByChapterRevisionVariantParams{
		ProjectID: projectUUID, BookID: bookUUID, ChapterID: chapterUUID, Revision: chapter.Revision, Variant: "published",
	})
	if err != nil {
		return nil, err
	}
	paragraphs := make([]string, len(rows))
	for index, row := range rows {
		paragraphs[index] = row.Markdown
	}
	return paragraphs, nil
}
