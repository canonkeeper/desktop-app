// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// exportWords is testdata/export-words.json: the words of the files, shared with the test of the page of the map.
type exportWords struct {
	Card struct {
		NoType, Also, First, Met     string
		ChapterForms                 [3]string
		Links, FromMaterials         string
		Together                     string
		EventForms                   [3]string
		BothChanged, Beside, NamedIn string
		Events, Changes, Material    string
		Plan, Record                 string
		FromObsidian, FromFile       string
	} `json:"card"`
	Categories map[string]string `json:"categories"`
	Kinds      map[string]string `json:"kinds"`
	Files      struct {
		Lead, Part, NoName, Index, Chapters, Deleted string
		Happens, Who, Where, Named, Paragraph        string
	} `json:"files"`
}

// OE2.3: the words of the reference of a node are the words of the card of the page, the words of the files those of Data —
// one table for this test and for the test of the page.
func TestTheWordsOfTheFilesAreTheTableTheTestOfThePageReads(t *testing.T) {
	data, err := os.ReadFile("testdata/export-words.json")
	if err != nil {
		t.Fatal(err)
	}
	var table exportWords
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatal(err)
	}
	card := table.Card
	pairs := map[string][2]string{
		"noType": {wordNoType, card.NoType}, "also": {wordAlso, card.Also}, "first": {wordFirst, card.First}, "met": {wordMet, card.Met},
		"links": {wordLinks, card.Links}, "fromMaterials": {wordFromMaterials, card.FromMaterials}, "together": {wordTogether, card.Together},
		"bothChanged": {wordBothChanged, card.BothChanged}, "beside": {wordBeside, card.Beside}, "namedIn": {wordNamedIn, card.NamedIn},
		"events": {wordEvents, card.Events}, "changes": {wordChanges, card.Changes}, "material": {wordMaterial, card.Material},
		"plan": {wordPlan, card.Plan}, "record": {wordRecord, card.Record}, "fromObsidian": {wordFromObsidian, card.FromObsidian},
		"fromFile": {wordFromFile, card.FromFile},
		"lead":     {wordLead, table.Files.Lead}, "part": {wordPart, table.Files.Part}, "noName": {wordNoName, table.Files.NoName},
		"index": {wordIndex, table.Files.Index}, "chapters": {wordChapters, table.Files.Chapters}, "deleted": {wordDeleted, table.Files.Deleted},
		"happens": {wordHappens, table.Files.Happens}, "who": {wordWho, table.Files.Who}, "where": {wordWhere, table.Files.Where},
		"named": {wordNamed, table.Files.Named}, "paragraph": {wordParagraph, table.Files.Paragraph},
	}
	for name, pair := range pairs {
		if pair[0] == "" || pair[0] != pair[1] {
			t.Errorf("%s: the server says %q, the table %q", name, pair[0], pair[1])
		}
	}
	if chapterForms != card.ChapterForms || eventForms != card.EventForms {
		t.Errorf("the forms differ from the table")
	}
	categories := map[string]string{}
	for category, word := range categoryWords {
		categories[string(category)] = word
	}
	if !reflect.DeepEqual(categories, table.Categories) || !reflect.DeepEqual(exportKindWords, table.Kinds) {
		t.Errorf("the categories or the kinds differ from the table")
	}
}

