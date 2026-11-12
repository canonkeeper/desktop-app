// Package shelf numbers the books of a saga as the author reads them (specs/2026-11-11-ux7-reorder.md RK1.6). It performs
// no I/O.
package shelf

import "sort"

// Book is a living book of a saga: its id and its stored order on the shelf. The stored order keeps the place a deleted
// book comes back to and may leave gaps; the number the author reads does not.
type Book struct {
	ID    string
	Order int
}

// Places are the numbers of «Книга N» — on the shelf, in the reader, in the places of chapters, on the scale of the map,
// in the contents, in the window of the move, for the helpers and the voice: the rank of a book among the living books
// of the saga in the order of the shelf, 1…n without gaps. A deleted book has none.
func Places(books []Book) map[string]int {
	sorted := append([]Book(nil), books...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Order != sorted[j].Order {
			return sorted[i].Order < sorted[j].Order
		}
		return sorted[i].ID < sorted[j].ID
	})
	places := make(map[string]int, len(sorted))
	for i, book := range sorted {
		places[book.ID] = i + 1
	}
	return places
}
