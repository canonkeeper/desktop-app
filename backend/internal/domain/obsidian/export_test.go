// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// specs/2026-11-15-desk-obsidian-export.md OE2.6: a name of a file of an export reads the same on Windows and on Linux, and
// no sign of it breaks a link of Obsidian.
func TestTheNameOfAFileOfAnExportReadsOnEverySystem(t *testing.T) {
	for _, item := range []struct{ name, want string }{
		{"Белый порт", "Белый порт"},
		{`Глава 12: «Белый» порт?`, "Глава 12 «Белый» порт"},
		{`a\b/c*d"e<f>g|h#i^j[k]l`, "a b c d e f g h i j k l"},
		{"Тень\tи\nсвет\x00", "Тень и свет"},
		{" .Скрытая. ", "Скрытая"},
		{"...", "Без имени"},
		{"[]|#^", "Без имени"},
		{"", "Без имени"},
		{"CON", "_CON"},
		{"con", "_con"},
		{"Aux.Prime", "_Aux.Prime"},
		{"nul.txt", "_nul.txt"},
		{"COM1", "_COM1"},
		{"lpt9", "_lpt9"},
		{"COM0", "COM0"},
		{"COM10", "COM10"},
		{"Конь", "Конь"},
		// NFD from macOS is NFC.
		{"\u0418\u0306", "\u0419"},
	} {
		if got := ExportName(item.name); got != item.want {
			t.Errorf("ExportName(%q) = %q, want %q", item.name, got, item.want)
		}
	}
	// Not longer than 120 signs and 200 bytes of UTF-8: a file system keeps 255 bytes of a name, and a name in _Удалено/ takes
	// « 2026-11-15 (999).md» after it.
	for sign, want := range map[string]int{"z": MaxExportNameRunes, "я": MaxExportNameBytes / 2, "龍": MaxExportNameBytes / 3, "🜂": MaxExportNameBytes / 4} {
		got := ExportName(strings.Repeat(sign, MaxExportNameRunes+30))
		if len([]rune(got)) != want || len(got) > MaxExportNameBytes {
			t.Errorf("a long name of %q keeps %d signs and %d bytes, want %d signs", sign, len([]rune(got)), len(got), want)
		}
		if deleted := DeletedNames(got, time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)); len(deleted[len(deleted)-1]) > 255 {
			t.Errorf("a name in _Удалено/ of %d bytes", len(deleted[len(deleted)-1]))
		}
	}
	if got := ExportName(strings.Repeat("я", MaxExportNameRunes-1) + ". хвост"); strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ") {
		t.Fatalf("a cut name ends with a dot or a space: %q", got)
	}
}

func TestAFreeNameTakesTheNextNumberTheCaseIgnoredByTheCaller(t *testing.T) {
	taken := map[string]bool{FoldKey("Герои/Анна"): true, FoldKey("Герои/Анна (2)"): true}
	got := FreeName("Герои/АННА", func(candidate string) bool { return taken[FoldKey(candidate)] })
	if got != "Герои/АННА (3)" {
		t.Fatalf("FreeName = %q", got)
	}
	if FreeName("x", func(string) bool { return true }) != "" {
		t.Fatal("a name with no free number is not given")
	}
	day := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	names := DeletedNames("Анна", day)
	if names[0] != "Анна.md" || names[1] != "Анна 2026-11-15.md" || names[2] != "Анна 2026-11-15 (2).md" {
		t.Fatalf("the names in _Удалено/: %q", names[:3])
	}
}

