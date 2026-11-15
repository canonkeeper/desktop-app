// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"

	"golang.org/x/text/unicode/norm"
)

// staleSeen is what a request says the author saw of a record when its preview said otherwise.
const staleSeen = "0000000000000000"

// replaceTaken is the rule «replace the records that keep the names of the notes».
var replaceTaken = obsidian.Decisions{States: map[string]obsidian.Decision{obsidian.StateOtherSource: obsidian.DecisionReplace}}

// revisions are the revisions of the records of the saga by their names.
func (core *bridgeCore) revisions() string {
	core.t.Helper()
	revisions := map[string]int64{}
	for name, record := range core.materials() {
		revisions[name] = record.Revision
	}
	return fmt.Sprint(revisions)
}

// execute loads the records of the input by a fresh store.
func (core *bridgeCore) execute(input service.MaterialsInput) (service.MaterialsLoaded, error) {
	return core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(context.Background(), input)
}

// specs/2026-11-15-desk-obsidian-notes.md ON1.2, ON1.4: two notes of one name a record of the author keeps — the first by the
// order of the paths may replace it, the second has the name of another note of the load; a load with the rule «replace»
// replaces the record by the first and keeps the second, and the same key answers the same.
func TestTwoNotesOfOneNameOnARecordOfTheAuthorPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_twice")
	core.write(map[string]string{
		"Персонажи/Перевал.md": "Персонаж по прозвищу Перевал.",
		"Персонажи/Застава.md": "---\ntitle: Перевал\n---\nВторая заметка того же имени.",
	})
	pass := core.byHand("Перевал")
	input, preview := core.previewed("notes-twice-0001", characters, replaceTaken)
	first, second := previewedRecord(preview, "Персонажи/Застава.md"), previewedRecord(preview, "Персонажи/Перевал.md")
	if first.State != obsidian.StateOtherSource || first.Target != pass.ID || first.HeldBy != "" || second.State != obsidian.StateOtherSource ||
		second.Target != "" || second.HeldBy != "Персонажи/Застава.md" {
		t.Fatalf("the preview = %+v / %+v", first.NoteState, second.NoteState)
	}
	loaded, err := core.execute(input)
	if err != nil || groupNames(loaded.Replaced) != "1 [Перевал]" || groupNames(loaded.Kept.Taken) != "1 [Перевал]" {
		t.Fatalf("the load with the rule to replace = %+v, %v", loaded, err)
	}
	record := core.materials()["Перевал"]
	if record.ID != pass.ID || record.SourceKind != authormaterials.SourceObsidian || record.SourceName != "Персонажи/Застава.md" {
		t.Fatalf("the record of the author after the load = %+v", record)
	}
	if again, err := core.execute(input); err != nil || fmt.Sprint(again) != fmt.Sprint(loaded) {
		t.Fatalf("the same key = %+v, %v", again, err)
	}
	if id, _ := core.linkOf("Персонажи/Перевал.md"); id != "" || core.noteLinks() != 1 {
		t.Fatalf("the second note is linked with %q; %d links", id, core.noteLinks())
	}
}

