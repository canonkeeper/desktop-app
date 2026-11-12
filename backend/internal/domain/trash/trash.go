// Package trash is «Недавно удалённые» (specs/2026-11-11-ux7-trash.md TB1): one list of what an author deleted and may
// return, in every saga of his, with where it was and until when it waits.
package trash

import "time"

// GroupLimit is the most rows a group of the list carries; Total says how many there are.
const GroupLimit = 100

// Saga is a deleted saga its owner or administrator returns.
type Saga struct {
	ID           string
	Title        string
	Books        int
	DeletedAt    time.Time
	RestoreUntil time.Time
}

// Book is a deleted book of a live saga. It has no number of the shelf: «Книга N» is a place among the living books
// (specs/2026-11-11-ux7-reorder.md RK1.6); it is named by its title.
type Book struct {
	ID           string
	ProjectID    string
	SagaTitle    string
	Title        string
	DeletedAt    time.Time
	RestoreUntil time.Time
}

// Chapter is a deleted chapter of a live book.
type Chapter struct {
	ID                string
	ProjectID         string
	SagaTitle         string
	BookID            string
	BookTitle         string
	BookDisplayNumber string // the place of its living book on the shelf, «Книга N» (RK1.6)
	Title             string
	Order             int
	DeletedAt         time.Time
	RestoreUntil      time.Time
}

// BookUpdate is a live book of a live saga updated from a file whose version before the update can still be returned
// (specs/2026-11-12-ux10-book-revert.md BR3.3): revertBookUpdate returns it until RevertUntil.
type BookUpdate struct {
	BookID      string
	ProjectID   string
	SagaTitle   string
	Title       string
	UpdatedAt   time.Time
	RevertUntil time.Time
}

// Drawing is a deleted drawing of a live saga (TB3.1): its title and the number of its current version, if it has one.
type Drawing struct {
	ID           string
	ProjectID    string
	SagaTitle    string
	Title        string
	Number       int
	DeletedAt    time.Time
	RestoreUntil time.Time
}

// Group is a part of the list newest first: at most GroupLimit rows and how many there are.
type Group[T any] struct {
	Items []T
	Total int
}

// Sources are the sagas, books and chapters of the list and the updates of books that can be returned.
type Sources struct {
	Sagas       Group[Saga]
	Books       Group[Book]
	Chapters    Group[Chapter]
	BookUpdates Group[BookUpdate]
}

// List is the whole of «Недавно удалённые»; Drawings is nil in a build without drawings.
type List struct {
	Sources
	Drawings *Group[Drawing]
}
