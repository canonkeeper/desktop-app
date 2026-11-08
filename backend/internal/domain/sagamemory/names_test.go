package sagamemory

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// heroCards reproduce the split measured on the saga: «Ворон» is given by four differently written
// names of one hero, which the rule of descriptions takes for a description, and a thing has the
// same name as the hero.
func heroCards() map[int]*Card {
	return map[int]*Card{
		0: {Entities: []Mention{{Name: "Ворон", Type: "птица", Note: "Птица с герба"}}},
		1: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон"}, Note: "Главный герой"}}, Facts: []Fact{{Subject: "Первый король", Attribute: "титул", Value: "король"}}},
		2: {Characters: []Mention{{Name: "Мирон Сватов", Aliases: []string{"Ворон"}}}},
		3: {Characters: []Mention{{Name: "Белый Ворон", Aliases: []string{"Ворон"}}}},
		4: {Characters: []Mention{{Name: "Мирон Ворон", Aliases: []string{"Ворон"}}}},
		5: {Characters: []Mention{{Name: "Ворон", Note: "Глава клана"}, {Name: "Любава", Aliases: []string{"Мирон"}}}},
	}
}

func heroRegistry() *NameRegistry {
	return &NameRegistry{Persons: []NamePerson{
		{Name: "мирон", Names: []string{"мирон", "ворон", "белый ворон", "мирон сватов", "мирон ворон"}, Roles: []string{"первый король"}},
		{Name: "любава", Names: []string{"любава"}},
	}}
}

// R5.4: everything said under any name of the hero reaches one entry, the thing with his name too.
func TestRegistryBringsTheSplitHeroToOneEntry(t *testing.T) {
	glossary := BuildGlossary(heroCards(), heroRegistry())
	hero, ok := glossary.Lookup("Ворон")
	if !ok || hero.Name != "Мирон" || hero.Type != characterType {
		t.Fatalf("Lookup(Ворон) = %+v, %v; want the hero", hero, ok)
	}
	for _, name := range []string{"мирон", "белый ворон", "мирон сватов", "мирон ворон", "первый король"} {
		if found, ok := glossary.Lookup(name); !ok || found != hero {
			t.Fatalf("Lookup(%s) = %+v, %v; want the hero", name, found, ok)
		}
	}
	if !reflect.DeepEqual(hero.Chapters, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("chapters = %v, want all six", hero.Chapters)
	}
	if hero.Note != "Главный герой" {
		t.Fatalf("note = %q; a thing's note does not describe the hero", hero.Note)
	}
	if len(glossary.Entries) != 2 {
		t.Fatalf("entries = %d (%+v), want the hero and Любава", len(glossary.Entries), glossary.Entries)
	}
	// An alias belonging to another person is dropped: Любава does not become Мирон.
	lyubava, ok := glossary.Lookup("любава")
	if !ok || lyubava == hero || len(lyubava.Aliases) != 0 {
		t.Fatalf("Любава = %+v, %v; want her own entry without aliases", lyubava, ok)
	}
	// Without the registry the same cards keep the split.
	if split := BuildGlossary(heroCards(), nil); len(split.Entries) < 3 {
		t.Fatalf("without a registry = %d entries, want the old split", len(split.Entries))
	}
}

// R5.4: the entry shows the spellings of the cards, not the lowercase of the registry.
func TestRegistryShowsTheMostFrequentSpelling(t *testing.T) {
	glossary := BuildGlossary(map[int]*Card{
		0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"ВОРон"}}}},
		1: {Characters: []Mention{{Name: "ВОРон"}}},
		2: {Characters: []Mention{{Name: "Ворон"}}},
		3: {Characters: []Mention{{Name: "ВОРон"}}},
	}, &NameRegistry{Persons: []NamePerson{{Name: "ворон", Names: []string{"мирон"}}}})
	hero, ok := glossary.Lookup("мирон")
	if !ok || hero.Name != "ВОРон" || !reflect.DeepEqual(hero.Aliases, []string{"Мирон"}) {
		t.Fatalf("hero = %+v, %v; want the frequent spelling as the name and Мирон as an alias", hero, ok)
	}
	if EntryKey(hero) != "ворон" {
		t.Fatalf("EntryKey = %q", EntryKey(hero))
	}
}

