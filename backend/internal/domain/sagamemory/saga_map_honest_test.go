package sagamemory

import (
	"fmt"
	"reflect"
	"testing"
)

// honestSaga is the case of R11.1 in small: Мирон and Безмолвный share two events of one chapter; Горислав shares one
// with Безмолвный and stands beside him in three more chapters; Купава shares no event with Мирон, but a change names them both;
// Письмо and Мила are named in one chapter only, and two events bring Любава to Мирон.
func honestSaga() *CardSet {
	cards := map[int]*Card{}
	for chapter := range 24 {
		cards[chapter] = &Card{Characters: []Mention{{Name: "Мирон"}}}
	}
	both := func(chapter int, names ...string) {
		for _, name := range names {
			cards[chapter].Characters = append(cards[chapter].Characters, Mention{Name: name})
		}
	}
	both(0, "Безмолвный")
	cards[0].Events = []Event{{Event: "Мирон спорит", Who: []string{"Мирон", "Безмолвный"}, P: 2}, {Event: "Мирон уходит", Who: []string{"Мирон", "Безмолвный"}, P: 5}}
	both(1, "Безмолвный", "Горислав")
	cards[1].Events = []Event{{Event: "Все слышат голос", Who: []string{"Безмолвный", "Горислав"}}}
	for chapter := 2; chapter < 5; chapter++ {
		both(chapter, "Безмолвный", "Горислав")
	}
	both(6, "Купава")
	both(7, "Купава")
	cards[7].Changes = []Change{{Subject: "Купава", Change: "стала женой Мирона", P: 3}}
	both(8, "Мила")
	cards[8].Entities = []Mention{{Name: "Письмо Безмолвного", Type: "предмет"}}
	both(9, "Любава")
	cards[9].Events = []Event{{Event: "Любава лечит Мирона", Who: []string{"Любава", "Мирон"}}, {Event: "Любава ведёт Мирона", Who: []string{"Любава", "Мирон"}}}
	both(10, "Любава")
	return NewCardSet(cards, nil)
}

func honestRows(t *testing.T) MapRows {
	t.Helper()
	chapters := make([]Chapter, 24)
	for i := range chapters {
		chapters[i] = Chapter{ID: fmt.Sprintf("c%d", i), BookID: "b", BookOrder: 1, BookTitle: "Том", Order: i + 1, Title: fmt.Sprintf("Глава %d", i+1), Paragraphs: []Paragraph{{Text: "текст"}}}
	}
	version := NewVersion("project", "draft", "fp", chapters)
	return BuildMapRows(version, honestSaga(), wholeIndex(t, version, map[string]string{"текст": "текст", "мирона": "мирон"}), "revision", nil)
}

func documentPairs(document MapDocument) []string {
	pairs := []string{}
	for _, link := range document.Links {
		pairs = append(pairs, document.Nodes[link.Source].Name+" — "+document.Nodes[link.Target].Name)
	}
	return pairs
}

func documentNames(document MapDocument) []string {
	names := []string{}
	for _, node := range document.Nodes {
		names = append(names, node.Name)
	}
	return names
}

// R11.1: the map draws two shared events and a change naming both. One shared event and chapters beside each other are lines of
// the card. Events are counted one by one: two of one chapter are two.
func TestTheMapDrawsOnlyLinksWithAReason(t *testing.T) {
	rows := honestRows(t)
	want := []string{"Мирон — Безмолвный", "Мирон — Купава", "Мирон — Любава"}
	if got := documentPairs(rows.Document); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %v, want %v", got, want)
	}
	card := storedNode(t, rows, "Безмолвный").Card(rows.Document.Chapters, -1, MapHidden{})
	if len(card.Links) != 1 || card.Links[0].Name != "Мирон" || card.Links[0].Together != 2 {
		t.Fatalf("links of the card = %+v", card.Links)
	}
	if len(card.Beside) != 1 || card.Beside[0].Name != "Горислав" {
		t.Fatalf("beside = %+v", card.Beside)
	}
	kupava := storedNode(t, rows, "Купава").Card(rows.Document.Chapters, -1, MapHidden{})
	if len(kupava.Links) != 1 || kupava.Links[0].Name != "Мирон" || kupava.Links[0].Together != 0 || len(kupava.Links[0].Changes) != 1 {
		t.Fatalf("a change is a reason by itself: %+v", kupava.Links)
	}
	if early := storedNode(t, rows, "Купава").Card(rows.Document.Chapters, 6, MapHidden{}); len(early.Links) != 0 {
		t.Fatalf("before its change the link is not there: %+v", early)
	}
}

