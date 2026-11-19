// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func names(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		found = append(found, entry.Name())
	}
	return found
}

func makeCopy(t *testing.T, backups, name string, complete bool) {
	t.Helper()
	directory := filepath.Join(backups, name)
	if err := os.MkdirAll(filepath.Join(directory, "codex", "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, backupDump), "dump")
	if complete {
		writeFile(t, filepath.Join(directory, backupComplete), "")
	}
}

// specs/2026-11-12-desk-process.md PR3.2: a copy a stop left without its mark is removed at the next start; complete
// copies and whatever else lies in backups/ stay.
func TestAnUnfinishedCopyIsRemovedAtTheNextStart(t *testing.T) {
	backups := t.TempDir()
	makeCopy(t, backups, "pre-139-20261110T080000.000Z", true)
	makeCopy(t, backups, "pre-140-20261111T080000.000Z", false)
	writeFile(t, filepath.Join(backups, "notes.txt"), "the author's")
	if err := os.Mkdir(filepath.Join(backups, "pre-manual"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeUnfinishedBackups(backups); err != nil {
		t.Fatal(err)
	}
	if got := names(t, backups); !slices.Equal(got, []string{"notes.txt", "pre-139-20261110T080000.000Z", "pre-manual"}) {
		t.Fatalf("backups after the start = %v", got)
	}
}

// PR3.2, DT13.6: after a copy, whether its migrations went through or failed, two complete copies stay — the copy of this
// start and the newest other one by the time in their names, not by the number of the schema, which a return to an
// earlier version may lower.
func TestTheTwoNewestCopiesStay(t *testing.T) {
	backups := t.TempDir()
	makeCopy(t, backups, "pre-150-20261101T080000.000Z", true)
	makeCopy(t, backups, "pre-139-20261110T080000.000Z", true)
	makeCopy(t, backups, "pre-140-20261111T080000.000Z", true)
	makeCopy(t, backups, "pre-141-20261112T080000.000Z", true)
	writeFile(t, filepath.Join(backups, "notes.txt"), "the author's")
	if err := pruneBackups(backups, backupsKept, "pre-141-20261112T080000.000Z"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, backups); !slices.Equal(got, []string{"notes.txt", "pre-140-20261111T080000.000Z", "pre-141-20261112T080000.000Z"}) {
		t.Fatalf("backups after the migrations = %v", got)
	}
}

// DT13.6: the copy a fatal migrate names stays even when the clock of the computer was set back and its time is older
// than the times of the copies before it.
func TestTheCopyOfThisStartStaysWhateverItsTime(t *testing.T) {
	backups := t.TempDir()
	makeCopy(t, backups, "pre-140-20261111T080000.000Z", true)
	makeCopy(t, backups, "pre-141-20261112T080000.000Z", true)
	makeCopy(t, backups, "pre-142-20261001T080000.000Z", true)
	if err := pruneBackups(backups, backupsKept, "pre-142-20261001T080000.000Z"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, backups); !slices.Equal(got, []string{"pre-141-20261112T080000.000Z", "pre-142-20261001T080000.000Z"}) {
		t.Fatalf("backups after a copy with the clock set back = %v", got)
	}
}

// PR3.1: the copy of the threads takes the files and directories of the author, private to the user; the links to the
// profile of the bundle are the program's, the start makes them again, and the copy leaves them out.
func TestTheCopyOfTheThreadsTakesTheFilesOfTheAuthor(t *testing.T) {
	source := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(source, "research-chat", "sessions", "2026", "11"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "research-chat", "sessions", "2026", "11", "rollout.jsonl"), `{"type":"turn"}`)
	writeFile(t, filepath.Join(source, "research-chat", "state.sqlite"), "sqlite")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("/opt/canonkeeper/codex-homes/research-chat/skills", filepath.Join(source, "research-chat", "skills")); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "copy", "codex", "home")
	if err := copyTree(source, target, profileOfAFlow); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target, "research-chat", "sessions", "2026", "11", "rollout.jsonl"))
	if err != nil || string(raw) != `{"type":"turn"}` {
		t.Fatalf("the copied thread = %q, %v", raw, err)
	}
	if _, err := os.Lstat(filepath.Join(target, "research-chat", "skills")); err == nil {
		t.Fatal("the link to the profile of the bundle was copied")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{target, filepath.Join(target, "research-chat", "sessions")} {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("%s = %v, %v", path, info.Mode(), err)
			}
		}
		if info, err := os.Stat(filepath.Join(target, "research-chat", "state.sqlite")); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("a copied file = %v, %v", info.Mode(), err)
		}
	}
	if err := copyTree(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "copy-of-nothing"), nil); err != nil {
		t.Fatalf("a directory Codex has not made yet: %v", err)
	}
}

