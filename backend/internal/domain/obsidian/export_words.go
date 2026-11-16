// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import "canonkeeper/backend/internal/domain/sagamemory"

// The words of the files of an export (specs/2026-11-15-desk-obsidian-export.md, Data). The author reads them in his vault,
// so they are the words of the pages: the reference of a node says what the card of a node of the map says, in its words,
// and testdata/export-words.json holds every word of this file for the test of this package and for the test of the page
// of the map, which checks them by the dictionary of the page too.
const (
	// wordLead is the first line of the text of every reference.
	wordLead = "Эту заметку ведёт CanonKeeper и переписывает при выгрузке. Свои мысли пишите в своей заметке со ссылкой сюда."
	// wordPart stands under a list the card of a node cuts.
	wordPart   = "Показана часть: полный список — на карте саги в CanonKeeper"
	wordNoName = "Без имени"

	// The folders and the index.
	wordIndex    = "Сага"
	wordChapters = "Главы"
	wordDeleted  = "_Удалено"

	// The card of a node, as the page of the map says it (src/features/saga/NodeCard.tsx).
	wordNoType        = "без типа"
	wordAlso          = "Также:"
	wordFirst         = "Впервые"
	wordMet           = "Встречается"
	wordLinks         = "Связи"
	wordFromMaterials = "в ваших материалах"
	wordTogether      = "Вместе в"
	wordBothChanged   = "Оба названы, когда что-то изменилось"
	wordBeside        = "Упоминаются в одних главах:"
	wordNamedIn       = "В ваших материалах"
	wordEvents        = "События"
	wordChanges       = "Что изменилось"
	wordMaterial      = "По вашим материалам"
	wordPlan          = "План автора, в книге ещё нет"
	wordRecord        = "Запись автора; при расхождении прав текст книги"
	wordFromObsidian  = "Запись из Obsidian:"
	wordFromFile      = "Из файла"

	// The reference of a chapter (Data «справка главы»).
	wordHappens   = "Что происходит"
	wordWho       = "Кто в главе"
	wordWhere     = "Где"
	wordNamed     = "Что ещё названо"
	wordParagraph = "абзац"
)

// The forms of the words after a number, as the page counts them: one, a few, many.
var (
	chapterForms = [3]string{"главе", "главах", "главах"}
	eventForms   = [3]string{"событии", "событиях", "событиях"}
)

// categoryWords are the words of the categories of the map (mapCategories of the page): the folders of the nodes.
var categoryWords = map[sagamemory.MapCategory]string{
	sagamemory.MapCharacter: "Герои",
	sagamemory.MapPlace:     "Места",
	sagamemory.MapFaction:   "Фракции",
	sagamemory.MapItem:      "Предметы",
}

// categoryOrder is the order of the categories, as the page lists them.
var categoryOrder = []sagamemory.MapCategory{sagamemory.MapCharacter, sagamemory.MapPlace, sagamemory.MapFaction, sagamemory.MapItem}

// exportKindWords are the words of the kinds of the records of the author (authorMaterialKinds of the page).
var exportKindWords = map[string]string{
	"skill": "Навык", "place": "Место", "character": "Герой", "item": "Предмет", "faction": "Фракция", "other": "Другое",
}

// plural is the form of a word after a number by the rule of Russian.
func plural(count int, forms [3]string) string {
	tens, ones := count%100, count%10
	switch {
	case tens >= 11 && tens <= 14:
		return forms[2]
	case ones == 1:
		return forms[0]
	case ones >= 2 && ones <= 4:
		return forms[1]
	}
	return forms[2]
}
