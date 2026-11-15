// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/library"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	materialsservice "canonkeeper/backend/internal/services/authormaterials"
	service "canonkeeper/backend/internal/services/obsidian"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

// characters is the choice of the folder of the heroes of the example as materials.
var characters = obsidian.Choice{Materials: []obsidian.MaterialFolder{{Path: "Персонажи", Kind: authormaterials.KindCharacter}}}

func (core *bridgeCore) materialsUseCase(notes service.Notes, book service.BookNames) service.ImportMaterials {
	if book == nil {
		book = quietSaga{}
	}
	return service.ImportMaterials{
		Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Notes: notes, Book: book, Plain: library.MarkdownPlainLines,
		Gate: service.NewGate(), Limits: service.DefaultLimits,
	}
}

// previewed is the input of a load of the records of what a preview of the choice showed — the fingerprints of its folders
// of materials, what the author saw of each record — with the decisions, and the preview.
func (core *bridgeCore) previewed(key string, choice obsidian.Choice, decisions obsidian.Decisions) (service.MaterialsInput, service.Preview) {
	core.t.Helper()
	preview := core.preview(choice)
	input := service.MaterialsInput{
		ActorUserID: core.author, ProjectID: core.saga, Choice: choice, Fingerprints: map[string]string{}, Seen: map[string]string{},
		Decisions: decisions, Key: key, Proof: core.proof,
	}
	for _, folder := range preview.Materials.Folders {
		input.Fingerprints[folder.Folder] = folder.Fingerprint
	}
	for _, record := range preview.Materials.Records {
		input.Seen[record.Path] = record.Seen
	}
	return input, preview
}

// loadNotes loads the records of what a fresh preview of the choice shows, with the decisions.
func (core *bridgeCore) loadNotes(key string, choice obsidian.Choice, decisions obsidian.Decisions) service.MaterialsLoaded {
	core.t.Helper()
	input, _ := core.previewed(key, choice, decisions)
	loaded, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(context.Background(), input)
	if err != nil {
		core.t.Fatalf("the load of the records = %v", err)
	}
	return loaded
}

// materials are the records of the saga by their names, as the page lists them.
func (core *bridgeCore) materials() map[string]authormaterials.Record {
	core.t.Helper()
	page, err := materialsrepo.New(core.db, jobstore.New(core.db)).ListAuthorMaterials(context.Background(),
		materialsservice.Actor{UserID: core.author, ProjectID: core.saga}, authormaterials.Filter{Limit: 100})
	if err != nil {
		core.t.Fatal(err)
	}
	records := map[string]authormaterials.Record{}
	for _, record := range page.Records {
		records[record.Name] = record
	}
	return records
}

// linkOf is the record the note is linked with and the revision the link keeps, by the read of the preview.
func (core *bridgeCore) linkOf(note string) (string, int64) {
	core.t.Helper()
	facts, err := core.links.Notes(context.Background(), core.saga, nil, []string{note})
	if err != nil {
		core.t.Fatal(err)
	}
	for _, record := range facts.Records {
		if record.Note == note {
			return record.ID, record.LinkRevision
		}
	}
	return "", 0
}

func (core *bridgeCore) materialsWrite() materialsservice.Write {
	return materialsservice.Write{Actor: materialsservice.Actor{UserID: core.author, ProjectID: core.saga}, Proof: core.proof}
}

// byHand is a record the author writes on the page.
func (core *bridgeCore) byHand(name string) authormaterials.Record {
	core.t.Helper()
	record, err := materialsrepo.New(core.db, jobstore.New(core.db)).CreateAuthorMaterial(context.Background(), materialsservice.CreateCommand{
		Write:  core.materialsWrite(),
		Replay: materialsservice.Replay{ScopeHash: "scope", OperationID: "author_materials.create.v1", KeyHash: "key-" + name, RequestHash: name},
		Draft:  authormaterials.Draft{Name: name, Kind: authormaterials.KindCharacter, Description: "Запись автора."},
		Key:    sagamemory.GlossaryKey(name), At: time.Now(),
	})
	if err != nil {
		core.t.Fatal(err)
	}
	return record
}

