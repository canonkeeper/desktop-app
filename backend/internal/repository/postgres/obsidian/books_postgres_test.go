// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/obsidian"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// shelf is the core of a test: an author who writes, his sagas, the queue of the volumes and the import Jobs it runs by
// hand, one at a time, as a worker does.
type shelf struct {
	t       *testing.T
	db      *storedb.DB
	author  string
	proof   auth.SessionWriteProof
	store   *librarystore.Store
	library *library.Service
	vaults  *Vaults
	books   *Books
	worker  string
	writers int
	// owners are the authors of the sagas, by saga.
	owners map[string]owner
}

type owner struct {
	author string
	proof  auth.SessionWriteProof
}

// as makes the author of the shelf the author of the saga.
func (shelf *shelf) as(projectID string) {
	shelf.author, shelf.proof = shelf.owners[projectID].author, shelf.owners[projectID].proof
}

func newShelf(t *testing.T, prefix string) *shelf {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, prefix)
	var schema string
	if err := db.Pool.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	store := librarystore.New(db)
	shelf := &shelf{
		t: t, db: db, store: store,
		library: library.NewService(store, library.NewSecureIDGenerator(), auth.SystemClock{}),
		vaults:  New(db), books: NewBooks(db), worker: pgx.Identifier{schema + "_worker"}.Sanitize(), owners: map[string]owner{},
	}
	shelf.writer()
	return shelf
}

