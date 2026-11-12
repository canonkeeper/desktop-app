package sagadraft

import (
	"errors"
	"testing"
)

// WA7.5: «Вернуть эту версию» takes it whole; «Взять в главу» puts a piece in place of a place, after it or at the end,
// keeping what of it the author wrote: a piece of a block of the model's text keeps its marks, and inside a block of
// the working text the block is closed before the piece and opened after it. The seams are where the piece meets the
// text.
func TestATakeKeepsWhoWroteWhat(t *testing.T) {
	version := "Автор начал.\n⟦Кира ушла в ночь. Ольм молчал.⟧\nАвтор кончил."
	if text, seams, err := TakeInto("Сейчас.", version, Take{Whole: true}); err != nil || text != version || seams != nil {
		t.Fatalf("whole = %q, %v, %v", text, seams, err)
	}
	text, seams, err := TakeInto("Глава автора.", version, Take{From: "Кира ушла", To: "в ночь."})
	if err != nil || text != "Глава автора.\n\n⟦Кира ушла в ночь.⟧" || len(seams) != 1 || seams[0] != len("Глава автора.\n\n") {
		t.Fatalf("a piece of a block at the end = %q, %v, %v", text, seams, err)
	}
	working := "Первый абзац.\n⟦Модель писала. Здесь место.⟧\nПоследний."
	text, seams, err = TakeInto(working, version, Take{From: "Автор начал.", To: "Автор начал.", Place: &Scene{Place: "Здесь место."}})
	if err != nil || text != "Первый абзац.\n⟦Модель писала. ⟧Автор начал.\nПоследний." || len(seams) != 2 || !ValidMarks(text) {
		t.Fatalf("the author's piece inside a block = %q, %v, %v", text, seams, err)
	}
	text, _, err = TakeInto("Один.\nДва.", version, Take{From: "Автор кончил.", To: "Автор кончил.", Place: &Scene{Place: "Один."}, After: true})
	if err != nil || text != "Один.\n\nАвтор кончил.\nДва." {
		t.Fatalf("after a place = %q, %v", text, err)
	}
	for name, take := range map[string]Take{
		"no piece":                 {Place: &Scene{Place: "Один."}},
		"a piece not in it":        {From: "Нет такого.", To: "Нет такого."},
		"a whole with a piece":     {Whole: true, From: "Автор начал.", To: "Автор начал."},
		"a piece across the marks": {From: "Автор начал.", To: "Кира ушла в ночь."},
	} {
		if _, _, err := TakeInto("Один.\nДва.", version, take); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}

// specs/2026-11-08-italic-bold-in-text.md TF2.2, TF2.3: a piece of a version is named through its faces and keeps them
// whole at its edges; in place of a place inside a face of the working text it takes that face, and the seams stand
// where the piece meets the text.
func TestATakeKeepsTheFaces(t *testing.T) {
	version := "Утро. ⟨Кот спал на окне⟩ и ждал."
	working := "Ночь. Он ⟨шёл домой⟩ быстро."
	text, seams, err := TakeInto(working, version, Take{From: "спал на", To: "и ждал.", Place: &Scene{Place: "домой"}})
	if err != nil || text != "Ночь. Он ⟨шёл спал на окне и ждал.⟩ быстро." || !ValidMarks(text) || len(seams) != 2 {
		t.Fatalf("a piece in place of a place inside a face = %q, %v, %v", text, seams, err)
	}
	if before := WithoutMarks(text[:seams[0]]); before != "Ночь. Он шёл " {
		t.Fatalf("the first seam stands after %q", before)
	}
	if before := WithoutMarks(text[:seams[1]]); before != "Ночь. Он шёл спал на окне и ждал." {
		t.Fatalf("the second seam stands after %q", before)
	}
	text, _, err = TakeInto("Один ⟨раз\nдва⟩.", version, Take{From: "Кот спал", To: "Кот спал", Place: &Scene{Place: "Один раз"}, After: true})
	if err != nil || text != "Один ⟨раз⟩\n\n⟨Кот спал\nдва⟩." {
		t.Fatalf("a piece after a place = %q, %v", text, err)
	}
}

// specs/2026-11-11-ux9-writing-b.md WB3.1: a version is named by the place its action changed — the first words of what
// stood there, whole words, without the marks of the helper and the faces of the author; nothing when nothing went.
func TestAVersionNamesThePlaceItsActionChanged(t *testing.T) {
	for _, each := range []struct{ before, after, want string }{
		{"Ольм со шрамом через губу вышел.", "Ольм со шрамом на щеке вышел.", "через губу"},
		{"Ворота стояли тёмные.\n\n⟦Кира ушла через восточный выход.⟧\n\nКонец.", "Ворота стояли тёмные.\n\nКонец.", "Кира ушла через восточный выход."},
		{"Один два три четыре пять шесть семь восемь.", "Девять.", "Один два три четыре пять шесть…"},
		{"Кира ⟨шла⟩ к воротам.", "Кира бежала к воротам.", "шла"},
		{"Кира шла.", "Кира шла. Потом бежала.", ""},
		{"Кира шла.", "Кира шла.", ""},
	} {
		if got := ChangedPlace(each.before, each.after); got != each.want {
			t.Fatalf("ChangedPlace(%q, %q) = %q, want %q", each.before, each.after, got, each.want)
		}
	}
}
