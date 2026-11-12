package sagamemory

import (
	"reflect"
	"testing"
)

// firstMap is a prepared document of the map as the build writes it (specs/2026-11-10-first-questions.md FQ1.1): Тихон Ремезов is
// the hero; Захар Плотников shares three events with him, Ульяна two and a change, Ферапонт two; Маяк is named in four chapters,
// Слобода in two; a record of the author, Белый острог, is on the map with a link of its own.
func firstMap() *MapDocument {
	hero := "тихон ремезов"
	return &MapDocument{
		Hero: &hero,
		Nodes: []MapDocumentNode{
			{Key: "тихон ремезов", Name: "Тихон Ремезов", Category: "character", Chapters: []int{0, 1, 2, 3, 4}},
			{Key: "захар плотников", Name: "Захар Плотников", Category: "character", Chapters: []int{0, 1, 2}},
			{Key: "ульяна", Name: "Ульяна", Category: "character", Chapters: []int{1, 2}},
			{Key: "ферапонт", Name: "Ферапонт", Category: "character", Chapters: []int{3, 4}},
			{Key: "маяк", Name: "Маяк", Category: "place", Chapters: []int{0, 1, 3, 4}},
			{Key: "слобода", Name: "Слобода", Category: "place", Chapters: []int{2, 3}},
			{Key: "белый острог", Name: "Белый острог", Category: "place", Chapters: []int{}, Material: true},
			{Key: "орден", Name: "Орден", Category: "faction", Chapters: []int{1, 2, 3}},
		},
		Links: []MapDocumentLink{
			{Source: 0, Target: 1, Events: []int{0, 1, 2}},
			{Source: 0, Target: 2, Events: []int{1, 2}, Changes: []int{2}},
			{Source: 0, Target: 3, Events: []int{3, 4}},
			{Source: 0, Target: 4, Events: []int{0, 1, 3, 4}},
			{Source: 0, Target: 6, Material: true},
		},
	}
}

func questionTexts(questions []FirstQuestion) []string {
	texts := make([]string, len(questions))
	for i, question := range questions {
		texts[i] = string(question.Kind) + ": " + question.Text
	}
	return texts
}

// FQ1.2, FQ1.3: the hero, his strongest neighbor of the characters and the place of most chapters, in these words.
func TestFirstQuestionsAskOfTheHeroHisNeighborAndThePlace(t *testing.T) {
	got := questionTexts(FirstQuestions(firstMap()))
	want := []string{
		"hero: Тихон Ремезов: кто это и что важного происходит с этим персонажем?",
		"pair: Тихон Ремезов и Захар Плотников: что их связывает?",
		"place: Где впервые упоминается Маяк и что там происходит?",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("questions = %q\nwant %q", got, want)
	}
}

// FQ1.2: the neighbor goes by the shared events, then by the changes, then by the name; a place linked more strongly is no pair.
func TestTheNeighborOfTheHeroGoesByEventsThenChangesThenName(t *testing.T) {
	pair := func(document *MapDocument) string {
		t.Helper()
		for _, question := range FirstQuestions(document) {
			if question.Kind == FirstQuestionPair {
				return question.Text
			}
		}
		return ""
	}
	document := firstMap()
	document.Links[0].Events = []int{0, 1}
	if got := pair(document); got != "Тихон Ремезов и Ульяна: что их связывает?" {
		t.Fatalf("equal events, more changes: %q", got)
	}
	document.Links[1].Changes = nil
	if got := pair(document); got != "Тихон Ремезов и Захар Плотников: что их связывает?" {
		t.Fatalf("equal events and changes, the first name: %q", got)
	}
	document.Links = document.Links[3:]
	if got := pair(document); got != "" {
		t.Fatalf("a hero linked only to a place and a record has no pair: %q", got)
	}
	if got := questionTexts(FirstQuestions(document)); len(got) != 2 || got[1] != "place: Где впервые упоминается Маяк и что там происходит?" {
		t.Fatalf("without a pair = %q", got)
	}
}

