// Package sagamemory holds the saga text model used by the research agent: one text
// version of a project, normalized paragraphs, chapter keys and the paragraph index.
// It performs no I/O.
package sagamemory

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Paragraph is one block of a chapter revision; N is its 1-based position in the chapter,
// the number the agent cites and the reader highlights.
type Paragraph struct {
	N    int
	Text string
	Norm string
}

// Chapter is one chapter of the text version in reading order.
type Chapter struct {
	ID          string
	Revision    int64  // source revision whose paragraphs the chapter holds
	ContentHash string // text fingerprint of that revision; cards are keyed by it
	BookID      string
	// BookOrder is the place of the book on the shelf (shelf.Places: its rank among the living books, 1…n — RK1.6): it
	// orders the reading and names the book to the author — «Книга 2», «Том 2», «т. 2». BookKeyNumber is the number the
	// book got once in the saga, the number of its chapter keys: a move, a deletion and a return of books leave it
	// (specs/2026-11-11-ux7-reorder.md RK1.1).
	BookOrder     int
	BookKeyNumber int
	BookTitle     string
	Order         int
	Title         string
	Number        int    // global chapter number written in the title, 0 when absent
	Key           string // stable agent-facing reference, assigned by NewVersion
	Index         int    // position in reading order, assigned by NewVersion
	// ParagraphCount and Runes size the chapter text; a call loads Paragraphs only where it
	// reads them, so they come from the stored search text (NewOutline) or from the paragraphs.
	ParagraphCount int
	Runes          int
	Paragraphs     []Paragraph
}

// Label is the human-facing chapter name.
func (chapter *Chapter) Label() string {
	return fmt.Sprintf("Том %d, %s", chapter.BookOrder, strings.TrimSpace(chapter.Title))
}

// Cite names the chapter in answers as the author sees it: "гл. 481" when the key is the
// chapter's own number, otherwise its book and title.
func (chapter *Chapter) Cite() string {
	if chapter.Number > 0 && chapter.Key == strconv.Itoa(chapter.Number) {
		return "гл. " + chapter.Key
	}
	return fmt.Sprintf("т. %d, «%s»", chapter.BookOrder, strings.TrimSpace(chapter.Title))
}

// Ref names the chapter in tool output: its citation plus the key when the key is not the
// chapter number, so the agent can pass the key back to the tools.
func (chapter *Chapter) Ref() string {
	if cite := chapter.Cite(); strings.HasPrefix(cite, "гл. ") {
		return cite
	}
	return chapter.Cite() + " [" + chapter.Key + "]"
}

// Link is the Markdown source link answers use for paragraphs first..last of the chapter.
func (chapter *Chapter) Link(first, last int) string {
	if last > first {
		return fmt.Sprintf("[%s ¶%d–%d](source:%s/%d-%d)", chapter.Cite(), first, last, chapter.Key, first, last)
	}
	return fmt.Sprintf("[%s ¶%d](source:%s/%d)", chapter.Cite(), first, chapter.Key, first)
}

// Version is the whole text of one project version (draft or published) at one fingerprint.
type Version struct {
	ProjectID   string
	View        string
	Fingerprint string
	Chapters    []Chapter
	byKey       map[string]int
}

var chapterNumber = regexp.MustCompile(`(?i)^\s*глава\s+(\d+)`)

// ChapterNumber returns the global chapter number written at the start of a title.
func ChapterNumber(title string) int {
	match := chapterNumber.FindStringSubmatch(title)
	if match == nil {
		return 0
	}
	number, _ := strconv.Atoi(match[1])
	return number
}

