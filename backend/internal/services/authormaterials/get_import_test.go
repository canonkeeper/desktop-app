package authormaterials

import (
	"context"
	"testing"

	domain "canonkeeper/backend/internal/domain/authormaterials"
)

type fakeImportStore struct {
	found *domain.Import
	keys  map[string]bool
}

func (store fakeImportStore) AuthorMaterialsImport(context.Context, Actor) (*domain.Import, map[string]bool, error) {
	return store.found, store.keys, nil
}

type fakeBookNames map[string]string

func (names fakeBookNames) BookNames(_ context.Context, _ string, keys []string) (map[string]string, error) {
	found := map[string]string{}
	for _, key := range keys {
		if name, ok := names[key]; ok {
			found[key] = name
		}
	}
	return found, nil
}

// specs/2026-11-04-saga-map.md R9.4: the review says what saving does to each name, by the prepared names of the map: a record of
// the author is replaced, so it wins over the book, and the book still names it.
func TestGetImportSaysWhatEachNameIs(t *testing.T) {
	found := &domain.Import{Candidates: []domain.Draft{{Name: "Лиса"}, {Name: "Омут  СНОВ"}, {Name: "Сава"}, {Name: "Новое"}}}
	view, err := GetImport{
		Store: fakeImportStore{found: found, keys: map[string]bool{"омут снов": true, "сава": true}},
		Names: fakeBookNames{"лиса": "Мила", "сава": "Сава"},
	}.Execute(context.Background(), Actor{ProjectID: "project"})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		match Match
		book  string
	}{{MatchBook, "Мила"}, {MatchMaterials, ""}, {MatchMaterials, "Сава"}, {MatchNew, ""}}
	for i, candidate := range view.Views {
		if candidate.Match != want[i].match || candidate.BookName != want[i].book {
			t.Errorf("%s = %s %q, want %s %q", candidate.Name, candidate.Match, candidate.BookName, want[i].match, want[i].book)
		}
	}
	waiting, err := GetImport{Store: fakeImportStore{found: &domain.Import{}}}.Execute(context.Background(), Actor{ProjectID: "project"})
	if err != nil || waiting.Views != nil {
		t.Fatalf("an import before its Job ends = %+v, %v", waiting, err)
	}
}