// edit changes a record on the page.
func (core *bridgeCore) edit(record authormaterials.Record, description string) {
	core.t.Helper()
	draft := record.Draft
	draft.Description = description
	if _, err := materialsrepo.New(core.db, jobstore.New(core.db)).UpdateAuthorMaterial(context.Background(), core.materialsWrite(), record.ID,
		record.Revision, draft, sagamemory.GlossaryKey(draft.Name)); err != nil {
		core.t.Fatal(err)
	}
}

// remove removes a note of the vault of the example.
func (core *bridgeCore) remove(name string) {
	core.t.Helper()
	if err := os.Remove(filepath.Join(core.vault, filepath.FromSlash(name))); err != nil {
		core.t.Fatal(err)
	}
}

func groupNames(group service.NoteGroup) string { return fmt.Sprint(group.Count, group.Names) }

// specs/2026-11-15-desk-obsidian-notes.md ON1.1, ON1.3–ON1.5: the first load makes the records of the notes and their links,
// of the source obsidian with the path of the note; a load of the same notes writes nothing — the revisions are the ones
// before —; a note changed updates its record by its link; a record edited in the app is kept, and replaced by a decision; a
// name a record of the author keeps is kept, and by a decision to replace the same record gets the note and its link, and the
// note it had before has none; two notes of a load exchange their names; a record the author makes with the name of a new
// note between the preview and the load is not replaced and is named; no upload of the day, no row of an import, no Job.
func TestALoadOfNotesWritesTheirRecordsPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_load")
	core.write(map[string]string{
		"Персонажи/Мирон Сватов.md": "---\naliases: [Мирон]\n---\nСтарший брат.",
		"Персонажи/Ольга.md":        "Старшая на заставе.",
		"Персонажи/Перевал.md":      "---\ntitle: Перевал\n---\nПерсонаж по прозвищу Перевал.",
	})
	pass := core.byHand("Перевал")
	first := core.loadNotes("notes-load-0001", characters, obsidian.Decisions{})
	if groupNames(first.Created) != "2 [Мирон Сватов Ольга]" || groupNames(first.Kept.Taken) != "1 [Перевал]" || first.Updated.Count+first.Replaced.Count != 0 {
		t.Fatalf("the first load = %+v", first)
	}
	records := core.materials()
	miron, olga := records["Мирон Сватов"], records["Ольга"]
	if miron.SourceKind != authormaterials.SourceObsidian || miron.SourceName != "Персонажи/Мирон Сватов.md" || miron.Section != "Персонажи" ||
		olga.Description != "Старшая на заставе." || records["Перевал"].ID != pass.ID || records["Перевал"].SourceKind != authormaterials.SourceManual {
		t.Fatalf("the records = %+v", records)
	}
	if id, revision := core.linkOf("Персонажи/Ольга.md"); id != olga.ID || revision != olga.Revision {
		t.Fatalf("the link of the note = %s %d, want %s %d", id, revision, olga.ID, olga.Revision)
	}

	again := core.loadNotes("notes-load-0002", characters, obsidian.Decisions{})
	if groupNames(again.Unchanged) != "2 [Мирон Сватов Ольга]" || again.Created.Count+again.Updated.Count != 0 {
		t.Fatalf("a load of the same notes = %+v", again)
	}
	if after := core.materials(); after["Ольга"].Revision != olga.Revision || !after["Ольга"].UpdatedAt.Equal(olga.UpdatedAt) {
		t.Fatalf("a load of the same notes wrote %+v", after["Ольга"])
	}

	core.write(map[string]string{"Персонажи/Ольга.md": "Старшая на заставе, носит отцовский меч."})
	core.edit(miron, "Правлено в приложении.")
	changed := core.loadNotes("notes-load-0003", characters, obsidian.Decisions{})
	if groupNames(changed.Updated) != "1 [Ольга]" || groupNames(changed.Kept.Edited) != "1 [Мирон Сватов]" {
		t.Fatalf("a load of a changed note and an edited record = %+v", changed)
	}
	records = core.materials()
	if records["Ольга"].ID != olga.ID || records["Ольга"].Revision != olga.Revision+1 || records["Мирон Сватов"].Description != "Правлено в приложении." {
		t.Fatalf("after the change = %+v", records)
	}
	if _, revision := core.linkOf("Персонажи/Ольга.md"); revision != olga.Revision+1 {
		t.Fatalf("the link keeps the revision %d", revision)
	}

	replace := obsidian.Decisions{States: map[string]obsidian.Decision{obsidian.StateEditedInApp: obsidian.DecisionReplace, obsidian.StateOtherSource: obsidian.DecisionReplace}}
	replaced := core.loadNotes("notes-load-0004", characters, replace)
	if groupNames(replaced.Updated) != "1 [Мирон Сватов]" || groupNames(replaced.Replaced) != "1 [Перевал]" {
		t.Fatalf("a load with the decisions to replace = %+v", replaced)
	}
	records = core.materials()
	if records["Мирон Сватов"].Description != "Старший брат." || records["Перевал"].ID != pass.ID ||
		records["Перевал"].SourceKind != authormaterials.SourceObsidian || records["Перевал"].Description != "Персонаж по прозвищу Перевал." {
		t.Fatalf("after the replace = %+v", records)
	}
	if id, _ := core.linkOf("Персонажи/Перевал.md"); id != pass.ID {
		t.Fatalf("the replaced record is linked with %q", id)
	}

	// A renamed file with the title of its record: the record of the note gone may be replaced, and its link moves.
	core.remove("Персонажи/Ольга.md")
	core.write(map[string]string{"Персонажи/Ольга Сватова.md": "---\ntitle: Ольга\n---\nСтаршая на заставе."})
	moved := core.loadNotes("notes-load-0005", characters, obsidian.Decisions{Notes: map[string]obsidian.Decision{"Персонажи/Ольга Сватова.md": obsidian.DecisionReplace}})
	if groupNames(moved.Replaced) != "1 [Ольга]" {
		t.Fatalf("a load of a renamed file = %+v", moved)
	}
	if id, _ := core.linkOf("Персонажи/Ольга Сватова.md"); id != olga.ID {
		t.Fatalf("the renamed note is linked with %q, want %s", id, olga.ID)
	}
	if id, _ := core.linkOf("Персонажи/Ольга.md"); id != "" {
		t.Fatalf("the note gone keeps the link with %s", id)
	}
	if links := core.count(`SELECT count(*) FROM obsidian_notes WHERE project_id = $1`); links != 3 {
		t.Fatalf("%d links", links)
	}

	// Two notes of one load exchange their names: both records keep their ids.
	core.write(map[string]string{
		"Персонажи/Мирон Сватов.md":  "---\ntitle: Ольга\n---\nСтарший брат.",
		"Персонажи/Ольга Сватова.md": "---\ntitle: Мирон Сватов\n---\nСтаршая на заставе.",
	})
	swapped := core.loadNotes("notes-load-0006", characters, obsidian.Decisions{})
	if swapped.Updated.Count != 2 {
		t.Fatalf("a load of two notes that exchange names = %+v", swapped)
	}
	records = core.materials()
	if records["Ольга"].ID != miron.ID || records["Мирон Сватов"].ID != olga.ID || records["Ольга"].Description != "Старший брат." {
		t.Fatalf("after the exchange of the names = %+v", records)
	}

	// A record the author makes with the name of a new note after the preview is his: kept and named.
	core.write(map[string]string{"Персонажи/Ива.md": "Травница."})
	input, preview := core.previewed("notes-load-0007", characters, obsidian.Decisions{})
	if state := recordState(preview, "Персонажи/Ива.md"); state != obsidian.StateNew {
		t.Fatalf("the new note is %s", state)
	}
	core.byHand("Ива")
	late, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(context.Background(), input)
	if err != nil || groupNames(late.Kept.Changed) != "1 [Ива]" || late.Created.Count != 0 {
		t.Fatalf("a record made after the preview = %+v, %v", late, err)
	}
	if record := core.materials()["Ива"]; record.SourceKind != authormaterials.SourceManual || record.Description != "Запись автора." {
		t.Fatalf("the record of the author = %+v", record)
	}

	// A record the author edits after the preview is his too: a decision to replace, taken on what the preview showed, does not
	// touch it.
	edited, _ := core.previewed("notes-load-0008", characters, replace)
	core.edit(core.materials()["Перевал"], "Правлено после предпросмотра.")
	later, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(context.Background(), edited)
	if err != nil || groupNames(later.Kept.Changed) != "1 [Перевал]" || later.Updated.Count != 0 {
		t.Fatalf("a record edited after the preview = %+v, %v", later, err)
	}
	if record := core.materials()["Перевал"]; record.Description != "Правлено после предпросмотра." {
		t.Fatalf("the record edited after the preview = %+v", record)
	}

	if imports := core.count(`SELECT coalesce(sum(imports), 0)::int FROM writer_usage_days AS usage JOIN projects ON projects.owner_user_id = usage.user_id WHERE projects.id = $1`); imports != 0 {
		t.Fatalf("%d uploads of the day spent", imports)
	}
	if rows := core.count(`SELECT count(*) FROM author_material_imports WHERE project_id = $1`) + core.count(`SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'author_materials_import'`); rows != 0 {
		t.Fatalf("%d rows of an import and its Jobs", rows)
	}
}

