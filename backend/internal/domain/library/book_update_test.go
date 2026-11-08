package library

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func updateChapter(title string, paragraphs ...string) BookChapterText {
	return BookChapterText{Title: title, Paragraphs: paragraphs}
}

// R3.1: spaces of any kind, their number and the spaces at the edges do not change a chapter.
func TestBookUpdatePlanIgnoresSpaces(t *testing.T) {
	book := []BookChapterText{updateChapter("Глава 1", "Он сказал — нет.", "Второй  абзац,\tи всё.")}
	file := []BookChapterText{updateChapter("Глава 1", " Он сказал — нет. ", "Второй абзац, и всё. ")}
	plan := PlanBookUpdate(book, file)
	if got := plan.Chapters[0]; got != (ChapterPairing{Book: 0, Change: TextSame}) || len(plan.Removed) != 0 {
		t.Fatalf("plan = %+v, want the chapter unchanged", plan)
	}
	if counts := plan.Counts(); counts != (BookUpdateCounts{Unchanged: 1}) {
		t.Fatalf("counts = %+v, want one unchanged chapter", counts)
	}
}

// R3.2: the same words with other signs, letter case or «ё» are a change of signs; R3.4: one other word, or
// another number of paragraphs, is a change of words.
func TestBookUpdatePlanTellsSignsFromWords(t *testing.T) {
	book := []BookChapterText{
		updateChapter("Глава 1", "Ёлка «зелёная» - да!", "Он ушёл."),
		updateChapter("Глава 2", "Отряд из пятнадцати человек вышел к реке."),
		updateChapter("Глава 3", "Первый абзац.", "Второй абзац."),
		updateChapter("Глава 4", "Третья сцена. Четвёртая сцена."),
	}
	file := []BookChapterText{
		updateChapter("Глава 1", "Елка „зеленая“ — да.", "он ушел…"),
		updateChapter("Глава 2", "Отряд из тринадцати человек вышел к реке."),
		updateChapter("Глава 3", "Первый абзац.", "Второй абзац.", "Третий абзац."),
		updateChapter("Глава 4", "Третья сцена.", "Четвёртая сцена."),
	}
	plan := PlanBookUpdate(book, file)
	want := []ChapterPairing{
		{Book: 0, Change: TextPunctuation},
		{Book: 1, Change: TextWords},
		{Book: 2, Change: TextWords},
		{Book: 3, Change: TextWords},
	}
	for i := range want {
		if plan.Chapters[i] != want[i] {
			t.Fatalf("chapter %d = %+v, want %+v", i+1, plan.Chapters[i], want[i])
		}
	}
	if counts := plan.Counts(); counts != (BookUpdateCounts{Words: 3, Punctuation: 1}) {
		t.Fatalf("counts = %+v", counts)
	}
}

// R4.1, R4.2: a chapter with a new title keeps its place in the book when its text is the same or almost the
// same; a chapter the file no longer has leaves the book.
func TestBookUpdatePlanKeepsRenamedChapters(t *testing.T) {
	long := sagaText(7, 400)
	edited := append([]string(nil), long...)
	edited[3] = strings.Replace(edited[3], " ", " тринадцати ", 1)
	book := []BookChapterText{
		updateChapter("Глава 33. Берег", sagaText(1, 300)...),
		updateChapter("Глава 34. Переправа", long...),
		updateChapter("Глава 35. Лагерь", sagaText(3, 300)...),
		updateChapter("Глава 36. Пустая", "Короткая глава, которой в файле нет."),
	}
	file := []BookChapterText{
		updateChapter("Глава 33. Берег", sagaText(1, 300)...),
		updateChapter("Глава 34. Мост", edited...),
		updateChapter("Глава тридцать пятая", sagaText(3, 300)...),
	}
	plan := PlanBookUpdate(book, file)
	want := []ChapterPairing{
		{Book: 0, Change: TextSame},
		{Book: 1, Change: TextWords, Renamed: true},
		{Book: 2, Change: TextSame, Renamed: true},
	}
	for i := range want {
		if plan.Chapters[i] != want[i] {
			t.Fatalf("chapter %d = %+v, want %+v", i+1, plan.Chapters[i], want[i])
		}
	}
	if len(plan.Removed) != 1 || plan.Removed[0] != 3 {
		t.Fatalf("removed = %v, want the fourth chapter", plan.Removed)
	}
	if counts := plan.Counts(); counts != (BookUpdateCounts{Unchanged: 1, Words: 1, Renamed: 2, Removed: 1}) {
		t.Fatalf("counts = %+v", counts)
	}
}

