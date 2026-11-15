// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/obsidian"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

func (core *bridgeCore) updateUseCase() service.UpdateBook {
	return service.UpdateBook{
		Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Plans: obsidianPlans{store: core.store}, Volumes: obsidianVolumes{},
		Shelf: core.shelf(), Gate: service.NewGate(), Limits: service.DefaultLimits, Logger: log.New(io.Discard, "", 0),
	}
}

func (core *bridgeCore) linkUseCase() service.LinkBookFolder {
	return service.LinkBookFolder{
		Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Shelf: core.shelf(), Gate: service.NewGate(),
		Limits: service.DefaultLimits, Logger: log.New(io.Discard, "", 0), Now: time.Now,
	}
}

func (core *bridgeCore) updateInput(book string, apply bool, expected, key string) service.UpdateInput {
	return service.UpdateInput{
		ActorUserID: core.author, ProjectID: core.saga, BookID: book, Apply: apply, Expected: expected, Key: key,
		Method: "POST", Path: "/api/v1/projects/" + core.saga + "/obsidian/books/" + book + "/update", Proof: core.proof,
	}
}

func (core *bridgeCore) folderInput(book, folder string) service.BookFolderInput {
	return service.BookFolderInput{ActorUserID: core.author, ProjectID: core.saga, BookID: book, Folder: folder}
}

// runIndexes claims the text index Jobs that wait, one at a time as a worker does, and runs each: the books of the imports
// are ready, and a queue whose every volume is ready ends.
func (core *bridgeCore) runIndexes() {
	core.t.Helper()
	ctx := context.Background()
	handler := indexing.NewTextIndexHandler(indexing.NewService(indexingstore.New(core.db), indexing.NewSecureIDGenerator(), auth.SystemClock{}))
	for {
		var claim jobs.WorkerClaim
		err := core.db.Pool.QueryRow(ctx, `
			SELECT project_id::text, work_id::text, claim_incarnation FROM `+core.worker+`.claim_next_queued_job_for_worker($1, NULL)
		`, []string{string(jobs.KindCanonTextIndex)}).Scan(&claim.ProjectID, &claim.JobID, &claim.ClaimIncarnation)
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			core.t.Fatalf("the claim of a text index Job: %v", err)
		}
		job, err := jobstore.New(core.db).LoadJobForWorkerClaim(ctx, claim)
		if err != nil {
			core.t.Fatal(err)
		}
		if result, err := handler.HandleResult(ctx, job); err != nil || result.Status != jobs.StatusSucceeded {
			core.t.Fatalf("the text index = %+v, %v", result, err)
		}
	}
}

// loadOne loads a folder of a book through the queue, runs its Job and the text index of its book: the book it gives, its
// queue ended.
func (core *bridgeCore) loadOne(key string, choice obsidian.Choice) string {
	core.t.Helper()
	input, _ := core.fingerprinted(key, choice)
	if loaded, err := core.loadUseCase().Execute(context.Background(), input); err != nil || loaded.Queue == nil {
		core.t.Fatalf("the load = %+v, %v", loaded, err)
	}
	core.runImport(false)
	core.runIndexes()
	if core.liveQueues() != 0 {
		core.t.Fatal("the queue of the load did not end with its book ready")
	}
	held := core.heldFolders()[choice.Books[0]]
	if held.Book == nil {
		core.t.Fatalf("the folder after its load = %+v", held)
	}
	return held.Book.BookID
}

// chapterRows are the live chapters of a book: title, place and the Markdown of each row of its draft.
func (core *bridgeCore) chapterRows(book string) []string {
	core.t.Helper()
	rows, err := core.db.Pool.Query(context.Background(), `
		SELECT chapter.title || ' @' || chapter."order" || ': ' || COALESCE(string_agg(row.markdown, ' | ' ORDER BY row."order"), '')
		FROM chapters AS chapter
		LEFT JOIN chapter_paragraphs AS row ON row.chapter_id = chapter.id AND row.revision = chapter.revision AND row.variant = 'draft'
		WHERE chapter.project_id = $1 AND chapter.book_id = $2 AND chapter.deleted_at IS NULL
		GROUP BY chapter.id, chapter.title, chapter."order" ORDER BY chapter."order"
	`, core.saga, book)
	if err != nil {
		core.t.Fatal(err)
	}
	defer rows.Close()
	var chapters []string
	for rows.Next() {
		var chapter string
		if err := rows.Scan(&chapter); err != nil {
			core.t.Fatal(err)
		}
		chapters = append(chapters, chapter)
	}
	return chapters
}

