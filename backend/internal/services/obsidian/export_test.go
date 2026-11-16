// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/obsidian"
)

// exportStore is the link of one saga of a test with its vault and the answers of its keys.
type exportStore struct {
	mu      sync.Mutex
	start   service.ExportStart
	linked  bool
	ended   *time.Time
	answers map[string]service.Exported
	// unlinkAtEnd takes the link off when the run asks it the last time.
	unlinkAtEnd bool
}

func (store *exportStore) BeginExport(_ context.Context, actor, _ string, _ auth.SessionWriteProof) (service.ExportStart, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if actor == "reader" {
		return service.ExportStart{}, service.ErrForbidden
	}
	return store.start, nil
}

func (store *exportStore) KeepExportDir(_ context.Context, _, path, dir string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.linked || path != store.start.VaultPath {
		return service.ErrNotLinked
	}
	store.start.ExportDir = dir
	return nil
}

func (store *exportStore) Linked(_ context.Context, _, path, dir string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.unlinkAtEnd {
		store.linked = false
	}
	return store.linked && path == store.start.VaultPath && dir == store.start.ExportDir, nil
}

func (store *exportStore) EndExport(_ context.Context, _, path, dir string, at time.Time) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.linked || path != store.start.VaultPath || dir != store.start.ExportDir {
		return false, nil
	}
	store.ended = &at
	return true, nil
}

func (store *exportStore) ExportReplay(_ context.Context, key service.ReplayKey) (*service.Exported, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if answer, ok := store.answers[key.Key+":"+key.Request]; ok {
		return &answer, nil
	}
	return nil, nil
}

func (store *exportStore) KeepExport(_ context.Context, key service.ReplayKey, exported service.Exported) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.answers[key.Key+":"+key.Request] = exported
	return nil
}

// sagaReads are the reads of the pages over a saga of the boards: two chapters of one book with cards, the nodes Мара and
// Сава, and a map that is fresh, behind its cards or without cards.
type sagaReads struct {
	building bool
	noCards  bool
	nodes    []string
	// kind is the type the cards of the nodes say, «героиня» when none.
	kind string
}

func (reads sagaReads) Map(context.Context, string, string) (sagamemory.MapDocument, error) {
	document := sagamemory.MapDocument{Coverage: sagamemory.MapCoverage{Chapters: 2, WithCards: 2}, Building: reads.building}
	if reads.noCards {
		document.Coverage.WithCards = 0
	}
	for _, name := range reads.nodes {
		document.Nodes = append(document.Nodes, sagamemory.MapDocumentNode{Key: sagamemory.GlossaryKey(name), Name: name, Category: "character"})
	}
	return document, nil
}

func (reads sagaReads) Node(_ context.Context, _, _, key string) (sagamemory.MapCardDocument, error) {
	for _, name := range reads.nodes {
		if sagamemory.GlossaryKey(name) == key {
			kind := reads.kind
			if kind == "" {
				kind = "героиня"
			}
			return sagamemory.MapCardDocument{Key: key, Name: name, Type: kind, Mentions: 2}, nil
		}
	}
	return sagamemory.MapCardDocument{}, service.ErrNodeGone
}

func (sagaReads) Chapters(context.Context, string) (*sagamemory.Version, *sagamemory.CardSet, error) {
	version := sagamemory.NewVersion("saga-1", "published", "fp", []sagamemory.Chapter{
		{ID: "ch-1", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 1, Title: "Глава 1. Застава"},
		{ID: "ch-2", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 2, Title: "Глава 2. Переправа"},
	})
	cards := map[int]*sagamemory.Card{
		0: {Summary: "Мара у ворот.", Characters: []sagamemory.Mention{{Name: "Мара"}, {Name: "Сава"}}},
		1: {Summary: "Сава у переправы.", Characters: []sagamemory.Mention{{Name: "Сава"}}},
	}
	return version, sagamemory.NewCardSet(cards, nil), nil
}

// exportCase is an export of a test: its vault on the disk and its use case.
type exportCase struct {
	t      *testing.T
	vault  string
	store  *exportStore
	export service.Export
}