// exportFixture is a saga of the boards, «Северный предел»: two books, three chapters — the second without a fresh card —,
// a map of five nodes and a record of the author the book does not know; «Призрак» the author took off the map.
func exportFixture() ExportSaga {
	version := sagamemory.NewVersion("saga-1", "published", "fp", []sagamemory.Chapter{
		{ID: "ch-1", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 1, Title: "Глава 1. Застава"},
		{ID: "ch-2", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 2, Title: "Белый порт"},
		{ID: "ch-3", BookID: "book-2", BookOrder: 2, BookKeyNumber: 2, BookTitle: "Перевал", Order: 1, Title: "Глава 3. Перевал"},
	})
	ref := func(index int) sagamemory.MapChapterRef {
		chapter := version.Chapters[index]
		return sagamemory.MapChapterRef{Key: chapter.Key, Label: chapter.Place(), ChapterID: chapter.ID, BookID: chapter.BookID, Title: chapter.Title}
	}
	note := "дозорная у ворот"
	hero := sagamemory.MapCardDocument{
		Key: "мара", Name: "Мара", Type: "героиня", Aliases: []string{"Марка"}, Note: &note,
		First: &sagamemory.CardMention{Chapter: ref(0), Paragraph: 3}, Mentions: 2,
		Events:      []sagamemory.CardEvent{{Chapter: ref(0), Event: "Мара и Сава держат ворота", Paragraph: 5}},
		EventsTotal: 41,
		Changes:     []sagamemory.CardChange{{Chapter: ref(2), Subject: "Мара", Change: "стала капитаном", Paragraph: 15}}, ChangesTotal: 1,
		Links: []sagamemory.CardLink{
			{Key: "сава", Name: "Сава", Category: "character", Together: 2, Shared: []sagamemory.CardEvent{{Chapter: ref(0), Event: "Мара и Сава держат ворота", Paragraph: 5}}},
			{Key: "ледяной трон", Name: "Ледяной трон", Category: "item", FromMaterials: true},
			{Key: "призрак", Name: "Призрак", Category: "character", Changes: []sagamemory.CardChange{{Chapter: ref(1), Subject: "Призрак", Change: "исчез", Paragraph: 2}}},
		},
		Beside:  []sagamemory.CardNeighbor{{Key: "глеб", Name: "Глеб"}},
		NamedIn: []sagamemory.CardMaterialRef{{ID: "record-2", Name: "Ледяной трон", Kind: "item"}},
		Material: &sagamemory.CardMaterial{
			ID: "record-1", Name: "Мара", Kind: "character", Description: "Мара родом с севера.",
			Properties: []sagamemory.CardMaterialProperty{{Key: "Возраст", Value: "24"}}, SourceKind: "obsidian", SourceName: "Персонажи/Мара.md",
		},
	}
	throne := sagamemory.MapCardDocument{
		Key: "ледяной трон", Name: "Ледяной трон", Type: "из ваших материалов · ещё не в книге",
		Material: &sagamemory.CardMaterial{ID: "record-2", Name: "Ледяной трон", Kind: "item", Description: "Трон из льда.", SourceKind: "manual"},
	}
	nodes := []sagamemory.MapDocumentNode{
		{Key: "мара", Name: "Мара", Category: "character"},
		{Key: "сава", Name: "Сава", Category: "character"},
		{Key: "белый порт", Name: "Белый порт", Category: "place"},
		{Key: "гильдия смотрителей", Name: "Гильдия смотрителей", Category: "faction"},
		{Key: "кинжал", Name: "Кинжал", Category: "item"},
		{Key: "ледяной трон", Name: "Ледяной трон", Category: "item", Material: true},
	}
	cards := map[int]*sagamemory.Card{
		0: {
			Summary: "Мара встречает Саву у ворот заставы.",
			// The verbatim fields of the card — the time of an event, the opening of the chapter, the time lines — and what a
			// character knows are no part of the table: no reference says them.
			When:  &sagamemory.ChapterTime{Opens: "«На рассвете третьего дня» (¶1)", Note: "утро"},
			Time:  []string{"«к полудню» (¶4)"},
			Knows: []sagamemory.Knowledge{{Who: "Мара", Kind: "скрывает", What: "ключ от ворот"}},
			Events: []sagamemory.Event{
				{Event: "Мара и Сава держат ворота", Who: []string{"Мара", "Сава", "Незнакомец"}, Time: "«в полдень» (¶5)", P: 5},
				{Event: "Колокол бьёт тревогу"},
			},
			Characters: []sagamemory.Mention{{Name: "Мара", Note: "дозорная", P: 1}, {Name: "Незнакомец"}},
			Entities: []sagamemory.Mention{
				{Name: "Белый порт", Type: "город", Note: "виден с холма"},
				{Name: "Кинжал", Type: "оружие"},
				{Name: "Песня ветра", Type: "понятие"},
			},
			Changes: []sagamemory.Change{{Subject: "Мара", Change: "стала капитаном", P: 15}},
			Facts:   []sagamemory.Fact{{Subject: "Мара", Attribute: "возраст", Value: "24"}},
			Hooks:   []sagamemory.Hook{{What: "Кто звонил в колокол?"}},
		},
		2: {Summary: "Перевал."},
	}
	// The names of the cards lead to the nodes by the glossary, a record the book does not know by the names of the map.
	keys := map[string]string{"мара": "мара", "сава": "сава", "белый порт": "белый порт", "кинжал": "кинжал", "ледяной трон": "ледяной трон"}
	return ExportSaga{
		ID: "saga-1", Title: "Северный предел", Map: sagamemory.MapDocument{Nodes: nodes, Chapters: []sagamemory.MapChapterRef{ref(0), ref(1), ref(2)}},
		Nodes:   map[string]sagamemory.MapCardDocument{"мара": hero, "ледяной трон": throne, "сава": {Key: "сава", Name: "Сава", Mentions: 1}},
		Version: version, Cards: cards,
		NodeOf: func(name string) (string, bool) {
			key, ok := keys[sagamemory.GlossaryKey(name)]
			return key, ok
		},
	}
}

