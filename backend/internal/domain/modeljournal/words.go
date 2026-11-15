// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"canonkeeper/backend/internal/domain/disclosure"
)

// PlaceState is where a chapter a sending named stands now.
type PlaceState string

const (
	// PlaceLive: the chapter and its book are in the books.
	PlaceLive PlaceState = "live"
	// PlaceChapterDeleted: the chapter is in «Недавно удалённые», its book is not.
	PlaceChapterDeleted PlaceState = "chapter_deleted"
	// PlaceBookDeleted: the book of the chapter is in «Недавно удалённые».
	PlaceBookDeleted PlaceState = "book_deleted"
)

// ChapterPlace is a chapter as it stands now (JR2.3): its state, the place of its living book on the shelf or the title of
// its deleted book, and the number and the title without the number by the one rule of the reader
// (sagamemory.NumberedTitle; a deleted chapter has only the number its title writes). A chapter the places do not hold is
// no more.
type ChapterPlace struct {
	State     PlaceState `json:"state"`
	BookID    string     `json:"bookId"`
	Book      int        `json:"book,omitempty"`
	BookTitle string     `json:"bookTitle,omitempty"`
	Number    string     `json:"number,omitempty"`
	Title     string     `json:"title,omitempty"`
}

// AttachmentPlace is a chapter attached to a question as it stands now: its title, or gone with its chat.
type AttachmentPlace struct {
	Title string `json:"title,omitempty"`
	Gone  bool   `json:"gone,omitempty"`
}

// Places are the places the words of a row read now, by the id of a chapter and by the chat and the number of an
// attachment («<chat>/<number>»).
type Places struct {
	Chapters    map[string]ChapterPlace    `json:"chapters"`
	Attachments map[string]AttachmentPlace `json:"attachments"`
}

// AttachmentKey is the key of an attachment in Places.
func AttachmentKey(chat string, number int) string { return chat + "/" + strconv.Itoa(number) }

// shownInFull is how many places a group names by their labels; more go by books, counted with their precision (JR2.3).
const shownInFull = 3

// Paging is how a reading of a program of the author went by pages (specs/2026-11-15-desk-mcp.md MC4.4): a sending knows its
// page K of N; a row of a day knows only that one of its answers went by pages.
type Paging struct {
	Page  int
	Pages int
	Paged bool
}

// sending says the words are of one sending of an answer of several pages: what went is named as of an answer of one page,
// and the page is a segment of its own.
func (paging Paging) sending() bool { return paging.Pages > 1 }

// WhatWent is «что ушло» of a row in the words of the author (JR2.3, Data): the new of all its sendings, every place by
// its label of now, a place once at its larger precision, up to three labels in a group and by books beyond. A question
// without anything new is the previous conversation of its chat; a check names no text of the author — for a check,
// named.Protocol says every sending of the row was the probe of the protocol («Определить» inside a work). A reading of a
// program without the text of the books is «ничего из книг». The pages of an answer (MC4.4): a sending of an answer of
// several pages names what went as of one page and ends with its own segment «· страница K из N ответа»; a row of a day names
// the text of the chapters given only by pages «страницами ответа», and when one of its answers went by pages and it names
// anything else that could — titles, cards, counts of records, threads, titles of sagas —, it ends with «; часть ответов
// отдана страницами» once.
func WhatWent(kind Kind, named disclosure.Named, places Places, paging Paging) string {
	if kind == KindCheck {
		if named.Protocol && named.Empty() {
			return "определение протокола, без вашего текста"
		}
		return "без вашего текста"
	}
	var parts []string
	if named.Question {
		parts = append(parts, "ваш вопрос")
	}
	if named.Correction {
		parts = append(parts, "ваше уточнение")
	}
	var text, titles, cards []disclosure.Chapter
	for _, chapter := range named.Chapters {
		switch chapter.Precision {
		case disclosure.Titles:
			titles = append(titles, chapter)
		case disclosure.Cards:
			cards = append(cards, chapter)
		default:
			text = append(text, chapter)
		}
	}
	if len(text) > 0 {
		parts = append(parts, chapterWords(text, places, true, paging)...)
	}
	for _, attachment := range named.Attachments {
		place, known := places.Attachments[AttachmentKey(attachment.Chat, attachment.Number)]
		if !known || place.Gone {
			parts = append(parts, "приложенная глава удалённого чата")
			continue
		}
		title := "без названия"
		if place.Title != "" {
			title = place.Title
		}
		parts = append(parts, "приложенная глава «"+title+"», "+paragraphWords(attachment.Paragraphs))
	}
	if len(titles) > 0 {
		parts = append(parts, "названия глав: "+strings.Join(chapterWords(titles, places, false, paging), "; "))
	}
	if len(cards) > 0 {
		parts = append(parts, "что помощник узнал о главах: "+strings.Join(chapterWords(cards, places, false, paging), "; "))
	}
	if named.Threads {
		parts = append(parts, "нити саги")
	}
	if count := len(named.Memory); count > 0 {
		parts = append(parts, fmt.Sprintf("записи «Вы просили помнить»: %d", count))
	}
	if count := len(named.Materials); count > 0 {
		parts = append(parts, fmt.Sprintf("ваши материалы: %d %s", count, plural(count, "запись", "записи", "записей")))
	}
	if named.Names > 0 {
		parts = append(parts, fmt.Sprintf("список имён саги: %d %s", named.Names, plural(named.Names, "имя", "имени", "имён")))
	}
	if len(named.Sections) > 0 {
		parts = append(parts, "файл или текст для «Ваших материалов»: "+sectionWords(named.Sections))
	}
	if named.Sagas > 0 {
		parts = append(parts, fmt.Sprintf("названия саг: %d", named.Sagas))
	}
	words := strings.Join(parts, "; ")
	switch {
	case len(parts) == 0 && kind == KindProgram:
		words = "ничего из книг"
	case len(parts) == 0:
		return "прежний разговор этого чата"
	}
	if paging.sending() {
		return words + fmt.Sprintf(" · страница %d из %d ответа", paging.Page, paging.Pages)
	}
	// What else a row names could have gone by the pages of one of its answers: the row says so once.
	if paging.Paged && (len(titles) > 0 || len(cards) > 0 || named.Threads || len(named.Memory) > 0 || len(named.Materials) > 0 || named.Sagas > 0) {
		return words + "; часть ответов отдана страницами"
	}
	return words
}

