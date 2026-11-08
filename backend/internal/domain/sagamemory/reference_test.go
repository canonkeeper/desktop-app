package sagamemory

import (
	"reflect"
	"testing"
)

// referenceSaga is two books of a LitRPG saga with cards for three of four chapters; the
// stems mimic russian_stem for the words the cards use.
func referenceSaga(t *testing.T) (*Index, *CardSet) {
	version := NewVersion("project", "draft", "fp", []Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 5. Ночь на заставе",
			Paragraphs: []Paragraph{{Text: "Мирон изучил взлом."}}},
		{ID: "c2", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 2, Title: "Глава 137. Лёд у переправы",
			Paragraphs: []Paragraph{{Text: "Навык взлома Мирона вырос."}}},
		{ID: "c3", BookID: "b2", BookOrder: 2, BookTitle: "Том 2", Order: 1, Title: "Глава 201. Дорога на перевал",
			Paragraphs: []Paragraph{{Text: "Ворон купил книги."}}},
		{ID: "c4", BookID: "b2", BookOrder: 2, BookTitle: "Том 2", Order: 2, Title: "Глава 202. Без карточки",
			Paragraphs: []Paragraph{{Text: "Тишина."}}},
	})
	index := wholeIndex(t, version, map[string]string{
		"взлом": "взлом", "взлома": "взлом", "взломом": "взлом", "вломился": "вломил", "мирон": "мирон", "мирона": "мирон",
		"мирону": "мирон", "ворон": "ворон", "ворона": "ворон", "навык": "навык", "навыки": "навык", "кинжал": "кинжал",
		"кинжалом": "кинжал", "третий": "трет", "третьего": "трет", "этаж": "этаж", "этажа": "этаж", "в": "", "и": "",
	})
	cards := map[int]*Card{
		0: {Summary: "Мирон учится вскрывать замки и знакомится с гильдией воров.", Time: []string{"Утром (¶1)", "Когда Мирон вошёл (¶2)"},
			Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон"}, Note: "главный герой", P: 1}},
			Entities:   []Mention{{Name: "Взлом", Type: "навык", Note: "вскрытие замков", P: 2}},
			Changes:    []Change{{Subject: "Мирон", Change: "Изучает навык «Взлом».", P: 2}}},
		1: {Summary: "Тайник контрабандистов и рост навыков.", Time: []string{"Почти сутки прошли с начала оттепели (¶19)", "Шорох был в Карагоре (¶45, ¶55)"},
			Characters: []Mention{{Name: "Мирон", P: 1}},
			Entities:   []Mention{{Name: "Взлом", Type: "навык", P: 3}, {Name: "Кинжал", Type: "оружие", P: 4}},
			Changes: []Change{
				{Subject: "Навык «Взлом» Мирона", Change: "Немного повысился благодаря тайнику.", P: 3},
				{Subject: "Вор", Change: "Вломился в дом.", P: 5},
				{Subject: "Мирон", Change: "Получил кинжал.", P: 4},
			}},
		2: {Summary: "Ворон покупает книги и поднимается на третий этаж.", Time: []string{"Через неделю (¶2)"},
			Characters: []Mention{{Name: "Ворон", P: 1}},
			Entities:   []Mention{{Name: "Взлом", Type: "навык", P: 2}, {Name: "Третий этаж", Type: "этаж", P: 6}},
			Changes: []Change{
				{Subject: "навыки Мирона", Change: "Купил книги для улучшения «Взлома» до ранга мастер.", P: 2},
				{Subject: "Ворон", Change: "Поднялся до третьего этажа.", P: 6},
			}},
	}
	return index, NewCardSet(cards, nil)
}

func entry(t *testing.T, set *CardSet, name string) *GlossaryEntry {
	t.Helper()
	found, ok := set.Glossary.Lookup(name)
	if !ok {
		t.Fatalf("glossary has no %q", name)
	}
	return found
}

func TestLinksNameEntriesByWordStems(t *testing.T) {
	index, set := referenceSaga(t)
	var named [][]string
	for _, link := range set.Links(index) {
		var names []string
		for _, linked := range link.Entries {
			names = append(names, linked.Name)
		}
		named = append(named, names)
	}
	want := [][]string{
		{"Взлом", "Мирон"},
		{"Взлом", "Мирон"},
		nil, // «Вломился» is not the skill «Взлом»
		{"Кинжал", "Мирон"},
		{"Взлом", "Мирон"},
		{"Мирон", "Третий этаж"}, // «Ворон» is an alias of «Мирон»; «третьего этажа» is the floor
	}
	if !reflect.DeepEqual(named, want) {
		t.Fatalf("links = %v, want %v", named, want)
	}
}

