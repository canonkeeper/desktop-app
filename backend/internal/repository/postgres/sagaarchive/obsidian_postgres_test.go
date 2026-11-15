package sagaarchive

import (
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/library"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	obsidianservice "canonkeeper/backend/internal/services/obsidian"
)

// specs/2026-11-15-desk-obsidian-notes.md ON1.6, ON1.9: the records a load of notes wrote — of a folder with a space at the
// end of its name and of a path longer than the source name of a record; a note of a name of no key skipped — go into the
// saga file and come out of it into a new saga with their source and its name, and without the links of their notes.
func TestTheRecordsOfNotesGoThroughTheSagaFile(t *testing.T) {
	world := newWorld(t, "ck_saga_file_notes")
	first := world.mustOpen(northernLimit(t, fixtureOptions{}), "notes-open-key-1")
	if err := obsidianrepo.New(world.db).Link(world.ctx, first.ProjectID, "/home/author/Хранилище"); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Очень длинная папка/", 15) + "Мирон.md"
	var records []obsidian.Record
	skipped := obsidian.Skipped{}
	for _, note := range [][2]string{{"Персонажи /Ведана.md", "Знахарка."}, {"Персонажи /!!!.md", "Никто."}, {long, "Старший брат."}} {
		read, skip := obsidian.ReadNote(note[0], []byte(note[1]))
		if skip == "" {
			var record obsidian.Record
			if record, skip = obsidian.Material(read, authormaterials.KindCharacter, library.MarkdownPlainLines); skip == "" {
				records = append(records, record)
				continue
			}
		}
		skipped[skip]++
	}
	var keys, paths []string
	for _, record := range records {
		keys, paths = append(keys, sagamemory.GlossaryKey(record.Draft.Name)), append(paths, record.Path)
	}
	facts, err := obsidianrepo.NewBooks(world.db).Notes(world.ctx, first.ProjectID, keys, paths)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for index, state := range obsidian.NoteStates(records, facts, nil, nil) {
		seen[records[index].Path] = state.Seen
	}
	loaded, err := obsidianrepo.NewNotes(world.db).WriteNotes(world.ctx, obsidianservice.NotesWrite{
		ActorUserID: world.user, ProjectID: first.ProjectID, Proof: world.proof, Records: records, Seen: seen, Skipped: skipped,
		Key: obsidianservice.ReplayKey{ActorUserID: world.user, ProjectID: first.ProjectID, Key: "notes-file-key", Request: "file"},
	})
	if err != nil || loaded.Created.Count != 2 || loaded.Skipped[obsidian.SkipNoName] != 1 {
		t.Fatalf("the load of the notes = %+v, %v", loaded, err)
	}
	second := world.mustOpen(world.save(first.ProjectID), "notes-open-key-2")
	got := world.strings(`
		SELECT name || '|' || source_kind || '|' || section || '|' || source_name FROM author_materials WHERE project_id = $1 AND source_kind = 'obsidian'
	`, second.ProjectID)
	tail := obsidian.NoteSource(long)
	if len(got) != 2 || got[0] != "Ведана|obsidian|Персонажи|Персонажи /Ведана.md" || !strings.HasPrefix(got[1], "Мирон|obsidian|") ||
		!strings.HasSuffix(got[1], "|"+tail) || !strings.HasPrefix(tail, "…") {
		t.Fatalf("the records of the notes in the new saga = %v", got)
	}
	if links := world.count(`SELECT count(*) FROM obsidian_notes WHERE project_id = $1`, second.ProjectID); links != 0 {
		t.Fatalf("the new saga has %d links of notes", links)
	}
	if vaults := world.count(`SELECT count(*) FROM obsidian_vaults WHERE project_id = $1`, second.ProjectID); vaults != 0 {
		t.Fatalf("the new saga has %d vaults", vaults)
	}
	if !slices.Contains(world.strings(`SELECT name FROM author_materials WHERE project_id = $1`, second.ProjectID), "Северный маяк") {
		t.Fatal("the records of the file are not in the new saga")
	}
}
