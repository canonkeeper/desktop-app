// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/library"
)

func recordOf(t *testing.T, notePath, text string) obsidian.Record {
	t.Helper()
	record, skip := obsidian.Material(note(t, notePath, text), "", library.MarkdownPlainLines)
	if skip != "" {
		t.Fatalf("%s skipped: %s", notePath, skip)
	}
	if _, ok := authormaterials.CleanDraft(record.Draft); !ok {
		t.Fatalf("the owner refuses the record of %s", notePath)
	}
	return record
}

// specs/2026-11-15-desk-wiki-target.md WT1.1–WT1.3: in the text of a record a link of the wiki whose caption does not name
// its target is «подпись (Цель)»; a link without a caption and one whose caption names its target — by the rule by which
// the map finds a name in the text of a record: the words of the name in a row in any of their cases, «ё» as «е», any
// register — are as before. The target is the name of the note of the link: the last name of its path without its heading,
// its block and .md; a link to a heading of its own note and an embed give none.
func TestARecordKeepsTheTargetOfAWikiLinkWithACaption(t *testing.T) {
	for name, test := range map[string]struct{ text, want string }{
		"a link without a caption":                         {"Дорога ведёт в [[Зимогорск]].", "Дорога ведёт в Зимогорск."},
		"a link without a caption with a path and heading": {"См. [[Места/Зимогорск#История]].", "См. Места/Зимогорск#История."},
		"the caption names the target in its case":         {"Дорога ведёт [[Зимогорск|к Зимогорску]].", "Дорога ведёт к Зимогорску."},
		"the caption names the target in another register": {"Дорога ведёт [[Зимогорск|К ЗИМОГОРСКУ]].", "Дорога ведёт К ЗИМОГОРСКУ."},
		"the caption names the target with «е» for «ё»":    {"Жили [[Озёрный край|в озерном крае]].", "Жили в озерном крае."},
		"a caption names a target of two words":            {"Корабли [[Белый порт|у Белого порта]].", "Корабли у Белого порта."},
		"the caption does not name the target":             {"Тракт уходит [[Зимогорск|к городу у гор]].", "Тракт уходит к городу у гор (Зимогорск)."},
		"a word that only holds the target is no name":     {"Ждали [[Сад|в засаде]].", "Ждали в засаде (Сад)."},
		"a caption that is a short target itself":          {"Пришёл [[Ян|ян]].", "Пришёл ян."},
		"the target is the last name of its path without its heading": {
			"Жил [[Места/Зимогорск#История|в старом городе]].", "Жил в старом городе (Зимогорск)."},
		"the target is without its block and .md": {"Видел [[Места/Зимогорск.md#^a1b2|там]].", "Видел там (Зимогорск)."},
		"the target is in NFC":                    {"Шли [[Озёрный край|туда]].", "Шли туда (Озёрный край)."},
		"a link to a heading of its own note":     {"Смотри [[#Внешность|ниже]].", "Смотри ниже."},
		"an embed gives no target":                {"Вид ![[Зимогорск.png|200]] с холма.", "Вид с холма."},
		"a sign after a backslash is no link":     {`Знак \[[Зимогорск|туда]] остаётся.`, "Знак [[Зимогорск|туда]] остаётся."},
		"the signs of a target are its letters":   {"Лежит [[_Черновик_|там]].", "Лежит там (_Черновик_)."},
		"several links in a line": {
			"[[Мирон Сватов|Брат]] и [[Ольга]] ушли [[Зимогорск|туда]], [[Мирон|к Мирону]] не зашли.",
			"Брат (Мирон Сватов) и Ольга ушли туда (Зимогорск), к Мирону не зашли."},
		// The caption as the map reads it — after the signs of the faces go.
		"the caption names the target under the signs of a face": {"Дорога ведёт [[Зимогорск|к *Зимо*горску]].", "Дорога ведёт к Зимогорску."},
		// Only a note is a target: a link to another file is as before; a dot inside a name of a note is no extension.
		"a link to a file that is not a note": {"Карта [[Зимогорск.pdf|на стр. 3]] и [[План.canvas|план]].", "Карта на стр. 3 и план."},
		"a dot in the name of a note":         {"Было [[Глава 1.2|раньше]].", "Было раньше (Глава 1.2)."},
		// An escaped bar of a table parts the target and the caption too.
		"an escaped bar of a table": {`Шли [[Зимогорск\|туда]].`, "Шли туда (Зимогорск)."},
		// The links in code are as before: a fence reads them as they are, code in a line as their captions.
		"a link in a fence of code": {
			"До.\n\n```\nкод [[Зимогорск|туда]] код\n```\n\nПосле.", "До.\n\nкод [[Зимогорск|туда]] код\n\nПосле."},
		"a link in code in a line": {"Пример `[[Зимогорск|туда]]` и [[Зимогорск|сюда]].", "Пример туда и сюда (Зимогорск)."},
	} {
		t.Run(name, func(t *testing.T) {
			if record := recordOf(t, "Места/Застава.md", test.text+"\n"); record.Draft.Description != test.want {
				t.Fatalf("description = %q, want %q", record.Draft.Description, test.want)
			}
		})
	}
}

