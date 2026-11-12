package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	trashrepo "canonkeeper/backend/internal/repository/postgres/trash"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
	retentionstore "canonkeeper/backend/internal/store/retention"

	"github.com/jackc/pgx/v5"
)

// revertFixture is a saga of one owner whose books are loaded and updated from files by the import of the product
// (specs/2026-11-12-ux10-book-revert.md).
type revertFixture struct {
	t         *testing.T
	ctx       context.Context
	db        *storedb.DB
	store     *Store
	base      int
	ownerID   string
	projectID string
	proof     auth.SessionWriteProof
	imports   int
}

func newRevertFixture(t *testing.T, base int) *revertFixture {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture := &revertFixture{
		t: t, ctx: context.Background(), db: db, store: New(db), base: base,
		ownerID: testUUID(base + 1), projectID: testUUID(base + 2),
	}
	cleanupLibraryImportStoreRows(t, db, fixture.ownerID, fixture.projectID)
	fixture.proof = insertProjectStoreUserWithSessionForTest(t, db, fixture.ownerID, fmt.Sprintf("revert-%d@example.test", base))
	insertImportStoreProjectForTest(t, db, fixture.projectID, fixture.ownerID, "Сага с обновлением", now)
	insertProjectStoreMembershipForTest(t, db, testUUID(base+3), fixture.projectID, fixture.ownerID, projects.RoleOwner, now, false)
	return fixture
}

// member adds a person of the given role to the saga, or none for a stranger.
func (fixture *revertFixture) member(number int, role projects.Role) string {
	fixture.t.Helper()
	userID := testUUID(fixture.base + number)
	cleanup := func() { _, _ = fixture.db.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }
	cleanup()
	fixture.t.Cleanup(cleanup)
	insertProjectStoreUserForTest(fixture.t, fixture.db, userID, fmt.Sprintf("revert-%d-%d@example.test", fixture.base, number))
	if role != "" {
		insertProjectStoreMembershipForTest(fixture.t, fixture.db, testUUID(fixture.base+number+50), fixture.projectID, userID, role, time.Now().UTC(), false)
	}
	return userID
}

func revertChapter(title string, paragraphs ...string) library.FinalizeImportChapterInput {
	input := library.FinalizeImportChapterInput{Title: title}
	for _, paragraph := range paragraphs {
		input.Paragraphs = append(input.Paragraphs, library.ParagraphInput{Markdown: paragraph})
	}
	return input
}

// stage queues an import of a file into the saga and reports its Job.
func (fixture *revertFixture) stage(mode library.ImportMode, bookID string) (string, string) {
	fixture.t.Helper()
	fixture.imports++
	key := fmt.Sprintf("revert-%d-%d", fixture.base, fixture.imports)
	jobID := testUUID(fixture.base + 100 + fixture.imports)
	payload := []byte("<FictionBook>" + key + "</FictionBook>")
	digest := sha256.Sum256(payload)
	if _, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.ownerID, SessionWriteProof: fixture.proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	}); err != nil {
		fixture.t.Fatalf("CreateImportStagingForActor(%s) error = %v", key, err)
	}
	return jobID, key
}

// load imports a new book or updates one from a file of these chapters.
func (fixture *revertFixture) load(mode library.ImportMode, bookID string, chapters ...library.FinalizeImportChapterInput) (library.FinalizedImport, error) {
	fixture.t.Helper()
	jobID, key := fixture.stage(mode, bookID)
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != jobID {
		fixture.t.Fatalf("claim %s = %+v, %v", key, decision.Claim, err)
	}
	if err := fixture.store.MarkImportParsingForClaim(fixture.ctx, decision.Claim); err != nil {
		fixture.t.Fatal(err)
	}
	if err := fixture.store.MarkImportSavingForClaim(fixture.ctx, decision.Claim, len(chapters)); err != nil {
		fixture.t.Fatal(err)
	}
	return fixture.store.FinalizeImport(fixture.ctx, library.FinalizeImportStoreInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.ownerID, StorageKey: key,
		ExpectedClaimIncarnation: decision.Claim.ClaimIncarnation, BookTitle: "Том первый", BookID: bookID, ImportMode: mode,
		SourceFileName: key + ".fb2", InitialPublication: library.InitialPublicationPublishImported, Chapters: chapters,
	})
}

