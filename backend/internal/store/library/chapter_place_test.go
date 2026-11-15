package librarystore

import (
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	sagarepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagareference"
)

// storedOrders are the stored orders of the live chapters of a book in each text version, in order.
func (fixture *readyWaveFixture) storedOrders(bookID string) map[string][]int {
	fixture.t.Helper()
	rows, err := fixture.db.Pool.Query(fixture.ctx, `
		SELECT member.source_view, member.chapter_order
		FROM chapter_source_members AS member
		JOIN chapters AS chapter ON chapter.id = member.chapter_id AND chapter.deleted_at IS NULL
		WHERE member.book_id = $1
		ORDER BY member.source_view, member.chapter_order
	`, bookID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer rows.Close()
	orders := map[string][]int{}
	for rows.Next() {
		var view string
		var order int
		if err := rows.Scan(&view, &order); err != nil {
			fixture.t.Fatal(err)
		}
		orders[view] = append(orders[view], order)
	}
	if err := rows.Err(); err != nil {
		fixture.t.Fatal(err)
	}
	return orders
}

// specs/2026-11-14-chapter-place-number.md CP1.1–CP1.3: a book imported with its chapters published — the path whose
// normalization writes the published order from zero — names its first chapter «гл. 1» in the text of the saga, by the
// key of the chapter, in the contents of the saga and in a note; the keys of its chapters are the ones of the stored
// order, as they were before; a deleted chapter leaves no gap in the places and changes no key.
func TestTheFirstChapterOfABookPublishedFromZeroIsChapterOne(t *testing.T) {
	fixture := newReadyWaveFixture(t, "f81")
	book := fixture.loadTitled(testUUID(81_001), "place-zero", "Северный предел", "Застава у перевала", "Тропа над ледником", "Сигнальные огни")
	fixture.settle()
	if orders := fixture.storedOrders(book.BookID); !slices.Equal(orders["draft"], []int{1, 2, 3}) || !slices.Equal(orders["published"], []int{0, 1, 2}) {
		t.Fatalf("the stored orders of the imported book = %v; want the draft from one and the published from zero", orders)
	}
	chapters := fixture.chaptersOfBook(book.BookID)
	saga := sagarepo.New(fixture.db)
	memory := memoryservice.NewSaga(saga, saga)
	contents := sagareference.NewGetContents(saga, memory)
	reads := func(step string, ids, keys, places []string) {
		t.Helper()
		got, version := fixture.keysOf()
		for i, id := range ids {
			if got[id] != keys[i] {
				t.Fatalf("%s: the key of chapter %d = %q, want %q of its stored order (all: %v)", step, i+1, got[id], keys[i], got)
			}
			if chapter, ok := version.Find(keys[i]); !ok || chapter.ID != id || chapter.Place() != places[i] {
				t.Fatalf("%s: the text reads %q as %+v, %v; want %q", step, keys[i], chapter, ok, places[i])
			}
			// The chat tools and the source panel find a chapter by its key; the feed, the drawings and the voice name the
			// keys by Places; the reader and the voice open a key through the contents.
			found, ok, err := memory.FindChapter(fixture.ctx, fixture.projectID, "published", keys[i])
			if err != nil || !ok || found.ID != id || found.Place() != places[i] {
				t.Fatalf("%s: FindChapter(%q) = %q, %v, %v; want %q", step, keys[i], found.Place(), ok, err, places[i])
			}
			named, err := memory.Places(fixture.ctx, fixture.projectID, []string{keys[i]})
			if err != nil || named[keys[i]] != places[i] {
				t.Fatalf("%s: Places(%q) = %v, %v; want %q", step, keys[i], named, err, places[i])
			}
			result, err := contents.Execute(fixture.ctx, sagareference.ContentsInput{
				Scope: sagareference.Scope{ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, View: "published"}, Chapter: keys[i],
			})
			if err != nil || result.Found == nil || result.Found.ID != id || result.Found.Place() != places[i] {
				t.Fatalf("%s: the contents of %q = %+v, %v; want %q", step, keys[i], result.Found, err, places[i])
			}
			if place := fixture.notePlace(fixture.noteOn(book.BookID, id)); place != places[i] {
				t.Fatalf("%s: a note of chapter %d reads %q, want %q", step, i+1, place, places[i])
			}
		}
	}
	reads("the book read", chapters, []string{"b1c0", "b1c1", "b1c2"}, []string{
		"Книга 1, гл. 1 „Застава у перевала“", "Книга 1, гл. 2 „Тропа над ледником“", "Книга 1, гл. 3 „Сигнальные огни“",
	})

	if _, err := fixture.store.FenceChapterDeletion(fixture.ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: book.BookID, ChapterID: chapters[1],
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceChapterDeletion() error = %v", err)
	}
	fixture.settle()
	reads("the second chapter deleted", []string{chapters[0], chapters[2]}, []string{"b1c0", "b1c2"}, []string{
		"Книга 1, гл. 1 „Застава у перевала“", "Книга 1, гл. 2 „Сигнальные огни“",
	})
}
