package sagamemory

import (
	"reflect"
	"testing"
)

// The cards Job writes the instructions its version names; the operator resolves the sets by name.
func TestCardInstructionSets(t *testing.T) {
	// specs/2026-11-01-index-foundation.md R2.5: the Job writes v4 cards with the pass over them.
	if CardVersion != 4 || CurrentCardInstructions.Text != CardInstructionsV4.Text || CurrentCardInstructions.Pass == "" {
		t.Fatalf("the cards Job writes v%d, want the v4 instructions with their pass", CardVersion)
	}
	if CurrentCardInstructions.Version != CardVersion {
		t.Fatalf("current instructions are v%d, CardVersion is %d", CurrentCardInstructions.Version, CardVersion)
	}
	for name, want := range map[string]int{"current": CardVersion, "v2": 2, "v3": 3, "v4": 4} {
		if set, ok := CardInstructionsNamed(name); !ok || set.Version != want || set.Text == "" {
			t.Fatalf("CardInstructionsNamed(%q) = v%d, %v; want v%d", name, set.Version, ok, want)
		}
	}
	if _, ok := CardInstructionsNamed("v9"); ok {
		t.Fatal("an unknown instruction set resolved")
	}
}

// specs/2026-10-29-chapter-cards-quality.md UC2: the v3 parts are normalized like the v2 ones —
// paragraphs outside the chapter become 0, blank entries go — and a span the model wrote loosely
// is read or dropped without losing the card: a span without an amount (null, missing) is
// unknown, and NaN or infinity would not even serialize.
func TestParseCardReadsV3Parts(t *testing.T) {
	card, err := ParseCard(`Карточка: {
	 "summary": "Мирон получил уровень.",
	 "when": {"opens": " На следующее утро ", "after": {"n": "1", "unit": "дня"}, "lasts": "3 часа", "date": ""},
	 "events": [{"event": "Мирон получил уровень", "who": ["Мирон", " "], "p": 2}, {"event": " ", "p": 1}, {"event": "Вне главы", "p": 40}],
	 "facts": [{"subject": "Мирон", "attribute": "Возраст", "value": "24 года", "p": 1}, {"subject": "Мирон", "attribute": "уровень", "value": ""},
	           {"subject": "Мирон", "attribute": "возраст", "value": "7 лет", "p": 2, "past": true}],
	 "changes": [{"subject": "Мирон", "change": "уровень 71", "p": 2}]
	}`, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := Card{
		Summary: "Мирон получил уровень.",
		When:    &ChapterTime{Opens: "На следующее утро", After: &Span{N: 1, Unit: "день"}, Lasts: &Span{N: 3, Unit: "час"}},
		Events:  []Event{{Event: "Мирон получил уровень", Who: []string{"Мирон"}, P: 2}, {Event: "Вне главы"}},
		Facts:   []Fact{{Subject: "Мирон", Attribute: "возраст", Value: "24 года", P: 1}, {Subject: "Мирон", Attribute: "возраст", Value: "7 лет", P: 2, Past: true}},
		Changes: []Change{{Subject: "Мирон", Change: "уровень 71", P: 2}},
	}
	if !reflect.DeepEqual(card, want) {
		t.Fatalf("ParseCard = %+v\nwant %+v", card, want)
	}
	for _, loose := range []string{
		`{"summary": "s", "when": {"after": {"n": 2, "unit": "эпоха"}, "lasts": {"n": -1, "unit": "час"}}}`,
		`{"summary": "s", "when": {"after": "давно", "lasts": null}}`,
		`{"summary": "s", "when": {}}`,
		`{"summary": "s", "when": {"after": {"n": null, "unit": "день"}, "lasts": {"unit": "час"}}}`,
		`{"summary": "s", "when": {"after": "NaN дней", "lasts": {"n": "Inf", "unit": "час"}}}`,
		`{"summary": "s", "when": {"after": {"n": "-Inf", "unit": "год"}}}`,
	} {
		card, err := ParseCard(loose, 3)
		if err != nil || card.When != nil {
			t.Fatalf("ParseCard(%s) = %+v, %v; want the card without a chapter time", loose, card, err)
		}
	}
	approx, err := ParseCard(`{"summary": "s", "when": {"lasts": {"n": 0.5, "unit": "Недели", "approx": true}}}`, 3)
	if err != nil || approx.When == nil || *approx.When.Lasts != (Span{N: 0.5, Unit: "неделя", Approx: true}) {
		t.Fatalf("approximate span = %+v, %v", approx.When, err)
	}
}

// A v2 reader reads only the parts it needs: the v3 parts are left out unless asked for.
func TestCardPartsLeaveOutV3Parts(t *testing.T) {
	omitted := CardParts{Summary: true}.Omitted()
	for _, key := range []string{"when", "events", "facts"} {
		found := false
		for _, left := range omitted {
			found = found || left == key
		}
		if !found {
			t.Fatalf("CardParts{Summary}.Omitted() = %v, want %q left out", omitted, key)
		}
	}
	if left := AllCardParts.Omitted(); len(left) != 0 {
		t.Fatalf("AllCardParts leaves out %v", left)
	}
}

// specs/2026-11-01-index-foundation.md R2.1: who knows what and what stays open are normalized like
// the other parts; blank entries go and paragraphs outside the chapter become 0.
func TestParseCardReadsV4Parts(t *testing.T) {
	card, err := ParseCard(`{"summary": "Глава.",
	 "events": [{"event": "Бой", "p": 1, "key": true}, {"event": "Обед", "p": 2}],
	 "knows": [{"who": " Мирон ", "kind": "Скрывает", "what": "кто он", "p": 2}, {"who": "", "what": "пусто"}, {"who": "Молли", "what": "ничего", "p": 99}],
	 "hooks": [{"what": "шрам без причины", "p": 3}, {"what": "  "}]}`, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !card.Events[0].Key || card.Events[1].Key {
		t.Fatalf("events %+v", card.Events)
	}
	want := []Knowledge{{Who: "Мирон", Kind: "скрывает", What: "кто он", P: 2}, {Who: "Молли", What: "ничего"}}
	if !reflect.DeepEqual(card.Knows, want) {
		t.Fatalf("knows %+v", card.Knows)
	}
	if !reflect.DeepEqual(card.Hooks, []Hook{{What: "шрам без причины", P: 3}}) {
		t.Fatalf("hooks %+v", card.Hooks)
	}
}

// R2.4: additions need no summary; what a list already says in the same words is left out.
func TestCardAddLeavesOutWhatTheCardSays(t *testing.T) {
	card := Card{Summary: "Глава.", Facts: []Fact{{Subject: "Мирон", Attribute: "возраст", Value: "24 года"}}, Hooks: []Hook{{What: "Шрам"}}}
	more, err := ParseCardAdditions(`{"facts": [{"subject": "мирон", "attribute": "Возраст", "value": "24 года"}, {"subject": "Мирон", "attribute": "рост", "value": "высокий"}],
	 "hooks": [{"what": "шрам"}, {"what": "письмо"}], "knows": [{"who": "Мирон", "what": "тайна"}]}`, 1)
	if err != nil {
		t.Fatal(err)
	}
	card.Add(more)
	if card.Summary != "Глава." || len(card.Facts) != 2 || len(card.Hooks) != 2 || len(card.Knows) != 1 {
		t.Fatalf("card after the pass: %+v", card)
	}
}

// v4 parts are cut in SQL like the others.
func TestCardPartsLeaveOutV4Parts(t *testing.T) {
	omitted := CardParts{Summary: true}.Omitted()
	for _, key := range []string{"knows", "hooks"} {
		found := false
		for _, name := range omitted {
			found = found || name == key
		}
		if !found {
			t.Fatalf("%s is read by a reader that did not ask for it: %v", key, omitted)
		}
	}
}