func TestEntryChangesAndMentions(t *testing.T) {
	index, set := referenceSaga(t)
	skill := entry(t, set, "взлом")
	changes := set.EntryChanges(index, skill, nil, -1)
	if len(changes) != 3 || changes[0].Chapter != 0 || changes[2].Chapter != 2 {
		t.Fatalf("changes = %+v, want three in story order", changes)
	}
	if upTo := set.EntryChanges(index, skill, entry(t, set, "Мирон"), 1); len(upTo) != 2 {
		t.Fatalf("changes with Мирон up to chapter 137 = %d, want 2", len(upTo))
	}
	var mentions []string
	for _, mention := range set.Mentions(index.Version, skill) {
		mentions = append(mentions, mention.Chapter.Key)
		if mention.P == 0 {
			t.Fatalf("mention in %s has no paragraph", mention.Chapter.Key)
		}
	}
	if !reflect.DeepEqual(mentions, []string{"5", "137", "201"}) {
		t.Fatalf("mentions = %v", mentions)
	}
	if got := set.Glossary.TypeCounts(); !reflect.DeepEqual(got, []TypeCount{{"навык", 1}, {"оружие", 1}, {"персонаж", 1}, {"этаж", 1}}) {
		t.Fatalf("type counts = %v", got)
	}
}

// TestGlossaryKeepsDescriptionsAndKindsApart is the identity rule of BuildGlossary on the
// shapes TEST cards have: shared descriptions, lowercase titles, type-prefixed skill names,
// things named after their owner and nicknames that are also skill names.
func TestGlossaryKeepsDescriptionsAndKindsApart(t *testing.T) {
	set := NewCardSet(map[int]*Card{
		0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон", "мужчина", "юноша"}}, {Name: "Ефим Полозов", Aliases: []string{"мужчина"}}},
			Entities: []Mention{{Name: "Навыки", Type: "навык"}, {Name: "Взлом", Type: "навык"}}},
		1: {Characters: []Mention{{Name: "Эсма", Aliases: []string{"юноша", "Его Высочество"}}, {Name: "Гаррон", Aliases: []string{"мужчина"}}},
			Entities: []Mention{{Name: "Навык «Взлом»", Type: "навык"}, {Name: "Этаж Ворона", Type: "место", Aliases: []string{"Ворон"}}}},
		2: {Characters: []Mention{{Name: "Дорин", Aliases: []string{"мужчина"}}, {Name: "Пак", Aliases: []string{"Ужас"}}},
			Entities: []Mention{{Name: "Ужас", Type: "навык"}}},
	}, nil)
	will := entry(t, set, "Мирон")
	if !reflect.DeepEqual(will.Aliases, []string{"Ворон"}) || len(will.Chapters) != 1 {
		t.Fatalf("Мирон = %+v, want only the name-like alias «Ворон»", will)
	}
	for _, name := range []string{"Ефим Полозов", "Эсма", "Гаррон", "Дорин"} {
		if got := entry(t, set, name); got.Name != name {
			t.Fatalf("%s merged into %s through a description", name, got.Name)
		}
	}
	if _, ok := set.Glossary.Lookup("мужчина"); ok {
		t.Fatal("the description «мужчина» names an entry")
	}
	if _, ok := set.Glossary.Lookup("Навыки"); ok {
		t.Fatal("the category «Навыки» became an entry")
	}
	if skill := entry(t, set, "Навык «Взлом»"); skill.Name != "Взлом" || !reflect.DeepEqual(skill.Chapters, []int{0, 1}) {
		t.Fatalf("Навык «Взлом» = %+v, want the skill «Взлом» of chapters 0 and 1", skill)
	}
	if floor := entry(t, set, "Этаж Ворона"); floor.Name != "Этаж Ворона" || floor.Type != "место" {
		t.Fatalf("Этаж Ворона = %+v, want its own place, not the hero", floor)
	}
	var fear *GlossaryEntry
	for _, candidate := range set.Glossary.Entries {
		if candidate.Name == "Ужас" {
			fear = candidate
		}
	}
	pak := entry(t, set, "Пак")
	if fear == nil || fear.Type != "навык" || EntryKey(fear) == EntryKey(pak) || entry(t, set, EntryKey(fear)) != fear {
		t.Fatalf("skill «Ужас» = %+v, want its own entry with its own key beside the nickname of Пак", fear)
	}
}

