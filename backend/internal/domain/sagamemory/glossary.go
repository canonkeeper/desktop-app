package sagamemory

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// CardSet is the saga memory of one text version: the card of each chapter that has one, by
// reading-order index, and the glossary built from them.
type CardSet struct {
	Cards    map[int]*Card
	Glossary *Glossary
	// Names is the name registry of the saga, nil until a cards Job built it; a glossary of a part
	// of the cards is built with it too.
	Names *NameRegistry

	linksOnce sync.Once
	links     []ChangeLink
}

func NewCardSet(cards map[int]*Card, names *NameRegistry) *CardSet {
	return &CardSet{Cards: cards, Glossary: BuildGlossary(cards, names), Names: names}
}

// GlossaryEntry is one saga-wide name: a character or named entity merged across chapter
// cards by name and alias.
type GlossaryEntry struct {
	Name     string
	Type     string
	Aliases  []string
	Roles    []string // keys of what leads to the entry without being shown: roles of the registry and names the cards do not write
	Note     string
	Chapters []int // reading-order indexes of the chapters that mention it

	key string // the normalized name, numbered when another entry already has it
}

// Glossary is the saga dictionary built from chapter cards (R3.1 glossary).
type Glossary struct {
	Entries    []*GlossaryEntry
	byKey      map[string]*GlossaryEntry
	characters map[string]*GlossaryEntry // the names of characters, which Lookup prefers
}

const characterType = "персонаж"

// GlossaryKey is the normalized form names of the glossary are compared by.
func GlossaryKey(name string) string { return glossaryKey(name) }

func glossaryKey(name string) string {
	name = strings.Trim(Normalize(strings.TrimSpace(name)), "[]«»\"'().,:;!? ")
	return strings.Join(strings.Fields(name), " ")
}

// descriptorNames is how many differently named mentions may share an alias before it counts
// as a description («мужчина», «король», «вор») rather than a name: a description neither
// merges mentions nor names an entry.
const descriptorNames = 4

