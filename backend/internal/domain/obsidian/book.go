// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// chapterTitlePattern is the start of a line the import of a book reads as the title of a chapter by itself
// (library.chapterTitleLine, specs/2026-11-10-book-file-kinds.md K2.3): the title of a book must not read so, or the
// import would take it for a chapter.
var chapterTitlePattern = regexp.MustCompile(`^(?:глава\s+(?:\d+|[ivxlcdm]+)|пролог|эпилог|интерлюдия)(?:$|[\s.:,)—–-])`)

var (
	headingLine   = regexp.MustCompile(`^(#{1,6})(?:\s+(.*?))?\s*#*\s*$`)
	leadingNumber = regexp.MustCompile(`^\d+(?:[.\-_)]\s*|\s+)`)
)

// GluedNote is a note of a book in its place: its title, where its place comes from and the titles of the chapters it
// gives — its title, or the headings of a book in a note.
type GluedNote struct {
	Path     string
	Title    string
	OrderBy  OrderSource
	Chapters []string
	InNote   bool
}

// Glued is a folder of notes as one text of Markdown the import of a book reads (OB3.3), and what the preview says of it.
type Glued struct {
	Title    string
	TitleCut bool
	// FileName is the name of the file of the volume: the title without slashes, with .md.
	FileName    string
	Notes       []GluedNote
	Text        string
	Fingerprint string
	Dropped     Dropped
	Skipped     Skipped
	// Cut are the notes whose title was cut; OrderByName says the notes stand only by names without numbers.
	Cut         []string
	OrderByName bool
}

// Glue makes the notes of a folder one text of Markdown the import reads into the book (OB3.3): the first line is
// «# <название книги>» — book or title of the header of _book.md, otherwise the name of the folder —, then the notes in
// their order, each under the heading of the first level of its title with its own headings one level lower and its
// first heading gone when it repeats the title. A note whose headings of its upper level stand at least twice is a book
// in a note: those headings are its chapters — in a folder of several notes only headings of the first level, which a
// chapter of a note does not use for its scenes. _book.md, and the note of the name of the folder that links other notes
// of it, are its index: they order the notes and are no chapter. A note without words is skipped. notes are read and
// chosen by the caller — what other choices hold and what the author left out are not among them.
func Glue(folder, folderName string, notes []Note) Glued {
	glued := Glued{Skipped: Skipped{}}
	var bookNote, folderNote *Note
	var chapters []Note
	for index := range notes {
		note := notes[index]
		direct := parentOf(note.Path) == folder || folder == Root && !strings.Contains(note.Path, "/")
		switch {
		case direct && strings.EqualFold(Base(note.Path), BookNote):
			bookNote = &notes[index]
		case direct && strings.EqualFold(note.Name, NFC(folderName)) && folderNote == nil:
			folderNote = &notes[index]
			chapters = append(chapters, note)
		default:
			chapters = append(chapters, note)
		}
	}
	index := bookNote
	if index == nil && folderNote != nil && linksAnother(*folderNote, notes) {
		index = folderNote
		chapters = withoutNote(chapters, folderNote.Path)
	}
	glued.Title, glued.TitleCut = bookTitle(folderName, bookNote)
	glued.FileName = strings.NewReplacer("/", "-", `\`, "-").Replace(glued.Title) + ".md"

	type readied struct {
		note  Note
		lines []string
	}
	ready := make([]readied, 0, len(chapters))
	for _, note := range chapters {
		cleaned := Clean(note.Body, false)
		glued.Dropped.Add(cleaned.Dropped)
		glued.Dropped.FrontKeys += note.Front.Skipped
		if !hasWords(cleaned.Text) {
			glued.Skipped[SkipEmpty]++
			continue
		}
		note.Body = cleaned.Text
		ready = append(ready, readied{note: note, lines: strings.Split(cleaned.Text, "\n")})
	}
	ordered := make([]Note, len(ready))
	byPath := map[string][]string{}
	for place, item := range ready {
		ordered[place] = item.note
		byPath[item.note.Path] = item.lines
	}
	ordered, sources := orderNotes(ordered, index)

	var text strings.Builder
	text.WriteString("# " + escapeTitle(glued.Title) + "\n")
	single := len(ordered) == 1
	numbered := false
	for place, note := range ordered {
		title, cut := noteTitle(note)
		if cut {
			glued.Cut = append(glued.Cut, note.Path)
		}
		if strings.ContainsFunc(note.Name, unicode.IsDigit) {
			numbered = true
		}
		emitted := emitNote(&text, title, note, byPath[note.Path], single)
		emitted.Path, emitted.OrderBy = note.Path, sources[place]
		glued.Notes = append(glued.Notes, emitted)
	}
	glued.OrderByName = len(ordered) >= 2 && !numbered
	for _, source := range sources {
		if source != OrderName {
			glued.OrderByName = false
		}
	}
	glued.Text = text.String()
	sum := sha256.Sum256([]byte(glued.Text))
	glued.Fingerprint = hex.EncodeToString(sum[:])
	return glued
}

func withoutNote(notes []Note, notePath string) []Note {
	kept := notes[:0:0]
	for _, note := range notes {
		if note.Path != notePath {
			kept = append(kept, note)
		}
	}
	return kept
}

// bookTitle is the title of a book (OB3.3): book or title of the header of _book.md, otherwise the name of the folder;
// up to MaxTitleRunes, and in quotes when it would read as the title of a chapter.
func bookTitle(folderName string, bookNote *Note) (string, bool) {
	title := ""
	if bookNote != nil {
		for _, key := range []string{"book", "title"} {
			if value, ok := bookNote.Front.Get(key); ok && !value.IsList && cleanTitle(value.Text) != "" {
				title = cleanTitle(value.Text)
				break
			}
		}
	}
	if title == "" {
		title = cleanTitle(folderName)
	}
	if title == "" {
		title = "Без названия"
	}
	title, cut := cutRunes(title, MaxTitleRunes-2)
	if chapterTitlePattern.MatchString(strings.ToLower(title)) || sceneBreakTitle(title) {
		title = "«" + title + "»"
	}
	return title, cut
}

// sceneBreakSigns are the signs a line of a break between scenes is made of (library, isSceneBreak): a heading of them
// alone is read as that break, not as a heading.
const sceneBreakSigns = "*-_=~#•·+ "

// sceneBreakTitle says a title is made of the signs of a break between scenes alone.
func sceneBreakTitle(title string) bool {
	return strings.Trim(title, sceneBreakSigns) == ""
}

// noteTitle is the title of a note (OB3.3): title of its header, otherwise its name without .md and without its leading
// number; up to MaxTitleRunes.
func noteTitle(note Note) (string, bool) {
	name := ""
	if value, ok := note.Front.Get("title"); ok && !value.IsList {
		name = cleanTitle(value.Text)
	}
	if name == "" {
		name = cleanTitle(note.Name)
		if rest := leadingNumber.ReplaceAllString(name, ""); strings.TrimSpace(rest) != "" {
			name = strings.TrimSpace(rest)
		}
	}
	if sceneBreakTitle(name) {
		// A title of the signs of a break between scenes alone goes in quotes, or the import reads a break there.
		name, cut := cutRunes(name, MaxTitleRunes-2)
		return "«" + name + "»", cut
	}
	return cutTitle(name)
}

// escapeTitle keeps the signs of a title the import would read as the signs of Markdown: the signs it reads escaped, and
// a «#» at the end — the import takes the closing «#» of a heading off. A «>» inside a heading is no sign — escaped, it
// would break an address in angle brackets —; an address keeps its words without the brackets, a code without its
// backticks, which sameTitle does not tell apart.
func escapeTitle(title string) string {
	escaped := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "!", `\!`).Replace(title)
	if strings.HasSuffix(escaped, "#") {
		escaped += " #"
	}
	return escaped
}

// sameTitle says a heading repeats a title, its signs and case aside.
func sameTitle(heading string, titles ...string) bool {
	plain := func(value string) string {
		value = strings.Map(func(r rune) rune {
			if strings.ContainsRune("*_`[]<>\\", r) {
				return -1
			}
			return r
		}, value)
		return strings.ToLower(strings.Join(strings.Fields(value), " "))
	}
	for _, title := range titles {
		if title != "" && plain(heading) == plain(title) {
			return true
		}
	}
	return false
}

