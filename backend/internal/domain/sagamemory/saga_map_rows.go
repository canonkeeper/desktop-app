package sagamemory

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"slices"
	"sort"
	"sync"
)

// storedWriters reuses a compressor between rows: a new one takes a megabyte, and a saga has tens of thousands of rows.
var storedWriters = sync.Pool{New: func() any {
	writer, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	if err != nil {
		panic(err)
	}
	return writer
}}

// EncodeStored writes a stored document: gzip of JSON. The fastest level: a Job writes all the rows of a saga.
func EncodeStored(value any) ([]byte, error) {
	var buffer bytes.Buffer
	writer := storedWriters.Get().(*gzip.Writer)
	defer storedWriters.Put(writer)
	writer.Reset(&buffer)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// DecodeStored reads a stored document.
func DecodeStored(body []byte, target any) error {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// The prepared saga map (specs/2026-11-04-saga-map.md, UC10): what a reader of the map and of the card of a node gets is
// built once, by the last step of the cards Job, and read by key. No request reads or parses the cards of a saga.

const (
	maxNodeEvents      = 40
	maxNodeChanges     = 30
	maxNodeLinks       = 10
	maxNodeLinkChanges = 5
	// maxLinkShared is how many shared events a link of the card prints: the reason of the link (R11.2).
	maxLinkShared = 3
	// maxNodeBeside is how many names the card prints as mentioned in the same chapters (R11.1).
	maxNodeBeside = 8
	// maxPartLinks is how many links of the strongest a part holds: more than a card prints, so what the author takes off
	// the map gives way to the next one (R11.3).
	maxPartLinks = 2 * maxNodeLinks
	// maxPartItems is how many events and changes of its own chapters a part of a node holds: a part is read and parsed whole.
	maxPartItems = 300
)

// MapChapterRef is a chapter as the map and the cards name it, in the shape of the API. Label is its place as the
// author reads it, «Книга 2, гл. 12 „Белый порт“»: the page shows it and never the key (specs/2026-11-10-ux1-names.md N4.1).
type MapChapterRef struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	ChapterID string `json:"chapterId"`
	BookID    string `json:"bookId"`
	BookOrder int    `json:"bookOrder"`
	BookTitle string `json:"bookTitle"`
	Title     string `json:"title"`
	Number    int    `json:"number,omitempty"`
}

// MapCoverage says how many chapters of a text version have cards, in the shape of the API.
type MapCoverage struct {
	Chapters  int `json:"chapters"`
	WithCards int `json:"withCards"`
}

// MapDocumentNode is a node of the prepared graph, in the shape of the API.
type MapDocumentNode struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Type     string   `json:"type"`
	Aliases  []string `json:"aliases"`
	Chapters []int    `json:"chapters"`
	// Material marks a record of the author the book does not know (R9.5).
	Material bool `json:"material,omitempty"`
}

// MapDocumentLink is a link of the prepared graph, in the shape of the API.
type MapDocumentLink struct {
	Source   int   `json:"source"`
	Target   int   `json:"target"`
	Events   []int `json:"events"`
	Mentions []int `json:"mentions"`
	// Changes are the chapters of the changes naming both.
	Changes []int `json:"changes,omitempty"`
	// Material marks a link from a record of the author: it has no chapters.
	Material bool `json:"material,omitempty"`
}

// MapDocument is the answer of getSagaMap, written once and served as it is.
type MapDocument struct {
	Revision   string            `json:"revision"`
	Coverage   MapCoverage       `json:"coverage"`
	Chapters   []MapChapterRef   `json:"chapters"`
	Hero       *string           `json:"hero"`
	NodesTotal int               `json:"nodesTotal"`
	Nodes      []MapDocumentNode `json:"nodes"`
	Links      []MapDocumentLink `json:"links"`
	// Building says the map of a saga with cards is being prepared: the page asks again.
	Building bool `json:"building,omitempty"`
}

// MapSearchEntry is an entry found by a search of the map, in the shape of the API.
type MapSearchEntry struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"`
	// Material marks a record of the author the book does not know.
	Material bool `json:"material,omitempty"`
}

// MapNow is what the map of a text version should be built from now: the revision of its cards and how many chapters have one.
type MapNow struct {
	Revision string
	Coverage MapCoverage
}

