package sagamemory

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"
)

// TestThePlaceOfAChapterIsReadByTheAuthor: the place names the book, the number of the chapter as its title writes it
// or its place in the book, and the title without that number — never the key (specs/2026-11-10-ux1-names.md N1.1). The
// table is shared with the mock of the web (src/api/mock/repository/places.test.ts), which names chapters as the server does;
// its «order» is the place of the chapter in its book, from one (specs/2026-11-14-chapter-place-number.md CP1.1).
func TestThePlaceOfAChapterIsReadByTheAuthor(t *testing.T) {
	data, err := os.ReadFile("testdata/places.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Book  int    `json:"book"`
		Order int    `json:"order"`
		Title string `json:"title"`
		Place string `json:"place"`
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("places table: %v, %d cases", err, len(cases))
	}
	for _, test := range cases {
		chapter := Chapter{BookOrder: test.Book, InBook: test.Order, Title: test.Title, Key: "b9c99"}
		if got := chapter.Place(); got != test.Place {
			t.Errorf("Place(%q) = %q, want %q", test.Title, got, test.Place)
		}
	}
}

// TestTheNumberAndTheTitleOfAChapterAreOneRule: the number of a chapter and its title without that number are read from
// its title by one rule — for the reader, the search of chapters, «Недавно удалённые» and the place — and a title without
// a number takes the place of the chapter in its book, or none for a chapter without a place, a deleted one
// (specs/2026-11-15-reader-chapter-number.md RN1.1, RN1.4). The table is shared with the mock of the web
// (src/api/mock/repository/places.test.ts); the place on the same table says what it said before.
func TestTheNumberAndTheTitleOfAChapterAreOneRule(t *testing.T) {
	data, err := os.ReadFile("testdata/chapter-titles.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Title  string `json:"title"`
		InBook int    `json:"inBook"`
		Number string `json:"number"`
		Name   string `json:"name"`
		Place  string `json:"place"`
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("chapter titles table: %v, %d cases", err, len(cases))
	}
	for _, test := range cases {
		if test.InBook == 0 {
			if number, name := TitledNumber(test.Title); number != test.Number || name != test.Name {
				t.Errorf("TitledNumber(%q) = %q, %q; want %q, %q", test.Title, number, name, test.Number, test.Name)
			}
			continue
		}
		if number, name := NumberedTitle(test.Title, test.InBook); number != test.Number || name != test.Name {
			t.Errorf("NumberedTitle(%q, %d) = %q, %q; want %q, %q", test.Title, test.InBook, number, name, test.Number, test.Name)
		}
		chapter := Chapter{BookOrder: 1, InBook: test.InBook, Title: test.Title}
		if got := chapter.Place(); got != test.Place {
			t.Errorf("Place(%q) = %q, want %q", test.Title, got, test.Place)
		}
	}
}

// TestThePlaceNamesItsParagraphsInWords: «абзац 3», «абзацы 3–5» — no «¶» (N1.2).
func TestThePlaceNamesItsParagraphsInWords(t *testing.T) {
	chapter := Chapter{BookOrder: 1, Order: 14, Title: "Глава XIV"}
	for _, test := range []struct {
		first, last int
		want        string
	}{
		{0, 0, "Книга 1, гл. XIV"},
		{33, 33, "Книга 1, гл. XIV, абзац 33"},
		{33, 0, "Книга 1, гл. XIV, абзац 33"},
		{33, 35, "Книга 1, гл. XIV, абзацы 33–35"},
	} {
		if got := chapter.PlaceAt(test.first, test.last); got != test.want {
			t.Errorf("PlaceAt(%d, %d) = %q, want %q", test.first, test.last, got, test.want)
		}
	}
}

// TestTheLinksOfATextAreSaidInTheWordsOfTheAuthor: a link a button once wrote into an idea reads as the place it names
// (N3.4); a chapter the book lost is said so, and a text without links is left as it is.
func TestTheLinksOfATextAreSaidInTheWordsOfTheAuthor(t *testing.T) {
	places := map[string]string{"14": "Книга 1, гл. XIV", "b2c12": "Книга 2, гл. 12 „Белый порт“"}
	text := "Нарисуй сцену [гл. 14 ¶33](source:14/33-35): кто в кадре; ещё [т. 2, «Белый порт»](source:b2c12) и [x ¶1](source:b9c99/1)."
	want := "Нарисуй сцену Книга 1, гл. XIV, абзацы 33–35: кто в кадре; ещё Книга 2, гл. 12 „Белый порт“ и глава, которой нет в книге, абзац 1."
	if got := PlainLinks(text, places); got != want {
		t.Fatalf("PlainLinks = %q, want %q", got, want)
	}
	if got := LinkedKeys(text); len(got) != 3 || got[0] != "14" || got[1] != "b2c12" || got[2] != "b9c99" {
		t.Fatalf("LinkedKeys = %v", got)
	}
	if plain := "Дракон [на скале] (ночью)"; PlainLinks(plain, places) != plain || len(LinkedKeys(plain)) != 0 {
		t.Fatalf("a text without links changed: %q", PlainLinks(plain, places))
	}
}