// BuildGlossary merges the mentions of the cards, keyed by chapter reading-order index.
// Characters are typed "персонаж"; an entity's type is its most frequent card type. A mention
// merges into the first entry that already has its name or one of its aliases; descriptions
// («юноша», «его высочество», «мужчина») do not count as aliases, «Навык «Взлом»» also answers
// to «Взлом», and an entity named by its own type («Навыки» of type «навык») names the
// category, not a thing, so it is left out.
//
// A registry goes first (specs/2026-11-01-index-foundation.md, R5.4): a mention named by a name or
// a role of a person belongs to that person's one entry, whatever the card typed it; a shared name
// has its own entry, is nobody's alias and pulls nothing in by its aliases; a wrong pair and an
// alias of another person are dropped. Other names follow the rules above; nil is no registry.
func BuildGlossary(cards map[int]*Card, registry *NameRegistry) *Glossary {
	glossary := &Glossary{byKey: map[string]*GlossaryEntry{}}
	registered := newNameIndex(registry)
	descriptions := descriptionAliases(cards, registered)
	spellings := registered.spellings(cards)
	votes := map[*GlossaryEntry]map[string]int{}
	// The names of characters and of things are merged apart: the skill «ужас» is not the
	// character nicknamed «Ужас»; byKey keeps the first entry of each name for lookups.
	kinds := map[bool]map[string]*GlossaryEntry{true: {}, false: {}}
	glossary.characters = kinds[true]
	newEntry := func(name string) *GlossaryEntry {
		key := glossaryKey(name)
		if _, taken := glossary.byKey[key]; taken {
			key += "#" + strconv.Itoa(len(glossary.Entries)+1)
		}
		entry := &GlossaryEntry{Name: strings.TrimSpace(name), key: key}
		glossary.Entries = append(glossary.Entries, entry)
		glossary.byKey[key] = entry
		votes[entry] = map[string]int{}
		return entry
	}
	persons := map[int]*GlossaryEntry{}
	thingNotes := map[*GlossaryEntry]bool{} // a person's note taken from a thing named like him
	person := func(at int, mentioned string) *GlossaryEntry {
		if entry := persons[at]; entry != nil {
			return entry
		}
		name, aliases, keysOnly := registered.persons[at].shown(spellings)
		if name == "" {
			name = mentioned // the cards write only a role of this person
		}
		entry := newEntry(name)
		entry.Aliases = aliases
		entry.Roles = append(append([]string{}, registered.persons[at].roles...), keysOnly...)
		persons[at] = entry
		for _, key := range entry.Keys() {
			kinds[true][key] = entry
			if _, ok := glossary.byKey[key]; !ok {
				glossary.byKey[key] = entry
			}
		}
		return entry
	}
	add := func(chapter int, mention Mention, kind string) {
		if kind == "" && categoryName(mention.Name, mention.Type) {
			return
		}
		character := kind == characterType
		nameKey := glossaryKey(mention.Name)
		var entry *GlossaryEntry
		var names []string
		thingOfPerson := false
		if at, ok := registered.of[nameKey]; ok {
			entry = person(at, mention.Name)
			thingOfPerson = !character
			kind, character = characterType, true
			for _, alias := range mention.Aliases {
				key := glossaryKey(alias)
				if _, known := registered.of[key]; !known && registered.allows(nameKey, key) && aliasName(alias, characterType, descriptions) {
					names = append(names, alias)
				}
			}
		} else {
			names = []string{mention.Name}
			if !registered.shared[nameKey] {
				if bare := withoutTypeWord(mention.Name, mention.Type); bare != "" && !registered.shared[glossaryKey(bare)] {
					names = append(names, bare)
				}
				for _, alias := range mention.Aliases {
					if registered.allows(nameKey, glossaryKey(alias)) && aliasName(alias, kind, descriptions) {
						names = append(names, alias)
					}
				}
			}
			for _, name := range names {
				key := glossaryKey(name)
				if key == "" {
					continue
				}
				if found, ok := kinds[character][key]; ok {
					entry = found
					break
				}
				if at, ok := registered.of[key]; ok && character {
					entry = person(at, name)
					break
				}
			}
			if entry == nil {
				if nameKey == "" {
					return
				}
				entry = newEntry(mention.Name)
			}
		}
		for _, name := range names {
			key := glossaryKey(name)
			if key == "" {
				continue
			}
			if _, known := registered.of[key]; known && character {
				continue // a person's name goes to that person's entry alone
			}
			if _, ok := kinds[character][key]; !ok {
				kinds[character][key] = entry
				if key != entry.key && key != glossaryKey(entry.Name) {
					entry.Aliases = append(entry.Aliases, strings.TrimSpace(name))
				}
			}
			if _, ok := glossary.byKey[key]; !ok {
				glossary.byKey[key] = entry
			}
		}
		if kind == "" {
			kind = strings.ToLower(strings.TrimSpace(mention.Type))
		}
		if kind != "" {
			votes[entry][kind]++
		}
		note := strings.TrimSpace(mention.Note)
		// The note of a thing named like a person («Ворон», the bird) never replaces his own.
		if note != "" && (entry.Note == "" || !thingOfPerson && (thingNotes[entry] || len([]rune(note)) > len([]rune(entry.Note)) && len([]rune(note)) < 240)) {
			entry.Note, thingNotes[entry] = note, thingOfPerson
		}
		if n := len(entry.Chapters); n == 0 || entry.Chapters[n-1] != chapter {
			entry.Chapters = append(entry.Chapters, chapter)
		}
	}
	chapters := make([]int, 0, len(cards))
	for chapter := range cards {
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	for _, chapter := range chapters {
		card := cards[chapter]
		for _, mention := range card.Characters {
			add(chapter, mention, characterType)
		}
		for _, mention := range card.Entities {
			add(chapter, mention, "")
		}
	}
	for entry, counts := range votes {
		best, count := "", 0
		for kind, n := range counts {
			if n > count || n == count && kind < best {
				best, count = kind, n
			}
		}
		entry.Type = best
	}
	// Lookup prefers characters (R5.5), so a thing whose key is a character's name gets a key of its
	// own and links to it stay links to it.
	for at, entry := range glossary.Entries {
		if found, ok := kinds[true][entry.key]; ok && found != entry {
			entry.key += "#" + strconv.Itoa(at+1)
			glossary.byKey[entry.key] = entry
		}
	}
	return glossary
}

// aliasName reports whether an alias names the mention rather than describes it: it is not
// shared by many names, and a character's alias is capitalized («Ворон», not «юноша»).
func aliasName(alias, kind string, descriptions map[string]bool) bool {
	if key := glossaryKey(alias); key == "" || descriptions[key] {
		return false
	}
	if kind != characterType {
		return true
	}
	first, _ := utf8.DecodeRuneInString(strings.TrimLeft(alias, "«»\"'([ "))
	return unicode.IsUpper(first)
}

// descriptionAliases returns the alias keys that cards give to descriptorNames or more
// differently named mentions. The names of one person of the registry count as one name, and an
// alias the registry knows is never a description.
func descriptionAliases(cards map[int]*Card, registered *nameIndex) map[string]bool {
	named := map[string]map[string]bool{}
	for _, card := range cards {
		for _, group := range [][]Mention{card.Characters, card.Entities} {
			for _, mention := range group {
				name := glossaryKey(mention.Name)
				for _, alias := range mention.Aliases {
					key := glossaryKey(alias)
					if key == "" || key == name || registered.knows(key) {
						continue
					}
					if named[key] == nil {
						named[key] = map[string]bool{}
					}
					if at, ok := registered.of[name]; ok {
						named[key]["\x00"+strconv.Itoa(at)] = true
					} else {
						named[key][name] = true
					}
				}
			}
		}
	}
	descriptions := map[string]bool{}
	for key, names := range named {
		if len(names) >= descriptorNames {
			descriptions[key] = true
		}
	}
	return descriptions
}

// categoryName reports whether an entity is named by one word of its own type.
func categoryName(name, kind string) bool {
	words := strings.Fields(glossaryKey(name))
	return len(words) == 1 && typeWord(words[0], glossaryKey(kind))
}

// withoutTypeWord drops a leading type word before a quoted name: «Навык «Взлом»» → «Взлом»;
// «Этаж Ворона» stays whole, it is Ворон's floor.
func withoutTypeWord(name, kind string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "«") && !strings.HasPrefix(fields[1], "\"") || !typeWord(glossaryKey(fields[0]), glossaryKey(kind)) {
		return ""
	}
	return strings.Trim(strings.Join(fields[1:], " "), "«»\"' ")
}