// R11.4: a node of one chapter stays off the map without a reason, in a big saga and a small one; search and the cards still have it.
func TestANodeOfOneChapterNeedsAReason(t *testing.T) {
	small := NewCardSet(map[int]*Card{
		0: {
			Characters: []Mention{{Name: "Мирон"}, {Name: "Мила"}, {Name: "Любава"}},
			Entities:   []Mention{{Name: "Письмо", Type: "предмет"}},
			Events: []Event{
				{Event: "Любава лечит Мирона", Who: []string{"Любава", "Мирон"}}, {Event: "Любава ведёт Мирона", Who: []string{"Любава", "Мирон"}},
				{Event: "Мила читает письмо Мирону", Who: []string{"Мила", "Мирон", "Письмо"}},
			},
		},
		1: {Characters: []Mention{{Name: "Мирон"}}},
		2: {Characters: []Mention{{Name: "Мирон"}}},
	}, nil)
	chapters := []Chapter{
		{ID: "c0", BookID: "b", BookOrder: 1, BookTitle: "Том", Order: 1, Title: "Глава 1", Paragraphs: []Paragraph{{Text: "текст"}}},
		{ID: "c1", BookID: "b", BookOrder: 1, BookTitle: "Том", Order: 2, Title: "Глава 2", Paragraphs: []Paragraph{{Text: "текст"}}},
		{ID: "c2", BookID: "b", BookOrder: 1, BookTitle: "Том", Order: 3, Title: "Глава 3", Paragraphs: []Paragraph{{Text: "текст"}}},
	}
	version := NewVersion("project", "draft", "fp", chapters)
	rows := BuildMapRows(version, small, wholeIndex(t, version, map[string]string{"текст": "текст"}), "revision", nil)
	if got, want := documentNames(rows.Document), []string{"Мирон", "Любава"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes of a small saga = %v, want %v", got, want)
	}
	if rows.Document.Hero == nil || *rows.Document.Hero != "мирон" {
		t.Fatalf("hero = %v", rows.Document.Hero)
	}
	if hero := storedNode(t, rows, "Мирон").Card(rows.Document.Chapters, -1, MapHidden{}); len(hero.Links) != 1 || hero.Links[0].Name != "Любава" {
		t.Fatalf("the card of the hero = %+v", hero)
	}
	hero := storedNode(t, rows, "Мирон").Card(rows.Document.Chapters, -1, MapHidden{})
	if len(hero.Beside) != 2 || hero.Beside[0].Name != "Мила" || hero.Beside[1].Name != "Письмо" {
		t.Fatalf("the card of the hero names what is off the map: %+v", hero.Beside)
	}
	if card := storedNode(t, rows, "Письмо").Card(rows.Document.Chapters, -1, MapHidden{}); card.Mentions != 1 || rows.Names["письмо"] != "письмо" {
		t.Fatalf("a node off the map keeps its card and its name: %+v", card)
	}
	big := honestRows(t)
	for _, name := range []string{"Мила", "Письмо Безмолвного"} {
		for _, node := range big.Document.Nodes {
			if node.Name == name {
				t.Fatalf("%s of one chapter stays off the map", name)
			}
		}
	}
}

