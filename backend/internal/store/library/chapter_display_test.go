package librarystore

import (
	"context"
	"testing"
)

func TestLibraryStoreNavigationUsesExactSourceOrdinalWithoutChangingPosition(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1880)
	importedID := indexingStoreUUIDForTest(1887)
	manualID := indexingStoreUUIDForTest(1888)
	createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, importedID, "Глава 582. Порог", 2, indexingStoreTestNow())
	createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, manualID, "Эпилог", 3, indexingStoreTestNow())

	first, err := store.GetChapter(ctx, fixture.projectID, fixture.bookID, fixture.chapterID)
	if err != nil {
		t.Fatalf("GetChapter(first) error = %v", err)
	}
	if first.Navigation.Position != 1 || first.Navigation.Total != 3 {
		t.Fatalf("first position = %d/%d, want 1/3", first.Navigation.Position, first.Navigation.Total)
	}
	if first.Navigation.Next == nil {
		t.Fatal("first next is nil, want display 582")
	}
	if got := first.Navigation.Next.DisplayNumber; got != "582" {
		t.Fatalf("first next display = %q, want %q", got, "582")
	}

	imported, err := store.GetChapter(ctx, fixture.projectID, fixture.bookID, importedID)
	if err != nil {
		t.Fatalf("GetChapter(imported) error = %v", err)
	}
	if imported.Navigation.Position != 2 || imported.Navigation.Total != 3 {
		t.Fatalf("imported position = %d/%d, want 2/3", imported.Navigation.Position, imported.Navigation.Total)
	}
	if imported.Navigation.Previous == nil || imported.Navigation.Previous.DisplayNumber != "1" {
		t.Fatalf("imported previous = %#v, want local display 1", imported.Navigation.Previous)
	}
	if imported.Navigation.Next == nil || imported.Navigation.Next.DisplayNumber != "3" {
		t.Fatalf("imported next = %#v, want local display 3", imported.Navigation.Next)
	}

	last, err := store.GetChapter(ctx, fixture.projectID, fixture.bookID, manualID)
	if err != nil {
		t.Fatalf("GetChapter(manual) error = %v", err)
	}
	if last.Navigation.Position != 3 || last.Navigation.Total != 3 || last.Navigation.Previous == nil || last.Navigation.Previous.DisplayNumber != "582" || last.Navigation.Next != nil {
		t.Fatalf("last navigation = %#v, want position 3/3 and previous display 582", last.Navigation)
	}
}