// group is where a chapter stands for the words by books: a living book by its place, the chapters of a living book in
// «Недавно удалённые», a deleted book by its title, or the chapters that are no more.
type group struct {
	order int
	book  int
	name  string
}

func groupOf(place ChapterPlace, known bool) group {
	switch {
	case !known:
		return group{order: 3, name: "gone"}
	case place.State == PlaceBookDeleted:
		return group{order: 2, name: "книга «" + place.BookTitle + "» (в «Недавно удалённых»)"}
	case place.State == PlaceChapterDeleted:
		return group{order: 1, book: place.Book, name: fmt.Sprintf("Книга %d (в «Недавно удалённых»)", place.Book)}
	default:
		return group{order: 0, book: place.Book, name: fmt.Sprintf("Книга %d", place.Book)}
	}
}

// chapterWords are the words of the chapters of one group: their labels, up to three, or the books with the count of the
// chapters of each precision; withPrecision says the text of the chapters went, not their titles or cards.
func chapterWords(chapters []disclosure.Chapter, places Places, withPrecision bool, paging Paging) []string {
	gone := 0
	for _, chapter := range chapters {
		if _, known := places.Chapters[chapter.ID]; !known {
			gone++
		}
	}
	shown := len(chapters) - gone
	if gone > 0 {
		shown++
	}
	if shown <= shownInFull {
		var words []string
		for _, chapter := range chapters {
			place, known := places.Chapters[chapter.ID]
			if !known {
				continue
			}
			words = append(words, labelOf(place, chapter, withPrecision, paging))
		}
		if gone > 0 {
			words = append(words, goneWords(gone))
		}
		return words
	}
	type key struct {
		group     group
		precision disclosure.Precision
	}
	counts := map[key]int{}
	var groups []group
	for _, chapter := range chapters {
		place, known := places.Chapters[chapter.ID]
		at := groupOf(place, known)
		if !slices.Contains(groups, at) {
			groups = append(groups, at)
		}
		precision := chapter.Precision
		if !withPrecision || precision == disclosure.Pages && paging.sending() {
			precision = ""
		}
		counts[key{at, precision}]++
	}
	slices.SortStableFunc(groups, func(a, b group) int {
		if a.order != b.order {
			return a.order - b.order
		}
		if a.book != b.book {
			return a.book - b.book
		}
		return strings.Compare(a.name, b.name)
	})
	var words []string
	for _, at := range groups {
		var counted []string
		for _, precision := range []disclosure.Precision{disclosure.Whole, disclosure.Pages, disclosure.Beginning, disclosure.Paragraphs, disclosure.Excerpts, ""} {
			count := counts[key{at, precision}]
			if count == 0 {
				continue
			}
			counted = append(counted, countedWords(count, precision))
		}
		if at.name == "gone" {
			words = append(words, goneWords(gone))
			continue
		}
		words = append(words, at.name+" — "+listWords(counted))
	}
	return words
}

