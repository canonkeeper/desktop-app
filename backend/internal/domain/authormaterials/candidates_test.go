package authormaterials

import (
	"reflect"
	"testing"
)

func draftNamed(t *testing.T, drafts []Draft, name string) Draft {
	t.Helper()
	for _, draft := range drafts {
		if draft.Name == name {
			return draft
		}
	}
	t.Fatalf("no draft %q in %+v", name, drafts)
	return Draft{}
}

// specs/2026-11-04-saga-map.md UC9 Tests: the note is the main material of a record, the fill signs its rarity.
func TestCandidatesTakeTheNoteAndTheFill(t *testing.T) {
	parsed, err := ParseXLSX(skillsWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	drafts := Candidates(parsed, []Mapping{
		{Kind: KindSkill, Header: true, Name: 0, Description: -1},
		{Kind: KindPlace, Header: true, Name: 0, Description: -1},
	})
	nightmare := draftNamed(t, drafts, "омут снов")
	if nightmare.Kind != KindSkill || nightmare.Section != "Навыки" || nightmare.Description != "Сны становятся явью." {
		t.Fatalf("nightmare = %+v", nightmare)
	}
	want := []Property{
		{Key: "Редкость", Value: "эпический (оранжевая заливка)"},
		{Key: "Улучшить навык", Value: "[ужас] → [омут снов]"},
		{Key: "Ранг", Value: "Уникальный (вне уровней)"},
		{Key: "Затраты эфира", Value: "40"},
	}
	if !reflect.DeepEqual(nightmare.Properties, want) {
		t.Fatalf("properties = %+v", nightmare.Properties)
	}
	moment := draftNamed(t, drafts, "Миг назад")
	if moment.Description != "Возвращает бой на миг назад.\nТолько раз в бою." {
		t.Fatalf("moment = %+v", moment)
	}
	if !reflect.DeepEqual(moment.Properties, []Property{{Key: "Столбец 3", Value: "12 (красный текст)"}}) {
		t.Fatalf("a column without a header = %+v", moment.Properties)
	}
	port := draftNamed(t, drafts, "Белый порт")
	if port.Kind != KindPlace || !reflect.DeepEqual(port.Properties, []Property{{Key: "Оформление", Value: "оранжевая заливка"}}) {
		t.Fatalf("port = %+v", port)
	}
	if len(drafts) != 3 {
		t.Fatalf("a row without a name is not a record: %+v", drafts)
	}
}

func TestCandidatesOfProseTakeOnlyNamesOfTheText(t *testing.T) {
	section := Section{Title: "Мир", Paragraphs: []Paragraph{
		{Text: "Студена", Level: 2},
		{Text: "Столица севера.", Note: "Население: 40 тысяч"},
		{Text: "Туманный лес за городом."},
		{Text: "Кир — проводник."},
	}}
	drafts := Candidates(Parsed{Sections: []Section{section}}, []Mapping{{Kind: KindOther, Items: []ProseItem{
		{Name: "Студена", Kind: KindPlace, From: 0, To: 2},
		{Name: "Придуманное имя", Kind: KindPlace, From: 2, To: 2},
		{Name: "Кир", Kind: KindCharacter, From: 3, To: 99},
		{Name: "туманный лес", From: 5, To: 1},
	}}})
	city := draftNamed(t, drafts, "Студена")
	if city.Kind != KindPlace || city.Description != "Столица севера.\nТуманный лес за городом." || city.Section != "Мир" {
		t.Fatalf("city = %+v", city)
	}
	if !reflect.DeepEqual(city.Properties, []Property{{Key: "Население", Value: "40 тысяч"}}) {
		t.Fatalf("properties = %+v", city.Properties)
	}
	if guide := draftNamed(t, drafts, "Кир"); guide.Kind != KindCharacter || guide.Description != "проводник." {
		t.Fatalf("guide = %+v", guide)
	}
	if len(drafts) != 2 {
		t.Fatalf("a name the text does not hold or wrong bounds are not a record: %+v", drafts)
	}
}

func TestCandidatesMergeOneNameOfAFile(t *testing.T) {
	section := Section{Title: "Навыки", Rows: [][]Cell{
		{{Text: "Ужас"}, {Text: "первое"}},
		{{Text: "УЖАС"}, {Text: "второе"}},
		{{Text: "ёж"}, {Text: "третье"}},
		{{Text: "еж"}, {Text: "третье"}},
	}}
	drafts := Candidates(Parsed{Sections: []Section{section}}, []Mapping{{Kind: KindSkill, Name: 0, Description: 1}})
	if len(drafts) != 2 {
		t.Fatalf("drafts = %+v", drafts)
	}
	if drafts[0].Name != "Ужас" || drafts[0].Description != "первое\nвторое" {
		t.Fatalf("merged = %+v", drafts[0])
	}
	if drafts[1].Description != "третье" {
		t.Fatalf("the same text is not repeated: %+v", drafts[1])
	}
}

func TestCandidatesKeepTheLimitsOfARecord(t *testing.T) {
	long := make([]rune, MaxDescriptionRunes+100)
	for index := range long {
		long[index] = 'я'
	}
	section := Section{Rows: [][]Cell{{{Text: "Навык"}, {Text: string(long)}}}}
	drafts := Candidates(Parsed{Sections: []Section{section}}, []Mapping{{Kind: KindSkill, Name: 0, Description: 1}})
	if len([]rune(drafts[0].Description)) != MaxDescriptionRunes {
		t.Fatalf("description of %d runes", len([]rune(drafts[0].Description)))
	}
	if _, ok := CleanDraft(drafts[0]); !ok {
		t.Fatal("a candidate must be a valid record")
	}
}

func TestCleanDraftRefusesWhatARecordCannotBe(t *testing.T) {
	good := Draft{Name: " Белый порт ", Kind: KindPlace, Description: " Город. ", Properties: []Property{{Key: " Ранг ", Value: " 1 "}}, Section: " Места "}
	clean, ok := CleanDraft(good)
	if !ok || clean.Name != "Белый порт" || clean.Description != "Город." || clean.Properties[0] != (Property{Key: "Ранг", Value: "1"}) || clean.Section != "Места" {
		t.Fatalf("clean = %+v, %v", clean, ok)
	}
	for name, draft := range map[string]Draft{
		"no name":      {Kind: KindPlace},
		"bad kind":     {Name: "Имя", Kind: "planet"},
		"no key":       {Name: "Имя", Kind: KindPlace, Properties: []Property{{Value: "1"}}},
		"many":         {Name: "Имя", Kind: KindPlace, Properties: make([]Property, MaxProperties+1)},
		"control char": {Name: "Им\x00я", Kind: KindPlace},
	} {
		if _, ok := CleanDraft(draft); ok {
			t.Errorf("%s passed", name)
		}
	}
}

func TestLinkedNamesAreInBracketsAndInText(t *testing.T) {
	draft := Draft{Name: "омут снов", Description: "Пугает Мирона у Белого порта.", Properties: []Property{{Key: "Улучшить навык", Value: "[ужас] → [омут снов]"}}}
	if got := BracketNames(draft); !reflect.DeepEqual(got, []string{"ужас"}) {
		t.Fatalf("brackets = %v", got)
	}
}

// In prose a colon is part of a sentence, and a paragraph that starts with the name of its record says what the record is:
// neither is a property (R9.8). A short key of a note stays one.
func TestProseKeepsSentencesWithAColonInTheDescription(t *testing.T) {
	section := Section{Paragraphs: []Paragraph{
		{Text: "Гильдия теней давно хочет его смерти: он отказался от их заказа."},
		{Text: "Порт Альба: торговый город на юге."},
		{Text: "Ледяная стрела — замедляет цель."},
		{Text: "Студена — столица севера.", Note: "Население: 40 тысяч"},
	}}
	drafts := Candidates(Parsed{Sections: []Section{section}}, []Mapping{{Kind: KindOther, Items: []ProseItem{
		{Name: "Гильдия теней", Kind: KindFaction, From: 0, To: 0},
		{Name: "Порт Альба", Kind: KindPlace, From: 1, To: 1},
		{Name: "Ледяная стрела", Kind: KindSkill, From: 2, To: 2},
		{Name: "Студена", Kind: KindPlace, From: 3, To: 3},
	}}})
	want := map[string]Draft{
		"Гильдия теней":  {Description: "Гильдия теней давно хочет его смерти: он отказался от их заказа."},
		"Порт Альба":     {Description: "торговый город на юге."},
		"Ледяная стрела": {Description: "замедляет цель."},
		"Студена":        {Description: "столица севера.", Properties: []Property{{Key: "Население", Value: "40 тысяч"}}},
	}
	for name, expected := range want {
		got := draftNamed(t, drafts, name)
		if got.Description != expected.Description || !reflect.DeepEqual(got.Properties, expected.Properties) {
			t.Errorf("%s: %q %+v", name, got.Description, got.Properties)
		}
	}
}
