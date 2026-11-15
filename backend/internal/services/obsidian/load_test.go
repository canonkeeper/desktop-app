// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/obsidian"
)

// bookLinks are the links of a test: the folders it names and what the load wrote.
type bookLinks struct {
	mu      sync.Mutex
	folders []service.HeldFolder
	added   []service.VolumeLink
	drafts  []service.Draft
}

func (links *bookLinks) Books(ctx context.Context, project string) ([]service.BookFolder, error) {
	return service.BooksOfFolders(ctx, links, project)
}
func (links *bookLinks) Notes(context.Context, string, []string, []string) (obsidian.NoteFacts, error) {
	return obsidian.NoteFacts{}, nil
}
func (links *bookLinks) Folders(context.Context, string) ([]service.HeldFolder, error) {
	links.mu.Lock()
	defer links.mu.Unlock()
	return slices.Clone(links.folders), nil
}
func (links *bookLinks) AddVolumes(_ context.Context, _ string, volumes []service.VolumeLink, _ time.Time) error {
	links.mu.Lock()
	defer links.mu.Unlock()
	links.added = append(links.added, volumes...)
	return nil
}
func (links *bookLinks) LinkBook(context.Context, string, string, string, time.Time) error {
	return nil
}
func (links *bookLinks) UnlinkBook(context.Context, string, string) error { return nil }
func (links *bookLinks) Drafts(context.Context, string) ([]service.Draft, error) {
	links.mu.Lock()
	defer links.mu.Unlock()
	return slices.Clone(links.drafts), nil
}

// queueShelf is the queue of the volumes of a test: what the load asked of it, in order.
type queueShelf struct {
	mu       sync.Mutex
	calls    []string
	volumes  []service.QueueVolume
	live     *service.QueueRef
	keyMade  bool
	create   error
	failAt   string
	canceled []service.Draft
	// hold, when set, stops the uploads until it is closed; uploading says an upload began.
	hold      chan struct{}
	uploading chan struct{}
	// imports are the import Jobs of the updates asked, the Jobs by their keys; importErr refuses them as the core would.
	imports   []importCall
	byKey     map[string]jobs.Job
	importErr error
}

// importCall is an import Job of «Обновить из файла» the bridge asked of the core.
type importCall struct {
	write  service.Write
	update service.UpdateJob
}

func (shelf *queueShelf) called(call string) {
	shelf.mu.Lock()
	defer shelf.mu.Unlock()
	shelf.calls = append(shelf.calls, call)
}

func (shelf *queueShelf) seen() []string {
	shelf.mu.Lock()
	defer shelf.mu.Unlock()
	return slices.Clone(shelf.calls)
}

func (shelf *queueShelf) LiveQueue(context.Context, string, string) (service.QueueRef, bool, error) {
	if shelf.live == nil {
		return service.QueueRef{}, false, nil
	}
	return *shelf.live, true, nil
}

func (shelf *queueShelf) CreateQueue(_ context.Context, write service.Write, volumes []service.QueueVolume) (string, []string, error) {
	shelf.called("create")
	if shelf.live != nil {
		return "", nil, service.ErrQueueExists
	}
	if shelf.create != nil {
		return "", nil, shelf.create
	}
	if write.Key != service.DerivedKey("load-key-0001", "queue") {
		return "", nil, errors.New("not the key of the queue")
	}
	shelf.volumes = volumes
	var ids []string
	for index := range volumes {
		ids = append(ids, "item-"+string(rune('a'+index)))
	}
	return "q-new", ids, nil
}

func (shelf *queueShelf) Upload(_ context.Context, _ service.Write, _, itemID string, _ service.QueueVolume) (int64, error) {
	shelf.called("upload " + itemID)
	if shelf.uploading != nil {
		select {
		case shelf.uploading <- struct{}{}:
		default:
		}
	}
	if shelf.hold != nil {
		<-shelf.hold
	}
	if shelf.failAt == "upload" {
		return 0, service.ErrConflict
	}
	return 3, nil
}

