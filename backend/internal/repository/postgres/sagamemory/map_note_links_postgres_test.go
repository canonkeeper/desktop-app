package sagamemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	authormaterialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	obsidianservice "canonkeeper/backend/internal/services/obsidian"
	service "canonkeeper/backend/internal/services/sagamemory"
	reference "canonkeeper/backend/internal/services/sagareference"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"
)

// heroAndPlaceCardModel writes every chapter a card that names the hero «Мирон» and the town «Зимогорск»: both are nodes of the
// map whatever the count of the cards asks of a node.
type heroAndPlaceCardModel struct{}

func (heroAndPlaceCardModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions == domain.NameInstructions {
		return `{"persons":[]}`, quota.ModelUsage{Calls: 1}, nil
	}
	if strings.Contains(input, "КАРТОЧКА:") {
		return `{}`, quota.ModelUsage{Calls: 1}, nil
	}
	return `{"summary": "Пересказ главы", "characters": [{"name": "Мирон", "p": 1}], "entities": [{"name": "Зимогорск", "type": "город", "p": 1}]}`,
		quota.ModelUsage{Calls: 1}, nil
}

// noteSaga is what a preview of the folders of records reads of the saga: the names of its book and no reading.
type noteSaga struct{ *Repository }

func (noteSaga) Reading(context.Context, string) (obsidianservice.Reading, error) {
	return obsidianservice.Reading{Helper: obsidianservice.HelperNone}, nil
}

// noBooks are the plans and the volumes of a preview without folders of books.
type noBooks struct{}

func (noBooks) Changes(context.Context, string, string, []obsidianservice.VolumeChapter) (bool, error) {
	return false, nil
}
func (noBooks) Check(string, []byte) obsidianservice.Volume { return obsidianservice.Volume{} }