// OE2.5: the fingerprint does not change when a synchronizer changes the ends of the lines or adds the mark of the byte order,
// nor when Obsidian rewrites the target of a link; it changes with a word, with a caption and with a link that became text.
func TestTheFingerprintOfAReferenceIsWhatTheAuthorReads(t *testing.T) {
	base := "---\ncanonkeeper-key: \"анна\"\n---\nТекст.\n\n- [[CanonKeeper/Сага/Герои/Борис|Борис]] — Вместе в 2 событиях\n"
	print := ExportPrint([]byte(base))
	same := map[string]string{
		"CRLF":               strings.ReplaceAll(base, "\n", "\r\n"),
		"CR":                 strings.ReplaceAll(base, "\n", "\r"),
		"the mark of UTF-8":  "\uFEFF" + base,
		"a target rewritten": strings.Replace(base, "CanonKeeper/Сага/Герои/Борис", "Борис Сватов", 1),
		"a target shortened": strings.Replace(base, "CanonKeeper/Сага/Герои/Борис|", "Борис|", 1),
	}
	for name, text := range same {
		if ExportPrint([]byte(text)) != print {
			t.Errorf("%s changed the fingerprint", name)
		}
	}
	other := map[string]string{
		"a word":              strings.Replace(base, "Текст.", "Текст автора.", 1),
		"a caption":           strings.Replace(base, "|Борис]]", "|Боря]]", 1),
		"a link became text":  strings.Replace(base, "[[CanonKeeper/Сага/Герои/Борис|Борис]]", "Борис", 1),
		"a line added":        base + "Моя мысль.\n",
		"a space in the text": strings.Replace(base, "Текст.", "Текст. ", 1),
	}
	for name, text := range other {
		if ExportPrint([]byte(text)) == print {
			t.Errorf("%s did not change the fingerprint", name)
		}
	}
	if ExportSum([]byte(base)) == ExportSum([]byte(strings.Replace(base, "CanonKeeper/Сага/Герои/Борис", "Борис", 1))) {
		t.Fatal("the SHA-256 of the bytes must see a target")
	}
}

// OE2.8: the manifest reads back what it wrote, and a manifest a run cannot trust does not read.
func TestTheManifestReadsWhatItWroteAndRefusesWhatItCannotTrust(t *testing.T) {
	manifest := Manifest{
		Format: ExportFormat, Saga: "saga-1", At: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC), Complete: true,
		Books: []ManifestBook{{Book: "book-1", Folder: "Главы/Туманный берег"}},
		Files: []ManifestFile{
			{Kind: ExportIndex, Key: ExportIndexKey, Path: "Сага.md", Print: "p1", Sum: "s1"},
			{Kind: ExportNode, Key: "анна", Path: "Герои/Анна.md", Print: "p2", Sum: "s2"},
		},
	}
	read, err := ReadManifest(manifest.Encode())
	if err != nil || read.Saga != "saga-1" || len(read.Files) != 2 || read.Files[1].Path != "Герои/Анна.md" || !read.Complete {
		t.Fatalf("ReadManifest = %+v, %v", read, err)
	}
	bad := map[string]string{
		"not JSON":          "{",
		"another format":    `{"format":2,"saga":"s","files":[]}`,
		"no saga":           `{"format":1,"saga":"","files":[]}`,
		"a path out":        `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":"../a.md"}]}`,
		"a hidden path":     `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":".canonkeeper/a.md"}]}`,
		"an absolute path":  `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":"/a.md"}]}`,
		"not a note":        `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":"a.txt"}]}`,
		"a key twice":       `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":"a.md"},{"kind":"node","key":"a","path":"b.md"}]}`,
		"a path twice":      `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":"A.md"},{"kind":"node","key":"b","path":"a.md"}]}`,
		"an unknown kind":   `{"format":1,"saga":"s","files":[{"kind":"other","key":"a","path":"a.md"}]}`,
		"a backslash":       `{"format":1,"saga":"s","files":[{"kind":"node","key":"a","path":"a\\b.md"}]}`,
		"two JSON values":   `{"format":1,"saga":"s","files":[]}{}`,
		"a book folder out": `{"format":1,"saga":"s","books":[{"book":"b","folder":"../x"}],"files":[]}`,
	}
	for name, data := range bad {
		if _, err := ReadManifest([]byte(data)); !errors.Is(err, ErrExportManifestUnreadable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