func (shelf *queueShelf) Start(_ context.Context, _ service.Write, queueID string, version int64) (service.QueueRef, error) {
	shelf.called("start")
	if shelf.failAt == "start" {
		return service.QueueRef{}, auth.ErrUnauthenticated
	}
	return service.QueueRef{ID: queueID, Status: "running"}, nil
}

func (shelf *queueShelf) CancelDraft(_ context.Context, draft service.Draft) error {
	shelf.called("cancel " + draft.QueueID)
	return nil
}

func (shelf *queueShelf) QueueOfKey(context.Context, string, string, string) (bool, error) {
	return shelf.keyMade, nil
}

func (shelf *queueShelf) ImportJob(_ context.Context, write service.Write, update service.UpdateJob) (jobs.Job, error) {
	shelf.mu.Lock()
	defer shelf.mu.Unlock()
	shelf.calls = append(shelf.calls, "import")
	if shelf.importErr != nil {
		return jobs.Job{}, shelf.importErr
	}
	shelf.imports = append(shelf.imports, importCall{write: write, update: update})
	job := jobs.Job{ID: "job-" + string(rune('a'+len(shelf.imports)-1)), Kind: jobs.KindImport, Status: jobs.StatusQueued}
	if shelf.byKey == nil {
		shelf.byKey = map[string]jobs.Job{}
	}
	shelf.byKey[write.Key] = job
	return job, nil
}

func (shelf *queueShelf) JobOfKey(_ context.Context, write service.Write) (jobs.Job, bool, error) {
	shelf.mu.Lock()
	defer shelf.mu.Unlock()
	job, ok := shelf.byKey[write.Key]
	return job, ok, nil
}

// replays keep the answers of a test in memory.
type replays struct {
	mu   sync.Mutex
	kept map[string]service.ReplayKey
	ans  map[string]service.Loaded
}

func (store *replays) Replay(_ context.Context, key service.ReplayKey) (*service.Loaded, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	kept, ok := store.kept[key.Key]
	if !ok {
		return nil, nil
	}
	if kept != key {
		return nil, service.ErrReplayConflict
	}
	answer := store.ans[key.Key]
	return &answer, nil
}

func (store *replays) Keep(_ context.Context, key service.ReplayKey, loaded service.Loaded) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.kept[key.Key]; !ok {
		store.kept[key.Key], store.ans[key.Key] = key, loaded
	}
	return nil
}

type loadCase struct {
	vault   string
	links   *bookLinks
	shelf   *queueShelf
	replays *replays
	load    service.ImportBooks
}

func newLoad(t *testing.T) *loadCase {
	t.Helper()
	vault := filepath.Join(t.TempDir(), "Северный предел")
	for name, text := range map[string]string{
		"Книга 1/01 Застава.md":        "Ольга вышла к заставе на рассвете.",
		"Книга 1/Персонажи/Ольга.md":   "Старшая на заставе.",
		"Книга 1/Черновики/Обрывок.md": "Обрывок без места.",
		"Книга 2/01 Переправа.md":      "Мирон ждал у переправы.",
		"Книга 3/01 Порт.md":           "Судно вошло в порт.",
	} {
		write(t, vault, name, text)
	}
	test := &loadCase{vault: vault, links: &bookLinks{}, shelf: &queueShelf{}, replays: &replays{kept: map[string]service.ReplayKey{}, ans: map[string]service.Loaded{}}}
	test.load = service.ImportBooks{
		Vaults: &vaults{paths: map[string]string{"saga": vault}}, Files: obsidianvault.Files{}, Links: test.links, Volumes: volumes{},
		Shelf: test.shelf, Replays: test.replays, Gate: service.NewGate(), Limits: service.DefaultLimits,
		Logger: log.New(io.Discard, "", 0),
	}
	return test
}

// fingerprints are the fingerprints of the glue of the folders the preview gives.
func (test *loadCase) fingerprints(t *testing.T, books ...string) map[string]string {
	t.Helper()
	preview := previewOf(test.vault, service.NoLinks{}, obsidianvault.Files{})
	result, err := run(t, preview, test.input(nil, books...).Choice)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints := map[string]string{}
	for _, book := range result.Books {
		fingerprints[book.Folder] = book.Fingerprint
	}
	return fingerprints
}

