// Package bookexport writes a book into the file of a reader or a publisher: Word, EPUB or FB2
// (specs/2026-11-05-book-export.md). The structure of headings is part of the file: a screen reader walks a book by it.
package bookexport

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/textface"
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

// Book is what a file says of a book. The text is read one chapter at a time and the pictures one at a time, so a book
// is never in memory whole.
type Book struct {
	ID       string
	Title    string
	Author   string
	Date     time.Time
	Cover    *Image
	Chapters []string
	// Text gives the paragraphs of the chapter at an index of Chapters in the Markdown subset of the book, with their
	// italic and bold (specs/2026-11-08-italic-bold-in-text.md TF7.1).
	Text func(chapter int) ([]string, error)
	// Pictures are the drawings the author put in the book (specs/2026-11-12-ux10-book-pictures.md BP2.1), known before
	// the file is written: Word and EPUB list their parts before the chapters.
	Pictures []Picture
}

// Picture is a drawing in a chapter of the book: after paragraph After, 1…, of what Text gives for Chapter, a paragraph
// without words counted as the reader counts it (BP1.1); a place beyond the end of the chapter is after its last paragraph.
type Picture struct {
	Chapter int
	After   int
	// Name is what a screen reader says of the picture: the name of the drawing. The file has no caption: a line by the
	// picture would come back as the text of the book (BP2.3).
	Name string
	// Read gives the picture when the file comes to it. One that cannot be read is left out and the file goes on (BP2.4).
	Read func() (Image, error)
}

// Image is a picture every reader shows: JPEG or PNG, with its size in pixels.
type Image struct {
	MediaType string
	Content   []byte
	Width     int
	Height    int
}

func (image Image) extension() string {
	if image.MediaType == "image/png" {
		return "png"
	}
	return "jpg"
}

func (image Image) readable() bool {
	return (image.MediaType == "image/jpeg" || image.MediaType == "image/png") && len(image.Content) > 0 && image.Width > 0 && image.Height > 0
}

func coverFileName(cover Image) string { return "cover." + cover.extension() }

// coverDescription is what a screen reader says of a cover.
func coverDescription(title string) string {
	return "Обложка книги «" + title + "»"
}

// placed is a picture that was read, as its references in the file name it.
type placed struct {
	Picture
	number    int
	mediaType string
	width     int
	height    int
}

// id names a picture in its file by its place in the book, fileName names its part.
func (picture *placed) id() string { return fmt.Sprintf("picture-%04d", picture.number) }

func (picture *placed) fileName() string {
	return picture.id() + "." + Image{MediaType: picture.mediaType}.extension()
}

func (picture *placed) name() string {
	if name := strings.TrimSpace(picture.Name); name != "" {
		return name
	}
	return "Рисунок"
}

// block is what a chapter holds in its order: a paragraph of text or a picture.
type block struct {
	paragraph string
	picture   *placed
}

// bookFile is a book as its file is written: its pictures in the order of the book and those of them read so far.
type bookFile struct {
	Book
	pictures []Picture
	placed   []*placed
}

// readPictures reads the pictures one at a time and gives each to keep before the next is read; one that cannot be read is
// left out (BP2.2, BP2.4).
func (book *bookFile) readPictures(keep func(*placed, Image) error) error {
	for index, picture := range book.pictures {
		if picture.Read == nil {
			continue
		}
		image, err := picture.Read()
		if err != nil || !image.readable() {
			continue
		}
		read := &placed{Picture: picture, number: index + 1, mediaType: image.MediaType, width: image.Width, height: image.Height}
		if keep != nil {
			if err := keep(read, image); err != nil {
				return err
			}
		}
		book.placed[index] = read
	}
	return nil
}

// pictured are the pictures of the book that were read.
func (book *bookFile) pictured() []*placed {
	read := make([]*placed, 0, len(book.placed))
	for _, picture := range book.placed {
		if picture != nil {
			read = append(read, picture)
		}
	}
	return read
}

// chapter reads the text of a chapter with its pictures in their places. A paragraph without text is not written —
// readers do not show it, and a screen reader would say «пусто» —, but the place of a picture counts it (BP1.1).
func (book *bookFile) chapter(index int) ([]block, error) {
	paragraphs, err := book.Text(index)
	if err != nil {
		return nil, err
	}
	var pictures []*placed
	for _, picture := range book.placed {
		if picture != nil && picture.Chapter == index {
			pictures = append(pictures, picture)
		}
	}
	blocks := make([]block, 0, len(paragraphs)+len(pictures))
	next := 0
	for at, paragraph := range paragraphs {
		if strings.TrimSpace(textface.MarkdownPlain(paragraph)) != "" {
			blocks = append(blocks, block{paragraph: paragraph})
		}
		for ; next < len(pictures) && pictures[next].After <= at+1; next++ {
			blocks = append(blocks, block{picture: pictures[next]})
		}
	}
	for ; next < len(pictures); next++ {
		blocks = append(blocks, block{picture: pictures[next]})
	}
	return blocks, nil
}

// Write writes the file of a book as it goes.
func Write(writer io.Writer, format Format, written Book) error {
	if len(written.Chapters) == 0 {
		return ErrEmptyBook
	}
	pictures := slices.Clone(written.Pictures)
	slices.SortStableFunc(pictures, func(left, right Picture) int {
		return cmp.Or(cmp.Compare(left.Chapter, right.Chapter), cmp.Compare(left.After, right.After))
	})
	book := &bookFile{Book: written, pictures: pictures, placed: make([]*placed, len(pictures))}
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

// writeFaced writes a paragraph of the book in the Markdown subset run by run, each between the tags of its faces: bold
// outside, italic inside.
func writeFaced(out *output, paragraph, italic, bold string) {
	closing := func(tag string) string { return "</" + tag[1:] }
	for _, run := range textface.FromMarkdown(paragraph) {
		if run.Bold {
			out.raw(bold)
		}
		if run.Italic {
			out.raw(italic)
		}
		out.text(run.Text)
		if run.Italic {
			out.raw(closing(italic))
		}
		if run.Bold {
			out.raw(closing(bold))
		}
	}
}