// StoredMention is the first mention of an entry.
type StoredMention struct {
	Chapter int `json:"c"`
	P       int `json:"p,omitempty"`
}

// StoredEvent is an event an entry takes part in.
type StoredEvent struct {
	Chapter int    `json:"c"`
	Event   string `json:"e"`
	P       int    `json:"p,omitempty"`
	Key     bool   `json:"k,omitempty"`
}

// StoredChange is a change that names an entry.
type StoredChange struct {
	Chapter int    `json:"c"`
	Subject string `json:"s"`
	Change  string `json:"t"`
	P       int    `json:"p,omitempty"`
}

// StoredLink is a neighbor of a node on the map. Changes are the places, in the changes of the node, of those relating
// the two, Shared the places, in the events of the node, of the events both take part in, ChangeChapters the chapters of the
// changes naming both, on the map or not.
type StoredLink struct {
	Node           int    `json:"n"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	Category       string `json:"cat"`
	Events         []int  `json:"ev,omitempty"`
	Mentions       []int  `json:"me,omitempty"`
	Changes        []int  `json:"ch,omitempty"`
	Shared         []int  `json:"sh,omitempty"`
	ChangeChapters []int  `json:"cc,omitempty"`
	// Material marks a link from a record of the author: it has no chapters and stands at any chapter.
	Material bool `json:"m,omitempty"`
}

// StoredNode is everything the card of an entry prints, with the chapters to cut it at any point of the scale. Every entry
// of the glossary has one, on the map or not. A part of a node (Parts) is a node too, with only what its chapters can show and
// the count of what it left out.
type StoredNode struct {
	Key      string         `json:"key"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Category string         `json:"cat,omitempty"`
	Aliases  []string       `json:"aliases,omitempty"`
	Note     string         `json:"note,omitempty"`
	Chapters []int          `json:"chapters,omitempty"`
	First    *StoredMention `json:"first,omitempty"`
	Events   []StoredEvent  `json:"events,omitempty"`
	Changes  []StoredChange `json:"changes,omitempty"`
	Links    []StoredLink   `json:"links,omitempty"`
	// LeftEvents and LeftChanges count the events and changes before the part that it does not hold.
	LeftEvents  int `json:"le,omitempty"`
	LeftChanges int `json:"lc,omitempty"`
	// Material is the author's record of this name, NamedIn the other records that name it (R9.5).
	Material *CardMaterial     `json:"material,omitempty"`
	NamedIn  []CardMaterialRef `json:"named,omitempty"`
	// SearchText is what a search of the map looks in; it has its own column, not a place in the stored node.
	SearchText string `json:"-"`
}

// StoredPart is a node for the chapters from First up to the next part.
type StoredPart struct {
	First int
	Node  StoredNode
}

// Parts divides a node along the scale: the card of the most mentioned hero holds tens of thousands of events and changes,
// and a request at a chapter reads only the part holding it. The first part starts at the first chapter; a node of few
// events and changes is one part.
func (node *StoredNode) Parts(chapters int) []StoredPart {
	if len(node.Events)+len(node.Changes) <= maxPartItems {
		return []StoredPart{{Node: *node}}
	}
	var parts []StoredPart
	first, items, events, changes := 0, 0, 0, 0
	for chapter := range chapters {
		eventsEnd := events + sort.Search(len(node.Events)-events, func(i int) bool { return node.Events[events+i].Chapter > chapter })
		changesEnd := changes + sort.Search(len(node.Changes)-changes, func(i int) bool { return node.Changes[changes+i].Chapter > chapter })
		own := eventsEnd - events + changesEnd - changes
		if items > 0 && items+own > maxPartItems {
			parts = append(parts, node.part(first, chapter-1))
			first, items = chapter, 0
		}
		items, events, changes = items+own, eventsEnd, changesEnd
	}
	return append(parts, node.part(first, chapters-1))
}

