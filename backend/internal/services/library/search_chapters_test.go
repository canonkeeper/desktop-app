package library

import (
	domain "canonkeeper/backend/internal/domain/library"
	"context"
	"errors"
	"testing"
)

type chapterSearchStoreStub struct{ input domain.ChapterSearchInput }

func (store *chapterSearchStoreStub) SearchChapterMetadata(_ context.Context, input domain.ChapterSearchInput) ([]domain.ChapterNavigationSearchResult, error) {
	store.input = input
	return []domain.ChapterNavigationSearchResult{
		{Locator: domain.ChapterNavigationLocator{ProjectID: "00000000-0000-4000-8000-000000000001", BookID: "00000000-0000-4000-8000-000000000002", ChapterID: "00000000-0000-4000-8000-000000000003", TargetView: input.SourceView, Revision: 2}},
		{Locator: domain.ChapterNavigationLocator{ProjectID: "00000000-0000-4000-8000-000000000001", BookID: "00000000-0000-4000-8000-000000000002", ChapterID: "00000000-0000-4000-8000-000000000004", TargetView: input.SourceView, Revision: 1}},
	}, nil
}

func TestSearchChaptersNormalizesNumberAndBindsCursorToActorAndQuery(t *testing.T) {
	store := &chapterSearchStoreStub{}
	search := NewSearchChapters(store)
	actor := domain.Actor{UserID: "00000000-0000-4000-8000-000000000010"}
	request := domain.ChapterSearchRequest{Query: "  ГЛАВА 00582  ", SourceView: "published", Limit: 1}
	page, err := search.Execute(context.Background(), actor, request)
	if err != nil || len(page.Data) != 1 || !page.Meta.HasMore || page.Meta.NextCursor == "" || store.input.Number != "582" || store.input.Limit != 2 {
		t.Fatalf("page=%#v input=%#v err=%v", page, store.input, err)
	}
	request.Cursor = page.Meta.NextCursor
	if _, err = search.Execute(context.Background(), actor, request); err != nil || store.input.After == nil {
		t.Fatalf("valid cursor: %v", err)
	}
	request.Query = "583"
	if _, err = search.Execute(context.Background(), actor, request); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("query cursor=%v", err)
	}
	request.Query = "ГЛАВА 00582"
	actor.UserID = "00000000-0000-4000-8000-000000000011"
	if _, err = search.Execute(context.Background(), actor, request); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("actor cursor=%v", err)
	}
}
