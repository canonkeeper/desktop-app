package sagamemory

import (
	"sort"
	"strings"
)

// The reads of the saga map and the shared saga pages (specs/2026-11-04-saga-map.md,
// specs/2026-10-14-saga-reference.md): the glossary, the chapters naming an entry, the changes naming
// it and the chapter summaries of the cards.

// Coverage says for how many chapters of a version the cards exist.
type Coverage struct {
	Chapters  int
	WithCards int
}

func (set *CardSet) Coverage(version *Version) Coverage {
	return Coverage{Chapters: len(version.Chapters), WithCards: len(set.Cards)}
}

// TypeCount is the number of glossary entries of one type.
type TypeCount struct {
	Type  string
	Count int
}

// TypeCounts lists the entry types, most entries first.
func (glossary *Glossary) TypeCounts() []TypeCount {
	counts := glossary.Types()
	out := make([]TypeCount, 0, len(counts))
	for kind, count := range counts {
		out = append(out, TypeCount{Type: kind, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Count > out[j].Count || out[i].Count == out[j].Count && out[i].Type < out[j].Type
	})
	return out
}

// EntryKey is the stable reference of an entry in links: its normalized name, numbered when an
// earlier entry of another kind has the same name.
func EntryKey(entry *GlossaryEntry) string {
	return entry.key
}

// Lookup finds an entry by its key, name, alias or role; a character goes before a thing with the
// same name, so that the bird «Ворон» does not stand in for the hero (R5.5).
func (glossary *Glossary) Lookup(name string) (*GlossaryEntry, bool) {
	key := glossaryKey(name)
	if entry, ok := glossary.characters[key]; ok {
		return entry, true
	}
	entry, ok := glossary.byKey[key]
	return entry, ok
}

// Characters lists the characters, most chapters first.
func (glossary *Glossary) Characters(limit int) []*GlossaryEntry {
	var characters []*GlossaryEntry
	for _, entry := range glossary.Entries {
		if entry.Type == characterType {
			characters = append(characters, entry)
		}
	}
	sort.SliceStable(characters, func(i, j int) bool {
		if len(characters[i].Chapters) != len(characters[j].Chapters) {
			return len(characters[i].Chapters) > len(characters[j].Chapters)
		}
		return characters[i].Name < characters[j].Name
	})
	if limit > 0 && len(characters) > limit {
		characters = characters[:limit]
	}
	return characters
}

// EntryMention is a chapter naming an entry; P is the paragraph of the first mention, 0 when
// the card gave none.
type EntryMention struct {
	Chapter *Chapter
	P       int
}

// Mentions lists the chapters naming an entry in story order.
func (set *CardSet) Mentions(version *Version, entry *GlossaryEntry) []EntryMention {
	keys := map[string]bool{}
	for _, key := range entry.Keys() {
		keys[key] = true
	}
	mentions := make([]EntryMention, 0, len(entry.Chapters))
	for _, index := range entry.Chapters {
		mention := EntryMention{Chapter: &version.Chapters[index]}
		if card := set.Cards[index]; card != nil {
			mention.P = firstParagraph(card, keys)
		}
		mentions = append(mentions, mention)
	}
	return mentions
}

func firstParagraph(card *Card, keys map[string]bool) int {
	for _, group := range [][]Mention{card.Characters, card.Entities} {
		for _, mention := range group {
			if keys[glossaryKey(mention.Name)] {
				return mention.P
			}
			for _, alias := range mention.Aliases {
				if keys[glossaryKey(alias)] {
					return mention.P
				}
			}
		}
	}
	return 0
}

// EntryChanges lists the changes naming an entry — or, when with is set, the changes relating
// the two (one is the subject, the other is named) — up to a chapter index (inclusive;
// negative for the whole saga), in story order.
func (set *CardSet) EntryChanges(index *Index, entry, with *GlossaryEntry, to int) []ChangeLink {
	var changes []ChangeLink
	for _, link := range set.Links(index) {
		if to >= 0 && link.Chapter > to {
			break
		}
		if with == nil && link.Names(entry) || with != nil && link.Relates(with, entry) {
			changes = append(changes, link)
		}
	}
	return changes
}

// Book is one book of a version with the reading-order indexes of its first and last chapters.
type Book struct {
	ID          string
	Order       int
	Title       string
	First, Last int
}

// Books lists the books of the version in reading order.
func (version *Version) Books() []Book {
	var books []Book
	for i := range version.Chapters {
		chapter := &version.Chapters[i]
		if n := len(books); n > 0 && books[n-1].ID == chapter.BookID {
			books[n-1].Last = i
			continue
		}
		books = append(books, Book{ID: chapter.BookID, Order: chapter.BookOrder, Title: chapter.BookTitle, First: i, Last: i})
	}
	return books
}

// ContentsChapter is a chapter with the summary of its card.
type ContentsChapter struct {
	Chapter *Chapter
	Summary string
	HasCard bool
}

// Contents lists the chapters from and to (inclusive) with their summaries; with a query, only
// the chapters whose title or summary contains every query word start, at most limit of them.
func (set *CardSet) Contents(version *Version, from, to int, query string, limit int) []ContentsChapter {
	words := queryWords(query)
	var chapters []ContentsChapter
	for index := max(from, 0); index <= to && index < len(version.Chapters); index++ {
		chapter := &version.Chapters[index]
		item := ContentsChapter{Chapter: chapter}
		if card := set.Cards[index]; card != nil {
			item.Summary, item.HasCard = card.Summary, true
		}
		if len(words) > 0 && !hasWordStarts(chapter.Title+" "+item.Summary, words) {
			continue
		}
		if limit > 0 && len(chapters) == limit {
			break
		}
		chapters = append(chapters, item)
	}
	return chapters
}

func queryWords(query string) []string {
	var words []string
	for _, token := range Tokenize(Normalize(query)) {
		words = append(words, token.Word)
	}
	return words
}

// hasWordStarts reports whether every query word starts a word of the text.
func hasWordStarts(text string, words []string) bool {
	if len(words) == 0 {
		return true
	}
	tokens := Tokenize(Normalize(text))
	for _, word := range words {
		found := false
		for _, token := range tokens {
			if strings.HasPrefix(token.Word, word) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