// R11.4: a saga of one or two chapters has nothing but nodes of one chapter, and the map keeps them.
func TestAVerySmallSagaKeepsItsNodes(t *testing.T) {
	small := NewCardSet(map[int]*Card{0: {Characters: []Mention{{Name: "Мирон"}, {Name: "Мила"}}, Entities: []Mention{{Name: "Письмо", Type: "предмет"}}}}, nil)
	version := NewVersion("project", "draft", "fp", []Chapter{{ID: "c0", BookID: "b", BookOrder: 1, BookTitle: "Том", Order: 1, Title: "Глава 1", Paragraphs: []Paragraph{{Text: "текст"}}}})
	rows := BuildMapRows(version, small, wholeIndex(t, version, map[string]string{"текст": "текст"}), "revision", nil)
	if got, want := documentNames(rows.Document), []string{"Мила", "Мирон", "Письмо"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
}

// R11.2: the card names the reason of a link: the latest shared events, with their chapters and paragraphs.
func TestACardNamesTheReasonOfALink(t *testing.T) {
	rows := honestRows(t)
	chapters := rows.Document.Chapters
	hero := storedNode(t, rows, "Мирон")
	var god CardLink
	for _, link := range hero.Card(chapters, -1, MapHidden{}).Links {
		if link.Name == "Безмолвный" {
			god = link
		}
	}
	if len(god.Shared) != 2 || god.Shared[0].Event != "Мирон спорит" || god.Shared[0].Chapter.Key != chapters[0].Key || god.Shared[0].Paragraph != 2 || god.Shared[1].Event != "Мирон уходит" {
		t.Fatalf("the reason of the link = %+v", god.Shared)
	}
	many := StoredNode{Key: "а", Name: "А", Chapters: []int{0}, Links: []StoredLink{{Key: "б", Name: "Б", Events: []int{0, 0, 0, 0, 0}, Shared: []int{0, 1, 2, 3, 4}}}}
	for i := range 5 {
		many.Events = append(many.Events, StoredEvent{Chapter: 0, Event: fmt.Sprint(i)})
	}
	shared := many.Card(chapters, -1, MapHidden{}).Links[0].Shared
	if len(shared) != maxLinkShared || shared[len(shared)-1].Event != "4" {
		t.Fatalf("the latest events = %+v", shared)
	}
}

// R11.3: what the author took off the map leaves the document and the cards when they are read, and nothing else changes.
func TestWhatTheAuthorHidLeavesTheMap(t *testing.T) {
	rows := honestRows(t)
	chapters := rows.Document.Chapters
	hidden := NewMapHidden([]MapHiddenItem{
		{ID: "1", NodeKey: "любава"},
		{ID: "2", NodeKey: "безмолвный", OtherKey: "мирон"},
	})
	document := rows.Document
	total := document.NodesTotal
	document.Hide(hidden)
	if got, want := documentNames(document), []string{"Мирон", "Безмолвный", "Горислав", "Купава"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	if got, want := documentPairs(document), []string{"Мирон — Купава"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %v, want %v", got, want)
	}
	if document.NodesTotal != total-1 || document.Hero == nil || len(rows.Document.Nodes) != 5 {
		t.Fatalf("document = %+v, the stored one has %d nodes", document, len(rows.Document.Nodes))
	}
	card := storedNode(t, rows, "Мирон").Card(chapters, -1, hidden)
	if len(card.Links) != 1 || card.Links[0].Name != "Купава" {
		t.Fatalf("links of the card = %+v", card.Links)
	}
	for _, beside := range card.Beside {
		if beside.Name == "Любава" || beside.Name == "Безмолвный" {
			t.Fatalf("what is hidden is not beside either: %+v", card.Beside)
		}
	}
	if gone := storedNode(t, rows, "Любава").Card(chapters, -1, hidden); gone.HiddenID != "1" || gone.Mentions != 2 {
		t.Fatalf("the card of a hidden node says so and stays: %+v", gone)
	}
	noHero := rows.Document
	noHero.Hide(NewMapHidden([]MapHiddenItem{{ID: "3", NodeKey: "мирон"}}))
	if noHero.Hero != nil || len(noHero.Links) != 0 {
		t.Fatalf("without the hero = %+v", noHero)
	}
}

// A browser holds the map until the decisions change, whatever their order.
func TestTheTagFollowsTheDecisions(t *testing.T) {
	one := NewMapHidden([]MapHiddenItem{{ID: "1", NodeKey: "а"}, {ID: "2", NodeKey: "б", OtherKey: "в"}})
	same := NewMapHidden([]MapHiddenItem{{ID: "2", NodeKey: "б", OtherKey: "в"}, {ID: "1", NodeKey: "а"}})
	other := NewMapHidden([]MapHiddenItem{{ID: "1", NodeKey: "а"}})
	if one.Tag() != same.Tag() || one.Tag() == other.Tag() || one.Tag() == "" {
		t.Fatalf("tags %q %q %q", one.Tag(), same.Tag(), other.Tag())
	}
	if (MapHidden{}).Tag() != "" {
		t.Fatal("no decisions, no tag: the revision stays what it was")
	}
}
