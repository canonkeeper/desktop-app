// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// OrderSource says where the place of a note in its book comes from (OB3.3).
type OrderSource string

const (
	// OrderFront: the number order or chapter of its header.
	OrderFront OrderSource = "front"
	// OrderIndex: the place of its link in the note of the index of the book.
	OrderIndex OrderSource = "index"
	// OrderName: its path in the natural order.
	OrderName OrderSource = "name"
)

var wikiLink = regexp.MustCompile(`\[\[([^\]|#^]+)(?:[#^][^\]|]*)?(?:\|[^\]]*)?\]\]`)

// indexLinks are the notes an index links in the order of its links, by their names without .md in lower case: a link
// names a note by its name or by its path.
func indexLinks(index Note) []string {
	var names []string
	for _, match := range wikiLink.FindAllStringSubmatch(index.Body, -1) {
		names = append(names, linkKey(match[1]))
	}
	return names
}

func linkKey(target string) string {
	target = strings.ToLower(NFC(strings.TrimSpace(target)))
	return strings.TrimSuffix(target, ".md")
}

// frontNumber is the number order or chapter of the header of a note.
func frontNumber(note Note) (float64, bool) {
	for _, key := range []string{"order", "chapter"} {
		if value, ok := note.Front.Get(key); ok && !value.IsList {
			if number, err := strconv.ParseFloat(strings.TrimSpace(value.Text), 64); err == nil {
				return number, true
			}
		}
	}
	return 0, false
}

// orderNotes puts the notes of a book in their order (OB3.3): first those with the number of their header, by it; then
// those the index links, in the order of its links; then the rest by their paths in the natural order. Equal places go by
// path. The order of the walk of the system does not count.
func orderNotes(notes []Note, index *Note) ([]Note, []OrderSource) {
	links := map[string]int{}
	if index != nil {
		for place, name := range indexLinks(*index) {
			if _, taken := links[name]; !taken {
				links[name] = place
			}
		}
	}
	type placed struct {
		note   Note
		source OrderSource
		number float64
	}
	placedNotes := make([]placed, 0, len(notes))
	for _, note := range notes {
		item := placed{note: note, source: OrderName}
		if number, ok := frontNumber(note); ok {
			item.source, item.number = OrderFront, number
		} else if place, ok := linkPlace(links, note); ok {
			item.source, item.number = OrderIndex, float64(place)
		}
		placedNotes = append(placedNotes, item)
	}
	rank := map[OrderSource]int{OrderFront: 0, OrderIndex: 1, OrderName: 2}
	slices.SortStableFunc(placedNotes, func(left, right placed) int {
		if left.source != right.source {
			return rank[left.source] - rank[right.source]
		}
		if left.source != OrderName && left.number != right.number {
			if left.number < right.number {
				return -1
			}
			return 1
		}
		switch {
		case left.note.Path == right.note.Path:
			return 0
		case NaturalLess(left.note.Path, right.note.Path):
			return -1
		}
		return 1
	})
	ordered := make([]Note, len(placedNotes))
	sources := make([]OrderSource, len(placedNotes))
	for index, item := range placedNotes {
		ordered[index], sources[index] = item.note, item.source
	}
	return ordered, sources
}

// linkPlace is the place of the link to a note in an index, by the name of the note or by its path.
func linkPlace(links map[string]int, note Note) (int, bool) {
	if place, ok := links[linkKey(note.Name)]; ok {
		return place, true
	}
	place, ok := links[linkKey(note.Path)]
	return place, ok
}

// linksAnother says an index links a note of the book other than itself.
func linksAnother(index Note, notes []Note) bool {
	links := map[string]int{}
	for place, name := range indexLinks(index) {
		links[name] = place
	}
	for _, note := range notes {
		if note.Path == index.Path {
			continue
		}
		if _, ok := linkPlace(links, note); ok {
			return true
		}
	}
	return false
}