func newExportCase(t *testing.T) *exportCase {
	t.Helper()
	vault := filepath.Join(t.TempDir(), "Хранилище")
	if err := os.MkdirAll(filepath.Join(vault, "Мои заметки"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Мои заметки", "Мысль.md"), []byte("моя мысль\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &exportStore{start: service.ExportStart{Title: "Северный предел", VaultPath: vault}, linked: true, answers: map[string]service.Exported{}}
	return &exportCase{t: t, vault: vault, store: store, export: service.Export{
		Exports: store, Saga: sagaReads{nodes: []string{"Мара", "Сава"}}, Writers: obsidianvault.Writers{}, Gate: service.NewGate(),
		Limits: service.Limits{Scan: time.Second, Read: 10 * time.Second, Note: 5 * time.Second, Write: time.Minute},
		Now:    func() time.Time { return time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC) },
	}}
}

func (run *exportCase) run(key string, replace bool) (service.Exported, error) {
	return run.export.Execute(context.Background(), service.ExportInput{ActorUserID: "author", ProjectID: "saga-1", ReplaceChanged: replace, Key: key})
}

func (run *exportCase) must(key string, replace bool) service.Exported {
	run.t.Helper()
	exported, err := run.run(key, replace)
	if err != nil {
		run.t.Fatalf("the export %s = %v", key, err)
	}
	return exported
}

// folder is the folder of the export on the disk.
func (run *exportCase) folder() string {
	return filepath.Join(run.vault, "CanonKeeper", "Северный предел")
}

// files are the paths of the files under the folder of the export, its folder of the app left out.
func (run *exportCase) files() []string {
	run.t.Helper()
	var files []string
	_ = filepath.WalkDir(run.folder(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".canonkeeper" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			relative, _ := filepath.Rel(run.folder(), path)
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	})
	slices.Sort(files)
	return files
}

func (run *exportCase) manifest() obsidian.Manifest {
	run.t.Helper()
	data, err := os.ReadFile(filepath.Join(run.folder(), ".canonkeeper", "manifest.json"))
	if err != nil {
		run.t.Fatal(err)
	}
	manifest, err := obsidian.ReadManifest(data)
	if err != nil {
		run.t.Fatal(err)
	}
	return manifest
}

var exportedFiles = []string{"Герои/Мара.md", "Герои/Сава.md", "Главы/Туманный берег/1 Застава.md", "Главы/Туманный берег/2 Переправа.md", "Сага.md"}

// specs/2026-11-15-desk-obsidian-export.md OE2.8, OE1.1: a run cut after two references leaves them named by its manifest,
// not complete; the next run comes to the same files as one whole run — no « (2)» —, keeps its answer for its key, and its
// replay writes nothing.
func TestAnInterruptedExportComesToTheSameFiles(t *testing.T) {
	run := newExportCase(t)
	written, keptBefore := 0, ""
	run.export.Failpoint = func(point string) error {
		if point == "written" {
			if written++; written == 1 {
				run.store.mu.Lock()
				keptBefore = run.store.start.ExportDir
				run.store.mu.Unlock()
			}
			if written == 2 {
				return errors.New("the process stops")
			}
		}
		return nil
	}
	if _, err := run.run("key-1", false); err == nil {
		t.Fatal("the cut run answered")
	}
	// The two written are named as written, the others as the writes the run named before its first one (OE2.8).
	manifest := run.manifest()
	done := slices.DeleteFunc(slices.Clone(manifest.Files), func(file obsidian.ManifestFile) bool { return file.Writing != nil || file.Print == "" })
	if manifest.Complete || len(done) != 2 || len(manifest.Files) != len(exportedFiles) || len(run.files()) != 2 {
		t.Fatalf("after the cut: %+v, files %q", manifest, run.files())
	}
	// OE1.5: the folder the first run chose is kept on the link before its first file.
	if keptBefore != "CanonKeeper/Северный предел" {
		t.Fatalf("the folder of the first file is kept as %q", keptBefore)
	}
	run.export.Failpoint = nil
	exported := run.must("key-1", false)
	if !exported.Complete || exported.Created.Count != 3 || exported.Unchanged.Count != 2 || !slices.Equal(run.files(), exportedFiles) {
		t.Fatalf("the run after the cut = %+v, files %q", exported, run.files())
	}
	if manifest := run.manifest(); !manifest.Complete || len(manifest.Files) != len(exportedFiles) {
		t.Fatalf("the manifest = %+v", manifest)
	}
	if run.store.start.ExportDir != "CanonKeeper/Северный предел" || run.store.ended == nil {
		t.Fatalf("the link = %+v, ended %v", run.store.start, run.store.ended)
	}
	// The replay of the key answers what it kept and writes nothing — a reference the author removed meanwhile stays removed.
	if err := os.Remove(filepath.Join(run.folder(), "Сага.md")); err != nil {
		t.Fatal(err)
	}
	if replayed := run.must("key-1", false); replayed.Created.Count != 3 || slices.Contains(run.files(), "Сага.md") {
		t.Fatalf("the replay = %+v, files %q", replayed, run.files())
	}
	// Another key exports again: the reference comes back on its path.
	if again := run.must("key-2", false); again.Created.Count != 1 || again.Created.Names[0] != "Сага.md" || again.Unchanged.Count != 4 {
		t.Fatalf("the run after = %+v", again)
	}
}

// OE1.2: a link taken off during a run leaves what it wrote named by the manifest, not complete, the time of the link not
// written, the answer of its key not kept — the next request exports again.
func TestAnExportWhoseLinkWentIsPartial(t *testing.T) {
	run := newExportCase(t)
	run.store.unlinkAtEnd = true
	exported := run.must("key-1", false)
	if exported.Complete || exported.Created.Count != len(exportedFiles) || run.store.ended != nil || len(run.store.answers) != 0 {
		t.Fatalf("a run whose link went = %+v", exported)
	}
	if manifest := run.manifest(); manifest.Complete || len(manifest.Files) != len(exportedFiles) {
		t.Fatalf("the manifest = %+v", manifest)
	}
}

// OE1.4: a reference the author changed between the reading of the plan and its replacement is not touched, and a file put at
// the path of a new reference meanwhile stays his — the reference takes « (2)».
func TestWhatChangesBetweenThePlanAndTheWriteStaysTheAuthors(t *testing.T) {
	run := newExportCase(t)
	run.must("key-1", false)
	// A new node and a new text of the old ones: the plan replaces Мара.md and creates Глеб.md.
	run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава", "Глеб"}, kind: "капитан"}
	mara := filepath.Join(run.folder(), "Герои", "Мара.md")
	data, err := os.ReadFile(mara)
	if err != nil {
		t.Fatal(err)
	}
	gleb := filepath.Join(run.folder(), "Герои", "Глеб.md")
	run.export.Failpoint = func(point string) error {
		if point == "planned" {
			if err := os.WriteFile(mara, append(data, []byte("правка автора\n")...), 0o644); err != nil {
				return err
			}
			return os.WriteFile(gleb, []byte("файл автора\n"), 0o644)
		}
		return nil
	}
	exported := run.must("key-2", false)
	if !slices.Contains(exported.Kept.Names, "Герои/Мара.md") || !slices.Contains(exported.Created.Names, "Герои/Глеб (2).md") {
		t.Fatalf("the run = %+v", exported)
	}
	if text, _ := os.ReadFile(mara); !strings.HasSuffix(string(text), "правка автора\n") {
		t.Fatal("the change of the author is lost")
	}
	if text, _ := os.ReadFile(gleb); string(text) != "файл автора\n" {
		t.Fatal("the file of the author is written over")
	}
}

// OE1.5, OE1.9 (д): a folder of the name of the saga without its manifest and a folder of another saga are not touched — the
// export takes « (3)»; the manifest of the folder of the saga that does not read refuses the export, nothing written.
func TestAFolderThatIsNotTheSagasIsNotTouched(t *testing.T) {
	run := newExportCase(t)
	write := func(name, text string) {
		t.Helper()
		full := filepath.Join(run.vault, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CanonKeeper/Северный предел/Мои планы.md", "планы автора\n")
	other := obsidian.Manifest{Format: obsidian.ExportFormat, Saga: "saga-other"}
	write("CanonKeeper/Северный предел (2)/.canonkeeper/manifest.json", string(other.Encode()))
	write("CanonKeeper/Северный предел (2)/Герои/Мара.md", "другая сага\n")
	exported := run.must("key-1", false)
	if exported.Folder != "CanonKeeper/Северный предел (3)" || run.store.start.ExportDir != exported.Folder {
		t.Fatalf("the folder = %q, kept %q", exported.Folder, run.store.start.ExportDir)
	}
	if text, _ := os.ReadFile(filepath.Join(run.vault, "CanonKeeper", "Северный предел (2)", "Герои", "Мара.md")); string(text) != "другая сага\n" {
		t.Fatal("a folder of another saga changed")
	}
	if entries, _ := os.ReadDir(filepath.Join(run.vault, "CanonKeeper", "Северный предел")); len(entries) != 1 {
		t.Fatalf("a folder without a manifest changed: %v", entries)
	}
	// The manifest of the folder the saga keeps no longer reads.
	write("CanonKeeper/Северный предел (3)/.canonkeeper/manifest.json", "{")
	before, _ := os.ReadFile(filepath.Join(run.vault, "CanonKeeper", "Северный предел (3)", "Сага.md"))
	if _, err := run.run("key-2", false); !errors.Is(err, obsidian.ErrExportManifestUnreadable) {
		t.Fatalf("a manifest that does not read = %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(run.vault, "CanonKeeper", "Северный предел (3)", "Сага.md")); string(after) != string(before) {
		t.Fatal("a refused export wrote")
	}
}

// OE1.6: a map behind its cards or a saga without cards refuses before the vault is touched.
func TestAMapThatIsNotReadyWritesNothing(t *testing.T) {
	run := newExportCase(t)
	for reads, reason := range map[*sagaReads]obsidian.MapReason{
		{building: true, nodes: []string{"Мара"}}: obsidian.MapUpdating, {noCards: true}: obsidian.MapNoCards, {nodes: []string{"Мара", "Нет"}}: obsidian.MapUpdating,
	} {
		run.export.Saga = sagaSubset{reads: *reads}
		var notReady obsidian.MapNotReady
		if _, err := run.run("key", false); !errors.As(err, &notReady) || notReady.Reason != reason {
			t.Fatalf("%+v = %v", *reads, err)
		}
	}
	if _, err := os.Stat(filepath.Join(run.vault, "CanonKeeper")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a refused export touched the vault")
	}
}

// sagaSubset is a saga whose map names a node the cards of the nodes do not know — «Нет» — as a map that changed meanwhile.
type sagaSubset struct{ reads sagaReads }

func (subset sagaSubset) Map(ctx context.Context, actor, project string) (sagamemory.MapDocument, error) {
	return subset.reads.Map(ctx, actor, project)
}
func (subset sagaSubset) Node(ctx context.Context, actor, project, key string) (sagamemory.MapCardDocument, error) {
	if key == sagamemory.GlossaryKey("Нет") {
		return sagamemory.MapCardDocument{}, service.ErrNodeGone
	}
	return subset.reads.Node(ctx, actor, project, key)
}
func (subset sagaSubset) Chapters(ctx context.Context, project string) (*sagamemory.Version, *sagamemory.CardSet, error) {
	return subset.reads.Chapters(ctx, project)
}

// OE1.1: two requests at once are one export — the second is busy —, and a reader is refused before the vault is read.
func TestTwoExportsAtOnceAreOne(t *testing.T) {
	run := newExportCase(t)
	planned, release := make(chan struct{}), make(chan struct{})
	run.export.Failpoint = func(point string) error {
		if point == "planned" {
			close(planned)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := run.run("key-1", false)
		done <- err
	}()
	select {
	case <-planned:
	case err := <-done:
		t.Fatalf("the first export ended before its writes: %v", err)
	}
	var busy service.Busy
	if _, err := run.run("key-2", false); !errors.As(err, &busy) {
		t.Fatalf("a second export = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := run.export.Execute(context.Background(), service.ExportInput{ActorUserID: "reader", ProjectID: "saga-1", Key: "key-3"}); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a reader = %v", err)
	}
}

// hangingWriters open the real writer of a vault whose write of one path does not return until the test lets it go.
type hangingWriters struct {
	path    string
	entered chan struct{}
	release chan struct{}
}

func (writers hangingWriters) OpenWriter(vaultPath string) (service.VaultWriter, error) {
	writer, err := obsidianvault.Writers{}.OpenWriter(vaultPath)
	if err != nil {
		return nil, err
	}
	return hangingWriter{VaultWriter: writer, writers: writers}, nil
}

type hangingWriter struct {
	service.VaultWriter
	writers hangingWriters
}

func (writer hangingWriter) Write(ctx context.Context, path string, text []byte, expect string) error {
	if path == writer.writers.path {
		close(writer.writers.entered)
		<-writer.writers.release
	}
	return writer.VaultWriter.Write(ctx, path, text, expect)
}

// OE1.2: a write that does not return within its time answers at once with what was done; the place of the saga is held until
// it returns — no next export goes on top of it —, and once it returned the manifest names what it wrote.
func TestAWriteThatHangsHoldsThePlaceOfTheSaga(t *testing.T) {
	run := newExportCase(t)
	run.must("key-0", false)
	// The next run replaces the references of the nodes, and the write of Сава.md does not return: the run answers past the
	// time of that one write — the time of a write of the test is short, the writes before it are only the folder of the app
	// and Мара.md.
	run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава"}, kind: "капитан"}
	hang := hangingWriters{path: "Герои/Сава.md", entered: make(chan struct{}), release: make(chan struct{})}
	run.export.Writers = hang
	run.export.Limits.Note = 2 * time.Second
	run.store.ended = nil
	exported := run.must("key-1", false)
	select {
	case <-hang.entered:
	default:
		t.Fatalf("the run ended before the write that hangs: %+v", exported)
	}
	if exported.Complete || slices.Contains(exported.Updated.Names, "Герои/Сава.md") || run.store.ended != nil {
		t.Fatalf("a run with a write that hangs = %+v", exported)
	}
	var busy service.Busy
	if _, err := run.run("key-2", false); !errors.As(err, &busy) {
		t.Fatalf("an export while a write hangs = %v", err)
	}
	close(hang.release)
	deadline := time.Now().Add(time.Minute)
	fresh := func(file obsidian.ManifestFile) bool {
		data, err := os.ReadFile(filepath.Join(run.folder(), "Герои", "Сава.md"))
		return err == nil && file.Path == "Герои/Сава.md" && file.Sum == obsidian.ExportSum(data) && strings.Contains(string(data), "капитан")
	}
	for {
		if manifest := run.manifest(); slices.ContainsFunc(manifest.Files, fresh) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the manifest does not name the write that returned late")
		}
		time.Sleep(10 * time.Millisecond)
	}
	run.export.Writers = obsidianvault.Writers{}
	run.export.Limits.Note = 5 * time.Second
	var after service.Exported
	for {
		var err error
		if after, err = run.run("key-3", false); !errors.As(err, &busy) {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the place of the saga is not given back")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !after.Complete || slices.Contains(run.files(), "Герои/Сава (2).md") || !slices.Contains(after.Unchanged.Names, "Герои/Сава.md") ||
		len(after.Kept.Names) != 0 {
		t.Fatalf("the run after = %+v, files %q", after, run.files())
	}
}

// OE1.9: once a saga is exported, the reading of the bridge does not see its folder, nor a folder of an export of another
// computer: the look, the preview of the vault as one book and as materials, and «Обновить из Obsidian» of a book linked with
// the root of the vault; a request that names a folder of an export is not a path of the vault.
func TestTheBridgeDoesNotReadAFolderOfAnExport(t *testing.T) {
	ctx := context.Background()
	run := newExportCase(t)
	write(t, run.vault, "Пролог.md", "Ольга вышла к заставе.")
	run.must("key-1", false)
	other := obsidian.Manifest{Format: obsidian.ExportFormat, Saga: "saga-other"}
	write(t, run.vault, "Архив/.canonkeeper/manifest.json", string(other.Encode()))
	write(t, run.vault, "Архив/Мара.md", "Справка другого компьютера.")
	exported := func(path string) bool {
		return strings.HasPrefix(path, "CanonKeeper/") || strings.HasPrefix(path, "Архив")
	}
	scanned, err := scanOf(&vaults{paths: map[string]string{"saga": run.vault}}, obsidianvault.Files{}).Execute(ctx, service.ScanInput{ActorUserID: "author", ProjectID: "saga"})
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range scanned.Folders {
		if exported(folder.Path) {
			t.Fatalf("the look sees %s", folder.Path)
		}
	}
	preview, err := run.preview(obsidian.Choice{Books: []string{obsidian.Root}})
	if err != nil || len(preview.Books) != 1 {
		t.Fatalf("the preview of the vault as a book = %+v, %v", preview, err)
	}
	for _, note := range preview.Books[0].Notes {
		if exported(note.Path) {
			t.Fatalf("the book takes %s", note.Path)
		}
	}
	records, err := run.preview(obsidian.Choice{Materials: []obsidian.MaterialFolder{{Path: obsidian.Root}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records.Materials.Records {
		if exported(record.Path) {
			t.Fatalf("the records take %s", record.Path)
		}
	}
	for _, folder := range []string{"CanonKeeper/Северный предел", "CanonKeeper/Северный предел/Герои", "Архив"} {
		if _, err := run.preview(obsidian.Choice{Books: []string{folder}}); !errors.Is(err, obsidian.ErrPath) {
			t.Fatalf("a preview of %s = %v", folder, err)
		}
	}
	update := service.UpdateBook{
		Vaults: &vaults{paths: map[string]string{"saga": run.vault}}, Files: obsidianvault.Files{},
		Links: &bookLinks{folders: []service.HeldFolder{{Folder: obsidian.Root, Book: &service.LinkedBook{BookID: "b-1", Title: "Застава"}}}},
		Plans: plans{"b-1": true}, Volumes: volumes{}, Shelf: &queueShelf{}, Gate: service.NewGate(), Limits: service.DefaultLimits,
		Logger: log.New(io.Discard, "", 0),
	}
	updated, err := update.Execute(ctx, service.UpdateInput{ActorUserID: "author", ProjectID: "saga", BookID: "b-1", Key: "update-key-0001", Method: "POST", Path: "/u"})
	if err != nil || updated.Book == nil || strings.Contains(updated.Book.Glued.Text, "CanonKeeper") || strings.Contains(updated.Book.Glued.Text, "Справка") {
		t.Fatalf("the update of a book linked with the root = %+v, %v", updated.Book, err)
	}
}

// preview is a preview of the vault of the export by a choice.
func (run *exportCase) preview(choice obsidian.Choice) (service.Preview, error) {
	return previewOf(run.vault, links{}, obsidianvault.Files{}).Execute(context.Background(), service.PreviewInput{ActorUserID: "author", ProjectID: "saga", Choice: choice})
}

// keepFails opens the real writer of a vault whose manifest is not written from its n-th time on, once: a process stopped,
// a disk full or a manifest Windows holds past the wait — right after a reference was renamed into its place.
type keepFails struct {
	n     int
	calls *int
}

func (writers keepFails) OpenWriter(vaultPath string) (service.VaultWriter, error) {
	writer, err := obsidianvault.Writers{}.OpenWriter(vaultPath)
	if err != nil {
		return nil, err
	}
	return keepFailing{VaultWriter: writer, writers: writers}, nil
}

type keepFailing struct {
	service.VaultWriter
	writers keepFails
}

func (writer keepFailing) Keep(ctx context.Context, manifest []byte) error {
	if *writer.writers.calls++; *writer.writers.calls == writer.writers.n {
		return errors.New("the manifest is not written")
	}
	return writer.VaultWriter.Keep(ctx, manifest)
}

// OE2.8: a run cut right after a rename, before the manifest says it, leaves the next run neither a duplicate « (2)» nor a
// reference of its own it takes for one the author changed; a file the author changed after the cut stays his.
func TestARunCutBetweenARenameAndItsRecordKnowsItsFiles(t *testing.T) {
	cut := func(run *exportCase, n int) {
		t.Helper()
		calls := 0
		run.export.Writers = keepFails{n: n, calls: &calls}
		if first := run.must("key-cut", false); first.Complete {
			t.Fatalf("the cut run = %+v", first)
		}
		run.export.Writers = obsidianvault.Writers{}
	}
	// A new reference: the manifest of the first run never says Сага.md was written.
	run := newExportCase(t)
	cut(run, 2)
	if !slices.Contains(run.files(), "Сага.md") {
		t.Fatalf("the cut run wrote %q", run.files())
	}
	exported := run.must("key-1", false)
	if !exported.Complete || !slices.Equal(run.files(), exportedFiles) || exported.Kept.Count != 0 || !slices.Contains(exported.Unchanged.Names, "Сага.md") {
		t.Fatalf("the run after the cut = %+v, files %q", exported, run.files())
	}
	// A replacement: the fresh text is on the disk, the manifest says the old one.
	run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава"}, kind: "капитан"}
	cut(run, 2)
	again := run.must("key-2", false)
	if !again.Complete || again.Kept.Count != 0 || !slices.Equal(run.files(), exportedFiles) {
		t.Fatalf("the run after a cut replacement = %+v, files %q", again, run.files())
	}
	for _, name := range []string{"Герои/Мара.md", "Герои/Сава.md"} {
		if data, _ := os.ReadFile(filepath.Join(run.folder(), filepath.FromSlash(name))); !strings.Contains(string(data), "капитан") {
			t.Fatalf("%s is not fresh:\n%s", name, data)
		}
	}
	// The author writes into the reference the cut run wrote and the manifest does not say: it is his change, kept.
	other := newExportCase(t)
	cut(other, 2)
	saga := filepath.Join(other.folder(), "Сага.md")
	data, err := os.ReadFile(saga)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(data) + "моя мысль\n"
	if err := os.WriteFile(saga, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	// Its content did not change since: it is neither read nor written (OE2.5); once it changes, the change of the author is
	// kept.
	same := other.must("key-1", false)
	other.export.Saga = sagaReads{nodes: []string{"Мара", "Сава", "Глеб"}}
	kept := other.must("key-2", false)
	if !slices.Contains(same.Unchanged.Names, "Сага.md") || !slices.Contains(kept.Kept.Names, "Сага.md") ||
		slices.ContainsFunc(other.files(), func(name string) bool { return strings.Contains(name, " (2)") }) {
		t.Fatalf("the runs after the cut and the change = %+v, %+v, files %q", same, kept, other.files())
	}
	if after, _ := os.ReadFile(saga); string(after) != edited {
		t.Fatal("the change of the author is lost")
	}
}

// OE1.5, OE2.8: a first run cut after it made its folder and before its first manifest leaves a folder with nothing but the
// folder of the app in it; the next run takes it — no folder « (2)» —, and its temporary files go.
func TestAFirstRunCutBeforeItsManifestLeavesNoSecondFolder(t *testing.T) {
	run := newExportCase(t)
	calls := 0
	run.export.Writers = keepFails{n: 1, calls: &calls}
	if first := run.must("key-cut", false); first.Complete || first.Created.Count != 0 {
		t.Fatalf("the cut run = %+v", first)
	}
	// What a manifest stopped in the middle of its write leaves.
	write(t, run.folder(), ".canonkeeper/tmp/left.tmp", "{")
	run.export.Writers = obsidianvault.Writers{}
	exported := run.must("key-1", false)
	if exported.Folder != "CanonKeeper/Северный предел" || !exported.Complete || !slices.Equal(run.files(), exportedFiles) {
		t.Fatalf("the run after the cut = %+v, files %q", exported, run.files())
	}
	if entries, _ := os.ReadDir(filepath.Join(run.vault, "CanonKeeper")); len(entries) != 1 {
		t.Fatalf("the folders of the export: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(run.folder(), ".canonkeeper", "tmp", "left.tmp")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the temporary file of the cut run stays: %v", err)
	}
}

// tree is every file under a folder with its bytes.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(full)
		relative, _ := filepath.Rel(root, full)
		files[filepath.ToSlash(relative)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// OE1.5: the folder the saga keeps is a path from the root of its vault, and a new path of the vault keeps it; there it may be
// the folder of another saga or of the author. Such a folder is not touched — no reference of the other saga goes into
// _Удалено/, no note of the author is written over —, and the export chooses its folder anew by the rule of a first one.
func TestTheFolderASagaKeepsIsNotTakenWhereItIsAnothers(t *testing.T) {
	run := newExportCase(t)
	run.must("key-0", false)
	remembered := run.store.start.ExportDir
	// A vault with the export of another saga at the path this saga keeps.
	other := newExportCase(t)
	other.export.Saga = sagaReads{nodes: []string{"Глеб", "Вера"}}
	if exported, err := other.export.Execute(context.Background(), service.ExportInput{ActorUserID: "author", ProjectID: "saga-other", Key: "other"}); err != nil || !exported.Complete {
		t.Fatalf("the export of the other saga = %+v, %v", exported, err)
	}
	// A vault with a folder of the author at that path.
	authors := filepath.Join(t.TempDir(), "Хранилище автора")
	write(t, authors, remembered+"/Мои планы.md", "планы автора\n")
	for _, vault := range []string{other.vault, authors} {
		before := tree(t, filepath.Join(vault, filepath.FromSlash(remembered)))
		run.store.start.VaultPath, run.store.start.ExportDir = vault, remembered
		exported := run.must("key-"+filepath.Base(vault), false)
		if exported.Folder != remembered+" (2)" || run.store.start.ExportDir != exported.Folder || !exported.Complete || exported.Moved.Count != 0 {
			t.Fatalf("the export into %s = %+v, kept %q", vault, exported, run.store.start.ExportDir)
		}
		if after := tree(t, filepath.Join(vault, filepath.FromSlash(remembered))); !maps.Equal(after, before) {
			t.Fatalf("the folder of someone else changed:\n%v\n%v", before, after)
		}
	}
}

// OE2.6: a reference the author moved within the folder of the export — renamed, or put into a folder of his — is found by its
// key: the run updates it where it lies, names its new path in the manifest and writes no second one at the path before.
func TestAReferenceTheAuthorMovedIsUpdatedWhereItLies(t *testing.T) {
	run := newExportCase(t)
	run.must("key-1", false)
	moved := filepath.Join(run.folder(), "Мои", "Мара — справка.md")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(run.folder(), "Герои", "Мара.md"), moved); err != nil {
		t.Fatal(err)
	}
	run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава"}, kind: "капитан"}
	exported := run.must("key-2", false)
	if !slices.Contains(exported.Updated.Names, "Мои/Мара — справка.md") || slices.Contains(run.files(), "Герои/Мара.md") {
		t.Fatalf("the run after the move = %+v, files %q", exported, run.files())
	}
	if data, _ := os.ReadFile(moved); !strings.Contains(string(data), "капитан") {
		t.Fatalf("the moved reference is not fresh:\n%s", data)
	}
	if !slices.ContainsFunc(run.manifest().Files, func(file obsidian.ManifestFile) bool {
		return file.Key == "мара" && file.Path == "Мои/Мара — справка.md"
	}) {
		t.Fatalf("the manifest = %+v", run.manifest().Files)
	}
}

// OE2.5 on a run: a synchronizer that turned the ends of the lines into CRLF and added the mark of the byte order, and Obsidian
// that rewrote the target of a link, changed no reference of the author — the next run updates them.
func TestWhatASynchronizerAndObsidianRewriteIsNoChangeOfTheAuthor(t *testing.T) {
	run := newExportCase(t)
	run.must("key-1", false)
	rewrite := func(name string, change func(string) string) {
		t.Helper()
		full := filepath.Join(run.folder(), filepath.FromSlash(name))
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("\uFEFF"+strings.ReplaceAll(change(string(data)), "\n", "\r\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rewrite("Герои/Мара.md", func(text string) string { return text })
	rewrite("Сага.md", func(text string) string {
		if !strings.Contains(text, "[[CanonKeeper/Северный предел/Герои/Сава|") {
			t.Fatalf("Сага.md has no link to Сава:\n%s", text)
		}
		return strings.Replace(text, "[[CanonKeeper/Северный предел/Герои/Сава|", "[[Сава|", 1)
	})
	run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава", "Глеб"}, kind: "капитан"}
	exported := run.must("key-2", false)
	if exported.Kept.Count != 0 || !slices.Contains(exported.Updated.Names, "Герои/Мара.md") || !slices.Contains(exported.Updated.Names, "Сага.md") {
		t.Fatalf("the run after the rewrites = %+v", exported)
	}
}

// OE1.5: two sagas of one title exported at once into one vault never take one folder. The choice of a folder holds the
// process from the choice until the folder it makes holds its manifest — a first export, and an export whose folder the
// author deleted, which goes as a first one —: the second saga waits for it, then goes past it. A choice that finds the folder
// of its saga with its manifest makes nothing and holds nothing: the second saga does not wait for its reading and writes.
func TestTwoSagasOfOneTitleNeverTakeOneFolder(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(run *exportCase)
		waits bool
	}{
		{"a first export", func(*exportCase) {}, true},
		{"the folder the saga keeps is deleted", func(run *exportCase) {
			run.must("key-0", false)
			if err := os.RemoveAll(run.folder()); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"the folder of the saga is found by its manifest", func(run *exportCase) {
			run.must("key-0", false)
			run.store.start.ExportDir = ""
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := newExportCase(t)
			test.setup(run)
			other := &exportStore{start: service.ExportStart{Title: run.store.start.Title, VaultPath: run.vault}, linked: true, answers: map[string]service.Exported{}}
			second := run.export
			second.Exports = other
			second.Limits.Read = 300 * time.Millisecond
			exportSecond := func() (service.Exported, error) {
				return second.Execute(context.Background(), service.ExportInput{ActorUserID: "author", ProjectID: "saga-2", Key: "key-2"})
			}
			planned, release := make(chan struct{}), make(chan struct{})
			run.export.Failpoint = func(point string) error {
				if point == "planned" {
					close(planned)
					<-release
				}
				return nil
			}
			done := make(chan error, 1)
			go func() {
				_, err := run.run("key-1", false)
				done <- err
			}()
			select {
			case <-planned:
			case err := <-done:
				t.Fatalf("the first export ended before its writes: %v", err)
			}
			// The first saga stands before its first write.
			exported, err := exportSecond()
			switch {
			case test.waits && !errors.Is(err, service.ErrSlow):
				close(release)
				t.Fatalf("the second saga while the first makes its folder = %+v, %v", exported, err)
			case !test.waits && (err != nil || exported.Folder != "CanonKeeper/Северный предел (2)"):
				close(release)
				t.Fatalf("the second saga while the first writes into its own folder = %+v, %v", exported, err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if test.waits {
				if exported, err = exportSecond(); err != nil || exported.Folder != "CanonKeeper/Северный предел (2)" {
					t.Fatalf("the second saga after the first = %+v, %v", exported, err)
				}
			}
			if manifest := run.manifest(); manifest.Saga != "saga-1" || !manifest.Complete || run.store.start.ExportDir != "CanonKeeper/Северный предел" {
				t.Fatalf("the folder of the first saga = %+v, kept %q", manifest, run.store.start.ExportDir)
			}
		})
	}
}

// OE1.5: a reading of an export that answers right at the end of its time, when its caller already heard obsidian.slow, gives
// back what it holds — the choice of a folder and the vault —: the next first export of the process does not wait for it.
func TestAReadingThatAnswersPastItsTimeGivesTheChoiceBack(t *testing.T) {
	run := newExportCase(t)
	run.export.Limits.Read = 300 * time.Millisecond
	looked, release := make(chan struct{}), make(chan struct{})
	run.export.Failpoint = func(point string) error {
		if point == "looked" {
			close(looked)
			<-release
		}
		return nil
	}
	if exported, err := run.run("key-1", false); !errors.Is(err, service.ErrSlow) {
		t.Fatalf("a reading past its time = %+v, %v", exported, err)
	}
	<-looked
	close(release)
	other := &exportStore{start: service.ExportStart{Title: "Другая сага", VaultPath: run.vault}, linked: true, answers: map[string]service.Exported{}}
	second := run.export
	second.Exports, second.Failpoint, second.Limits.Read = other, nil, 5*time.Second
	if exported, err := second.Execute(context.Background(), service.ExportInput{ActorUserID: "author", ProjectID: "saga-2", Key: "key-2"}); err != nil || !exported.Complete {
		t.Fatalf("the next first export = %+v, %v", exported, err)
	}
}

// errStop stands for a process that stops in the middle of a run.
var errStop = errors.New("the process stops")

// cutWriters open the real writer of a vault whose writes, moves or n-th manifest stop like a process that stops; every
// manifest a run writes is read back as the next run reads it, and what does not read is said.
type cutWriters struct {
	failWrite func(path string) bool
	failMove  func(path string) bool
	keepN     int
	calls     *int
	unread    *[]error
}

func (writers cutWriters) OpenWriter(vaultPath string) (service.VaultWriter, error) {
	writer, err := obsidianvault.Writers{}.OpenWriter(vaultPath)
	if err != nil {
		return nil, err
	}
	return cutWriter{VaultWriter: writer, writers: writers}, nil
}

type cutWriter struct {
	service.VaultWriter
	writers cutWriters
}

func (writer cutWriter) Write(ctx context.Context, path string, text []byte, expect string) error {
	if writer.writers.failWrite != nil && writer.writers.failWrite(path) {
		return errStop
	}
	return writer.VaultWriter.Write(ctx, path, text, expect)
}

func (writer cutWriter) Move(ctx context.Context, path string, names []string, expect string) (string, error) {
	if writer.writers.failMove != nil && writer.writers.failMove(path) {
		return "", errStop
	}
	return writer.VaultWriter.Move(ctx, path, names, expect)
}

func (writer cutWriter) Keep(ctx context.Context, manifest []byte) error {
	if writer.writers.unread != nil {
		if _, err := obsidian.ReadManifest(manifest); err != nil {
			*writer.writers.unread = append(*writer.writers.unread, err)
		}
	}
	if writer.writers.calls != nil {
		if *writer.writers.calls++; *writer.writers.calls == writer.writers.keepN {
			return errStop
		}
	}
	return writer.VaultWriter.Keep(ctx, manifest)
}

// OE2.8, OE1.4: a run in which a reference that went frees its path and a new reference takes it — «Сава#» is another node
// whose file is named «Сава» — writes only manifests the next run reads, and a cut anywhere in it leaves the next run a saga
// it exports: the move stopped, the move done and its manifest not written, the file of the one that went gone and the write
// stopped.
func TestAFreedPathTakenInOneRunCutAnywhereLeavesAManifestThatReads(t *testing.T) {
	// Not cut: every manifest of the run reads, the freed name goes to the new reference without « (2)».
	run := newExportCase(t)
	run.must("key-1", false)
	run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава#"}}
	var unread []error
	run.export.Writers = cutWriters{unread: &unread}
	exported := run.must("key-2", false)
	if len(unread) != 0 || !exported.Complete || !slices.Contains(exported.Created.Names, "Герои/Сава.md") || exported.Moved.Count != 1 {
		t.Fatalf("the run = %+v, manifests that do not read %v, files %q", exported, unread, run.files())
	}
	// The author changes the reference that went between the plan and its move: it stays his and in its place, the new one
	// takes « (2)», and no manifest names the path twice.
	edited := newExportCase(t)
	edited.must("key-1", false)
	edited.export.Saga = sagaReads{nodes: []string{"Мара", "Сава#"}}
	sava := filepath.Join(edited.folder(), "Герои", "Сава.md")
	edited.export.Failpoint = func(point string) error {
		if point != "planned" {
			return nil
		}
		data, err := os.ReadFile(sava)
		if err != nil {
			return err
		}
		return os.WriteFile(sava, append(data, "моя мысль\n"...), 0o644)
	}
	unread = nil
	edited.export.Writers = cutWriters{unread: &unread}
	if exported := edited.must("key-2", false); len(unread) != 0 || !slices.Contains(exported.Kept.Names, "Герои/Сава.md") ||
		!slices.Contains(exported.Created.Names, "Герои/Сава (2).md") {
		t.Fatalf("the run over a change of the author = %+v, manifests that do not read %v", exported, unread)
	}
	for _, cut := range []struct {
		name    string
		writers func(unread *[]error) service.Writers
	}{
		{"the move stops", func(unread *[]error) service.Writers {
			return cutWriters{failMove: func(string) bool { return true }, unread: unread}
		}},
		{"the manifest after the move is not written", func(unread *[]error) service.Writers {
			calls := 0
			return cutWriters{keepN: 2, calls: &calls, unread: unread}
		}},
		{"the file of the one that went is gone, the write stops", func(unread *[]error) service.Writers {
			return cutWriters{failWrite: func(string) bool { return true }, unread: unread}
		}},
	} {
		t.Run(cut.name, func(t *testing.T) {
			run := newExportCase(t)
			run.must("key-1", false)
			if strings.Contains(cut.name, "gone") {
				if err := os.Remove(filepath.Join(run.folder(), "Герои", "Сава.md")); err != nil {
					t.Fatal(err)
				}
			}
			run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава#"}}
			var unread []error
			run.export.Writers = cut.writers(&unread)
			if first, _ := run.run("key-2", false); first.Complete {
				t.Fatalf("the cut run ended: %+v", first)
			}
			if len(unread) != 0 {
				t.Fatalf("the cut run wrote manifests that do not read: %v", unread)
			}
			run.manifest()
			run.export.Writers = obsidianvault.Writers{}
			exported, err := run.run("key-3", false)
			if err != nil || !exported.Complete || slices.ContainsFunc(run.files(), func(name string) bool { return strings.Contains(name, " (2)") }) {
				t.Fatalf("the run after the cut = %+v, %v; files %q", exported, err, run.files())
			}
			if data, _ := os.ReadFile(filepath.Join(run.folder(), "Герои", "Сава.md")); !strings.Contains(string(data), `canonkeeper-key: "сава#"`) {
				t.Fatalf("the freed path holds:\n%s", data)
			}
		})
	}
}

// OE2.8 and the decision «свежее содержание равно записанному решает SHA-256»: a reference whose only change is the target of
// a link — the author moved the reference it links —, its run cut before the renames, is written by the next run: no link of a
// complete run stays pointing at the path the reference left.
func TestALinkOnlyUpdateCutBeforeItsRenameIsWrittenLater(t *testing.T) {
	for _, cut := range []bool{false, true} {
		t.Run(map[bool]string{false: "not cut", true: "cut before the renames"}[cut], func(t *testing.T) {
			run := newExportCase(t)
			run.must("key-1", false)
			moved := filepath.Join(run.folder(), "Мои", "Мара — справка.md")
			if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(run.folder(), "Герои", "Мара.md"), moved); err != nil {
				t.Fatal(err)
			}
			if cut {
				run.export.Writers = cutWriters{failWrite: func(string) bool { return true }}
				if first, _ := run.run("key-2", false); first.Complete {
					t.Fatalf("the cut run ended: %+v", first)
				}
				run.export.Writers = obsidianvault.Writers{}
			}
			run.must("key-3", false)
			for _, file := range run.files() {
				data, err := os.ReadFile(filepath.Join(run.folder(), filepath.FromSlash(file)))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "Северный предел/Герои/Мара|") {
					t.Fatalf("a complete run leaves a link to the path Мара left in %s", file)
				}
			}
		})
	}
}

// OE2.8: a new reference named before its rename and cut; a file comes to its path before the next run — a note of the
// author, a reference of another saga with the same key, a copy of this reference with a thought of the author. A file that
// is not this reference of this saga is not the run's: it stays as it is and the reference takes « (2)»; the copy of this
// reference is the run's, changed by the author: kept, and with his word moved into _Удалено/ — never lost.
func TestAFileAtThePathOfACutNewReferenceIsTheRunsOnlyByItsHeader(t *testing.T) {
	source := newExportCase(t)
	source.must("key-0", false)
	reference, err := os.ReadFile(filepath.Join(source.folder(), "Герои", "Сава.md"))
	if err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(string(reference), `canonkeeper-saga: "saga-1"`, `canonkeeper-saga: "saga-9"`, 1)
	if other == string(reference) {
		t.Fatalf("the header names no saga:\n%s", reference)
	}
	for _, test := range []struct {
		name, text string
		ours       bool
	}{
		{"a note of the author", "мои заметки о Саве\n", false},
		{"a reference of another saga", other + "чужая\n", false},
		{"a copy of this reference with his thought", string(reference) + "моя мысль\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := newExportCase(t)
			run.export.Writers = cutWriters{failWrite: func(string) bool { return true }}
			if first, err := run.run("key-cut", false); err != nil || first.Complete {
				t.Fatalf("the cut run = %+v, %v", first, err)
			}
			run.export.Writers = obsidianvault.Writers{}
			full := filepath.Join(run.folder(), "Герои", "Сава.md")
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(test.text), 0o644); err != nil {
				t.Fatal(err)
			}
			run.must("key-1", false)
			if got, _ := os.ReadFile(full); string(got) != test.text {
				t.Fatalf("the file at the path changed:\n%s", got)
			}
			if second := slices.Contains(run.files(), "Герои/Сава (2).md"); second == test.ours {
				t.Fatalf("« (2)» = %v; files %q", second, run.files())
			}
			if !test.ours {
				return
			}
			run.export.Saga = sagaReads{nodes: []string{"Мара", "Сава"}, kind: "капитан"}
			run.must("key-2", true)
			deleted, _ := filepath.Glob(filepath.Join(run.folder(), "_Удалено", "*"))
			if !slices.ContainsFunc(deleted, func(file string) bool { data, _ := os.ReadFile(file); return string(data) == test.text }) {
				t.Fatalf("the text of the author is not in _Удалено/: %q", deleted)
			}
		})
	}
}
