// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	materialsservice "canonkeeper/backend/internal/services/authormaterials"
	service "canonkeeper/backend/internal/services/obsidian"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagareference"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

// The rows of the journal of what went out, and the cards Jobs of a saga that wait.
const (
	journalRows = `SELECT (SELECT count(*) FROM model_disclosures) + (SELECT count(*) FROM model_requests)`
	queuedCards = `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued'`
)

// exportCardModel writes every chapter one card: Мара and Сава hold the gates together, Мара argues with Глеб, Вера leaves;
// the town Белый порт. It counts its calls: an export calls it never.
type exportCardModel struct{ calls int }

func (model *exportCardModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	model.calls++
	if instructions == domain.NameInstructions {
		return `{"persons":[]}`, quota.ModelUsage{Calls: 1}, nil
	}
	if strings.Contains(input, "КАРТОЧКА:") {
		return `{}`, quota.ModelUsage{Calls: 1}, nil
	}
	return `{"summary": "Мара держит ворота заставы.",
		"events": [
			{"event": "Мара и Сава держат ворота", "who": ["Мара", "Сава"], "p": 1, "key": true},
			{"event": "Мара спорит с Глебом", "who": ["Мара", "Глеб"], "p": 1},
			{"event": "Мара и Глеб чинят засов", "who": ["Мара", "Глеб"], "p": 1},
			{"event": "Вера уходит к Саве", "who": ["Вера", "Сава"], "p": 1},
			{"event": "Вера зовёт Саву", "who": ["Вера", "Сава"], "p": 1}
		],
		"characters": [{"name": "Мара", "note": "дозорная", "p": 1}, {"name": "Сава", "p": 1}, {"name": "Глеб", "p": 1}, {"name": "Вера", "p": 1}],
		"entities": [{"name": "Белый порт", "type": "город", "p": 1}],
		"changes": [{"subject": "Мара", "change": "стала капитаном", "p": 1}]}`, quota.ModelUsage{Calls: 1}, nil
}

// exportCore is the bridge core with the memory of its saga: the text index and the cards Jobs run by hand, as the workers
// of the app run them, the map by the last step of the cards.
type exportCore struct {
	*bridgeCore
	saga  *sagamemoryrepo.Repository
	model *exportCardModel
}

func newExportCore(t *testing.T, prefix string) *exportCore {
	t.Helper()
	core := &exportCore{bridgeCore: newBridgeCore(t, prefix), model: &exportCardModel{}}
	core.saga = sagamemoryrepo.New(core.db).WithTextIndex(indexingstore.New(core.db))
	return core
}

// index runs the text index Jobs of the saga that wait.
func (core *exportCore) index() {
	core.t.Helper()
	ctx := context.Background()
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		var jobID string
		var revision uint64
		var incarnation int64
		err := core.db.Pool.QueryRow(ctx, `
			UPDATE jobs SET status='running', claim_incarnation=claim_incarnation+1, claimed_at=clock_timestamp(), updated_at=clock_timestamp()
			WHERE project_id=$1 AND kind='canon_text_index' AND status='queued' AND subject_json->>'sourceView'=$2
			RETURNING id::text, (subject_json->>'catalogRevision')::bigint, claim_incarnation
		`, core.bridgeCore.saga, string(view)).Scan(&jobID, &revision, &incarnation)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			core.t.Fatal(err)
		}
		claim := jobs.WorkerClaim{ProjectID: core.bridgeCore.saga, JobID: jobID, ClaimIncarnation: incarnation}
		indexing := indexingstore.New(core.db)
		descriptors, err := indexing.ListCanonTextChapterDescriptorsForClaim(ctx, claim)
		if err != nil {
			core.t.Fatal(err)
		}
		for _, descriptor := range descriptors {
			snapshot, err := indexing.LoadCanonTextChapterSnapshotForClaim(ctx, claim, descriptor)
			if err != nil {
				core.t.Fatal(err)
			}
			if _, err := indexing.ReplaceCanonTextChapterForClaim(ctx, indexingstore.ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: time.Now().UTC()}); err != nil {
				core.t.Fatal(err)
			}
		}
		if err := indexing.CompleteCanonTextIndexForClaim(ctx, claim, jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: core.bridgeCore.saga, SourceView: view, CatalogRevision: revision,
		}); err != nil {
			core.t.Fatal(err)
		}
	}
}

