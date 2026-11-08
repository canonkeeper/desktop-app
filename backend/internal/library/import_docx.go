package library

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	docxWordprocessingMLNamespace       = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	docxStrictWordprocessingMLNamespace = "http://purl.oclc.org/ooxml/wordprocessingml/main"
)
const maxDOCXDocumentXMLBytes int64 = 32 * 1024 * 1024

// The title page of a book the product itself wrote into Word (specs/2026-11-05-book-export.md): a line in the style
// of the title may be long and still be a title, and the line of the author is not the text of the book.
const (
	docxTitleStyle      = "Title"
	docxBookAuthorStyle = "BookAuthor"
)

// ParseDOCX reads the paragraphs of a Word document and splits them into chapters at level-1
// headings: style «Heading1»/«Заголовок1», a style named «heading 1» in word/styles.xml (Russian
// Word saves it under the id «1»), a style based on one, or outline level 0. A manuscript without
// such headings is split at its chapter title lines («Глава 618. Колизей»). Chapters are assembled
// while document.xml is read and only up to the book limits.
func ParseDOCX(reader io.ReaderAt, size int64) (BookImport, error) {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return BookImport{}, fmt.Errorf("parse docx archive: %w", err)
	}
	var document, styles *zip.File
	for _, file := range archive.File {
		switch file.Name {
		case "word/document.xml":
			if document != nil {
				return BookImport{}, fmt.Errorf("parse docx: word/document.xml is duplicated")
			}
			document = file
		case "word/styles.xml":
			styles = file
		}
	}
	if document == nil {
		return BookImport{}, fmt.Errorf("parse docx: word/document.xml is missing")
	}
	contents, err := readDOCXPart(document)
	if err != nil {
		return BookImport{}, err
	}
	headingStyles := map[string]bool{}
	if styles != nil {
		styleContents, err := readDOCXPart(styles)
		if err != nil {
			return BookImport{}, err
		}
		if headingStyles, err = parseDOCXHeadingStyles(styleContents); err != nil {
			return BookImport{}, err
		}
	}
	book, err := docxBook(contents, headingStyles)
	if err != nil {
		return BookImport{}, err
	}
	if len(book.Chapters) == 0 {
		return BookImport{}, ErrImportNoContent
	}
	return book, nil
}

func readDOCXPart(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > uint64(maxDOCXDocumentXMLBytes) {
		return nil, fmt.Errorf("parse docx: %s is too large", file.Name)
	}
	stream, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open docx %s: %w", file.Name, err)
	}
	contents, readErr := readDOCXDocument(stream)
	closeErr := stream.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close docx %s: %w", file.Name, closeErr)
	}
	return contents, nil
}

func readDOCXDocument(reader io.Reader) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, maxDOCXDocumentXMLBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read docx document: %w", err)
	}
	if int64(len(contents)) > maxDOCXDocumentXMLBytes {
		return nil, fmt.Errorf("parse docx: word/document.xml is too large")
	}
	return contents, nil
}

type docxParagraph struct {
	text    string
	heading bool
	title   bool
}

