package library

import (
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
)

var ErrImportNoContent = errors.New("library: import has no content")

type BookImport struct {
	Title    string
	Chapters []ChapterImport
}

type ChapterImport struct {
	Title      string
	Paragraphs []string
}

// maxFB2Sections caps the sections of a file, empty ones included. A book keeps empty sections
// for its parts and volumes, far below the cap; a file of empty sections stops at it instead of
// being walked whole (R2.2).
const maxFB2Sections = 10 * maxImportedChapters

// ParseFB2 reads the book title (description/title-info/book-title) and turns every section of a
// body with paragraphs into a chapter: its own <p> are the paragraphs, the first line of its
// <title> is the title, and it comes before the chapters of its subsections. The file is read as
// a stream that stops at the book limits.
func ParseFB2(reader io.Reader) (BookImport, error) {
	parser := fb2Parser{xml: newImportXMLReader(reader)}
	if err := parser.parse(); err != nil {
		return BookImport{}, err
	}
	book := parser.book
	if len(book.Chapters) == 0 {
		return BookImport{}, ErrImportNoContent
	}
	for index := range book.Chapters {
		if book.Chapters[index].Title == "" {
			book.Chapters[index].Title = chapterFallbackTitle(index + 1)
		}
	}
	return book, nil
}

// fb2Element is the role of an open element; the elements whose text is read end in Text.
type fb2Element uint8

const (
	fb2Other fb2Element = iota
	fb2Root
	fb2Description
	fb2TitleInfo
	fb2Body
	fb2Section
	fb2SectionTitle
	fb2BookTitleText
	fb2ParagraphText
	fb2TitleLineText
)

type fb2Parser struct {
	xml      *importXMLReader
	book     BookImport
	limits   importLimits
	elements []fb2Element
	sections []fb2OpenSection
	opened   int // sections opened so far
	text     strings.Builder
	// textDepth is the depth of the element whose text is read, 0 when none is.
	textDepth int
}

// fb2OpenSection keeps the chapter of a section until it closes; the chapter then goes to
// position, the number of chapters before the section opened, ahead of its subsections.
type fb2OpenSection struct {
	position int
	chapter  ChapterImport
}

func (parser *fb2Parser) parse() error {
	for {
		token, err := parser.xml.Token()
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if err := parser.start(token.Name.Local); err != nil {
				return err
			}
		case xml.CharData:
			if parser.textDepth > 0 {
				parser.text.Write(token)
			}
		case xml.EndElement:
			if err := parser.end(); err != nil {
				return err
			}
			if len(parser.elements) == 0 {
				return nil
			}
		}
	}
}

func (parser *fb2Parser) start(name string) error {
	element := fb2Other
	if parser.textDepth == 0 {
		element = parser.role(name)
	}
	parser.elements = append(parser.elements, element)
	switch element {
	case fb2Section:
		parser.opened++
		if parser.opened > maxFB2Sections {
			return ErrImportTooManyChapters
		}
		parser.sections = append(parser.sections, fb2OpenSection{position: len(parser.book.Chapters)})
	case fb2BookTitleText, fb2ParagraphText, fb2TitleLineText:
		parser.text.Reset()
		parser.textDepth = len(parser.elements)
	}
	return nil
}

func (parser *fb2Parser) role(name string) fb2Element {
	if len(parser.elements) == 0 {
		return fb2Root
	}
	switch parent := parser.elements[len(parser.elements)-1]; {
	case parent == fb2Root && name == "description":
		return fb2Description
	case parent == fb2Root && name == "body":
		return fb2Body
	case parent == fb2Description && name == "title-info":
		return fb2TitleInfo
	case parent == fb2TitleInfo && name == "book-title":
		return fb2BookTitleText
	case (parent == fb2Body || parent == fb2Section) && name == "section":
		return fb2Section
	case parent == fb2Section && name == "title":
		return fb2SectionTitle
	case parent == fb2Section && name == "p":
		return fb2ParagraphText
	case parent == fb2SectionTitle && name == "p":
		return fb2TitleLineText
	default:
		return fb2Other
	}
}

func (parser *fb2Parser) end() error {
	depth := len(parser.elements)
	element := parser.elements[depth-1]
	parser.elements = parser.elements[:depth-1]
	if depth == parser.textDepth {
		parser.textDepth = 0
		return parser.endText(element, normalizeImportText(parser.text.String()))
	}
	if element == fb2Section {
		section := parser.sections[len(parser.sections)-1]
		parser.sections = parser.sections[:len(parser.sections)-1]
		if len(section.chapter.Paragraphs) > 0 {
			parser.book.Chapters = slices.Insert(parser.book.Chapters, section.position, section.chapter)
		}
	}
	return nil
}

func (parser *fb2Parser) endText(element fb2Element, text string) error {
	if element == fb2BookTitleText {
		parser.book.Title = text
		return nil
	}
	if text == "" {
		return nil
	}
	chapter := &parser.sections[len(parser.sections)-1].chapter
	if element == fb2TitleLineText {
		if chapter.Title == "" {
			chapter.Title = text
		}
		return nil
	}
	if len(chapter.Paragraphs) == 0 {
		if err := parser.limits.addChapter(); err != nil {
			return err
		}
	}
	if err := parser.limits.addParagraph(text); err != nil {
		return err
	}
	chapter.Paragraphs = append(chapter.Paragraphs, text)
	return nil
}

func normalizeImportText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// chapterFallbackTitle names a chapter that has no heading in the imported file.
func chapterFallbackTitle(number int) string {
	return "Глава " + strconv.Itoa(number)
}
