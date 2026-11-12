package sagamemory

import (
	"slices"
	"sort"
	"strings"
)

// The saga map (specs/2026-11-04-saga-map.md, UC1): the characters, places, factions and items of the
// glossary as nodes, linked where they take part in one event of a chapter and, weaker, where one card
// names both. Every link carries the chapters that show it, so a reader of the map counts its weight up
// to any chapter without asking again.

// MapCategory is the kind of a node of the saga map.
type MapCategory string

const (
	MapCharacter MapCategory = "character"
	MapPlace     MapCategory = "place"
	MapFaction   MapCategory = "faction"
	MapItem      MapCategory = "item"
)

// mapCategoryTypes are the card types of each category (R1.2, Data mapCategories), normalized.
var mapCategoryTypes = func() map[string]MapCategory {
	groups := map[MapCategory]string{
		MapCharacter: characterType,
		MapPlace: "место локация город страна королевство империя регион континент мир планета деревня село район квартал " +
			"улица здание сооружение замок крепость храм башня этаж подземелье остров лес гора река море озеро пустыня",
		MapFaction: "фракция организация клан гильдия орден братство семья род народ группа отряд команда армия банда союз альянс",
		MapItem:    "предмет артефакт оружие снаряжение броня доспех зелье амулет кольцо транспорт",
	}
	types := map[string]MapCategory{}
	for category, words := range groups {
		for _, word := range strings.Fields(words) {
			types[word] = category
		}
	}
	return types
}()

// MapCategoryOf returns the category of a card type; a type of several words goes by its last word
// («древний артефакт»). Other types (skills, notions, resources) are not on the map.
func MapCategoryOf(kind string) (MapCategory, bool) {
	words := strings.Fields(Normalize(kind))
	if len(words) == 0 {
		return "", false
	}
	if category, ok := mapCategoryTypes[strings.Join(words, " ")]; ok {
		return category, true
	}
	category, ok := mapCategoryTypes[words[len(words)-1]]
	return category, ok
}

const (
	// maxMapNodes bounds the nodes of a map, most chapters first.
	maxMapNodes = 600
	// mapTwoChapters is how many cards a saga needs before a node is named in two chapters and a node of two needs a reason
	// (specs/2026-11-10-map-nodes.md MN1.1, MN1.2): in a story of 17 chapters a name of one chapter, and one of two without a
	// reason, is a word the book passes by — the porters, a banker, a country on the way.
	mapTwoChapters = 8
	// weakLinkChapters is how many chapters two nodes stand beside each other before that is a link.
	weakLinkChapters = 3
	// weakLinksPerNode is how many of its strongest weak links a node keeps.
	weakLinksPerNode = 8
	// mapFewCards is how many cards a saga needs before a node of one chapter needs a reason: with one or two everything is of one chapter (R11.4).
	mapFewCards = 3
	// minSharedEvents is how many events two nodes share before the map draws a link between them: one event often names
	// everyone its chapter mentions, and such links were found false in a real saga (R11.1).
	minSharedEvents = 2
)

// MapNode is one glossary entry on the map; its chapters are Entry.Chapters, ascending.
type MapNode struct {
	Entry    *GlossaryEntry
	Category MapCategory
	// Material marks a record of the author the book does not know (R9.5): it has no chapters.
	Material bool
	// Authored marks a name of the book the author has a record of (MN1.3): it is no word the book passes by, at any chapters.
	Authored bool
}

// MapLink links two nodes (Source < Target, node indexes): Events are the chapters of the events both take part in, one
// per event, Mentions the chapters whose card names both without a shared event, Changes the chapters of the changes
// naming both (one is the subject, the other is named), ascending.
type MapLink struct {
	Source   int
	Target   int
	Events   []int
	Mentions []int
	Changes  []int
	// Material marks a link from a record of the author: it has no chapters and stands at any chapter.
	Material bool
}

// Map is the graph of a saga: Hero is the node of the main character, -1 without one; Total is how many nodes the map draws
// without the bound (MN1.6): above it, the nodes past the bound are counted without a reason looked for.
type Map struct {
	Nodes []MapNode
	Links []MapLink
	Hero  int
	Total int

	nodeOf map[*GlossaryEntry]int
}