// cards runs the cards Jobs of the saga that wait — the map is their last step.
func (core *exportCore) cards() {
	core.t.Helper()
	ctx := context.Background()
	failed := func(_, stage string, err error) { core.t.Errorf("the cards Job failed at %s: %v", stage, err) }
	build := memoryservice.NewBuildCards(core.saga, core.saga, core.model, nil, "test-cards", 1, failed, nil).
		WithMap(memoryservice.NewMapBuilder(memoryservice.NewSaga(core.saga, core.saga), core.saga, materialsrepo.New(core.db, jobstore.New(core.db))), failed)
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		var jobID string
		var incarnation int64
		err := core.db.Pool.QueryRow(ctx, `
			UPDATE jobs SET status='running', claim_incarnation=claim_incarnation+1, claimed_at=clock_timestamp(), updated_at=clock_timestamp()
			WHERE project_id=$1 AND kind='chapter_cards' AND status='queued' AND subject_json->>'sourceView'=$2
			RETURNING id::text, claim_incarnation
		`, core.bridgeCore.saga, string(view)).Scan(&jobID, &incarnation)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			core.t.Fatal(err)
		}
		status, err := build.RunForClaim(ctx, jobs.WorkerClaim{ProjectID: core.bridgeCore.saga, JobID: jobID, ClaimIncarnation: incarnation},
			jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: core.bridgeCore.saga, SourceView: view}, true)
		if err != nil || status != jobs.StatusSucceeded {
			core.t.Fatalf("the cards Job of the %s text = %q, %v", view, status, err)
		}
	}
}

// exportUseCase is exportObsidianSaga of the build over the core of the test.
func (core *exportCore) exportUseCase() service.Export {
	return service.Export{
		Exports: obsidianrepo.NewExports(core.db), Saga: newObsidianSagaMap(app.Core{Saga: memoryservice.NewSaga(core.saga, core.saga), SagaStore: core.saga}),
		Writers: obsidianvault.Writers{}, Gate: service.NewGate(), Limits: service.DefaultLimits, Now: time.Now,
	}
}

func (core *exportCore) export(actor, key string, proof auth.SessionWriteProof, replace bool) (service.Exported, error) {
	return core.exportUseCase().Execute(context.Background(), service.ExportInput{
		ActorUserID: actor, ProjectID: core.bridgeCore.saga, ReplaceChanged: replace, Key: key, Proof: proof,
	})
}

func (core *exportCore) mustExport(key string, replace bool) service.Exported {
	core.t.Helper()
	exported, err := core.export(core.author, key, core.proof, replace)
	if err != nil {
		core.t.Fatalf("the export %s = %v", key, err)
	}
	if !exported.Complete {
		core.t.Fatalf("the export %s did not end: %+v", key, exported)
	}
	return exported
}

// hide takes a node or a link off the map, as «Убрать с карты» and «Убрать связь» do.
func (core *exportCore) hide(node, other string) {
	core.t.Helper()
	_, err := sagareference.NewHideOnMap(core.saga, core.saga, core.saga).Execute(context.Background(), sagareference.MapWrite{
		Scope: sagareference.Scope{ActorUserID: core.author, ProjectID: core.bridgeCore.saga, View: "published"}, Proof: core.proof,
	}, sagareference.HideInput{NodeKey: node, OtherKey: other})
	if err != nil {
		core.t.Fatal(err)
	}
}

// record is a record of «Ваши материалы» the book does not know.
func (core *exportCore) record(name string) {
	core.t.Helper()
	if _, err := materialsrepo.New(core.db, jobstore.New(core.db)).CreateAuthorMaterial(context.Background(), materialsservice.CreateCommand{
		Write:  materialsservice.Write{Actor: materialsservice.Actor{UserID: core.author, ProjectID: core.bridgeCore.saga}, Proof: core.proof},
		Replay: materialsservice.Replay{ScopeHash: "scope", OperationID: "author_materials.create.v1", KeyHash: "key-" + name, RequestHash: name},
		Draft:  authormaterials.Draft{Name: name, Kind: authormaterials.KindItem, Description: "Трон изо льда, о нём говорит Мара."},
		Key:    domain.GlossaryKey(name), At: time.Now(),
	}); err != nil {
		core.t.Fatal(err)
	}
}

