package librarystore

import (
	"context"

	"canonkeeper/backend/internal/domain/shelf"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type bookPlacesQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// placeBooks gives the living books of a page their number of «Книга N» — the rank among all the living books of the saga
// in the order of the shelf (shelf.Places, specs/2026-11-11-ux7-reorder.md RK1.6); the stored order may hold gaps.
func placeBooks(ctx context.Context, queryer bookPlacesQueryer, projectID pgtype.UUID, books []library.Book) error {
	if len(books) == 0 {
		return nil
	}
	rows, err := queryer.Query(ctx, `
		SELECT id, "order" FROM books WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID)
	if err != nil {
		return err
	}
	shelved, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (shelf.Book, error) {
		var id pgtype.UUID
		var order int
		err := row.Scan(&id, &order)
		return shelf.Book{ID: storedb.UUIDString(id), Order: order}, err
	})
	if err != nil {
		return err
	}
	places := shelf.Places(shelved)
	for index := range books {
		books[index].Place = places[books[index].ID]
	}
	return nil
}
