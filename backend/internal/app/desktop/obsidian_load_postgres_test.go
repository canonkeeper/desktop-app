// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/library"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
	quotastore "canonkeeper/backend/internal/store/quota"
)

func (core *bridgeCore) shelf() obsidianShelf {
	return obsidianShelf{library: core.library, store: core.store, jobs: core.jobs, keys: core.links}
}

func (core *bridgeCore) loadUseCase() service.ImportBooks {
	return service.ImportBooks{
		Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Volumes: obsidianVolumes{}, Shelf: core.shelf(),
		Replays: obsidianrepo.NewReplays(core.db), Gate: service.NewGate(), Limits: service.DefaultLimits,
		Logger: log.New(io.Discard, "", 0), Now: time.Now,
	}
}

// fingerprinted is a request of a load of the books of the choice with the fingerprints the preview gave.
func (core *bridgeCore) fingerprinted(key string, choice obsidian.Choice) (service.LoadInput, service.Preview) {
	core.t.Helper()
	preview := core.preview(choice)
	input := service.LoadInput{
		ActorUserID: core.author, ProjectID: core.saga, Choice: choice, Fingerprints: map[string]string{}, Key: key,
		Method: "POST", Path: "/api/v1/projects/" + core.saga + "/obsidian/books", Proof: core.proof,
	}
	for _, book := range preview.Books {
		input.Fingerprints[book.Folder] = book.Fingerprint
	}
	return input, preview
}

// count counts rows of the saga.
func (core *bridgeCore) count(query string) int {
	core.t.Helper()
	var rows int
	if err := core.db.Pool.QueryRow(context.Background(), query, core.saga).Scan(&rows); err != nil {
		core.t.Fatal(err)
	}
	return rows
}