// snapshotRows are the rows of the snapshot «Вернуть» of a book keeps, without their ids.
func (core *bridgeCore) snapshotRows(book string) []string {
	core.t.Helper()
	rows, err := core.db.Pool.Query(context.Background(), `
		SELECT change || ' ' || revision || ' ' || generation || ' ' || COALESCE(previous_revision::text, '-') || ' ' ||
		       COALESCE(previous_title, '-') || ' ' || COALESCE(previous_order::text, '-') || ' ' || COALESCE(array_to_string(previous_markdown, ' | '), '-')
		FROM book_update_snapshot_chapters WHERE book_id = $1 ORDER BY 1
	`, book)
	if err != nil {
		core.t.Fatal(err)
	}
	defer rows.Close()
	var snapshot []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			core.t.Fatal(err)
		}
		snapshot = append(snapshot, row)
	}
	return snapshot
}

func (core *bridgeCore) importsOfTheDay() int {
	core.t.Helper()
	var imports int
	if err := core.db.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(imports), 0) FROM writer_usage_days WHERE user_id = $1`, core.author).
		Scan(&imports); err != nil {
		core.t.Fatal(err)
	}
	return imports
}

// previewOf is the preview of an import Job once it ran.
func previewOf(t *testing.T, job jobs.Job) *jobs.ImportPreview {
	t.Helper()
	result, ok := job.Result.(jobs.ImportJobResult)
	if !ok || result.Preview == nil || job.Status != jobs.StatusSucceeded {
		t.Fatalf("the Job of a preview = %s %+v", job.Status, job.Result)
	}
	return result.Preview
}

// specs/2026-11-14-desk-obsidian-books.md OK2.1, OK2.2 on PostgreSQL and the service of the library: the preview of the bridge
// is the preview of «Обновить из файла» with the same bytes on the same book, and its glue takes the choice of the load — the
// folder of the characters inside the book and the drafts left out are no chapters —; the application gives the book what the
// update from a file gives an equal book of another saga — chapters, places, faces and the rows of the snapshot —, «Вернуть»
// returns it, and the preview and the application cost one upload, as with a file; an application the plan of which changes
// nothing is nothing_to_apply and the snapshot of the update before stays.
func TestAnUpdateFromTheFolderIsTheUpdateFromAFilePostgres(t *testing.T) {
	ctx := context.Background()
	notes := map[string]string{
		"Книга 1/01 Застава.md":        "Ольга вышла к заставе на рассвете. Ветер с перевала нёс снег.",
		"Книга 1/02 Переправа.md":      "Мирон ждал у переправы. Паром не шёл.",
		"Книга 1/Персонажи/Ольга.md":   "Старшая на заставе.",
		"Книга 1/Черновики/Обрывок.md": "Обрывок без места.",
	}
	choice := obsidian.Choice{
		Books: []string{"Книга 1"}, Materials: []obsidian.MaterialFolder{{Path: "Книга 1/Персонажи"}}, Exclude: []string{"Книга 1/Черновики"},
	}
	core := newBridgeCore(t, "ck_obsidian_update_file")
	core.write(notes)
	loaded := core.preview(choice).Books[0].Glued
	book := core.loadOne("update-file-load", choice)
	before := core.chapterRows(book)

	twin := newBridgeCore(t, "ck_obsidian_update_twin")
	twin.importFile(loaded.FileName, loaded.Text, library.ImportModeNewBook, "", "update-twin-new")
	twinBook := twin.books()[core.heldFolders()["Книга 1"].Book.Title]
	if twinBook == "" || !slices.Equal(twin.chapterRows(twinBook), before) {
		t.Fatalf("the twin of the book = %q, %v", twin.chapterRows(twinBook), twin.books())
	}

	core.write(map[string]string{
		"Книга 1/01 Застава.md":      "Ольга вышла к заставе в полдень. Ветер с перевала *стих*.",
		"Книга 1/03 Порт.md":         "Судно вошло в порт. Берег был пуст.",
		"Книга 1/Персонажи/Мирон.md": "Паромщик.",
	})
	units := core.importsOfTheDay()
	updated, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-file-preview"))
	if err != nil || updated.Book == nil || !updated.Book.State.Changed {
		t.Fatalf("the preview of the bridge = %+v, %v", updated, err)
	}
	if titles := titlesOfBook(*updated.Book); !slices.Equal(titles, []string{"Застава", "Переправа", "Порт"}) {
		t.Fatalf("the chapters of the update = %q", titles)
	}
	bridged := previewOf(t, core.runImport(false))
	if core.importsOfTheDay() != units+1 {
		t.Fatalf("the uploads of the preview = %d, before %d", core.importsOfTheDay(), units)
	}
	glued := updated.Book.Glued
	core.importFile(glued.FileName, glued.Text, library.ImportModeUpdatePreview, book, "update-file-standard")
	standard := previewOf(t, core.jobOfKey("update-file-standard"))
	if !reflect.DeepEqual(bridged, standard) {
		t.Fatalf("the preview of the bridge %+v is not the preview of the file %+v", bridged, standard)
	}

	units = core.importsOfTheDay()
	applied, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, updated.Fingerprint, "update-file-apply"))
	if err != nil || applied.Job.ID == "" {
		t.Fatalf("the application = %+v, %v", applied, err)
	}
	ended := core.runImport(false)
	if ended.ID != applied.Job.ID || core.importsOfTheDay() != units || ended.CompletedAt == nil {
		t.Fatalf("the application ran %s, uploads %d, before %d", ended.ID, core.importsOfTheDay(), units)
	}
	twin.importFile(glued.FileName, glued.Text, library.ImportModeUpdateBook, twinBook, "update-twin-update")
	if after, twinAfter := core.chapterRows(book), twin.chapterRows(twinBook); !slices.Equal(after, twinAfter) || slices.Equal(after, before) {
		t.Fatalf("the book after the application = %q, the twin %q", after, twinAfter)
	}
	snapshot := core.snapshotRows(book)
	if twinSnapshot := twin.snapshotRows(twinBook); len(snapshot) == 0 || !slices.Equal(snapshot, twinSnapshot) {
		t.Fatalf("the snapshot = %q, the twin %q", snapshot, twinSnapshot)
	}
	// OK2.6: the book was last loaded from its folder when the application ended, not when its volume did.
	if held := core.heldFolders()["Книга 1"]; held.Book == nil || held.Book.LoadedAt == nil || !held.Book.LoadedAt.Equal(*ended.CompletedAt) {
		t.Fatalf("the folder after the application = %+v, the application ended %v", held.Book, ended.CompletedAt)
	}

	// The plan changes nothing now: the preview says so without a refusal, the application is refused before a Job.
	same, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-file-same"))
	if err != nil || same.Book.State.Changed {
		t.Fatalf("a preview that changes nothing = %+v, %v", same.Book, err)
	}
	core.runImport(false)
	jobsBefore := core.count(`SELECT count(*) FROM jobs WHERE project_id = $1`)
	if _, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, same.Fingerprint, "update-file-same-apply")); !errors.Is(err, service.ErrNothingToApply) ||
		core.count(`SELECT count(*) FROM jobs WHERE project_id = $1`) != jobsBefore || !slices.Equal(core.snapshotRows(book), snapshot) {
		t.Fatalf("an application that changes nothing = %v", err)
	}

	// «Вернуть» returns the book as it was loaded.
	revert, err := core.store.PreviewBookUpdateRevert(ctx, libraryActor(core.author), book)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.store.RevertBookUpdate(ctx, libraryActor(core.author), book, revert.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := core.chapterRows(book); !slices.Equal(got, before) {
		t.Fatalf("the book after «Вернуть» = %q, loaded %q", got, before)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK2.2 and specs/2026-11-12-ux10-book-revert.md BR1.2 on PostgreSQL: the same
// preview applied twice under two keys before the first Job ran passes the plan twice — the book has not changed yet — and
// the second Job changes nothing: it writes no snapshot, and «Вернуть» returns the book as it was before the first.
func TestTwoApplicationsOfOnePreviewKeepTheReturnPostgres(t *testing.T) {
	ctx := context.Background()
	core := newBridgeCore(t, "ck_obsidian_update_twice")
	core.write(map[string]string{"Книга 1/01 Застава.md": "Ольга вышла к заставе.", "Книга 1/02 Переправа.md": "Мирон ждал у переправы."})
	book := core.loadOne("update-twice-load", obsidian.Choice{Books: []string{"Книга 1"}})
	before := core.chapterRows(book)
	core.write(map[string]string{"Книга 1/03 Порт.md": "Судно вошло в порт."})
	preview, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-twice-preview"))
	if err != nil {
		t.Fatal(err)
	}
	core.runImport(false)
	first, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, preview.Fingerprint, "update-twice-apply-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, preview.Fingerprint, "update-twice-apply-2"))
	if err != nil || second.Job.ID == first.Job.ID {
		t.Fatalf("the second application = %+v, %v; the first %s", second.Job, err, first.Job.ID)
	}
	if one, two := core.runImport(false), core.runImport(false); one.ID != first.Job.ID || one.Status != jobs.StatusSucceeded ||
		two.ID != second.Job.ID || two.Status != jobs.StatusSucceeded {
		t.Fatalf("the applications ran %s %s and %s %s", one.ID, one.Status, two.ID, two.Status)
	}
	after := core.chapterRows(book)
	if slices.Equal(after, before) || len(after) != 3 {
		t.Fatalf("the book after both applications = %q", after)
	}
	revert, err := core.store.PreviewBookUpdateRevert(ctx, libraryActor(core.author), book)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.store.RevertBookUpdate(ctx, libraryActor(core.author), book, revert.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := core.chapterRows(book); !slices.Equal(got, before) {
		t.Fatalf("the book after «Вернуть» = %q, before the first application %q, after both %q", got, before, after)
	}
}

func titlesOfBook(book service.BookPreview) []string {
	var titles []string
	for _, chapter := range book.Chapters {
		titles = append(titles, chapter.Title)
	}
	return titles
}

// jobOfKey is the import Job of a key of «Загрузить книгу» of a test, ended.
func (core *bridgeCore) jobOfKey(key string) jobs.Job {
	core.t.Helper()
	jobID, ok, err := core.links.JobOfKey(context.Background(), core.author, "POST", "/api/v1/projects/"+core.saga+"/imports", key)
	if err != nil || !ok {
		core.t.Fatalf("the Job of %s: %v", key, err)
	}
	job, err := core.jobs.GetJob(context.Background(), jobs.Actor{UserID: core.author}, jobID)
	if err != nil {
		core.t.Fatal(err)
	}
	return job
}

// cloudFiles are the files of a vault whose notes of a name live only in the cloud.
type cloudFiles struct {
	obsidianvault.Files
	name string
}

func (files cloudFiles) Open(vaultPath string) (service.Vault, error) {
	vault, err := files.Files.Open(vaultPath)
	if err != nil {
		return nil, err
	}
	return cloudVault{Vault: vault, name: files.name}, nil
}

type cloudVault struct {
	service.Vault
	name string
}

func (vault cloudVault) Read(ctx context.Context, notePath string) ([]byte, obsidian.Skip) {
	if strings.Contains(notePath, vault.name) {
		return nil, obsidian.SkipCloudOnly
	}
	return vault.Vault.Read(ctx, notePath)
}

// specs/2026-11-14-desk-obsidian-books.md OK2.2, OK2.4, OK2.5 on PostgreSQL: an application of a folder changed since its
// preview is folder_changed and starts no Job; a note only in the cloud is named by the preview, and the application without
// the word of the author is notes_unread with the chapter in the book; a lost answer of an application and an edit of the
// folder give the same Job; a live queue of the saga is queue_busy; the folder of the link gone is folder_missing with the
// book and its link as they were.
func TestAnApplicationKeepsTheBookPostgres(t *testing.T) {
	ctx := context.Background()
	core := newBridgeCore(t, "ck_obsidian_update_apply")
	core.write(map[string]string{
		"Книга 1/01 Застава.md":   "Ольга вышла к заставе на рассвете.",
		"Книга 1/02 Переправа.md": "Мирон ждал у переправы.",
	})
	book := core.loadOne("update-apply-load", obsidian.Choice{Books: []string{"Книга 1"}})
	core.write(map[string]string{"Книга 1/03 Порт.md": "Судно вошло в порт."})
	preview, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-apply-preview"))
	if err != nil {
		t.Fatal(err)
	}
	core.runImport(false)
	jobsOf := func() int { return core.count(`SELECT count(*) FROM jobs WHERE project_id = $1`) }
	jobsBefore := jobsOf()

	core.write(map[string]string{"Книга 1/03 Порт.md": "Судно вошло в порт на закате."})
	var changed service.FolderChanged
	if _, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, preview.Fingerprint, "update-apply-changed")); !errors.As(err, &changed) ||
		jobsOf() != jobsBefore {
		t.Fatalf("an application of a folder changed since its preview = %v", err)
	}

	cloud := core.updateUseCase()
	cloud.Files = cloudFiles{name: "Переправа"}
	shown, err := cloud.Execute(ctx, core.updateInput(book, false, "", "update-apply-cloud"))
	if err != nil || shown.Book.Skipped[obsidian.SkipCloudOnly] != 1 {
		t.Fatalf("a preview with a note in the cloud = %+v, %v", shown.Book, err)
	}
	core.runImport(false)
	jobsBefore = jobsOf()
	var unread service.NotesUnread
	if _, err := cloud.Execute(ctx, core.updateInput(book, true, shown.Fingerprint, "update-apply-cloud-apply")); !errors.As(err, &unread) ||
		unread.Unread[obsidian.SkipCloudOnly] != 1 || jobsOf() != jobsBefore || !slices.ContainsFunc(core.chapterRows(book), func(row string) bool {
		return strings.HasPrefix(row, "Переправа")
	}) {
		t.Fatalf("an application without the word of the author = %v, chapters %q", err, core.chapterRows(book))
	}

	// The answer of an application is lost and the author edits the folder: the replay gives the same Job.
	current, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-apply-current"))
	if err != nil {
		t.Fatal(err)
	}
	core.runImport(false)
	applied, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, current.Fingerprint, "update-apply-lost"))
	if err != nil {
		t.Fatal(err)
	}
	core.write(map[string]string{"Книга 1/04 Берег.md": "Берег был пуст."})
	again, err := core.updateUseCase().Execute(ctx, core.updateInput(book, true, current.Fingerprint, "update-apply-lost"))
	if err != nil || again.Job.ID != applied.Job.ID {
		t.Fatalf("the replay of a lost application = %+v, %v; the first %s", again.Job.ID, err, applied.Job.ID)
	}
	core.runImport(false)

	page := core.pageQueue("update-apply-page")
	var busy service.QueueBusy
	if _, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-apply-busy")); !errors.As(err, &busy) || busy.Queue.ID != page.ID {
		t.Fatalf("an update beside a live queue = %v", err)
	}
	if _, err := core.library.CancelImportQueue(ctx, library.CancelImportQueueInput{ProjectID: core.saga, QueueID: page.ID, ActorUserID: core.author, SessionWriteProof: core.proof}); err != nil {
		t.Fatal(err)
	}

	chapters := core.chapterRows(book)
	if err := os.RemoveAll(filepath.Join(core.vault, "Книга 1")); err != nil {
		t.Fatal(err)
	}
	var missing service.FolderMissing
	if _, err := core.updateUseCase().Execute(ctx, core.updateInput(book, false, "", "update-apply-missing")); !errors.As(err, &missing) ||
		missing.Folder != "Книга 1" || !slices.Equal(core.chapterRows(book), chapters) || core.heldFolders()["Книга 1"].Book == nil {
		t.Fatalf("the folder of the link gone = %v", err)
	}
	if _, err := core.updateUseCase().Execute(ctx, core.updateInput(core.libraryBook("Без папки"), false, "", "update-apply-none")); !errors.Is(err, service.ErrBookNotLinked) {
		t.Fatalf("an update of a book without a link = %v", err)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK2.3, OK2.4, OK2.6, OK0.3 on PostgreSQL: a book that came from a file is linked
// with a folder without a load, and the preview of its update shows the difference; the folder of another live book, one
// inside it and one that holds it are refused; a new link takes the place of the one before; a book A deleted, a book B
// linked with its folder, A returned: B holds the folder, A is named to be linked anew; the link of a book in «Недавно
// удалённые» taken off, its folder loads; a folder not in the vault is folder_missing; a live queue is queue_busy.
func TestALinkOfABookWithoutALoadPostgres(t *testing.T) {
	ctx := context.Background()
	core := newBridgeCore(t, "ck_obsidian_link_book")
	core.write(map[string]string{
		"Книга 1/01 Застава.md": "Ольга вышла к заставе в полдень. Ветер стих.",
		"Книга 2/01 Порт.md":    "Судно вошло в порт.",
		"Книга 3/01 Берег.md":   "Берег был пуст.",
		"Книга 4/01 Переход.md": "Отряд ушёл на перевал.",
	})
	job := core.importFile("Застава.md", "# Застава\n\n# Застава\n\nОльга вышла к заставе на рассвете.\n", library.ImportModeNewBook, "", "link-book-file")
	result, _ := job.Result.(jobs.ImportJobResult)
	fromFile := result.BookID
	link := core.linkUseCase()
	if err := link.Execute(ctx, core.folderInput(fromFile, "Книга 1")); err != nil {
		t.Fatal(err)
	}
	if state := core.preview(obsidian.Choice{Books: []string{"Книга 1"}}).Books[0].State; !state.Linked || state.BookID != fromFile || !state.Changed {
		t.Fatalf("the folder of a book from a file = %+v", state)
	}
	updated, err := core.updateUseCase().Execute(ctx, core.updateInput(fromFile, false, "", "link-book-preview"))
	if err != nil {
		t.Fatal(err)
	}
	if plan := previewOf(t, core.runImport(false)); updated.Book == nil || plan == nil {
		t.Fatalf("the preview of the update of a book from a file = %+v", updated)
	}

	other := core.libraryBook("Порт")
	if err := link.Execute(ctx, core.folderInput(other, "Книга 2")); err != nil {
		t.Fatal(err)
	}
	var linked service.FolderLinked
	var overlap obsidian.Overlap
	if err := link.Execute(ctx, core.folderInput(fromFile, "Книга 2")); !errors.As(err, &linked) || linked.BookID != other {
		t.Fatalf("the folder of another live book = %v", err)
	}
	core.write(map[string]string{"Книга 2/Часть/01 Начало.md": "Начало."})
	for _, folder := range []string{"Книга 2/Часть", obsidian.Root} {
		if err := link.Execute(ctx, core.folderInput(fromFile, folder)); !errors.As(err, &overlap) {
			t.Fatalf("the folder %q = %v", folder, err)
		}
	}
	// A new link takes the place of the one before.
	if err := link.Execute(ctx, core.folderInput(fromFile, "Книга 4")); err != nil {
		t.Fatal(err)
	}
	if held := core.heldFolders(); held["Книга 1"].Book != nil || held["Книга 4"].Book == nil || held["Книга 4"].Book.BookID != fromFile {
		t.Fatalf("the folders after a new link = %+v", held)
	}

	// Delete A, link B with its folder, return A: B holds the folder, A is to be linked anew.
	first, second := core.libraryBook("Берег"), core.libraryBook("Берег (второй)")
	if err := link.Execute(ctx, core.folderInput(first, "Книга 3")); err != nil {
		t.Fatal(err)
	}
	if err := core.library.DeleteBook(ctx, library.Actor{UserID: core.author}, first); err != nil {
		t.Fatal(err)
	}
	if held := core.heldFolders()["Книга 3"]; held.Trash == nil || held.Trash.BookID != first {
		t.Fatalf("the folder of a book in the trash = %+v", held)
	}
	if err := link.Execute(ctx, core.folderInput(second, "Книга 3")); err != nil {
		t.Fatal(err)
	}
	if _, err := core.library.RestoreBook(ctx, library.Actor{UserID: core.author}, first); err != nil {
		t.Fatal(err)
	}
	read := service.ReadVault{Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Openings: service.NewOpenings(), Wait: service.AvailableTime}
	state, err := read.Execute(ctx, core.author, core.saga)
	if err != nil {
		t.Fatal(err)
	}
	holder := slices.IndexFunc(state.Books, func(book service.VaultBook) bool { return book.Folder == "Книга 3" })
	if holder < 0 || state.Books[holder].BookID != second || state.Books[holder].Present == nil || !*state.Books[holder].Present ||
		len(state.Relink) != 1 || state.Relink[0].BookID != first {
		t.Fatalf("the vault after the return of A = %+v", state)
	}

	// The link of a book in the trash taken off: its folder loads.
	if err := core.library.DeleteBook(ctx, library.Actor{UserID: core.author}, other); err != nil {
		t.Fatal(err)
	}
	if got := core.preview(obsidian.Choice{Books: []string{"Книга 2"}}).Books[0].State; !got.InTrash {
		t.Fatalf("the folder of a book in the trash = %+v", got)
	}
	if err := (service.UnlinkBookFolder{Vaults: core.vaults, Links: core.links}).Execute(ctx, core.folderInput(other, "")); err != nil {
		t.Fatal(err)
	}
	input, _ := core.fingerprinted("link-book-reload", obsidian.Choice{Books: []string{"Книга 2"}})
	if loaded, err := core.loadUseCase().Execute(ctx, input); err != nil || loaded.Folders[0].Outcome != service.OutcomeQueued {
		t.Fatalf("the folder whose link was taken off = %+v, %v", loaded, err)
	}
	var busy service.QueueBusy
	if err := link.Execute(ctx, core.folderInput(fromFile, "Книга 1")); !errors.As(err, &busy) {
		t.Fatalf("a link beside a live queue = %v", err)
	}
	core.runImport(false)
	core.runIndexes()
	var missing service.FolderMissing
	if err := link.Execute(ctx, core.folderInput(fromFile, "Книга 9")); !errors.As(err, &missing) || missing.Folder != "Книга 9" {
		t.Fatalf("a folder not in the vault = %v", err)
	}
	if held := core.heldFolders()["Книга 4"]; held.Book == nil || held.Book.BookID != fromFile {
		t.Fatalf("the link after a refusal = %+v", held)
	}
}

// specs/2026-11-15-desk-obsidian-web.md OW2.5 (SM1 of the review) on PostgreSQL: getObsidianVault names the composition of a
// book the link keeps — the paths its load left out under its folder and the folders inside it chosen otherwise —, which the
// glue of an update takes, so the page asks a preview of its folder by the same composition; a book linked without a load
// keeps none. No absolute path is among them.
func TestTheVaultNamesTheCompositionOfALoadedBookPostgres(t *testing.T) {
	ctx := context.Background()
	core := newBridgeCore(t, "ck_obsidian_vault_choice")
	core.write(map[string]string{
		"Книга 1/01 Застава.md":        "Ольга вышла к заставе на рассвете.",
		"Книга 1/Персонажи/Ольга.md":   "Старшая на заставе.",
		"Книга 1/Черновики/Обрывок.md": "Обрывок без места.",
		"Книга 2/01 Порт.md":           "Судно вошло в порт.",
	})
	loaded := core.loadOne("vault-choice-load", obsidian.Choice{
		Books: []string{"Книга 1"}, Materials: []obsidian.MaterialFolder{{Path: "Книга 1/Персонажи"}}, Exclude: []string{"Книга 1/Черновики"},
	})
	fromFile := core.libraryBook("Порт")
	if err := core.linkUseCase().Execute(ctx, core.folderInput(fromFile, "Книга 2")); err != nil {
		t.Fatal(err)
	}
	read := service.ReadVault{Vaults: core.vaults, Files: obsidianvault.Files{}, Links: core.links, Openings: service.NewOpenings(), Wait: service.AvailableTime}
	state, err := read.Execute(ctx, core.author, core.saga)
	if err != nil {
		t.Fatal(err)
	}
	byBook := map[string]service.VaultBook{}
	for _, book := range state.Books {
		byBook[book.BookID] = book
	}
	if got := byBook[loaded]; got.Folder != "Книга 1" || !slices.Equal(got.Choice.Excluded, []string{"Книга 1/Черновики"}) ||
		!slices.Equal(got.Choice.Nested, []string{"Книга 1/Персонажи"}) {
		t.Fatalf("the loaded book = %+v", got)
	}
	if got := byBook[fromFile]; got.Folder != "Книга 2" || len(got.Choice.Excluded) != 0 || len(got.Choice.Nested) != 0 {
		t.Fatalf("the book linked without a load = %+v", got)
	}
}
