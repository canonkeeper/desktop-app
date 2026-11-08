package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/bookexport"
	domainlibrary "canonkeeper/backend/internal/domain/library"
)

type exportStoreFake struct {
	head     domainlibrary.BookHead
	headErr  error
	chapters []domainlibrary.ExportChapter
	text     map[string][]string
}

func (store exportStoreFake) BookHead(context.Context, domainlibrary.Actor, string) (domainlibrary.BookHead, error) {
	return store.head, store.headErr
}

func (store exportStoreFake) BookExportChapters(context.Context, string, string) ([]domainlibrary.ExportChapter, error) {
	return store.chapters, nil
}

func (store exportStoreFake) BookExportParagraphs(_ context.Context, _, _ string, chapter domainlibrary.ExportChapter) ([]string, error) {
	return store.text[chapter.ID], nil
}

type exportCoverFake struct {
	content CoverContent
	lookup  *domainlibrary.CoverLookup
	err     error
}

func (cover exportCoverFake) Execute(_ context.Context, lookup domainlibrary.CoverLookup) (CoverContent, error) {
	if cover.lookup != nil {
		*cover.lookup = lookup
	}
	return cover.content, cover.err
}

func exportFixture() exportStoreFake {
	return exportStoreFake{
		head:     domainlibrary.BookHead{ProjectID: "project", Title: "Белый порт", AuthorName: "Автор", CanEdit: true},
		chapters: []domainlibrary.ExportChapter{{ID: "one", Title: "Первая", Revision: 2}},
		text:     map[string][]string{"one": {"Текст."}},
	}
}

func exportBook(store exportStoreFake, cover BookCoverReader, format string) (BookFile, error) {
	return NewExportBook(store, cover, func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) }).Execute(
		context.Background(), ExportBookInput{Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Format: format},
	)
}

// E1.3, E1.5, E1.7: the file is named by the book; a viewer, a book without text and an unknown format get no file.
func TestExportBookRefusesBeforeTheFirstByte(t *testing.T) {
	file, err := exportBook(exportFixture(), exportCoverFake{}, "fb2")
	if err != nil || file.Name != "Белый порт.fb2" || file.MediaType != "application/x-fictionbook+xml" {
		t.Fatalf("file = %+v, %v", file, err)
	}
	var written bytes.Buffer
	if err := file.Write(&written); err != nil || !strings.Contains(written.String(), "<p>Текст.</p>") {
		t.Fatalf("written = %q, %v", written.String(), err)
	}
	if _, err := exportBook(exportFixture(), exportCoverFake{}, "pdf"); !errors.Is(err, bookexport.ErrUnknownFormat) {
		t.Fatalf("pdf: %v", err)
	}
	stranger := exportFixture()
	stranger.headErr = domainlibrary.ErrNotFound
	if _, err := exportBook(stranger, exportCoverFake{}, "docx"); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("stranger: %v", err)
	}
	viewer := exportFixture()
	viewer.head.CanEdit = false
	if _, err := exportBook(viewer, exportCoverFake{}, "docx"); !errors.Is(err, domainlibrary.ErrForbidden) {
		t.Fatalf("viewer: %v", err)
	}
	empty := exportFixture()
	empty.chapters = nil
	if _, err := exportBook(empty, exportCoverFake{}, "docx"); !errors.Is(err, bookexport.ErrEmptyBook) {
		t.Fatalf("empty: %v", err)
	}
}

// E1.6: a chapter gone while the file is written stops the file instead of leaving a chapter without text in it.
func TestExportBookStopsWhenAChapterIsGone(t *testing.T) {
	store := exportFixture()
	store.text = map[string][]string{}
	file, err := exportBook(store, exportCoverFake{}, "fb2")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Write(&bytes.Buffer{}); !errors.Is(err, domainlibrary.ErrConflict) {
		t.Fatalf("write: %v", err)
	}
}

func encodedCover(t *testing.T, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 60, 90))
	picture.Set(1, 1, color.RGBA{R: 200, A: 255})
	var content bytes.Buffer
	if err := encode(&content, picture); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

// E1.1: the active cover goes in as it is, by its version, with its real size.
func TestExportBookTakesTheActiveCover(t *testing.T) {
	store := exportFixture()
	store.head.CoverVersion = 3
	var lookup domainlibrary.CoverLookup
	content := encodedCover(t, func(buffer *bytes.Buffer, picture image.Image) error { return png.Encode(buffer, picture) })
	file, err := exportBook(store, exportCoverFake{content: CoverContent{Content: content, MIMEType: "image/png"}, lookup: &lookup}, "docx")
	if err != nil {
		t.Fatal(err)
	}
	if lookup.Version != 3 || lookup.BookID != "book" || lookup.Actor.UserID != "actor" {
		t.Fatalf("lookup = %+v", lookup)
	}
	cover := file.book.Cover
	if cover == nil || cover.MediaType != "image/png" || !bytes.Equal(cover.Content, content) || cover.Width != 60 || cover.Height != 90 {
		t.Fatalf("cover = %+v", cover)
	}
	jpegContent := encodedCover(t, func(buffer *bytes.Buffer, picture image.Image) error { return jpeg.Encode(buffer, picture, nil) })
	file, err = exportBook(store, exportCoverFake{content: CoverContent{Content: jpegContent, MIMEType: "image/jpeg"}}, "docx")
	if err != nil || file.book.Cover == nil || file.book.Cover.MediaType != "image/jpeg" || !bytes.Equal(file.book.Cover.Content, jpegContent) {
		t.Fatalf("jpeg cover = %+v, %v", file.book.Cover, err)
	}
	unavailable := errors.New("the file of the cover is gone")
	if _, err := exportBook(store, exportCoverFake{err: unavailable}, "docx"); !errors.Is(err, unavailable) {
		t.Fatalf("a cover that cannot be read: %v", err)
	}
}

// E1.4: Word and the readers of FB2 do not know WebP, so such a cover becomes JPEG; what cannot be decoded is left out.
func TestExportBookConvertsAWebPCover(t *testing.T) {
	store := exportFixture()
	store.head.CoverVersion = 1
	content, err := os.ReadFile("testdata/cover.webp")
	if err != nil {
		t.Fatal(err)
	}
	file, err := exportBook(store, exportCoverFake{content: CoverContent{Content: content, MIMEType: "image/webp"}}, "fb2")
	if err != nil {
		t.Fatal(err)
	}
	cover := file.book.Cover
	if cover == nil || cover.MediaType != "image/jpeg" || cover.Width < 1 || cover.Height < 1 {
		t.Fatalf("cover = %+v", cover)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(cover.Content))
	if err != nil || format != "jpeg" || config.Width != cover.Width || config.Height != cover.Height {
		t.Fatalf("converted = %+v %s, %v", config, format, err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(cover.Content))
	if err != nil {
		t.Fatal(err)
	}
	// The left half of the picture is transparent: it lies on the white of a page, not on black.
	if red, green, blue, _ := decoded.At(5, 45).RGBA(); red < 0xf000 || green < 0xf000 || blue < 0xf000 {
		t.Fatalf("the transparent part = %d %d %d", red, green, blue)
	}
	broken, err := exportBook(store, exportCoverFake{content: CoverContent{Content: []byte("RIFFnot a picture"), MIMEType: "image/webp"}}, "fb2")
	if err != nil || broken.book.Cover != nil {
		t.Fatalf("a broken cover = %+v, %v", broken.book.Cover, err)
	}
}
