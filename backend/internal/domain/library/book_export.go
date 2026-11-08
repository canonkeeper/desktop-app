package library

// BookHead is what the export of a book and a cover from a drawing need to know of the book for an actor.
type BookHead struct {
	ProjectID string
	Title     string
	// AuthorName is the name of the owner of the project: a book has no author of its own in the product.
	AuthorName string
	// CanEdit is the right to change the book and to take it away whole as a file (specs/2026-11-05-book-export.md E1.5).
	CanEdit bool
	// CoverVersion is the active cover, 0 without one.
	CoverVersion int64
}

// ExportChapter is a chapter with text in the book, without its text.
type ExportChapter struct {
	ID       string
	Title    string
	Revision int64
}
