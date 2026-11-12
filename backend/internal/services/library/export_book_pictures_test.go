package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"reflect"
	"testing"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

type picturesFake struct {
	pictures  []BookPicture
	err       error
	projectID *string
	chapters  *[]domainlibrary.ExportChapter
}

func (fake picturesFake) Pictures(_ context.Context, projectID string, chapters []domainlibrary.ExportChapter) ([]BookPicture, error) {
	if fake.projectID != nil {
		*fake.projectID, *fake.chapters = projectID, chapters
	}
	return fake.pictures, fake.err
}

func pictureOf(chapter, after int, name string, content []byte, mimeType string, err error) BookPicture {
	return BookPicture{Chapter: chapter, After: after, Name: name, Read: func(context.Context) ([]byte, string, error) {
		return content, mimeType, err
	}}
}

func exportWithPictures(store exportStoreFake, pictures BookPictures, drawings bool) (BookFile, error) {
	return NewExportBook(store, exportCoverFake{}, func() time.Time { return time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC) }).
		WithPictures(pictures).
		Execute(context.Background(), ExportBookInput{Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Format: "epub", Drawings: drawings})
}

// specs/2026-11-12-ux10-book-pictures.md BP2.1, BP2.2: the drawings of the chapters go into the file only when the author
// asks for them: the port reads the chapters of the book, a picture of another chapter is no picture of the file, a
// picture is read when the file comes to it — WebP as JPEG, one that cannot be decoded left out.
func TestExportBookTakesTheDrawingsOnlyWhenAsked(t *testing.T) {
	store := exportFixture()
	store.chapters = []domainlibrary.ExportChapter{{ID: "one", BookID: "book", Title: "Первая", Revision: 2, Paragraphs: 3}}
	pictureBytes := encodedCover(t, func(buffer *bytes.Buffer, picture image.Image) error { return png.Encode(buffer, picture) })
	webp, err := os.ReadFile("testdata/cover.webp")
	if err != nil {
		t.Fatal(err)
	}
	var projectID string
	var chapters []domainlibrary.ExportChapter
	port := picturesFake{projectID: &projectID, chapters: &chapters, pictures: []BookPicture{
		pictureOf(0, 1, "Мара у пристани", pictureBytes, "image/png", nil),
		pictureOf(0, 2, "Костёр", webp, "image/webp", nil),
		pictureOf(0, 3, "Сломанный", []byte("not a picture"), "image/png", nil),
		pictureOf(0, 3, "Пропавший", nil, "", errors.New("the file is gone")),
		pictureOf(5, 1, "Чужая глава", pictureBytes, "image/png", nil),
	}}
	without, err := exportWithPictures(store, port, false)
	if err != nil || len(without.book.Pictures) != 0 || projectID != "" {
		t.Fatalf("without the drawings: %+v, %v, asked %q", without.book.Pictures, err, projectID)
	}
	file, err := exportWithPictures(store, port, true)
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "project" || !reflect.DeepEqual(chapters, store.chapters) {
		t.Fatalf("asked %q for %+v", projectID, chapters)
	}
	if len(file.book.Pictures) != 4 {
		t.Fatalf("%d pictures", len(file.book.Pictures))
	}
	first, err := file.book.Pictures[0].Read()
	if err != nil || first.MediaType != "image/png" || !bytes.Equal(first.Content, pictureBytes) || first.Width != 60 || first.Height != 90 {
		t.Fatalf("the first picture = %+v, %v", first, err)
	}
	if picture := file.book.Pictures[0]; picture.Chapter != 0 || picture.After != 1 || picture.Name != "Мара у пристани" {
		t.Fatalf("the first picture stands %+v", picture)
	}
	converted, err := file.book.Pictures[1].Read()
	if err != nil || converted.MediaType != "image/jpeg" || converted.Width < 1 {
		t.Fatalf("the WebP picture = %+v, %v", converted.MediaType, err)
	}
	for _, index := range []int{2, 3} {
		if _, err := file.book.Pictures[index].Read(); err == nil {
			t.Fatalf("picture %d is read", index)
		}
	}
	if err := file.Write(&bytes.Buffer{}); err != nil {
		t.Fatalf("a file with a picture that cannot be read: %v", err)
	}
}

// BP2.2: a build without drawings has no port, and the file asked with them is the file without them; a list of the
// drawings that cannot be read stops the file before its first byte.
func TestExportBookWithoutTheDrawingsOfABuild(t *testing.T) {
	file, err := exportWithPictures(exportFixture(), nil, true)
	if err != nil || len(file.book.Pictures) != 0 {
		t.Fatalf("the desktop: %+v, %v", file.book.Pictures, err)
	}
	unavailable := errors.New("the drawings cannot be read")
	if _, err := exportWithPictures(exportFixture(), picturesFake{err: unavailable}, true); !errors.Is(err, unavailable) {
		t.Fatalf("a list that cannot be read: %v", err)
	}
}

type drawingCountsStoreFake struct {
	canEdit  bool
	err      error
	chapters []domainlibrary.ExportChapter
}

func (store drawingCountsStoreFake) ProjectCanEdit(context.Context, domainlibrary.Actor, string) (bool, error) {
	return store.canEdit, store.err
}

func (store drawingCountsStoreFake) ProjectExportChapters(context.Context, string) ([]domainlibrary.ExportChapter, error) {
	return store.chapters, nil
}

// BP3.1, BP3.2, BP2.5: the window counts the drawings each book of the saga would carry in its file — the same drawings
// the file takes —; a viewer gets no count, a stranger does not see the saga, a build without drawings counts none.
func TestCountBookDrawingsCountsWhatTheFilesTake(t *testing.T) {
	store := drawingCountsStoreFake{canEdit: true, chapters: []domainlibrary.ExportChapter{
		{ID: "one", BookID: "first", Paragraphs: 3}, {ID: "two", BookID: "first", Paragraphs: 2}, {ID: "three", BookID: "second", Paragraphs: 4},
	}}
	var chapters []domainlibrary.ExportChapter
	var projectID string
	port := picturesFake{projectID: &projectID, chapters: &chapters, pictures: []BookPicture{
		{Chapter: 0, After: 1}, {Chapter: 1, After: 2}, {Chapter: 2, After: 4}, {Chapter: 1, After: 1}, {Chapter: 9, After: 1},
	}}
	counts, err := NewCountBookDrawings(store, port).Execute(context.Background(), domainlibrary.Actor{UserID: "actor"}, "project")
	if err != nil {
		t.Fatal(err)
	}
	if want := []BookDrawingCount{{BookID: "first", Drawings: 3}, {BookID: "second", Drawings: 1}}; !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts = %+v", counts)
	}
	if projectID != "project" || !reflect.DeepEqual(chapters, store.chapters) {
		t.Fatalf("asked %q for %+v", projectID, chapters)
	}
	if counts, err := NewCountBookDrawings(store, nil).Execute(context.Background(), domainlibrary.Actor{UserID: "actor"}, "project"); err != nil || len(counts) != 0 {
		t.Fatalf("the desktop: %+v, %v", counts, err)
	}
	viewer := store
	viewer.canEdit = false
	if _, err := NewCountBookDrawings(viewer, port).Execute(context.Background(), domainlibrary.Actor{UserID: "actor"}, "project"); !errors.Is(err, domainlibrary.ErrForbidden) {
		t.Fatalf("viewer: %v", err)
	}
	stranger := store
	stranger.err = domainlibrary.ErrNotFound
	if _, err := NewCountBookDrawings(stranger, port).Execute(context.Background(), domainlibrary.Actor{UserID: "actor"}, "project"); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("stranger: %v", err)
	}
}
