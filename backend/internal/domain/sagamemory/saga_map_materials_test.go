package sagamemory

import (
	"slices"
	"strings"
	"testing"
)

func TestNamedInFindsANameInItsCases(t *testing.T) {
	for _, testCase := range []struct {
		text, name string
		want       bool
	}{
		{"Пугает Мирона у ворот", "Мирон", true},
		{"Мирон", "Мирон", true},
		{"Пугает Милу", "Мила", true},
		{"[ужас] → [кошмар]", "ужас", true},
		{"У Белого порта", "Белый порт", true},
		{"Белый и порт", "Белый порт", false},
		{"засада у моста", "сада", false},
		{"адамант твёрд", "ада", false},
		{"милость князя", "Мила", false},
		{"ал", "ал", false},
		{"засада, и сада нет", "сада", true},
	} {
		if got := newNamedText(testCase.text).names(&GlossaryEntry{Name: testCase.name}); got != testCase.want {
			t.Errorf("NamedIn(%q, %q) = %v", testCase.text, testCase.name, got)
		}
	}
	if !newNamedText("Зовут Лисой").names(&GlossaryEntry{Name: "Мила", Aliases: []string{"Лиса"}}) {
		t.Error("an alias names the entry")
	}
}

// specs/2026-11-04-saga-map.md UC9 Tests: a record the book does not know is a node without chapters with a link to the
// node it names; once the book knows the name there is one node.
func TestAddMaterialsDrawsWhatTheBookDoesNotKnow(t *testing.T) {
	set := mapSaga()
	m := BuildMap(set)
	before, links := len(m.Nodes), len(m.Links)
	hero := m.Nodes[m.Hero].Entry
	m.AddMaterials(set.Glossary, []MapMaterial{
		{Name: "Миг назад", Category: MapPlace, Text: "Сюда приходит " + hero.Name + "а."},
		{Name: hero.Name, Category: MapCharacter, Text: "Уже в книге."},
		{Name: "омут снов", Text: "Навык, который пугает " + hero.Name + "а."},
	})
	if len(m.Nodes) != before+1 {
		t.Fatalf("nodes = %d, were %d", len(m.Nodes), before)
	}
	node := m.Nodes[before]
	if !node.Material || node.Entry.Name != "Миг назад" || node.Category != MapPlace || len(node.Entry.Chapters) != 0 {
		t.Fatalf("node = %+v", node)
	}
	if len(m.Links) != links+1 {
		t.Fatalf("links = %d, were %d", len(m.Links), links)
	}
	link := m.Links[links]
	if !link.Material || link.Target != before || link.Source != m.Hero || len(link.Events) != 0 {
		t.Fatalf("link = %+v", link)
	}
	if found, ok := m.Node(node.Entry); !ok || found != before {
		t.Fatalf("node of its entry = %d, %v", found, ok)
	}
}

// R9.5: on the rows a record the book does not know has its node and a link that stands at any chapter, after the links of the book.
func TestMaterialRowsCountWithoutChapters(t *testing.T) {
	rows := mapRowsWith(t, []MapMaterial{
		{Name: "Миг назад", Category: MapPlace, Text: "Здесь был Мирон.", Card: CardMaterial{ID: "place"}},
		{Name: "Взлом", Category: "", Text: "Навык Молли.", Card: CardMaterial{ID: "known"}},
		{Name: "Омут снов", Category: "", Text: "Пугает Мирона.", Card: CardMaterial{ID: "skill"}},
		{Name: "омут  СНОВ", Category: "", Text: "Та же запись.", Card: CardMaterial{ID: "same key"}},
		{Name: "« »", Category: MapPlace, Text: "Без имени.", Card: CardMaterial{ID: "no key"}},
	})
	chapters := rows.Document.Chapters
	last := rows.Document.Nodes[len(rows.Document.Nodes)-1]
	if last.Name != "Миг назад" || !last.Material || last.Category != "place" {
		t.Fatalf("last node = %+v", last)
	}
	place := storedNode(t, rows, "Миг назад").Card(chapters, -1, MapHidden{})
	if place.Material == nil || place.Material.ID != "place" || len(place.Links) != 1 || place.Links[0].Name != "Мирон" || !place.Links[0].FromMaterials {
		t.Fatalf("the card of a record = %+v", place)
	}
	hero := storedNode(t, rows, "Мирон")
	early := hero.Card(chapters, 0, MapHidden{})
	if len(early.Links) != 1 || !early.Links[0].FromMaterials {
		t.Fatalf("a link from a record stands at any chapter: %+v", early.Links)
	}
	if whole := hero.Card(chapters, -1, MapHidden{}); !whole.Links[len(whole.Links)-1].FromMaterials || whole.Links[0].FromMaterials {
		t.Fatalf("the links of the book come first: %+v", whole.Links)
	}
	var named []string
	for _, record := range hero.Card(chapters, -1, MapHidden{}).NamedIn {
		named = append(named, record.ID)
	}
	if want := []string{"place", "skill"}; !slices.Equal(named, want) {
		t.Fatalf("named in %v, want %v", named, want)
	}
	if own := storedNode(t, rows, "Взлом").Card(chapters, -1, MapHidden{}); own.Material == nil || own.Material.ID != "known" || own.Mentions != 20 {
		t.Fatalf("a record of a name the book knows = %+v", own)
	}
	skill := storedNode(t, rows, "Омут снов")
	if skill.Category != "" || skill.Material.ID != "skill" || rows.Names["омут снов"] != skill.Key || !strings.Contains(skill.SearchText, "пугает мирона") {
		t.Fatalf("a skill = %+v", skill)
	}
	for _, node := range rows.Nodes {
		if node.Key == "" || node.Material != nil && node.Material.ID == "same key" {
			t.Fatalf("a record without a key of its own has a row: %+v", node)
		}
	}
	// A record stays on every part of a node along the scale.
	for _, part := range hero.Parts(len(chapters)) {
		if card := part.Node.Card(chapters, part.First, MapHidden{}); len(card.NamedIn) != 2 || !card.Links[len(card.Links)-1].FromMaterials {
			t.Fatalf("part from %d = %+v", part.First, card)
		}
	}
}
