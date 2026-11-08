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
