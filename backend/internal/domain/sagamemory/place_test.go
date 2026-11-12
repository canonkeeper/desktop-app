package sagamemory

import (
	"encoding/json"
	"os"
	"testing"
)

// TestThePlaceOfAChapterIsReadByTheAuthor: the place names the book, the number of the chapter as its title writes it
// or its place in the book, and the title without that number — never the key (specs/2026-11-10-ux1-names.md N1.1). The
// table is shared with the mock of the web (src/api/mock/repository/places.test.ts), which names chapters as the server does.
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
		chapter := Chapter{BookOrder: test.Book, Order: test.Order, Title: test.Title, Key: "b9c99"}
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
