package sagamemory

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	authormaterialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	obsidianservice "canonkeeper/backend/internal/services/obsidian"
	service "canonkeeper/backend/internal/services/sagamemory"
	reference "canonkeeper/backend/internal/services/sagareference"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-15-desk-obsidian-notes.md ON1.4: a load of notes with a new record changes the stamp of the records and the
// map reads as building; a load where all is unchanged leaves the stamp as it was and the map as it is; the record of the
// name of a hero of the book stands by his node.
func TestARecordOfANoteStandsByItsHeroOnTheMap(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_notes")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
	saga := service.NewSaga(repository, repository)
	model := &namesCardModel{}
	failed := func(_, stage string, err error) { t.Errorf("the step %s of the Job failed: %v", stage, err) }
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, failed, nil).
		WithMap(service.NewMapBuilder(saga, repository, authormaterialsrepo.New(db, jobstore.New(db))), failed)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	graph, nodes := mapUseCases(repository)
	fresh, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	writer := mapWriterForTest(t, db, memoryOwnerID, "owner", "n")
	if err := obsidianrepo.New(db).Link(ctx, memoryProjectID, "/home/author/Хранилище"); err != nil {
		t.Fatal(err)
	}
	load := func(key string) obsidianservice.MaterialsLoaded {
		t.Helper()
		records := []obsidian.Record{{
			Path:  "Персонажи/Мирон.md",
			Draft: authormaterials.Draft{Name: "Мирон", Kind: authormaterials.KindCharacter, Description: "Запись из заметки о Мироне."},
		}}
		facts, err := obsidianrepo.NewBooks(db).Notes(ctx, memoryProjectID, []string{"мирон"}, []string{records[0].Path})
		if err != nil {
			t.Fatal(err)
		}
		states := obsidian.NoteStates(records, facts, nil, nil)
		loaded, err := obsidianrepo.NewNotes(db).WriteNotes(ctx, obsidianservice.NotesWrite{
			ActorUserID: memoryOwnerID, ProjectID: memoryProjectID, Proof: writer.Proof, Records: records,
			Seen: map[string]string{records[0].Path: states[0].Seen},
			Key:  obsidianservice.ReplayKey{ActorUserID: memoryOwnerID, ProjectID: memoryProjectID, Key: key, Request: key},
		})
		if err != nil {
			t.Fatalf("the load of the notes = %v", err)
		}
		return loaded
	}
	stamp := func() string {
		t.Helper()
		read, err := repository.CardsStamp(ctx, memoryProjectID, nil)
		if err != nil {
			t.Fatal(err)
		}
		return read.Materials
	}

	before := stamp()
	if loaded := load("map-notes-1"); loaded.Created.Count != 1 || stamp() == before {
		t.Fatalf("a load with a new record = %+v, the stamp %s", loaded, stamp())
	}
	behind, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{fresh.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, behind); !document.Building || behind.NotModified {
		t.Fatalf("the map after a new record = %+v", document)
	}
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	rebuilt, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMapRow(t, rebuilt)
	found, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: "Мирон", To: document.Chapters[0].Key})
	if err != nil {
		t.Fatal(err)
	}
	var hero domain.MapCardDocument
	if err := domain.DecodeStored(found.Document, &hero); err != nil {
		t.Fatal(err)
	}
	if hero.Material == nil || hero.Material.Name != "Мирон" || hero.Material.Description != "Запись из заметки о Мироне." {
		t.Fatalf("the card of the hero = %+v", hero)
	}

	kept := stamp()
	if loaded := load("map-notes-2"); loaded.Unchanged.Count != 1 || stamp() != kept {
		t.Fatalf("a load of the same note = %+v, the stamp %s, want %s", loaded, stamp(), kept)
	}
	if again, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{rebuilt.Revision}}); err != nil || !again.NotModified {
		t.Fatalf("the map after a load of the same note = %+v, %v", again, err)
	}
}