// R5.4: a role leads facts, lookups and Find to the hero, but it is no name: not shown, not a link.
func TestRegistryRoleReachesFactsButIsNoAlias(t *testing.T) {
	cards := heroCards()
	set := NewCardSet(cards, heroRegistry())
	hero, _ := set.Glossary.Lookup("Мирон")
	if !reflect.DeepEqual(hero.Roles, []string{"первый король"}) {
		t.Fatalf("roles = %v", hero.Roles)
	}
	for _, alias := range hero.Aliases {
		if GlossaryKey(alias) == "первый король" {
			t.Fatalf("aliases = %v: a role is not a name", hero.Aliases)
		}
	}
	profile := BuildProfile(hero, []ChapterFact{{Chapter: 1, Fact: cards[1].Facts[0]}})
	if profile.Facts != 1 {
		t.Fatalf("profile = %+v, want the fact under the role", profile)
	}
	if found := set.Glossary.Find("первый король", nil); len(found) != 1 || found[0] != hero {
		t.Fatalf("Find(role) = %+v", found)
	}
	if words := strings.Join(set.LinkWords(), " "); strings.Contains(words, "первыи") || strings.Contains(words, "первый") {
		t.Fatalf("link words = %q, a role makes no links", words)
	}
	if set.Names == nil {
		t.Fatal("the card set keeps the registry for scoped rebuilds")
	}
}

// R5.4: a shared name is never merged: it is nobody's alias, and its aliases pull it nowhere.
func TestRegistrySharedNameDoesNotMerge(t *testing.T) {
	glossary := BuildGlossary(map[int]*Card{
		0: {Characters: []Mention{{Name: "Король", Aliases: []string{"Мирон"}}, {Name: "Мирон", Aliases: []string{"Король"}}}},
		1: {Characters: []Mention{{Name: "Король", Aliases: []string{"Борислав"}}, {Name: "Борислав", Aliases: []string{"Король"}}}},
	}, &NameRegistry{
		Persons: []NamePerson{{Name: "мирон", Names: []string{"король"}}},
		Shared:  []string{"король"},
	})
	if len(glossary.Entries) != 3 {
		t.Fatalf("entries = %+v, want Мирон, Король and Борислав apart", glossary.Entries)
	}
	king, ok := glossary.Lookup("король")
	if !ok || king.Name != "Король" || len(king.Aliases) != 0 || !reflect.DeepEqual(king.Chapters, []int{0, 1}) {
		t.Fatalf("Король = %+v, %v", king, ok)
	}
	if hero, _ := glossary.Lookup("мирон"); hero == king || len(hero.Aliases) != 0 {
		t.Fatalf("Мирон = %+v: a shared name is nobody's alias", hero)
	}
	if other, _ := glossary.Lookup("борислав"); other == king || len(other.Aliases) != 0 {
		t.Fatalf("Борислав = %+v: a shared name is nobody's alias", other)
	}
}

// R5.4: a shared name that is a person's main name stays that person's.
func TestRegistryMainNameWinsOverShared(t *testing.T) {
	glossary := BuildGlossary(map[int]*Card{
		0: {Characters: []Mention{{Name: "Ворон"}, {Name: "Белый Ворон"}}},
	}, &NameRegistry{
		Persons: []NamePerson{{Name: "ворон", Names: []string{"белый ворон"}}},
		Shared:  []string{"ворон"},
	})
	if len(glossary.Entries) != 1 {
		t.Fatalf("entries = %+v, want one", glossary.Entries)
	}
}

