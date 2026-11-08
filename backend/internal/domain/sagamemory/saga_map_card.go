package sagamemory

import "sort"

// The card of a node in the shape of the API.

// CardMention is the first mention of an entry.
type CardMention struct {
	Chapter   MapChapterRef `json:"chapter"`
	Paragraph int           `json:"paragraph,omitempty"`
}

// CardEvent is an event of the card.
type CardEvent struct {
	Chapter   MapChapterRef `json:"chapter"`
	Event     string        `json:"event"`
	Paragraph int           `json:"paragraph,omitempty"`
	Key       bool          `json:"key"`
}

// CardChange is a change of the card.
type CardChange struct {
	Chapter   MapChapterRef `json:"chapter"`
	Subject   string        `json:"subject"`
	Change    string        `json:"change"`
	Paragraph int           `json:"paragraph,omitempty"`
}

// CardLink is a neighbor of the node up to the chapter, with its reason (R11.2): the latest events both take part in and
// the latest changes relating the two.
type CardLink struct {
	Key      string       `json:"key"`
	Name     string       `json:"name"`
	Category string       `json:"category"`
	Together int          `json:"together"`
	Nearby   int          `json:"nearby"`
	Changes  []CardChange `json:"changes"`
	Shared   []CardEvent  `json:"shared,omitempty"`
	// FromMaterials says the link comes from a record of the author (R9.5).
	FromMaterials bool `json:"fromMaterials,omitempty"`
}

// CardNeighbor is a name mentioned in the same chapters as the node: no link, a line of the card (R11.1).
type CardNeighbor struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// MapCardDocument is the answer of getSagaMapNode.
type MapCardDocument struct {
	Key          string       `json:"key"`
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	Category     *string      `json:"category"`
	Aliases      []string     `json:"aliases"`
	Note         *string      `json:"note"`
	First        *CardMention `json:"first"`
	Mentions     int          `json:"mentions"`
	Events       []CardEvent  `json:"events"`
	EventsTotal  int          `json:"eventsTotal"`
	Changes      []CardChange `json:"changes"`
	ChangesTotal int          `json:"changesTotal"`
	Links        []CardLink   `json:"links"`
	// Beside are the names mentioned in the same chapters; HiddenID the decision of the author that took the node off the map (R11.3).
	Beside   []CardNeighbor `json:"beside,omitempty"`
	HiddenID string         `json:"hiddenId,omitempty"`
	// Material is the author's record of this name; NamedIn the other records that name it (R9.5).
	Material *CardMaterial     `json:"material"`
	NamedIn  []CardMaterialRef `json:"namedIn"`
}

// Card cuts a stored node at a chapter index (negative for the whole saga): what its card shows at that point of the scale
// (UC2), without what the author took off the map (R11.3). Reading a node is the only reading of a request; the cut is cheap and
// needs no card of a chapter.
func (node *StoredNode) Card(chapters []MapChapterRef, to int, hidden MapHidden) MapCardDocument {
	card := MapCardDocument{
		Key: node.Key, Name: node.Name, Type: node.Type, Aliases: node.Aliases, Mentions: CountTo(node.Chapters, to),
		Events: []CardEvent{}, Changes: []CardChange{}, Links: []CardLink{}, Material: node.Material, NamedIn: node.NamedIn, HiddenID: hidden.Nodes[node.Key],
	}
	if card.NamedIn == nil {
		card.NamedIn = []CardMaterialRef{}
	}
	if card.Aliases == nil {
		card.Aliases = []string{}
	}
	if node.Category != "" {
		category := node.Category
		card.Category = &category
	}
	if node.Note != "" {
		note := node.Note
		card.Note = &note
	}
	if node.First != nil && (to < 0 || node.First.Chapter <= to) {
		card.First = &CardMention{Chapter: chapters[node.First.Chapter], Paragraph: node.First.P}
	}
	events := node.Events
	if to >= 0 {
		events = events[:sort.Search(len(events), func(i int) bool { return events[i].Chapter > to })]
	}
	card.EventsTotal = node.LeftEvents + len(events)
	for _, event := range shownEvents(events, maxNodeEvents) {
		card.Events = append(card.Events, CardEvent{Chapter: chapters[event.Chapter], Event: event.Event, Paragraph: event.P, Key: event.Key})
	}
	changes := node.Changes
	if to >= 0 {
		changes = changes[:sort.Search(len(changes), func(i int) bool { return changes[i].Chapter > to })]
	}
	card.ChangesTotal = node.LeftChanges + len(changes)
	for _, change := range changes[max(0, len(changes)-maxNodeChanges):] {
		card.Changes = append(card.Changes, cardChange(chapters, change))
	}
	card.Links, card.Beside = node.linksUpto(chapters, to, len(events), len(changes), hidden)
	return card
}

