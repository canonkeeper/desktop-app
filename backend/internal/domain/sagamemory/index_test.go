package sagamemory

import (
	"reflect"
	"strings"
	"testing"
)

func testVersion() *Version {
	paragraphs := func(texts ...string) []Paragraph {
		out := make([]Paragraph, len(texts))
		for i, text := range texts {
			out[i] = Paragraph{Text: text}
		}
		return out
	}
	return NewVersion("project", "draft", "fp", []Chapter{
		{ID: "c3", BookID: "b2", BookOrder: 2, BookKeyNumber: 2, Order: 1, Title: "Глава 83. Подземный город Зимого́рск.",
			Paragraphs: paragraphs("Дозорный первым нашёл скрытый город-алтарь Зимого́рск.", "Жрица вьюжница ждала у входа.")},
		{ID: "c1", BookID: "b1", BookOrder: 1, BookKeyNumber: 1, Order: 1, Title: "Глава 1. Начало",
			Paragraphs: paragraphs("Его друг по отряду позвал Мирона с собой.", "Мирон купил капсулу и мини-босс.")},
		{ID: "c2", BookID: "b1", BookOrder: 1, BookKeyNumber: 1, Order: 2, Title: "Послесловие",
			Paragraphs: paragraphs("Ёлки, это был миниконтро́ллер!")},
		{ID: "c4", BookID: "b2", BookOrder: 2, BookKeyNumber: 2, Order: 2, Title: "Глава 83. Повтор номера",
			Paragraphs: paragraphs("Вьюжница снова появилась в Зимогорске.")},
	})
}

// stems mimics russian_stem for the words of the test version.
var testStems = map[string]string{
	"зимогорск": "зимогорск", "зимогорске": "зимогорск", "вьюжница": "вьюжниц", "друг": "друг", "по": "",
	"отряду": "отряд", "мирона": "мирон", "мирон": "мирон", "капсулу": "капсул", "город": "город",
}

func TestNewVersionOrdersChaptersAndAssignsStableKeys(t *testing.T) {
	version := testVersion()
	var keys []string
	for _, chapter := range version.Chapters {
		keys = append(keys, chapter.Key)
	}
	if !reflect.DeepEqual(keys, []string{"1", "b1c2", "b2c1", "b2c2"}) {
		t.Fatalf("keys = %v, want unique numbers and b/c keys for the afterword and the duplicated 83", keys)
	}
	if chapter, ok := version.Find("гл. 1"); !ok || chapter.ID != "c1" {
		t.Fatalf("Find(гл. 1) = %v, %v", chapter, ok)
	}
	// specs/2026-11-11-ux7-reorder.md RK1.5: an ambiguous number is no key of its own, but reads as its first holder —
	// the earliest such chapter of the book with the lowest number.
	if chapter, ok := version.Find("83"); !ok || chapter.ID != "c3" || chapter.Key != "b2c1" {
		t.Fatalf("ambiguous number 83 = %+v, %v; want the first «Глава 83» under its own key b2c1", chapter, ok)
	}
}

func TestSearchIgnoresStressMarksAndYo(t *testing.T) {
	index := wholeIndex(t, testVersion(), testStems)
	result := index.Run(Query{Text: "Зимогорск", To: -1, Story: true})
	if len(result.Hits) != 2 || len(result.Chapters) != 2 {
		t.Fatalf("hits = %d chapters = %d, want both chapters despite the stress mark", len(result.Hits), len(result.Chapters))
	}
	if chapter, _ := index.Locate(result.Hits[0].Para); chapter.Key != "b2c1" {
		t.Fatalf("story order starts at %s, want the earliest chapter", chapter.Key)
	}
	if len(index.Run(Query{Text: "елки", To: -1}).Hits) != 1 {
		t.Fatal("ё/е must match")
	}
}

func TestSearchModes(t *testing.T) {
	index := wholeIndex(t, testVersion(), testStems)
	if hits := index.Run(Query{Text: `"друг по отряду"`, To: -1}).Hits; len(hits) != 1 {
		t.Fatalf("phrase hits = %d, want 1", len(hits))
	}
	if hits := index.Run(Query{Text: "контроллер", Mode: ModeSubstring, To: -1}).Hits; len(hits) != 1 {
		t.Fatalf("substring hits = %d, want the stressed compound word", len(hits))
	}
	if hits := index.Run(Query{Text: "минибосс", To: -1}).Hits; len(hits) != 1 {
		t.Fatalf("hyphenated word hits = %d, want the joined form", len(hits))
	}
	if hits := index.Run(Query{Text: "мирон капсулу", Mode: ModeAll, To: -1}).Hits; len(hits) != 1 {
		t.Fatalf("all-words hits = %d, want one paragraph with both", len(hits))
	}
	if hits := index.Run(Query{Text: "мирон капсулу", To: -1}).Hits; len(hits) != 2 {
		t.Fatalf("any-word hits = %d, want both paragraphs", len(hits))
	}
}