func (fixture *revertFixture) mustLoad(mode library.ImportMode, bookID string, chapters ...library.FinalizeImportChapterInput) library.FinalizedImport {
	fixture.t.Helper()
	loaded, err := fixture.load(mode, bookID, chapters...)
	if err != nil {
		fixture.t.Fatalf("load %s error = %v", mode, err)
	}
	return loaded
}

type revertChapterRow struct {
	id       string
	title    string
	order    int
	revision int64
	deleted  bool
	text     string
	markdown string
}

// chapters reads every chapter of the book by its id: its row and the text of its current revision.
func (fixture *revertFixture) chapters(bookID string) map[string]revertChapterRow {
	fixture.t.Helper()
	rows, err := fixture.db.Pool.Query(fixture.ctx, `
		SELECT chapter.id::text, chapter.title, chapter."order", chapter.revision, chapter.deleted_at IS NOT NULL,
		       COALESCE(string_agg(paragraph.plain_text, ' | ' ORDER BY paragraph."order"), ''),
		       COALESCE(string_agg(paragraph.markdown, ' | ' ORDER BY paragraph."order"), '')
		FROM chapters AS chapter
		LEFT JOIN chapter_paragraphs AS paragraph
		  ON paragraph.chapter_id = chapter.id AND paragraph.revision = chapter.revision AND paragraph.variant = 'published'
		WHERE chapter.project_id = $1 AND chapter.book_id = $2
		GROUP BY chapter.id
	`, fixture.projectID, bookID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	read, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (revertChapterRow, error) {
		var chapter revertChapterRow
		err := row.Scan(&chapter.id, &chapter.title, &chapter.order, &chapter.revision, &chapter.deleted, &chapter.text, &chapter.markdown)
		return chapter, err
	})
	if err != nil {
		fixture.t.Fatal(err)
	}
	byID := map[string]revertChapterRow{}
	for _, chapter := range read {
		byID[chapter.id] = chapter
	}
	return byID
}