// specs/2026-11-15-desk-wiki-target.md WT1.1–WT1.3: a value of a property — of the header, a field «Ключ::», an item of a
// list — writes the target of a link the same way; the key and the name of a record keep the caption alone, an embed, a link
// to a file that is not a note and a link in code are as before. The map reads a value as it is, so a caption names its
// target in a value without its signs taken off.
func TestAPropertyKeepsTheTargetOfAWikiLinkWithACaption(t *testing.T) {
	record := recordOf(t, "Персонажи/Ольга.md", "---\n"+
		"title: Ольга\n"+
		"дом: [[Места/Зимогорск|город у гор]]\n"+
		"родня: [\"[[Мирон|брат]]\", \"[[Ольга Сватова]]\"]\n"+
		"---\n"+
		"Родина:: [[Зимогорск|в Зимогорске]]\n"+
		"Друг:: [[Ян|ян]]\n"+
		"Герб:: ![[герб.png|100]]\n"+
		"Письмо:: [[Письмо.pdf|копия]]\n"+
		"Таблица:: [[Зимогорск\\|туда]]\n"+
		"Код:: `[[Зимогорск|туда]]` и [[Зимогорск|сюда]]\n"+
		"Путь:: [[Зимогорск|к *Зимо*горску]]\n"+
		"Сестра.\n")
	var properties [][2]string
	for _, property := range record.Draft.Properties {
		properties = append(properties, [2]string{property.Key, property.Value})
	}
	want := [][2]string{
		{"дом", "город у гор (Зимогорск)"}, {"родня", "брат (Мирон), Ольга Сватова"}, {"Родина", "в Зимогорске"}, {"Друг", "ян"},
		{"Герб", "!100"}, {"Письмо", "копия"}, {"Таблица", "туда (Зимогорск)"}, {"Код", "`туда` и сюда (Зимогорск)"},
		{"Путь", "к *Зимо*горску (Зимогорск)"},
	}
	if !reflect.DeepEqual(properties, want) || record.Draft.Name != "Ольга" || record.Draft.Description != "Сестра." {
		t.Fatalf("record %q, properties %q", record.Draft.Name, properties)
	}
}

// specs/2026-11-15-desk-wiki-target.md WT1.5: the brackets count in the limits of a record — a description they take past
// its limit loses its last paragraph, a value is cut at a word — by the rule of before.
func TestTheTargetOfAWikiLinkCountsInTheLimitsOfARecord(t *testing.T) {
	link := " [[Зимогорск|туда]]."
	first := strings.Repeat("Ольга стояла на башне. ", 10)
	// The second paragraph fits with its caption alone and does not with its target.
	filler := strings.Repeat("ж", authormaterials.MaxDescriptionRunes-utf8.RuneCountInString(strings.TrimSpace(first))-2-utf8.RuneCountInString(" туда."))
	record := recordOf(t, "Места/Застава.md", first+"\n\n"+filler+link+"\n")
	if record.Draft.Description != strings.TrimSpace(first) || record.Cut != 1 {
		t.Fatalf("the description of %d signs, cut %d", utf8.RuneCountInString(record.Draft.Description), record.Cut)
	}
	value := strings.Repeat("слово ", (authormaterials.MaxValueRunes-utf8.RuneCountInString("туда"))/6) + "[[Зимогорск|туда]]"
	record = recordOf(t, "Места/Застава.md", "Путь:: "+value+"\nТекст.\n")
	got := record.Draft.Properties[0].Value
	if record.Cut != 1 || utf8.RuneCountInString(got) > authormaterials.MaxValueRunes || strings.Contains(got, "Зимогорск") {
		t.Fatalf("the value of %d signs, cut %d, ends %q", utf8.RuneCountInString(got), record.Cut, string([]rune(got)[utf8.RuneCountInString(got)-20:]))
	}
}

// specs/2026-11-15-desk-wiki-target.md WT1.4: the text of a chapter of a book from notes keeps the caption of a link alone —
// the record of the same note writes its target.
func TestTheTargetOfAWikiLinkStaysOutOfTheBook(t *testing.T) {
	text := "Тракт уходит [[Зимогорск|к городу у гор]].\n"
	_, book := parsed(t, "Книга", "Книга", note(t, "Книга/01 Тракт.md", text), note(t, "Книга/02 Переправа.md", "Паром.\n"))
	if len(book.Chapters) != 2 || !reflect.DeepEqual(book.Chapters[0].Paragraphs, []string{"Тракт уходит к городу у гор."}) {
		t.Fatalf("chapters %+v", book.Chapters)
	}
	if record := recordOf(t, "Места/Тракт.md", text); record.Draft.Description != "Тракт уходит к городу у гор (Зимогорск)." {
		t.Fatalf("the record = %q", record.Draft.Description)
	}
}