// R5.4: a wrong pair cuts the alias of that name only.
func TestRegistryWrongPairCutsTheAlias(t *testing.T) {
	cards := map[int]*Card{
		0: {Characters: []Mention{{Name: "Тихон", Aliases: []string{"Седой Волк"}}}},
		1: {Characters: []Mention{{Name: "Ярина", Aliases: []string{"Седой Волк"}}}},
		2: {Characters: []Mention{{Name: "Седой Волк"}}},
	}
	glossary := BuildGlossary(cards, &NameRegistry{Wrong: []WrongAlias{{Name: "тихон", Alias: "седой волк"}}})
	tikhon, ok := glossary.Lookup("тихон")
	if !ok || len(tikhon.Aliases) != 0 {
		t.Fatalf("Тихон = %+v, %v; want the wrong alias cut", tikhon, ok)
	}
	yarina, ok := glossary.Lookup("седой волк")
	if !ok || yarina.Name != "Ярина" {
		t.Fatalf("Седой Волк = %+v, %v; want Ярина", yarina, ok)
	}
}

// R5.4: a wrong pair also cuts an alias of a registered person's mention.
func TestRegistryWrongPairCutsAnAliasOfAPerson(t *testing.T) {
	glossary := BuildGlossary(map[int]*Card{
		0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Посланник", "Первый"}}}},
	}, &NameRegistry{
		Persons: []NamePerson{{Name: "мирон", Names: []string{"ворон"}}},
		Wrong:   []WrongAlias{{Name: "мирон", Alias: "посланник"}},
	})
	hero, _ := glossary.Lookup("мирон")
	if !reflect.DeepEqual(hero.Aliases, []string{"Первый"}) {
		t.Fatalf("aliases = %v, want the name unknown to the registry kept and the wrong one cut", hero.Aliases)
	}
}

// R5.4: descriptions: several names of one person count as one, and an alias the registry knows is
// never a description.
func TestRegistryCountsAPersonsNamesAsOneForDescriptions(t *testing.T) {
	cards := map[int]*Card{
		0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Вор"}}}},
		1: {Characters: []Mention{{Name: "Мирон Сватов", Aliases: []string{"Вор"}}}},
		2: {Characters: []Mention{{Name: "Ворон", Aliases: []string{"Вор"}}}},
		3: {Characters: []Mention{{Name: "Белый Ворон", Aliases: []string{"Вор"}}}},
		4: {Characters: []Mention{{Name: "Гарри", Aliases: []string{"Гарольд"}}}},
		5: {Characters: []Mention{{Name: "Вор"}}},
	}
	registry := &NameRegistry{Persons: []NamePerson{{Name: "мирон", Names: []string{"мирон сватов", "ворон", "белый ворон"}}}}
	hero, _ := BuildGlossary(cards, registry).Lookup("вор")
	if hero == nil || hero.Name != "Мирон" {
		t.Fatalf("Вор = %+v; four names of one person make no description", hero)
	}
	known := &NameRegistry{Persons: []NamePerson{{Name: "гарри", Names: []string{"вор"}}}}
	if !descriptionAliases(cards, newNameIndex(nil))["вор"] || descriptionAliases(cards, newNameIndex(known))["вор"] {
		t.Fatal("an alias the registry knows is never a description")
	}
}

// R5.4: a name unknown to the registry merges by its aliases as before, also into a person.
func TestRegistryUnknownNameFollowsTheRules(t *testing.T) {
	glossary := BuildGlossary(map[int]*Card{
		0: {Characters: []Mention{{Name: "Незнакомец", Aliases: []string{"Мирон"}}}},
		1: {Characters: []Mention{{Name: "Мирон"}, {Name: "Марк", Aliases: []string{"Маркус"}}}},
		2: {Characters: []Mention{{Name: "Маркус"}}},
	}, &NameRegistry{Persons: []NamePerson{{Name: "мирон", Names: []string{"ворон"}}}})
	hero, _ := glossary.Lookup("незнакомец")
	if hero == nil || hero.Name != "Мирон" {
		t.Fatalf("Незнакомец = %+v, want merged with the person by his alias", hero)
	}
	mark, _ := glossary.Lookup("маркус")
	if mark == nil || mark.Name != "Марк" || !reflect.DeepEqual(mark.Chapters, []int{1, 2}) {
		t.Fatalf("Маркус = %+v", mark)
	}
}

