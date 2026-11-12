package sagamemory

import "testing"

// namedSaga is a glossary where Варвара is also Варя and the keeper of the lighthouse by the name registry.
func namedSaga() *Glossary {
	return BuildGlossary(map[int]*Card{
		0: {
			Characters: []Mention{{Name: "Варвара", P: 1}, {Name: "Гриша", P: 2}, {Name: "Ян", P: 3}},
			Entities:   []Mention{{Name: "Белый порт", Type: "город", P: 4}},
		},
		1: {Characters: []Mention{{Name: "Варя", P: 1}}},
	}, &NameRegistry{Persons: []NamePerson{{Name: "варвара", Names: []string{"варя"}, Roles: []string{"хозяйка маяка"}}}})
}

// specs/2026-11-11-chat-corrections.md CR1.2: the author's words are about a hero when the name they are about is one of
// his names by the glossary and the name registry, or one of his names stands in their text as whole words in any case;
// words about another hero are not.
func TestTheAuthorsWordsAreAboutAHeroUnderAnyOfHisNames(t *testing.T) {
	subject := NamedNames("Варвара").Subject(namedSaga())
	for _, item := range []struct {
		name, text string
		want       bool
	}{
		{"Варя", "Лампу остановила она, а не Гриша.", true},
		{"", "Лампу остановила Варвара, а не Гриша.", true},
		{"", "Гриша отдал лампу Варваре.", true},
		{"", "Лампу Варе отдал Гриша.", true},
		{"хозяйка маяка", "Ей двадцать лет.", true},
		{"Варвара Петровна", "Ей двадцать лет.", true},
		{"Гриша", "Гриша старше брата на год.", false},
		{"", "Засада у Белого порта.", false},
	} {
		if got := subject.About(item.name, item.text); got != item.want {
			t.Errorf("About(%q, %q) = %v, want %v", item.name, item.text, got, item.want)
		}
	}
	unknown := NamedNames("Зоя").Subject(namedSaga())
	if !unknown.About("", "Зоя появится в пятом томе.") || !unknown.About("Зоя", "Появится в пятом томе.") || unknown.About("Гриша", "Ему двадцать.") {
		t.Error("a name the glossary does not know is named alone")
	}
	if short := NamedNames("Ян").Subject(namedSaga()); !short.About("Ян", "Он старше всех.") || short.About("", "Янтарь в кармане.") {
		t.Error("a short name is the name of the record, not a part of another word")
	}
	if NamedNames("Варвара").Subject(nil).About("Варя", "Ей двадцать лет.") {
		t.Error("without a glossary no other name is known")
	}
}

// CR1.2: for a search what is named is the words of its queries: a word of at least four letters that is not a function word,
// and every name of an entry the glossary knows by a query or by one of its words; the name a record is about stands in a
// query.
func TestTheWordsOfASearchNameWhatTheyAsk(t *testing.T) {
	glossary := namedSaga()
	words := NamedWords("кто остановил лампу").Subject(glossary)
	if !words.About("", "Лампа погасла у Гриши.") || !words.About("", "Остановила её Варвара.") {
		t.Error("a word of a query names the record that has it in any case")
	}
	if words.About("", "Кто такой Гриша, решу позже.") {
		t.Error("a short word of a query names nothing")
	}
	if !NamedWords("Варя").Subject(glossary).About("Варвара", "Ей двадцать лет.") {
		t.Error("a word the glossary knows as a name names every name of its entry")
	}
	if !NamedWords("Ян").Subject(glossary).About("Ян", "Он старше всех.") {
		t.Error("a short name of the glossary is named")
	}
	if !NamedWords("Белый порт сгорел").Subject(glossary).About("Белый порт", "Его отстроят к пятому тому.") {
		t.Error("the name of a record standing in a query is named")
	}
	if !NamedWords("и в на", "что это").Subject(glossary).Empty() || NamedWords("лампу").Subject(glossary).Empty() {
		t.Error("function words name nothing")
	}
	both := NamedNames("Гриша").And(NamedWords("лампа")).Subject(glossary)
	if !both.About("Гриша", "Ему двадцать.") || !both.About("", "Лампу разбили.") || both.About("Варвара", "Ей двадцать лет.") {
		t.Error("what two parts of a call name is named")
	}
}