// TestLinksNeedOwnWordsAndTheChapterCard: a name needs a word of its own, the entry has to be in
// the card of the chapter, a longer name covers a shorter one of another thing, and the hero is
// related to a thing only by a change about one of them.
func TestLinksNeedOwnWordsAndTheChapterCard(t *testing.T) {
	version := NewVersion("project", "draft", "fp", []Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 1", Paragraphs: []Paragraph{{Text: "x"}}},
		{ID: "c2", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 2, Title: "Глава 2", Paragraphs: []Paragraph{{Text: "x"}}},
	})
	index := wholeIndex(t, version, map[string]string{
		"мирон": "мирон", "ворон": "ворон", "ворона": "ворон", "повышает": "повыша", "навык": "навык", "и": "", "надевает": "надева",
		"маску": "маск", "маска": "маск", "я": "", "не": "", "молчание": "молчан", "молчания": "молчан", "молчит": "молч",
		"пока": "", "ждет": "ждет", "алтын": "алтын", "ир": "ир", "причина": "причин", "неизвестна": "неизвестн",
	})
	set := NewCardSet(map[int]*Card{
		0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон"}}, {Name: "Алтын-Ир"}},
			Entities: []Mention{{Name: "Навык «я не я»", Type: "навык"}, {Name: "маска", Type: "предмет"}, {Name: "маска Ворона", Type: "предмет"}, {Name: "Молчание", Type: "заклинание"}},
			Changes: []Change{
				{Subject: "Мирон", Change: "Повышает навык и надевает маску Ворона."},
				{Subject: "Алтын-Ир", Change: "Молчит, пока Ворон ждёт молчания."},
			}},
		1: {Characters: []Mention{{Name: "Мирон"}},
			Changes: []Change{{Subject: "Мирон", Change: "Причина молчания неизвестна."}}},
	}, nil)
	links := set.Links(index)
	names := func(entries []*GlossaryEntry) []string {
		var out []string
		for _, named := range entries {
			out = append(out, named.Name)
		}
		return out
	}
	if got := names(links[0].Entries); !reflect.DeepEqual(got, []string{"Мирон", "маска Ворона"}) {
		t.Fatalf("first change names %v, want the hero and «маска Ворона» without «маска» and «Навык «я не я»»", got)
	}
	if got := names(links[1].Entries); !reflect.DeepEqual(got, []string{"Алтын-Ир", "Мирон", "Молчание"}) {
		t.Fatalf("second change names %v", got)
	}
	if got := names(links[2].Entries); !reflect.DeepEqual(got, []string{"Мирон"}) {
		t.Fatalf("chapter 2 names %v, want no «Молчание»: its card does not mention it", got)
	}
	will, silence := entry(t, set, "Мирон"), entry(t, set, "Молчание")
	if links[1].Relates(will, silence) || !links[0].Relates(will, entry(t, set, "маска Ворона")) {
		t.Fatal("the hero is related to a thing only by a change about one of them")
	}
	// The changes of a pair on the map card (specs/2026-11-04-saga-map.md, R2.4): Алтын-Ир is the
	// subject of a change naming Ворон; the silence is nobody's subject.
	if pair := set.EntryChanges(index, entry(t, set, "Алтын-Ир"), will, -1); len(pair) != 1 || pair[0].Change.Subject != "Алтын-Ир" {
		t.Fatalf("changes of Мирон and Алтын-Ир = %+v", pair)
	}
	if pair := set.EntryChanges(index, silence, will, -1); len(pair) != 0 {
		t.Fatalf("changes of Мирон and the silence = %+v", pair)
	}
}

func TestContentsAndBooks(t *testing.T) {
	index, set := referenceSaga(t)
	books := index.Version.Books()
	if len(books) != 2 || books[1].First != 2 || books[1].Last != 3 || books[1].Title != "Том 2" {
		t.Fatalf("books = %+v", books)
	}
	contents := set.Contents(index.Version, books[1].First, books[1].Last, "", 200)
	if len(contents) != 2 || contents[0].Summary == "" || contents[1].HasCard {
		t.Fatalf("book 2 contents = %+v, want the second chapter without a card", contents)
	}
	found := set.Contents(index.Version, 0, 3, "гильди вор", 200)
	if len(found) != 1 || found[0].Chapter.Key != "5" {
		t.Fatalf("search = %+v, want the chapter whose summary has both word starts", found)
	}
	if coverage := set.Coverage(index.Version); coverage != (Coverage{Chapters: 4, WithCards: 3}) {
		t.Fatalf("coverage = %+v", coverage)
	}
}

