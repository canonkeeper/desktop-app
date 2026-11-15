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
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// bridgeCore is the core of a test of the bridge on PostgreSQL: an author who writes, his saga linked with a vault of the
// example on the disk, the stores of the bridge, and the import Jobs run by hand one at a time, as a worker does.
type bridgeCore struct {
	t       *testing.T
	db      *storedb.DB
	author  string
	proof   auth.SessionWriteProof
	store   *librarystore.Store
	library *library.Service
	vaults  *obsidianrepo.Vaults
	links   *obsidianrepo.Books
	worker  string
	saga    string
	vault   string
	jobs    *jobs.Service
}

func newBridgeCore(t *testing.T, prefix string) *bridgeCore {
	t.Helper()
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, prefix)
	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	user, err := accounts.CreateUser(ctx, auth.NewUser{Email: "author@example.test", DisplayName: "Автор", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	session, csrf, err := accounts.CreateSession(ctx, auth.NewSession{UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, proof, err := accounts.AuthenticateWrite(ctx, session.CookieValue, csrf)
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	saga, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, user.ID, projects.CreateProjectInput{Title: "Северный предел"})
	if err != nil {
		t.Fatal(err)
	}
	store := librarystore.New(db)
	core := &bridgeCore{
		t: t, db: db, author: user.ID, proof: proof, store: store,
		library: library.NewService(store, library.NewSecureIDGenerator(), auth.SystemClock{}),
		vaults:  obsidianrepo.New(db), links: obsidianrepo.NewBooks(db), worker: pgx.Identifier{schema + "_worker"}.Sanitize(),
		saga: saga.ID, vault: filepath.Join(t.TempDir(), "Северный предел"),
		jobs: jobs.NewService(jobstore.New(db), auth.SystemClock{}),
	}
	if err := os.MkdirAll(core.vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.vaults.Link(ctx, saga.ID, core.vault); err != nil {
		t.Fatal(err)
	}
	return core
}

// write writes notes of the vault of the example.
func (core *bridgeCore) write(notes map[string]string) {
	core.t.Helper()
	for name, text := range notes {
		full := filepath.Join(core.vault, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			core.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			core.t.Fatal(err)
		}
	}
}

// runImport claims the next import Job, as a worker does, and ends it by its handler — or failed.
func (core *bridgeCore) runImport(fail bool) jobs.Job {
	core.t.Helper()
	ctx := context.Background()
	var claim jobs.WorkerClaim
	if err := core.db.Pool.QueryRow(ctx, `
		SELECT project_id::text, work_id::text, claim_incarnation FROM `+core.worker+`.claim_next_queued_job_for_worker($1, NULL)
	`, []string{string(jobs.KindImport)}).Scan(&claim.ProjectID, &claim.JobID, &claim.ClaimIncarnation); err != nil {
		core.t.Fatalf("no import Job to claim: %v", err)
	}
	job, err := jobstore.New(core.db).LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		core.t.Fatal(err)
	}
	handler := library.NewImportHandler(core.store, core.store)
	if fail {
		if _, err := handler.HandleFailedResult(ctx, job); err != nil {
			core.t.Fatal(err)
		}
	} else if result, err := handler.HandleResult(ctx, job); err != nil || result.Status != jobs.StatusSucceeded {
		core.t.Fatalf("the import = %+v, %v", result, err)
	}
	ended, err := core.jobs.GetJob(ctx, jobs.Actor{UserID: core.author}, job.ID)
	if err != nil {
		core.t.Fatal(err)
	}
	return ended
}

// importFile is «Загрузить книгу» or «Обновить из файла» of the core with a text, its Job run.
func (core *bridgeCore) importFile(name, text string, mode library.ImportMode, bookID, key string) jobs.Job {
	core.t.Helper()
	if _, err := core.library.CreateImportJob(context.Background(), library.Actor{UserID: core.author}, core.saga, library.CreateImportJobInput{
		SourceFileName: name, SizeBytes: int64(len(text)), FileBytes: []byte(text), IdempotencyKey: key,
		RequestMethod: "POST", RequestPath: "/api/v1/projects/" + core.saga + "/imports", ImportMode: mode, BookID: bookID,
		SessionWriteProof: core.proof,
	}); err != nil {
		core.t.Fatalf("CreateImportJob(%s) = %v", key, err)
	}
	return core.runImport(false)
}

// books are the live books of the saga by their titles.
func (core *bridgeCore) books() map[string]string {
	core.t.Helper()
	page, err := core.library.ListBooks(context.Background(), library.Actor{UserID: core.author}, core.saga, library.Page{Limit: 50})
	if err != nil {
		core.t.Fatal(err)
	}
	books := map[string]string{}
	for _, book := range page.Data {
		books[book.Title] = book.ID
	}
	return books
}

func (core *bridgeCore) preview(choice obsidian.Choice) service.Preview {
	core.t.Helper()
	preview, err := core.previewUseCase().Execute(context.Background(), service.PreviewInput{
		ActorUserID: core.author, ProjectID: core.saga, Choice: choice,
	})
	if err != nil {
		core.t.Fatalf("the preview = %v", err)
	}
	return preview
}

func (core *bridgeCore) previewUseCase() service.PreviewImport {
	return service.PreviewImport{
		Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Plans: obsidianPlans{store: core.store},
		Volumes: obsidianVolumes{}, Plain: library.MarkdownPlainLines, Saga: quietSaga{}, Gate: service.NewGate(), Limits: service.DefaultLimits,
	}
}

func libraryActor(userID string) domainlibrary.Actor { return domainlibrary.Actor{UserID: userID} }

// quietSaga is a saga whose book knows no name: a preview of books asks only its reading.
type quietSaga struct{}

func (quietSaga) BookNames(context.Context, string, []string) (map[string]string, error) {
	return nil, nil
}
func (quietSaga) Reading(context.Context, string) (service.Reading, error) {
	return service.Reading{Helper: service.HelperNone}, nil
}

// specs/2026-11-14-desk-obsidian-books.md OK0.3, OK0.4: the preview of part A1 with the links of the store — the folder of
// a linked book is linked and unchanged while the book is the glued text; after «Вернуть» of the update, an edit of a
// chapter in the app and «Обновить из файла» with another text it is changed, and after an update by the same glue it is
// not; a folder whose volume is in work is queued; an inert link gives neither linked nor folders_overlap.
func TestThePreviewOfALinkedFolderComparesTheBookPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_preview_links")
	ctx := context.Background()
	core.write(map[string]string{
		"Книга 1/01 Застава.md":   "Ольга вышла к заставе на рассвете. Ветер с перевала нёс снег.",
		"Книга 1/02 Переправа.md": "Мирон ждал у переправы. Паром не шёл.",
	})
	choice := obsidian.Choice{Books: []string{"Книга 1"}}
	first := core.preview(choice).Books[0]
	if first.State.Linked || first.State.Queued {
		t.Fatalf("a new folder = %+v", first.State)
	}
	core.importFile(first.Glued.FileName, first.Glued.Text, library.ImportModeNewBook, "", "preview-links-new")
	book := core.books()[first.Title]
	if book == "" {
		t.Fatalf("the books = %v", core.books())
	}
	if err := core.links.LinkBook(ctx, core.saga, book, "Книга 1", time.Now()); err != nil {
		t.Fatal(err)
	}
	state := func() service.FolderState {
		t.Helper()
		return core.preview(choice).Books[0].State
	}
	if got := state(); !got.Linked || got.BookID != book || got.Changed || got.InTrash {
		t.Fatalf("the folder of the book it made = %+v", got)
	}

	// The author writes on in Obsidian: changed; the update by the glue makes it the book again.
	core.write(map[string]string{"Книга 1/02 Переправа.md": "Мирон ждал у переправы. Паром пришёл к вечеру."})
	edited := core.preview(choice).Books[0]
	if !edited.State.Changed {
		t.Fatalf("a folder written on = %+v", edited.State)
	}
	core.importFile(edited.Glued.FileName, edited.Glued.Text, library.ImportModeUpdateBook, book, "preview-links-update")
	if got := state(); got.Changed {
		t.Fatalf("after the update by the glue = %+v", got)
	}
	// «Вернуть» brings the text before the update back: the folder differs from the book again.
	revert, err := core.store.PreviewBookUpdateRevert(ctx, libraryActor(core.author), book)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.store.RevertBookUpdate(ctx, libraryActor(core.author), book, revert.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := state(); !got.Changed {
		t.Fatalf("after «Вернуть» = %+v", got)
	}
	core.importFile(edited.Glued.FileName, edited.Glued.Text, library.ImportModeUpdateBook, book, "preview-links-again")
	if got := state(); got.Changed {
		t.Fatalf("after the update by the same glue = %+v", got)
	}
	// An edit of a chapter in the app.
	var chapter string
	var revision int64
	if err := core.db.Pool.QueryRow(ctx, `SELECT id::text, revision FROM chapters WHERE book_id = $1 AND deleted_at IS NULL ORDER BY "order" LIMIT 1`, book).
		Scan(&chapter, &revision); err != nil {
		t.Fatal(err)
	}
	if err := core.db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := core.store.ReplaceChapterTextInTx(ctx, tx, library.ReplaceChapterTextInput{
			ProjectID: core.saga, BookID: book, ChapterID: chapter, ExpectedRevision: revision, Title: "Застава",
			Paragraphs: []string{"Ольга вышла к заставе в полдень."},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := state(); !got.Changed {
		t.Fatalf("after an edit of a chapter in the app = %+v", got)
	}
	// «Обновить из файла» with another text, then with the glue.
	core.importFile("Другая.md", "# Застава\n\n# Застава\n\nДругой текст.\n", library.ImportModeUpdateBook, book, "preview-links-file")
	if got := state(); !got.Changed {
		t.Fatalf("after «Обновить из файла» with another text = %+v", got)
	}
	core.importFile(edited.Glued.FileName, edited.Glued.Text, library.ImportModeUpdateBook, book, "preview-links-glue")
	if got := state(); got.Changed {
		t.Fatalf("after «Обновить из файла» with the glue = %+v", got)
	}

	// An inert link — a volume of a canceled draft — gives neither linked nor folders_overlap.
	core.write(map[string]string{"Книга 2/Часть/01 Начало.md": "Начало второй книги.", "Книга 3/01 Начало.md": "Начало третьей книги."})
	queue, err := core.library.CreateImportQueue(ctx, library.CreateImportQueueInput{
		ProjectID: core.saga, ActorUserID: core.author, IdempotencyKey: "preview-links-canceled", RequestMethod: "POST",
		RequestPath: "/test/queue", SessionWriteProof: core.proof,
		Items: []library.CreateImportQueueItemInput{{ClientItemID: "Книга 2/Часть", Position: 1, SourceFileName: "Часть.md"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.links.AddVolumes(ctx, core.saga, []service.VolumeLink{{Folder: "Книга 2/Часть", ItemID: queue.Items[0].ID, Fingerprint: fmt.Sprintf("%064d", 0)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := core.library.CancelImportQueue(ctx, library.CancelImportQueueInput{
		ProjectID: core.saga, QueueID: queue.ID, ActorUserID: core.author, SessionWriteProof: core.proof,
	}); err != nil {
		t.Fatal(err)
	}
	if got := core.preview(obsidian.Choice{Books: []string{"Книга 2"}}).Books[0].State; got.Linked || got.Queued {
		t.Fatalf("a folder over an inert link = %+v", got)
	}

	// A folder whose volume is in work is queued.
	third := core.preview(obsidian.Choice{Books: []string{"Книга 3"}}).Books[0]
	queue, err = core.library.CreateImportQueue(ctx, library.CreateImportQueueInput{
		ProjectID: core.saga, ActorUserID: core.author, IdempotencyKey: "preview-links-queued", RequestMethod: "POST",
		RequestPath: "/test/queue", SessionWriteProof: core.proof,
		Items: []library.CreateImportQueueItemInput{{ClientItemID: "Книга 3", Position: 1, SourceFileName: third.Glued.FileName}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.links.AddVolumes(ctx, core.saga, []service.VolumeLink{{Folder: "Книга 3", ItemID: queue.Items[0].ID, Fingerprint: third.Fingerprint}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := core.preview(obsidian.Choice{Books: []string{"Книга 3"}}); !got.Books[0].State.Queued || got.Books[0].State.QueueID != queue.ID || got.NewChapters != 0 {
		t.Fatalf("a folder whose volume is in work = %+v", got.Books[0].State)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK0.3, OK0.4 (one rule of what holds a folder): the operations of the bridge the
// build mounts — the look, the preview, the load, the update, the link of a book and getObsidianVault — read the links of
// the store by the same rule: a folder a book holds is its book in each of them.
func TestTheBridgeOfTheBuildReadsTheLinksByOneRulePostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_bridge_rule")
	ctx := context.Background()
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе на рассвете."})
	book := core.libraryBook("Застава")
	if err := core.links.LinkBook(ctx, core.saga, book, "Книга 1", time.Now()); err != nil {
		t.Fatal(err)
	}
	handlers := bridgeOfTheTest().handlers(
		app.Core{DB: core.db, Library: core.store, LibraryService: core.library, JobService: core.jobs},
		func(context.Context) service.Helper { return service.HelperNone }, func(context.Context, string) (bool, error) { return false, nil }, nil,
	)

	scanned, err := handlers.Scan.Execute(ctx, service.ScanInput{ActorUserID: core.author, ProjectID: core.saga})
	if err != nil {
		t.Fatal(err)
	}
	if folder := scanned.Folders[slices.IndexFunc(scanned.Folders, func(folder service.ScannedFolder) bool { return folder.Path == "Книга 1" })]; folder.Book == nil || folder.Book.BookID != book {
		t.Fatalf("the look = %+v", folder)
	}
	choice := obsidian.Choice{Books: []string{"Книга 1"}}
	preview, err := handlers.Preview.Execute(ctx, service.PreviewInput{ActorUserID: core.author, ProjectID: core.saga, Choice: choice})
	if err != nil || !preview.Books[0].State.Linked || preview.Books[0].State.BookID != book || !preview.Books[0].State.Changed {
		t.Fatalf("the preview = %+v, %v", preview.Books, err)
	}
	vault, err := handlers.Read.Execute(ctx, core.author, core.saga)
	if err != nil || len(vault.Books) != 1 || vault.Books[0].BookID != book || vault.Books[0].Folder != "Книга 1" {
		t.Fatalf("getObsidianVault = %+v, %v", vault, err)
	}
	loaded, err := handlers.Load.Execute(ctx, service.LoadInput{
		ActorUserID: core.author, ProjectID: core.saga, Choice: choice, Fingerprints: map[string]string{"Книга 1": preview.Books[0].Fingerprint},
		Key: "bridge-rule-load", Method: "POST", Path: "/api/v1/projects/" + core.saga + "/obsidian/books", Proof: core.proof,
	})
	if err != nil || loaded.Queue != nil || loaded.Folders[0].Outcome != service.OutcomeLinked || loaded.Folders[0].BookID != book {
		t.Fatalf("the load = %+v, %v", loaded, err)
	}
	updated, err := handlers.Update.Execute(ctx, core.updateInput(book, false, "", "bridge-rule-update"))
	if err != nil || updated.Book == nil || updated.Book.Folder != "Книга 1" || !updated.Book.State.Changed {
		t.Fatalf("the update = %+v, %v", updated, err)
	}
	core.runImport(false)
	var linked service.FolderLinked
	if err := handlers.LinkBook.Execute(ctx, core.folderInput(core.libraryBook("Другая"), "Книга 1")); !errors.As(err, &linked) || linked.BookID != book {
		t.Fatalf("the link of another book with the folder = %v", err)
	}
}