// live lists the titles of the live chapters of the book in their order.
func (fixture *revertFixture) live(bookID string) string {
	fixture.t.Helper()
	var titles string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT COALESCE(string_agg(title, ', ' ORDER BY "order", id), '') FROM chapters
		WHERE project_id = $1 AND book_id = $2 AND deleted_at IS NULL
	`, fixture.projectID, bookID).Scan(&titles); err != nil {
		fixture.t.Fatal(err)
	}
	return titles
}

func (fixture *revertFixture) count(query string, args ...any) int {
	fixture.t.Helper()
	var value int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, query, args...).Scan(&value); err != nil {
		fixture.t.Fatal(err)
	}
	return value
}

func (fixture *revertFixture) actor() domainlibrary.Actor {
	return domainlibrary.Actor{UserID: fixture.ownerID}
}

func (fixture *revertFixture) preview(bookID string) domainlibrary.BookRevertPreview {
	fixture.t.Helper()
	preview, err := fixture.store.PreviewBookUpdateRevert(fixture.ctx, fixture.actor(), bookID)
	if err != nil {
		fixture.t.Fatalf("PreviewBookUpdateRevert() error = %v", err)
	}
	return preview
}

func titlesOf(chapters []domainlibrary.RevertChapter) string {
	titles := make([]string, len(chapters))
	for index, chapter := range chapters {
		titles[index] = chapter.Title
		if chapter.PreviousTitle != "" {
			titles[index] += " → " + chapter.PreviousTitle
		}
		if chapter.Stay != "" {
			titles[index] += " (" + string(chapter.Stay) + ")"
		}
	}
	return strings.Join(titles, ", ")
}

func titleIDs(chapters map[string]revertChapterRow) map[string]string {
	ids := map[string]string{}
	for id, chapter := range chapters {
		ids[chapter.title] = id
	}
	return ids
}

// The book before every update of these tests: five chapters, the first of them without italic.
func (fixture *revertFixture) firstBook() library.FinalizedImport {
	return fixture.mustLoad(library.ImportModeNewBook, "",
		revertChapter("Глава 1", "Один."), revertChapter("Глава 2", "Два."), revertChapter("Глава 3", "Три.", "Ещё три."),
		revertChapter("Глава 5", "Пять."), revertChapter("Глава 6", "Шесть, и всё."))
}

// The update of these tests: italic in «Глава 1», other words in «Глава 2», «Глава 3» renamed, «Глава 4» added,
// «Глава 5» removed, other signs in «Глава 6».
func (fixture *revertFixture) updateBook(bookID string) library.FinalizedImport {
	return fixture.mustLoad(library.ImportModeUpdateBook, bookID,
		revertChapter("Глава 1", "*Один.*"), revertChapter("Глава 2", "Два, исправлено."), revertChapter("Глава третья", "Три.", "Ещё три."),
		revertChapter("Глава 4", "Четыре."), revertChapter("Глава 6", "Шесть и всё!"))
}

// BR1.1, BR1.2, BR1.3: the update keeps in its transaction what each chapter was before it — revision, title, place, italic
// and bold of the same words — and what it did; the book tells its last update for 7 days; a new update replaces it.
func TestAnUpdateFromAFileKeepsWhatTheBookWasBeforeIt(t *testing.T) {
	fixture := newRevertFixture(t, 142_000)
	first := fixture.firstBook()
	before := titleIDs(fixture.chapters(first.BookID))
	update := fixture.updateBook(first.BookID)
	after := titleIDs(fixture.chapters(first.BookID))

	rows, err := fixture.db.Pool.Query(fixture.ctx, `
		SELECT chapter_id::text, change, revision, COALESCE(previous_revision, 0), COALESCE(previous_title, ''),
		       COALESCE(previous_order, 0), COALESCE(array_to_string(previous_markdown, '|'), '')
		FROM book_update_snapshot_chapters WHERE book_id = $1
	`, first.BookID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]string{}
	for rows.Next() {
		var id, change, title, markdown string
		var revision, previousRevision int64
		var order int
		if err := rows.Scan(&id, &change, &revision, &previousRevision, &title, &order, &markdown); err != nil {
			t.Fatal(err)
		}
		snapshot[id] = fmt.Sprintf("%s r%d←r%d %q #%d %s", change, revision, previousRevision, title, order, markdown)
	}
	rows.Close()
	want := map[string]string{
		before["Глава 1"]: `placed r1←r1 "Глава 1" #1 Один.`,
		before["Глава 2"]: `words r2←r1 "Глава 2" #2 `,
		before["Глава 3"]: `placed r1←r1 "Глава 3" #3 `,
		after["Глава 4"]:  `added r1←r0 "" #0 `,
		before["Глава 5"]: `removed r1←r1 "Глава 5" #4 `,
		before["Глава 6"]: `punctuation r2←r1 "Глава 6" #5 `,
	}
	if len(snapshot) != len(want) {
		t.Fatalf("snapshot = %v, want %v", snapshot, want)
	}
	for id, row := range want {
		if snapshot[id] != row {
			t.Fatalf("snapshot of %s = %s, want %s", id, snapshot[id], row)
		}
	}
	var jobID string
	var term time.Duration
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job_id::text, (EXTRACT(EPOCH FROM purge_after - updated_at) * 1000000)::bigint FROM book_update_snapshots WHERE book_id = $1
	`, first.BookID).Scan(&jobID, &term); err != nil {
		t.Fatal(err)
	}
	if jobID != testUUID(142_000+100+2) || term != 7*24*time.Hour/time.Microsecond {
		t.Fatalf("snapshot head = job %s, term %v", jobID, term)
	}
	_ = update

	// BR1.3: the book tells its last update — from any device, by the server.
	books, err := fixture.store.ListBooks(fixture.ctx, library.ListBooksStoreInput{ProjectID: fixture.projectID, Limit: 10})
	if err != nil || len(books) != 1 || books[0].Update == nil {
		t.Fatalf("ListBooks() = %+v, %v", books, err)
	}
	told := *books[0].Update
	if told.Changed != 4 || told.Added != 1 || told.Removed != 1 || told.JobID != jobID || !told.RevertUntil.Equal(told.At.Add(7*24*time.Hour)) {
		t.Fatalf("Book.update = %+v", told)
	}
	got, err := fixture.store.GetBook(fixture.ctx, fixture.projectID, first.BookID)
	if err != nil || got.Update == nil || *got.Update != told {
		t.Fatalf("GetBook().Update = %+v, %v", got.Update, err)
	}

	// BR1.2: a new update replaces the snapshot of the one before.
	fixture.mustLoad(library.ImportModeUpdateBook, first.BookID,
		revertChapter("Глава 1", "*Один.*"), revertChapter("Глава 2", "Два, исправлено."), revertChapter("Глава третья", "Три.", "Ещё три."),
		revertChapter("Глава 4", "Четыре, и пять."), revertChapter("Глава 6", "Шесть и всё!"))
	if heads := fixture.count(`SELECT count(*) FROM book_update_snapshots WHERE book_id = $1 AND job_id = $2`, first.BookID, testUUID(142_000+100+3)); heads != 1 {
		t.Fatalf("snapshots of the second update = %d, want 1", heads)
	}
	if changed := fixture.count(`SELECT count(*) FROM book_update_snapshot_chapters WHERE book_id = $1 AND change NOT IN ('same')`, first.BookID); changed != 1 {
		t.Fatalf("chapters the second update changed = %d, want 1: «Глава 4»", changed)
	}
}

// BR1.2: the snapshot goes with its book and with its saga in their deletions, and after its term; a read after the
// term does not give it.
func TestTheSnapshotOfAnUpdateGoesWithItsBookSagaAndTerm(t *testing.T) {
	fixture := newRevertFixture(t, 142_200)
	first := fixture.firstBook()
	fixture.updateBook(first.BookID)
	if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: first.BookID,
		TombstoneID: testUUID(142_290), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() = %v", err)
	}
	if left := fixture.count(`SELECT count(*) FROM book_update_snapshots WHERE project_id = $1`, fixture.projectID); left != 0 {
		t.Fatalf("snapshots after the deletion of the book = %d", left)
	}

	second := fixture.firstBook()
	fixture.updateBook(second.BookID)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE book_update_snapshots SET updated_at = updated_at - interval '8 days', purge_after = purge_after - interval '8 days'
		WHERE book_id = $1
	`, second.BookID); err != nil {
		t.Fatal(err)
	}
	if book, err := fixture.store.GetBook(fixture.ctx, fixture.projectID, second.BookID); err != nil || book.Update != nil {
		t.Fatalf("a book after the term of its update = %+v, %v", book.Update, err)
	}
	if _, err := fixture.store.PreviewBookUpdateRevert(fixture.ctx, fixture.actor(), second.BookID); !errors.Is(err, domainlibrary.ErrUpdateGone) {
		t.Fatalf("the preview after the term = %v, want ErrUpdateGone", err)
	}
	removed, err := retentionstore.New(fixture.db).RemoveExpiredBookUpdates(fixture.ctx)
	if err != nil || removed < 1 {
		t.Fatalf("RemoveExpiredBookUpdates() = %d, %v", removed, err)
	}
	if left := fixture.count(`SELECT count(*) FROM book_update_snapshot_chapters WHERE book_id = $1`, second.BookID); left != 0 {
		t.Fatalf("chapters of an expired snapshot = %d", left)
	}

	// The fence of a saga scrubs what its sources leave by the same function the fence of a book does
	// (store/projects FenceProjectDeletion).
	fixture.updateBook(second.BookID)
	var generation int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT lifecycle_generation FROM projects WHERE id = $1`, fixture.projectID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return fixture.store.canon.ScrubCanonicalDerivedSourceInTx(fixture.ctx, tx, governance.DeletionSelectionProject,
			governance.SourceGenerationVector{ProjectID: fixture.projectID, ProjectGeneration: governance.LifecycleGeneration(generation)}, time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	if left := fixture.count(`SELECT count(*) FROM book_update_snapshots WHERE project_id = $1`, fixture.projectID); left != 0 {
		t.Fatalf("snapshots after the deletion of the saga = %d", left)
	}
}

// BR2.1, BR2.2, BR2.3: after an update that changed a chapter, renamed another, removed a third and added a fourth, the
// return brings back the text, the titles, the places, the italic and the removed chapter; the added one waits in
// «Недавно удалённые». The return is not an upload: no import Job, no upload of the day.
func TestARevertBringsTheBookBackAsItWasBeforeTheUpdate(t *testing.T) {
	fixture := newRevertFixture(t, 142_400)
	first := fixture.firstBook()
	before := fixture.chapters(first.BookID)
	ids := titleIDs(before)
	fixture.updateBook(first.BookID)
	added := titleIDs(fixture.chapters(first.BookID))["Глава 4"]
	if got := fixture.live(first.BookID); got != "Глава 1, Глава 2, Глава третья, Глава 4, Глава 6" {
		t.Fatalf("the book after the update = %s", got)
	}

	preview := fixture.preview(first.BookID)
	if got := titlesOf(preview.Text); got != "Глава 2, Глава 6" {
		t.Fatalf("the text that comes back = %s", got)
	}
	if got := titlesOf(preview.Place); got != "Глава 1, Глава третья → Глава 3" {
		t.Fatalf("the titles and places that come back = %s", got)
	}
	if got := titlesOf(preview.Returned); got != "Глава 5" {
		t.Fatalf("the chapters that come back = %s", got)
	}
	if got := titlesOf(preview.Removed); got != "Глава 4" {
		t.Fatalf("the chapters that go = %s", got)
	}
	if len(preview.Kept) != 0 || preview.BookTitle != "Том первый" || len(preview.Fingerprint) != 32 {
		t.Fatalf("preview = %+v", preview)
	}
	// BR3.3: «Недавно удалённые» name the update of the book until its return.
	trash := trashrepo.New(fixture.db)
	if sources, err := trash.ListDeletedSources(fixture.ctx, fixture.ownerID, 100); err != nil || sources.BookUpdates.Total != 1 ||
		sources.BookUpdates.Items[0].BookID != first.BookID || sources.BookUpdates.Items[0].Title != "Том первый" ||
		!sources.BookUpdates.Items[0].RevertUntil.Equal(preview.RevertUntil) {
		t.Fatalf("the updates of books in «Недавно удалённые» = %+v, %v", sources.BookUpdates, err)
	}
	importJobs := fixture.count(`SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'import'`, fixture.projectID)
	uploads := fixture.count(`SELECT COALESCE(sum(imports), 0)::integer FROM writer_usage_days WHERE user_id = $1`, fixture.ownerID)

	if _, err := fixture.store.RevertBookUpdate(fixture.ctx, fixture.actor(), first.BookID, preview.Fingerprint); err != nil {
		t.Fatalf("RevertBookUpdate() error = %v", err)
	}
	if got := fixture.live(first.BookID); got != "Глава 1, Глава 2, Глава 3, Глава 5, Глава 6" {
		t.Fatalf("the book after the return = %s", got)
	}
	reverted := fixture.chapters(first.BookID)
	for title, id := range ids {
		was, now := before[id], reverted[id]
		if now.deleted || now.title != was.title || now.order != was.order || now.text != was.text || now.markdown != was.markdown {
			t.Fatalf("%s after the return = %+v, before the update %+v", title, now, was)
		}
	}
	if reverted[ids["Глава 2"]].revision != 3 || reverted[ids["Глава 1"]].revision != 1 {
		t.Fatalf("revisions after the return: «Глава 2» %d, «Глава 1» %d", reverted[ids["Глава 2"]].revision, reverted[ids["Глава 1"]].revision)
	}
	if !reverted[added].deleted || fixture.count(`
		SELECT count(*) FROM chapters WHERE id = $1 AND purge_after > clock_timestamp() + interval '6 days'
	`, added) != 1 {
		t.Fatalf("the chapter the file added = %+v, want it in «Недавно удалённые» for 7 days", reverted[added])
	}
	if left := fixture.count(`SELECT count(*) FROM book_update_snapshots WHERE book_id = $1`, first.BookID); left != 0 {
		t.Fatalf("snapshots after the return = %d", left)
	}
	if sources, err := trash.ListDeletedSources(fixture.ctx, fixture.ownerID, 100); err != nil || sources.BookUpdates.Total != 0 || sources.Chapters.Total != 1 {
		t.Fatalf("«Недавно удалённые» after the return = %+v, %v; want the chapter of the file and no update", sources, err)
	}
	if got := fixture.count(`SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'import'`, fixture.projectID); got != importJobs {
		t.Fatalf("import Jobs after the return = %d, before %d", got, importJobs)
	}
	if got := fixture.count(`SELECT COALESCE(sum(imports), 0)::integer FROM writer_usage_days WHERE user_id = $1`, fixture.ownerID); got != uploads {
		t.Fatalf("uploads of the day after the return = %d, before %d", got, uploads)
	}
	// The text of both versions is read again by the ordinary path.
	if intents := fixture.count(`
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued'
	`, fixture.projectID); intents == 0 {
		t.Fatal("no text index is queued after the return")
	}
	if _, err := fixture.store.PreviewBookUpdateRevert(fixture.ctx, fixture.actor(), first.BookID); !errors.Is(err, domainlibrary.ErrUpdateGone) {
		t.Fatalf("a second preview = %v, want ErrUpdateGone", err)
	}
}

// BR2.1, BR2.2: a chapter changed after the update stays as it is and the preview names it; a chapter deleted after it
// stays deleted; the rest comes back. A book changed between the preview and the return is refused, nothing changes.
func TestARevertLeavesWhatTheAuthorChangedAfterTheUpdate(t *testing.T) {
	fixture := newRevertFixture(t, 142_600)
	first := fixture.firstBook()
	ids := titleIDs(fixture.chapters(first.BookID))
	fixture.updateBook(first.BookID)
	replace := func(chapterID, title string, revision int64, text string) {
		t.Helper()
		if err := fixture.store.WithTx(fixture.ctx, func(tx pgx.Tx) error {
			_, err := fixture.store.ReplaceChapterTextInTx(fixture.ctx, tx, library.ReplaceChapterTextInput{
				ProjectID: fixture.projectID, BookID: first.BookID, ChapterID: chapterID, ExpectedRevision: revision,
				Title: title, Paragraphs: []string{text},
			})
			return err
		}); err != nil {
			t.Fatalf("ReplaceChapterTextInTx() = %v", err)
		}
	}
	// «Глава 2» edited in «Письмо» after the update, «Глава 6» deleted.
	replace(ids["Глава 2"], "Глава 2", 2, "Два, как решил автор.")
	if _, err := fixture.store.FenceChapterDeletion(fixture.ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: first.BookID, ChapterID: ids["Глава 6"],
		TombstoneID: testUUID(142_690), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	preview := fixture.preview(first.BookID)
	if got := titlesOf(preview.Kept); got != "Глава 2 (edited), Глава 6 (deleted)" && got != "Глава 6 (deleted), Глава 2 (edited)" {
		t.Fatalf("kept = %s", got)
	}
	if len(preview.Text) != 0 {
		t.Fatalf("text = %s, want none", titlesOf(preview.Text))
	}

	// The book changes after the preview: the return refuses and changes nothing.
	replace(ids["Глава 1"], "Глава 1", 1, "Один, и ещё.")
	before := fixture.chapters(first.BookID)
	if _, err := fixture.store.RevertBookUpdate(fixture.ctx, fixture.actor(), first.BookID, preview.Fingerprint); !errors.Is(err, domainlibrary.ErrUpdateChanged) {
		t.Fatalf("a return after a change = %v, want ErrUpdateChanged", err)
	}
	if after := fixture.chapters(first.BookID); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("a refused return changed the book: %v, before %v", after, before)
	}

	again := fixture.preview(first.BookID)
	if got := titlesOf(again.Kept); !strings.Contains(got, "Глава 1 (edited)") {
		t.Fatalf("kept after the second edit = %s", got)
	}
	if _, err := fixture.store.RevertBookUpdate(fixture.ctx, fixture.actor(), first.BookID, again.Fingerprint); err != nil {
		t.Fatalf("RevertBookUpdate() = %v", err)
	}
	reverted := fixture.chapters(first.BookID)
	if chapter := reverted[ids["Глава 2"]]; chapter.text != "Два, как решил автор." || chapter.deleted {
		t.Fatalf("the chapter edited after the update = %+v", chapter)
	}
	if chapter := reverted[ids["Глава 6"]]; !chapter.deleted {
		t.Fatalf("the chapter deleted after the update = %+v", chapter)
	}
	if got := fixture.live(first.BookID); got != "Глава 1, Глава 2, Глава 3, Глава 5" {
		t.Fatalf("the book after the return = %s", got)
	}
}

// BR2.3: text the return brings back over the limit of the tier is refused by the limit, and the book stays as it is.
func TestARevertOverTheLimitOfTheTierChangesNothing(t *testing.T) {
	fixture := newRevertFixture(t, 142_800)
	long := strings.Repeat("я", 500)
	first := fixture.mustLoad(library.ImportModeNewBook, "", revertChapter("Глава 1", long))
	fixture.mustLoad(library.ImportModeUpdateBook, first.BookID, revertChapter("Глава 1", strings.Repeat("ю", 300)))
	if err := fixture.store.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		value := int64(400)
		_, err := quotastore.SetOverridesInTx(fixture.ctx, tx, fixture.ownerID, []quota.OverrideChange{{Key: quota.KeyProjectCharacters, Value: &value}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	preview := fixture.preview(first.BookID)
	_, err := fixture.store.RevertBookUpdate(fixture.ctx, fixture.actor(), first.BookID, preview.Fingerprint)
	wantQuotaKey(t, err, quota.KeyProjectCharacters)
	if got := fixture.chapters(first.BookID); len(got) != 1 {
		t.Fatalf("chapters = %v", got)
	} else {
		for _, chapter := range got {
			if chapter.text != strings.Repeat("ю", 300) || chapter.revision != 2 {
				t.Fatalf("the book after a refused return = %+v", chapter)
			}
		}
	}
	if left := fixture.count(`SELECT count(*) FROM book_update_snapshots WHERE book_id = $1`, first.BookID); left != 1 {
		t.Fatalf("snapshots after a refused return = %d, want the snapshot kept", left)
	}
}

// BR2.4: a reader is refused, a stranger finds nothing, the demo saga takes no text, a book without an update has none to
// return, and a book an update from a file writes now waits.
func TestARevertKeepsItsGuards(t *testing.T) {
	fixture := newRevertFixture(t, 143_000)
	first := fixture.firstBook()
	other := fixture.firstBook()
	fixture.updateBook(first.BookID)
	reader := fixture.member(10, projects.RoleViewer)
	stranger := fixture.member(11, "")
	for _, test := range []struct {
		name  string
		actor string
		book  string
		want  error
	}{
		{"reader", reader, first.BookID, domainlibrary.ErrForbidden},
		{"stranger", stranger, first.BookID, domainlibrary.ErrNotFound},
		{"unknown book", fixture.ownerID, testUUID(143_099), domainlibrary.ErrNotFound},
		{"no update", fixture.ownerID, other.BookID, domainlibrary.ErrUpdateGone},
	} {
		if _, err := fixture.store.PreviewBookUpdateRevert(fixture.ctx, domainlibrary.Actor{UserID: test.actor}, test.book); !errors.Is(err, test.want) {
			t.Errorf("preview by %s = %v, want %v", test.name, err, test.want)
		}
		if _, err := fixture.store.RevertBookUpdate(fixture.ctx, domainlibrary.Actor{UserID: test.actor}, test.book, strings.Repeat("0", 32)); !errors.Is(err, test.want) {
			t.Errorf("return by %s = %v, want %v", test.name, err, test.want)
		}
	}
	if sources, err := trashrepo.New(fixture.db).ListDeletedSources(fixture.ctx, reader, 100); err != nil || sources.BookUpdates.Total != 0 {
		t.Fatalf("a reader's «Недавно удалённые» = %+v, %v", sources.BookUpdates, err)
	}
	fingerprint := fixture.preview(first.BookID).Fingerprint
	// An update of the book waits in the queue: the return waits for it.
	fixture.stage(library.ImportModeUpdateBook, first.BookID)
	if _, err := fixture.store.RevertBookUpdate(fixture.ctx, fixture.actor(), first.BookID, fingerprint); !errors.Is(err, domainlibrary.ErrBookBusy) {
		t.Fatalf("a return while the book is updated = %v, want ErrBookBusy", err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE projects SET demo = true WHERE id = $1`, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RevertBookUpdate(fixture.ctx, fixture.actor(), first.BookID, fingerprint); !errors.Is(err, quota.ErrDemoSaga) {
		t.Fatalf("a return in the demo saga = %v, want ErrDemoSaga", err)
	}
}