// typeWord compares a word with a type by the type without its last letter, so that the
// plural and case forms of the type («навыки», «задания») count as the type itself.
func typeWord(word, kind string) bool {
	runes := []rune(kind)
	if len(runes) < 4 || strings.Contains(kind, " ") {
		return word != "" && word == kind
	}
	return strings.HasPrefix(word, string(runes[:len(runes)-1])) && len([]rune(word)) <= len(runes)+2
}

// Find returns the entries whose name, alias, role or note contains the query, optionally only of
// the given types, most frequently mentioned first.
func (glossary *Glossary) Find(query string, types []string) []*GlossaryEntry {
	query = glossaryKey(query)
	wanted := map[string]bool{}
	for _, kind := range types {
		if kind = strings.ToLower(strings.TrimSpace(kind)); kind != "" {
			wanted[kind] = true
		}
	}
	var found []*GlossaryEntry
	for _, entry := range glossary.Entries {
		if len(wanted) > 0 && !wanted[entry.Type] {
			continue
		}
		if query != "" {
			if !strings.Contains(entry.searchText(), query) {
				continue
			}
		}
		found = append(found, entry)
	}
	sort.SliceStable(found, func(i, j int) bool {
		if len(found[i].Chapters) != len(found[j].Chapters) {
			return len(found[i].Chapters) > len(found[j].Chapters)
		}
		return found[i].Name < found[j].Name
	})
	return found
}

// searchText is what a query of the glossary looks in: the name, aliases, roles and note.
func (entry *GlossaryEntry) searchText() string {
	return glossaryKey(entry.Name) + " | " + glossaryKey(strings.Join(entry.Aliases, " | ")) + " | " + strings.Join(entry.Roles, " | ") + " | " + glossaryKey(entry.Note)
}

// Types returns the number of entries of each type.
func (glossary *Glossary) Types() map[string]int {
	counts := map[string]int{}
	for _, entry := range glossary.Entries {
		counts[entry.Type]++
	}
	return counts
}