// part keeps what the card shows at any chapter of first..last (Card): the events and changes of these chapters, the ones before
// that can still be shown, and the neighbors that can still be among the strongest with the changes relating them. What it
// leaves out is counted, so the totals of the card stay.
func (node *StoredNode) part(first, last int) StoredPart {
	part := *node
	reached := node.linksWithin(first, last)
	start := sort.Search(len(node.Events), func(i int) bool { return node.Events[i].Chapter >= first })
	end := sort.Search(len(node.Events), func(i int) bool { return node.Events[i].Chapter > last })
	places := lastEvents(node.Events[:start])
	for place := start; place < end; place++ {
		places = append(places, place)
	}
	for _, link := range reached {
		before := sort.SearchInts(link.Shared, start)
		places = append(places, link.Shared[max(0, before-maxLinkShared):sort.SearchInts(link.Shared, end)]...)
	}
	slices.Sort(places)
	places = slices.Compact(places)
	part.Events = make([]StoredEvent, len(places))
	movedEvents := make(map[int]int, len(places))
	for at, place := range places {
		part.Events[at] = node.Events[place]
		movedEvents[place] = at
	}
	part.LeftEvents = start - sort.SearchInts(places, start)
	eventsStart, eventsEnd := start, end

	start = sort.Search(len(node.Changes), func(i int) bool { return node.Changes[i].Chapter >= first })
	end = sort.Search(len(node.Changes), func(i int) bool { return node.Changes[i].Chapter > last })
	places = make([]int, 0, end-max(0, start-maxNodeChanges))
	for place := max(0, start-maxNodeChanges); place < end; place++ {
		places = append(places, place)
	}
	for _, link := range reached {
		before := sort.SearchInts(link.Changes, start)
		places = append(places, link.Changes[max(0, before-maxNodeLinkChanges):sort.SearchInts(link.Changes, end)]...)
	}
	slices.Sort(places)
	places = slices.Compact(places)
	part.Changes = make([]StoredChange, len(places))
	moved := make(map[int]int, len(places))
	for at, place := range places {
		part.Changes[at] = node.Changes[place]
		moved[place] = at
	}
	part.LeftChanges = start - sort.SearchInts(places, start)
	part.Links = make([]StoredLink, len(reached))
	for i, link := range reached {
		part.Links[i] = link
		part.Links[i].Changes = nil
		before := sort.SearchInts(link.Changes, start)
		for _, place := range link.Changes[max(0, before-maxNodeLinkChanges):sort.SearchInts(link.Changes, end)] {
			part.Links[i].Changes = append(part.Links[i].Changes, moved[place])
		}
		part.Links[i].Shared = nil
		before = sort.SearchInts(link.Shared, eventsStart)
		for _, place := range link.Shared[max(0, before-maxLinkShared):sort.SearchInts(link.Shared, eventsEnd)] {
			part.Links[i].Shared = append(part.Links[i].Shared, movedEvents[place])
		}
	}
	return StoredPart{First: first, Node: part}
}

// lastEvents names the places of the events before a part the card can still show: the latest key events and the latest others, in order.
func lastEvents(events []StoredEvent) []int {
	kept := make([]int, 0, min(len(events), 2*maxNodeEvents))
	keys, others := 0, 0
	for i := len(events) - 1; i >= 0 && (keys < maxNodeEvents || others < maxNodeEvents); i-- {
		count := &others
		if events[i].Key {
			count = &keys
		}
		if *count < maxNodeEvents {
			kept = append(kept, i)
			*count++
		}
	}
	slices.Reverse(kept)
	return kept
}

// linksWithin lists the neighbors a card can print at some chapter of first..last: the strongest drawn and mentioned beside.
func (node *StoredNode) linksWithin(first, last int) []StoredLink {
	if len(node.Links) <= maxPartLinks+maxNodeBeside {
		return node.Links
	}
	within := make(map[int]bool, maxPartLinks+maxNodeBeside)
	for to := first; to <= last; to++ {
		honest, beside := node.rankedLinks(to, MapHidden{})
		for _, item := range honest[:min(len(honest), maxPartLinks)] {
			within[item.place] = true
		}
		for _, item := range beside[:min(len(beside), maxNodeBeside)] {
			within[item.place] = true
		}
	}
	reached := make([]StoredLink, 0, len(within))
	for place, link := range node.Links {
		if within[place] {
			reached = append(reached, link)
		}
	}
	return reached
}

// MapRows is what one build of a text version writes: the graph, the node of every entry and, by every name, its node.
type MapRows struct {
	Document MapDocument
	Nodes    []StoredNode
	Names    map[string]string
}

