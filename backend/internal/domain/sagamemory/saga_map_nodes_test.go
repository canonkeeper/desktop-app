package sagamemory

import (
	"fmt"
	"testing"
)

// specs/2026-11-10-map-nodes.md: a node of the map is what the book names more than once.

// nodesRows prepares the map of a saga of n cards with Мирон in every chapter and what a case adds.
func nodesRows(t *testing.T, n int, add func(cards map[int]*Card), materials []MapMaterial) MapRows {
	t.Helper()
	cards := map[int]*Card{}
	chapters := make([]Chapter, n)
	for chapter := range n {
		cards[chapter] = &Card{Characters: []Mention{{Name: "Мирон"}}}
		chapters[chapter] = Chapter{ID: fmt.Sprintf("c%d", chapter), BookID: "b", BookOrder: 1, BookTitle: "Том", Order: chapter + 1, Title: fmt.Sprintf("Глава %d", chapter+1), Paragraphs: []Paragraph{{Text: "текст"}}}
	}
	add(cards)
	version := NewVersion("project", "draft", "fp", chapters)
	return BuildMapRows(version, NewCardSet(cards, nil), wholeIndex(t, version, map[string]string{"текст": "текст", "мирона": "мирон"}), "revision", materials)
}

// nameIn puts characters in chapters.
func nameIn(cards map[int]*Card, name string, chapters ...int) {
	for _, chapter := range chapters {
		cards[chapter].Characters = append(cards[chapter].Characters, Mention{Name: name})
	}
}

// shareEvents gives a chapter events all of who take part in: two shared events are a reason (R11.1), one is not.
func shareEvents(cards map[int]*Card, chapter, events int, who ...string) {
	for i := range events {
		cards[chapter].Events = append(cards[chapter].Events, Event{Event: fmt.Sprintf("%v вместе %d", who, i), Who: who})
	}
}

func onMap(rows MapRows, name string) *MapDocumentNode {
	for i := range rows.Document.Nodes {
		if rows.Document.Nodes[i].Name == name {
			return &rows.Document.Nodes[i]
		}
	}
	return nil
}

// MN1.1: from 8 cards a name of one chapter is no node, even with two events of its chapter; below 8 it is one with a reason (R11.4).
func TestFromEightCardsANodeIsNamedInTwoChapters(t *testing.T) {
	lone := func(cards map[int]*Card) {
		nameIn(cards, "Мила", 0)
		shareEvents(cards, 0, 2, "Мила", "Мирон")
	}
	if seven := nodesRows(t, 7, lone, nil); onMap(seven, "Мила") == nil {
		t.Fatalf("a saga of 7 cards draws a node of one chapter with a reason: %v", documentNames(seven.Document))
	}
	eight := nodesRows(t, 8, lone, nil)
	if onMap(eight, "Мила") != nil {
		t.Fatalf("a saga of 8 cards draws a node of one chapter: %v", documentNames(eight.Document))
	}
	if eight.Names["мила"] != "мила" || storedNode(t, eight, "Мила").Card(eight.Document.Chapters, -1, MapHidden{}).Mentions != 1 {
		t.Fatalf("a name off the map keeps its search and its card: %v", eight.Names)
	}
}

// MN1.2: from 8 cards a node of two chapters needs a reason among the names of two chapters; one of three needs none.
func TestANodeOfTwoChaptersNeedsAReason(t *testing.T) {
	rows := nodesRows(t, 8, func(cards map[int]*Card) {
		nameIn(cards, "Рейн", 1, 2)
		nameIn(cards, "Любава", 3, 4)
		shareEvents(cards, 3, 1, "Любава", "Мирон")
		shareEvents(cards, 4, 1, "Любава", "Мирон")
		nameIn(cards, "Купава", 5, 6)
		cards[6].Changes = []Change{{Subject: "Купава", Change: "стала женой Мирона", P: 3}}
		nameIn(cards, "Анна", 6, 7)
		nameIn(cards, "Борис", 6, 7)
		shareEvents(cards, 7, 2, "Анна", "Борис")
		nameIn(cards, "Глеб", 2, 3)
		nameIn(cards, "Ольга", 3)
		shareEvents(cards, 3, 2, "Глеб", "Ольга")
		for _, chapter := range []int{1, 2, 3} {
			cards[chapter].Entities = append(cards[chapter].Entities, Mention{Name: "Россия", Type: "страна"})
		}
	}, nil)
	for name, want := range map[string]bool{
		"Мирон": true, "Россия": true, "Любава": true, "Купава": true, "Анна": true, "Борис": true,
		"Рейн": false, "Глеб": false, "Ольга": false,
	} {
		if got := onMap(rows, name) != nil; got != want {
			t.Errorf("%s on the map = %v, want %v; nodes %v", name, got, want, documentNames(rows.Document))
		}
	}
	// MN1.6: the total is what the map draws.
	if rows.Document.NodesTotal != len(rows.Document.Nodes) {
		t.Errorf("total = %d, the map draws %d", rows.Document.NodesTotal, len(rows.Document.Nodes))
	}
}

