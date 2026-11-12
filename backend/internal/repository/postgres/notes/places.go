package notes

import (
	"context"

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
