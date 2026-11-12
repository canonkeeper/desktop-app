package sagamemory

import (
	"strings"
	"testing"
)

// specs/2026-11-11-ux7-reorder.md RK1.1, RK1.3: the key of a chapter names its book by the number the book got once in
// the saga, the place of the book on the shelf orders the reading and names it to the author. The third book of a saga
// put first keeps «b3c1», reads first and is «Книга 1».
func TestTheKeyOfAChapterNamesItsBookByItsNumberAndThePlaceByTheShelf(t *testing.T) {
	version := NewVersion("project", "published", "fingerprint", []Chapter{
		{ID: "first-1", BookID: "first", BookOrder: 2, BookKeyNumber: 1, BookTitle: "Первая", Order: 1, Title: "Пролог"},
		{ID: "third-1", BookID: "third", BookOrder: 1, BookKeyNumber: 3, BookTitle: "Третья", Order: 1, Title: "Белый порт"},
		{ID: "third-2", BookID: "third", BookOrder: 1, BookKeyNumber: 3, BookTitle: "Третья", Order: 2, Title: "Глава 7. Письмо"},
	})
	if got := []string{version.Chapters[0].ID, version.Chapters[1].ID, version.Chapters[2].ID}; got[0] != "third-1" || got[1] != "third-2" || got[2] != "first-1" {
		t.Fatalf("reading order = %v, want the book put first read first", got)
	}
	chapter, ok := version.Find("b3c1")
	if !ok || chapter.ID != "third-1" {
		t.Fatalf("b3c1 = %+v, %v; want the first chapter of the third book", chapter, ok)
	}
	if chapter.Place() != "Книга 1, гл. 1 „Белый порт“" || chapter.Cite() != "т. 1, «Белый порт»" || chapter.Label() != "Том 1, Белый порт" {
		t.Fatalf("the chapter of the book put first is named %q, %q, %q", chapter.Place(), chapter.Cite(), chapter.Label())
	}
	if first, ok := version.Find("b1c1"); !ok || first.ID != "first-1" || first.Place() != "Книга 2, гл. 1 „Пролог“" {
		t.Fatalf("b1c1 = %+v, %v; want the first book, now second on the shelf", first, ok)
	}
	// A number the title writes, alone in the saga, is the key as before.
	if numbered, ok := version.Find("7"); !ok || numbered.ID != "third-2" {
		t.Fatalf("7 = %+v, %v", numbered, ok)
	}
}

// specs/2026-11-11-ux7-reorder.md RK1.5: «2» was the key of «Глава 2» while the saga had one book; a second volume with
// its own «Глава 2» makes the number ambiguous — the new keys are bKcM, and «2» a kept place holds still names the chapter
// of the book with the lowest number in the saga, wherever the books stand on the shelf.
func TestABareNumberThatTurnedAmbiguousStillNamesTheChapterOfTheFirstBook(t *testing.T) {
	chapters := []Chapter{
		{ID: "one-2", BookID: "one", BookOrder: 1, BookKeyNumber: 1, Order: 2, Title: "Глава 2. Вырванные страницы"},
		{ID: "one-3", BookID: "one", BookOrder: 1, BookKeyNumber: 1, Order: 3, Title: "Глава 3. Пепел"},
		{ID: "two-2", BookID: "two", BookOrder: 2, BookKeyNumber: 2, Order: 2, Title: "Глава 2. Снова дорога"},
	}
	version := NewVersion("project", "published", "fingerprint", chapters)
	if got := []string{version.Chapters[0].Key, version.Chapters[1].Key, version.Chapters[2].Key}; got[0] != "b1c2" || got[1] != "3" || got[2] != "b2c2" {
		t.Fatalf("keys = %v, want b1c2, 3, b2c2", got)
	}
	for _, key := range []string{"2", "гл. 2", "b1c2"} {
		if chapter, ok := version.Find(key); !ok || chapter.ID != "one-2" {
			t.Fatalf("%q = %+v, %v; want the «Глава 2» of the first book", key, chapter, ok)
		}
	}
	if places := version.Places([]string{"2"}); places["2"] != "Книга 1, гл. 2 „Вырванные страницы“" {
		t.Fatalf("the place of «2» = %q", places["2"])
	}
	// The second volume put first: «2» stays with the book numbered first in the saga, the place names its new place.
	moved := append([]Chapter(nil), chapters...)
	moved[0].BookOrder, moved[1].BookOrder, moved[2].BookOrder = 2, 2, 1
	version = NewVersion("project", "published", "fingerprint", moved)
	if chapter, ok := version.Find("2"); !ok || chapter.ID != "one-2" || chapter.Place() != "Книга 2, гл. 2 „Вырванные страницы“" {
		t.Fatalf("«2» after the move = %+v, %v", chapter, ok)
	}
	if chapter, ok := version.Find("b2c2"); !ok || chapter.ID != "two-2" {
		t.Fatalf("b2c2 after the move = %+v, %v", chapter, ok)
	}
	// Every key the chapter is read by — what a drawing, a link or a record may keep.
	first, _ := version.Find("2")
	if keys := strings.Join(version.KeysOf(first), " "); keys != "2 b1c2" {
		t.Fatalf("the keys of the «Глава 2» of the first book = %s", keys)
	}
	second, _ := version.Find("b2c2")
	if keys := strings.Join(version.KeysOf(second), " "); keys != "b2c2" {
		t.Fatalf("the keys of the «Глава 2» of the second book = %s", keys)
	}
}

// RK1.5, the other way: b2c2 was the key of the «Глава 2» of the second book while the first had one too; with the first
// book gone the chapter is keyed «2» again, and the links that kept b2c2 lead to it still.
func TestABookKeyKeptWhileTheNumberWasAmbiguousStillNamesItsChapter(t *testing.T) {
	version := NewVersion("project", "published", "fingerprint", []Chapter{
		{ID: "two-2", BookID: "two", BookOrder: 1, BookKeyNumber: 2, Order: 2, Title: "Глава 2. Снова дорога"},
		{ID: "two-3", BookID: "two", BookOrder: 1, BookKeyNumber: 2, Order: 3, Title: "Глава 3. Конец"},
	})
	if version.Chapters[0].Key != "2" {
		t.Fatalf("the key of the only «Глава 2» = %q", version.Chapters[0].Key)
	}
	for _, key := range []string{"2", "b2c2"} {
		if chapter, ok := version.Find(key); !ok || chapter.ID != "two-2" {
			t.Fatalf("%q = %+v, %v; want the «Глава 2» of the second book", key, chapter, ok)
		}
	}
	if places := version.Places([]string{"b2c2"}); places["b2c2"] != "Книга 1, гл. 2 „Снова дорога“" {
		t.Fatalf("the place of b2c2 = %q", places["b2c2"])
	}
	if _, ok := version.Find("b1c2"); ok {
		t.Fatal("b1c2 names a chapter of a book the saga has no more")
	}
}