// R4.1: the threshold 0.5. A chapter with every tenth word replaced is still the chapter (≈ 0.57); another
// chapter of the same saga, with the same names and common words, is far below it.
func TestBookUpdatePlanSimilarityThreshold(t *testing.T) {
	original := sagaText(11, 600)
	everyTenth := replaceEveryTenthWord(original)
	other := sagaText(12, 600)
	if got := TextSimilarity(original, everyTenth); got < RenamedTextSimilarity || got > 0.7 {
		t.Fatalf("similarity with every tenth word replaced = %.3f, want just above %.2f", got, RenamedTextSimilarity)
	}
	if got := TextSimilarity(original, other); got >= 0.1 {
		t.Fatalf("similarity of two chapters of one saga = %.3f, want below 0.1", got)
	}
	if got := TextSimilarity(original, original); got != 1 {
		t.Fatalf("similarity of a chapter with itself = %.3f", got)
	}
	plan := PlanBookUpdate(
		[]BookChapterText{updateChapter("Глава 1. Старое", original...), updateChapter("Глава 2. Старое", sagaText(13, 600)...)},
		[]BookChapterText{updateChapter("Глава 1. Новое", everyTenth...), updateChapter("Глава 2. Новое", other...)},
	)
	if plan.Chapters[0] != (ChapterPairing{Book: 0, Change: TextWords, Renamed: true}) {
		t.Fatalf("chapter with every tenth word replaced = %+v, want the same chapter renamed", plan.Chapters[0])
	}
	if plan.Chapters[1].Book != -1 || len(plan.Removed) != 1 || plan.Removed[0] != 1 {
		t.Fatalf("another text under another title = %+v, removed %v; want a new chapter and the old one removed", plan.Chapters[1], plan.Removed)
	}
}

// R6.2: a place of a change names the words around it, both sides at most 160 characters, and a chapter
// shows at most two places.
func TestChangePlaces(t *testing.T) {
	before := []string{
		"Утро выдалось холодным.",
		"Отряд из пятнадцати человек вышел к реке на рассвете, и никто из них не оглянулся на лагерь, где остались раненые.",
		"Вода была тёмной.",
	}
	after := []string{
		"Утро выдалось холодным.",
		"Отряд из тринадцати человек вышел к реке на рассвете, и никто из них не оглянулся на лагерь, где остались раненые.",
		"Вода была тёмной.",
	}
	places := ChangePlaces(before, after, 2)
	if len(places) != 1 || !strings.Contains(places[0].Before, "пятнадцати") || !strings.Contains(places[0].After, "тринадцати") ||
		strings.Contains(places[0].Before, "тринадцати") {
		t.Fatalf("places = %+v, want one place «пятнадцати» → «тринадцати»", places)
	}
	long := strings.Repeat("слово ", 300)
	many := ChangePlaces([]string{"а", "б", "в", long + "конец"}, []string{"а!", "б!", "в!", long + "финал"}, 2)
	if len(many) != 2 {
		t.Fatalf("places of four changed paragraphs = %d, want two", len(many))
	}
	rewritten := ChangePlaces([]string{strings.Repeat("старый текст ", 100)}, []string{strings.Repeat("новые слова ", 100)}, 2)
	for _, place := range append(many, rewritten...) {
		if utf8.RuneCountInString(place.Before) > MaxChangePlaceRunes || utf8.RuneCountInString(place.After) > MaxChangePlaceRunes {
			t.Fatalf("place %q → %q is longer than %d characters", place.Before, place.After, MaxChangePlaceRunes)
		}
	}
	added := ChangePlaces([]string{"Один."}, []string{"Один.", "Два."}, 2)
	if len(added) != 1 || added[0].Before != "" || added[0].After != "Два." {
		t.Fatalf("places of an added paragraph = %+v", added)
	}
	if spaces := ChangePlaces([]string{"Он  сказал"}, []string{"Он сказал"}, 2); len(spaces) != 0 {
		t.Fatalf("places of spaces = %+v, want none", spaces)
	}
}

// sagaText writes paragraphs of a saga: common words, the same names in every chapter, the rest by seed.
func sagaText(seed, words int) []string {
	common := []string{"и", "в", "не", "на", "он", "что", "с", "она", "как", "а", "то", "к", "по", "из", "у", "за", "его", "но", "было", "так"}
	names := []string{"Кир", "Мила", "Леон", "Аста", "капитан", "отряд", "башня", "город", "меч", "река"}
	rare := []string{"рассвет", "тень", "камень", "ветер", "дорога", "огонь", "голос", "стена", "дверь", "лагерь", "крик", "ночь",
		"кровь", "щит", "мост", "берег", "лодка", "тропа", "сон", "письмо", "ключ", "зал", "окно", "поле", "лес", "гора", "снег", "дождь"}
	state := uint32(seed*7919 + 17)
	next := func(n int) int {
		state = state*1664525 + 1013904223
		return int(state>>8) % n
	}
	var paragraphs []string
	var paragraph []string
	for i := 0; i < words; i++ {
		var word string
		switch roll := next(10); {
		case roll < 5:
			word = common[next(len(common))]
		case roll < 7:
			word = names[next(len(names))]
		default:
			word = rare[next(len(rare))]
		}
		paragraph = append(paragraph, word)
		if len(paragraph) == 40 {
			paragraphs = append(paragraphs, strings.Join(paragraph, " ")+".")
			paragraph = nil
		}
	}
	if len(paragraph) > 0 {
		paragraphs = append(paragraphs, strings.Join(paragraph, " ")+".")
	}
	return paragraphs
}

func replaceEveryTenthWord(paragraphs []string) []string {
	out := make([]string, len(paragraphs))
	count := 0
	for i, paragraph := range paragraphs {
		words := strings.Fields(paragraph)
		for j := range words {
			count++
			if count%10 == 0 {
				words[j] = fmt.Sprintf("замена%d", count)
			}
		}
		out[i] = strings.Join(words, " ")
	}
	return out
}