// exportFixturePaths are the paths of the references of the fixture.
func exportFixturePaths() ExportPaths {
	return ExportPaths{Folder: "CanonKeeper/Северный предел", Files: map[FileID]string{
		{Kind: ExportIndex, Key: ExportIndexKey}:       "Сага.md",
		{Kind: ExportNode, Key: "мара"}:                "Герои/Мара.md",
		{Kind: ExportNode, Key: "сава"}:                "Герои/Сава.md",
		{Kind: ExportNode, Key: "белый порт"}:          "Места/Белый порт.md",
		{Kind: ExportNode, Key: "гильдия смотрителей"}: "Фракции/Гильдия смотрителей.md",
		{Kind: ExportNode, Key: "кинжал"}:              "Предметы/Кинжал.md",
		{Kind: ExportNode, Key: "ледяной трон"}:        "Предметы/Ледяной трон.md",
		{Kind: ExportChapter, Key: "ch-1"}:             "Главы/Туманный берег/1 Застава.md",
		{Kind: ExportChapter, Key: "ch-3"}:             "Главы/Перевал/3 Перевал.md",
	}}
}

const (
	leadLine = "Эту заметку ведёт CanonKeeper и переписывает при выгрузке. Свои мысли пишите в своей заметке со ссылкой сюда.\n"
	ch1Link  = "[[CanonKeeper/Северный предел/Главы/Туманный берег/1 Застава|Книга 1, гл. 1 „Застава“]]"
	ch3Link  = "[[CanonKeeper/Северный предел/Главы/Перевал/3 Перевал|Книга 2, гл. 3 „Перевал“]]"
	maraLink = "[[CanonKeeper/Северный предел/Герои/Мара|Мара]]"
	savaLink = "[[CanonKeeper/Северный предел/Герои/Сава|Сава]]"
)