// A book-scoped glossary shows only what the cards of the scope write: a later name of the hero
// from the registry is no alias there.
func TestRegistryShowsOnlyNamesOfTheGivenCards(t *testing.T) {
	cards := map[int]*Card{0: {Characters: []Mention{{Name: "Мирон"}}}}
	hero, _ := BuildGlossary(cards, heroRegistry()).Lookup("мирон")
	if hero == nil || hero.Name != "Мирон" || len(hero.Aliases) != 0 {
		t.Fatalf("hero = %+v, want no alias the cards do not write", hero)
	}
	if !reflect.DeepEqual(hero.Keys(), []string{"мирон", "первый король", "ворон", "белый ворон", "мирон сватов", "мирон ворон"}) {
		t.Fatalf("keys = %v, want facts under any name still reaching him", hero.Keys())
	}
}

// R5.5: a character is found before a thing of the same name, with or without a registry, and the
// thing keeps a key of its own.
func TestLookupPrefersTheCharacterOverAThing(t *testing.T) {
	glossary := BuildGlossary(map[int]*Card{
		0: {Entities: []Mention{{Name: "Ворон", Type: "птица"}}},
		1: {Characters: []Mention{{Name: "Ворон"}}},
	}, nil)
	found, ok := glossary.Lookup("ворон")
	if !ok || found.Type != characterType {
		t.Fatalf("Lookup = %+v, %v; want the character", found, ok)
	}
	var bird *GlossaryEntry
	for _, entry := range glossary.Entries {
		if entry.Type == "птица" {
			bird = entry
		}
	}
	if byKey, ok := glossary.Lookup(EntryKey(bird)); !ok || byKey != bird {
		t.Fatalf("Lookup(EntryKey(bird)) = %+v, %v; a link to the thing stays the thing", byKey, ok)
	}
	if byKey, ok := glossary.Lookup(EntryKey(found)); !ok || byKey != found {
		t.Fatalf("Lookup(EntryKey(character)) = %+v, %v", byKey, ok)
	}
}

func TestParseNameRegistryIsTolerant(t *testing.T) {
	registry, err := ParseNameRegistry("Вот реестр:\n" + `{"persons":[
		{"name":"Миро́н ","names":["мирон","Ворон","","ворон"],"roles":["Первый король","ворон"]},
		{"name":"","names":["любава","любаша"]},
		{"name":" ","names":[" "]}
	],"shared":[{"name":"Король","who":["мирон","борислав"]},"Император",{"name":""}],
	"wrong":[{"name":"Тихон","alias":"Седой Волк"},{"name":"","alias":"x"},{"name":"а","alias":"а"}]}` + "\nГотово.")
	if err != nil {
		t.Fatal(err)
	}
	want := NameRegistry{
		Persons: []NamePerson{
			{Name: "мирон", Names: []string{"ворон"}, Roles: []string{"первый король"}},
			{Name: "любава", Names: []string{"любаша"}},
		},
		Shared: []string{"король", "император"},
		Wrong:  []WrongAlias{{Name: "тихон", Alias: "седой волк"}},
	}
	if !reflect.DeepEqual(registry, want) {
		t.Fatalf("registry = %+v, want %+v", registry, want)
	}
	for _, output := range []string{"нет JSON", `{"persons":`, `{"shared":[]}`} {
		if _, err := ParseNameRegistry(output); !errors.Is(err, ErrModelOutput) {
			t.Fatalf("ParseNameRegistry(%q) = %v, want ErrModelOutput", output, err)
		}
	}
	if empty, err := ParseNameRegistry(`{"persons":[]}`); err != nil || len(empty.Persons) != 0 {
		t.Fatalf("a registry without persons = %+v, %v; a book may have none", empty, err)
	}
}

