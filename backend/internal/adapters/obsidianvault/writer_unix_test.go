// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package obsidianvault

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

// snapshot is every entry under a directory, its links unfollowed, with the bytes of its files: what a test says did not
// change.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, full)
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(full)
			entries[relative] = "link:" + target
		case info.IsDir():
			entries[relative] = "dir"
		default:
			data, err := os.ReadFile(full)
			if err != nil {
				return err
			}
			entries[relative] = "file:" + info.Mode().Perm().String() + ":" + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func openWriter(t *testing.T, vault string) *Writer {
	t.Helper()
	opened, err := Writers{}.OpenWriter(vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened.(*Writer)
}

func readBack(t *testing.T, full string) string {
	t.Helper()
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// specs/2026-11-15-desk-obsidian-export.md OE1.3: the folder of the export is made under the vault and written only under
// it, a new file with the mode of a note of the author, through a temporary file of the folder of the app that a run
// removes; nothing out of the folder changes.
func TestAnExportWritesOnlyUnderItsFolder(t *testing.T) {
	ctx := context.Background()
	vault := filepath.Join(t.TempDir(), "Хранилище")
	write(t, vault, "Мои заметки/Мысль.md", "моя мысль")
	before := snapshot(t, vault)
	writer := openWriter(t, vault)
	if state, err := writer.Folder(ctx, "CanonKeeper/Северный предел"); err != nil || state != service.ExportFolderMissing {
		t.Fatalf("Folder = %v, %v", state, err)
	}
	if err := writer.Open(ctx, "CanonKeeper/Северный предел"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(ctx, "Герои/Мара.md", []byte("справка\n"), ""); err != nil {
		t.Fatal(err)
	}
	if err := writer.Keep(ctx, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(vault, "CanonKeeper", "Северный предел")
	info, err := os.Stat(filepath.Join(export, "Герои", "Мара.md"))
	if err != nil || info.Mode().Perm() != 0o644&^currentUmask(t) || readBack(t, filepath.Join(export, "Герои", "Мара.md")) != "справка\n" {
		t.Fatalf("the reference: %v %v", info, err)
	}
	// What a run before left in the folder of the app goes at the start of the next one.
	write(t, export, ".canonkeeper/tmp/left.tmp", "остаток")
	if err := writer.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(export, ".canonkeeper", "tmp")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the temporary files stay: %v", err)
	}
	after := snapshot(t, vault)
	for name, value := range before {
		if after[name] != value {
			t.Fatalf("%s out of the folder of the export changed", name)
		}
	}
	for name := range after {
		if _, known := before[name]; !known && !strings.HasPrefix(name, "CanonKeeper") {
			t.Fatalf("%s is written out of the folder of the export", name)
		}
	}
	if readBack(t, filepath.Join(export, ".canonkeeper", "manifest.json")) != "{}\n" {
		t.Fatal("the manifest is not where it is read")
	}
	if notes, err := writer.Notes(ctx); err != nil || len(notes) != 1 || notes[0] != "Герои/Мара.md" {
		t.Fatalf("Notes = %q, %v", notes, err)
	}
}

// currentUmask is the umask of the process of the test.
func currentUmask(t *testing.T) fs.FileMode {
	probe := filepath.Join(t.TempDir(), "umask-probe")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o777)
	if err != nil {
		return 0o022
	}
	info, _ := file.Stat()
	_ = file.Close()
	return 0o777 &^ info.Mode().Perm()
}

// OE1.3: a link on the way to the folder of the export refuses the operation, and nothing changes — out of the vault and in
// it; a link inside the folder skips its file by the reason link and writes nothing through it.
func TestALinkOnTheWayRefusesAndALinkInsideSkips(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	write(t, outside, "Чужое.md", "чужое")
	vault := filepath.Join(t.TempDir(), "Хранилище")
	write(t, vault, "Внутри/Заметка.md", "заметка")
	if err := os.Symlink(outside, filepath.Join(vault, "CanonKeeper")); err != nil {
		t.Fatal(err)
	}
	beforeOut, beforeIn := snapshot(t, outside), snapshot(t, vault)
	writer := openWriter(t, vault)
	if _, err := writer.Folder(ctx, "CanonKeeper/Сага"); !errors.Is(err, obsidian.ErrExportDirUnsafe) {
		t.Fatalf("Folder through a link: %v", err)
	}
	if err := writer.Open(ctx, "CanonKeeper/Сага"); !errors.Is(err, obsidian.ErrExportDirUnsafe) {
		t.Fatalf("Open through a link: %v", err)
	}
	if state, err := writer.Folder(ctx, "CanonKeeper"); err != nil || state != service.ExportFolderTaken {
		t.Fatalf("a link at the name of the folder: %v, %v", state, err)
	}
	if !reflectEqual(snapshot(t, outside), beforeOut) || !reflectEqual(snapshot(t, vault), beforeIn) {
		t.Fatal("a refused export changed something")
	}

	// Inside the folder of the export: a folder that is a link to elsewhere in the vault.
	if err := os.Remove(filepath.Join(vault, "CanonKeeper")); err != nil {
		t.Fatal(err)
	}
	writer = openWriter(t, vault)
	if err := writer.Open(ctx, "CanonKeeper/Сага"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(vault, "Внутри"), filepath.Join(vault, "CanonKeeper", "Сага", "Герои")); err != nil {
		t.Fatal(err)
	}
	inside := snapshot(t, filepath.Join(vault, "Внутри"))
	var skipped service.ExportSkipped
	if err := writer.Write(ctx, "Герои/Заметка.md", []byte("справка"), ""); !errors.As(err, &skipped) || skipped.Skip != obsidian.ExportLink {
		t.Fatalf("a write through a link: %v", err)
	}
	if exists, skip := writer.Stat(ctx, "Герои/Заметка.md"); !exists || skip != obsidian.ExportLink {
		t.Fatalf("Stat through a link: %v %q", exists, skip)
	}
	if _, skip := writer.Read(ctx, "Герои/Заметка.md"); skip != obsidian.ExportLink {
		t.Fatalf("Read through a link: %q", skip)
	}
	if !reflectEqual(snapshot(t, filepath.Join(vault, "Внутри")), inside) {
		t.Fatal("a write went through a link")
	}
	// A file of the folder that is a link is not read, replaced or moved.
	if err := os.Symlink(filepath.Join(outside, "Чужое.md"), filepath.Join(vault, "CanonKeeper", "Сага", "Ссылка.md")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(ctx, "Ссылка.md", []byte("x"), "print"); !errors.As(err, &skipped) || skipped.Skip != obsidian.ExportLink {
		t.Fatalf("a replacement of a link: %v", err)
	}
	if _, err := writer.Move(ctx, "Ссылка.md", []string{"Ссылка.md"}, "print"); !errors.As(err, &skipped) {
		t.Fatalf("a move of a link: %v", err)
	}
	if readBack(t, filepath.Join(outside, "Чужое.md")) != "чужое" {
		t.Fatal("a file out of the vault changed")
	}
}

func reflectEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if left[key] != right[key] {
			return false
		}
	}
	return true
}

// OE1.4: right before a replacement the file is read again — one the author changed since the plan read it is not
// touched —, a new reference never takes the place of a file there, and a move goes only with the file the plan read, under
// a free name of _Удалено/.
func TestAWriteChecksTheFileRightBeforeItsRename(t *testing.T) {
	ctx := context.Background()
	vault := filepath.Join(t.TempDir(), "Хранилище")
	export := filepath.Join(vault, "CanonKeeper", "Сага")
	write(t, export, "Герои/Мара.md", "наша справка\n")
	writer := openWriter(t, vault)
	if err := writer.Open(ctx, "CanonKeeper/Сага"); err != nil {
		t.Fatal(err)
	}
	planned := obsidian.ExportPrint([]byte("наша справка\n"))
	// The author writes into the file between the reading of the plan and the replacement.
	write(t, export, "Герои/Мара.md", "наша справка\nмоя правка\n")
	if err := writer.Write(ctx, "Герои/Мара.md", []byte("свежая\n"), planned); !errors.Is(err, service.ErrChangedMeanwhile) {
		t.Fatalf("a replacement over a change: %v", err)
	}
	if readBack(t, filepath.Join(export, "Герои", "Мара.md")) != "наша справка\nмоя правка\n" {
		t.Fatal("the change of the author is lost")
	}
	if _, err := writer.Move(ctx, "Герои/Мара.md", []string{"Мара.md"}, planned); !errors.Is(err, service.ErrChangedMeanwhile) {
		t.Fatalf("a move over a change: %v", err)
	}
	// A file of the author put at the path of a new reference stays his.
	write(t, export, "Герои/Сава.md", "файл автора\n")
	if err := writer.Write(ctx, "Герои/Сава.md", []byte("справка\n"), ""); !errors.Is(err, service.ErrOccupied) {
		t.Fatalf("a new reference over a file: %v", err)
	}
	if readBack(t, filepath.Join(export, "Герои", "Сава.md")) != "файл автора\n" {
		t.Fatal("the file of the author is written over")
	}
	// A replacement of the file the plan read is atomic: the whole new text, no temporary file left.
	edited := obsidian.ExportPrint([]byte("наша справка\nмоя правка\n"))
	if err := writer.Write(ctx, "Герои/Мара.md", []byte("свежая\n"), edited); err != nil {
		t.Fatal(err)
	}
	if readBack(t, filepath.Join(export, "Герои", "Мара.md")) != "свежая\n" {
		t.Fatal("the replacement")
	}
	if entries, _ := os.ReadDir(filepath.Join(export, ".canonkeeper", "tmp")); len(entries) != 0 {
		t.Fatalf("temporary files stay: %v", entries)
	}
	// A move keeps the text under the first free name of _Удалено/.
	write(t, export, "_Удалено/Мара.md", "перенесённое раньше\n")
	fresh := obsidian.ExportPrint([]byte("свежая\n"))
	moved, err := writer.Move(ctx, "Герои/Мара.md", []string{"Мара.md", "Мара 2026-11-15.md"}, fresh)
	if err != nil || moved != "_Удалено/Мара 2026-11-15.md" {
		t.Fatalf("Move = %q, %v", moved, err)
	}
	if readBack(t, filepath.Join(export, "_Удалено", "Мара 2026-11-15.md")) != "свежая\n" ||
		readBack(t, filepath.Join(export, "_Удалено", "Мара.md")) != "перенесённое раньше\n" {
		t.Fatal("a move lost a text")
	}
	if _, err := writer.Move(ctx, "Герои/Мара.md", []string{"x.md"}, fresh); !errors.Is(err, service.ErrGoneMeanwhile) {
		t.Fatalf("a move of a file that went: %v", err)
	}
	// _Удалено/ is no note of the folder for the search of a moved reference.
	if notes, _ := writer.Notes(ctx); len(notes) != 1 || notes[0] != "Герои/Сава.md" {
		t.Fatalf("Notes = %q", notes)
	}
}

// OE1.5, OE1.7: a manifest that does not read is said so; a file that does not read is skipped by its reason, not read as
// empty.
func TestWhatDoesNotReadIsSaid(t *testing.T) {
	ctx := context.Background()
	vault := filepath.Join(t.TempDir(), "Хранилище")
	export := filepath.Join(vault, "CanonKeeper", "Сага")
	write(t, export, ".canonkeeper/manifest.json", `{"format":1}`)
	writer := openWriter(t, vault)
	if data, ok, err := writer.Manifest(ctx, "CanonKeeper/Сага"); err != nil || !ok || string(data) != `{"format":1}` {
		t.Fatalf("Manifest = %q %v %v", data, ok, err)
	}
	if _, ok, err := writer.Manifest(ctx, "CanonKeeper/Другая"); ok || err != nil {
		t.Fatalf("no manifest: %v %v", ok, err)
	}
	write(t, vault, "CanonKeeper/Файл/.canonkeeper", "не папка")
	if _, _, err := writer.Manifest(ctx, "CanonKeeper/Файл"); !errors.Is(err, obsidian.ErrExportManifestUnreadable) {
		t.Fatalf("a manifest that does not read: %v", err)
	}
	if err := writer.Open(ctx, "CanonKeeper/Сага"); err != nil {
		t.Fatal(err)
	}
	write(t, export, "Герои/Закрытый.md", "закрыто")
	if err := os.Chmod(filepath.Join(export, "Герои", "Закрытый.md"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(export, "Герои", "Закрытый.md"), 0o644) })
	if os.Geteuid() != 0 {
		if _, skip := writer.Read(ctx, "Герои/Закрытый.md"); skip != obsidian.ExportUnreadable {
			t.Fatalf("a file without the right to read: %q", skip)
		}
	}
	if err := os.Mkdir(filepath.Join(export, "Герои", "Папка.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, skip := writer.Read(ctx, "Герои/Папка.md"); skip != obsidian.ExportLink {
		t.Fatalf("a folder at the path of a reference: %q", skip)
	}
}

// OE1.4: a replacement and a move that a hold makes wait look at the file again before each try. Outside Windows no failure
// is a hold, so the test takes a folder without the right to write for one — the rename into it fails until the folder is
// writable again —, and while the app waits, the author saves the file: the try after the wait leaves it his and in its place;
// a hold that outlasts the wait is busy, the file untouched.
func TestAWriteAHoldMakesWaitChecksTheFileBeforeEachTry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the folder without the right to write does not stop the root")
	}
	ctx := context.Background()
	vault := filepath.Join(t.TempDir(), "Хранилище")
	export := filepath.Join(vault, "CanonKeeper", "Сага")
	write(t, export, "Герои/Мара.md", "наша справка\n")
	write(t, export, "_Удалено/Прежняя.md", "прежняя\n")
	planned := obsidian.ExportPrint([]byte("наша справка\n"))
	edited := "наша справка\nмоя правка во время ожидания\n"
	held := func(dir string) waiter {
		clock := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return waiter{
			held: func(err error) bool { return errors.Is(err, fs.ErrPermission) }, wait: 2 * time.Second,
			now: func() time.Time { return clock },
			sleep: func(pause time.Duration) {
				clock = clock.Add(pause)
				// The hold lets the file go, and the author has saved it meanwhile.
				_ = os.Chmod(dir, 0o755)
				if err := os.WriteFile(filepath.Join(export, "Герои", "Мара.md"), []byte(edited), 0o644); err != nil {
					t.Error(err)
				}
			},
		}
	}
	writer := openWriter(t, vault)
	if err := writer.Open(ctx, "CanonKeeper/Сага"); err != nil {
		t.Fatal(err)
	}
	writer.holds = held(filepath.Join(export, "Герои"))
	if err := writer.Write(ctx, "Герои/Мара.md", []byte("свежая\n"), planned); !errors.Is(err, service.ErrChangedMeanwhile) {
		t.Fatalf("a replacement a hold made wait = %v", err)
	}
	if readBack(t, filepath.Join(export, "Герои", "Мара.md")) != edited {
		t.Fatal("the save of the author during the hold is lost")
	}
	write(t, export, "Герои/Мара.md", "наша справка\n")
	writer.holds = held(filepath.Join(export, "_Удалено"))
	if _, err := writer.Move(ctx, "Герои/Мара.md", []string{"Мара.md"}, planned); !errors.Is(err, service.ErrChangedMeanwhile) {
		t.Fatalf("a move a hold made wait = %v", err)
	}
	if readBack(t, filepath.Join(export, "Герои", "Мара.md")) != edited {
		t.Fatal("the reference the author saved during the hold left its place")
	}
	// A hold that does not let go: busy, nothing replaced.
	write(t, export, "Герои/Мара.md", "наша справка\n")
	dir := filepath.Join(export, "Герои")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	clock := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	writer.holds = waiter{
		held: func(err error) bool { return errors.Is(err, fs.ErrPermission) }, wait: 2 * time.Second,
		now: func() time.Time { return clock }, sleep: func(pause time.Duration) { clock = clock.Add(pause) },
	}
	var skipped service.ExportSkipped
	if err := writer.Write(ctx, "Герои/Мара.md", []byte("свежая\n"), planned); !errors.As(err, &skipped) || skipped.Skip != obsidian.ExportBusy {
		t.Fatalf("a hold past the wait = %v", err)
	}
	if readBack(t, filepath.Join(export, "Герои", "Мара.md")) != "наша справка\n" {
		t.Fatal("a write held past the wait changed the file")
	}
}