// MN1.3: what the author wrote about stays: a name of the book with a record of the same name at any chapters, a record the book
// does not know with its links; a name the record only speaks of stays by that link once it is named in two chapters.
func TestWhatTheAuthorWroteAboutStaysOnTheMap(t *testing.T) {
	rows := nodesRows(t, 8, func(cards map[int]*Card) {
		nameIn(cards, "Полина", 0)
		nameIn(cards, "Рейн", 1, 2)
		nameIn(cards, "Мила", 3)
	}, []MapMaterial{
		{Name: "Полина", Category: MapCharacter, Text: "Падчерица генерала.", Card: CardMaterial{ID: "polina"}},
		{Name: "Башня", Category: MapPlace, Text: "Здесь живут Рейн и Мила.", Card: CardMaterial{ID: "tower"}},
	})
	polina := onMap(rows, "Полина")
	if polina == nil || polina.Material || len(polina.Chapters) != 1 {
		t.Fatalf("a name of one chapter the author wrote about = %+v; nodes %v", polina, documentNames(rows.Document))
	}
	tower := onMap(rows, "Башня")
	if tower == nil || !tower.Material || len(tower.Chapters) != 0 {
		t.Fatalf("a record the book does not know = %+v", tower)
	}
	if onMap(rows, "Рейн") == nil || onMap(rows, "Мила") != nil {
		t.Fatalf("a record holds a name of two chapters it speaks of, not one of one chapter: %v", documentNames(rows.Document))
	}
	if got, want := documentPairs(rows.Document), []string{"Рейн — Башня"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("links = %v, want %v", got, want)
	}
	// MN1.6: a record the book does not know is a node of the map and counts.
	if rows.Document.NodesTotal != len(rows.Document.Nodes) {
		t.Errorf("total = %d, the map draws %d", rows.Document.NodesTotal, len(rows.Document.Nodes))
	}
}

// MN1.4: what the rules took off stays in search and in the card, and the card of a neighbor names it beside, after the nodes of the map.
func TestWhatTheRulesTookOffStaysBeside(t *testing.T) {
	rows := nodesRows(t, 8, func(cards map[int]*Card) {
		nameIn(cards, "Любава", 3, 4)
		shareEvents(cards, 3, 2, "Любава", "Мирон")
		nameIn(cards, "Ротшильд", 2, 5)
		shareEvents(cards, 2, 1, "Ротшильд", "Мирон")
	}, nil)
	if onMap(rows, "Ротшильд") != nil {
		t.Fatalf("a name of two chapters without a reason stands on the map: %v", documentNames(rows.Document))
	}
	if rows.Names["ротшильд"] != "ротшильд" {
		t.Fatalf("search finds what the rules took off: %v", rows.Names)
	}
	if card := storedNode(t, rows, "Ротшильд").Card(rows.Document.Chapters, -1, MapHidden{}); card.Mentions != 2 || len(card.Beside) != 1 || card.Beside[0].Name != "Мирон" {
		t.Fatalf("the card of what the rules took off = %+v", card)
	}
	hero := storedNode(t, rows, "Мирон")
	card := hero.Card(rows.Document.Chapters, -1, MapHidden{})
	if len(card.Links) != 1 || card.Links[0].Name != "Любава" || len(card.Beside) != 1 || card.Beside[0].Name != "Ротшильд" {
		t.Fatalf("the card of the hero = links %+v, beside %+v", card.Links, card.Beside)
	}
	if last := hero.Links[len(hero.Links)-1]; last.Name != "Ротшильд" || last.Node != len(rows.Document.Nodes) {
		t.Fatalf("the link to what the rules took off stands after the nodes of the map: %+v", hero.Links)
	}
}

// MN1.6: without the bound the total is what the map draws; with it, what passed the chapters.
func TestTheTotalOfTheMapIsWhatItDraws(t *testing.T) {
	rows := mapRows(t)
	if rows.Document.NodesTotal != len(rows.Document.Nodes) {
		t.Fatalf("total = %d, the map draws %d: %v", rows.Document.NodesTotal, len(rows.Document.Nodes), documentNames(rows.Document))
	}
}