// OE2.1–OE2.4: the reference of a node says what its card on the page of the map shows, in its words: a place as a link to
// the reference of its chapter with «абзац N», a node without a reference — taken off the map — as its name alone, a list the
// card cuts with the line that says so; no key of a chapter and no «¶».
func TestTheReferenceOfANodeSaysItsCard(t *testing.T) {
	saga := exportFixture()
	got := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, exportFixturePaths()))
	want := "---\n" +
		"canonkeeper-saga: \"saga-1\"\ncanonkeeper-key: \"мара\"\ncanonkeeper-kind: \"node\"\naliases:\n  - \"Марка\"\n" +
		"---\n" + leadLine +
		"\n# Мара\n\nгероиня\n\nТакже: Марка\n\nдозорная у ворот\n\n" +
		"- Впервые: " + ch1Link + ", абзац 3\n" +
		"- Встречается: в 2 главах\n" +
		"\n## По вашим материалам\n\nЗапись автора; при расхождении прав текст книги · Герой\n\nМара родом с севера.\n\n- Возраст: 24\n\n" +
		"Запись из Obsidian: Персонажи/Мара.md\n" +
		"\n## Связи\n\n" +
		"- " + savaLink + " — Вместе в 2 событиях\n" +
		"  - " + ch1Link + ", абзац 5 — Мара и Сава держат ворота\n" +
		"- [[CanonKeeper/Северный предел/Предметы/Ледяной трон|Ледяной трон]] — в ваших материалах\n" +
		"- Призрак — Оба названы, когда что-то изменилось\n" +
		"  - Книга 1, гл. 2 „Белый порт“, абзац 2 — Призрак — исчез\n" +
		"\nУпоминаются в одних главах: Глеб\n" +
		"\n## В ваших материалах\n\n- [[CanonKeeper/Северный предел/Предметы/Ледяной трон|Ледяной трон]] · Предмет\n" +
		"\n## События (41)\n\n- " + ch1Link + ", абзац 5 — Мара и Сава держат ворота\n" +
		"\nПоказана часть: полный список — на карте саги в CanonKeeper\n" +
		"\n## Что изменилось (1)\n\n- " + ch3Link + ", абзац 15 — Мара — стала капитаном\n"
	if got != want {
		t.Fatalf("the reference of a node:\n%s\nwant:\n%s", got, want)
	}
	noDevice(t, got)
	// A record of the author the book does not know: the plan of the author, no line of the book.
	throne := string(saga.Text(FileID{Kind: ExportNode, Key: "ледяной трон"}, exportFixturePaths()))
	if !strings.Contains(throne, "План автора, в книге ещё нет · Предмет") || strings.Contains(throne, "Встречается") || strings.Contains(throne, "Впервые") {
		t.Fatalf("the reference of a record the book does not know:\n%s", throne)
	}
}

// Data «справка главы»: what happens with its events and who, who is in the chapter, where, what else is named — the nodes
// as links, the rest as text —, what changed; the parts the table does not name are not written.
func TestTheReferenceOfAChapterSaysItsCardByTheTable(t *testing.T) {
	saga := exportFixture()
	got := string(saga.Text(FileID{Kind: ExportChapter, Key: "ch-1"}, exportFixturePaths()))
	want := "---\n" +
		"canonkeeper-saga: \"saga-1\"\ncanonkeeper-key: \"ch-1\"\ncanonkeeper-kind: \"chapter\"\naliases:\n  - \"Книга 1, гл. 1 „Застава“\"\n" +
		"---\n" + leadLine +
		"\n# Книга 1, гл. 1 „Застава“\n" +
		"\n## Что происходит\n\nМара встречает Саву у ворот заставы.\n\n" +
		"- Мара и Сава держат ворота — " + maraLink + ", " + savaLink + ", Незнакомец · абзац 5\n" +
		"- Колокол бьёт тревогу\n" +
		"\n## Кто в главе\n\n- " + maraLink + " — дозорная\n- Незнакомец\n" +
		"\n## Где\n\n- [[CanonKeeper/Северный предел/Места/Белый порт|Белый порт]] — виден с холма\n" +
		"\n## Что ещё названо\n\n- [[CanonKeeper/Северный предел/Предметы/Кинжал|Кинжал]]\n- Песня ветра\n" +
		"\n## Что изменилось\n\n- " + maraLink + " — стала капитаном · абзац 15\n"
	if got != want {
		t.Fatalf("the reference of a chapter:\n%s\nwant:\n%s", got, want)
	}
	noDevice(t, got)
	for _, left := range []string{"колокол?", "возраст", "рассвете", "утро", "полудню", "в полдень", "ключ от ворот", "скрывает"} {
		if strings.Contains(got, left) {
			t.Fatalf("%q of a field the table does not name is in the reference", left)
		}
	}
}

