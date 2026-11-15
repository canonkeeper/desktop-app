package sagamemory

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The number a chapter title starts with: «Глава 12. Белый порт», «Глава XIV — Письмо», «12. Белый порт», with a fraction
// too — «Глава 581.1: Переход», «12.5. Белый порт» (one fraction: «12.05.2024. Отъезд» is a date, not a number).
var (
	titledNumber  = regexp.MustCompile(`^(?i:глава)\s+(\d+(?:\.\d+)?|[IVXLCDM]+)(?:\s*[.:—–-]\s*|\s+|$)(.*)$`)
	numberedTitle = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*[.)]\s+(.+)$`)
)

// TitledNumber is the number a chapter title writes — «Глава 12. Белый порт», «Глава XIV — Письмо», «12. Белый порт»,
// «Глава 581.1: Переход» — and the title without it, its spaces collapsed; a title that is only its number («Глава 12.»)
// leaves no title, a title without a number gives no number and the whole title. It is the one rule of the number of a
// chapter (specs/2026-11-15-reader-chapter-number.md RN1.1): the place, the reader, the search of chapters and «Недавно
// удалённые» read it, and none of them, the page and the mock of the web keep a rule of their own.
func TitledNumber(title string) (number, name string) {
	name = strings.Join(strings.Fields(title), " ")
	if match := titledNumber.FindStringSubmatch(name); match != nil {
		return match[1], strings.TrimSpace(match[2])
	}
	if match := numberedTitle.FindStringSubmatch(name); match != nil {
		return match[1], strings.TrimSpace(match[2])
	}
	return "", name
}

// NumberedTitle is the number of a living chapter and its title without that number, as the reader, the search of
// chapters and the place show them: the number its title writes (TitledNumber) or else inBook, its place among the living
// chapters of its book from one and without gaps (specs/2026-11-15-reader-chapter-number.md RN1.2).
func NumberedTitle(title string, inBook int) (number, name string) {
	number, name = TitledNumber(title)
	if number == "" {
		number = strconv.Itoa(inBook)
	}
	return number, name
}

// Place names the chapter as the author reads it everywhere on the screen — «Книга 2, гл. 12 „Белый порт“»
// (specs/2026-11-10-ux1-names.md N1.1): the order of its book in the saga, the number its title writes or its place in
// the book (InBook, from one — specs/2026-11-14-chapter-place-number.md CP1.1), and the title without that number, by the
// rule the reader reads too (NumberedTitle). The key is the address of the model and is never part of it.
func (chapter *Chapter) Place() string {
	number, name := NumberedTitle(chapter.Title, chapter.InBook)
	place := fmt.Sprintf("Книга %d, гл. %s", chapter.BookOrder, number)
	if name != "" {
		place += " „" + name + "“"
	}
	return place
}

// PlacesInBooks gives every chapter its place among the chapters of its book (InBook) — the one rule of the number of a
// place (specs/2026-11-14-chapter-place-number.md CP1.1): from one and without gaps in the order of the chapters, whatever
// the stored order is — from zero, as a publication normalizes it, from one, or with the gap of a deleted chapter —, the
// number the search of chapters shows. The chapters come in any order, all of the text of their books; each book counts
// from its own one, so every chapter must carry its BookID: chapters without one are counted as one book. The operator
// build of cards (operator/chaptercards) and the copies of a saga of the lab (sagalab/moment) build their chapters
// without a book and read only their keys and labels, never a place — places_test.go of operator/chaptercards holds it.
// Order, the keys and the order of the slice stay.
func PlacesInBooks(chapters []Chapter) {
	reading := make([]int, len(chapters))
	for i := range reading {
		reading[i] = i
	}
	sort.SliceStable(reading, func(i, j int) bool {
		a, b := &chapters[reading[i]], &chapters[reading[j]]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
	counted := map[string]int{}
	for _, i := range reading {
		counted[chapters[i].BookID]++
		chapters[i].InBook = counted[chapters[i].BookID]
	}
}

// PlaceAt is the place with paragraphs first..last in words: «…, абзац 3», «…, абзацы 3–5» (N1.2); first 0 — the
// chapter as a whole.
func (chapter *Chapter) PlaceAt(first, last int) string {
	return placeAt(chapter.Place(), first, last)
}

// AtParagraph is a place with one paragraph in words, «…, абзац 4», for a reader that keeps the place of a chapter but
// not the chapter (specs/2026-11-12-ux12-writing.md WR1.2); paragraph 0 — the chapter as a whole.
func AtParagraph(place string, paragraph int) string {
	return placeAt(place, paragraph, paragraph)
}

func placeAt(place string, first, last int) string {
	switch {
	case first <= 0:
		return place
	case last > first:
		return fmt.Sprintf("%s, абзацы %d–%d", place, first, last)
	default:
		return fmt.Sprintf("%s, абзац %d", place, first)
	}
}

// MissingPlace stands for a chapter the text of the saga does not have.
const MissingPlace = "глава, которой нет в книге"

// sourceLink is a link to a place of the book in a text, `[гл. 14 ¶33](source:14/33)`: its key and paragraphs.
var sourceLink = regexp.MustCompile(`\[[^\]\n]*\]\(source:([^)\s/]+)(?:/(\d+)(?:[-–](\d+))?)?\)`)

// LinkedKeys are the chapter keys of the source links of a text.
func LinkedKeys(text string) []string {
	var keys []string
	for _, match := range sourceLink.FindAllStringSubmatch(text, -1) {
		keys = append(keys, match[1])
	}
	return keys
}

// PlainLinks says the source links of a text in the words of the author (N3.4): each becomes the place of its chapter
// with the paragraphs, «Книга 1, гл. XIV, абзацы 33–35», by the places of the keys; a key without a place is a chapter
// the book does not have.
func PlainLinks(text string, places map[string]string) string {
	return sourceLink.ReplaceAllStringFunc(text, func(link string) string {
		match := sourceLink.FindStringSubmatch(link)
		place := places[match[1]]
		if place == "" {
			place = MissingPlace
		}
		first, _ := strconv.Atoi(match[2])
		last, _ := strconv.Atoi(match[3])
		return placeAt(place, first, last)
	})
}

// Places are the places of the chapters the keys name, by the key as it was written; a key the version has no chapter
// for is left out (N1.3).
func (version *Version) Places(keys []string) map[string]string {
	places := map[string]string{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if chapter, ok := version.Find(key); ok {
			places[key] = chapter.Place()
		}
	}
	return places
}
