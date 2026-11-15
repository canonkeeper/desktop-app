// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

// specs/2026-11-13-desk-upgrade.md UG4.3: the roles without login a return creates before pg_restore are the roles the
// migrations create — a migration that adds one adds it to the list.
func TestAReturnCreatesTheRolesTheMigrationsCreate(t *testing.T) {
	created := regexp.MustCompile(`CREATE ROLE ([a-z_]+)`)
	var roles []string
	for _, file := range treeMigrations(t) {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range created.FindAllStringSubmatch(string(raw), -1) {
			if !slices.Contains(roles, match[1]) {
				roles = append(roles, match[1])
			}
		}
	}
	slices.Sort(roles)
	listed := slices.Sorted(slices.Values(baselineRoles))
	if !slices.Equal(roles, listed) {
		t.Fatalf("the migrations create %v; a return creates %v", roles, listed)
	}
}

// layout writes the parts of a data directory, each holding one file with its words.
func layout(t *testing.T, root string, parts map[string]string) {
	t.Helper()
	for part, words := range parts {
		if err := os.MkdirAll(filepath.Join(root, part), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, part, "words"), words)
	}
}

// partsOf reads the words of the parts of a data directory.
func partsOf(t *testing.T, root string) map[string]string {
	t.Helper()
	found := map[string]string{}
	for _, part := range restoreParts {
		if raw, err := os.ReadFile(filepath.Join(root, part, "words")); err == nil {
			found[part] = string(raw)
		}
	}
	return found
}

// UG4.2: a return stopped at any point of its moves — the mark written, half restored, a part moved away, a restored part
// in its place — is undone by the next start before its cluster: the parts of the author are back whole, nothing half
// restored and no mark is left, and a folder «до возврата» of an earlier return stays as it is.
func TestAReturnStoppedAnywhereIsUndoneByTheNextStart(t *testing.T) {
	theirs := map[string]string{}
	for _, part := range restoreParts {
		theirs[part] = "the author's " + part
	}
	before := beforePrefix + "20261113T110500.000Z"
	// The moves of a return in their order: each part of the author goes, then its restored part comes.
	type move struct{ from, to string }
	var moves []move
	for _, part := range restoreParts {
		moves = append(moves, move{part, filepath.Join(before, part)}, move{part + restoringSuffix, part})
	}
	for done := 0; done <= len(moves); done++ {
		root := t.TempDir()
		data := DataDir{Root: root}
		layout(t, root, theirs)
		// A folder of an earlier return and a file of the author beside the parts.
		layout(t, root, map[string]string{filepath.Join(beforePrefix+"20261101T080000.000Z", "pg"): "the month of work"})
		writeFile(t, filepath.Join(root, "settings.json"), `{"port":24117}`)
		record, _ := json.Marshal(restoreRecord{Copy: "pre-145-20261113T110500.000Z", Before: before})
		writeFile(t, filepath.Join(root, restoreMark), string(record))
		restored := map[string]string{}
		for _, part := range restoreParts {
			restored[part+restoringSuffix] = "restored " + part
		}
		layout(t, root, restored)
		if err := os.MkdirAll(filepath.Join(root, before, "codex"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, step := range moves[:done] {
			if err := os.Rename(filepath.Join(root, step.from), filepath.Join(root, step.to)); err != nil {
				t.Fatal(err)
			}
		}

		cluster := &Cluster{data: data}
		if err := cluster.undoRestore(context.Background()); err != nil {
			t.Fatalf("after %d moves: %v", done, err)
		}
		if got := partsOf(t, root); !mapsEqual(got, theirs) {
			t.Fatalf("after %d moves the parts are %v", done, got)
		}
		for _, left := range []string{restoreMark, before, "pg" + restoringSuffix, filepath.Join("codex", "home"+restoringSuffix), filepath.Join("codex", "sqlite"+restoringSuffix)} {
			if _, err := os.Lstat(filepath.Join(root, left)); !os.IsNotExist(err) {
				t.Fatalf("after %d moves %s is left: %v", done, left, err)
			}
		}
		if raw, err := os.ReadFile(filepath.Join(root, beforePrefix+"20261101T080000.000Z", "pg", "words")); err != nil || string(raw) != "the month of work" {
			t.Fatalf("the folder of an earlier return = %q, %v", raw, err)
		}
	}

	// Without a mark only what was half restored goes.
	root := t.TempDir()
	layout(t, root, theirs)
	layout(t, root, map[string]string{"pg" + restoringSuffix: "half"})
	if err := (&Cluster{data: DataDir{Root: root}}).undoRestore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "pg"+restoringSuffix)); !os.IsNotExist(err) || !mapsEqual(partsOf(t, root), theirs) {
		t.Fatalf("a half restored part without a mark: %v %v", err, partsOf(t, root))
	}
	// A mark that names no folder of a return stops the start rather than guess.
	writeFile(t, filepath.Join(root, restoreMark), `{"before":"../pg"}`)
	if err := (&Cluster{data: DataDir{Root: root}}).undoRestore(context.Background()); err == nil {
		t.Fatal("a mark with a path was followed")
	}
	if !mapsEqual(partsOf(t, root), theirs) {
		t.Fatal("a mark with a path changed the data")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// UG4.4: the folders «до возврата» are listed for the question, the newest first, with their sizes; anything else in the
// data directory is not one.
func TestTheFoldersOfEarlierReturnsAreNamedWithTheirSizes(t *testing.T) {
	root := t.TempDir()
	layout(t, root, map[string]string{
		filepath.Join(beforePrefix+"20261101T080000.000Z", "pg"): "1234",
		filepath.Join(beforePrefix+"20261110T080000.000Z", "pg"): "12",
		filepath.Join("before-restore-later", "pg"):              "x",
		"pg": "the cluster",
	})
	befores := beforeRestores(DataDir{Root: root})
	if len(befores) != 2 || befores[0].Bytes != 2 || befores[1].Bytes != 4 {
		t.Fatalf("the folders = %+v", befores)
	}
	made, err := time.Parse(time.RFC3339, befores[0].MadeAt)
	if err != nil || !made.Equal(time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("made at %q: %v", befores[0].MadeAt, err)
	}
}