// BuildMapRows prepares the map of a text version from its cards and the author's materials. index stems the changes (CardSet.Links).
func BuildMapRows(version *Version, set *CardSet, index *Index, revision string, materials []MapMaterial) MapRows {
	materials = uniqueMaterials(materials)
	graph := BuildMap(set, materials)
	graph.AddMaterials(set.Glossary, materials)
	glossary := set.Glossary
	nodes := make(map[*GlossaryEntry]*StoredNode, len(glossary.Entries))
	for _, entry := range glossary.Entries {
		category, _ := MapCategoryOf(entry.Type)
		node := &StoredNode{Key: entry.key, Name: entry.Name, Type: entry.Type, Category: string(category), Aliases: entry.Aliases, Note: entry.Note, Chapters: entry.Chapters, SearchText: entry.searchText()}
		if len(entry.Chapters) > 0 {
			first := StoredMention{Chapter: entry.Chapters[0]}
			if card := set.Cards[first.Chapter]; card != nil {
				first.P = firstParagraph(card, keySet(entry))
			}
			node.First = &first
		}
		nodes[entry] = node
	}
	shared := addEvents(set, nodes, graph)
	relating := addChanges(set.Links(index), nodes)
	graph.AddChanges(changeChapters(relating, nodes))
	pruned := graph.PruneWithoutReason(len(set.Cards))
	own := addMaterialRows(graph, glossary, materials, nodes)
	for _, link := range graph.Links {
		source, target := graph.Nodes[link.Source], graph.Nodes[link.Target]
		addLink(nodes[source.Entry], link, target, link.Target, relating[source.Entry][target.Entry], shared[source.Entry][target.Entry])
		addLink(nodes[target.Entry], link, source, link.Source, relating[target.Entry][source.Entry], shared[target.Entry][source.Entry])
	}
	// A name off the map has no index on it; the cards rank it after the nodes that do.
	for _, gone := range pruned {
		source, target := gone.Source, gone.Target
		addLink(nodes[source.Entry], gone.Link, target, len(graph.Nodes), relating[source.Entry][target.Entry], shared[source.Entry][target.Entry])
		addLink(nodes[target.Entry], gone.Link, source, len(graph.Nodes), relating[target.Entry][source.Entry], shared[target.Entry][source.Entry])
	}
	rows := MapRows{Document: mapDocument(version, set, graph, revision), Nodes: make([]StoredNode, 0, len(nodes)), Names: map[string]string{}}
	for _, entry := range glossary.Entries {
		rows.Nodes = append(rows.Nodes, *nodes[entry])
		rows.Names[entry.key] = entry.key
		for _, key := range entry.Keys() {
			if found, ok := glossary.Lookup(key); ok {
				rows.Names[key] = found.key
			}
		}
	}
	for _, entry := range own {
		rows.Nodes = append(rows.Nodes, *nodes[entry])
		rows.Names[entry.key] = entry.key
	}
	return rows
}

func keySet(entry *GlossaryEntry) map[string]bool {
	keys := map[string]bool{}
	for _, key := range entry.Keys() {
		keys[key] = true
	}
	return keys
}