// countedWords are the chapters of a book counted with what of them went: «40 глав целиком», «1 глава абзацами», «3 главы
// отрывками», «начала 2 глав»; titles and cards go without a precision — «40 глав».
func countedWords(count int, precision disclosure.Precision) string {
	chapters := fmt.Sprintf("%d %s", count, plural(count, "глава", "главы", "глав"))
	switch precision {
	case disclosure.Whole:
		return chapters + " целиком"
	case disclosure.Pages:
		return chapters + " страницами ответа"
	case disclosure.Paragraphs:
		return chapters + " абзацами"
	case disclosure.Excerpts:
		return chapters + " отрывками"
	case disclosure.Beginning:
		if count == 1 {
			return "начало 1 главы"
		}
		return fmt.Sprintf("начала %d %s", count, plural(count, "главы", "глав", "глав"))
	default:
		return chapters
	}
}

// listWords join the counts of a book: «A», «A и B», «A, B и C».
func listWords(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " и " + parts[len(parts)-1]
}

func goneWords(count int) string {
	if count == 1 {
		return "глава, которой уже нет в книге"
	}
	return fmt.Sprintf("%d %s, которых уже нет в книге", count, plural(count, "глава", "главы", "глав"))
}

// labelOf is the label of one chapter of now with what of it went: «Книга 2, гл. 12 „Белый порт“, абзацы 3–9».
func labelOf(place ChapterPlace, chapter disclosure.Chapter, withPrecision bool, paging Paging) string {
	name := "гл. " + place.Number
	if place.Number == "" {
		name = "глава"
	}
	if place.Title != "" {
		name += " „" + place.Title + "“"
	}
	label := fmt.Sprintf("Книга %d, %s", place.Book, name)
	if place.State == PlaceBookDeleted {
		label = name + " книги «" + place.BookTitle + "»"
	}
	if withPrecision {
		label += precisionSuffix(chapter.Precision, chapter.Paragraphs, paging)
	}
	if place.State != PlaceLive {
		label += " (в «Недавно удалённых»)"
	}
	return label
}

func precisionSuffix(precision disclosure.Precision, paragraphs []disclosure.Range, paging Paging) string {
	switch precision {
	case disclosure.Whole:
		return " целиком"
	case disclosure.Pages:
		// A sending says its page in a segment of its own; a row says the chapter went by the pages of an answer.
		if paging.sending() {
			return ""
		}
		return ", страницами ответа"
	case disclosure.Beginning:
		return ", начало"
	case disclosure.Excerpts:
		return ", отрывки"
	case disclosure.Paragraphs:
		if len(paragraphs) == 0 {
			return ", абзацы"
		}
		return ", " + paragraphWords(paragraphs)
	default:
		return ""
	}
}

// paragraphWords are «абзац 5», «абзацы 3–9», «абзацы 3, 7–9».
func paragraphWords(paragraphs []disclosure.Range) string {
	if len(paragraphs) == 1 && paragraphs[0].From == paragraphs[0].To {
		return "абзац " + strconv.Itoa(paragraphs[0].From)
	}
	ranges := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		ranges[i] = rangeWords(paragraph.From, paragraph.To)
	}
	return "абзацы " + strings.Join(ranges, ", ")
}

func rangeWords(from, to int) string {
	if from == to {
		return strconv.Itoa(from)
	}
	return strconv.Itoa(from) + "–" + strconv.Itoa(to)
}

// sectionWords are «раздел 3», «разделы 1–3, 5».
func sectionWords(sections []int) string {
	sorted := slices.Clone(sections)
	slices.Sort(sorted)
	if len(sorted) == 1 {
		return "раздел " + strconv.Itoa(sorted[0])
	}
	var ranges []string
	for i := 0; i < len(sorted); {
		j := i
		for j+1 < len(sorted) && sorted[j+1] == sorted[j]+1 {
			j++
		}
		ranges = append(ranges, rangeWords(sorted[i], sorted[j]))
		i = j + 1
	}
	return "разделы " + strings.Join(ranges, ", ")
}

// plural is the Russian form of a noun for the number: 1 глава, 2 главы, 5 глав, 11 глав, 21 глава.
func plural(count int, one, few, many string) string {
	lastTwo := count % 100
	switch lastOne := lastTwo % 10; {
	case lastTwo >= 11 && lastTwo <= 14:
		return many
	case lastOne == 1:
		return one
	case lastOne >= 2 && lastOne <= 4:
		return few
	}
	return many
}