// BuildMap builds the graph of the saga from its cards (UC1 «построить граф»): the entries of the map
// categories mentioned in two chapters (one in a saga of fewer than 8 cards) and those the author's
// materials have a record of, at most 600 by chapters; the pairs of every event's participants and of
// every card's nodes; a weak link stays when it is among the 8 strongest of one of its ends.
func BuildMap(set *CardSet, materials []MapMaterial) *Map {
	minChapters := 1
	if len(set.Cards) >= mapTwoChapters {
		minChapters = 2
	}
	authored := make(map[*GlossaryEntry]bool, len(materials))
	for _, material := range materials {
		if entry, ok := set.Glossary.Lookup(material.Name); ok {
			authored[entry] = true
		}
	}
	var nodes []MapNode
	for _, entry := range set.Glossary.Entries {
		if category, ok := MapCategoryOf(entry.Type); ok && (len(entry.Chapters) >= minChapters || authored[entry]) {
			nodes = append(nodes, MapNode{Entry: entry, Category: category, Authored: authored[entry]})
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		left, right := nodes[i].Entry, nodes[j].Entry
		if len(left.Chapters) != len(right.Chapters) {
			return len(left.Chapters) > len(right.Chapters)
		}
		return left.Name < right.Name
	})
	m := &Map{Hero: -1, Total: len(nodes), nodeOf: map[*GlossaryEntry]int{}}
	m.Nodes = nodes[:min(len(nodes), maxMapNodes)]
	nodesOfChapter := map[int][]int{}
	for i, node := range m.Nodes {
		m.nodeOf[node.Entry] = i
		if m.Hero < 0 && node.Category == MapCharacter {
			m.Hero = i
		}
		for _, chapter := range node.Entry.Chapters {
			nodesOfChapter[chapter] = append(nodesOfChapter[chapter], i)
		}
	}

	type pair struct{ a, b int }
	links := map[pair]*MapLink{}
	link := func(p pair) *MapLink {
		found := links[p]
		if found == nil {
			found = &MapLink{Source: p.a, Target: p.b}
			links[p] = found
		}
		return found
	}
	chapters := make([]int, 0, len(set.Cards))
	for chapter := range set.Cards {
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	for _, chapter := range chapters {
		together := map[pair]int{}
		for _, event := range set.Cards[chapter].Events {
			participants := m.participants(set.Glossary, event)
			for i := range participants {
				for j := i + 1; j < len(participants); j++ {
					together[pair{participants[i], participants[j]}]++
				}
			}
		}
		for p, events := range together {
			found := link(p)
			for range events {
				found.Events = append(found.Events, chapter)
			}
		}
		named := nodesOfChapter[chapter]
		for i := range named {
			for j := i + 1; j < len(named); j++ {
				if p := (pair{named[i], named[j]}); together[p] == 0 {
					found := link(p)
					found.Mentions = append(found.Mentions, chapter)
				}
			}
		}
	}

	kept := map[*MapLink]bool{}
	weak := map[int][]*MapLink{}
	for _, found := range links {
		switch {
		case len(found.Events) > 0:
			kept[found] = true
		case len(found.Mentions) >= weakLinkChapters:
			weak[found.Source] = append(weak[found.Source], found)
			weak[found.Target] = append(weak[found.Target], found)
		}
	}
	for node, candidates := range weak {
		other := func(found *MapLink) int { return found.Source + found.Target - node }
		sort.Slice(candidates, func(i, j int) bool {
			if len(candidates[i].Mentions) != len(candidates[j].Mentions) {
				return len(candidates[i].Mentions) > len(candidates[j].Mentions)
			}
			return other(candidates[i]) < other(candidates[j])
		})
		for _, found := range candidates[:min(len(candidates), weakLinksPerNode)] {
			kept[found] = true
		}
	}
	m.Links = make([]MapLink, 0, len(kept))
	for found := range kept {
		m.Links = append(m.Links, *found)
	}
	sort.Slice(m.Links, func(i, j int) bool {
		if m.Links[i].Source != m.Links[j].Source {
			return m.Links[i].Source < m.Links[j].Source
		}
		return m.Links[i].Target < m.Links[j].Target
	})
	return m
}

// honestLink says whether the map draws a link at a chapter (negative for the whole saga; R11.1): two shared events, a change
// naming both, or a record of the author. One shared event or chapters beside each other are no link: the card names them apart.
func honestLink(events, changes []int, material bool, to int) bool {
	return material || CountTo(events, to) >= minSharedEvents || CountTo(changes, to) > 0
}

// HonestAt says whether the map draws the link at a chapter.
func (link MapLink) HonestAt(to int) bool {
	return honestLink(link.Events, link.Changes, link.Material, to)
}

// AddChanges links the nodes a change names together (R11.1): changes holds, for a pair of entries, the chapters of its
// changes. A pair the build has no link for gets one: a change is a reason by itself.
func (m *Map) AddChanges(changes map[[2]*GlossaryEntry][]int) {
	type pair struct{ a, b int }
	at := make(map[pair]int, len(m.Links))
	for i, link := range m.Links {
		at[pair{link.Source, link.Target}] = i
	}
	for entries, chapters := range changes {
		a, okA := m.nodeOf[entries[0]]
		b, okB := m.nodeOf[entries[1]]
		if !okA || !okB || a == b {
			continue
		}
		p := pair{min(a, b), max(a, b)}
		i, ok := at[p]
		if !ok {
			i = len(m.Links)
			at[p] = i
			m.Links = append(m.Links, MapLink{Source: p.a, Target: p.b})
		}
		merged := append(append([]int{}, m.Links[i].Changes...), chapters...)
		sort.Ints(merged)
		m.Links[i].Changes = slices.Compact(merged)
	}
	sort.Slice(m.Links, func(i, j int) bool {
		if m.Links[i].Source != m.Links[j].Source {
			return m.Links[i].Source < m.Links[j].Source
		}
		return m.Links[i].Target < m.Links[j].Target
	})
}

// PrunedLink is a link of a node PruneWithoutReason took off the map: the cards at its ends still name the two apart.
type PrunedLink struct {
	Link           MapLink
	Source, Target MapNode
}

// PruneWithoutReason takes off the map the nodes of few chapters without an honest link to another node (R11.4, MN1.2): from
// 3 cards those of one chapter, from 8 those of two — the letter, the acquaintance, the country a hero passes through. Search
// and the cards still have them. A node of three chapters and more stays without a reason, and so does what the author wrote
// about; a saga of one or two cards keeps all: that is all it has. One pass: two nodes linked to each other stay both.
func (m *Map) PruneWithoutReason(cards int) []PrunedLink {
	var few int
	switch {
	case cards >= mapTwoChapters:
		few = 2
	case cards >= mapFewCards:
		few = 1
	default:
		return nil
	}
	honest := make([]bool, len(m.Nodes))
	for _, link := range m.Links {
		if link.HonestAt(-1) {
			honest[link.Source], honest[link.Target] = true, true
		}
	}
	index := make([]int, len(m.Nodes))
	original := slices.Clone(m.Nodes)
	kept := make([]MapNode, 0, len(m.Nodes))
	for i, node := range original {
		if len(node.Entry.Chapters) <= few && !node.Material && !node.Authored && !honest[i] {
			index[i] = -1
			delete(m.nodeOf, node.Entry)
			continue
		}
		index[i] = len(kept)
		m.nodeOf[node.Entry] = len(kept)
		kept = append(kept, node)
	}
	if m.Hero >= 0 {
		m.Hero = index[m.Hero]
	}
	links := make([]MapLink, 0, len(m.Links))
	var pruned []PrunedLink
	for _, link := range m.Links {
		if index[link.Source] < 0 || index[link.Target] < 0 {
			pruned = append(pruned, PrunedLink{Link: link, Source: original[link.Source], Target: original[link.Target]})
			continue
		}
		link.Source, link.Target = index[link.Source], index[link.Target]
		links = append(links, link)
	}
	m.Total -= len(original) - len(kept)
	m.Nodes, m.Links = kept, links
	return pruned
}

// participants are the nodes an event's names resolve to, ascending; two names of one person are one.
func (m *Map) participants(glossary *Glossary, event Event) []int {
	var nodes []int
	seen := map[int]bool{}
	for _, name := range event.Who {
		entry, ok := glossary.Lookup(name)
		if !ok {
			continue
		}
		if node, ok := m.nodeOf[entry]; ok && !seen[node] {
			seen[node] = true
			nodes = append(nodes, node)
		}
	}
	sort.Ints(nodes)
	return nodes
}

// Node returns the node of an entry; false when the entry is not on the map.
func (m *Map) Node(entry *GlossaryEntry) (int, bool) {
	node, ok := m.nodeOf[entry]
	return node, ok
}

// CountTo counts the chapters of an ascending list up to a chapter index (inclusive; negative for all).
func CountTo(chapters []int, to int) int {
	if to < 0 {
		return len(chapters)
	}
	return sort.SearchInts(chapters, to+1)
}