type heading struct {
	line  int
	level int
	text  string
}

// emitNote writes a note into the text of its book and says the chapters it gives.
func emitNote(text *strings.Builder, title string, note Note, lines []string, single bool) GluedNote {
	emitted := GluedNote{Title: title}
	var headings []heading
	fenced := false
	first := true
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fenceLine(trimmed) {
			fenced, first = !fenced, false
			continue
		}
		if fenced {
			continue
		}
		if trimmed == "" {
			continue
		}
		match := headingLine.FindStringSubmatch(trimmed)
		if match == nil || strings.TrimSpace(match[2]) == "" {
			first = false
			continue
		}
		// The first heading of a note that repeats its title, before any text of it, goes.
		stem := note.Name
		if first && sameTitle(match[2], title, stem, leadingNumber.ReplaceAllString(stem, "")) {
			lines[index] = ""
			first = false
			continue
		}
		first = false
		headings = append(headings, heading{line: index, level: len(match[1]), text: strings.TrimSpace(match[2])})
	}
	top, count := 7, 0
	for _, item := range headings {
		switch {
		case item.level < top:
			top, count = item.level, 1
		case item.level == top:
			count++
		}
	}
	emitted.InNote = count >= 2 && (top == 1 || single)
	shift := top - 1
	byLine := map[int]heading{}
	for _, item := range headings {
		byLine[item.line] = item
	}
	text.WriteString("\n")
	if !emitted.InNote || hasWords(strings.Join(lines[:firstTop(headings, top)], "\n")) {
		text.WriteString("# " + escapeTitle(title) + "\n\n")
		emitted.Chapters = append(emitted.Chapters, title)
	}
	for index, line := range lines {
		item, isHeading := byLine[index]
		if !isHeading {
			text.WriteString(line + "\n")
			continue
		}
		level := min(item.level+1, 6)
		if emitted.InNote {
			level = item.level - shift
			if item.level == top {
				emitted.Chapters = append(emitted.Chapters, item.text)
			}
		}
		text.WriteString(strings.Repeat("#", level) + " " + item.text + "\n")
	}
	return emitted
}

// firstTop is the line of the first heading of the upper level; a note without one has none, 0.
func firstTop(headings []heading, top int) int {
	for _, item := range headings {
		if item.level == top {
			return item.line
		}
	}
	return 0
}

// hasWords says a text holds a letter or a digit.
func hasWords(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) })
}