func (core *bridgeCore) liveQueues() int {
	return core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1 AND status IN ('uploading', 'draft', 'running')`)
}

func (core *bridgeCore) linkRows() int {
	return core.count(`SELECT count(*) FROM obsidian_books WHERE project_id = $1`)
}

func (core *bridgeCore) heldFolders() map[string]service.HeldFolder {
	core.t.Helper()
	folders, err := core.links.Folders(context.Background(), core.saga)
	if err != nil {
		core.t.Fatal(err)
	}
	held := map[string]service.HeldFolder{}
	for _, folder := range folders {
		held[folder.Folder] = folder
	}
	return held
}

// pageQueue is a queue the shelf of the window makes, with a key of its own.
func (core *bridgeCore) pageQueue(key string) library.ImportQueue {
	core.t.Helper()
	queue, err := core.library.CreateImportQueue(context.Background(), library.CreateImportQueueInput{
		ProjectID: core.saga, ActorUserID: core.author, IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/api/v1/projects/" + core.saga + "/import-queues",
		SessionWriteProof: core.proof, Items: []library.CreateImportQueueItemInput{{ClientItemID: "page", Position: 1, SourceFileName: "Страница.md"}},
	})
	if err != nil {
		core.t.Fatal(err)
	}
	return queue
}

// specs/2026-11-14-desk-obsidian-books.md OK1.1, OK1.3, OK0.3 on PostgreSQL and the service of the library: two folders of
// books make one queue of two volumes in the order of the request; the file of a volume is the glued text — its SHA-256 the
// fingerprint of its link — under the name of the glue; the start of the queue spends one upload a volume, as the queue of the
// shelf does, and the replay of the key none; once its Job succeeds the folder is linked to a book of the title and the
// chapters of the preview.
func TestALoadPutsTwoBooksIntoOneQueuePostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_load_queue")
	core.write(map[string]string{
		"Книга 1/01 Застава.md":      "Ольга вышла к заставе на рассвете. Ветер с перевала нёс снег.",
		"Книга 1/02 Переправа.md":    "Мирон ждал у переправы. Паром не шёл.",
		"Книга 1/Персонажи/Ольга.md": "Старшая на заставе.",
		"Книга 2/01 Порт.md":         "Судно вошло в порт. Берег был пуст.",
	})
	choice := obsidian.Choice{Books: []string{"Книга 2", "Книга 1"}, Materials: []obsidian.MaterialFolder{{Path: "Книга 1/Персонажи"}}}
	input, preview := core.fingerprinted("load-two-books", choice)
	units := core.importsOfTheDay()
	loaded, err := core.loadUseCase().Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Queue == nil || loaded.Queue.Status != "running" || len(loaded.Folders) != 2 || loaded.Folders[0].Outcome != service.OutcomeQueued ||
		loaded.Folders[1].QueueID != loaded.Queue.ID {
		t.Fatalf("the load = %+v", loaded)
	}
	if got := core.importsOfTheDay(); got != units+2 {
		t.Fatalf("the uploads of a load of two books = %d, before %d", got, units)
	}
	if _, err := core.loadUseCase().Execute(context.Background(), input); err != nil || core.importsOfTheDay() != units+2 {
		t.Fatalf("the uploads after the replay of the key = %d, before %d: %v", core.importsOfTheDay(), units, err)
	}
	rows, err := core.db.Pool.Query(context.Background(), `
		SELECT item.client_item_id, item.source_file_name, item.sha256, link.fingerprint, upload.content
		FROM import_queue_items AS item
		JOIN obsidian_books AS link ON link.item_id = item.id
		JOIN import_uploads AS upload ON upload.storage_key = item.storage_key
		WHERE item.queue_id = $1 ORDER BY item.position
	`, loaded.Queue.ID)
	if err != nil {
		t.Fatal(err)
	}
	var volumes []string
	for rows.Next() {
		var folder, name, sum, fingerprint string
		var content []byte
		if err := rows.Scan(&folder, &name, &sum, &fingerprint, &content); err != nil {
			t.Fatal(err)
		}
		book := preview.Books[len(volumes)]
		digest := sha256.Sum256(content)
		if folder != book.Folder || name != book.Glued.FileName || string(content) != book.Glued.Text || sum != fingerprint ||
			hex.EncodeToString(digest[:]) != book.Fingerprint {
			t.Fatalf("the volume of %s = %s %s %s %s", book.Folder, folder, name, sum, fingerprint)
		}
		volumes = append(volumes, folder)
	}
	if rows.Err() != nil || !slices.Equal(volumes, []string{"Книга 2", "Книга 1"}) {
		t.Fatalf("the volumes = %v, %v", volumes, rows.Err())
	}
	held := core.heldFolders()
	if held["Книга 2"].Queue != loaded.Queue.ID || held["Книга 1"].Queue != loaded.Queue.ID {
		t.Fatalf("the folders in work = %+v", held)
	}
	core.runImport(false)
	held = core.heldFolders()
	book := held["Книга 2"].Book
	if book == nil || book.Title != preview.Books[0].Title || held["Книга 1"].Queue != loaded.Queue.ID {
		t.Fatalf("the folders after the first Job = %+v", held)
	}
	chapters, err := core.library.ListChapters(context.Background(), library.Actor{UserID: core.author}, book.BookID, library.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var titles, previewed []string
	for _, chapter := range chapters.Data {
		titles = append(titles, chapter.Title)
	}
	for _, chapter := range preview.Books[0].Chapters {
		previewed = append(previewed, chapter.Title)
	}
	if !slices.Equal(titles, previewed) {
		t.Fatalf("the chapters = %q, the preview said %q", titles, previewed)
	}
	// The second volume holds its choice: the folder of materials inside it is left out of an update as of the load.
	if folder := held["Книга 1"]; folder.Book != nil || folder.Queue == "" {
		t.Fatalf("the second volume = %+v", folder)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK1.1–OK1.3, OK1.6: a folder changed after its preview is folder_changed and
// nothing is written; a volume the queue refuses — 2 001 chapters, over the size of a file — is refused with its code and the
// others go into the queue; when every volume is refused there is no queue; the limit of the books of the saga refuses every
// volume without a draft. An answer without a queue is kept for its key as the answer of a success is: its replay answers the
// same after the folder changed and after the limit was lifted.
func TestALoadChecksWhatItReadsPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_load_checks")
	var many strings.Builder
	for chapter := 1; chapter <= 2001; chapter++ {
		fmt.Fprintf(&many, "# Глава %d\n\nТекст главы %d.\n\n", chapter, chapter)
	}
	notes := map[string]string{
		"Книга 1/01 Застава.md": "Ольга вышла к заставе на рассвете.",
		"Много/Том.md":          many.String(),
	}
	// A volume over the size of a file of the queue: six notes, each under the size of a note that is read.
	long := strings.Repeat("Ветер с перевала нёс снег. ", 9_000_000/len("Ветер с перевала нёс снег. "))
	for note := 1; note <= 6; note++ {
		notes[fmt.Sprintf("Большая/%02d Часть.md", note)] = long
	}
	core.write(notes)
	input, _ := core.fingerprinted("load-checks-changed", obsidian.Choice{Books: []string{"Книга 1"}})
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе в полдень."})
	var changed service.FolderChanged
	if _, err := core.loadUseCase().Execute(context.Background(), input); !errors.As(err, &changed) || !slices.Equal(changed.Folders, []string{"Книга 1"}) ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 0 || core.linkRows() != 0 {
		t.Fatalf("a folder changed after its preview = %v", err)
	}

	input, _ = core.fingerprinted("load-checks-many", obsidian.Choice{Books: []string{"Много", "Большая"}})
	loaded, err := core.loadUseCase().Execute(context.Background(), input)
	if err != nil || loaded.Queue != nil || loaded.Folders[0].Outcome != service.OutcomeRefused || loaded.Folders[0].Refusal != "import.too_many_chapters" ||
		loaded.Folders[1].Outcome != service.OutcomeRefused || loaded.Folders[1].Refusal != "import.file_too_large" ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 0 {
		t.Fatalf("every volume refused = %+v, %v", loaded, err)
	}
	core.write(map[string]string{"Много/Том.md": "# Глава 1\n\nТекст главы.\n"})
	if again, err := core.loadUseCase().Execute(context.Background(), input); err != nil || !reflect.DeepEqual(again, loaded) ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 0 {
		t.Fatalf("the replay of every volume refused after the folder changed = %+v, %v", again, err)
	}
	core.write(map[string]string{"Много/Том.md": many.String()})

	none := int64(0)
	if _, err := quotastore.New(core.db, quotastore.Fence{}).SetOverrides(context.Background(), core.author, []quota.OverrideChange{{Key: quota.KeyProjectBooks, Value: &none}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	input, _ = core.fingerprinted("load-checks-limit", obsidian.Choice{Books: []string{"Книга 1", "Много"}})
	loaded, err = core.loadUseCase().Execute(context.Background(), input)
	if err != nil || loaded.Queue != nil || loaded.Folders[0].Refusal != "quota.project_books" || loaded.Folders[1].Refusal != "import.too_many_chapters" ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 0 || core.linkRows() != 0 {
		t.Fatalf("the limit of the books of the saga = %+v, %v", loaded, err)
	}
	if _, err := quotastore.New(core.db, quotastore.Fence{}).SetOverrides(context.Background(), core.author, []quota.OverrideChange{{Key: quota.KeyProjectBooks}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if again, err := core.loadUseCase().Execute(context.Background(), input); err != nil || !reflect.DeepEqual(again, loaded) ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 0 {
		t.Fatalf("the replay of the limit of the books after it was lifted = %+v, %v", again, err)
	}
	input.Key = "load-checks-mixed"
	loaded, err = core.loadUseCase().Execute(context.Background(), input)
	if err != nil || loaded.Queue == nil || loaded.Folders[0].Outcome != service.OutcomeQueued || loaded.Folders[1].Refusal != "import.too_many_chapters" ||
		core.linkRows() != 1 {
		t.Fatalf("a volume refused beside one that goes = %+v, %v", loaded, err)
	}
	if items := core.count(`SELECT count(*) FROM import_queue_items AS item JOIN import_queues AS queue ON queue.id = item.queue_id WHERE queue.project_id = $1`); items != 1 {
		t.Fatalf("the volumes of the queue = %d", items)
	}
}

// racingFiles are the files of a vault whose first walk lets the shelf of the window make its own queue: the queue appears
// between the reading of the bridge and its first write.
type racingFiles struct {
	obsidianvault.Files
	once  *sync.Once
	queue func()
}

func (files racingFiles) Open(vaultPath string) (service.Vault, error) {
	vault, err := files.Files.Open(vaultPath)
	if err != nil {
		return nil, err
	}
	files.once.Do(files.queue)
	return vault, nil
}

// specs/2026-11-14-desk-obsidian-books.md OK1.2, OK1.3, OK1.6: a live queue of the saga — there before, or made by the window
// between the reading of the bridge and its first write — refuses a load that has a volume to queue: queue_busy with that
// queue and nothing written; a load whose folders a live book or a book in the trash hold passes beside a live queue, and
// the replay of its key answers the same.
func TestALoadWithALiveQueueOfTheSagaPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_load_busy")
	ctx := context.Background()
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе.", "Книга 2/01 Порт.md": "Судно вошло в порт."})
	page := core.pageQueue("page-queue-before")
	input, _ := core.fingerprinted("load-busy-before", obsidian.Choice{Books: []string{"Книга 1"}})
	var busy service.QueueBusy
	if _, err := core.loadUseCase().Execute(ctx, input); !errors.As(err, &busy) || busy.Queue.ID != page.ID || core.linkRows() != 0 ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 1 {
		t.Fatalf("a live queue there before = %v", err)
	}
	if _, err := core.library.CancelImportQueue(ctx, library.CancelImportQueueInput{ProjectID: core.saga, QueueID: page.ID, ActorUserID: core.author, SessionWriteProof: core.proof}); err != nil {
		t.Fatal(err)
	}

	var raced library.ImportQueue
	racing := core.loadUseCase()
	racing.Files = racingFiles{once: &sync.Once{}, queue: func() { raced = core.pageQueue("page-queue-between") }}
	input.Key = "load-busy-between"
	if _, err := racing.Execute(ctx, input); !errors.As(err, &busy) || busy.Queue.ID != raced.ID || core.linkRows() != 0 || core.liveQueues() != 1 {
		t.Fatalf("a live queue made between the reading and the write = %v", err)
	}

	linked, trashed := core.libraryBook("Застава"), core.libraryBook("Порт")
	if err := core.links.LinkBook(ctx, core.saga, linked, "Книга 1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := core.links.LinkBook(ctx, core.saga, trashed, "Книга 2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := core.library.DeleteBook(ctx, library.Actor{UserID: core.author}, trashed); err != nil {
		t.Fatal(err)
	}
	input, _ = core.fingerprinted("load-busy-held", obsidian.Choice{Books: []string{"Книга 1", "Книга 2"}})
	loaded, err := core.loadUseCase().Execute(ctx, input)
	if err != nil || loaded.Queue != nil || loaded.Folders[0].Outcome != service.OutcomeLinked || loaded.Folders[0].BookID != linked ||
		loaded.Folders[1].Outcome != service.OutcomeInTrash || loaded.Folders[1].BookID != trashed {
		t.Fatalf("held folders beside a live queue = %+v, %v", loaded, err)
	}
	// The answer without a queue is kept for its key (OK1.6): its replay is the same when the folder holds no book any more.
	if err := core.links.UnlinkBook(ctx, core.saga, linked); err != nil {
		t.Fatal(err)
	}
	if again, err := core.loadUseCase().Execute(ctx, input); err != nil || !reflect.DeepEqual(again, loaded) {
		t.Fatalf("the replay of held folders after the link was taken off = %+v, %v", again, err)
	}
}

func (core *bridgeCore) libraryBook(title string) string {
	core.t.Helper()
	book, err := core.library.CreateBook(context.Background(), library.Actor{UserID: core.author}, core.saga, library.BookInput{Title: title})
	if err != nil {
		core.t.Fatal(err)
	}
	return book.ID
}

// specs/2026-11-14-desk-obsidian-books.md OK0.1, OK1.3, OK1.6: a failure at a point after the queue was made cancels the
// draft — no live queue —, keeps the answer of the failure and its replay gives it; a caller who goes away and the time of the
// reading that passes in the middle of the uploads do not stop the writes: the queue starts, no draft is left, and the
// replay of the key gives the answer kept.
func TestALoadThatFailsLeavesNoDraftPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_load_fails")
	ctx := context.Background()
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе.", "Книга 2/01 Порт.md": "Судно вошло в порт."})
	choice := obsidian.Choice{Books: []string{"Книга 1", "Книга 2"}}
	for _, point := range []string{"after_queue", "after_upload"} {
		load := core.loadUseCase()
		load.Failpoint = func(at string) error {
			if at == point {
				return errors.New("the point of the test")
			}
			return nil
		}
		input, _ := core.fingerprinted("load-fails-"+point, choice)
		var failed service.LoadFailed
		if _, err := load.Execute(ctx, input); !errors.As(err, &failed) || failed.Code != "server.error" || core.liveQueues() != 0 {
			t.Fatalf("a failure %s = %v, live queues %d", point, err, core.liveQueues())
		}
		if _, err := core.loadUseCase().Execute(ctx, input); !errors.As(err, &failed) || failed.Code != "server.error" {
			t.Fatalf("the replay of a failure %s = %v", point, err)
		}
		if held := core.heldFolders(); len(held) != 0 {
			t.Fatalf("the folders after a failure %s = %+v", point, held)
		}
	}

	requestCtx, cancel := context.WithCancel(ctx)
	load := core.loadUseCase()
	// The reading of two notes ends well within its time; the time passes while the uploads go on.
	load.Limits.Read = time.Second
	load.Failpoint = func(at string) error {
		if at == "after_upload" {
			cancel()
			time.Sleep(1500 * time.Millisecond)
		}
		return nil
	}
	input, _ := core.fingerprinted("load-fails-gone", choice)
	loaded, err := load.Execute(requestCtx, input)
	if err != nil || loaded.Queue == nil || loaded.Queue.Status != "running" || core.liveQueues() != 1 ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1 AND status IN ('uploading', 'draft')`) != 0 {
		t.Fatalf("the writes after the caller went away = %+v, %v", loaded, err)
	}
	again, err := core.loadUseCase().Execute(ctx, input)
	if err != nil || again.Queue == nil || again.Queue.ID != loaded.Queue.ID {
		t.Fatalf("the replay of the load = %+v, %v", again, err)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK1.4–OK1.6, OK0.3: the replay of a success answers the same when the folder
// changed and makes no second queue; a volume whose Job failed holds the saga — the next load is queue_busy with its queue —
// and its link holds nothing; the author gives it another file at its place: the book of that file is not linked; a volume
// the author skips holds nothing and the folder loads again; a draft the bridge left is canceled before a load, and a queue
// the author started from the shelf is not.
func TestTheLinkOfAVolumeActsOnlyForItsFilePostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_load_volume")
	ctx := context.Background()
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе."})
	choice := obsidian.Choice{Books: []string{"Книга 1"}}
	input, _ := core.fingerprinted("load-volume-first", choice)
	first, err := core.loadUseCase().Execute(ctx, input)
	if err != nil || first.Queue == nil {
		t.Fatalf("the load = %+v, %v", first, err)
	}
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе в полдень."})
	if again, err := core.loadUseCase().Execute(ctx, input); err != nil || again.Queue.ID != first.Queue.ID ||
		core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1`) != 1 {
		t.Fatalf("the replay of a success = %+v, %v", again, err)
	}

	core.runImport(true)
	if held := core.heldFolders(); len(held) != 0 {
		t.Fatalf("a volume whose Job failed = %+v", held)
	}
	input, _ = core.fingerprinted("load-volume-busy", choice)
	var busy service.QueueBusy
	if _, err := core.loadUseCase().Execute(ctx, input); !errors.As(err, &busy) || busy.Queue.ID != first.Queue.ID {
		t.Fatalf("a load while a volume waits for the author = %v", err)
	}
	queue, err := core.library.GetImportQueue(ctx, library.GetImportQueueInput{ProjectID: core.saga, QueueID: first.Queue.ID, ActorUserID: core.author})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.library.UploadImportQueueItem(ctx, library.UploadImportQueueItemInput{
		ProjectID: core.saga, QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: core.author, FileName: "Чужая.md",
		Content: []byte("# Чужая\n\n# Глава 1\n\nЭто текст другого файла.\n"), IdempotencyKey: "page-replace", RequestMethod: "POST",
		RequestPath: "/api/v1/projects/" + core.saga + "/import-queues/" + queue.ID + "/items", SessionWriteProof: core.proof,
	}); err != nil {
		t.Fatal(err)
	}
	core.runImport(false)
	if held := core.heldFolders(); len(held) != 0 || core.books()["Чужая"] == "" {
		t.Fatalf("a volume given another file = %+v, books %v", held, core.books())
	}
	if state := core.preview(choice).Books[0].State; state.Linked || state.Queued {
		t.Fatalf("the folder of a volume given another file = %+v", state)
	}

	skipped := newBridgeCore(t, "ck_obsidian_load_skipped")
	skipped.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе."})
	input, _ = skipped.fingerprinted("load-skipped-first", choice)
	loaded, err := skipped.loadUseCase().Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	skipped.runImport(true)
	queue, err = skipped.library.GetImportQueue(ctx, library.GetImportQueueInput{ProjectID: skipped.saga, QueueID: loaded.Queue.ID, ActorUserID: skipped.author})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := skipped.library.SkipImportQueueItem(ctx, library.SkipImportQueueItemInput{
		ProjectID: skipped.saga, QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: skipped.author, SessionWriteProof: skipped.proof,
	}); err != nil {
		t.Fatal(err)
	}
	input.Key = "load-skipped-again"
	again, err := skipped.loadUseCase().Execute(ctx, input)
	if err != nil || again.Queue == nil || again.Queue.ID == loaded.Queue.ID || again.Folders[0].Outcome != service.OutcomeQueued {
		t.Fatalf("a folder whose volume was skipped = %+v, %v", again, err)
	}

	// A draft of the bridge is canceled before the next load; a draft the author started from the shelf runs on.
	drafts := newBridgeCore(t, "ck_obsidian_load_drafts")
	drafts.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе.", "Книга 2/01 Порт.md": "Судно вошло в порт."})
	stop := drafts.loadUseCase()
	stop.Failpoint = func(at string) error {
		if at == "after_links" {
			panic("the process falls")
		}
		return nil
	}
	input, _ = drafts.fingerprinted("load-drafts-fallen", obsidian.Choice{Books: []string{"Книга 1"}})
	func() {
		defer func() { _ = recover() }()
		_, _ = stop.Execute(ctx, input)
	}()
	if drafts.count(`SELECT count(*) FROM import_queues WHERE project_id = $1 AND status = 'uploading'`) != 1 {
		t.Fatal("the fallen load left no draft; the test proves nothing")
	}
	if _, err := drafts.loadUseCase().Execute(ctx, input); !errors.Is(err, service.ErrLoadInterrupted) {
		t.Fatalf("the key of a fallen load = %v", err)
	}
	input, _ = drafts.fingerprinted("load-drafts-next", obsidian.Choice{Books: []string{"Книга 2"}})
	next, err := drafts.loadUseCase().Execute(ctx, input)
	if err != nil || next.Queue == nil || drafts.liveQueues() != 1 {
		t.Fatalf("the load after a fallen one = %+v, %v, live %d", next, err, drafts.liveQueues())
	}
	if err := service.CancelDrafts(ctx, drafts.links, drafts.shelf(), log.New(io.Discard, "", 0), ""); err != nil || drafts.liveQueues() != 1 {
		t.Fatalf("the start cancels a running queue: %v, live %d", err, drafts.liveQueues())
	}
}

// lostQueue is a shelf whose queue the core makes and whose answer is lost on the way back.
type lostQueue struct{ service.Shelf }

func (shelf lostQueue) CreateQueue(ctx context.Context, write service.Write, volumes []service.QueueVolume) (string, []string, error) {
	if _, _, err := shelf.Shelf.CreateQueue(ctx, write, volumes); err != nil {
		return "", nil, err
	}
	return "", nil, errors.New("the answer of the queue is lost")
}

// uncancelable is a shelf whose cancel of a draft fails.
type uncancelable struct{ service.Shelf }

func (uncancelable) CancelDraft(context.Context, service.Draft) error {
	return errors.New("the cancel fails")
}

// unlinked are links of the books whose links of the volumes fail.
type unlinked struct{ service.BookLinks }

func (unlinked) AddVolumes(context.Context, string, []service.VolumeLink, time.Time) error {
	return errors.New("the links fail")
}

// specs/2026-11-14-desk-obsidian-books.md OK1.4: a draft the bridge made and left without the links of its volumes — the
// process fell right after the queue, the answer of the queue was lost, the links and the cancel of the draft failed — is
// known by the key the bridge derives for its queue: the start of the backend and the next load cancel it, and the saga is
// not busy with it; the replay of the key of the load that left it is its kept failure or, without one, load_interrupted.
func TestADraftOfTheBridgeWithoutLinksIsCanceledPostgres(t *testing.T) {
	ctx := context.Background()
	choice := obsidian.Choice{Books: []string{"Книга 1"}}
	for _, test := range []struct {
		name  string
		load  func(core *bridgeCore) service.ImportBooks
		start bool
		// kept says the load kept the answer of its failure for its key.
		kept bool
	}{
		{"fallen", func(core *bridgeCore) service.ImportBooks {
			load := core.loadUseCase()
			load.Failpoint = func(at string) error {
				if at == "after_queue" {
					panic("the process falls")
				}
				return nil
			}
			return load
		}, true, false},
		{"lost", func(core *bridgeCore) service.ImportBooks {
			load := core.loadUseCase()
			load.Shelf = lostQueue{core.shelf()}
			return load
		}, false, false},
		{"unlinked", func(core *bridgeCore) service.ImportBooks {
			load := core.loadUseCase()
			load.Links, load.Shelf = unlinked{core.links}, uncancelable{core.shelf()}
			return load
		}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			core := newBridgeCore(t, "ck_obsidian_bare_"+test.name)
			core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе.", "Книга 2/01 Порт.md": "Судно вошло в порт."})
			input, _ := core.fingerprinted("load-bare-"+test.name, choice)
			func() {
				defer func() { _ = recover() }()
				_, _ = test.load(core).Execute(ctx, input)
			}()
			if core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1 AND status = 'uploading'`) != 1 || core.linkRows() != 0 {
				t.Fatalf("%s: the load left no draft without links; the test proves nothing", test.name)
			}
			if test.start {
				if err := service.CancelDrafts(ctx, core.links, core.shelf(), log.New(io.Discard, "", 0), ""); err != nil || core.liveQueues() != 0 {
					t.Fatalf("%s: the start leaves the draft: %v, live %d", test.name, err, core.liveQueues())
				}
			}
			var failed service.LoadFailed
			if _, err := core.loadUseCase().Execute(ctx, input); test.kept && (!errors.As(err, &failed) || failed.Code != "server.error") ||
				!test.kept && !errors.Is(err, service.ErrLoadInterrupted) {
				t.Fatalf("%s: the key of the load that left the draft = %v", test.name, err)
			}
			next, _ := core.fingerprinted("load-bare-next-"+test.name, obsidian.Choice{Books: []string{"Книга 2"}})
			loaded, err := core.loadUseCase().Execute(ctx, next)
			if err != nil || loaded.Queue == nil || core.liveQueues() != 1 || core.count(`SELECT count(*) FROM import_queues WHERE project_id = $1 AND status = 'canceled'`) != 1 {
				t.Fatalf("%s: the load after the draft = %+v, %v, live %d", test.name, loaded, err, core.liveQueues())
			}
		})
	}
}
