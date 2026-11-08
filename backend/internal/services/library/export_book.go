package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"time"

	"canonkeeper/backend/internal/domain/bookexport"
	domainlibrary "canonkeeper/backend/internal/domain/library"

	"golang.org/x/image/webp"
)

// BookExportStore reads a book for its file.
type BookExportStore interface {
	BookHead(context.Context, domainlibrary.Actor, string) (domainlibrary.BookHead, error)
	BookExportChapters(ctx context.Context, projectID, bookID string) ([]domainlibrary.ExportChapter, error)
	BookExportParagraphs(ctx context.Context, projectID, bookID string, chapter domainlibrary.ExportChapter) ([]string, error)
}

// BookCoverReader reads the bytes of a cover the actor may see.
type BookCoverReader interface {
	Execute(context.Context, domainlibrary.CoverLookup) (CoverContent, error)
}

// ExportBookInput names a book and the format of its file.
type ExportBookInput struct {
	Actor  domainlibrary.Actor
	BookID string
	Format string
}

// ExportBook gives a whole book away as one file (specs/2026-11-05-book-export.md, E1). Nothing is stored and no model
// takes part; the rights are checked and the chapters listed before the first byte of the file.
type ExportBook struct {
	store  BookExportStore
	covers BookCoverReader
	now    func() time.Time
}

func NewExportBook(store BookExportStore, covers BookCoverReader, now func() time.Time) *ExportBook {
	if store == nil || covers == nil || now == nil {
		panic("book export: dependencies are required")
	}
	return &ExportBook{store: store, covers: covers, now: now}
}

// BookFile is a file ready to be written as it goes.
type BookFile struct {
	Name      string
	MediaType string
	format    bookexport.Format
	book      bookexport.Book
}

// Write writes the file. An error means the file is not whole: the receiver must not take it for a success.
func (file BookFile) Write(writer io.Writer) error {
	return bookexport.Write(writer, file.format, file.book)
}

func (export *ExportBook) Execute(ctx context.Context, input ExportBookInput) (BookFile, error) {
	format, err := bookexport.ParseFormat(input.Format)
	if err != nil {
		return BookFile{}, err
	}
	head, err := export.store.BookHead(ctx, input.Actor, input.BookID)
	if err != nil {
		return BookFile{}, err
	}
	if !head.CanEdit {
		return BookFile{}, domainlibrary.ErrForbidden
	}
	chapters, err := export.store.BookExportChapters(ctx, head.ProjectID, input.BookID)
	if err != nil {
		return BookFile{}, err
	}
	if len(chapters) == 0 {
		return BookFile{}, bookexport.ErrEmptyBook
	}
	book := bookexport.Book{
		ID: input.BookID, Title: head.Title, Author: head.AuthorName, Date: export.now().UTC(),
		Chapters: make([]string, len(chapters)),
		Text: func(chapter int) ([]string, error) {
			paragraphs, err := export.store.BookExportParagraphs(ctx, head.ProjectID, input.BookID, chapters[chapter])
			if err == nil && len(paragraphs) == 0 {
				// The list saw text here: the chapter was deleted or replaced while the file was being written.
				return nil, domainlibrary.ErrConflict
			}
			return paragraphs, err
		},
	}
	for index, chapter := range chapters {
		book.Chapters[index] = chapter.Title
	}
	if head.CoverVersion > 0 {
		content, err := export.covers.Execute(ctx, domainlibrary.CoverLookup{Actor: input.Actor, BookID: input.BookID, Version: head.CoverVersion})
		if err != nil {
			return BookFile{}, err
		}
		book.Cover = readableCover(content)
	}
	return BookFile{Name: bookexport.FileName(head.Title, format), MediaType: format.MediaType(), format: format, book: book}, nil
}

// maxConvertedCoverPixels keeps one export from taking the memory of the server to decode a picture: a real cover is
// around 4 million pixels, and upload limits only its bytes.
const maxConvertedCoverPixels = 25_000_000

// readableCover gives a cover every reader shows: JPEG and PNG go as they are, WebP becomes JPEG because Word and the
// readers of FB2 do not know it. A cover that cannot be read is left out: the book is what the author came for.
func readableCover(content CoverContent) *bookexport.Cover {
	reader := bytes.NewReader(content.Content)
	if content.MIMEType != "image/webp" {
		config, _, err := image.DecodeConfig(reader)
		if err != nil || config.Width < 1 || config.Height < 1 {
			return nil
		}
		return &bookexport.Cover{MediaType: content.MIMEType, Content: content.Content, Width: config.Width, Height: config.Height}
	}
	config, err := webp.DecodeConfig(reader)
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxConvertedCoverPixels/config.Height {
		return nil
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	decoded, err := webp.Decode(reader)
	if err != nil {
		return nil
	}
	converted, err := coverJPEG(decoded)
	if err != nil {
		return nil
	}
	return &bookexport.Cover{MediaType: "image/jpeg", Content: converted, Width: decoded.Bounds().Dx(), Height: decoded.Bounds().Dy()}
}

// coverJPEG writes a picture as JPEG of a cover. JPEG has no transparency: what is transparent lies on the white of a page.
func coverJPEG(picture image.Image) ([]byte, error) {
	bounds := picture.Bounds()
	opaque := image.NewRGBA(bounds)
	draw.Draw(opaque, bounds, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(opaque, bounds, picture, bounds.Min, draw.Over)
	var converted bytes.Buffer
	if err := jpeg.Encode(&converted, opaque, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return converted.Bytes(), nil
}