// OE2.1: Сага.md lists the books and their chapters — a chapter without a reference by its place alone — and the categories
// of the map with their nodes, each a link by the full path.
func TestTheIndexListsTheBooksAndTheCategories(t *testing.T) {
	saga := exportFixture()
	got := string(saga.Text(FileID{Kind: ExportIndex, Key: ExportIndexKey}, exportFixturePaths()))
	want := "---\n" +
		"canonkeeper-saga: \"saga-1\"\ncanonkeeper-key: \"saga\"\ncanonkeeper-kind: \"index\"\n---\n" + leadLine +
		"\n# Северный предел\n" +
		"\n## Туманный берег\n\n- " + ch1Link + "\n- Книга 1, гл. 2 „Белый порт“\n" +
		"\n## Перевал\n\n- " + ch3Link + "\n" +
		"\n## Герои\n\n- " + maraLink + "\n- " + savaLink + "\n" +
		"\n## Места\n\n- [[CanonKeeper/Северный предел/Места/Белый порт|Белый порт]]\n" +
		"\n## Фракции\n\n- [[CanonKeeper/Северный предел/Фракции/Гильдия смотрителей|Гильдия смотрителей]]\n" +
		"\n## Предметы\n\n- [[CanonKeeper/Северный предел/Предметы/Кинжал|Кинжал]]\n- [[CanonKeeper/Северный предел/Предметы/Ледяной трон|Ледяной трон]]\n"
	if got != want {
		t.Fatalf("the index:\n%s\nwant:\n%s", got, want)
	}
}

// OE2.1, OE2.6, OE2.7: the references of a saga — one a node in the folder of its category, one a chapter with a fresh card,
// named by the number of its place and its title without the number —, and the chapter without a fresh card stays.
func TestTheFilesOfASagaAreItsNodesAndItsChaptersWithCards(t *testing.T) {
	saga := exportFixture()
	var names []string
	for _, file := range saga.Files() {
		names = append(names, string(file.ID.Kind)+":"+Join(folderOrRoot(file.Folder+file.Book), file.Name))
	}
	want := []string{
		"index:Сага", "node:Герои/Мара", "node:Герои/Сава", "node:Места/Белый порт", "node:Фракции/Гильдия смотрителей",
		"node:Предметы/Кинжал", "node:Предметы/Ледяной трон", "chapter:book-1/1 Застава", "chapter:book-2/3 Перевал",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Files = %q", names)
	}
	if stay := saga.Stay(); !reflect.DeepEqual(stay, []FileID{{Kind: ExportChapter, Key: "ch-2"}}) {
		t.Fatalf("Stay = %v", stay)
	}
}

// OE2.2: the values of the header are in double quotes by the rules of YAML, and the reader of the bridge reads them back.
func TestTheValuesOfTheHeaderAreQuoted(t *testing.T) {
	saga := exportFixture()
	weird := `мара: "дочь" #1`
	saga.Nodes[weird] = sagamemory.MapCardDocument{Key: weird, Name: "Мара", Aliases: []string{`Марка: "Север"`, "[[x]]"}}
	text := saga.Text(FileID{Kind: ExportNode, Key: weird}, exportFixturePaths())
	note, skip := ReadNote("Герои/Мара.md", text)
	if skip != "" {
		t.Fatal(skip)
	}
	key, _ := note.Front.Get("canonkeeper-key")
	saga1, _ := note.Front.Get("canonkeeper-saga")
	aliases, _ := note.Front.Get("aliases")
	if key.Text != weird || saga1.Text != "saga-1" || !reflect.DeepEqual(aliases.List, []string{`Марка: "Север"`, "[[x]]"}) {
		t.Fatalf("the header reads back as %+v %+v %+v", key, saga1, aliases)
	}
}

// noDevice says a text has no key of a chapter and no «¶».
func noDevice(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, "¶") || regexp.MustCompile(`(?:^|[^\p{L}\d])b\d+c\d+(?:[^\p{L}\d]|$)`).MatchString(text) {
		t.Fatalf("a key of a chapter or «¶» in:\n%s", text)
	}
}

