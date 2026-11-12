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

// ExportChapter is a chapter with text in the book, without its text: its book and the number of its paragraphs, all of
// them, as the place of a drawing counts them (specs/2026-11-12-ux10-book-pictures.md BP1.1).
type ExportChapter struct {
	ID         string
	BookID     string
	Title      string
	Revision   int64
	Paragraphs int
}