// ON1.2, ON1.3: two notes that swap their names and a chain of three renames — a load of what the preview showed writes them
// all; a load whose author did not see one of them as the preview showed it keeps the records whose names depend on it,
// names them as changed since the preview and writes nothing — no conflict —, and the same key answers the same; a request
// without what the author saw of a note of its folders is refused before anything is written.
func TestARenameTheAuthorDidNotSeeKeepsTheNamesThatDependOnItPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_stale")
	core.write(map[string]string{
		"Персонажи/А.md": "---\ntitle: Ведана\n---\nЗнахарка.", "Персонажи/Б.md": "---\ntitle: Ярослава\n---\nВоевода.",
		"Персонажи/В.md": "---\ntitle: Злата\n---\nКупчиха.",
	})
	core.loadNotes("notes-stale-0001", characters, obsidian.Decisions{})
	before := core.materials()
	core.write(map[string]string{"Персонажи/А.md": "---\ntitle: Ярослава\n---\nЗнахарка.", "Персонажи/Б.md": "---\ntitle: Ведана\n---\nВоевода."})

	swap, preview := core.previewed("notes-stale-0002", characters, obsidian.Decisions{})
	for _, note := range []string{"Персонажи/А.md", "Персонажи/Б.md"} {
		if state := recordState(preview, note); state != obsidian.StateSameNote {
			t.Fatalf("%s is %s in the preview of a swap", note, state)
		}
	}
	unseen := swap
	unseen.Seen = map[string]string{"Персонажи/А.md": swap.Seen["Персонажи/А.md"], "Персонажи/В.md": swap.Seen["Персонажи/В.md"]}
	if _, err := core.execute(unseen); !errors.Is(err, service.ErrMaterialsSeen) {
		t.Fatalf("a request without what the author saw of a note = %v", err)
	}
	swap.Seen["Персонажи/Б.md"] = staleSeen
	kept, err := core.execute(swap)
	if err != nil || groupNames(kept.Kept.Changed) != "2 [Ярослава Ведана]" || kept.Updated.Count != 0 || core.revisions() != fmt.Sprint(revisionsOf(before)) {
		t.Fatalf("a swap one side of which the author did not see = %+v, %v; %s", kept, err, core.revisions())
	}
	if again, err := core.execute(swap); err != nil || fmt.Sprint(again) != fmt.Sprint(kept) {
		t.Fatalf("the same key = %+v, %v", again, err)
	}
	if swapped := core.loadNotes("notes-stale-0003", characters, obsidian.Decisions{}); groupNames(swapped.Updated) != "2 [Ярослава Ведана]" {
		t.Fatalf("a load of what the preview of the swap showed = %+v", swapped)
	}

	// A chain: А takes the name of Б, Б of В, В a new one.
	core.write(map[string]string{
		"Персонажи/А.md": "---\ntitle: Ведана\n---\nЗнахарка.", "Персонажи/Б.md": "---\ntitle: Злата\n---\nВоевода.",
		"Персонажи/В.md": "---\ntitle: Ива\n---\nКупчиха.",
	})
	chain, _ := core.previewed("notes-stale-0004", characters, obsidian.Decisions{})
	chain.Seen["Персонажи/В.md"] = staleSeen
	revisions := core.revisions()
	held, err := core.execute(chain)
	if err != nil || held.Kept.Changed.Count != 3 || held.Updated.Count != 0 || core.revisions() != revisions {
		t.Fatalf("a chain whose last rename the author did not see = %+v, %v", held, err)
	}
	if renamed := core.loadNotes("notes-stale-0005", characters, obsidian.Decisions{}); renamed.Updated.Count != 3 {
		t.Fatalf("a load of what the preview of the chain showed = %+v", renamed)
	}
	if after := core.materials(); after["Ива"].ID != before["Злата"].ID || after["Злата"].ID != before["Ярослава"].ID || after["Ведана"].ID != before["Ведана"].ID {
		t.Fatalf("the records after the chain = %+v", after)
	}
}

// Страницам: a folder of materials its preview read and that is gone from the vault since is obsidian.folder_changed with
// that folder, and nothing is written.
func TestALoadOfAFolderOfMaterialsGoneSincePreviewIsAChangeOfItPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_gone")
	core.write(map[string]string{"Персонажи/Ива.md": "Травница."})
	input, _ := core.previewed("notes-gone-0001", characters, obsidian.Decisions{})
	if err := os.Rename(filepath.Join(core.vault, "Персонажи"), filepath.Join(core.vault, "Герои")); err != nil {
		t.Fatal(err)
	}
	var changed service.FolderChanged
	if _, err := core.execute(input); !errors.As(err, &changed) || !slices.Equal(changed.Folders, []string{"Персонажи"}) || core.records() != 0 {
		t.Fatalf("a load of a folder gone since its preview = %v; %d records", err, core.records())
	}
}

// ON1.4: the decision of a note is known by its path in NFC, as what the author saw and the fingerprints are — a path the page
// sends decomposed decides the same note.
func TestADecisionOfANoteIsKnownByItsPathInNFCPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_nfc")
	core.write(map[string]string{"Персонажи/Йорген.md": "Кузнец с перевала."})
	core.byHand("Йорген")
	decided := obsidian.Decisions{Notes: map[string]obsidian.Decision{norm.NFD.String("Персонажи/Йорген.md"): obsidian.DecisionReplace}}
	input, _ := core.previewed("notes-nfc-0001", characters, decided)
	if loaded, err := core.execute(input); err != nil || groupNames(loaded.Replaced) != "1 [Йорген]" {
		t.Fatalf("a decision by a decomposed path = %+v, %v", loaded, err)
	}
}

func revisionsOf(records map[string]authormaterials.Record) map[string]int64 {
	revisions := map[string]int64{}
	for name, record := range records {
		revisions[name] = record.Revision
	}
	return revisions
}

func previewedRecord(preview service.Preview, note string) service.RecordPreview {
	for _, record := range preview.Materials.Records {
		if record.Path == note {
			return record
		}
	}
	return service.RecordPreview{}
}
