// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package obsidianvault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
)

// write puts a file of the test under its own temporary directory.
func write(t *testing.T, root, name, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.2: the look goes by the entries of the directories and follows no link — out of
// the vault, into it, in a loop; a FIFO named .md, a folder on a dot, a file that is not a note are counted, not read; a
// note named .MD is a note, a name in NFD comes back in NFC and reads by it; the root is closed after the operation.
func TestTheLookFollowsNoLinkAndReadsNoFIFO(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "Чужое.md", "Чужая заметка.")
	vault := filepath.Join(t.TempDir(), "Северный предел")
	write(t, vault, "Книга 1/01 Застава.md", "Ольга вышла к заставе.")
	write(t, vault, "Книга 1/02 Переправа.MD", "Мирон ждал у переправы.")
	write(t, vault, "Книга 1/обложка.png", "png")
	write(t, vault, ".obsidian/app.md", "служебное")
	write(t, vault, "Персонажи/Йорген.md", "Йорген — кузнец.")
	nfd := "Персонажи/И\u0306орген старший.md"
	write(t, vault, nfd, "Старший брат.")
	for link, target := range map[string]string{
		"Наружу":            outside,
		"Книга 1/Внутрь":    filepath.Join(vault, "Персонажи"),
		"Книга 1/Петля":     "..",
		"Книга 1/Ссылка.md": filepath.Join(vault, "Книга 1/01 Застава.md"),
	} {
		if err := os.Symlink(target, filepath.Join(vault, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	fifo := filepath.Join(vault, "Книга 1", "Труба.md")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}

	opened, err := Files{}.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	tree := obsidian.NewTree(filepath.Base(vault))
	done := make(chan error, 1)
	go func() { done <- opened.Walk(context.Background(), obsidian.Root, tree.Add) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the look does not end")
	}
	got := map[string]string{}
	for _, folder := range tree.Folders() {
		got[folder.Path] = fmt.Sprintf("%d+%d %v", folder.Notes, folder.Below, folder.Skipped)
	}
	want := map[string]string{
		obsidian.Root: "0+4 map[hidden:1 link:1]",
		"Книга 1":     "2+0 map[link:4 not_note:1]",
		"Персонажи":   "2+0 map[]",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tree = %v", got)
	}
	data, skip := opened.Read(context.Background(), "Персонажи/Йорген старший.md")
	if skip != "" || string(data) != "Старший брат." {
		t.Fatalf("the note in NFD = %q %q", data, skip)
	}
	for notePath, want := range map[string]obsidian.Skip{
		"Книга 1/Труба.md":         obsidian.SkipLink,
		"Книга 1/Ссылка.md":        obsidian.SkipLink,
		"Книга 1/Внутрь/Йорген.md": obsidian.SkipUnreadable,
		"Наружу/Чужое.md":          obsidian.SkipUnreadable,
		"Нет такой.md":             obsidian.SkipUnreadable,
	} {
		if data, skip := opened.Read(context.Background(), notePath); skip != want || data != nil {
			t.Fatalf("%s = %q %q, want %q", notePath, data, skip, want)
		}
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := opened.Walk(context.Background(), obsidian.Root, tree.Add); err == nil {
		t.Fatal("the root is open after its operation")
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.3: a folder of a request is a folder of the vault without a link on its way; a path
// that goes through a link, a file and a folder that is not are refused; its NFD form is the same folder.
func TestAFolderOfARequestIsAFolderWithoutALink(t *testing.T) {
	vault := t.TempDir()
	write(t, vault, "Северный предел/Книга 1/01 Застава.md", "Застава.")
	if err := os.Symlink(filepath.Join(vault, "Северный предел"), filepath.Join(vault, "Ссылка")); err != nil {
		t.Fatal(err)
	}
	opened, err := Files{}.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	for _, folder := range []string{obsidian.Root, "Северный предел", "Северный предел/Книга 1"} {
		if err := opened.Folder(context.Background(), folder); err != nil {
			t.Fatalf("%s: %v", folder, err)
		}
	}
	nfd, err := obsidian.Relative("Северный предел/Книга 1")
	if err != nil || opened.Folder(context.Background(), nfd) != nil {
		t.Fatalf("the NFD form of a folder: %q %v", nfd, err)
	}
	for _, folder := range []string{"Ссылка", "Ссылка/Книга 1", "Северный предел/Книга 1/01 Застава.md", "Нет"} {
		if err := opened.Folder(context.Background(), folder); !errors.Is(err, obsidian.ErrPath) {
			t.Fatalf("%s is taken: %v", folder, err)
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB1.3: a vault opens only as a folder that reads, and no error names its path.
func TestAVaultOpensOnlyAsAFolderThatReads(t *testing.T) {
	root := t.TempDir()
	write(t, root, "заметка.md", "текст")
	locked := filepath.Join(root, "Закрытая")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	for vaultPath, want := range map[string]obsidian.Reason{
		filepath.Join(root, "Нет"):        obsidian.ReasonNotFound,
		filepath.Join(root, "заметка.md"): obsidian.ReasonNotFolder,
		"относительная/папка":             obsidian.ReasonNotFound,
	} {
		_, err := Files{}.Open(vaultPath)
		if obsidian.ReasonOf(err) != want {
			t.Fatalf("%s: %v, want %s", vaultPath, err, want)
		}
		if err != nil && filepath.IsAbs(vaultPath) && containsPath(err.Error(), vaultPath) {
			t.Fatalf("the error names the path: %v", err)
		}
	}
	if os.Geteuid() != 0 {
		_, err := Files{}.Open(locked)
		if obsidian.ReasonOf(err) != obsidian.ReasonUnreadable || containsPath(err.Error(), locked) {
			t.Fatalf("a folder that does not read: %v", err)
		}
	}
}

// containsPath says an error names a folder: its last name stands in the text.
func containsPath(text, value string) bool {
	return strings.Contains(text, filepath.Base(value))
}

// specs/2026-11-13-desk-obsidian.md OB0.1: the walk stops when its context ends — between the batches of a folder, before
// the next folder, and with the end of the context as its answer —, so the time of an operation ends its reading too.
func TestTheWalkStopsWhenItsTimeEnds(t *testing.T) {
	walk := func(vault string) (int, error) {
		opened, err := Files{}.Open(vault)
		if err != nil {
			t.Fatal(err)
		}
		defer opened.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		visits := 0
		err = opened.Walk(ctx, obsidian.Root, func(_ string, found obsidian.Found) (bool, bool) {
			visits++
			cancel()
			return found.Dir, false
		})
		return visits, err
	}

	// A folder of more notes than a batch: the walk reads one batch and answers the end of its context.
	wide := t.TempDir()
	for index := range readBatch*2 + 10 {
		write(t, wide, fmt.Sprintf("%04d.md", index), "x")
	}
	if visits, err := walk(wide); !errors.Is(err, context.Canceled) || visits != readBatch {
		t.Fatalf("a wide folder: %d entries, %v", visits, err)
	}

	// Folders to enter after the one whose batch ended the time: none of them is read.
	deep := t.TempDir()
	write(t, deep, "Книга 1/01 Застава.md", "Застава.")
	write(t, deep, "Книга 2/01 Весна.md", "Весна.")
	if visits, err := walk(deep); !errors.Is(err, context.Canceled) || visits != 2 {
		t.Fatalf("folders after the end: %d entries, %v", visits, err)
	}
}
