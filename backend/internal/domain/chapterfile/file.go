// Package chapterfile reads a chapter of the author from his file — Word, plain text or Markdown, or a pasted text —
// into its paragraphs with their italic and bold, and writes it back as a Word document in which every change of the
// working text is a revision of Word (specs/2026-11-07-writing-proofread.md, PR1, PR6). It is the one reader of a
// chapter file: «Письмо» loads a chapter by it, and the chat that attaches a chapter reads it the same way. No I/O: bytes
// in, a document out.
package chapterfile

import (
	"errors"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The refusals of a chapter file.
var (
	// ErrFormat is a file of a kind the reader does not know.
	ErrFormat = errors.New("chapter file: unsupported format")
	// ErrTooLarge is a file over MaxFileBytes.
	ErrTooLarge = errors.New("chapter file: file too large")
	// ErrTooLong is a text over MaxTextRunes: a book rather than a chapter.
	ErrTooLong = errors.New("chapter file: text too long")
	// ErrUnreadable is a broken archive or markup.
	ErrUnreadable = errors.New("chapter file: unreadable")
	// ErrEmpty is a file without a word of text.
	ErrEmpty = errors.New("chapter file: no text")
)

// The limits of a chapter file (PR1.6): a Word file with pictures may be large, the pictures are not read; the text is
// as long as a working text of a draft may be.
const (
	MaxFileBytes = 10 << 20
	MaxTextRunes = 400000
	// maxPartBytes bounds an unpacked part of a Word file, as the import of books does: a small archive must not unpack
	// into gigabytes.
	maxPartBytes = 32 << 20
)

// Format is what a document was read from.
type Format string

// The formats of a chapter.
const (
	FormatDOCX     Format = "docx"
	FormatText     Format = "txt"
	FormatMarkdown Format = "md"
	// FormatPasted is a text pasted, not a file.
	FormatPasted Format = "text"
)

// Run is a piece of a paragraph in one face.
type Run struct {
	Text   string `json:"t"`
	Italic bool   `json:"i,omitempty"`
	Bold   bool   `json:"b,omitempty"`
}

// Paragraph is one paragraph of a chapter: its runs, its edges trimmed. An empty one keeps its place in the file.
type Paragraph struct {
	Runs []Run `json:"r,omitempty"`
}

// Text is the words of a paragraph without its faces.
func (paragraph Paragraph) Text() string {
	var text strings.Builder
	for _, run := range paragraph.Runs {
		text.WriteString(run.Text)
	}
	return text.String()
}

// Document is a chapter as the author's file holds it: the title of the chapter, when the file names it, and its
// paragraphs in their order, empty ones too.
type Document struct {
	Format     Format      `json:"format"`
	Title      *Paragraph  `json:"title,omitempty"`
	Paragraphs []Paragraph `json:"paragraphs"`
}

// TitleText is the title of the chapter the file names, or empty.
func (document Document) TitleText() string {
	if document.Title == nil {
		return ""
	}
	return document.Title.Text()
}

// Lines are the paragraphs with words, in order: what the working text of a draft is made of.
func (document Document) Lines() []string {
	var lines []string
	for _, paragraph := range document.Paragraphs {
		if text := paragraph.Text(); strings.TrimSpace(text) != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

// Text is the working text of a draft from the document: its paragraphs with words, a blank line between them (PR1.5).
func (document Document) Text() string {
	return strings.Join(document.Lines(), "\n\n")
}

// Words counts the words of the text: runs of letters and digits.
func (document Document) Words() int {
	return len(strings.FieldsFunc(document.Text(), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) }))
}

// Parse reads a chapter file by the extension of its name — .docx, .txt, .md (or .markdown) — the step «разобрать файл
// главы» (PR1). A Word file is known by its archive too, whatever its name.
func Parse(name string, data []byte) (Document, error) {
	if len(data) > MaxFileBytes {
		return Document{}, ErrTooLarge
	}
	var (
		document Document
		err      error
	)
	switch extension := strings.ToLower(path.Ext(strings.TrimSpace(name))); {
	case extension == ".docx":
		document, err = parseDOCX(data)
	case extension == ".txt":
		document, err = parsePlain(data, FormatText)
	case extension == ".md" || extension == ".markdown":
		document, err = parseMarkdown(data)
	default:
		return Document{}, ErrFormat
	}
	if err != nil {
		return Document{}, err
	}
	return finish(document)
}

// ParseText reads a pasted text: every line with words is a paragraph, as in a file .txt.
func ParseText(text string) (Document, error) {
	if len(text) > MaxFileBytes {
		return Document{}, ErrTooLarge
	}
	if !utf8.ValidString(text) {
		return Document{}, ErrUnreadable
	}
	document := plainDocument(text, FormatPasted)
	return finish(document)
}

// chapterTitle is a line that names a chapter rather than starts it: «Глава 634. …», «Пролог», «Эпилог»
// (the rule of the import of books, library/import_docx.go).
var chapterTitle = regexp.MustCompile(`^(?:глава\s+(?:\d+|[ivxlcdm]+)|пролог|эпилог|интерлюдия)(?:$|[\s.:,)—–-])`)

// maxTitleRunes is the longest title a draft keeps.
const maxTitleRunes = 200

// finish trims the paragraphs, takes the title of the chapter out of the text and holds the limits.
func finish(document Document) (Document, error) {
	for i := range document.Paragraphs {
		document.Paragraphs[i] = clean(document.Paragraphs[i])
	}
	if document.Title != nil {
		title := clean(*document.Title)
		document.Title = &title
		if strings.TrimSpace(title.Text()) == "" {
			document.Title = nil
		}
	}
	// Without a heading the first line that names a chapter is its title.
	if document.Title == nil {
		for i, paragraph := range document.Paragraphs {
			text := paragraph.Text()
			if text == "" {
				continue
			}
			if chapterTitle.MatchString(strings.ToLower(text)) && utf8.RuneCountInString(text) <= maxTitleRunes {
				title := paragraph
				document.Title = &title
				document.Paragraphs = append(document.Paragraphs[:i:i], document.Paragraphs[i+1:]...)
			}
			break
		}
	}
	if document.Title != nil && utf8.RuneCountInString(document.Title.Text()) > maxTitleRunes {
		document.Paragraphs = append([]Paragraph{*document.Title}, document.Paragraphs...)
		document.Title = nil
	}
	if document.Words() == 0 {
		return Document{}, ErrEmpty
	}
	if utf8.RuneCountInString(document.Text()) > MaxTextRunes {
		return Document{}, ErrTooLong
	}
	return document, nil
}

// clean trims the edges of a paragraph, makes every space one sign of its kind and joins the runs of one face. The
// marks ⟦ and ⟧ belong to the model's text of a draft: in the author's file they are brackets.
func clean(paragraph Paragraph) Paragraph {
	var runs []Run
	for _, run := range paragraph.Runs {
		run.Text = strings.Map(func(r rune) rune {
			switch {
			case r == '⟦':
				return '['
			case r == '⟧':
				return ']'
			case r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f':
				return ' '
			case r == utf8.RuneError || unicode.IsControl(r):
				return -1
			}
			return r
		}, run.Text)
		if run.Text == "" {
			continue
		}
		if last := len(runs) - 1; last >= 0 && runs[last].Italic == run.Italic && runs[last].Bold == run.Bold {
			runs[last].Text += run.Text
			continue
		}
		runs = append(runs, run)
	}
	for len(runs) > 0 {
		runs[0].Text = strings.TrimLeftFunc(runs[0].Text, unicode.IsSpace)
		if runs[0].Text != "" {
			break
		}
		runs = runs[1:]
	}
	for len(runs) > 0 {
		last := len(runs) - 1
		runs[last].Text = strings.TrimRightFunc(runs[last].Text, unicode.IsSpace)
		if runs[last].Text != "" {
			break
		}
		runs = runs[:last]
	}
	return Paragraph{Runs: runs}
}
