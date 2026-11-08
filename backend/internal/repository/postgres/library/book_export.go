package library

import (
	"context"
	"errors"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
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
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	bookUUID, err := storedb.ParseUUID(bookID)
	if err != nil {
		return nil, domainlibrary.ErrNotFound
	}
	rows, err := sqlc.New(repository.db.Pool).ListBookExportChapters(ctx, sqlc.ListBookExportChaptersParams{ProjectID: projectUUID, BookID: bookUUID})
	if err != nil {
		return nil, err
	}
	chapters := make([]domainlibrary.ExportChapter, len(rows))
	for index, row := range rows {
		chapters[index] = domainlibrary.ExportChapter{ID: row.ID.String(), Title: row.Title, Revision: row.PublishedRevision}
	}
	return chapters, nil
}

// BookExportParagraphs reads the published text of one chapter. A chapter deleted or replaced since the list has none.
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
		paragraphs[index] = row.PlainText
	}
	return paragraphs, nil
}
