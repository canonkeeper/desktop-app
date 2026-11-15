package librarystore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/domain/bookexport"
	"canonkeeper/backend/internal/library"
)

// importedChapterIDs imports a book of the titles given by the handler of the import Job — its text published, the contents
// of the reader — and gives its book and the ids of its chapters in their order.
func importedChapterIDs(t *testing.T, titles ...string) (*importWriteFixture, string, []string) {
	t.Helper()
	fixture := newImportWriteFixture(t, openCurrentStoreTestDB(t))
	chapters := make([]scenarioChapter, len(titles))
	for index, title := range titles {
		chapters[index] = scenarioChapter{title: title, paragraphs: []string{"Дорога шла вдоль реки."}}
	}
	book := fixture.importFile(library.ImportModeNewBook, "", "book.fb2", scenarioFile(t, bookexport.FormatFB2, chapters))
	listed, err := fixture.store.ListChapters(fixture.ctx, library.ListChaptersStoreInput{ProjectID: fixture.projectID, BookID: book.BookID, Limit: len(titles) + 1})
	if err != nil || len(listed) != len(titles) {
		t.Fatalf("the chapters of the imported book = %d, %v; want %d", len(listed), err, len(titles))
	}
	ids := make([]string, len(listed))
	for index, chapter := range listed {
		ids[index] = chapter.ID
	}
	return fixture, book.BookID, ids
}

func TestLibraryStoreNavigationUsesExactSourceOrdinalWithoutChangingPosition(t *testing.T) {
	ctx := context.Background()
	fixture, bookID, ids := importedChapterIDs(t, "Пролог", "Глава 582. Порог", "Эпилог")
	store := fixture.store
	importedID, manualID := ids[1], ids[2]

	first, err := store.GetChapter(ctx, fixture.projectID, bookID, ids[0])
	if err != nil {
		t.Fatalf("GetChapter(first) error = %v", err)
	}
	if first.Navigation.Position != 1 || first.Navigation.Total != 3 {
		t.Fatalf("first position = %d/%d, want 1/3", first.Navigation.Position, first.Navigation.Total)
	}
	if first.Navigation.Next == nil {
		t.Fatal("first next is nil, want display 582")
	}
	if got, _ := first.Navigation.Next.Numbered(); got != "582" {
		t.Fatalf("first next display = %q, want %q", got, "582")
	}

	imported, err := store.GetChapter(ctx, fixture.projectID, bookID, importedID)
	if err != nil {
		t.Fatalf("GetChapter(imported) error = %v", err)
	}
	if imported.Navigation.Position != 2 || imported.Navigation.Total != 3 {
		t.Fatalf("imported position = %d/%d, want 2/3", imported.Navigation.Position, imported.Navigation.Total)
	}
	if imported.Navigation.Previous == nil || imported.Navigation.Previous.Place != 1 {
		t.Fatalf("imported previous = %#v, want local display 1", imported.Navigation.Previous)
	}
	if imported.Navigation.Next == nil || imported.Navigation.Next.Place != 3 {
		t.Fatalf("imported next = %#v, want local display 3", imported.Navigation.Next)
	}

	last, err := store.GetChapter(ctx, fixture.projectID, bookID, manualID)
	if err != nil {
		t.Fatalf("GetChapter(manual) error = %v", err)
	}
	if last.Navigation.Previous == nil {
		t.Fatal("last previous is nil, want display 582")
	}
	if number, _ := last.Navigation.Previous.Numbered(); last.Navigation.Position != 3 || last.Navigation.Total != 3 || last.Chapter.Place != 3 || number != "582" || last.Navigation.Next != nil {
		t.Fatalf("last navigation = %#v, want position 3/3 and previous display 582", last.Navigation)
	}
}