// OE2.3: a text that came from a card, a record of the author or the model reads in Obsidian as the page shows it — no tag, no
// link or embed of the wiki, no comment, no heading, quote, list or table it did not mean —, and a description keeps its
// paragraphs and its lines.
func TestATextOfACardReadsAsItselfInObsidian(t *testing.T) {
	for text, want := range map[string]string{
		"Мара носит #знак севера":         `Мара носит \#знак севера`,
		"брат [[Сава]] и ![[герб.png]]":   `брат \[\[Сава\]\] и !\[\[герб.png\]\]`,
		"# не заголовок":                  `\# не заголовок`,
		"> не цитата":                     `\> не цитата`,
		"- не пункт":                      `\- не пункт`,
		"+ не пункт":                      `\+ не пункт`,
		"12. не пункт":                    `12\. не пункт`,
		"3) не пункт":                     `3\) не пункт`,
		"=":                               `\=`,
		"| a | b |":                       `\| a \| b \|`,
		"%%скрыто%% и 50%":                `\%\%скрыто\%\% и 50%`,
		"==выделено== и a=b":              `\=\=выделено\=\= и a=b`,
		"~~зачёркнуто~~ и ~5 лет":         `\~\~зачёркнуто\~\~ и ~5 лет`,
		"*курсив* _тоже_ `код`":           "\\*курсив\\* \\_тоже\\_ \\`код\\`",
		"$x$ <b>жирный</b> &amp; ^блок":   `\$x\$ \<b\>жирный\</b\> \&amp; \^блок`,
		`путь C:\карта`:                   `путь C:\\карта`,
		"обычный текст, без разметки: 5.": "обычный текст, без разметки: 5.",
	} {
		if got := plain(text); got != want {
			t.Errorf("plain(%q) = %q, want %q", text, got, want)
		}
	}
	description := "Рог из кости кита.\r\n\r\n\r\nТрубят в него только при осаде.\n- первая примета\n  # вторая примета  \n"
	if got, want := paragraphs(description), "Рог из кости кита.\n\nТрубят в него только при осаде.\n\\- первая примета\n\\# вторая примета"; got != want {
		t.Fatalf("paragraphs = %q, want %q", got, want)
	}
	// In a reference: the description with its lines, the texts of the card escaped, a name without a reference as text.
	saga := exportFixture()
	hero := saga.Nodes["мара"]
	hero.Material.Description = description
	hero.Events = []sagamemory.CardEvent{{Chapter: hero.Events[0].Chapter, Event: "Мара прячет #ключ в [[тайник]]", Paragraph: 5}}
	hero.Links = append(hero.Links, sagamemory.CardLink{Key: "нет", Name: "Песня #ветра", Changes: []sagamemory.CardChange{{Chapter: hero.Events[0].Chapter, Subject: "Песня", Change: "%%смолкла%%"}}})
	saga.Nodes["мара"] = hero
	got := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, exportFixturePaths()))
	for _, want := range []string{
		"\n\nРог из кости кита.\n\nТрубят в него только при осаде.\n\\- первая примета\n\\# вторая примета\n\n- Возраст: 24\n",
		", абзац 5 — Мара прячет \\#ключ в \\[\\[тайник\\]\\]\n",
		"\n- Песня \\#ветра — Оба названы, когда что-то изменилось\n  - " + ch1Link + " — Песня — \\%\\%смолкла\\%\\%\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the reference has no %q:\n%s", want, got)
		}
	}
}

