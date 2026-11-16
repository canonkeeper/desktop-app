// SPDX-License-Identifier: AGPL-3.0-only

package obsidianvault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"canonkeeper/backend/internal/domain/obsidian"
)

// specs/2026-11-15-desk-obsidian-export.md OE1.9: a folder that holds the manifest of an export — of this saga, of another or of
// another computer — is not entered by the walk, a request that names it or a folder under it is not a path of the vault, and
// a note under it does not read; the folder above it and the notes beside it read as before.
func TestTheReadingOfTheBridgeDoesNotSeeAFolderOfAnExport(t *testing.T) {
	ctx := context.Background()
	vault := filepath.Join(t.TempDir(), "Хранилище")
	writeFile(t, vault, "Пролог.md", "Ольга вышла к заставе.")
	writeFile(t, vault, "CanonKeeper/Северный предел/.canonkeeper/manifest.json", `{"format":1}`)
	writeFile(t, vault, "CanonKeeper/Северный предел/Герои/Мара.md", "справка")
	writeFile(t, vault, "Архив/.canonkeeper/manifest.json", `{"format":1}`)
	writeFile(t, vault, "Архив/Мара.md", "справка другого компьютера")
	writeFile(t, vault, "CanonKeeper/Заметка рядом.md", "заметка автора")
	opened, err := Files{}.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	tree := obsidian.NewTree("Хранилище")
	if err := opened.Walk(ctx, obsidian.Root, tree.Add); err != nil {
		t.Fatal(err)
	}
	notes := map[string]int{}
	for _, folder := range tree.Folders() {
		notes[folder.Path] = folder.Notes
	}
	if len(notes) != 2 || notes[obsidian.Root] != 1 || notes["CanonKeeper"] != 1 {
		t.Fatalf("the folders with notes = %v", notes)
	}
	for _, folder := range []string{"CanonKeeper/Северный предел", "CanonKeeper/Северный предел/Герои", "Архив"} {
		if err := opened.Folder(ctx, folder); !errors.Is(err, obsidian.ErrPath) {
			t.Fatalf("Folder(%s) = %v", folder, err)
		}
		if err := opened.Walk(ctx, folder, tree.Add); !errors.Is(err, obsidian.ErrPath) {
			t.Fatalf("Walk(%s) = %v", folder, err)
		}
	}
	for _, note := range []string{"CanonKeeper/Северный предел/Герои/Мара.md", "Архив/Мара.md"} {
		if _, skip := opened.Read(ctx, note); skip == "" {
			t.Fatalf("%s reads", note)
		}
	}
	if err := opened.Folder(ctx, "CanonKeeper"); err != nil {
		t.Fatalf("the folder above a folder of an export = %v", err)
	}
	if data, skip := opened.Read(ctx, "CanonKeeper/Заметка рядом.md"); skip != "" || string(data) != "заметка автора" {
		t.Fatalf("a note beside a folder of an export = %q %q", data, skip)
	}
}

// writeFile puts a file of the test under its own temporary directory, on every system.
func writeFile(t *testing.T, root, name, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
