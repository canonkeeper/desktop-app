package library

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/textface"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// bookOutline writes a parsed book as lines: «# Title» of the book, then each chapter as «## Title» and its paragraphs
// in the Markdown subset of the book.
func bookOutline(book BookImport) []string {
	lines := []string{"# " + book.Title}
	for _, chapter := range book.Chapters {
		lines = append(lines, "## "+chapter.Title)
		for _, paragraph := range chapter.Paragraphs {
			lines = append(lines, textface.Markdown(paragraph.Runs))
		}
	}
	if book.Unsplit {
		lines = append(lines, "(unsplit)")
	}
	return lines
}

func parseTextForTest(t *testing.T, text string, markdown bool) BookImport {
	t.Helper()
	book, err := ParseText([]byte(text), markdown)
	if err != nil {
		t.Fatalf("ParseText() error = %v", err)
	}
	return book
}

func assertOutline(t *testing.T, book BookImport, want ...string) {
	t.Helper()
	if got := bookOutline(book); !slices.Equal(got, want) {
		t.Fatalf("book =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

const textBookForTest = "Северный ветер\n\nГлава 1. Порт\nКорабль вошёл в гавань на рассвете.\nМарта ждала на причале.\n\nГлава 2\nНочью начался шторм.\n"

// K2.1: UTF-8 with its mark and without, UTF-16 by its mark, otherwise Windows-1251; a text no encoding of the rule
// reads without signs of replacement is refused.
func TestParseTextReadsItsEncodings(t *testing.T) {
	utf16 := func(order unicode.Endianness) []byte {
		encoded, err := unicode.UTF16(order, unicode.UseBOM).NewEncoder().Bytes([]byte(textBookForTest))
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	cp1251, err := charmap.Windows1251.NewEncoder().Bytes([]byte(textBookForTest))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"utf-8":           []byte(textBookForTest),
		"utf-8 with mark": append([]byte{0xEF, 0xBB, 0xBF}, textBookForTest...),
		"utf-16 le":       utf16(unicode.LittleEndian),
		"utf-16 be":       utf16(unicode.BigEndian),
		"windows-1251":    cp1251,
	} {
		book, err := ParseText(data, false)
		if err != nil {
			t.Fatalf("%s: ParseText() error = %v", name, err)
		}
		assertOutline(t, book, "# Северный ветер",
			"## Глава 1. Порт", "Корабль вошёл в гавань на рассвете.", "Марта ждала на причале.",
			"## Глава 2", "Ночью начался шторм.")
	}
	for name, data := range map[string][]byte{
		// 0x98 is no letter of Windows-1251.
		"no letter of windows-1251": append(cp1251, 0x98),
		// A half of a pair of UTF-16.
		"broken utf-16": append(utf16(unicode.LittleEndian), 0x00, 0xD8, 0x41, 0x00),
	} {
		if _, err := ParseText(data, false); !errors.Is(err, errImportUnsupportedEncoding) {
			t.Fatalf("%s: ParseText() error = %v, want the unsupported encoding", name, err)
		}
		if failure := importParseFailure(errImportUnsupportedEncoding, ".txt"); failure.Code != "import.unsupported_encoding" {
			t.Fatalf("failure = %q", failure.Code)
		}
	}
}

// K2.2: a paragraph is a line with words; in a text cut by width it is a block of lines up to a blank line or an
// indent, its lines joined by a space. The rule is measured on these texts.
func TestParseTextFindsParagraphsByOneRule(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       []string
	}{
		{
			name: "a paragraph a line",
			text: "Марта проснулась до рассвета, когда над гаванью ещё висел туман.\nОна оделась и вышла.\nНа причале никого не было.\n",
			want: []string{"Марта проснулась до рассвета, когда над гаванью ещё висел туман.", "Она оделась и вышла.", "На причале никого не было."},
		},
		{
			name: "a paragraph a line, blank lines between",
			text: "Марта проснулась до рассвета.\n\nОна оделась и вышла.\n\nНа причале никого не было.\n",
			want: []string{"Марта проснулась до рассвета.", "Она оделась и вышла.", "На причале никого не было."},
		},
		{
			name: "cut by width, blank lines between",
			text: "Марта проснулась до рассвета, когда над гаванью\nещё висел туман и чайки молчали.\n\nОна оделась, взяла фонарь и вышла на улицу,\nстараясь не разбудить отца.\n\nНа причале никого не было, только старый\nрыбак чинил сеть у самой воды.\n",
			want: []string{
				"Марта проснулась до рассвета, когда над гаванью ещё висел туман и чайки молчали.",
				"Она оделась, взяла фонарь и вышла на улицу, стараясь не разбудить отца.",
				"На причале никого не было, только старый рыбак чинил сеть у самой воды.",
			},
		},
		{
			name: "cut by width, a paragraph by its indent",
			text: "     Марта проснулась до рассвета, когда над гаванью\nещё висел туман и чайки молчали.\n     Она оделась, взяла фонарь и вышла на улицу,\nстараясь не разбудить отца.\n     - Куда ты? - спросил он из темноты.\n",
			want: []string{
				"Марта проснулась до рассвета, когда над гаванью ещё висел туман и чайки молчали.",
				"Она оделась, взяла фонарь и вышла на улицу, стараясь не разбудить отца.",
				"- Куда ты? - спросил он из темноты.",
			},
		},
		{
			name: "cut by width, a dialogue after the end of a sentence",
			text: "Отец сидел у окна и долго смотрел\nна неё, не говоря ни слова, и только\nпотом отвернулся к огню.\n— Куда ты? — спросил он наконец.\n\nМарта не ответила, накинула плащ и\nвышла.\n",
			want: []string{
				"Отец сидел у окна и долго смотрел на неё, не говоря ни слова, и только потом отвернулся к огню.",
				"— Куда ты? — спросил он наконец.",
				"Марта не ответила, накинула плащ и вышла.",
			},
		},
		{
			name: "a dialogue of short lines, scenes between blank lines",
			text: "— Ты пойдёшь?\n— Пойду.\n— Когда?\n— Утром.\n\n— Возьми фонарь.\n— Возьму.\n",
			want: []string{"— Ты пойдёшь?", "— Пойду.", "— Когда?", "— Утром.", "— Возьми фонарь.", "— Возьму."},
		},
		{
			name: "paragraphs between blank lines, a dialogue line by line",
			text: "Он вошёл в комнату.\n\n— Привет, — сказал он.\n— Привет, — ответила она.\n\nОни помолчали.\n",
			want: []string{"Он вошёл в комнату.", "— Привет, — сказал он.", "— Привет, — ответила она.", "Они помолчали."},
		},
		{
			name: "cut by width, a chapter title inside a block stands alone",
			text: "Шторм длился три дня, и всё это время\nкорабли стояли у причала.\nГлава 2\nНа четвёртый день море стихло, и\nрыбаки вышли в море.\n\nМарта ждала их весь вечер.\n",
			want: []string{
				"## ",
				"Шторм длился три дня, и всё это время корабли стояли у причала.",
				"## Глава 2",
				"На четвёртый день море стихло, и рыбаки вышли в море.",
				"Марта ждала их весь вечер.",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := bookOutline(parseTextForTest(t, test.text, false))
			var paragraphs []string
			for _, line := range got[1:] {
				if line != "(unsplit)" && (!strings.HasPrefix(line, "## ") || strings.HasPrefix(test.want[0], "## ")) {
					paragraphs = append(paragraphs, line)
				}
			}
			if !slices.Equal(paragraphs, test.want) {
				t.Fatalf("paragraphs =\n%s\nwant\n%s", strings.Join(paragraphs, "\n"), strings.Join(test.want, "\n"))
			}
		})
	}
}

// K2.3, K2.4: chapters start at the lines that name them, the text before the first one is a chapter without a title;
// a book without one is one chapter named as the book. The first line names the book when it is short, a blank line
// follows it and it does not read as a sentence, or when a chapter starts right after it.
func TestParseTextFindsChaptersAndTheTitle(t *testing.T) {
	assertOutline(t, parseTextForTest(t, "Пролог\nДо войны было тихо.\nГлава 1\nВойна началась.\nГлава 2. Мир\nМир наступил.\nЭпилог: годы спустя\nВсё забылось.\n", false),
		"# ", "## Пролог", "До войны было тихо.", "## Глава 1", "Война началась.", "## Глава 2. Мир", "Мир наступил.",
		"## Эпилог: годы спустя", "Всё забылось.")
	assertOutline(t, parseTextForTest(t, "Посвящается отцу, который научил меня ходить под парусом и не бояться шторма.\nГлава 1\nВойна началась.\n", false),
		"# ", "## ", "Посвящается отцу, который научил меня ходить под парусом и не бояться шторма.", "## Глава 1", "Война началась.")
	assertOutline(t, parseTextForTest(t, "Северный ветер\nГлава 1\nВойна началась.\n", false),
		"# Северный ветер", "## Глава 1", "Война началась.")
	assertOutline(t, parseTextForTest(t, "Северный ветер\n\nВойна началась внезапно.\nНикто не ждал её.\n", false),
		"# Северный ветер", "## ", "Война началась внезапно.", "Никто не ждал её.", "(unsplit)")
	assertOutline(t, parseTextForTest(t, "Было холодно.\n\nВойна началась внезапно.\n", false),
		"# ", "## ", "Было холодно.", "Война началась внезапно.", "(unsplit)")
	assertOutline(t, parseTextForTest(t, "— Стой!\n\nВойна началась внезапно.\n", false),
		"# ", "## ", "— Стой!", "Война началась внезапно.", "(unsplit)")
	assertOutline(t, parseTextForTest(t, "Северный ветер\nВойна началась внезапно.\n", false),
		"# ", "## ", "Северный ветер", "Война началась внезапно.", "(unsplit)")
	assertOutline(t, parseTextForTest(t, "Глава 1\n\nВойна началась внезапно.\n", false),
		"# ", "## Глава 1", "Война началась внезапно.")
	// «Глава гильдии кивнул.» is prose, not a title.
	assertOutline(t, parseTextForTest(t, "Глава гильдии кивнул.\nВсе вышли.\n", false),
		"# ", "## ", "Глава гильдии кивнул.", "Все вышли.", "(unsplit)")
}

// K2.3: a book without a line that names a chapter is one chapter named as the book: by its first line or its file.
func TestImportPlanNamesTheOneChapterAsTheBook(t *testing.T) {
	for _, test := range []struct{ text, file, want string }{
		{text: "Северный ветер\n\nВойна началась внезапно.\n", file: "rukopis.txt", want: "Северный ветер"},
		{text: "Война началась внезапно.\nНикто не ждал её.\n", file: "Северный ветер.txt", want: "Северный ветер"},
		{text: "Война началась внезапно.\n", file: "Том 2.fb2.zip", want: "Том 2"},
	} {
		parsed := parseTextForTest(t, test.text, false)
		plan, err := buildImportPlan(ImportFileMetadata{SourceFileName: test.file}, parsed)
		if err != nil {
			t.Fatalf("buildImportPlan() error = %v", err)
		}
		if plan.BookTitle != test.want || len(plan.Chapters) != 1 || plan.Chapters[0].Title != test.want {
			t.Fatalf("plan of %q = %q / %+v, want one chapter named %q", test.file, plan.BookTitle, plan.Chapters, test.want)
		}
	}
}

// K2.3, K2.5: in Markdown a heading of the first level starts a chapter too, italic and bold are faces, the rest of the
// signs go and their words stay.
func TestParseMarkdownReadsHeadingsFacesAndLinks(t *testing.T) {
	text := strings.Join([]string{
		"# Северный ветер",
		"",
		"# Утро",
		"",
		"Марта *проснулась* до _рассвета_, когда над **гаванью** ещё висел __туман__.",
		"",
		"## У причала",
		"",
		"Она прошла мимо [таверны «Якорь»](https://example.test/anchor) и ![карта](map.png)свернула к [[Маяк|маяку]].",
		"",
		"> Море помнит всё, говорил отец.",
		"",
		"- первое — сеть,",
		"* второе — фонарь,",
		"1. третье — нож.",
		"",
		"* * *",
		"",
		"Слово [^1] со сноской и `кодом`, звёздочка \\* и подчёркивание snake_case.",
		"",
		"[^1]: Сноска не входит в текст.",
		"",
		"# Глава 2",
		"",
		"**Глава** без *пары",
	}, "\n")
	assertOutline(t, parseTextForTest(t, text, true),
		"# Северный ветер",
		"## Утро",
		"Марта *проснулась* до *рассвета*, когда над **гаванью** ещё висел **туман**.",
		"У причала",
		"Она прошла мимо таверны «Якорь» и свернула к маяку.",
		"Море помнит всё, говорил отец.",
		"первое — сеть,",
		"второе — фонарь,",
		"третье — нож.",
		"\\* \\* \\*",
		"Слово со сноской и кодом, звёздочка \\* и подчёркивание snake_case.",
		"## Глава 2",
		"**Глава** без \\*пары",
	)
	// The only heading at the top names the book: the book is one chapter.
	assertOutline(t, parseTextForTest(t, "# Северный ветер\n\nМарта проснулась.\n\nОна вышла.\n", true),
		"# Северный ветер", "## ", "Марта проснулась.", "Она вышла.", "(unsplit)")
	// A Markdown cut by width: a block is a paragraph, its faces read over its lines.
	assertOutline(t, parseTextForTest(t, "# Глава 1\n\nМарта проснулась до рассвета, когда *над\nгаванью* ещё висел туман и чайки молчали.\n\nОна оделась, взяла фонарь и вышла на\nулицу, стараясь не разбудить отца.\n", true),
		"# ", "## Глава 1",
		"Марта проснулась до рассвета, когда *над гаванью* ещё висел туман и чайки молчали.",
		"Она оделась, взяла фонарь и вышла на улицу, стараясь не разбудить отца.")
}

// K2.6: the book limits are counted as the text is read; a file without words has no content.
func TestParseTextHoldsTheBookLimits(t *testing.T) {
	var chapters strings.Builder
	for number := 1; number <= maxImportedChapters+1; number++ {
		fmt.Fprintf(&chapters, "Глава %d\nТекст.\n", number)
	}
	if _, err := ParseText([]byte(chapters.String()), false); !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("too many chapters error = %v", err)
	}
	long := strings.Repeat(strings.Repeat("я", 1000)+"\n", maxImportedTextRunes/1000+1)
	if _, err := ParseText([]byte(long), false); !errors.Is(err, ErrImportTooLarge) {
		t.Fatalf("too large error = %v", err)
	}
	for _, empty := range []string{"", "\n\n  \n", "\ufeff"} {
		if _, err := ParseText([]byte(empty), false); !errors.Is(err, ErrImportNoContent) {
			t.Fatalf("ParseText(%q) error = %v, want no content", empty, err)
		}
	}
}