// OE2.3 (Data «обрезанный список»): «Связи» the card cuts say so when the map draws more links of the node than the card shows,
// and «Упоминаются в одних главах» when the card shows as many names as it shows at most — a list without a total that reached
// its bound is a part.
func TestAListTheCardCutsSaysItIsAPart(t *testing.T) {
	saga := exportFixture()
	got := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, exportFixturePaths()))
	if strings.Count(got, wordPart) != 1 {
		t.Fatalf("only the events of the fixture are cut:\n%s", got)
	}
	// The map draws four links of Мара, the card shows three.
	links := []sagamemory.MapDocumentLink{{Source: 0, Target: 1}, {Source: 0, Target: 2}, {Source: 3, Target: 0}, {Source: 0, Target: 4}, {Source: 1, Target: 2}}
	saga.Map.Links = links
	hero := saga.Nodes["мара"]
	hero.Beside = nil
	for _, name := range []string{"Глеб", "Вера", "Ольга", "Тихон", "Яков", "Лада", "Мирон", "Ждан"} {
		hero.Beside = append(hero.Beside, sagamemory.CardNeighbor{Key: sagamemory.GlossaryKey(name), Name: name})
	}
	saga.Nodes["мара"] = hero
	got = string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, exportFixturePaths()))
	for _, want := range []string{
		"  - Книга 1, гл. 2 „Белый порт“, абзац 2 — Призрак — исчез\n\n" + wordPart + "\n\nУпоминаются в одних главах: Глеб,",
		", Ждан\n\n" + wordPart + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the reference has no %q:\n%s", want, got)
		}
	}
	// Three links drawn, three shown, seven names beside: nothing is cut.
	saga.Map.Links = links[:3]
	hero.Beside = hero.Beside[:7]
	saga.Nodes["мара"] = hero
	if got := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, exportFixturePaths())); strings.Count(got, wordPart) != 1 {
		t.Fatalf("a list that is whole says it is a part:\n%s", got)
	}
}

// The bound of «Упоминаются в одних главах» is the one the card of the map holds: a node with more names beside shows that many.
func TestTheBoundOfTheNamesBesideIsTheCards(t *testing.T) {
	node := sagamemory.StoredNode{Key: "мара", Name: "Мара", Chapters: []int{0}}
	for index := range cardBeside + 3 {
		node.Links = append(node.Links, sagamemory.StoredLink{Node: index + 1, Key: fmt.Sprint("имя-", index), Name: fmt.Sprint("Имя ", index), Mentions: []int{0}})
	}
	card := node.Card([]sagamemory.MapChapterRef{{Key: "1", Label: "Книга 1, гл. 1"}}, -1, sagamemory.MapHidden{})
	if len(card.Beside) != cardBeside {
		t.Fatalf("the card shows %d names beside, the export counts %d", len(card.Beside), cardBeside)
	}
}

// OE2.2: the header is YAML a strict reader reads — every value in double quotes with the escapes of YAML, whatever the name
// holds: a colon, quotes of both kinds, «#», «-» and «?» at the start, the words YAML reads as a truth or as nothing, a number,
// a backslash; an end of a line and a tab of a name are a space.
func TestTheHeaderIsExactYAMLForHardNames(t *testing.T) {
	saga := exportFixture()
	key := "мара: \"дочь\"\n#1"
	saga.Nodes[key] = sagamemory.MapCardDocument{Key: key, Name: "Мара", Aliases: []string{
		`Марка: "Север"`, `'одинарные'`, "#тег", "- дефис", "? вопрос", "yes", "no", "null", "123", `C:\путь\к`, "две\nстроки", "таб\tуляция",
	}}
	text := string(saga.Text(FileID{Kind: ExportNode, Key: key}, exportFixturePaths()))
	want := "---\n" +
		"canonkeeper-saga: \"saga-1\"\n" +
		"canonkeeper-key: \"мара: \\\"дочь\\\"\\n#1\"\n" +
		"canonkeeper-kind: \"node\"\n" +
		"aliases:\n" +
		"  - \"Марка: \\\"Север\\\"\"\n" +
		"  - \"'одинарные'\"\n" +
		"  - \"#тег\"\n" +
		"  - \"- дефис\"\n" +
		"  - \"? вопрос\"\n" +
		"  - \"yes\"\n" +
		"  - \"no\"\n" +
		"  - \"null\"\n" +
		"  - \"123\"\n" +
		"  - \"C:\\\\путь\\\\к\"\n" +
		"  - \"две строки\"\n" +
		"  - \"таб уляция\"\n" +
		"---\n" + leadLine
	if !strings.HasPrefix(text, want) {
		t.Fatalf("the header:\n%s\nwant:\n%s", text, want)
	}
	// The reader of the bridge reads the values back as they were given.
	note, skip := ReadNote("Герои/Мара.md", []byte(text))
	if skip != "" {
		t.Fatal(skip)
	}
	if got, _ := note.Front.Get("canonkeeper-key"); got.Text != key {
		t.Fatalf("the key reads back as %q", got.Text)
	}
}

