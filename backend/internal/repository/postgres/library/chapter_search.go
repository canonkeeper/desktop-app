package library

import (
	"context"
	"strings"

	domain "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// SearchChapterMetadata reads navigation metadata in one SQL query, before any indexing prerequisite. A chapter is named
// by the one rule of the number of a chapter — the number its title writes or its place among the chapters of its book
// (specs/2026-11-15-reader-chapter-number.md RN1.1, RN1.2) —, and a search by a number keeps the chapters whose number it
// is: the query gives all the candidates in the order of the cursor, the repository keeps them by the rule and cuts the
// page, so a search is one query whatever the chapters and the page.
func (repository *Repository) SearchChapterMetadata(ctx context.Context, input domain.ChapterSearchInput) ([]domain.ChapterNavigationSearchResult, error) {
	actor, err := storedb.ParseUUID(input.Actor.UserID)
	if err != nil {
		return nil, domain.ErrValidation
	}
	params := sqlc.SearchChapterNavigationParams{ActorUserID: actor, SourceView: input.SourceView, Query: input.Query, ChapterNumber: input.Number}
	if input.Number == "" {
		params.ResultLimit = pgtype.Int4{Int32: int32(input.Limit), Valid: true}
	}
	if input.After != nil {
		params.HasCursor = true
		params.AfterProjectID, err = storedb.ParseUUID(input.After.ProjectID)
		if err != nil {
			return nil, domain.ErrValidation
		}
		params.AfterBookID, err = storedb.ParseUUID(input.After.BookID)
		if err != nil {
			return nil, domain.ErrValidation
		}
		params.AfterChapterID, err = storedb.ParseUUID(input.After.ChapterID)
		if err != nil {
			return nil, domain.ErrValidation
		}
	}
	rows, err := sqlc.New(repository.db.Pool).SearchChapterNavigation(ctx, params)
	if err != nil {
		return nil, err
	}
	results := make([]domain.ChapterNavigationSearchResult, 0, min(len(rows), input.Limit))
	for _, row := range rows {
		if len(results) == input.Limit {
			break
		}
		number, title := sagamemory.NumberedTitle(row.ChapterTitle, int(row.ChapterPlace))
		if input.Number != "" && !sameNumber(number, input.Number) {
			continue
		}
		results = append(results, domain.ChapterNavigationSearchResult{
			ProjectTitle: row.ProjectTitle, BookTitle: row.BookTitle, ChapterTitle: row.ChapterTitle, ChapterNumber: number, ChapterDisplayTitle: title,
			Locator: domain.ChapterNavigationLocator{ProjectID: storedb.UUIDString(row.ProjectID), BookID: storedb.UUIDString(row.BookID), ChapterID: storedb.UUIDString(row.ChapterID), TargetView: input.SourceView, Revision: int(row.Revision)},
		})
	}
	return results, nil
}

// sameNumber says whether the number of a chapter is the number asked, the zeros it starts with left aside: «Глава 007» is
// found by 7.
func sameNumber(number, asked string) bool {
	trimmed := strings.TrimLeft(number, "0")
	if trimmed == "" {
		trimmed = "0"
	}
	return trimmed == asked
}
