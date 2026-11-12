package sagamemory

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The number a chapter title starts with: «Глава 12. Белый порт», «Глава XIV — Письмо», «12. Белый порт».
var (
	titledNumber  = regexp.MustCompile(`^(?i:глава)\s+(\d+|[IVXLCDM]+)(?:\s*[.:—–-]\s*|\s+|$)(.*)$`)
	numberedTitle = regexp.MustCompile(`^(\d+)\s*[.)]\s+(.+)$`)
)

// Place names the chapter as the author reads it everywhere on the screen — «Книга 2, гл. 12 „Белый порт“»
// (specs/2026-11-10-ux1-names.md N1.1): the order of its book in the saga, the number its title writes or its place in
// the book, and the title without that number. The key is the address of the model and is never part of it.
func (chapter *Chapter) Place() string {
	title := strings.Join(strings.Fields(chapter.Title), " ")
	number, name := strconv.Itoa(chapter.Order), title
	if match := titledNumber.FindStringSubmatch(title); match != nil {
		number, name = match[1], strings.TrimSpace(match[2])
	} else if match := numberedTitle.FindStringSubmatch(title); match != nil {
		number, name = match[1], strings.TrimSpace(match[2])
	}
	place := fmt.Sprintf("Книга %d, гл. %s", chapter.BookOrder, number)
	if name != "" {
		place += " „" + name + "“"
	}
	return place
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