// OE2.4: a caption of a link keeps no sign that would break the link — «[», «]», «|» — and no end of a line.
func TestACaptionOfALinkKeepsNoSignOfTheLink(t *testing.T) {
	saga := exportFixture()
	saga.Map.Nodes[0].Name = "Мара [Север] | дочь\nкапитана"
	index := string(saga.Text(FileID{Kind: ExportIndex, Key: ExportIndexKey}, exportFixturePaths()))
	if !strings.Contains(index, "\n- [[CanonKeeper/Северный предел/Герои/Мара|Мара Север дочь капитана]]\n") {
		t.Fatalf("the index:\n%s", index)
	}
}

// OE2.3: «Что изменилось» the card cuts says it is a part, as «События» does.
func TestTheChangesTheCardCutsSayTheyAreAPart(t *testing.T) {
	saga := exportFixture()
	hero := saga.Nodes["мара"]
	hero.ChangesTotal = 31
	saga.Nodes["мара"] = hero
	got := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, exportFixturePaths()))
	if want := "\n## Что изменилось (31)\n\n- " + ch3Link + ", абзац 15 — Мара — стала капитаном\n\n" + wordPart + "\n"; !strings.HasSuffix(got, want) {
		t.Fatalf("the reference:\n%s\nwant the end:\n%s", got, want)
	}
}

// OE2.4, OE2.6: a note of the author links a node by its name; when the name of the file is not the name — cleaned of the signs
// a name of a file does not keep, or « (2)» beside a namesake —, the name stands first among the aliases of the header, so
// Obsidian finds the reference by it.
func TestTheNameOfANodeIsAnAliasWhenItsFileIsNamedOtherwise(t *testing.T) {
	saga := exportFixture()
	paths := exportFixturePaths()
	if text := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, paths)); !strings.Contains(text, "aliases:\n  - \"Марка\"\n---") {
		t.Fatalf("a node whose file is its name:\n%s", text)
	}
	for name, file := range map[string]string{"Мара: дочь капитана?": "Герои/Мара дочь капитана.md", "Мара": "Герои/Мара (2).md"} {
		hero := saga.Nodes["мара"]
		hero.Name = name
		saga.Nodes["мара"] = hero
		paths.Files[FileID{Kind: ExportNode, Key: "мара"}] = file
		text := string(saga.Text(FileID{Kind: ExportNode, Key: "мара"}, paths))
		if want := "aliases:\n  - " + strconv.Quote(name) + "\n  - \"Марка\"\n---"; !strings.Contains(text, want) {
			t.Fatalf("a node %q in %s:\n%s", name, file, text)
		}
		if strings.Contains(text, "Также: "+name) {
			t.Fatalf("the name is said as another name:\n%s", text)
		}
	}
}

// OE2.1: Сага.md names two books of one title as their folders do — the second with « (2)».
func TestTheIndexNamesBooksOfOneTitleAsTheirFolders(t *testing.T) {
	saga := exportFixture()
	saga.Version = sagamemory.NewVersion("saga-1", "published", "fp", []sagamemory.Chapter{
		{ID: "ch-1", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 1, Title: "Глава 1. Застава"},
		{ID: "ch-3", BookID: "book-2", BookOrder: 2, BookKeyNumber: 2, BookTitle: "туманный берег", Order: 1, Title: "Глава 3. Перевал"},
	})
	index := string(saga.Text(FileID{Kind: ExportIndex, Key: ExportIndexKey}, exportFixturePaths()))
	if !strings.Contains(index, "\n## Туманный берег\n") || !strings.Contains(index, "\n## туманный берег (2)\n") {
		t.Fatalf("the index:\n%s", index)
	}
}
