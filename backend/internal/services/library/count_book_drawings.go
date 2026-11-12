package library

import (
	"context"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

// BookDrawingsStore reads a saga for the count of the drawings in its books.
type BookDrawingsStore interface {
	// ProjectCanEdit is the right to take the books of a saga away as files; a saga the actor is not in is not found.
	ProjectCanEdit(ctx context.Context, actor domainlibrary.Actor, projectID string) (bool, error)
	// ProjectExportChapters are the chapters with text of the living books of a saga, in the order of the shelf.
	ProjectExportChapters(ctx context.Context, projectID string) ([]domainlibrary.ExportChapter, error)
}

// BookDrawingCount is how many drawings stand in a book.
type BookDrawingCount struct {
	BookID   string
	Drawings int
}

// CountBookDrawings is getBookDrawingCounts (specs/2026-11-12-ux10-book-pictures.md BP3.1, BP3.2): the window «Скачать
// книгу…» says how many drawings the file of a book would carry, and «Скачать всю сагу…» their sum. The count is of the
// drawings the file itself takes: the same port reads them for the same chapters.
type CountBookDrawings struct {
	store    BookDrawingsStore
	pictures BookPictures
}

// NewCountBookDrawings counts by the drawings of the build; without them — the desktop — there are none.
func NewCountBookDrawings(store BookDrawingsStore, pictures BookPictures) *CountBookDrawings {
	if store == nil {
		panic("book drawings: the store is required")
	}
	return &CountBookDrawings{store: store, pictures: pictures}
}

// Execute counts the drawings in the books of a saga that have any, in the order of the shelf. Like the file of a book
// it is the right of an editor and above (E1.5).
func (count *CountBookDrawings) Execute(ctx context.Context, actor domainlibrary.Actor, projectID string) ([]BookDrawingCount, error) {
	canEdit, err := count.store.ProjectCanEdit(ctx, actor, projectID)
	if err != nil {
		return nil, err
	}
	if !canEdit {
		return nil, domainlibrary.ErrForbidden
	}
	counts := []BookDrawingCount{}
	if count.pictures == nil {
		return counts, nil
	}
	chapters, err := count.store.ProjectExportChapters(ctx, projectID)
	if err != nil || len(chapters) == 0 {
		return counts, err
	}
	pictures, err := count.pictures.Pictures(ctx, projectID, chapters)
	if err != nil {
		return nil, err
	}
	inChapter := make([]int, len(chapters))
	for _, picture := range pictures {
		if picture.Chapter >= 0 && picture.Chapter < len(chapters) {
			inChapter[picture.Chapter]++
		}
	}
	for index, chapter := range chapters {
		if inChapter[index] == 0 {
			continue
		}
		if last := len(counts) - 1; last >= 0 && counts[last].BookID == chapter.BookID {
			counts[last].Drawings += inChapter[index]
			continue
		}
		counts = append(counts, BookDrawingCount{BookID: chapter.BookID, Drawings: inChapter[index]})
	}
	return counts, nil
}