func cardChange(chapters []MapChapterRef, change StoredChange) CardChange {
	return CardChange{Chapter: chapters[change.Chapter], Subject: change.Subject, Change: change.Change, Paragraph: change.P}
}

// rankedLink is a neighbor counted up to a chapter; place is where the node keeps it.
type rankedLink struct {
	place    int
	together int
	nearby   int
}

// rankedLinks lists the neighbors up to a chapter, the strongest of the book first: honest are the links the map draws then
// (honestLink), beside the names that only stand in the same chapters. What the author took off the map is neither.
func (node *StoredNode) rankedLinks(to int, hidden MapHidden) (honest, beside []rankedLink) {
	for place := range node.Links {
		link := &node.Links[place]
		if hidden.HidesLink(node.Key, link.Key) {
			continue
		}
		together, nearby := CountTo(link.Events, to), CountTo(link.Mentions, to)
		switch {
		case honestLink(link.Events, link.ChangeChapters, link.Material, to):
			honest = append(honest, rankedLink{place, together, nearby})
		case together+nearby > 0:
			beside = append(beside, rankedLink{place, together, nearby})
		}
	}
	strongest := func(links []rankedLink) {
		sort.SliceStable(links, func(i, j int) bool {
			if left, right := node.Links[links[i].place].Material, node.Links[links[j].place].Material; left != right {
				return right
			}
			if links[i].together != links[j].together {
				return links[i].together > links[j].together
			}
			if links[i].nearby != links[j].nearby {
				return links[i].nearby > links[j].nearby
			}
			return node.Links[links[i].place].Node < node.Links[links[j].place].Node
		})
	}
	strongest(honest)
	strongest(beside)
	return honest, beside
}

// linksUpto lists the strongest neighbors up to a chapter, ten at most, with the reason of each, and the names beside. events and
// changes are how many of the node's stand up to that chapter.
func (node *StoredNode) linksUpto(chapters []MapChapterRef, to, events, changes int, hidden MapHidden) ([]CardLink, []CardNeighbor) {
	honest, beside := node.rankedLinks(to, hidden)
	cards := make([]CardLink, 0, min(len(honest), maxNodeLinks))
	for _, item := range honest[:min(len(honest), maxNodeLinks)] {
		link := &node.Links[item.place]
		places := link.Changes[:sort.SearchInts(link.Changes, changes)]
		related := []CardChange{}
		for _, place := range places[max(0, len(places)-maxNodeLinkChanges):] {
			related = append(related, cardChange(chapters, node.Changes[place]))
		}
		var shared []CardEvent
		places = link.Shared[:sort.SearchInts(link.Shared, events)]
		for _, place := range places[max(0, len(places)-maxLinkShared):] {
			event := node.Events[place]
			shared = append(shared, CardEvent{Chapter: chapters[event.Chapter], Event: event.Event, Paragraph: event.P, Key: event.Key})
		}
		cards = append(cards, CardLink{
			Key: link.Key, Name: link.Name, Category: link.Category, Together: item.together, Nearby: item.nearby, Changes: related, Shared: shared, FromMaterials: link.Material,
		})
	}
	var neighbors []CardNeighbor
	for _, item := range beside[:min(len(beside), maxNodeBeside)] {
		neighbors = append(neighbors, CardNeighbor{Key: node.Links[item.place].Key, Name: node.Links[item.place].Name})
	}
	return cards, neighbors
}

// shownEvents keeps at most limit events in story order: the key events first, the latest of them when they are too many,
// then the latest others.
func shownEvents(events []StoredEvent, limit int) []StoredEvent {
	if len(events) <= limit {
		return events
	}
	shown := make([]bool, len(events))
	left := limit
	for i := len(events) - 1; i >= 0 && left > 0; i-- {
		if events[i].Key {
			shown[i], left = true, left-1
		}
	}
	for i := len(events) - 1; i >= 0 && left > 0; i-- {
		if !shown[i] {
			shown[i], left = true, left-1
		}
	}
	kept := make([]StoredEvent, 0, limit)
	for i, event := range events {
		if shown[i] {
			kept = append(kept, event)
		}
	}
	return kept
}