// OE1.5, OE2.8: a folder is bare — free for an export — only when nothing of anyone is in it: empty, or the folder of the app
// without a manifest (a first run cut before its first manifest). A manifest of any saga in it, a note, a folder or a hidden
// file of the author, or a file or a link named as the folder of the app make it a folder that is someone's.
func TestWhatAFolderIsBare(t *testing.T) {
	ctx := context.Background()
	mkdir := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		setup func(dir string)
		want  service.ExportFolder
	}{
		{"an empty folder", func(string) {}, service.ExportFolderBare},
		{"only the folder of the app, no manifest", func(dir string) { mkdir(filepath.Join(dir, ".canonkeeper")) }, service.ExportFolderBare},
		{"the folder of the app with a temporary file", func(dir string) { write(t, dir, ".canonkeeper/tmp/x.tmp", "{") }, service.ExportFolderBare},
		{"the folder of the app with a manifest", func(dir string) { write(t, dir, ".canonkeeper/manifest.json", "{}") }, service.ExportFolderPresent},
		{"a note of the author", func(dir string) { write(t, dir, "Мои планы.md", "планы\n") }, service.ExportFolderPresent},
		{"a note of the author and the folder of the app", func(dir string) {
			write(t, dir, "Мои планы.md", "планы\n")
			mkdir(filepath.Join(dir, ".canonkeeper"))
		}, service.ExportFolderPresent},
		{"an empty folder of the author inside", func(dir string) { mkdir(filepath.Join(dir, "Черновики")) }, service.ExportFolderPresent},
		{"a hidden file only", func(dir string) { write(t, dir, ".DS_Store", "x") }, service.ExportFolderPresent},
		{"a file named like the folder of the app", func(dir string) { write(t, dir, ".canonkeeper", "x") }, service.ExportFolderPresent},
		{"a link named like the folder of the app", func(dir string) {
			if err := os.Symlink(t.TempDir(), filepath.Join(dir, ".canonkeeper")); err != nil {
				t.Fatal(err)
			}
		}, service.ExportFolderPresent},
	} {
		t.Run(test.name, func(t *testing.T) {
			vault := filepath.Join(t.TempDir(), "Хранилище")
			dir := filepath.Join(vault, "CanonKeeper", "Сага")
			mkdir(dir)
			test.setup(dir)
			if got, err := openWriter(t, vault).Folder(ctx, "CanonKeeper/Сага"); err != nil || got != test.want {
				t.Fatalf("Folder = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}