func (test *loadCase) input(fingerprints map[string]string, books ...string) service.LoadInput {
	return service.LoadInput{
		ActorUserID: "author", ProjectID: "saga", Key: "load-key-0001", Method: "POST", Path: "/projects/saga/obsidian/books",
		Choice: obsidian.Choice{
			Books: books, Materials: []obsidian.MaterialFolder{{Path: "Книга 1/Персонажи"}}, Exclude: []string{"Книга 1/Черновики"},
		},
		Fingerprints: fingerprints,
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK1.1–OK1.3: the volumes of the folders go into one queue in the order of the
// request — the queue first, the links of its volumes with their fingerprints and the choice of their books, their files,
// its start —; a folder a book holds, one in the trash and one whose volume is in work make no volume.
func TestALoadPutsTheVolumesIntoOneQueue(t *testing.T) {
	test := newLoad(t)
	test.links.folders = []service.HeldFolder{{Folder: "Книга 3", Queue: "q-old"}}
	fingerprints := test.fingerprints(t, "Книга 2", "Книга 1", "Книга 3")
	loaded, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 2", "Книга 1", "Книга 3"))
	if err != nil {
		t.Fatal(err)
	}
	if got := test.shelf.seen(); !slices.Equal(got, []string{"create", "upload item-a", "upload item-b", "start"}) {
		t.Fatalf("the writes = %v", got)
	}
	if len(test.shelf.volumes) != 2 || test.shelf.volumes[0].Folder != "Книга 2" || test.shelf.volumes[1].Folder != "Книга 1" {
		t.Fatalf("the volumes = %+v", test.shelf.volumes)
	}
	if len(test.links.added) != 2 || test.links.added[1].ItemID != "item-b" || test.links.added[1].Fingerprint != fingerprints["Книга 1"] ||
		!slices.Equal(test.links.added[1].Choice.Excluded, []string{"Книга 1/Черновики"}) ||
		!slices.Equal(test.links.added[1].Choice.Nested, []string{"Книга 1/Персонажи"}) || len(test.links.added[0].Choice.Nested) != 0 {
		t.Fatalf("the links = %+v", test.links.added)
	}
	if loaded.Queue == nil || loaded.Queue.ID != "q-new" || loaded.Folders[0].Outcome != service.OutcomeQueued ||
		loaded.Folders[1].QueueID != "q-new" || loaded.Folders[2].Outcome != service.OutcomeQueued || loaded.Folders[2].QueueID != "q-old" {
		t.Fatalf("the outcomes = %+v", loaded)
	}

	// OK1.6: the same key after a success answers the same, the folder changed or not, and writes nothing.
	write(t, test.vault, "Книга 1/01 Застава.md", "Ольга вышла к заставе в полдень.")
	again, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 2", "Книга 1", "Книга 3"))
	if err != nil || again.Queue == nil || again.Queue.ID != "q-new" || len(test.shelf.seen()) != 4 {
		t.Fatalf("the replay = %+v, %v, writes %v", again, err, test.shelf.seen())
	}
	// Another request under the key is a conflict.
	if _, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 2")); !errors.Is(err, service.ErrReplayConflict) {
		t.Fatalf("another request of the key = %v", err)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK1.1–OK1.3: a folder changed since its preview is folder_changed with its
// folders and nothing written; a folder a book holds or one in the trash gives its outcome without a queue — a live queue of
// the saga refuses nothing then —; a live queue refuses a load that has a volume to queue with nothing written; a limit of the
// books of the saga refuses every volume without a draft; a volume the queue would refuse is refused with its code.
func TestALoadReadsFirstAndWritesTheQueueFirst(t *testing.T) {
	test := newLoad(t)
	fingerprints := test.fingerprints(t, "Книга 1", "Книга 2")
	changed := map[string]string{"Книга 1": fingerprints["Книга 1"], "Книга 2": "0000000000000000000000000000000000000000000000000000000000000000"}
	var folderChanged service.FolderChanged
	if _, err := test.load.Execute(context.Background(), test.input(changed, "Книга 1", "Книга 2")); !errors.As(err, &folderChanged) ||
		!slices.Equal(folderChanged.Folders, []string{"Книга 2"}) || len(test.shelf.seen()) != 0 || len(test.links.added) != 0 {
		t.Fatalf("a folder changed = %v, writes %v", err, test.shelf.seen())
	}

	test.shelf.live = &service.QueueRef{ID: "q-page", Status: "draft"}
	test.links.folders = []service.HeldFolder{
		{Folder: "Книга 1", Book: &service.LinkedBook{BookID: "b-1", Title: "Застава"}},
		{Folder: "Книга 2", Trash: &service.LinkedBook{BookID: "b-2", Title: "Переправа"}},
	}
	loaded, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 1", "Книга 2"))
	if err != nil || loaded.Queue != nil || loaded.Folders[0].Outcome != service.OutcomeLinked || loaded.Folders[0].BookID != "b-1" ||
		loaded.Folders[1].Outcome != service.OutcomeInTrash || len(test.shelf.seen()) != 0 {
		t.Fatalf("held folders with a live queue = %+v, %v, writes %v", loaded, err, test.shelf.seen())
	}

	test.links.folders = nil
	var busy service.QueueBusy
	input := test.input(fingerprints, "Книга 1", "Книга 2")
	input.Key = "load-key-0002"
	if _, err := test.load.Execute(context.Background(), input); !errors.As(err, &busy) || busy.Queue.ID != "q-page" ||
		!slices.Equal(test.shelf.seen(), []string{"create"}) || len(test.links.added) != 0 {
		t.Fatalf("a live queue = %v, writes %v", err, test.shelf.seen())
	}

	test.shelf.live, test.shelf.calls = nil, nil
	test.shelf.create = &quota.Exceeded{Key: quota.KeyProjectBooks, Limit: 1}
	input.Key = "load-key-0003"
	loaded, err = test.load.Execute(context.Background(), input)
	if err != nil || loaded.Queue != nil || loaded.Folders[0].Outcome != service.OutcomeRefused || loaded.Folders[1].Refusal != "quota.project_books" ||
		!slices.Equal(test.shelf.seen(), []string{"create"}) || len(test.links.added) != 0 {
		t.Fatalf("a limit of the books = %+v, %v, writes %v", loaded, err, test.shelf.seen())
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK0.1, OK1.3, OK1.4, OK1.6: a failure after the queue was made cancels its draft
// and keeps its code for the key; a key whose queue was made and whose answer was not kept is load_interrupted; the drafts
// of the bridge are canceled before the load looks at the saga.
func TestALoadLeavesNoQueueAfterAFailure(t *testing.T) {
	test := newLoad(t)
	fingerprints := test.fingerprints(t, "Книга 1")
	test.links.drafts = []service.Draft{{ProjectID: "saga", QueueID: "q-fallen", ActorUserID: "author"}}
	test.shelf.failAt = "start"
	var failed service.LoadFailed
	if _, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 1")); !errors.As(err, &failed) ||
		failed.Code != "auth.session_required" {
		t.Fatalf("a start that failed = %v", err)
	}
	if got := test.shelf.seen(); !slices.Equal(got, []string{"cancel q-fallen", "create", "upload item-a", "start", "cancel q-new"}) {
		t.Fatalf("the writes = %v", got)
	}
	test.links.drafts = nil
	if _, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 1")); !errors.As(err, &failed) ||
		failed.Code != "auth.session_required" || len(test.shelf.seen()) != 5 {
		t.Fatalf("the replay of a failure = %v, writes %v", err, test.shelf.seen())
	}

	interrupted := newLoad(t)
	interrupted.shelf.keyMade = true
	if _, err := interrupted.load.Execute(context.Background(), interrupted.input(fingerprints, "Книга 1")); !errors.Is(err, service.ErrLoadInterrupted) ||
		len(interrupted.shelf.seen()) != 0 {
		t.Fatalf("a key whose load fell = %v, writes %v", err, interrupted.shelf.seen())
	}

	failing := newLoad(t)
	failing.load.Failpoint = func(point string) error {
		if point == "after_links" {
			return errors.New("a point of the test")
		}
		return nil
	}
	if _, err := failing.load.Execute(context.Background(), failing.input(fingerprints, "Книга 1")); !errors.As(err, &failed) || failed.Code != "server.error" ||
		!slices.Equal(failing.shelf.seen(), []string{"create", "cancel q-new"}) {
		t.Fatalf("a failure after the links = %v, writes %v", err, failing.shelf.seen())
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK1.6, specs/2026-11-15-desk-obsidian-notes.md (Страницам): a folder of a book its
// preview read and that is gone from the vault since — renamed or removed — is obsidian.folder_changed with that folder, not a
// request of a path the vault has not; nothing is written.
func TestALoadOfAFolderGoneSincePreviewIsAChangeOfIt(t *testing.T) {
	test := newLoad(t)
	fingerprints := test.fingerprints(t, "Книга 1", "Книга 2")
	if err := os.Rename(filepath.Join(test.vault, "Книга 2"), filepath.Join(test.vault, "Книга вторая")); err != nil {
		t.Fatal(err)
	}
	var changed service.FolderChanged
	if _, err := test.load.Execute(context.Background(), test.input(fingerprints, "Книга 1", "Книга 2")); !errors.As(err, &changed) ||
		!slices.Equal(changed.Folders, []string{"Книга 2"}) || len(test.shelf.seen()) != 0 {
		t.Fatalf("a load of a folder gone since its preview = %v, writes %v", err, test.shelf.seen())
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK0.1: within the time of the operation only the vault is read — a vault that does
// not answer in it is obsidian.slow and nothing is written, then or after —; the writes go to their end when the caller goes
// away and hold the place of the saga until they end.
func TestOnlyTheReadingOfALoadIsWithinItsTime(t *testing.T) {
	stuck := newLoad(t)
	fingerprints := stuck.fingerprints(t, "Книга 1")
	release := make(chan struct{})
	stuck.load.Files = hangingNotes{Files: obsidianvault.Files{}, release: release}
	stuck.load.Limits.Read = 100 * time.Millisecond
	if _, err := stuck.load.Execute(context.Background(), stuck.input(fingerprints, "Книга 1")); !errors.Is(err, service.ErrSlow) {
		t.Fatalf("a vault that does not answer = %v", err)
	}
	close(release)
	// The reading returns and gives the place of the saga back: nothing was written after it.
	for {
		release, err := stuck.load.Gate.Enter("saga")
		if err == nil {
			release()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := stuck.shelf.seen(); len(got) != 0 || len(stuck.links.added) != 0 {
		t.Fatalf("a slow vault wrote %v %+v", got, stuck.links.added)
	}

	gone := newLoad(t)
	gone.shelf.hold, gone.shelf.uploading = make(chan struct{}), make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := gone.load.Execute(ctx, gone.input(fingerprints, "Книга 1"))
		done <- err
	}()
	select {
	case <-gone.shelf.uploading:
	case err := <-done:
		t.Fatalf("the load ended before its upload: %v", err)
	}
	cancel()
	// The second load answers at once when the first holds the place of the saga; one that went past it would wait for the
	// uploads the test holds — the test says so instead of waiting with it.
	second := make(chan error, 1)
	go func() {
		_, err := gone.load.Execute(context.Background(), gone.input(fingerprints, "Книга 1"))
		second <- err
	}()
	select {
	case err := <-second:
		if !errors.As(err, &service.Busy{}) {
			t.Fatalf("a second load of the saga while the first writes = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a second load of the saga went past the place the first holds while it writes")
	}
	close(gone.shelf.hold)
	if err := <-done; err != nil {
		t.Fatalf("the load whose caller went away = %v", err)
	}
	if got := gone.shelf.seen(); !slices.Equal(got, []string{"create", "upload item-a", "start"}) {
		t.Fatalf("the writes after the caller went away = %v", got)
	}
}