// The list is the input measured in the lab, line by line.
func TestBuildNameListFormat(t *testing.T) {
	long := strings.Repeat("ё", 200)
	subjectFacts := make([]Fact, 20)
	for i := range subjectFacts {
		subjectFacts[i] = Fact{Subject: "Владыка", Attribute: "титул", Value: "x"}
	}
	list, _ := BuildNameList(map[int]*Card{
		2: {
			Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон", "ВОРон", "Ворон"}, Note: "первая"}},
			Entities:   []Mention{{Name: "Клинок", Type: "оружие", Note: "меч"}, {Name: "Камень", Type: "предмет"}},
			Facts:      append([]Fact{{Subject: "Мирон", Attribute: "возраст", Value: "24"}}, subjectFacts...),
			Knows:      []Knowledge{{Who: "Клинок", What: "x"}, {Who: "Тень", What: "y"}},
		},
		5: {Characters: []Mention{{Name: "мирон", Note: "вторая"}}},
		7: {Characters: []Mention{{Name: "Мирон", Note: "третья"}}},
		9: {Characters: []Mention{{Name: "Ёж", Note: long}}, Entities: []Mention{{Name: "Мирон", Type: "птица", Note: "четвёртая"}}},
	})
	want := []string{
		"владыка тип:нет | глав 0 (-–-), фактов 20 | также:  | ",
		"мирон | глав 4 (2–9), фактов 1 | также: Ворон, ВОРон | первая / третья / четвёртая",
		"клинок тип:оружие | глав 1 (2–2), фактов 0 | также:  | меч",
		"тень тип:нет | глав 0 (-–-), фактов 0 | также:  | ",
		"еж | глав 1 (9–9), фактов 0 | также:  | " + strings.Repeat("ё", 110),
	}
	if got := strings.Split(list, "\n"); !reflect.DeepEqual(got, sortByWeightForTest(want)) {
		t.Fatalf("list:\n%s\nwant:\n%s", list, strings.Join(want, "\n"))
	}
}

func sortByWeightForTest(lines []string) []string {
	// владыка 20, мирон 1+4, еж 1, клинок 1+1, тень 1: the heaviest first, then by key.
	return []string{lines[0], lines[1], lines[2], lines[4], lines[3]}
}

func TestBuildNameListKeepsTheHeaviestWithinTheBound(t *testing.T) {
	characters := make([]Mention, maxNameLines+1)
	for i := range characters {
		characters[i] = Mention{Name: "Имя " + strings.Repeat("я", i%7) + string(rune(0x4e00+i))}
	}
	list, _ := BuildNameList(map[int]*Card{0: {Characters: characters}})
	if lines := strings.Count(list, "\n") + 1; lines != maxNameLines {
		t.Fatalf("lines = %d, want %d", lines, maxNameLines)
	}
}

// R5.2: only the names and the other names count; notes and counts do not.
func TestNameListDigestIgnoresNotesAndCounts(t *testing.T) {
	cards := func() map[int]*Card {
		return map[int]*Card{
			0: {Characters: []Mention{{Name: "Мирон", Aliases: []string{"Ворон"}, Note: "Заметка"}}},
		}
	}
	_, digest := BuildNameList(cards())

	changed := cards()
	changed[0].Characters[0].Note = "Совсем другая заметка"
	changed[0].Facts = []Fact{{Subject: "Мирон", Attribute: "возраст", Value: "24"}}
	changed[3] = &Card{Characters: []Mention{{Name: "мирон", Aliases: []string{"ВОРОН"}}}}
	if _, other := BuildNameList(changed); other != digest {
		t.Fatal("a change without new names changes the digest")
	}

	newAlias := cards()
	newAlias[0].Characters[0].Aliases = []string{"Ворон", "Белый Ворон"}
	if _, other := BuildNameList(newAlias); other == digest {
		t.Fatal("a new other name keeps the digest")
	}
	newName := cards()
	newName[1] = &Card{Characters: []Mention{{Name: "Любава"}}}
	if _, other := BuildNameList(newName); other == digest {
		t.Fatal("a new name keeps the digest")
	}
	if len(digest) != 64 {
		t.Fatalf("digest = %q", digest)
	}
}

func TestNameInstructionsAreGeneric(t *testing.T) {
	if !strings.HasPrefix(NameInstructions, "Дан список имён из карточек глав") || !strings.Contains(NameInstructions, `"wrong":[{"name":"","alias":""}]`) {
		t.Fatalf("instructions = %q", NameInstructions)
	}
	if NameInstructionsVersion < 1 {
		t.Fatal("a version is required")
	}
}
