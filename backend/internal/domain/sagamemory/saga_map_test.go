package sagamemory

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMapCategoryOfGroupsCardTypes(t *testing.T) {
	cases := map[string]MapCategory{
		"персонаж": MapCharacter, "Город": MapPlace, "этаж": MapPlace, "клан": MapFaction, "Организация": MapFaction,
		"оружие": MapItem, "древний артефакт": MapItem, "зёлье": MapItem,
	}
	for kind, want := range cases {
		got, ok := MapCategoryOf(kind)
		if got != want || ok != (want != "") {
			t.Errorf("MapCategoryOf(%q) = %q, %v; want %q", kind, got, ok, want)
		}
	}
	for _, kind := range []string{"навык", "понятие", "ресурс", ""} {
		if _, ok := MapCategoryOf(kind); ok {
			t.Errorf("%q must not be a map node", kind)
		}
	}
}

// mapSaga is a saga of 20 cards: Мирон (also Ворон) in every chapter, Молли with him in events of
// chapters 3 and 7, Ринна beside him in chapters 1, 2 and 4 without an event, Кот beside him in two
// chapters only, a skill and a city.
func mapSaga() *CardSet {
	cards := map[int]*Card{}
	for chapter := range 20 {
		card := &Card{Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон"}}}, Entities: []Mention{{Name: "Взлом", Type: "навык"}}}
		cards[chapter] = card
	}
	for _, chapter := range []int{3, 7} {
		cards[chapter].Characters = append(cards[chapter].Characters, Mention{Name: "Молли"})
		cards[chapter].Events = []Event{{Event: "Ворон и Молли бегут", Who: []string{"Ворон", "Молли", "Мирон"}}}
	}
	cards[11].Characters = append(cards[11].Characters, Mention{Name: "Молли"})
	for _, chapter := range []int{1, 2, 4} {
		cards[chapter].Characters = append(cards[chapter].Characters, Mention{Name: "Ринна"})
		cards[chapter].Entities = append(cards[chapter].Entities, Mention{Name: "Карагора", Type: "город"})
	}
	for _, chapter := range []int{5, 6} {
		cards[chapter].Characters = append(cards[chapter].Characters, Mention{Name: "Кот"})
	}
	return NewCardSet(cards, nil)
}

func mapNames(m *Map) []string {
	names := make([]string, len(m.Nodes))
	for i, node := range m.Nodes {
		names[i] = node.Entry.Name
	}
	return names
}

func mapLink(t *testing.T, m *Map, a, b string) *MapLink {
	t.Helper()
	for i := range m.Links {
		link := &m.Links[i]
		left, right := m.Nodes[link.Source].Entry.Name, m.Nodes[link.Target].Entry.Name
		if left == a && right == b || left == b && right == a {
			return link
		}
	}
	return nil
}

func TestBuildMapKeepsNodesOfMapCategoriesMentionedTwice(t *testing.T) {
	m := BuildMap(mapSaga())
	if got, want := mapNames(m), []string{"Мирон", "Карагора", "Молли", "Ринна", "Кот"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	if m.Hero != 0 || m.Nodes[0].Category != MapCharacter || m.Nodes[1].Category != MapPlace {
		t.Fatalf("hero = %d, categories %q %q", m.Hero, m.Nodes[0].Category, m.Nodes[1].Category)
	}
	if !reflect.DeepEqual(m.Nodes[2].Entry.Chapters, []int{3, 7, 11}) {
		t.Fatalf("Молли chapters = %v", m.Nodes[2].Entry.Chapters)
	}

	small := NewCardSet(map[int]*Card{0: {Characters: []Mention{{Name: "Мила"}}}}, nil)
	if got := mapNames(BuildMap(small)); !reflect.DeepEqual(got, []string{"Мила"}) {
		t.Fatalf("a small saga keeps a node of one chapter: %v", got)
	}
}

func TestBuildMapCapsNodesByChapters(t *testing.T) {
	cards := map[int]*Card{}
	for chapter := range 30 {
		card := &Card{}
		for i := range maxMapNodes + 10 {
			if i%30 <= chapter%30 || chapter < 2 {
				card.Characters = append(card.Characters, Mention{Name: fmt.Sprintf("Герой %03d", i)})
			}
		}
		cards[chapter] = card
	}
	m := BuildMap(NewCardSet(cards, nil))
	if len(m.Nodes) != maxMapNodes || m.Candidates != maxMapNodes+10 {
		t.Fatalf("nodes %d of %d", len(m.Nodes), m.Candidates)
	}
	for i := 1; i < len(m.Nodes); i++ {
		if len(m.Nodes[i].Entry.Chapters) > len(m.Nodes[i-1].Entry.Chapters) {
			t.Fatalf("node %d has more chapters than node %d", i, i-1)
		}
	}
}

func TestBuildMapLinksEventsAndChaptersTogether(t *testing.T) {
	m := BuildMap(mapSaga())
	molly := mapLink(t, m, "Мирон", "Молли")
	if molly == nil || !reflect.DeepEqual(molly.Events, []int{3, 7}) || !reflect.DeepEqual(molly.Mentions, []int{11}) {
		t.Fatalf("Мирон–Молли = %+v: «Ворон» is Мирон, one participant", molly)
	}
	rinna := mapLink(t, m, "Мирон", "Ринна")
	if rinna == nil || len(rinna.Events) != 0 || !reflect.DeepEqual(rinna.Mentions, []int{1, 2, 4}) {
		t.Fatalf("Мирон–Ринна = %+v", rinna)
	}
	if link := mapLink(t, m, "Мирон", "Кот"); link != nil {
		t.Fatalf("two chapters beside each other are not a link: %+v", link)
	}
	if link := mapLink(t, m, "Ринна", "Карагора"); link == nil || !reflect.DeepEqual(link.Mentions, []int{1, 2, 4}) {
		t.Fatalf("Ринна–Карагора = %+v", link)
	}
	if !molly.HonestAt(-1) || molly.HonestAt(5) || rinna.HonestAt(-1) {
		t.Fatalf("two shared events are a link, one event or chapters beside each other are not: %+v %+v", molly, rinna)
	}
	for _, link := range m.Links {
		if link.Source >= link.Target {
			t.Fatalf("link %+v is not ordered", link)
		}
	}
}

func TestBuildMapKeepsTheStrongestWeakLinksOfANode(t *testing.T) {
	cards := map[int]*Card{}
	for chapter := range 40 {
		card := &Card{Characters: []Mention{{Name: "Мирон"}}}
		// Сосед N is beside Мирон in N+3 chapters, two neighbors beside each other in as many as the lesser.
		for neighbor := range weakLinksPerNode + 2 {
			if chapter < neighbor+3 {
				card.Characters = append(card.Characters, Mention{Name: fmt.Sprintf("Сосед %d", neighbor)})
			}
		}
		cards[chapter] = card
	}
	m := BuildMap(NewCardSet(cards, nil))
	weak := 0
	for _, link := range m.Links {
		if m.Nodes[link.Source].Entry.Name == "Мирон" || m.Nodes[link.Target].Entry.Name == "Мирон" {
			weak++
		}
	}
	// Мирон keeps his 8 strongest; the two weakest neighbors keep their link to him as one of their own 8.
	if weak != weakLinksPerNode+2 {
		t.Fatalf("links of Мирон = %d", weak)
	}
	// Сосед 0 and Сосед 1 keep Мирон and the busier neighbors: their own link is among neither's 8.
	if link := mapLink(t, m, "Сосед 0", "Сосед 1"); link != nil {
		t.Fatalf("a weak link among neither end's strongest is dropped: %+v", link)
	}
	if link := mapLink(t, m, "Сосед 9", "Сосед 8"); link == nil {
		t.Fatal("Сосед 9 keeps its link to Сосед 8")
	}
}

// mapRows prepares the rows of mapSaga: twenty chapters of the same cards, without text to stem the changes with.
func mapRows(t *testing.T) MapRows {
	t.Helper()
	return mapRowsWith(t, nil)
}

func mapRowsWith(t *testing.T, materials []MapMaterial) MapRows {
	t.Helper()
	chapters := make([]Chapter, 20)
	for i := range chapters {
		chapters[i] = Chapter{ID: fmt.Sprintf("c%d", i), BookID: "b", BookOrder: 1, BookTitle: "Том", Order: i + 1, Title: fmt.Sprintf("Глава %d", i+1), Paragraphs: []Paragraph{{Text: "Текст."}}}
	}
	version := NewVersion("project", "draft", "fp", chapters)
	return BuildMapRows(version, mapSaga(), wholeIndex(t, version, map[string]string{"текст": "текст"}), "revision", materials)
}

func storedNode(t *testing.T, rows MapRows, name string) *StoredNode {
	t.Helper()
	for i := range rows.Nodes {
		if rows.Nodes[i].Name == name {
			return &rows.Nodes[i]
		}
	}
	t.Fatalf("no node %q", name)
	return nil
}

// specs/2026-11-04-saga-map.md UC2, R10.2: a stored node cut at a chapter is what the card shows at that point of the scale.
func TestStoredNodeCutsTheCardAtAChapter(t *testing.T) {
	rows := mapRows(t)
	chapters := rows.Document.Chapters
	hero := storedNode(t, rows, "Мирон")
	names := func(links []CardLink) []string {
		got := []string{}
		for _, link := range links {
			got = append(got, fmt.Sprintf("%s %d/%d", link.Name, link.Together, link.Nearby))
		}
		return got
	}
	if got, want := names(hero.Card(chapters, 7, MapHidden{}).Links), []string{"Молли 2/0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("neighbors to chapter 7 = %v, want %v", got, want)
	}
	if links := hero.Card(chapters, 5, MapHidden{}).Links; len(links) != 0 {
		t.Fatalf("one shared event is no link yet: %+v", links)
	}
	if all := hero.Card(chapters, -1, MapHidden{}).Links; len(all) != 1 || all[0].Together != 2 || all[0].Nearby != 1 {
		t.Fatalf("neighbors of the whole saga = %+v", all)
	}
	early := hero.Card(chapters, 5, MapHidden{})
	if early.EventsTotal != 1 || early.Events[0].Chapter.Key != chapters[3].Key || early.Mentions != 6 || early.First == nil {
		t.Fatalf("Мирон to chapter 5 = %+v", early)
	}
	molly := storedNode(t, rows, "Молли")
	if whole := molly.Card(chapters, -1, MapHidden{}); whole.EventsTotal != 2 || whole.Mentions != 3 {
		t.Fatalf("Молли = %+v", whole)
	}
	if before := molly.Card(chapters, 1, MapHidden{}); before.First != nil || before.Mentions != 0 || before.EventsTotal != 0 || before.Events == nil || before.Links == nil || before.Changes == nil {
		t.Fatalf("Молли before her first chapter = %+v", before)
	}
	if rows.Names["ворон"] != hero.Key || rows.Names["мирон"] != hero.Key {
		t.Fatalf("names = %v", rows.Names)
	}
	if CountTo([]int{1, 3, 7, 11}, 7) != 3 || CountTo([]int{1, 3}, -1) != 2 || CountTo([]int{4}, 2) != 0 {
		t.Fatal("CountTo counts the chapters up to a chapter")
	}
}

// The map document and the card hold what the node keeps, and a stored document reads back as it was written.
func TestStoredDocumentsReadBack(t *testing.T) {
	rows := mapRows(t)
	if rows.Document.Revision != "revision" || rows.Document.Coverage != (MapCoverage{Chapters: 20, WithCards: 20}) || rows.Document.Hero == nil || *rows.Document.Hero != "мирон" {
		t.Fatalf("document = %+v", rows.Document)
	}
	body, err := EncodeStored(rows.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	var back StoredNode
	if err := DecodeStored(body, &back); err != nil || !reflect.DeepEqual(back.Card(rows.Document.Chapters, -1, MapHidden{}), rows.Nodes[0].Card(rows.Document.Chapters, -1, MapHidden{})) {
		t.Fatalf("read back = %+v, %v", back, err)
	}
}

// R10.2: at every chapter the part holding it gives the very card of the whole node.
func TestPartsOfANodeGiveTheCardOfTheWholeNode(t *testing.T) {
	chapters := make([]MapChapterRef, 90)
	for i := range chapters {
		chapters[i] = MapChapterRef{Key: fmt.Sprint(i + 1), Title: fmt.Sprintf("Глава %d", i+1)}
	}
	hero := StoredNode{Key: "мирон", Name: "Мирон", Type: "персонаж", Category: "character", Chapters: []int{}}
	for chapter := range chapters {
		hero.Chapters = append(hero.Chapters, chapter)
		// Key events are rare and stop in the middle, so a late part still shows those of the early ones.
		for i := range 11 {
			hero.Events = append(hero.Events, StoredEvent{Chapter: chapter, Event: fmt.Sprintf("событие %d/%d", chapter, i), Key: i == 0 && chapter < 40})
		}
		for i := range 3 {
			hero.Changes = append(hero.Changes, StoredChange{Chapter: chapter, Subject: "Мирон", Change: fmt.Sprintf("перемена %d/%d", chapter, i)})
		}
	}
	// Forty neighbors of rising strength: the strongest ten change along the scale, and the changes relating them are sparse.
	for other := range 40 {
		link := StoredLink{Node: other + 1, Key: fmt.Sprintf("сосед %d", other), Name: fmt.Sprintf("Сосед %d", other), Category: "character"}
		for chapter := other * 2; chapter < len(chapters); chapter += 2 {
			link.Events = append(link.Events, chapter)
			link.Mentions = append(link.Mentions, chapter)
		}
		for place := other; place < len(hero.Changes); place += 17 + other {
			link.Changes = append(link.Changes, place)
			link.ChangeChapters = append(link.ChangeChapters, hero.Changes[place].Chapter)
		}
		for place := other; place < len(hero.Events); place += 23 + other {
			link.Shared = append(link.Shared, place)
		}
		hero.Links = append(hero.Links, link)
	}
	parts := hero.Parts(len(chapters))
	if len(parts) < 3 || parts[0].First != 0 {
		t.Fatalf("parts = %d, first at %d", len(parts), parts[0].First)
	}
	for at, part := range parts {
		if own := part.Node.LeftEvents + len(part.Node.Events) + part.Node.LeftChanges + len(part.Node.Changes); own == 0 {
			t.Fatalf("part %d is empty", at)
		}
		if len(part.Node.Events)+len(part.Node.Changes) > maxPartItems+2*maxNodeEvents+maxNodeChanges+len(part.Node.Links)*(maxNodeLinkChanges+maxLinkShared) {
			t.Fatalf("part %d holds %d events and %d changes", at, len(part.Node.Events), len(part.Node.Changes))
		}
		// Each part reads back from its stored form.
		body, err := EncodeStored(part.Node)
		if err != nil {
			t.Fatal(err)
		}
		var read StoredNode
		if err := DecodeStored(body, &read); err != nil {
			t.Fatal(err)
		}
		end := len(chapters)
		if at+1 < len(parts) {
			end = parts[at+1].First
		}
		for to := part.First; to < end; to++ {
			if got, want := read.Card(chapters, to, MapHidden{}), hero.Card(chapters, to, MapHidden{}); !reflect.DeepEqual(got, want) {
				t.Fatalf("to %d, part %d (from %d):\n got %+v\nwant %+v", to, at, part.First, got, want)
			}
			// What the author took off the map gives way to the next link in a part too (R11.3).
			hidden := NewMapHidden([]MapHiddenItem{{ID: "1", NodeKey: "сосед 0"}, {ID: "2", NodeKey: "сосед 1", OtherKey: "мирон"}})
			if got, want := read.Card(chapters, to, hidden), hero.Card(chapters, to, hidden); !reflect.DeepEqual(got, want) {
				t.Fatalf("hidden, to %d, part %d (from %d):\n got %+v\nwant %+v", to, at, part.First, got, want)
			}
		}
	}
	few := StoredNode{Key: "кот", Events: []StoredEvent{{Chapter: 3, Event: "кот"}}}
	if parts := few.Parts(len(chapters)); len(parts) != 1 || parts[0].First != 0 || !reflect.DeepEqual(parts[0].Node, few) {
		t.Fatalf("a node of few events = %+v", parts)
	}
}

func TestShownEventsPrefersKeyAndLateEvents(t *testing.T) {
	var events []StoredEvent
	for chapter := range 50 {
		events = append(events, StoredEvent{Chapter: chapter, Event: fmt.Sprint(chapter), Key: chapter%10 == 0})
	}
	shown := shownEvents(events, 8)
	var chapters []int
	for _, event := range shown {
		chapters = append(chapters, event.Chapter)
	}
	if want := []int{0, 10, 20, 30, 40, 47, 48, 49}; !reflect.DeepEqual(chapters, want) {
		t.Fatalf("shown = %v, want %v", chapters, want)
	}
	if len(shownEvents(events[:3], 8)) != 3 {
		t.Fatal("few events are shown whole")
	}
}