// scanDOCXParagraphs passes the non-empty paragraphs of document.xml to visit in order.
func scanDOCXParagraphs(contents []byte, headingStyles map[string]bool, visit func(docxParagraph) error) error {
	reader := newImportXMLReader(bytes.NewReader(contents))
	var paragraph strings.Builder
	paragraphDepth := 0
	paragraphPropertiesDepth := 0
	textDepth := 0
	paragraphIsHeading1, paragraphIsTitle, paragraphIsBookAuthor := false, false, false
	for {
		token, err := reader.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse docx document: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if isDOCXElement(value.Name, "p") {
				if paragraphDepth == 0 {
					paragraph.Reset()
					paragraphPropertiesDepth = 0
					paragraphIsHeading1, paragraphIsTitle, paragraphIsBookAuthor = false, false, false
				}
				paragraphDepth++
			} else if paragraphDepth > 0 && isDOCXElement(value.Name, "pPr") {
				paragraphPropertiesDepth++
			} else if paragraphPropertiesDepth > 0 && isDOCXElement(value.Name, "pStyle") {
				style := docxAttribute(value, "val")
				if isDOCXHeading1Style(style) || headingStyles[style] {
					paragraphIsHeading1 = true
				}
				paragraphIsTitle = style == docxTitleStyle
				paragraphIsBookAuthor = style == docxBookAuthorStyle
			} else if paragraphPropertiesDepth > 0 && isDOCXElement(value.Name, "outlineLvl") {
				paragraphIsHeading1 = strings.TrimSpace(docxAttribute(value, "val")) == "0"
			} else if paragraphDepth > 0 && isDOCXElement(value.Name, "t") {
				textDepth++
			} else if paragraphDepth > 0 && isDOCXElement(value.Name, "tab") {
				paragraph.WriteByte('\t')
			} else if paragraphDepth > 0 && (isDOCXElement(value.Name, "br") || isDOCXElement(value.Name, "cr")) {
				paragraph.WriteByte('\n')
			}
		case xml.CharData:
			if paragraphDepth > 0 && textDepth > 0 {
				paragraph.Write(value)
			}
		case xml.EndElement:
			if textDepth > 0 && isDOCXElement(value.Name, "t") {
				textDepth--
			}
			if paragraphPropertiesDepth > 0 && isDOCXElement(value.Name, "pPr") {
				paragraphPropertiesDepth--
			}
			if paragraphDepth > 0 && isDOCXElement(value.Name, "p") {
				paragraphDepth--
				if text := strings.TrimSpace(paragraph.String()); paragraphDepth == 0 && text != "" && !paragraphIsBookAuthor {
					if err := visit(docxParagraph{text: text, heading: paragraphIsHeading1, title: paragraphIsTitle}); err != nil {
						return err
					}
				}
			}
		}
	}
}