// TestTheNumberOfAPlaceIsThePlaceOfTheChapterInItsBook: a chapter whose title writes no number is named by its place among
// the chapters of its book in the text, from one and without gaps, whatever its stored order — from zero (the published
// order a publication normalizes), from one, or with the gap of a deleted chapter —, each book from its own one; a number
// the title writes stays (specs/2026-11-14-chapter-place-number.md CP1.1). The keys of the model keep the stored order:
// the set of keys is the one before, and Find reads every one of them (CP1.2).
func TestTheNumberOfAPlaceIsThePlaceOfTheChapterInItsBook(t *testing.T) {
	version := NewVersion("project", "published", "fingerprint", []Chapter{
		{ID: "z2", BookID: "zero", BookOrder: 1, BookKeyNumber: 1, Order: 2, Title: "Сигнальные огни"},
		{ID: "z0", BookID: "zero", BookOrder: 1, BookKeyNumber: 1, Order: 0, Title: "Застава у перевала"},
		{ID: "z1", BookID: "zero", BookOrder: 1, BookKeyNumber: 1, Order: 1, Title: "Тропа над ледником"},
		{ID: "o1", BookID: "one", BookOrder: 2, BookKeyNumber: 2, Order: 1, Title: "Северная пристань"},
		{ID: "o2", BookID: "one", BookOrder: 2, BookKeyNumber: 2, Order: 2, Title: " "},
		{ID: "o3", BookID: "one", BookOrder: 2, BookKeyNumber: 2, Order: 3, Title: "Белый порт"},
		{ID: "g1", BookID: "gap", BookOrder: 3, BookKeyNumber: 5, Order: 1, Title: "Брод"},
		{ID: "g2", BookID: "gap", BookOrder: 3, BookKeyNumber: 5, Order: 2, Title: "Глава 12. Белый порт"},
		{ID: "g4", BookID: "gap", BookOrder: 3, BookKeyNumber: 5, Order: 4, Title: "Дозор"},
		{ID: "t0", BookID: "titled", BookOrder: 4, BookKeyNumber: 4, Order: 0, Title: "12. Северная пристань"},
		{ID: "t1", BookID: "titled", BookOrder: 4, BookKeyNumber: 4, Order: 1, Title: "Глава XIV"},
		{ID: "t2", BookID: "titled", BookOrder: 4, BookKeyNumber: 4, Order: 2, Title: "Пепел"},
	})
	want := []struct{ id, key, place string }{
		{"z0", "b1c0", "Книга 1, гл. 1 „Застава у перевала“"},
		{"z1", "b1c1", "Книга 1, гл. 2 „Тропа над ледником“"},
		{"z2", "b1c2", "Книга 1, гл. 3 „Сигнальные огни“"},
		{"o1", "b2c1", "Книга 2, гл. 1 „Северная пристань“"},
		{"o2", "b2c2", "Книга 2, гл. 2"},
		{"o3", "b2c3", "Книга 2, гл. 3 „Белый порт“"},
		{"g1", "b5c1", "Книга 3, гл. 1 „Брод“"},
		{"g2", "12", "Книга 3, гл. 12 „Белый порт“"},
		{"g4", "b5c4", "Книга 3, гл. 3 „Дозор“"},
		{"t0", "b4c0", "Книга 4, гл. 12 „Северная пристань“"},
		{"t1", "b4c1", "Книга 4, гл. XIV"},
		{"t2", "b4c2", "Книга 4, гл. 3 „Пепел“"},
	}
	if len(version.Chapters) != len(want) {
		t.Fatalf("chapters = %d, want %d", len(version.Chapters), len(want))
	}
	keys := []string{}
	for i, chapter := range version.Chapters {
		if chapter.ID != want[i].id || chapter.Key != want[i].key {
			t.Fatalf("chapter %d = %s under %q, want %s under %q", i, chapter.ID, chapter.Key, want[i].id, want[i].key)
		}
		if got := chapter.Place(); got != want[i].place {
			t.Errorf("the place of %s (stored order %d) = %q, want %q", chapter.ID, chapter.Order, got, want[i].place)
		}
		keys = append(keys, version.KeysOf(&version.Chapters[i])...)
	}
	sort.Strings(keys)
	golden := []string{"12", "b1c0", "b1c1", "b1c2", "b2c1", "b2c2", "b2c3", "b4c0", "b4c1", "b4c2", "b5c1", "b5c2", "b5c4"}
	if !slices.Equal(keys, golden) {
		t.Fatalf("the keys of the saga = %v, want the keys of the stored order %v", keys, golden)
	}
	for _, key := range golden {
		if _, ok := version.Find(key); !ok {
			t.Errorf("Find(%q) reads no chapter", key)
		}
	}
	if places := version.Places([]string{"b1c0", "b5c4", "b5c2"}); places["b1c0"] != "Книга 1, гл. 1 „Застава у перевала“" ||
		places["b5c4"] != "Книга 3, гл. 3 „Дозор“" || places["b5c2"] != "Книга 3, гл. 12 „Белый порт“" {
		t.Fatalf("the places of the keys = %v", places)
	}
}

// TestPlacesNameOnlyTheChaptersOfTheText: a key the text does not have gets no place (N1.3).
func TestPlacesNameOnlyTheChaptersOfTheText(t *testing.T) {
	version := NewVersion("project", "published", "fingerprint", []Chapter{
		{ID: "one", BookOrder: 1, BookKeyNumber: 1, Order: 1, Title: "Туманный берег"},
		{ID: "two", BookOrder: 2, BookKeyNumber: 2, Order: 12, Title: "Глава 205. Белый порт"},
	})
	places := version.Places([]string{"b1c1", " 205 ", "гл. 205", "b9c99", ""})
	want := map[string]string{
		"b1c1": "Книга 1, гл. 1 „Туманный берег“", "205": "Книга 2, гл. 205 „Белый порт“", "гл. 205": "Книга 2, гл. 205 „Белый порт“",
	}
	if len(places) != len(want) {
		t.Fatalf("places = %#v, want %#v", places, want)
	}
	for key, place := range want {
		if places[key] != place {
			t.Errorf("places[%q] = %q, want %q", key, places[key], place)
		}
	}
}
