package sagamemory

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// ChangeLink is one state change of a chapter card with the glossary entries it names
// (UC2 of specs/2026-10-14-saga-reference.md: «собрать изменения, где упомянуты персонаж и
// запись»); Subjects are the entries named in the subject of the change.
type ChangeLink struct {
	Chapter  int // reading-order index
	Change   Change
	Entries  []*GlossaryEntry
	Subjects []*GlossaryEntry
}

// Names reports whether the change names the entry.
func (link ChangeLink) Names(entry *GlossaryEntry) bool {
	return contains(link.Entries, entry)
}

// Relates reports whether the change is about the character and the entry together: one of
// them is its subject and the other is named in it. «Мирон: получил кинжал» and «Навык «Взлом»
// Мирона: повысился» relate the hero and the thing; «Алтын-Ир молчит, Ворон ждёт» does not
// relate Ворон to the silence.
func (link ChangeLink) Relates(character, entry *GlossaryEntry) bool {
	return contains(link.Subjects, character) && contains(link.Entries, entry) ||
		contains(link.Subjects, entry) && contains(link.Entries, character)
}

func contains(entries []*GlossaryEntry, entry *GlossaryEntry) bool {
	for _, named := range entries {
		if named == entry {
			return true
		}
	}
	return false
}

// Links returns every change of the cards in story order with the entries it names. An entry
// is named when its card mentions it in that chapter and the stems of all words of its name or
// an alias occur among the stems of the change, so «Взлома» names «Взлом» and «Ворона» names
// the character known as «Ворон». A name needs a word of its own beyond stop words and type
// words, and a name contained in a longer named name of another entry does not count:
// «маска Ворона» is not also «маска». The links are built once per card set.
func (set *CardSet) Links(index *Index) []ChangeLink {
	set.linksOnce.Do(func() { set.links = buildLinks(index, set) })
	return set.links
}

// LinkWords lists the words Links stems: of the entry names and aliases, of the entry types and
// of the changes. The search call index (UC3 of specs/2026-10-28-saga-search-in-postgres.md)
// needs their vocabulary entries before Links, so that they stem as in the whole index.
func (set *CardSet) LinkWords() []string {
	seen := map[string]bool{}
	var words []string
	add := func(text string) {
		for _, token := range Tokenize(Normalize(text)) {
			if !seen[token.Word] {
				seen[token.Word] = true
				words = append(words, token.Word)
			}
		}
	}
	for _, entry := range set.Glossary.Entries {
		add(entry.Name)
		for _, alias := range entry.Aliases {
			add(alias)
		}
	}
	for kind := range set.Glossary.Types() {
		add(kind)
	}
	for _, card := range set.Cards {
		for _, change := range card.Changes {
			add(change.Subject + " " + change.Change)
		}
	}
	sort.Strings(words)
	return words
}

type nameMatch struct {
	entry *GlossaryEntry
	stems []string
}

func buildLinks(index *Index, set *CardSet) []ChangeLink {
	types := typeStems(index, set.Glossary)
	names := map[*GlossaryEntry][][]string{}
	byChapter := map[int][]*GlossaryEntry{}
	for _, entry := range set.Glossary.Entries {
		for _, name := range append([]string{entry.Name}, entry.Aliases...) {
			if stems := nameStems(index, name, types); len(stems) > 0 {
				names[entry] = append(names[entry], stems)
			}
		}
		if len(names[entry]) == 0 {
			continue
		}
		for _, chapter := range entry.Chapters {
			byChapter[chapter] = append(byChapter[chapter], entry)
		}
	}
	chapters := make([]int, 0, len(set.Cards))
	for chapter := range set.Cards {
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	var links []ChangeLink
	for _, chapter := range chapters {
		for _, change := range set.Cards[chapter].Changes {
			links = append(links, ChangeLink{
				Chapter:  chapter,
				Change:   change,
				Entries:  named(byChapter[chapter], names, textStems(index, change.Subject+" "+change.Change)),
				Subjects: named(byChapter[chapter], names, textStems(index, change.Subject)),
			})
		}
	}
	return links
}

// named returns the candidates named in the words, by name, without the names contained in a
// longer named name of another entry; a character is never dropped that way, since «маска
// Ворона» names Ворон too.
func named(candidates []*GlossaryEntry, names map[*GlossaryEntry][][]string, words map[string]bool) []*GlossaryEntry {
	var matches []nameMatch
	for _, entry := range candidates {
		var best []string
		for _, stems := range names[entry] {
			if len(stems) > len(best) && containsAll(words, stems) {
				best = stems
			}
		}
		if best != nil {
			matches = append(matches, nameMatch{entry: entry, stems: best})
		}
	}
	var entries []*GlossaryEntry
	for _, match := range matches {
		if !covered(match, matches) {
			entries = append(entries, match.entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// covered reports whether another entry's named name contains this one: longer, or as long and
// mentioned in more chapters (the concept «Вороны» against the hero «Ворон»).
func covered(match nameMatch, matches []nameMatch) bool {
	if match.entry.Type == characterType {
		return false
	}
	for _, other := range matches {
		if other.entry == match.entry || !subset(match.stems, other.stems) {
			continue
		}
		if len(other.stems) > len(match.stems) || len(other.entry.Chapters) > len(match.entry.Chapters) {
			return true
		}
	}
	return false
}

func subset(stems, of []string) bool {
	for _, stem := range stems {
		found := false
		for _, other := range of {
			if other == stem {
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

// typeStems returns the stems of the words of the glossary types: «навык», «задание».
func typeStems(index *Index, glossary *Glossary) map[string]bool {
	stems := map[string]bool{}
	for kind := range glossary.Types() {
		for _, token := range Tokenize(Normalize(kind)) {
			if stem := index.stem(token.Word); stem != "" {
				stems[stem] = true
			}
		}
	}
	return stems
}

// nameStems stems the words of a name with the version's russian_stem lexemes; stop words drop
// out and a hyphenated word counts by its parts. A name gives nothing without a word of its own:
// a stem of three or more letters that is not a type word («Навыки», «Навык «я не я»»).
func nameStems(index *Index, name string, types map[string]bool) []string {
	var stems []string
	own := false
	tokens := Tokenize(Normalize(name))
	for i, token := range tokens {
		if i > 0 && tokens[i-1].Pos == token.Pos && joinedForm(tokens, i) {
			continue
		}
		stem := index.stem(token.Word)
		if stem == "" {
			continue
		}
		if utf8.RuneCountInString(stem) >= 3 && !types[stem] {
			own = true
		}
		stems = append(stems, stem)
	}
	if !own {
		return nil
	}
	return stems
}

// joinedForm reports whether the token at i is the joined form of the hyphenated parts before it.
func joinedForm(tokens []Token, i int) bool {
	joined := ""
	for j := i - 1; j >= 0 && tokens[j].Pos == tokens[i].Pos; j-- {
		joined = tokens[j].Word + joined
	}
	return joined == tokens[i].Word
}

func textStems(index *Index, text string) map[string]bool {
	words := map[string]bool{}
	for _, token := range Tokenize(Normalize(strings.TrimSpace(text))) {
		if stem := index.stem(token.Word); stem != "" {
			words[stem] = true
		}
	}
	return words
}

func containsAll(words map[string]bool, stems []string) bool {
	for _, stem := range stems {
		if !words[stem] {
			return false
		}
	}
	return true
}
