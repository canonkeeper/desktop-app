package library

import (
	domain "canonkeeper/backend/internal/domain/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
	"context"
)

// SearchChapterMetadata reads navigation metadata in one SQL snapshot, before any indexing prerequisite.
func (repository *Repository) SearchChapterMetadata(ctx context.Context, input domain.ChapterSearchInput) ([]domain.ChapterNavigationSearchResult, error) {
	actor, err := storedb.ParseUUID(input.Actor.UserID)
	if err != nil {
		return nil, domain.ErrValidation
	}
	params := sqlc.SearchChapterNavigationParams{ActorUserID: actor, SourceView: input.SourceView, Query: input.Query, ChapterNumber: input.Number, ResultLimit: int32(input.Limit)}
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
	results := make([]domain.ChapterNavigationSearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, domain.ChapterNavigationSearchResult{ProjectTitle: row.ProjectTitle, BookTitle: row.BookTitle, ChapterTitle: row.ChapterTitle, ChapterNumber: row.ChapterNumber,
			Locator: domain.ChapterNavigationLocator{ProjectID: storedb.UUIDString(row.ProjectID), BookID: storedb.UUIDString(row.BookID), ChapterID: storedb.UUIDString(row.ChapterID), TargetView: input.SourceView, Revision: int(row.Revision)}})
	}
	return results, nil
}
