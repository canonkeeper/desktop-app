// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"canonkeeper/backend/internal/domain/authormaterials"
)

// specs/2026-11-13-desk-obsidian.md OB0.3: a path of a request is relative, valid, in NFC and without names on a dot; a
// path no system reads as another.
func TestAPathOfARequestIsAPathOfTheVault(t *testing.T) {
	for value, want := range map[string]string{
		"": Root, ".": Root, "Северный предел": "Северный предел", "Северный предел/Книга 1": "Северный предел/Книга 1",
		// NFD «й» comes back in NFC.
		"Северны\u0438\u0306 предел": "Северный предел",
		// A space at the edge of a name is a part of the name: another folder than the one without it.
		"Книга 1 ": "Книга 1 ", " Книга 1/ Глава.md": " Книга 1/ Глава.md",
	} {
		if got, err := Relative(value); err != nil || got != want {
			t.Fatalf("%q = %q %v, want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"../x", "/etc", "C:/x", `a\b`, ".trash/x", "a/.obsidian", "a//b", "a/", "a/./b", "a\x00b", "a/../b"} {
		if _, err := Relative(value); !errors.Is(err, ErrPath) {
			t.Fatalf("%q is taken: %v", value, err)
		}
	}
}

func TestNotesStandInTheNaturalOrder(t *testing.T) {
	names := []string{"Глава 10.md", "глава 2.md", "Глава 1.md", "Пролог/а.md", "Глава 2.md", "Эпилог.md"}
	slices.SortFunc(names, func(left, right string) int {
		if NaturalLess(left, right) {
			return -1
		}
		if NaturalLess(right, left) {
			return 1
		}
		return 0
	})
	want := []string{"Глава 1.md", "Глава 2.md", "глава 2.md", "Глава 10.md", "Пролог/а.md", "Эпилог.md"}
	if !slices.Equal(names, want) {
		t.Fatalf("order = %q", names)
	}
}

// specs/2026-11-13-desk-obsidian.md OB2.1, OB2.2: the tree counts the notes of a folder and below it without reading one,
// counts what it skips by its reasons, holds only folders with notes, and gives a hint to the folders of the first two
// levels; past its limits it is cut with its numbers.
func TestTheTreeOfAVault(t *testing.T) {
	tree := NewTree("Северный предел")
	add := func(dir string, found Found) {
		if enter, stop := tree.Add(dir, found); stop || enter != found.Dir && !found.Link && found.Name[0] != '.' {
			t.Fatalf("%s/%s: enter %v stop %v", dir, found.Name, enter, stop)
		}
	}
	for _, folder := range []string{"Книга 1", "Персонажи", "Шаблоны", "Пустая", ".obsidian"} {
		add(Root, Found{Name: folder, Dir: true})
	}
	add(Root, Found{Name: "Ссылка", Link: true})
	add(Root, Found{Name: "fifo.md"})
	add("Книга 1", Found{Name: "_book.md", Regular: true})
	add("Книга 1", Found{Name: "01 Застава.md", Regular: true, Size: 20})
	add("Книга 1", Found{Name: "02 Переправа.MD", Regular: true})
	add("Книга 1", Found{Name: "обложка.png", Regular: true})
	add("Книга 1", Found{Name: "Карта.excalidraw.md", Regular: true})
	add("Книга 1", Found{Name: "Облако.md", Regular: true, CloudOnly: true})
	add("Книга 1", Found{Name: "Большая.md", Regular: true, Size: MaxNoteBytes + 1})
	add("Книга 1", Found{Name: "Части", Dir: true})
	add("Книга 1/Части", Found{Name: "Интерлюдия.md", Regular: true})
	add("Персонажи", Found{Name: "Мирон.md", Regular: true})
	add("Шаблоны", Found{Name: "Глава.md", Regular: true})
	got := map[string]string{}
	for _, folder := range tree.Folders() {
		got[folder.Path] = fmt.Sprintf("%d+%d %v", folder.Notes, folder.Below, folder.Skipped)
	}
	want := map[string]string{
		Root:            "0+6 map[hidden:1 link:2]",
		"Книга 1":       "3+1 map[cloud_only:1 not_note:1 service:1 too_big:1]",
		"Книга 1/Части": "1+0 map[]",
		"Персонажи":     "1+0 map[]",
		"Шаблоны":       "1+0 map[]",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tree = %v", got)
	}
	if tree.Truncated || tree.Entries() != 18 {
		t.Fatalf("truncated %v entries %d", tree.Truncated, tree.Entries())
	}

	deep := NewTree("Хранилище")
	dir := Root
	for level := 1; level <= MaxDepth+1; level++ {
		enter, _ := deep.Add(dir, Found{Name: fmt.Sprintf("п%d", level), Dir: true})
		if enter != (level <= MaxDepth) {
			t.Fatalf("level %d entered %v", level, enter)
		}
		dir = Join(dir, fmt.Sprintf("п%d", level))
	}
	if !deep.Truncated {
		t.Fatal("a tree deeper than its limit is not cut")
	}
	wide := NewTree("Хранилище")
	for entry := 0; entry < MaxEntries; entry++ {
		if _, stop := wide.Add(Root, Found{Name: "x.png", Regular: true}); stop {
			t.Fatalf("stopped at %d", entry)
		}
	}
	if _, stop := wide.Add(Root, Found{Name: "x.md", Regular: true}); !stop || !wide.Truncated || wide.Entries() != MaxEntries {
		t.Fatalf("stop %v truncated %v entries %d", stop, wide.Truncated, wide.Entries())
	}
}

func TestTheHintsOfTheFolders(t *testing.T) {
	for _, test := range []struct {
		name     string
		depth    int
		bookNote bool
		linked   bool
		want     *Hint
	}{
		{"Персонажи", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindCharacter}},
		{"герои", 2, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindCharacter}},
		{"CHARACTERS", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindCharacter}},
		{"Места", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindPlace}},
		{"Locations", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindPlace}},
		{"Навыки", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindSkill}},
		{"Skills", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindSkill}},
		{"Предметы", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindItem}},
		{"Items", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindItem}},
		{"Фракции", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindFaction}},
		{"Factions", 1, false, false, &Hint{Kind: HintMaterials, Material: authormaterials.KindFaction}},
		{"Книга 1", 1, true, false, &Hint{Kind: HintBook}},
		{"Том", 2, false, true, &Hint{Kind: HintBook}},
		{"Templates", 1, false, false, &Hint{Kind: HintSkip}},
		{"Шаблоны", 1, false, false, &Hint{Kind: HintSkip}},
		{"Daily", 1, false, false, &Hint{Kind: HintSkip}},
		{"Дневник", 1, false, false, &Hint{Kind: HintSkip}},
		{"Вложения", 2, false, false, &Hint{Kind: HintSkip}},
		{"Attachments", 1, false, false, &Hint{Kind: HintSkip}},
		{"Разное", 1, false, false, &Hint{Kind: HintSkip}},
		{"Персонажи", 3, false, false, nil},
		{"Северный предел", 0, true, false, nil},
	} {
		got := HintOf(test.name, test.depth, test.bookNote, test.linked)
		if fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Fatalf("%s at %d = %+v, want %+v", test.name, test.depth, got, test.want)
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB4.1: no book holds another book or a linked folder; the deepest choice takes a note.
func TestAChoiceOfFolders(t *testing.T) {
	if _, err := (Choice{Books: []string{"Сага", "Сага/Книга 1"}}).Check(nil); !errors.As(err, new(Overlap)) {
		t.Fatalf("nested books = %v", err)
	}
	var overlap Overlap
	if _, err := (Choice{Books: []string{"Сага/Книга 2"}}).Check([]string{"Сага"}); !errors.As(err, &overlap) || overlap.Outer != "Сага" || overlap.Inner != "Сага/Книга 2" {
		t.Fatalf("a book inside a linked folder = %v %+v", err, overlap)
	}
	if _, err := (Choice{Books: []string{"../Сага"}}).Check(nil); !errors.Is(err, ErrPath) {
		t.Fatalf("a path out of the vault = %v", err)
	}
	choice, err := Choice{
		Books:     []string{"Книга 1"},
		Materials: []MaterialFolder{{Path: "Книга 1/Персонажи", Kind: authormaterials.KindCharacter}},
		Exclude:   []string{"Книга 1/Черновики", "Книга 1/План.md"},
	}.Check([]string{"Книга 1"})
	if err != nil {
		t.Fatal(err)
	}
	for note, want := range map[string]string{
		"Книга 1/Застава.md":            "Книга 1 true",
		"Книга 1/Персонажи/Мирон.md":    "Книга 1/Персонажи false",
		"Книга 1/Черновики/Набросок.md": "none",
		"Книга 1/План.md":               "none",
		"Дневник/2026.md":               "none",
	} {
		folder, book, ok := choice.Owner(note)
		got := "none"
		if ok {
			got = fmt.Sprintf("%s %v", folder, book)
		}
		if got != want {
			t.Fatalf("%s goes to %s, want %s", note, got, want)
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.2, OB0.3: an entry whose name no request can name — not UTF-8, with a control sign,
// a backslash or a colon — is counted as unreadable, not entered and not read.
func TestANameNoRequestCanNameIsNotRead(t *testing.T) {
	for _, name := range []string{"a\xffb.md", "a\nb", `a\b.md`, "Глава 1: Начало.md"} {
		for _, found := range []Found{{Name: name, Regular: true}, {Name: name, Dir: true}} {
			if kind, skip := Classify(found); kind != KindSkipped || skip != SkipUnreadable {
				t.Fatalf("%q: %v %q", name, kind, skip)
			}
		}
	}
}