// FQ1.2: without places the third question is of an item or a faction, without «что там происходит»; ties go by the name.
func TestWithoutPlacesTheThirdQuestionIsOfAnItemOrAFaction(t *testing.T) {
	document := firstMap()
	for i := range document.Nodes {
		if document.Nodes[i].Category == "place" && !document.Nodes[i].Material {
			document.Nodes[i].Category = "item"
		}
	}
	document.Nodes[4].Chapters = []int{1, 2, 3}
	got := FirstQuestions(document)
	if len(got) != 3 || got[2] != (FirstQuestion{Kind: FirstQuestionPlace, Text: "Где впервые упоминается Маяк?"}) {
		t.Fatalf("an item and a faction of three chapters = %+v", got)
	}
	document.Nodes, document.Links = document.Nodes[:4], document.Links[:3]
	if got := FirstQuestions(document); len(got) != 2 {
		t.Fatalf("no place, item or faction = %+v", got)
	}
}

// FQ1.2, FQ1.4: without a hero there are no questions.
func TestWithoutAHeroThereAreNoQuestions(t *testing.T) {
	document := firstMap()
	document.Hero = nil
	if got := FirstQuestions(document); got == nil || len(got) != 0 {
		t.Fatalf("no hero = %#v, want an empty list", got)
	}
	empty := &MapDocument{Nodes: []MapDocumentNode{}, Links: []MapDocumentLink{}}
	if got := FirstQuestions(empty); got == nil || len(got) != 0 {
		t.Fatalf("no map = %#v, want an empty list", got)
	}
}

// Invariant «нет имени, которое автор с карты убрал»: the questions are asked of the document after the decisions of the author
// (MapDocument.Hide) — a neighbor taken off gives way to the next, a link taken off too, and the hero taken off leaves no questions.
func TestWhatTheAuthorTookOffTheMapIsNotAskedOf(t *testing.T) {
	document := firstMap()
	document.Hide(NewMapHidden([]MapHiddenItem{{ID: "1", NodeKey: "захар плотников"}, {ID: "2", NodeKey: "маяк"}}))
	got := questionTexts(FirstQuestions(document))
	if len(got) != 3 || got[1] != "pair: Тихон Ремезов и Ульяна: что их связывает?" || got[2] != "place: Где впервые упоминается Слобода и что там происходит?" {
		t.Fatalf("after Захар and Маяк are taken off = %q", got)
	}
	document = firstMap()
	document.Hide(NewMapHidden([]MapHiddenItem{{ID: "3", NodeKey: "тихон ремезов", OtherKey: "захар плотников"}}))
	if got := questionTexts(FirstQuestions(document)); got[1] != "pair: Тихон Ремезов и Ульяна: что их связывает?" {
		t.Fatalf("after «Это не связь» = %q", got)
	}
	document = firstMap()
	document.Hide(NewMapHidden([]MapHiddenItem{{ID: "4", NodeKey: "тихон ремезов"}}))
	if got := FirstQuestions(document); len(got) != 0 {
		t.Fatalf("after the hero is taken off = %+v", got)
	}
}

// FQ1.3: a record of the author the book does not know is asked of neither as a neighbor nor as a place.
func TestARecordOfTheAuthorIsNotAskedOf(t *testing.T) {
	document := firstMap()
	document.Nodes[6].Category = "character"
	document.Links = []MapDocumentLink{{Source: 0, Target: 6, Material: true}}
	if got := questionTexts(FirstQuestions(document)); len(got) != 2 || got[1] != "place: Где впервые упоминается Маяк и что там происходит?" {
		t.Fatalf("a record as the only neighbor = %q", got)
	}
	document = firstMap()
	document.Nodes[6].Chapters = []int{0, 1, 2, 3, 4, 5, 6}
	if got := FirstQuestions(document); got[2].Text != "Где впервые упоминается Маяк и что там происходит?" {
		t.Fatalf("a record of more chapters than any place = %+v", got)
	}
}

// FQ1.3: a name stands as the map has it — with a hyphen, in Latin letters, of several words — once, without quotes or brackets.
func TestNamesStandAsTheMapHasThem(t *testing.T) {
	document := firstMap()
	document.Nodes[0].Name = "Жан-Поль де Ла Тур"
	document.Nodes[1].Name = "Mr. Astley"
	document.Nodes[4].Name = "Северная пристань Нового Света"
	want := []string{
		"hero: Жан-Поль де Ла Тур: кто это и что важного происходит с этим персонажем?",
		"pair: Жан-Поль де Ла Тур и Mr. Astley: что их связывает?",
		"place: Где впервые упоминается Северная пристань Нового Света и что там происходит?",
	}
	if got := questionTexts(FirstQuestions(document)); !reflect.DeepEqual(got, want) {
		t.Fatalf("questions = %q\nwant %q", got, want)
	}
}
