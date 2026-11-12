package librarystore

import (
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/sqlc"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func scanLibraryBook(row pgx.Row) (sqlc.Book, error) {
	var book sqlc.Book
	err := row.Scan(
		&book.ID,
		&book.ProjectID,
		&book.Title,
		&book.Subtitle,
		&book.CoverColor,
		&book.Order,
		&book.DisplayNumber,
		&book.Status,
		&book.Revision,
		&book.DeletedAt,
		&book.PurgeAfter,
		&book.CreatedAt,
		&book.UpdatedAt,
	)
	return book, err
}
func libraryBookFromGetSummarySQL(row sqlc.GetBookSummaryByIDRow) library.Book {
	return libraryBookFromSummaryColumns(
		row.ID,
		row.ProjectID,
		row.Title,
		row.Subtitle,
		row.CoverColor,
		row.Order,
		row.DisplayNumber,
		row.Status,
		row.Revision,
		row.CreatedAt,
		row.UpdatedAt,
		row.CoverVersion,
		row.ChapterCount,
		row.WordCount,
	)
}

func libraryBookFromListSummarySQL(row sqlc.ListBookSummariesByProjectRow) library.Book {
	book := libraryBookFromSummaryColumns(
		row.ID,
		row.ProjectID,
		row.Title,
		row.Subtitle,
		row.CoverColor,
		row.Order,
		row.DisplayNumber,
		row.Status,
		row.Revision,
		row.CreatedAt,
		row.UpdatedAt,
		row.CoverVersion,
		row.ChapterCount,
		row.WordCount,
	)
	book.Opening = row.Opening
	return book
}

func libraryBookFromSummaryColumns(
	id pgtype.UUID,
	projectID pgtype.UUID,
	title string,
	subtitle pgtype.Text,
	coverColor pgtype.Text,
	order int32,
	displayNumber string,
	status string,
	revision int64,
	createdAt pgtype.Timestamptz,
	updatedAt pgtype.Timestamptz,
	coverVersion int64,
	chapterCount int32,
	wordCount int32,
) library.Book {
	book := library.Book{
		ID:            id.String(),
		ProjectID:     projectID.String(),
		Title:         title,
		Subtitle:      textPtr(subtitle),
		CoverColor:    textPtr(coverColor),
		Order:         int(order),
		DisplayNumber: displayNumber,
		Status:        library.BookStatus(status),
		Revision:      revision,
		ChapterCount:  int(chapterCount),
		WordCount:     int(wordCount),
		CreatedAt:     createdAt.Time,
		UpdatedAt:     updatedAt.Time,
	}
	if coverVersion > 0 {
		coverURL := fmt.Sprintf("/api/v1/books/%s/cover/%d", book.ID, coverVersion)
		book.CoverURL = &coverURL
	}
	return book
}
func textValue(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
