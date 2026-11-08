// Package bookexport writes a book into the file of a reader or a publisher: Word, EPUB or FB2
// (specs/2026-11-05-book-export.md). The structure of headings is part of the file: a screen reader walks a book by it.
package bookexport

import (
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Format is a file format of a book.
type Format string

const (
	FormatDOCX Format = "docx"
	FormatEPUB Format = "epub"
	FormatFB2  Format = "fb2"
)

var (
	ErrUnknownFormat = errors.New("bookexport: unknown format")
	ErrEmptyBook     = errors.New("bookexport: the book has no text")
)

// ParseFormat reads a format as the contract names it.
func ParseFormat(value string) (Format, error) {
	switch format := Format(value); format {
	case FormatDOCX, FormatEPUB, FormatFB2:
		return format, nil
	}
	return "", ErrUnknownFormat
}

// MediaType is the type of a file of the format.
func (format Format) MediaType() string {
	switch format {
	case FormatDOCX:
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case FormatEPUB:
		return "application/epub+zip"
	default:
		return "application/x-fictionbook+xml"
	}
}

// Book is what a file says of a book. The text is read one chapter at a time, so a book is never in memory whole.
type Book struct {
	ID       string
	Title    string
	Author   string
	Date     time.Time
	Cover    *Cover
	Chapters []string
	// Text gives the paragraphs of the chapter at an index of Chapters.
	Text func(chapter int) ([]string, error)
}

// Cover is a picture every reader shows: JPEG or PNG, with its size in pixels.
type Cover struct {
	MediaType string
	Content   []byte
	Width     int
	Height    int
}

func (cover Cover) fileName() string {
	if cover.MediaType == "image/png" {
		return "cover.png"
	}
	return "cover.jpg"
}

// coverDescription is what a screen reader says of a cover.
func coverDescription(title string) string {
	return "Обложка книги «" + title + "»"
}

// Write writes the file of a book as it goes. A paragraph without text is not written: readers do not show it,
// and a screen reader would say «пусто».
func Write(writer io.Writer, format Format, book Book) error {
	if len(book.Chapters) == 0 {
		return ErrEmptyBook
	}
	text := book.Text
	book.Text = func(chapter int) ([]string, error) {
		paragraphs, err := text(chapter)
		if err != nil {
			return nil, err
		}
		written := make([]string, 0, len(paragraphs))
		for _, paragraph := range paragraphs {
			if strings.TrimSpace(paragraph) != "" {
				written = append(written, paragraph)
			}
		}
		return written, nil
	}
	switch format {
	case FormatDOCX:
		return writeDOCX(writer, book)
	case FormatEPUB:
		return writeEPUB(writer, book)
	case FormatFB2:
		return writeFB2(writer, book)
	}
	return ErrUnknownFormat
}

// maxNameBytes keeps a name with its extension within the 255 bytes of a file system.
const maxNameBytes = 240

// FileName names a file by its book: the signs file systems do not allow are replaced.
func FileName(title string, format Format) string {
	name := strings.Map(func(sign rune) rune {
		if sign < ' ' || sign == 0x7f || strings.ContainsRune(`/\:*?"<>|`, sign) {
			return '_'
		}
		return sign
	}, title)
	name = strings.Trim(name, " .")
	for len(name) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	name = strings.TrimRight(name, " .")
	if name == "" {
		name = "Книга"
	}
	if reservedName(name) {
		name = "_" + name
	}
	return name + "." + string(format)
}

// reservedName tells a name Windows keeps for its devices, whatever follows the first dot.
func reservedName(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}
