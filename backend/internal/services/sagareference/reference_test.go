package sagareference

import (
	"context"
	"errors"
	"slices"
	"testing"

	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

type fakeAccess struct{ err error }

func (access fakeAccess) ReadableView(context.Context, string, string, string) error {
	return access.err
}

func saga() Memory {
	version := sagamemory.NewVersion("project", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 1. Порт", Paragraphs: []sagamemory.Paragraph{{Text: "Мила."}}},
		{ID: "c2", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 2, Title: "Глава 2. Дым", Paragraphs: []sagamemory.Paragraph{{Text: "Сава."}}},
		{ID: "c3", BookID: "b2", BookOrder: 2, BookTitle: "Том 2", Order: 1, Title: "Глава 3. Суд", Paragraphs: []sagamemory.Paragraph{{Text: "Мила."}}},
	})
	stems := map[string]string{"мила": "мил", "милы": "мил", "кинжал": "кинжал", "кинжалом": "кинжал"}
	return sagamemorytest.NewSaga(version, stems, map[int]*sagamemory.Card{
		0: {Summary: "Мила приходит в порт.", Time: []string{"Утром (¶1)"},
			Characters: []sagamemory.Mention{{Name: "Мила", P: 1}, {Name: "Сава", P: 2}},
			Entities:   []sagamemory.Mention{{Name: "Кинжал", Type: "оружие", P: 3}},
			Changes:    []sagamemory.Change{{Subject: "Мила", Change: "Получает кинжал."}}},
		1: {Summary: "Сава замечает дым.", Time: []string{"Через час (¶3)"},
			Characters: []sagamemory.Mention{{Name: "Сава", P: 1}, {Name: "Мила", P: 4}},
			Entities:   []sagamemory.Mention{{Name: "Кинжал", Type: "оружие", P: 2}}},
		2: {Summary: "Суд над Милой.", Time: []string{"Спустя неделю (¶1)"},
			Characters: []sagamemory.Mention{{Name: "Мила", P: 1}},
			Entities:   []sagamemory.Mention{{Name: "Кинжал", Type: "оружие", P: 5}},
			Changes:    []sagamemory.Change{{Subject: "Мила", Change: "Теряет кинжал."}}},
	})
}

var scope = Scope{ActorUserID: "actor", ProjectID: "project", View: "draft"}

func TestReadsCheckAccessFirst(t *testing.T) {
	denied := NewListGlossary(fakeAccess{err: ErrDenied}, saga())
	if _, err := denied.Execute(context.Background(), GlossaryInput{Scope: scope}); !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
	glossary := NewListGlossary(fakeAccess{}, saga())
	if _, err := glossary.Execute(context.Background(), GlossaryInput{Scope: Scope{ActorUserID: "actor", ProjectID: "project", View: "other"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for an unknown view", err)
	}
}

func TestListGlossaryPagesEntries(t *testing.T) {
	glossary := NewListGlossary(fakeAccess{}, saga())
	page, err := glossary.Execute(context.Background(), GlossaryInput{Scope: scope, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Entries) != 1 || page.Entries[0].Entry.Name != "Мила" || page.Entries[0].First.Key != "1" {
		t.Fatalf("page = %+v, want the second most mentioned entry with its first chapter", page)
	}
	if page.Coverage != (sagamemory.Coverage{Chapters: 3, WithCards: 3}) || len(page.Types) != 2 {
		t.Fatalf("coverage %+v types %+v", page.Coverage, page.Types)
	}
	weapons, _ := glossary.Execute(context.Background(), GlossaryInput{Scope: scope, Type: "оружие"})
	if weapons.Total != 1 || weapons.Entries[0].Entry.Name != "Кинжал" {
		t.Fatalf("weapons = %+v", weapons)
	}
}

func TestContentsScopes(t *testing.T) {
	contents := NewGetContents(fakeAccess{}, saga())
	second, _ := contents.Execute(context.Background(), ContentsInput{Scope: scope, BookID: "b2"})
	if second.Book.ID != "b2" || len(second.Chapters) != 1 || second.Chapters[0].Summary != "Суд над Милой." {
		t.Fatalf("book 2 = %+v", second)
	}
	found, _ := contents.Execute(context.Background(), ContentsInput{Scope: scope, Query: "дым"})
	if found.Book != nil || len(found.Chapters) != 1 || found.Chapters[0].Chapter.Key != "2" {
		t.Fatalf("search = %+v", found)
	}
}

// A book lists the keys of its chapters: numbers and «bNcM» side by side, so the book of any key is the one that lists
// it (prod 06.11: the last chapter of 10 books of 13 has no number, and a range of the first and last keys lost them).
func TestContentsBooksListTheKeysOfTheirChapters(t *testing.T) {
	version := sagamemory.NewVersion("project", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, Order: 1, Title: "Глава 1. Порт"},
		{ID: "c2", BookID: "b1", BookOrder: 1, Order: 2, Title: "Послесловие"},
		{ID: "c3", BookID: "b2", BookOrder: 2, Order: 1, Title: "Пролог"},
		{ID: "c4", BookID: "b2", BookOrder: 2, Order: 2, Title: "Глава 2. Суд"},
	})
	contents := NewGetContents(fakeAccess{}, sagamemorytest.NewSaga(version, nil, nil))
	result, err := contents.Execute(context.Background(), ContentsInput{Scope: scope})
	if err != nil || len(result.Books) != 2 {
		t.Fatalf("contents = %+v %v", result, err)
	}
	if got := result.Books[0].Keys; !slices.Equal(got, []string{"1", "b1c2"}) {
		t.Fatalf("the keys of book 1 = %q", got)
	}
	if got := result.Books[1].Keys; !slices.Equal(got, []string{"b2c1", "2"}) {
		t.Fatalf("the keys of book 2 = %q", got)
	}
}
