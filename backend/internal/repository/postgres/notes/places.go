package notes

import (
	"context"

	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/domain/shelf"

	"github.com/jackc/pgx/v5"
)

type placesQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// shelfPlaces are the places of the living books of the sagas on their shelves, «Книга N» — the one rule of the number
// (shelf.Places, specs/2026-11-11-ux7-reorder.md RK1.6) — by saga and book.
func shelfPlaces(ctx context.Context, queryer placesQueryer, projects []string) (map[string]map[string]int, error) {
	places := map[string]map[string]int{}
	if len(projects) == 0 {
		return places, nil
	}
	rows, err := queryer.Query(ctx, `
		SELECT project_id::text, id::text, "order" FROM books WHERE project_id = ANY($1::uuid[]) AND deleted_at IS NULL
	`, projects)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shelves := map[string][]shelf.Book{}
	for rows.Next() {
		var project string
		var book shelf.Book
		if err := rows.Scan(&project, &book.ID, &book.Order); err != nil {
			return nil, err
		}
		shelves[project] = append(shelves[project], book)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for project, books := range shelves {
		places[project] = shelf.Places(books)
	}
	return places, nil
}

// chapterPlaces are the places of the published living chapters of the books in them, by chapter — the one rule of the
// number (sagamemory.PlacesInBooks, specs/2026-11-14-chapter-place-number.md CP1.1): from one and without gaps in the order
// of the published text, whatever the stored order is.
func chapterPlaces(ctx context.Context, queryer placesQueryer, books []string) (map[string]int, error) {
	places := map[string]int{}
	if len(books) == 0 {
		return places, nil
	}
	rows, err := queryer.Query(ctx, `
		SELECT id::text, book_id::text, "order" FROM chapters
		WHERE book_id = ANY($1::uuid[]) AND deleted_at IS NULL AND published_revision IS NOT NULL
	`, books)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chapters []sagamemory.Chapter
	for rows.Next() {
		var chapter sagamemory.Chapter
		if err := rows.Scan(&chapter.ID, &chapter.BookID, &chapter.Order); err != nil {
			return nil, err
		}
		chapters = append(chapters, chapter)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sagamemory.PlacesInBooks(chapters)
	for _, chapter := range chapters {
		places[chapter.ID] = chapter.InBook
	}
	return places, nil
}
