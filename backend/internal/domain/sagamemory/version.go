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
	BookOrder   int
	BookTitle   string
	Order       int
	Title       string
	Number      int    // global chapter number written in the title, 0 when absent
	Key         string // stable agent-facing reference, assigned by NewVersion
	Index       int    // position in reading order, assigned by NewVersion
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

// NewVersion orders chapters by book and chapter order, numbers and normalizes paragraphs and
// assigns chapter keys: the global number when it is unique in the version, otherwise
// "b<book order>c<chapter order>".
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
			chapter.Key = fmt.Sprintf("b%dc%d", chapter.BookOrder, chapter.Order)
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
	return version
}

// Find resolves a chapter key such as "481", "гл. 481" or "b3c50".
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