// specs/2026-11-15-desk-obsidian-book-web.md OU5.1 (specs/2026-11-07-desktop-app.md DA11.1.6): the record of a note whose
// description links a hero and a place of the book — a wiki-link by the name and one with a caption — has thin links with
// their nodes once the records are loaded from the folder and the map is built by the last step of the Job of the cards
// (specs/2026-11-04-saga-map.md R9.5: the link of a note goes into the description by its caption); a note without links has
// none. specs/2026-11-15-desk-wiki-target.md WT1.1, WT1.6: a caption that does not name the place links it too — the record
// writes the target of the link —, and a record the previous code loaded from such a note is «обновится» in the next preview.
func TestTheWikiLinksOfANoteLinkItsRecordOnTheMap(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_note_links")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
	failed := func(_, stage string, err error) { t.Errorf("the step %s of the Job failed: %v", stage, err) }
	build := service.NewBuildCards(repository, repository, heroAndPlaceCardModel{}, nil, "test-model", 2, failed, nil).
		WithMap(service.NewMapBuilder(service.NewSaga(repository, repository), repository, authormaterialsrepo.New(db, jobstore.New(db))), failed)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}

	// The folder of the saga with a folder of places: a note that links the hero by his name and the town by a caption in its
	// case, and a note without a link.
	vault := t.TempDir()
	writeNotes := func(notes map[string]string) {
		t.Helper()
		for name, text := range notes {
			full := filepath.Join(vault, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeNotes(map[string]string{
		"Места/Застава у перевала.md": "Сюда приходил [[Мирон]], а дорога ведёт [[Зимогорск|к Зимогорску]].\n",
		"Места/Тихая заводь.md":       "Тёплая вода и камыш.\n",
	})
	vaults := obsidianrepo.New(db)
	if err := vaults.Link(ctx, memoryProjectID, vault); err != nil {
		t.Fatal(err)
	}
	writer := mapWriterForTest(t, db, memoryOwnerID, "owner", "w")
	choice := obsidian.Choice{Materials: []obsidian.MaterialFolder{{Path: "Места", Kind: authormaterials.KindPlace}}}
	links := obsidianrepo.NewBooks(db)
	previewNotes := func() obsidianservice.Preview {
		t.Helper()
		preview, err := obsidianservice.PreviewImport{
			Vaults: vaults, Files: obsidianvault.Files{}, Links: links, Plans: noBooks{}, Saga: noteSaga{repository}, Volumes: noBooks{},
			Plain: library.MarkdownPlainLines, Gate: obsidianservice.NewGate(), Limits: obsidianservice.DefaultLimits,
		}.Execute(ctx, obsidianservice.PreviewInput{ActorUserID: memoryOwnerID, ProjectID: memoryProjectID, Choice: choice})
		if err != nil {
			t.Fatalf("the preview = %v", err)
		}
		return preview
	}
	loadNotes := func(preview obsidianservice.Preview, key string) obsidianservice.MaterialsLoaded {
		t.Helper()
		input := obsidianservice.MaterialsInput{
			ActorUserID: memoryOwnerID, ProjectID: memoryProjectID, Choice: choice, Fingerprints: map[string]string{}, Seen: map[string]string{},
			Key: key, Proof: writer.Proof,
		}
		for _, folder := range preview.Materials.Folders {
			input.Fingerprints[folder.Folder] = folder.Fingerprint
		}
		for _, record := range preview.Materials.Records {
			input.Seen[record.Path] = record.Seen
		}
		loaded, err := obsidianservice.ImportMaterials{
			Vaults: vaults, Files: obsidianvault.Files{}, Links: links, Notes: obsidianrepo.NewNotes(db), Book: repository,
			Plain: library.MarkdownPlainLines, Gate: obsidianservice.NewGate(), Limits: obsidianservice.DefaultLimits,
		}.Execute(ctx, input)
		if err != nil {
			t.Fatalf("the load of the records = %v", err)
		}
		return loaded
	}
	if loaded := loadNotes(previewNotes(), "map-note-links-1"); loaded.Created.Count != 2 {
		t.Fatalf("the load of the records = %+v", loaded)
	}

	buildMap := func() (domain.MapDocument, map[string]int) {
		t.Helper()
		if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
			t.Fatalf("run = %q, %v", status, err)
		}
		graph, _ := mapUseCases(repository)
		built, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
		if err != nil {
			t.Fatal(err)
		}
		document := decodedMapRow(t, built)
		index := map[string]int{}
		for at, node := range document.Nodes {
			index[node.Name] = at
		}
		return document, index
	}
	document, index := buildMap()
	for _, name := range []string{"Мирон", "Зимогорск", "Застава у перевала", "Тихая заводь"} {
		if _, ok := index[name]; !ok {
			t.Fatalf("the node %q is not on the map: %v", name, index)
		}
	}
	if !document.Nodes[index["Застава у перевала"]].Material || !document.Nodes[index["Тихая заводь"]].Material {
		t.Fatalf("the records are not nodes of the materials: %+v", document.Nodes)
	}
	linked := func(left, right string) bool {
		for _, link := range document.Links {
			ends := map[int]bool{link.Source: true, link.Target: true}
			if link.Material && ends[index[left]] && ends[index[right]] {
				return true
			}
		}
		return false
	}
	if !linked("Застава у перевала", "Мирон") {
		t.Fatalf("the record does not link the hero of its link by the name: %+v", document.Links)
	}
	if !linked("Застава у перевала", "Зимогорск") {
		t.Fatalf("the record does not link the place of its link with a caption: %+v", document.Links)
	}
	for _, link := range document.Links {
		if link.Source == index["Тихая заводь"] || link.Target == index["Тихая заводь"] {
			t.Fatalf("a note without links has a link: %+v", link)
		}
	}

	// The record of the note «Старый тракт» as the code before WT1.1 loaded it from [[Зимогорск|к городу у гор]]: the caption
	// alone — the same record its note gives with the caption written as plain words.
	writeNotes(map[string]string{"Места/Старый тракт.md": "Тракт уходит к городу у гор.\n"})
	if loaded := loadNotes(previewNotes(), "map-note-links-2"); loaded.Created.Count != 1 {
		t.Fatalf("the load of the record of the previous code = %+v", loaded)
	}
	// The note with the link: the next preview says the record will be updated, and the record names the place.
	writeNotes(map[string]string{"Места/Старый тракт.md": "Тракт уходит [[Зимогорск|к городу у гор]].\n"})
	preview := previewNotes()
	var road *obsidianservice.RecordPreview
	for at := range preview.Materials.Records {
		if preview.Materials.Records[at].Path == "Места/Старый тракт.md" {
			road = &preview.Materials.Records[at]
		}
	}
	if road == nil || road.State != obsidian.StateSameNote || !road.Changed || road.Draft.Description != "Тракт уходит к городу у гор (Зимогорск)." {
		t.Fatalf("the preview of the record of the previous code = %+v", road)
	}
	if loaded := loadNotes(preview, "map-note-links-3"); loaded.Updated.Count != 1 {
		t.Fatalf("the load of the record with the target = %+v", loaded)
	}
	document, index = buildMap()
	if _, ok := index["Старый тракт"]; !ok {
		t.Fatalf("the node of the record is not on the map: %v", index)
	}
	if !linked("Старый тракт", "Зимогорск") {
		t.Fatalf("the record does not link the place of its link with a caption without its name: %+v", document.Links)
	}
	if linked("Старый тракт", "Мирон") {
		t.Fatalf("the record links a hero it does not name: %+v", document.Links)
	}
}
