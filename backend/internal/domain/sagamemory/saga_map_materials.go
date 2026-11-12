package sagamemory

import (
	"strings"
	"unicode"
)

// The author's materials on the saga map (specs/2026-11-04-saga-map.md, R9.5): a record the book knows by name is no
// node of its own, its text goes to the card of the book's node; a record of a map category the book does not know is a
// node without chapters, linked to the nodes it names. When the book comes to know the name, the node merges by itself.

const (
	// maxMaterialNodes bounds the nodes of the materials on a map.
	maxMaterialNodes = 100
	// maxMaterialLinks bounds the links of one record, in the order of the map.
	maxMaterialLinks = 8
	// materialNodeType is the type a node of the materials shows.
	materialNodeType = "из ваших материалов · ещё не в книге"
	// minNamedRunes is the shortest name a text is searched for: a shorter one would be found in any text.
	minNamedRunes = 3
	// minStemRunes is how many letters a word keeps without its case ending.
	minStemRunes = 3
)

// MapMaterial is a record of the materials as the map needs it. Category is empty for a kind the map does not draw; Text is
// everything the record says, for finding the names it speaks of; Card is what the card of a node prints of it.
type MapMaterial struct {
	Name     string
	Category MapCategory
	Text     string
	Card     CardMaterial
}

// CardMaterialProperty is a property of a record, in the shape of the API.
type CardMaterialProperty struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// CardMaterial is a record of the materials on the card of a node, in the shape of the API.
type CardMaterial struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"`
	Description string                 `json:"description"`
	Properties  []CardMaterialProperty `json:"properties"`
	Section     string                 `json:"section"`
	SourceName  string                 `json:"sourceName"`
	SourceKind  string                 `json:"sourceKind"`
	Revision    int64                  `json:"revision"`
}

// CardMaterialRef is a record that names the node of a card: the card leads to it and prints nothing of what it says.
type CardMaterialRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// IsMaterialType says whether a type is the one a record the book does not know shows.
func IsMaterialType(kind string) bool { return kind == materialNodeType }

// MaterialEntry is the entry of a record the book does not know.
func MaterialEntry(name string) *GlossaryEntry {
	return &GlossaryEntry{Name: name, Type: materialNodeType, key: glossaryKey(name)}
}

// AddMaterials puts the records the book does not know on the map and links each to the nodes it names.
func (m *Map) AddMaterials(glossary *Glossary, materials []MapMaterial) {
	added := 0
	type texted struct {
		node int
		text namedText
	}
	var nodes []texted
	for _, material := range materials {
		if _, known := glossary.Lookup(material.Name); known || material.Category == "" || added == maxMaterialNodes {
			continue
		}
		added++
		entry := MaterialEntry(material.Name)
		m.nodeOf[entry] = len(m.Nodes)
		nodes = append(nodes, texted{node: len(m.Nodes), text: newNamedText(material.Text)})
		m.Nodes = append(m.Nodes, MapNode{Entry: entry, Category: material.Category, Material: true})
		m.Total++
	}
	for _, source := range nodes {
		links := 0
		for target := range m.Nodes {
			if target != source.node && links < maxMaterialLinks && source.text.names(m.Nodes[target].Entry) {
				links++
				m.Links = append(m.Links, MapLink{Source: min(source.node, target), Target: max(source.node, target), Material: true})
			}
		}
	}
}

// uniqueMaterials keeps the first record of a name the map can tell from the others: two rows of one key cannot stand.
func uniqueMaterials(materials []MapMaterial) []MapMaterial {
	seen := make(map[string]bool, len(materials))
	unique := make([]MapMaterial, 0, len(materials))
	for _, material := range materials {
		if key := glossaryKey(material.Name); key != "" && !seen[key] {
			seen[key] = true
			unique = append(unique, material)
		}
	}
	return unique
}