// NewVersion orders chapters by the place of their book and their order in it, numbers and normalizes paragraphs
// and assigns chapter keys — the one rule of the key: the global number when it is unique in the version, otherwise
// "b<number of the book>c<chapter order>", so a key outlives a move of the volumes (specs/2026-11-11-ux7-reorder.md RK1).
func NewVersion(projectID, view, fingerprint string, chapters []Chapter) *Version {
	ordered := append([]Chapter(nil), chapters...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].BookOrder != ordered[j].BookOrder {
			return ordered[i].BookOrder < ordered[j].BookOrder
		}
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}
		return ordered[i].ID < ordered[j].ID
	})
	counts := map[int]int{}
	for i := range ordered {
		ordered[i].Number = ChapterNumber(ordered[i].Title)
		if ordered[i].Number > 0 {
			counts[ordered[i].Number]++
		}
	}
	version := &Version{ProjectID: projectID, View: view, Fingerprint: fingerprint, Chapters: ordered, byKey: map[string]int{}}
	for i := range ordered {
		chapter := &version.Chapters[i]
		chapter.Index = i
		if chapter.Number > 0 && counts[chapter.Number] == 1 {
			chapter.Key = strconv.Itoa(chapter.Number)
		} else {
			chapter.Key = fmt.Sprintf("b%dc%d", chapter.BookKeyNumber, chapter.Order)
		}
		for j := range chapter.Paragraphs {
			chapter.Paragraphs[j].N = j + 1
			chapter.Paragraphs[j].Norm = Normalize(chapter.Paragraphs[j].Text)
		}
		if len(chapter.Paragraphs) > 0 {
			chapter.ParagraphCount = len(chapter.Paragraphs)
			chapter.Runes = 0
			for _, paragraph := range chapter.Paragraphs {
				chapter.Runes += utf8.RuneCountInString(paragraph.Text)
			}
		}
		version.byKey[chapter.Key] = i
	}
	version.keepBookKeys()
	version.keepBareNumbers(counts)
	return version
}

// keepBookKeys keeps the key bKcM of a chapter keyed by its bare number readable (specs/2026-11-11-ux7-reorder.md RK1.5):
// it was its key while another chapter had the same number, and the links of answers kept it; the number of a book in the
// saga is never given twice, so bKcM names no other chapter.
func (version *Version) keepBookKeys() {
	for i := range version.Chapters {
		chapter := &version.Chapters[i]
		if chapter.BookKeyNumber <= 0 {
			continue
		}
		key := fmt.Sprintf("b%dc%d", chapter.BookKeyNumber, chapter.Order)
		if _, taken := version.byKey[key]; !taken {
			version.byKey[key] = i
		}
	}
}

// keepBareNumbers keeps the bare numbers that turned ambiguous readable (specs/2026-11-11-ux7-reorder.md RK1.5). «2» was
// the key of «Глава 2» while it was the only one in the saga, and the links of answers, the places of drawings and the
// rest kept it; once another chapter with that number comes in, the new keys of both are bKcM, and «2» still names the
// chapter with that number in the book with the lowest number in the saga (the earliest of them in it): a bare key could
// only be given before the books that came later. The keys the chapters show do not change.
func (version *Version) keepBareNumbers(counts map[int]int) {
	holders := map[int]int{}
	for i := range version.Chapters {
		chapter := &version.Chapters[i]
		if chapter.Number <= 0 || counts[chapter.Number] < 2 {
			continue
		}
		if held, ok := holders[chapter.Number]; !ok || earlierHolder(chapter, &version.Chapters[held]) {
			holders[chapter.Number] = i
		}
	}
	for number, i := range holders {
		version.byKey[strconv.Itoa(number)] = i
	}
}

// earlierHolder says whether a chapter holds a bare number before another: by the number of its book in the saga, then
// by its place in the book.
func earlierHolder(chapter, other *Chapter) bool {
	if chapter.BookKeyNumber != other.BookKeyNumber {
		return chapter.BookKeyNumber < other.BookKeyNumber
	}
	if chapter.Order != other.Order {
		return chapter.Order < other.Order
	}
	return chapter.ID < other.ID
}

// KeysOf are all the keys Find reads as the chapter: its own key and the kept ones (keepBookKeys, keepBareNumbers) — what
// a stored key of the chapter may be.
func (version *Version) KeysOf(chapter *Chapter) []string {
	keys := []string{}
	for key, index := range version.byKey {
		if index == chapter.Index {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// Find resolves a chapter key such as "481", "гл. 481" or "b3c50" — every reader of a kept key reads it here; a bare
// number that turned ambiguous names the chapter keepBareNumbers says, the bKcM of a chapter keyed by its number names it.
func (version *Version) Find(key string) (*Chapter, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, prefix := range []string{"глава", "гл.", "гл"} {
		key = strings.TrimSpace(strings.TrimPrefix(key, prefix))
	}
	index, ok := version.byKey[key]
	if !ok {
		return nil, false
	}
	return &version.Chapters[index], true
}