// writer makes the author of the shelf a new author who writes: a saga of a test is his, under the limit of sagas of one.
func (shelf *shelf) writer() {
	shelf.t.Helper()
	ctx := context.Background()
	accounts := auth.NewService(authstore.New(shelf.db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	shelf.writers++
	user, err := accounts.CreateUser(ctx, auth.NewUser{
		Email: fmt.Sprintf("author-%d@example.test", shelf.writers), DisplayName: "Автор", EmailVerified: true,
	})
	if err != nil {
		shelf.t.Fatal(err)
	}
	session, csrf, err := accounts.CreateSession(ctx, auth.NewSession{UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		shelf.t.Fatal(err)
	}
	if _, shelf.proof, err = accounts.AuthenticateWrite(ctx, session.CookieValue, csrf); err != nil {
		shelf.t.Fatal(err)
	}
	shelf.author = user.ID
}

// saga is a new saga of a new author linked with a vault.
func (shelf *shelf) saga(title string) string {
	shelf.t.Helper()
	shelf.writer()
	saga, err := projects.NewService(projectstore.New(shelf.db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(context.Background(), shelf.author, projects.CreateProjectInput{Title: title})
	if err != nil {
		shelf.t.Fatal(err)
	}
	if err := shelf.vaults.Link(context.Background(), saga.ID, "/home/author/Хранилище"); err != nil {
		shelf.t.Fatal(err)
	}
	shelf.owners[saga.ID] = owner{author: shelf.author, proof: shelf.proof}
	return saga.ID
}

func fingerprintOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// queue is a queue of one volume of the folder the bridge links, its file not sent yet.
func (shelf *shelf) queue(projectID, folder, key string) (library.ImportQueue, string) {
	shelf.t.Helper()
	shelf.as(projectID)
	ctx := context.Background()
	queue, err := shelf.library.CreateImportQueue(ctx, library.CreateImportQueueInput{
		ProjectID: projectID, ActorUserID: shelf.author, IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/test/queue",
		SessionWriteProof: shelf.proof, Items: []library.CreateImportQueueItemInput{{ClientItemID: folder, Position: 1, SourceFileName: folder + ".md"}},
	})
	if err != nil {
		shelf.t.Fatal(err)
	}
	return queue, queue.Items[0].ID
}

// upload sends the file of a volume — to a draft, or «Заменить файл» of a volume of a running queue that waits.
func (shelf *shelf) upload(projectID, queueID, itemID, text, key string) int64 {
	shelf.t.Helper()
	shelf.as(projectID)
	item, err := shelf.library.UploadImportQueueItem(context.Background(), library.UploadImportQueueItemInput{
		ProjectID: projectID, QueueID: queueID, ItemID: itemID, ActorUserID: shelf.author, FileName: "Том.md", Content: []byte(text),
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/test/upload", SessionWriteProof: shelf.proof,
	})
	if err != nil {
		shelf.t.Fatal(err)
	}
	return item.QueueVersion
}

func (shelf *shelf) start(projectID, queueID string, version int64, key string) {
	shelf.t.Helper()
	shelf.as(projectID)
	if _, err := shelf.library.StartImportQueue(context.Background(), library.StartImportQueueInput{
		ProjectID: projectID, QueueID: queueID, ActorUserID: shelf.author, ExpectedVersion: version, IdempotencyKey: key,
		RequestMethod: "POST", RequestPath: "/test/start", SessionWriteProof: shelf.proof,
	}); err != nil {
		shelf.t.Fatal(err)
	}
}

// runImport claims the next import Job, as a worker does, and ends it: by its handler, or failed.
func (shelf *shelf) runImport(fail bool) {
	shelf.t.Helper()
	ctx := context.Background()
	var claim jobs.WorkerClaim
	if err := shelf.db.Pool.QueryRow(ctx, `
		SELECT project_id::text, work_id::text, claim_incarnation FROM `+shelf.worker+`.claim_next_queued_job_for_worker($1, NULL)
	`, []string{string(jobs.KindImport)}).Scan(&claim.ProjectID, &claim.JobID, &claim.ClaimIncarnation); err != nil {
		shelf.t.Fatalf("no import Job to claim: %v", err)
	}
	job, err := jobstore.New(shelf.db).LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		shelf.t.Fatal(err)
	}
	handler := library.NewImportHandler(shelf.store, shelf.store)
	if fail {
		if _, err := handler.HandleFailedResult(ctx, job); err != nil {
			shelf.t.Fatal(err)
		}
		return
	}
	if result, err := handler.HandleResult(ctx, job); err != nil || result.Status != jobs.StatusSucceeded {
		shelf.t.Fatalf("the import = %+v, %v", result, err)
	}
}

func (shelf *shelf) folders(projectID string) map[string]service.HeldFolder {
	shelf.t.Helper()
	folders, err := shelf.books.Folders(context.Background(), projectID)
	if err != nil {
		shelf.t.Fatal(err)
	}
	byFolder := map[string]service.HeldFolder{}
	for _, folder := range folders {
		byFolder[folder.Folder] = folder
	}
	return byFolder
}

func (shelf *shelf) book(projectID, title string) string {
	shelf.t.Helper()
	shelf.as(projectID)
	book, err := shelf.library.CreateBook(context.Background(), library.Actor{UserID: shelf.author}, projectID, library.BookInput{Title: title})
	if err != nil {
		shelf.t.Fatal(err)
	}
	return book.ID
}

func (shelf *shelf) linkVolume(projectID, folder, itemID, text string) {
	shelf.t.Helper()
	if err := shelf.books.AddVolumes(context.Background(), projectID, []service.VolumeLink{{
		Folder: folder, ItemID: itemID, Fingerprint: fingerprintOf(text),
		Choice: service.BookChoice{Excluded: []string{folder + "/Черновики"}, Nested: []string{folder + "/Персонажи"}},
	}}, time.Now()); err != nil {
		shelf.t.Fatal(err)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK0.2, OK0.3 on the queue of the volumes and the books of the core: a direct link
// and a volume whose Job succeeded hold their folders; a volume in work holds its folder as queued until the end of its
// queue; a canceled draft, a skipped volume, a volume whose Job failed and one the author gave another file at its place
// hold nothing; a book in «Недавно удалённые» holds its folder as in the trash; of two live books of one folder the later
// link holds it and the other is named; the unlink of the vault takes every link, a new path keeps them.
func TestWhatHoldsAFolderByTheCore(t *testing.T) {
	ctx := context.Background()
	shelf := newShelf(t, "ck_obsidian_books_rule")
	const first = "# Застава\n\n# Глава 1\n\nОльга вышла к заставе на рассвете.\n"
	const other = "# Чужая\n\n# Глава 1\n\nЭто текст другого файла.\n"

	// A direct link of a live book; the same book in the trash, then back.
	direct := shelf.saga("Северный предел")
	book := shelf.book(direct, "Прямая")
	if err := shelf.books.LinkBook(ctx, direct, book, "Прямая", time.Now()); err != nil {
		t.Fatal(err)
	}
	if held := shelf.folders(direct)["Прямая"]; held.Book == nil || held.Book.BookID != book || held.Book.Title != "Прямая" {
		t.Fatalf("a direct link = %+v", held)
	}
	shelf.as(direct)
	if err := shelf.library.DeleteBook(ctx, library.Actor{UserID: shelf.author}, book); err != nil {
		t.Fatal(err)
	}
	if held := shelf.folders(direct)["Прямая"]; held.Book != nil || held.Trash == nil || held.Trash.BookID != book {
		t.Fatalf("a book in the trash = %+v", held)
	}
	if _, err := shelf.library.RestoreBook(ctx, library.Actor{UserID: shelf.author}, book); err != nil {
		t.Fatal(err)
	}
	if held := shelf.folders(direct)["Прямая"]; held.Book == nil || held.Book.BookID != book {
		t.Fatalf("a book back from the trash = %+v", held)
	}

	// A volume: queued before its file and before its Job ends, its book after; LoadedAt is the end of its Job.
	loaded := shelf.saga("Северный предел, том")
	queue, item := shelf.queue(loaded, "Книга 1", "rule-loaded-queue")
	shelf.linkVolume(loaded, "Книга 1", item, first)
	if held := shelf.folders(loaded)["Книга 1"]; held.Queue != queue.ID || held.Book != nil {
		t.Fatalf("a volume of a draft = %+v", held)
	}
	shelf.start(loaded, queue.ID, shelf.upload(loaded, queue.ID, item, first, "rule-loaded-file"), "rule-loaded-start")
	if held := shelf.folders(loaded)["Книга 1"]; held.Queue != queue.ID {
		t.Fatalf("a volume in work = %+v", held)
	}
	shelf.runImport(false)
	held := shelf.folders(loaded)["Книга 1"]
	if held.Book == nil || held.Book.Title != "Застава" || held.Book.LoadedAt == nil || held.Queue != "" ||
		len(held.Book.Choice.Excluded) != 1 || len(held.Book.Choice.Nested) != 1 {
		t.Fatalf("a volume whose Job succeeded = %+v", held)
	}
	books, err := shelf.books.Books(ctx, loaded)
	if err != nil || len(books) != 1 || books[0].BookID != held.Book.BookID || books[0].Queued || books[0].InTrash {
		t.Fatalf("the books of the look = %+v, %v", books, err)
	}

	// A canceled draft holds nothing.
	canceled := shelf.saga("Северный предел, отменённая")
	queue, item = shelf.queue(canceled, "Книга 1", "rule-canceled-queue")
	shelf.linkVolume(canceled, "Книга 1", item, first)
	shelf.upload(canceled, queue.ID, item, first, "rule-canceled-file")
	shelf.as(canceled)
	if _, err := shelf.library.CancelImportQueue(ctx, library.CancelImportQueueInput{
		ProjectID: canceled, QueueID: queue.ID, ActorUserID: shelf.author, SessionWriteProof: shelf.proof,
	}); err != nil {
		t.Fatal(err)
	}
	if folders := shelf.folders(canceled); len(folders) != 0 {
		t.Fatalf("a canceled draft = %+v", folders)
	}

	// A volume whose Job failed holds nothing, and neither does the book of the other file the author gave it at its place.
	replaced := shelf.saga("Северный предел, заменённый")
	queue, item = shelf.queue(replaced, "Книга 1", "rule-replaced-queue")
	shelf.linkVolume(replaced, "Книга 1", item, first)
	shelf.start(replaced, queue.ID, shelf.upload(replaced, queue.ID, item, first, "rule-replaced-file"), "rule-replaced-start")
	shelf.runImport(true)
	if folders := shelf.folders(replaced); len(folders) != 0 {
		t.Fatalf("a volume whose Job failed = %+v", folders)
	}
	shelf.upload(replaced, queue.ID, item, other, "rule-replaced-other")
	shelf.runImport(false)
	var bookOfOther string
	if err := shelf.db.Pool.QueryRow(ctx, `SELECT COALESCE(book_id::text, '') FROM import_queue_items WHERE id = $1`, item).Scan(&bookOfOther); err != nil || bookOfOther == "" {
		t.Fatalf("the book of the other file = %q, %v", bookOfOther, err)
	}
	if folders := shelf.folders(replaced); len(folders) != 0 {
		t.Fatalf("a volume given another file = %+v", folders)
	}

	// A volume the author skipped holds nothing.
	skipped := shelf.saga("Северный предел, пропущенный")
	queue, item = shelf.queue(skipped, "Книга 1", "rule-skipped-queue")
	shelf.linkVolume(skipped, "Книга 1", item, first)
	shelf.start(skipped, queue.ID, shelf.upload(skipped, queue.ID, item, first, "rule-skipped-file"), "rule-skipped-start")
	shelf.runImport(true)
	shelf.as(skipped)
	if _, err := shelf.library.SkipImportQueueItem(ctx, library.SkipImportQueueItemInput{
		ProjectID: skipped, QueueID: queue.ID, ItemID: item, ActorUserID: shelf.author, SessionWriteProof: shelf.proof,
	}); err != nil {
		t.Fatal(err)
	}
	if folders := shelf.folders(skipped); len(folders) != 0 {
		t.Fatalf("a skipped volume = %+v", folders)
	}

	// Two live books of one folder: A in the trash, B linked with its folder, A back — B holds it, A is named.
	two := shelf.saga("Северный предел, две книги")
	bookA, bookB := shelf.book(two, "А"), shelf.book(two, "Б")
	shelf.as(two)
	if err := shelf.books.LinkBook(ctx, two, bookA, "Общая", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := shelf.library.DeleteBook(ctx, library.Actor{UserID: shelf.author}, bookA); err != nil {
		t.Fatal(err)
	}
	if err := shelf.books.LinkBook(ctx, two, bookB, "Общая", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := shelf.library.RestoreBook(ctx, library.Actor{UserID: shelf.author}, bookA); err != nil {
		t.Fatal(err)
	}
	if held := shelf.folders(two)["Общая"]; held.Book == nil || held.Book.BookID != bookB || len(held.Others) != 1 || held.Others[0].BookID != bookA {
		t.Fatalf("two live books of one folder = %+v", held)
	}
	// A book has one link: linking it with another folder takes the one before.
	if err := shelf.books.LinkBook(ctx, two, bookA, "Другая", time.Now()); err != nil {
		t.Fatal(err)
	}
	if folders := shelf.folders(two); folders["Общая"].Book.BookID != bookB || len(folders["Общая"].Others) != 0 || folders["Другая"].Book.BookID != bookA {
		t.Fatalf("a new link of a book = %+v", folders)
	}
	if err := shelf.books.LinkBook(ctx, two, "10000000-0000-4000-8000-0000000000ff", "Третья", time.Now()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a link of a book that is not = %v", err)
	}
	if err := shelf.books.UnlinkBook(ctx, two, bookA); err != nil {
		t.Fatal(err)
	}
	if folders := shelf.folders(two); len(folders) != 1 {
		t.Fatalf("after the unlink of a book = %+v", folders)
	}

	// A new path of the vault keeps the links; the unlink of the vault takes them; a saga without its vault keeps none.
	count := func(projectID string) int {
		var rows int
		if err := shelf.db.Pool.QueryRow(ctx, `SELECT count(*) FROM obsidian_books WHERE project_id = $1`, projectID).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	shelf.as(loaded)
	if err := shelf.vaults.Link(ctx, loaded, "/media/author/Хранилище"); err != nil || count(loaded) != 1 {
		t.Fatalf("a new path of the vault: %d links, %v", count(loaded), err)
	}
	if err := shelf.vaults.Unlink(ctx, shelf.author, loaded); err != nil || count(loaded) != 0 {
		t.Fatalf("the unlink of the vault: %d links, %v", count(loaded), err)
	}
	if err := shelf.books.LinkBook(ctx, loaded, held.Book.BookID, "Книга 1", time.Now()); !errors.Is(err, service.ErrNotLinked) {
		t.Fatalf("a link without the vault = %v", err)
	}
	drafts, err := shelf.books.Drafts(ctx, "")
	if err != nil || len(drafts) != 0 {
		t.Fatalf("the drafts of the bridge = %+v, %v", drafts, err)
	}
}