// addMaterialRows gives the rows of the map what the records say (R9.5): a record the book knows goes to the node of its entry, any
// other has a node of its own, on the map or not, so its card opens and a search finds it; every record that names an entry goes
// to the card of that entry. It returns the entries of the records the book does not know.
func addMaterialRows(graph *Map, glossary *Glossary, materials []MapMaterial, nodes map[*GlossaryEntry]*StoredNode) []*GlossaryEntry {
	drawn := make(map[string]*GlossaryEntry)
	for _, node := range graph.Nodes {
		if node.Material {
			drawn[node.Entry.key] = node.Entry
		}
	}
	var added []*GlossaryEntry
	for i := range materials {
		material := &materials[i]
		entry, known := glossary.Lookup(material.Name)
		if !known {
			if entry = drawn[glossaryKey(material.Name)]; entry == nil {
				entry = MaterialEntry(material.Name)
			}
			nodes[entry] = &StoredNode{Key: entry.key, Name: entry.Name, Type: entry.Type, Category: string(material.Category), SearchText: entry.searchText()}
			added = append(added, entry)
		}
		node := nodes[entry]
		if node.Material == nil {
			node.Material = &material.Card
		}
		node.SearchText += " | " + glossaryKey(material.Name) + " | " + glossaryKey(material.Text)
	}
	names := newEntryNames(nodes)
	for i := range materials {
		for _, entry := range names.namedIn(newNamedText(materials[i].Text)) {
			if node := nodes[entry]; node.Material == nil || node.Material.ID != materials[i].Card.ID {
				node.NamedIn = append(node.NamedIn, CardMaterialRef{ID: materials[i].Card.ID, Name: materials[i].Card.Name, Kind: materials[i].Card.Kind})
			}
		}
	}
	return added
}

// entryNames finds the entries a text names without trying every name of a saga on it: a name is looked at only when the text has its
// first word.
type entryNames map[string][]indexedName

type indexedName struct {
	stems []string
	entry *GlossaryEntry
}

func newEntryNames(nodes map[*GlossaryEntry]*StoredNode) entryNames {
	index := make(entryNames, len(nodes))
	for entry := range nodes {
		for _, name := range append([]string{entry.Name}, entry.Aliases...) {
			if stems := wordStems(name); len(stems) > 0 {
				index[stems[0]] = append(index[stems[0]], indexedName{stems, entry})
			}
		}
	}
	return index
}

func (index entryNames) namedIn(text namedText) []*GlossaryEntry {
	seen := make(map[*GlossaryEntry]bool)
	var found []*GlossaryEntry
	for stem := range text.at {
		for _, name := range index[stem] {
			if !seen[name.entry] && text.hasStems(name.stems) {
				seen[name.entry] = true
				found = append(found, name.entry)
			}
		}
	}
	return found
}

// namedText is a text as a sequence of word stems, so a name is found in any of its cases.
type namedText struct {
	stems []string
	at    map[string][]int
}

func newNamedText(text string) namedText {
	stems := wordStems(text)
	at := make(map[string][]int, len(stems))
	for index, stem := range stems {
		at[stem] = append(at[stem], index)
	}
	return namedText{stems: stems, at: at}
}

func (text namedText) names(entry *GlossaryEntry) bool {
	if text.hasName(entry.Name) {
		return true
	}
	for _, alias := range entry.Aliases {
		if text.hasName(alias) {
			return true
		}
	}
	return false
}

// hasName finds the words of a name in a row: «Мирона» names «Мирон» and «Белого порта» names «Белый порт», but «засада» does
// not name «сада» and «милость» does not name «Мила».
func (text namedText) hasName(name string) bool { return text.hasStems(wordStems(name)) }

func (text namedText) hasStems(wanted []string) bool {
	if len(wanted) == 0 || len(wanted) == 1 && len([]rune(wanted[0])) < minNamedRunes {
		return false
	}
next:
	for _, start := range text.at[wanted[0]] {
		if start+len(wanted) > len(text.stems) {
			continue
		}
		for offset, stem := range wanted {
			if text.stems[start+offset] != stem {
				continue next
			}
		}
		return true
	}
	return false
}

// wordEndings are the case endings of Russian names and nouns, the longest first.
var wordEndings = []string{
	"ами", "ями", "ого", "его", "ому", "ему", "ыми", "ими", "ых", "их", "ой", "ей", "ом", "ем", "ам", "ям", "ах", "ях", "ов", "ев",
	"ую", "юю", "ая", "яя", "ое", "ее", "ый", "ий", "а", "я", "у", "ю", "е", "ы", "и", "о", "ь", "й",
}

func wordStems(text string) []string {
	var stems []string
	for _, word := range strings.FieldsFunc(Normalize(text), func(char rune) bool { return !unicode.IsLetter(char) && !unicode.IsDigit(char) }) {
		stems = append(stems, wordStem(word))
	}
	return stems
}

// wordStem drops a case ending when at least three letters stay.
func wordStem(word string) string {
	for _, ending := range wordEndings {
		if strings.HasSuffix(word, ending) && len([]rune(word))-len([]rune(ending)) >= minStemRunes {
			return strings.TrimSuffix(word, ending)
		}
	}
	return word
}
