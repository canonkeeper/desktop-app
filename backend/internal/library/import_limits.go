package library

import (
	"encoding/xml"
	"errors"
	"io"
	"unicode/utf8"
)

// Book limits of an import (specs/2026-10-27-stage-0-protection.md, R2.1). Each chapter becomes a
// card built by the model, so a file over them fails with its own code instead of turning into
// hundreds of thousands of chapters.
const (
	maxImportedChapters  = 2000
	maxImportedTextRunes = 10_000_000
)

var (
	ErrImportTooManyChapters = errors.New("library: import has more chapters than a book may have")
	ErrImportTooLarge        = errors.New("library: import text is longer than a book may have")
)

// importLimits counts what the import plan keeps — chapters with text and the runes of their
// non-empty paragraphs — while a file is parsed, so every parser stops at the first chapter or
// paragraph over the book limits instead of after the whole file (R2.2).
type importLimits struct {
	chapters int
	runes    int
}

// addChapter counts a chapter when it gets its first paragraph.
func (limits *importLimits) addChapter() error {
	limits.chapters++
	if limits.chapters > maxImportedChapters {
		return ErrImportTooManyChapters
	}
	return nil
}

// addParagraph counts a non-empty paragraph as the import plan keeps it.
func (limits *importLimits) addParagraph(text string) error {
	limits.runes += utf8.RuneCountInString(text)
	if limits.runes > maxImportedTextRunes {
		return ErrImportTooLarge
	}
	return nil
}

// maxImportXMLDepth bounds element nesting in an imported XML part: encoding/xml keeps every open
// element, so a part of nothing but nested tags would cost memory per tag. It is the depth
// encoding/xml allows when it unmarshals.
const maxImportXMLDepth = 10_000

var errImportXMLTooDeep = errors.New("library: import XML nesting is too deep")

// importXMLReader reads the tokens of an imported XML part, decoded from its declared encoding,
// and refuses nesting deeper than maxImportXMLDepth. The book limits count the decoded text.
type importXMLReader struct {
	decoder *xml.Decoder
	depth   int
}

func newImportXMLReader(reader io.Reader) *importXMLReader {
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = importCharsetReader
	return &importXMLReader{decoder: decoder}
}

func (reader *importXMLReader) Token() (xml.Token, error) {
	token, err := reader.decoder.Token()
	switch token.(type) {
	case xml.StartElement:
		reader.depth++
		if reader.depth > maxImportXMLDepth {
			return nil, errImportXMLTooDeep
		}
	case xml.EndElement:
		reader.depth--
	}
	return token, err
}