// docxBook splits the paragraphs at their chapter starts and stops at the book limits. A single
// short line before the first chapter («13 том. Черновик») is the book title; longer front matter
// stays an untitled chapter. Chapters left without paragraphs are dropped later by the import plan.
func docxBook(contents []byte, headingStyles map[string]bool) (BookImport, error) {
	starts, err := docxChapterStarts(contents, headingStyles)
	if err != nil {
		return BookImport{}, err
	}
	var book BookImport
	var limits importLimits
	add := func(paragraph docxParagraph) error {
		if starts(paragraph) {
			book.Chapters = append(book.Chapters, ChapterImport{Title: paragraph.text})
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
		if err := limits.addParagraph(paragraph.text); err != nil {
			return err
		}
		chapter.Paragraphs = append(chapter.Paragraphs, paragraph.text)
		return nil
	}
	// titleLine holds the first paragraph until the next one tells whether it is the book title.
	var titleLine *docxParagraph
	first := true
	err = scanDOCXParagraphs(contents, headingStyles, func(paragraph docxParagraph) error {
		if first {
			first = false
			if !starts(paragraph) && (paragraph.title || utf8.RuneCountInString(paragraph.text) <= maxDOCXTitleLineRunes) {
				titleLine = &paragraph
				return nil
			}
		} else if titleLine != nil {
			line := *titleLine
			titleLine = nil
			if starts(paragraph) {
				book.Title = line.text
			} else if err := add(line); err != nil {
				return err
			}
		}
		return add(paragraph)
	})
	if err == nil && titleLine != nil {
		err = add(*titleLine)
	}
	if err != nil {
		return BookImport{}, err
	}
	return book, nil
}

var errDOCXChapterStartsKnown = errors.New("library: docx chapter starts are known")

// docxChapterStarts tells where chapters start: at level-1 headings, or at chapter title lines when
// there are fewer than two headings but at least two title lines. It reads document.xml only until
// a second heading settles it.
func docxChapterStarts(contents []byte, headingStyles map[string]bool) (func(docxParagraph) bool, error) {
	headings, titleLines := 0, 0
	err := scanDOCXParagraphs(contents, headingStyles, func(paragraph docxParagraph) error {
		if paragraph.heading {
			headings++
		}
		if docxChapterTitleLine(paragraph) {
			titleLines++
		}
		if headings >= 2 {
			return errDOCXChapterStartsKnown
		}
		return nil
	})
	switch {
	case errors.Is(err, errDOCXChapterStartsKnown):
	case err != nil:
		return nil, err
	case titleLines >= 2:
		return docxChapterTitleLine, nil
	}
	return func(paragraph docxParagraph) bool { return paragraph.heading }, nil
}

const maxDOCXTitleLineRunes = 120

// docxChapterTitleLine matches a short line that names a chapter by itself: «Глава 618. Колизей»,
// «Глава XII», «Пролог», «Эпилог: …»; prose like «Глава гильдии кивнул.» is not a title.
var docxChapterTitlePattern = regexp.MustCompile(`^(?:глава\s+(?:\d+|[ivxlcdm]+)|пролог|эпилог|интерлюдия)(?:$|[\s.:,)—–-])`)

func docxChapterTitleLine(paragraph docxParagraph) bool {
	text := strings.ToLower(paragraph.text)
	return utf8.RuneCountInString(text) <= maxDOCXTitleLineRunes && !strings.ContainsAny(text, "\n\t") &&
		docxChapterTitlePattern.MatchString(text)
}

// parseDOCXHeadingStyles returns the ids of the paragraph styles that are level-1 headings by
// name («heading 1», «Заголовок 1»), by outline level 0 or by a basedOn chain to such a style.
func parseDOCXHeadingStyles(contents []byte) (map[string]bool, error) {
	type style struct {
		name, basedOn string
		outline       string
	}
	styles := map[string]*style{}
	var current *style
	reader := newImportXMLReader(bytes.NewReader(contents))
	for {
		token, err := reader.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse docx styles: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch {
			case isDOCXElement(value.Name, "style"):
				current = nil
				if docxAttribute(value, "type") == "paragraph" {
					current = &style{}
					styles[docxAttribute(value, "styleId")] = current
				}
			case current != nil && isDOCXElement(value.Name, "name"):
				current.name = docxAttribute(value, "val")
			case current != nil && isDOCXElement(value.Name, "basedOn"):
				current.basedOn = docxAttribute(value, "val")
			case current != nil && isDOCXElement(value.Name, "outlineLvl"):
				current.outline = strings.TrimSpace(docxAttribute(value, "val"))
			}
		case xml.EndElement:
			if isDOCXElement(value.Name, "style") {
				current = nil
			}
		}
	}
	var heading func(id string, depth int) bool
	heading = func(id string, depth int) bool {
		value := styles[id]
		if value == nil || depth > len(styles) {
			return false
		}
		if value.outline != "" {
			return value.outline == "0"
		}
		return isDOCXHeading1Style(value.name) || value.basedOn != "" && heading(value.basedOn, depth+1)
	}
	headings := map[string]bool{}
	for id := range styles {
		if heading(id, 0) {
			headings[id] = true
		}
	}
	return headings, nil
}

func docxAttribute(element xml.StartElement, local string) string {
	for _, attribute := range element.Attr {
		if isDOCXElement(attribute.Name, local) {
			return attribute.Value
		}
	}
	return ""
}

func isDOCXHeading1Style(value string) bool {
	value = strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	return strings.EqualFold(value, "Heading1") || strings.EqualFold(value, "Заголовок1")
}

func isDOCXElement(name xml.Name, local string) bool {
	return name.Local == local &&
		(name.Space == docxWordprocessingMLNamespace || name.Space == docxStrictWordprocessingMLNamespace)
}