// files are the files under the folder of the export with the times they were written, the folder of the app left out.
func (core *exportCore) files() map[string]time.Time {
	core.t.Helper()
	folder := filepath.Join(core.vault, "CanonKeeper", "Северный предел")
	files := map[string]time.Time{}
	_ = filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".canonkeeper" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(folder, path)
			files[filepath.ToSlash(relative)] = info.ModTime()
		}
		return nil
	})
	return files
}

func (core *exportCore) read(path string) string {
	core.t.Helper()
	data, err := os.ReadFile(filepath.Join(core.vault, "CanonKeeper", "Северный предел", filepath.FromSlash(path)))
	if err != nil {
		core.t.Fatal(err)
	}
	return string(data)
}

func (core *exportCore) count(query string, args ...any) int {
	core.t.Helper()
	var count int
	if err := core.db.Pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		core.t.Fatal(err)
	}
	return count
}

// specs/2026-11-15-desk-obsidian-export.md OE1.1, OE1.6, OE1.8, OE2.1, OE2.5, OE2.7 on the builders of the app: the first export
// writes Сага.md, a file a node the map draws — the node and the link the author took off it left out, the record of the author
// the book does not know its own node — and a file a chapter with a card in the folder of its book; the second changes no file;
// a file the author changed is not updated without his word and moved into _Удалено/ with it; a node taken off the map moves its
// reference there; a map behind its cards refuses with the Job of the cards queued and writes nothing. No model is called and no
// record of what went out is written.
func TestAnExportWritesTheMapAndKeepsTheAuthorPostgres(t *testing.T) {
	core := newExportCore(t, "ck_obsidian_export")
	core.importFile("Туманный берег.md", "# Туманный берег\n\n# Глава 1. Застава\n\nМара вышла к заставе.\n\n# Глава 2. Переправа\n\nСава ждал у переправы.\n",
		library.ImportModeNewBook, "", "export-book-1")
	core.importFile("Перевал.md", "# Перевал\n\n# Глава 3. Перевал\n\nГлеб поднялся на перевал.\n", library.ImportModeNewBook, "", "export-book-2")
	core.record("Ледяной трон")
	core.index()
	core.cards()
	core.hide("вера", "")
	core.hide("мара", "глеб")
	calls := core.model.calls
	journal := core.count(journalRows)

	first := core.mustExport("export-1", false)
	names := slices.Sorted(maps.Keys(core.files()))
	want := []string{
		"Герои/Глеб.md", "Герои/Мара.md", "Герои/Сава.md", "Главы/Перевал/3 Перевал.md", "Главы/Туманный берег/1 Застава.md",
		"Главы/Туманный берег/2 Переправа.md", "Места/Белый порт.md", "Предметы/Ледяной трон.md", "Сага.md",
	}
	if !slices.Equal(names, want) || first.Created.Count != len(want) || first.Folder != "CanonKeeper/Северный предел" {
		t.Fatalf("the first export = %+v, files %q", first, names)
	}
	// specs/2026-11-16-desk-links-writer.md LW1.1, LW1.2: a place is a link that opens the app at its paragraph, the reference of
	// a chapter opens the chapter — by the ids of the saga and of the chapter the database gave.
	chapter := core.read("Главы/Туманный берег/1 Застава.md")
	header, skip := obsidian.ReadNote("1 Застава.md", []byte(chapter))
	chapterID, _ := header.Front.Get("canonkeeper-key")
	if skip != "" || chapterID.Text == "" {
		t.Fatalf("the reference of a chapter has no key:\n%s", chapter)
	}
	open := "canonkeeper://open?saga=" + core.bridgeCore.saga + "&chapter=" + chapterID.Text
	mara := core.read("Герои/Мара.md")
	for _, word := range []string{
		"canonkeeper-saga: \"" + core.bridgeCore.saga + "\"", "canonkeeper-key: \"мара\"", "canonkeeper-kind: \"node\"",
		"Эту заметку ведёт CanonKeeper", "[[CanonKeeper/Северный предел/Герои/Сава|Сава]] — Вместе в",
		"[[CanonKeeper/Северный предел/Главы/Туманный берег/1 Застава|Книга 1, гл. 1 „Застава“]], [абзац 1](" + open + "&p=1)",
	} {
		if !strings.Contains(mara, word) {
			t.Fatalf("the reference of Мара has no %q:\n%s", word, mara)
		}
	}
	if strings.Contains(mara, "Герои/Глеб|Глеб]] — ") || strings.Contains(mara, "Вера|") {
		t.Fatalf("the reference of Мара has what the author took off the map:\n%s", mara)
	}
	if !strings.Contains(chapter, "## Кто в главе") || !strings.Contains(chapter, "\n[Открыть главу в CanonKeeper]("+open+")\n") ||
		!strings.Contains(chapter, "[[CanonKeeper/Северный предел/Места/Белый порт|Белый порт]]") || !strings.Contains(chapter, "- Вера") {
		t.Fatalf("the reference of a chapter:\n%s", chapter)
	}
	linked, err := core.vaults.Vault(context.Background(), core.author, core.bridgeCore.saga, service.AccessRead)
	if err != nil || linked.ExportDir != "CanonKeeper/Северный предел" || linked.ExportedAt == nil {
		t.Fatalf("the link after the export = %+v, %v", linked, err)
	}
	if core.model.calls != calls {
		t.Fatalf("the export called the model %d times", core.model.calls-calls)
	}

	// The same key answers what it kept; another key changes no file.
	written := core.files()
	if replayed := core.mustExport("export-1", false); replayed.Created.Count != first.Created.Count {
		t.Fatalf("the replay = %+v", replayed)
	}
	// The key of an export that ended is of its request: another word of the author under it is a conflict.
	if _, err := core.export(core.author, "export-1", core.proof, true); !errors.Is(err, service.ErrReplayConflict) {
		t.Fatalf("the key with another word = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	second := core.mustExport("export-2", false)
	if second.Unchanged.Count != len(want) || second.Created.Count+second.Updated.Count != 0 {
		t.Fatalf("the second export = %+v", second)
	}
	for name, at := range core.files() {
		if !at.Equal(written[name]) {
			t.Fatalf("%s was written again", name)
		}
	}

	// The author writes into the reference of Сава: it is not updated, then with his word moved into _Удалено/.
	sava := filepath.Join(core.vault, "CanonKeeper", "Северный предел", "Герои", "Сава.md")
	edited := core.read("Герои/Сава.md") + "\nМоя мысль о Саве.\n"
	if err := os.WriteFile(sava, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	core.hide("сава", "мара")
	kept := core.mustExport("export-3", false)
	if !slices.Contains(kept.Kept.Names, "Герои/Сава.md") || core.read("Герои/Сава.md") != edited {
		t.Fatalf("an export over a change of the author = %+v", kept)
	}
	replaced := core.mustExport("export-4", true)
	if !slices.Contains(replaced.Moved.Names, "_Удалено/Сава.md") || !slices.Contains(replaced.Updated.Names, "Герои/Сава.md") ||
		core.read("_Удалено/Сава.md") != edited || strings.Contains(core.read("Герои/Сава.md"), "Моя мысль") {
		t.Fatalf("an export with the word of the author = %+v", replaced)
	}

	// A node taken off the map: its reference goes into _Удалено/ under a free name.
	core.hide("сава", "")
	gone := core.mustExport("export-5", false)
	if len(gone.Moved.Names) != 1 || !strings.HasPrefix(gone.Moved.Names[0], "_Удалено/Сава 20") || core.read("_Удалено/Сава.md") != edited {
		t.Fatalf("a node taken off the map = %+v", gone)
	}
	if _, ok := core.files()["Герои/Сава.md"]; ok {
		t.Fatal("the reference of a node taken off the map stays")
	}

	// A new record of the author: the map is behind its cards — the Job of the cards is queued, nothing is written.
	core.record("Северный рог")
	before := core.files()
	queued := core.count(queuedCards, core.bridgeCore.saga)
	var notReady obsidian.MapNotReady
	if _, err := core.export(core.author, "export-6", core.proof, false); !errors.As(err, &notReady) || notReady.Reason != obsidian.MapUpdating {
		t.Fatalf("an export of a map behind its cards = %v", err)
	}
	if after := core.count(queuedCards, core.bridgeCore.saga); after == 0 || after < queued {
		t.Fatal("no Job of the cards is queued")
	}
	if after := core.files(); len(after) != len(before) {
		t.Fatalf("a refused export wrote: %v", after)
	}
	if core.model.calls != calls || core.count(journalRows) != journal {
		t.Fatal("the export called the model or wrote the journal")
	}

	// OE1.2: a link taken off and given again to the same folder during a run is another link — the run did not end: its time
	// is not written, its answer not kept for its key.
	ctx := context.Background()
	core.cards()
	relinked := core.exportUseCase()
	relinked.Failpoint = func(point string) error {
		if point == "planned" {
			if err := core.vaults.Unlink(ctx, core.author, core.bridgeCore.saga); err != nil {
				return err
			}
			return core.vaults.Link(ctx, core.bridgeCore.saga, core.vault)
		}
		return nil
	}
	partial, err := relinked.Execute(ctx, service.ExportInput{ActorUserID: core.author, ProjectID: core.bridgeCore.saga, Key: "export-7", Proof: core.proof})
	if err != nil || partial.Complete {
		t.Fatalf("an export whose link was given again = %+v, %v", partial, err)
	}
	if linked, err := core.vaults.Vault(ctx, core.author, core.bridgeCore.saga, service.AccessRead); err != nil || linked.ExportedAt != nil {
		t.Fatalf("the link given again = %+v, %v", linked, err)
	}
	if again := core.mustExport("export-7", false); !again.Complete {
		t.Fatalf("the key of a run that did not end = %+v", again)
	}

	// OE1.2: a link to the same path keeps the time of the last export, a new path of the vault forgets it; the folder stays.
	if err := core.vaults.Link(ctx, core.bridgeCore.saga, core.vault); err != nil {
		t.Fatal(err)
	}
	if linked, err := core.vaults.Vault(ctx, core.author, core.bridgeCore.saga, service.AccessRead); err != nil || linked.ExportedAt == nil {
		t.Fatalf("the link to the same path = %+v, %v", linked, err)
	}
	if err := core.vaults.Link(ctx, core.bridgeCore.saga, filepath.Join(t.TempDir(), "Другое хранилище")); err != nil {
		t.Fatal(err)
	}
	if linked, err := core.vaults.Vault(ctx, core.author, core.bridgeCore.saga, service.AccessRead); err != nil || linked.ExportedAt != nil ||
		linked.ExportDir != "CanonKeeper/Северный предел" {
		t.Fatalf("the link to a new path = %+v, %v", linked, err)
	}
}

// OE1.1, OE1.6: a saga without cards, another session and a reader with his own session are refused before the vault is
// touched — the session by the session, the reader by his role.
func TestAnExportIsRefusedBeforeTheVaultPostgres(t *testing.T) {
	core := newExportCore(t, "ck_obsidian_export_refused")
	core.importFile("Туманный берег.md", "# Туманный берег\n\n# Глава 1. Застава\n\nМара вышла к заставе.\n", library.ImportModeNewBook, "", "export-refused-book")
	var notReady obsidian.MapNotReady
	if _, err := core.export(core.author, "export-1", core.proof, false); !errors.As(err, &notReady) || notReady.Reason != obsidian.MapNoCards {
		t.Fatalf("a saga without cards = %v", err)
	}
	other := auth.SessionWriteProof{SessionHash: strings.Repeat("e", 64), CSRFSecretHash: strings.Repeat("f", 64)}
	if _, err := core.export(core.author, "export-2", other, false); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("another session = %v", err)
	}
	reader := core.invite("reader@example.test", projects.RoleViewer)
	if _, err := core.export(reader.id, "export-3", reader.proof, false); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a reader = %v", err)
	}
	editor := core.invite("editor@example.test", projects.RoleEditor)
	if err := editor.logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := core.export(editor.id, "export-4", editor.proof, false); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an editor whose sessions ended = %v", err)
	}
	if _, err := os.Stat(filepath.Join(core.vault, "CanonKeeper")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a refused export touched the vault")
	}
}