// specs/2026-11-13-desk-home.md HM1.5: the copy before the migrations leaves out the entries of the profile in the home of
// every flow — files and trees the start copied from the bundle, and the staging a stopped copy left — since the next
// start copies them again; the threads and the state of Codex beside them are copied.
func TestTheCopyBeforeTheMigrationsLeavesTheProfileOut(t *testing.T) {
	source := filepath.Join(t.TempDir(), "home")
	for _, flow := range []string{"research-chat", "image-workshop"} {
		for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md", filepath.Join("skills", "chat", "SKILL.md"),
			filepath.Join("agents", "reader.toml"), filepath.Join(".canonkeeper-stage-0001", "skills", "chat", "SKILL.md"),
			filepath.Join("sessions", "2026", "11", "rollout.jsonl"), "state.sqlite", filepath.Join("memories", "skills", "notes.md")} {
			path := filepath.Join(source, flow, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, name)
		}
	}
	writeFile(t, filepath.Join(source, "config.toml"), "not of a flow")
	target := filepath.Join(t.TempDir(), "copy", "codex", "home")
	if err := copyTree(source, target, profileOfAFlow); err != nil {
		t.Fatal(err)
	}
	var copied []string
	if err := filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			relative, _ := filepath.Rel(target, path)
			copied = append(copied, filepath.ToSlash(relative))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"config.toml",
		"image-workshop/memories/skills/notes.md", "image-workshop/sessions/2026/11/rollout.jsonl", "image-workshop/state.sqlite",
		"research-chat/memories/skills/notes.md", "research-chat/sessions/2026/11/rollout.jsonl", "research-chat/state.sqlite"}
	if !slices.Equal(copied, want) {
		t.Fatalf("the copy of the homes = %v; want %v", copied, want)
	}
}

// specs/2026-11-19-desk-trust.md TW3.5: «Удалить копии…» removes every copy of backups/ — the complete ones and one an earlier
// removal left without its mark — and nothing else lying there; the list of the copies is empty after it, and a data directory
// without backups/ has nothing to remove.
func TestTheRemovalOfTheCopiesTakesOnlyTheCopies(t *testing.T) {
	backups := t.TempDir()
	makeCopy(t, backups, "pre-140-20261111T080000.000Z", true)
	makeCopy(t, backups, "pre-141-20261112T080000.000Z", true)
	makeCopy(t, backups, "pre-142-20261113T080000.000Z", false)
	writeFile(t, filepath.Join(backups, "notes.txt"), "the author's")
	if err := os.Mkdir(filepath.Join(backups, "pre-manual"), 0o700); err != nil {
		t.Fatal(err)
	}
	list := backupList{directory: backups}
	if copies, err := list.List(); err != nil || len(copies) != 2 {
		t.Fatalf("the copies before = %v, %v", copies, err)
	}
	if err := list.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := names(t, backups); !slices.Equal(got, []string{"notes.txt", "pre-manual"}) {
		t.Fatalf("backups after «Удалить копии…» = %v", got)
	}
	if copies, err := list.List(); err != nil || len(copies) != 0 {
		t.Fatalf("the copies after = %v, %v", copies, err)
	}
	if err := (backupList{directory: filepath.Join(t.TempDir(), "backups")}).Remove(); err != nil {
		t.Fatalf("no backups/ = %v", err)
	}
}

// TW3.5: the mark of a copy goes before its folder — a removal cut short leaves an unfinished copy, which the list does not
// show and the next start removes, never a complete copy without its db.dump.
func TestTheMarkOfACopyGoesBeforeItsFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder without the right to write keeps its files only for a user of Unix")
	}
	backups := t.TempDir()
	name := "pre-140-20261111T080000.000Z"
	makeCopy(t, backups, name, true)
	held := filepath.Join(backups, name, "codex", "home")
	writeFile(t, filepath.Join(held, "thread"), "a thread")
	if err := os.Chmod(held, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(held, 0o700) })
	if err := removeBackups(backups); err == nil {
		t.Fatal("a removal cut short said nothing")
	}
	copy := filepath.Join(backups, name)
	if _, err := os.Stat(copy); err != nil || backupFinished(copy) {
		t.Fatalf("the copy cut short: %v, complete %v", err, backupFinished(copy))
	}
	if copies, err := (backupList{directory: backups}).List(); err != nil || len(copies) != 0 {
		t.Fatalf("the list after a removal cut short = %v, %v", copies, err)
	}
	if err := os.Chmod(held, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeUnfinishedBackups(backups); err != nil {
		t.Fatal(err)
	}
	if got := names(t, backups); len(got) != 0 {
		t.Fatalf("backups after the next start = %v", got)
	}
}