// Snowball stems the nominative of a surname apart from its other cases ("кутузов" → "кутуз",
// "кутузова" → "кутузов"); a word query still finds every case form the text has.
func TestWordSearchFindsEveryCaseOfASurname(t *testing.T) {
	version := NewVersion("project", "draft", "fp", []Chapter{{ID: "c1", BookOrder: 1, BookKeyNumber: 1, Order: 1, Title: "Глава 1",
		Paragraphs: []Paragraph{{Text: "Брат — адъютант Кутузова."}, {Text: "Кутузов улыбнулся."}, {Text: "Письмо Кутузову."}}}})
	stems := map[string]string{
		"кутузов": "кутуз", "кутузова": "кутузов", "кутузову": "кутузов",
		"брат": "брат", "адъютант": "адъютант", "улыбнулся": "улыбнул", "письмо": "письм",
	}
	index := wholeIndex(t, version, stems)
	for _, text := range []string{"Кутузов", "Кутузова", "Кутузову", `"адъютант Кутузов"`} {
		want := 3
		if strings.HasPrefix(text, `"`) {
			want = 1
		}
		if hits := index.Run(Query{Text: text, To: -1}).Hits; len(hits) != want {
			t.Fatalf("%s hits = %d, want %d: every case of the surname", text, len(hits), want)
		}
	}
}

// A part-of-word search reads a hyphenated word as one: an author writes "мини-контроллер"
// in one chapter and "миниконтроллер" in another.
func TestSubstringSearchIgnoresHyphensInsideWords(t *testing.T) {
	index := wholeIndex(t, testVersion(), testStems)
	for _, text := range []string{"минибосс", "мини-босс", "инибо"} {
		result := index.Run(Query{Text: text, Mode: ModeSubstring, To: -1})
		if len(result.Hits) != 1 {
			t.Fatalf("substring %q hits = %d, want the hyphenated word", text, len(result.Hits))
		}
		if snippet := index.Snippet(result.Hits[0], 20); !strings.Contains(snippet, "мини-босс") {
			t.Fatalf("snippet = %q, want the matched hyphenated word", snippet)
		}
	}
	for _, text := range []string{"мини-контроллер", "город-алтарь", "городалтарь"} {
		if hits := index.Run(Query{Text: text, Mode: ModeSubstring, To: -1}).Hits; len(hits) != 1 {
			t.Fatalf("substring %q hits = %d, want one word written with or without the hyphen", text, len(hits))
		}
	}
}

func TestSearchFiltersByRangeAndBooks(t *testing.T) {
	version := testVersion()
	index := wholeIndex(t, version, testStems)
	if hits := index.Run(Query{Text: "вьюжница", From: 3, To: 3}).Hits; len(hits) != 1 {
		t.Fatalf("range hits = %d, want only the last chapter", len(hits))
	}
	if hits := index.Run(Query{Text: "мирон", To: -1, BookIDs: []string{"b2"}}).Hits; len(hits) != 0 {
		t.Fatalf("book filter hits = %d, want none in book 2", len(hits))
	}
}

func TestNormalizeAndTokenize(t *testing.T) {
	if got := Normalize("Ака́ши Ёж"); got != "акаши еж" {
		t.Fatalf("Normalize() = %q", got)
	}
	var words []string
	for _, token := range Tokenize("мини-босс и йод") {
		words = append(words, token.Word)
	}
	if !reflect.DeepEqual(words, []string{"мини", "босс", "минибосс", "и", "йод"}) {
		t.Fatalf("Tokenize() = %v", words)
	}
}

// Answers name a chapter the way the author sees it: by its own number, or by book and title
// when the number repeats across parts and books.
func TestChapterCitationsUseTheAuthorsNames(t *testing.T) {
	version := NewVersion("project", "draft", "fp", []Chapter{
		{ID: "c1", BookOrder: 9, BookKeyNumber: 9, Order: 1, Title: "Глава 312. Опознание", Paragraphs: []Paragraph{{Text: "a"}, {Text: "b"}}},
		{ID: "c2", BookOrder: 1, BookKeyNumber: 1, Order: 62, Title: "Часть третья. Глава XVI", Paragraphs: []Paragraph{{Text: "a"}}},
	})
	numbered, _ := version.Find("312")
	titled, _ := version.Find("b1c62")
	for _, check := range []struct{ got, want string }{
		{numbered.Cite(), "гл. 312"},
		{numbered.Link(15, 17), "[гл. 312 ¶15–17](source:312/15-17)"},
		{numbered.Ref(), "гл. 312"},
		{titled.Cite(), "т. 1, «Часть третья. Глава XVI»"},
		{titled.Link(26, 26), "[т. 1, «Часть третья. Глава XVI» ¶26](source:b1c62/26)"},
		{titled.Ref(), "т. 1, «Часть третья. Глава XVI» [b1c62]"},
	} {
		if check.got != check.want {
			t.Errorf("got %q, want %q", check.got, check.want)
		}
	}
}