// addEvents gives each entry the events it takes part in under any of its names, in story order, and returns the places, in
// the events of an entry on the map, of those it shares with each other entry on the map (R11.2).
func addEvents(set *CardSet, nodes map[*GlossaryEntry]*StoredNode, graph *Map) map[*GlossaryEntry]map[*GlossaryEntry][]int {
	chapters := make([]int, 0, len(set.Cards))
	for chapter := range set.Cards {
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	shared := map[*GlossaryEntry]map[*GlossaryEntry][]int{}
	for _, chapter := range chapters {
		for _, event := range set.Cards[chapter].Events {
			seen := make(map[*GlossaryEntry]bool, len(event.Who))
			var onMap []*GlossaryEntry
			places := make(map[*GlossaryEntry]int, len(event.Who))
			for _, name := range event.Who {
				if entry, ok := set.Glossary.Lookup(name); ok && !seen[entry] {
					seen[entry] = true
					places[entry] = len(nodes[entry].Events)
					nodes[entry].Events = append(nodes[entry].Events, StoredEvent{Chapter: chapter, Event: event.Event, P: event.P, Key: event.Key})
					if _, ok := graph.Node(entry); ok {
						onMap = append(onMap, entry)
					}
				}
			}
			for _, entry := range onMap {
				for _, other := range onMap {
					if other == entry {
						continue
					}
					if shared[entry] == nil {
						shared[entry] = map[*GlossaryEntry][]int{}
					}
					shared[entry][other] = append(shared[entry][other], places[entry])
				}
			}
		}
	}
	return shared
}

// addChanges gives each entry the changes naming it, and returns the places of those relating it to each other entry: one
// of the two is the subject of the change and the other is named in it (ChangeLink.Relates).
func addChanges(links []ChangeLink, nodes map[*GlossaryEntry]*StoredNode) map[*GlossaryEntry]map[*GlossaryEntry][]int {
	relating := make(map[*GlossaryEntry]map[*GlossaryEntry][]int, len(nodes))
	for _, link := range links {
		for _, entry := range link.Entries {
			node := nodes[entry]
			place := len(node.Changes)
			node.Changes = append(node.Changes, StoredChange{Chapter: link.Chapter, Subject: link.Change.Subject, Change: link.Change.Change, P: link.Change.P})
			others := link.Subjects
			if contains(link.Subjects, entry) {
				others = link.Entries
			}
			if relating[entry] == nil {
				relating[entry] = map[*GlossaryEntry][]int{}
			}
			for _, other := range others {
				if other != entry {
					relating[entry][other] = append(relating[entry][other], place)
				}
			}
		}
	}
	return relating
}

// changeChapters names, for a pair of entries, the chapters of the changes relating them.
func changeChapters(relating map[*GlossaryEntry]map[*GlossaryEntry][]int, nodes map[*GlossaryEntry]*StoredNode) map[[2]*GlossaryEntry][]int {
	chapters := make(map[[2]*GlossaryEntry][]int, len(relating))
	for entry, others := range relating {
		for other, places := range others {
			for _, place := range places {
				chapters[[2]*GlossaryEntry{entry, other}] = append(chapters[[2]*GlossaryEntry{entry, other}], nodes[entry].Changes[place].Chapter)
			}
		}
	}
	return chapters
}

func addLink(node *StoredNode, link MapLink, other MapNode, otherIndex int, changes, shared []int) {
	node.Links = append(node.Links, StoredLink{
		Node: otherIndex, Key: other.Entry.key, Name: other.Entry.Name, Category: string(other.Category),
		Events: link.Events, Mentions: link.Mentions, Changes: changes, Shared: shared, ChangeChapters: link.Changes, Material: link.Material,
	})
}

func mapDocument(version *Version, set *CardSet, graph *Map, revision string) MapDocument {
	document := MapDocument{
		Revision: revision, Coverage: MapCoverage{Chapters: len(version.Chapters), WithCards: len(set.Cards)},
		Chapters: make([]MapChapterRef, len(version.Chapters)), NodesTotal: graph.Total,
		Nodes: make([]MapDocumentNode, len(graph.Nodes)), Links: []MapDocumentLink{},
	}
	for i := range version.Chapters {
		chapter := &version.Chapters[i]
		document.Chapters[i] = MapChapterRef{
			Key: chapter.Key, Label: chapter.Place(), ChapterID: chapter.ID, BookID: chapter.BookID, BookOrder: chapter.BookOrder,
			BookTitle: chapter.BookTitle, Title: chapter.Title, Number: chapter.Number,
		}
	}
	for i, node := range graph.Nodes {
		document.Nodes[i] = MapDocumentNode{
			Key: node.Entry.key, Name: node.Entry.Name, Category: string(node.Category), Type: node.Entry.Type,
			Aliases: append([]string{}, node.Entry.Aliases...), Chapters: append([]int{}, node.Entry.Chapters...), Material: node.Material,
		}
	}
	if graph.Hero >= 0 {
		document.Hero = &document.Nodes[graph.Hero].Key
	}
	// What the map does not draw at any chapter stays in the rows of the nodes: their cards name it apart (R11.1).
	for _, link := range graph.Links {
		if link.HonestAt(-1) {
			document.Links = append(document.Links, MapDocumentLink{
				Source: link.Source, Target: link.Target, Events: append([]int{}, link.Events...), Mentions: append([]int{}, link.Mentions...),
				Changes: link.Changes, Material: link.Material,
			})
		}
	}
	return document
}
