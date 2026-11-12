package sagadraft

import "testing"

// specs/2026-11-11-ux9-writing-a.md WA1.1: a chapter without a title is named by the first words of its idea, else of its
// text — not «Новая глава».
func TestAChapterWithoutATitleIsNamedByTheBeginningOfItsIdeaElseOfItsText(t *testing.T) {
	for _, test := range []struct {
		idea, text, want string
	}{
		{"Кира ждёт у ворот, Тарена нет; она находит записку и уходит одна.", "", "Кира ждёт у ворот, Тарена нет…"},
		{"Кира ждёт у ворот. Тарен не пришёл.", "Ночь стояла тихая.", "Кира ждёт у ворот…"},
		{"Кира ждёт у ворот.", "", "Кира ждёт у ворот"},
		{"   ", "Ночь стояла тихая. Кира подошла к северным воротам и долго смотрела в темноту.", "Ночь стояла тихая…"},
		{"", "⟦Ночь стояла⟧ ⟨тихая⟩, и Кира подошла к воротам северной стены города", "Ночь стояла тихая, и Кира подошла…"},
		{"", "— Где ты был? — спросила Кира.", "Где ты был…"},
		{"", "", ""},
		{"Безоглядноперемещающийсявдольсеверноговаламедленныйкараван", "", "Безоглядноперемещающийсявдольсеверноговаламедлен…"},
	} {
		if got := Opening(test.idea, test.text); got != test.want {
			t.Errorf("Opening(%q, %q) = %q, want %q", test.idea, test.text, got, test.want)
		}
	}
}

// specs/2026-11-11-ux9-writing-a.md WA4.1: the book holds the edit when its text — paragraph for paragraph with its faces —
// and its title are those of the chapter; a title left empty is the chapter's.
func TestTheBookHoldsAnEditWhenItsTextAndTitleAreTheChapters(t *testing.T) {
	book := []string{"Ночь стояла *тихая*.", "Тарена не было."}
	for _, test := range []struct {
		name, text, title string
		want              bool
	}{
		{"the same text", "Ночь стояла ⟨тихая⟩.\n\nТарена не было.", "Белый порт", true},
		{"the same text with the marks of the helper", "Ночь стояла ⟨тихая⟩.\n\n⟦Тарена не было.⟧", "Белый порт", true},
		{"no title of its own", "Ночь стояла ⟨тихая⟩.\n\nТарена не было.", "  ", true},
		{"a word changed", "Ночь стояла ⟨тихая⟩.\n\nТарена не было видно.", "Белый порт", false},
		{"a face taken off", "Ночь стояла тихая.\n\nТарена не было.", "Белый порт", false},
		{"another title", "Ночь стояла ⟨тихая⟩.\n\nТарена не было.", "Северные ворота", false},
	} {
		if got := HeldByBook(test.text, test.title, book, "Белый порт"); got != test.want {
			t.Errorf("%s: HeldByBook = %v, want %v", test.name, got, test.want)
		}
	}
}

// WA1.3, WA4.1: where an edit stands against its chapter, as the list and the strip name it.
func TestAnEditSaysWhereItStandsAgainstItsChapter(t *testing.T) {
	for _, test := range []struct {
		edit Edit
		want EditState
	}{
		{Edit{Revision: 3, BookRevision: 3, Same: true}, EditSame},
		{Edit{Revision: 3, BookRevision: 3}, EditEdited},
		{Edit{Revision: 3, BookRevision: 4, Same: true}, EditChanged},
		{Edit{Revision: 3, Gone: true}, EditGone},
	} {
		if got := test.edit.State(); got != test.want {
			t.Errorf("%+v: State = %q, want %q", test.edit, got, test.want)
		}
	}
}

// WA4.1: the chapter of the book the author came to edit is his and written — nearly done, whatever its length; the strip
// asks nothing about it.
func TestAnEditOfAChapterOfTheBookIsNearlyDoneAtAnyLength(t *testing.T) {
	typical := 2000
	short := Draft{Text: "Ночь стояла тихая.", TypicalWords: &typical, Edits: &Edit{ChapterID: "chapter"}}
	if got := short.Case(""); got != CaseNearlyDone {
		t.Fatalf("case of a short edit = %q", got)
	}
	if got := short.Case(CaseScratch); got != CaseNearlyDone {
		t.Fatalf("case of an edit with a choice = %q", got)
	}
	gone := Draft{Text: "Ночь стояла тихая.", TypicalWords: &typical, Edits: &Edit{ChapterID: "chapter", Gone: true}}
	if got := gone.Case(""); got != CaseScratch {
		t.Fatalf("case of an edit whose chapter is gone = %q", got)
	}
}