func recordState(preview service.Preview, note string) string {
	for _, record := range preview.Materials.Records {
		if record.Path == note {
			return record.State
		}
	}
	return ""
}

// ON1.2: the preview and the load give one state to the records on the same data — a load of what a preview showed keeps
// nothing as changed since and writes by those states —, and the preview of a note whose record the author deleted calls
// it new.
func TestThePreviewAndTheLoadReadTheSameStatesPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_states")
	core.write(map[string]string{"Персонажи/Ведана.md": "Знахарка.", "Персонажи/Ярослава.md": "Воевода.", "Персонажи/Злата.md": "Купчиха."})
	core.byHand("Злата")
	core.loadNotes("notes-state-0001", characters, obsidian.Decisions{})
	core.write(map[string]string{"Персонажи/Ведана.md": "Знахарка с перевала.", "Персонажи/Ива.md": "Травница."})
	core.edit(core.materials()["Ярослава"], "Правлено.")
	input, preview := core.previewed("notes-state-0002", characters, obsidian.Decisions{})
	var states []string
	for _, record := range preview.Materials.Records {
		states = append(states, record.Path+"="+record.State)
	}
	want := []string{"Персонажи/Ведана.md=same_note", "Персонажи/Злата.md=other_source", "Персонажи/Ива.md=new", "Персонажи/Ярослава.md=edited_in_app"}
	if slices.Sort(states); fmt.Sprint(states) != fmt.Sprint(want) {
		t.Fatalf("the states of the preview = %v", states)
	}
	loaded, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(context.Background(), input)
	if err != nil || loaded.Kept.Changed.Count != 0 || groupNames(loaded.Updated) != "1 [Ведана]" || groupNames(loaded.Created) != "1 [Ива]" ||
		groupNames(loaded.Kept.Taken) != "1 [Злата]" || groupNames(loaded.Kept.Edited) != "1 [Ярослава]" {
		t.Fatalf("the load of what the preview showed = %+v, %v", loaded, err)
	}

	store := materialsrepo.New(core.db, jobstore.New(core.db))
	for _, name := range []string{"Злата", "Ведана"} {
		record := core.materials()[name]
		if err := store.DeleteAuthorMaterial(context.Background(), core.materialsWrite(), record.ID, record.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if id, _ := core.linkOf("Персонажи/Ведана.md"); id != "" {
		t.Fatalf("the deleted record keeps its link: %s", id)
	}
	_, after := core.previewed("notes-state-0003", characters, obsidian.Decisions{})
	if zlata, vedana := recordState(after, "Персонажи/Злата.md"), recordState(after, "Персонажи/Ведана.md"); zlata != obsidian.StateNew || vedana != obsidian.StateNew {
		t.Fatalf("the notes whose records were deleted are %s, %s", zlata, vedana)
	}
}
