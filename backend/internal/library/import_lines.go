package library

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
	"canonkeeper/backend/internal/domain/textface"
)

// maxTitleLineRunes is how long a line that names a chapter or a book may be (specs/2026-11-10-book-file-kinds.md
// K2.3, K2.4): a longer one is prose.
const maxTitleLineRunes = 120

// chapterTitlePattern is the start of a line that names a chapter by itself: «Глава 618. Колизей», «Глава XII»,
// «Пролог», «Эпилог: …».
var chapterTitlePattern = regexp.MustCompile(`^(?:глава\s+(?:\d+|[ivxlcdm]+)|пролог|эпилог|интерлюдия)(?:$|[\s.:,)—–-])`)

// chapterTitleLine says a paragraph is a line that names a chapter (K2.3): short, one line, and it starts as a title
// does; prose like «Глава гильдии кивнул.» is not a title. Word without headings, a text, an RTF and an ODT find their
// chapters by it.
func chapterTitleLine(text string) bool {
	text = strings.ToLower(text)
	return utf8.RuneCountInString(text) <= maxTitleLineRunes && !strings.ContainsAny(text, "\n\t") &&
		chapterTitlePattern.MatchString(text)
}

// bookLine is a paragraph of a book file that marks its chapters by its lines — a text, an RTF, an ODT (K2–K4): its
// words in the runs of their faces, none for a blank line, and whether the file marks it as a heading of the first
// level.
type bookLine struct {
	runs    []chapterfile.Run
	heading bool
}

// bookLineScan passes the paragraphs of a file to visit in their order, the blank ones too. bookOfLines reads a file
// twice through it: once to learn where its chapters start and what names the book, once to build the chapters.
type bookLineScan func(visit func(bookLine) error) error

// tidy is a paragraph as the import keeps it: no spaces at its edges, a face neither starts nor ends with a space.
func (line bookLine) tidy() (bookLine, string) {
	line.runs = textface.SpacesOutside(textface.Trimmed(line.runs))
	return line, chapterfile.Paragraph{Runs: line.runs}.Text()
}

// startsChapter says a paragraph names a chapter (K2.3, K3.2, K4.1): a heading of the first level short enough to be a
// title, or a line that names a chapter.
func (line bookLine) startsChapter(text string) bool {
	return line.heading && utf8.RuneCountInString(normalizeImportText(text)) <= maxTitleLineRunes || chapterTitleLine(text)
}

// bookOfLines splits the paragraphs of a file into chapters (K2.3, K2.4, K3.2, K4.2). A chapter starts at a heading of
// the first level or at a line that names a chapter; the text before the first one is a chapter without a title at the
// start of the book, as in Word, and a file that names no chapter is one chapter named as the book (Unsplit). The first
// paragraph names the book when it is short and does not name a chapter itself: a heading when a chapter starts right
// after it or it is the only heading of the file, a line of prose when it does not read as a sentence and a blank line
// or a chapter follows it. The book limits are counted as the chapters are built.
func bookOfLines(scan bookLineScan) (BookImport, error) {
	var (
		starts, seen  int
		first, second bookLine
		blankAfter    bool
	)
	err := scan(func(line bookLine) error {
		line, text := line.tidy()
		if text == "" {
			blankAfter = blankAfter || seen == 1
			return nil
		}
		if line.startsChapter(text) {
			starts++
		}
		switch seen {
		case 0:
			first = line
		case 1:
			second = line
		}
		seen++
		return nil
	})
	if err != nil {
		return BookImport{}, err
	}
	var book BookImport
	if seen >= 2 {
		book.Title = bookTitleLine(first, second, blankAfter, starts)
	}
	if book.Title != "" && first.heading {
		starts--
	}
	skipTitle := book.Title != ""
	var limits importLimits
	err = scan(func(line bookLine) error {
		line, text := line.tidy()
		if text == "" {
			return nil
		}
		if skipTitle {
			skipTitle = false
			return nil
		}
		if starts > 0 && line.startsChapter(text) {
			book.Chapters = append(book.Chapters, ChapterImport{Title: normalizeImportText(text)})
			return nil
		}
		if len(book.Chapters) == 0 {
			book.Chapters = append(book.Chapters, ChapterImport{})
		}
		chapter := &book.Chapters[len(book.Chapters)-1]
		if len(chapter.Paragraphs) == 0 {
			if err := limits.addChapter(); err != nil {
				return err
			}
		}
		if err := limits.addParagraph(text); err != nil {
			return err
		}
		chapter.Paragraphs = append(chapter.Paragraphs, chapterfile.Paragraph{Runs: line.runs})
		return nil
	})
	if err != nil {
		return BookImport{}, err
	}
	if limits.chapters == 0 {
		return BookImport{}, ErrImportNoContent
	}
	book.Unsplit = starts == 0
	return book, nil
}

// bookTitleLine is the title of the book its first paragraph gives (K2.4), or "".
func bookTitleLine(first, second bookLine, blankAfter bool, starts int) string {
	_, text := first.tidy()
	title := normalizeImportText(text)
	if utf8.RuneCountInString(title) > maxTitleLineRunes || chapterTitleLine(title) {
		return ""
	}
	_, next := second.tidy()
	if first.heading {
		if second.startsChapter(next) || starts == 1 {
			return title
		}
		return ""
	}
	if !readsAsSentence(title) && (second.startsChapter(next) || blankAfter) {
		return title
	}
	return ""
}

// readsAsSentence says a short line is prose rather than a title: it ends as a part of a sentence does, or it is a line
// of a dialogue.
func readsAsSentence(line string) bool {
	if strings.HasPrefix(line, "—") || strings.HasPrefix(line, "–") || strings.HasPrefix(line, "-") {
		return true
	}
	return !strings.HasSuffix(line, "...") && strings.ContainsAny(line[len(line)-1:], ".,;:")
}
