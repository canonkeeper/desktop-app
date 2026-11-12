package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/domain/quota"
	memorydomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	indexingrepo "canonkeeper/backend/internal/repository/postgres/indexing"
	sagarepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	indexingservice "canonkeeper/backend/internal/services/indexing"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
)

// The wave of the preparation of a saga on the real path of a book (specs/2026-11-09-first-session.md, FS1): the import
// of a new book opens it, a book loaded while the saga is in work joins it, and the cards Job that leaves the text the
// site reads ready, with no new book still to import, closes it once — not between the volumes of a queue and not while
// the text of the site is still indexed. «Обновить из файла» does not open a wave; the next new book does; an open wave
// nothing works on starts again with the next book.
func TestTheWaveOfTheSagaOpensWithANewBookAndClosesOnce(t *testing.T) {
	fixture := newReadyWaveFixture(t, "c51")
	ctx, db, projectID, ownerID, editorID := fixture.ctx, fixture.db, fixture.projectID, fixture.ownerID, fixture.editorID
	waves := fixture.waves

	// A queue of two volumes: the first new book opens the wave at the time the queue was started.
	queue := startReadyImportQueue(t, fixture.store, projectID, ownerID, fixture.ownerProof, "wave-queue", 1, 2)
	queueStartedAt := fixture.jobCreatedAt(queue.Items[0].ImportJobID)
	if wave := readyWaveRowForTest(t, db, projectID); wave.exists {
		t.Fatalf("wave before a book is imported = %+v", wave)
	}
	for position, item := range queue.Items {
		decision, err := fixture.claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		if err != nil || decision.Claim.JobID != item.ImportJobID {
			t.Fatalf("import claim %d = %+v, %v; want item %s", position+1, decision.Claim, err, item.ImportJobID)
		}
		fixture.finalize(decision.Claim, ownerID, item.StorageKey, item.SourceFileName, fmt.Sprintf("Том %d", position+1),
			library.ImportModeNewBook, "", position+1, fmt.Sprintf("Том %d, начало", position+1), fmt.Sprintf("Том %d, конец", position+1))
		fixture.assertOpen(fmt.Sprintf("volume %d imported", position+1), ownerID, queueStartedAt)
		if status := fixture.prepare(governance.SourceViewDraft); status != jobs.StatusSucceeded {
			t.Fatalf("draft cards of volume %d = %s", position+1, status)
		}
		// The text the site reads is not indexed yet: the wave stays open.
		fixture.assertOpen(fmt.Sprintf("draft cards of volume %d", position+1), ownerID, queueStartedAt)
		if status := fixture.prepare(governance.SourceViewPublished); status != jobs.StatusSucceeded {
			t.Fatalf("published cards of volume %d = %s", position+1, status)
		}
		if position == 0 {
			// Every chapter the site reads has its card, but the second volume of the queue still waits.
			fixture.assertOpen("between the volumes", ownerID, queueStartedAt)
		}
	}
	first := readyWaveRowForTest(t, db, projectID)
	if first.readyAt == nil || first.books != 2 || first.chapters != 4 || first.startedBy != ownerID || !first.startedAt.Equal(queueStartedAt) {
		t.Fatalf("wave after the queue = %+v, want closed with 2 books and 4 chapters", first)
	}
	if len(waves.closed) != 1 {
		t.Fatalf("the wave was closed %d times, want once", len(waves.closed))
	}
	closed := waves.closed[0]
	if closed.ProjectID != projectID || closed.ProjectName != "Import Queue Project" || closed.RecipientUserID != ownerID ||
		closed.Books != 2 || closed.Chapters != 4 || !closed.StartedAt.Equal(queueStartedAt) || !closed.ReadyAt.Equal(*first.readyAt) {
		t.Fatalf("closed wave = %+v, row %+v", closed, first)
	}
	fixture.announce.Execute(ctx, projectID)
	if again := readyWaveRowForTest(t, db, projectID); len(waves.closed) != 1 || !again.readyAt.Equal(*first.readyAt) {
		t.Fatalf("a closed wave was closed again: %d times, row %+v", len(waves.closed), again)
	}

	// «Обновить из файла» with one more chapter is not a new book: the closed wave stays as it was.
	var firstBookID string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM books WHERE project_id = $1 AND "order" = 1 AND deleted_at IS NULL`, projectID).Scan(&firstBookID); err != nil {
		t.Fatal(err)
	}
	fixture.load(testUUID(51_005), ownerID, "wave-update", "Том 1", library.ImportModeUpdateBook, firstBookID,
		"Том 1, начало", "Том 1, конец", "Том 1, эпилог")
	fixture.prepare(governance.SourceViewDraft)
	fixture.prepare(governance.SourceViewPublished)
	updated := readyWaveRowForTest(t, db, projectID)
	if len(waves.closed) != 1 || updated.readyAt == nil || !updated.readyAt.Equal(*first.readyAt) || updated.books != 2 || updated.chapters != 4 {
		t.Fatalf("wave after «Обновить из файла» = %+v, closed %d times; want %+v untouched", updated, len(waves.closed), first)
	}

	// A new book after the closed wave opens the next one, of the author who loaded it.
	third := testUUID(51_006)
	fixture.load(third, editorID, "wave-third", "Том 3", library.ImportModeNewBook, "", "Том 3, начало")
	fixture.assertOpen("a new book after a closed wave", editorID, fixture.jobCreatedAt(third))
	// A book loaded while the saga is in work joins the wave: its start and its author stay.
	fixture.load(testUUID(51_007), ownerID, "wave-fourth", "Том 4", library.ImportModeNewBook, "", "Том 4, начало")
	fixture.assertOpen("a book joining the wave", editorID, fixture.jobCreatedAt(third))

	// The cards fail: the wave stays open with nothing of the saga in work, and the next book starts it again.
	fixture.model.fail.Store(true)
	if status := fixture.prepare(governance.SourceViewDraft); status != jobs.StatusFailed {
		t.Fatalf("draft cards with a failing model = %s", status)
	}
	if status := fixture.prepare(governance.SourceViewPublished); status != jobs.StatusFailed {
		t.Fatalf("published cards with a failing model = %s", status)
	}
	fixture.assertOpen("failed cards", editorID, fixture.jobCreatedAt(third))
	fixture.model.fail.Store(false)
	fifth := testUUID(51_008)
	fixture.load(fifth, ownerID, "wave-fifth", "Том 5", library.ImportModeNewBook, "", "Том 5, начало")
	fixture.assertOpen("a new book after an idle wave", ownerID, fixture.jobCreatedAt(fifth))
	fixture.prepare(governance.SourceViewDraft)
	fixture.prepare(governance.SourceViewPublished)
	last := readyWaveRowForTest(t, db, projectID)
	if last.readyAt == nil || last.books != 5 || last.chapters != 8 || len(waves.closed) != 2 || waves.closed[1].RecipientUserID != ownerID {
		t.Fatalf("wave after the fifth book = %+v, closed %d times; want closed with 5 books and 8 chapters", last, len(waves.closed))
	}
}

// The closed wave names whom to write to — the author who loaded the books, or the owner of the saga once that author
// has left it (FS2.2). «Обновить из файла» of another book does not hold the wave of a new one, and a wave whose new
// book was deleted while it was prepared is forgotten: nobody is told (FS1.5).
func TestTheWaveOfTheSagaIsToldToTheOwnerAndForgetsADeletedBook(t *testing.T) {
	fixture := newReadyWaveFixture(t, "c52")
	ctx, db, projectID, ownerID, editorID := fixture.ctx, fixture.db, fixture.projectID, fixture.ownerID, fixture.editorID

	first := testUUID(52_005)
	book := fixture.load(first, editorID, "left-first", "Том 1", library.ImportModeNewBook, "", "Том 1, начало", "Том 1, конец")
	fixture.prepare(governance.SourceViewDraft)
	// The author leaves the saga. The store of the members imports this package, so the test writes what it writes.
	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, fixture.editorMembershipID); err != nil {
		t.Fatal(err)
	}
	// «Обновить из файла» waits for its turn: its text is already in the saga, so the wave closes without it.
	fixture.stage(testUUID(52_006), ownerID, "left-update", library.ImportModeUpdateBook, book.BookID)
	fixture.prepare(governance.SourceViewPublished)
	if len(fixture.waves.closed) != 1 || fixture.waves.closed[0].RecipientUserID != ownerID ||
		fixture.waves.closed[0].Books != 1 || fixture.waves.closed[0].Chapters != 2 {
		t.Fatalf("closed waves = %+v, want one of the owner with 1 book and 2 chapters", fixture.waves.closed)
	}
	if row := readyWaveRowForTest(t, db, projectID); row.startedBy != editorID || row.readyAt == nil {
		t.Fatalf("wave of the author who left = %+v", row)
	}
	fixture.importStaged(testUUID(52_006), ownerID, "left-update", "Том 1", library.ImportModeUpdateBook, book.BookID,
		"Том 1, начало", "Том 1, конец", "Том 1, эпилог")
	fixture.settle()
	if len(fixture.waves.closed) != 1 {
		t.Fatalf("«Обновить из файла» closed a wave: %+v", fixture.waves.closed)
	}

	second := testUUID(52_007)
	deleted := fixture.load(second, ownerID, "deleted-second", "Том 2", library.ImportModeNewBook, "", "Том 2, начало")
	fixture.assertOpen("a new book", ownerID, fixture.jobCreatedAt(second))
	fixture.prepare(governance.SourceViewDraft)
	if _, err := fixture.store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: ownerID, ProjectID: projectID, BookID: deleted.BookID, TombstoneID: testUUID(52_008), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	fixture.settle()
	if row := readyWaveRowForTest(t, db, projectID); row.exists || len(fixture.waves.closed) != 1 {
		t.Fatalf("wave of a deleted book = %+v, closed %d times; want it forgotten", row, len(fixture.waves.closed))
	}
}

// The status of the saga follows its wave (FS4.1, FS5): while the wave is open and the text of the site is prepared it
// carries the estimate — once three cards make a pace and no new book is still to be imported — and the promise of the
// letter to whom the wave will be told to, once the wave is two minutes old; a closed wave is told with what is read.
func TestTheStatusOfTheSagaFollowsItsWave(t *testing.T) {
	fixture := newReadyWaveFixture(t, "c53")
	ctx, db, projectID, ownerID, editorID := fixture.ctx, fixture.db, fixture.projectID, fixture.ownerID, fixture.editorID
	statuses := indexingrepo.New(db)
	status := func(actorID string) canon.IndexingStatusResponse {
		t.Helper()
		response, err := statuses.GetStatus(ctx, indexingservice.GetStatusInput{ActorUserID: actorID, ProjectID: projectID})
		if err != nil {
			t.Fatalf("GetStatus() error = %v", err)
		}
		return response
	}
	if before := status(ownerID); before.Preparation != nil || before.SagaReady != nil {
		t.Fatalf("a saga without a wave = %+v, %+v", before.Preparation, before.SagaReady)
	}

	fixture.load(testUUID(53_005), ownerID, "status-first", "Том 1", library.ImportModeNewBook, "", "Том 1, начало", "Том 1, середина", "Том 1, конец")
	fresh := status(ownerID)
	// Three chapters are few: their usual time is said before any card is written, a minute a chapter.
	if fresh.Preparation == nil || fresh.Preparation.RemainingSeconds == nil || *fresh.Preparation.RemainingSeconds != 180 ||
		fresh.Preparation.LetterPromised || fresh.SagaReady != nil {
		t.Fatalf("a wave that has just opened = %+v, %+v; want the usual time of three chapters and no promise", fresh.Preparation, fresh.SagaReady)
	}

	// The wave began three minutes ago: there is no way to wait for it here, so the test moves its start.
	if _, err := db.Pool.Exec(ctx, `UPDATE saga_ready_waves SET started_at = started_at - interval '3 minutes' WHERE project_id = $1`, projectID); err != nil {
		t.Fatal(err)
	}
	if aged := status(ownerID); aged.Preparation == nil || !aged.Preparation.LetterPromised ||
		aged.Preparation.RemainingSeconds == nil || *aged.Preparation.RemainingSeconds != 180 {
		t.Fatalf("the author of a wave of three minutes = %+v; want the promise and the usual time before the cards", aged.Preparation)
	}
	if other := status(editorID); other.Preparation == nil || other.Preparation.LetterPromised {
		t.Fatalf("another member of the saga = %+v; want a preparation without the promise", other.Preparation)
	}

	// The cards of the three chapters are written with the other text; the text of the site is still indexed.
	if cards := fixture.prepare(governance.SourceViewDraft); cards != jobs.StatusSucceeded {
		t.Fatalf("draft cards = %s", cards)
	}
	paced := status(ownerID)
	if paced.Preparation == nil || paced.Preparation.RemainingSeconds == nil ||
		*paced.Preparation.RemainingSeconds < 180 || *paced.Preparation.RemainingSeconds > 300 {
		t.Fatalf("three cards in three minutes with three chapters left = %+v; want about three minutes", paced.Preparation)
	}
	// «Обновить из файла» that waits does not hide the number; a new book that waits does: its chapters are not counted.
	var firstBookID string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID).Scan(&firstBookID); err != nil {
		t.Fatal(err)
	}
	fixture.stage(testUUID(53_006), ownerID, "status-update", library.ImportModeUpdateBook, firstBookID)
	if updating := status(ownerID); updating.Preparation == nil || updating.Preparation.RemainingSeconds == nil {
		t.Fatalf("with «Обновить из файла» in line = %+v; want the number kept", updating.Preparation)
	}
	fixture.importStaged(testUUID(53_006), ownerID, "status-update", "Том 1", library.ImportModeUpdateBook, firstBookID,
		"Том 1, начало", "Том 1, середина", "Том 1, конец")
	fixture.stage(testUUID(53_007), editorID, "status-second", library.ImportModeNewBook, "")
	if importing := status(ownerID); importing.Preparation == nil || importing.Preparation.RemainingSeconds != nil {
		t.Fatalf("with a new book in line = %+v; want no number", importing.Preparation)
	}
	fixture.importStaged(testUUID(53_007), editorID, "status-second", "Том 2", library.ImportModeNewBook, "", "Том 2, начало")
	fixture.settle()

	row := readyWaveRowForTest(t, db, projectID)
	if row.readyAt == nil || len(fixture.waves.closed) != 1 || fixture.waves.closed[0].RecipientUserID != ownerID || !fixture.waves.closed[0].Long() {
		t.Fatalf("wave = %+v, closed %+v; want one long wave of the owner", row, fixture.waves.closed)
	}
	for _, reader := range []string{ownerID, editorID} {
		ready := status(reader)
		if ready.Preparation != nil || ready.SagaReady == nil || !ready.SagaReady.At.Equal(*row.readyAt) ||
			ready.SagaReady.Books != 2 || ready.SagaReady.Chapters != 4 {
			t.Fatalf("the status of a ready saga = %+v, %+v; want it told with 2 books and 4 chapters", ready.Preparation, ready.SagaReady)
		}
	}
}

// specs/2026-11-10-ux3-saga-state.md SS1.1 on the real path of two books: the status counts the reading of the saga by
// the counters its wave closes on — the chapters with their retelling, the books read, the first book not read, whether
// the search finds a chapter — while a file is parsed no book is current, and a ready saga has no preparation.
func TestTheStatusCountsTheReadingOfTwoBooks(t *testing.T) {
	fixture := newReadyWaveFixture(t, "c54")
	ctx, db, projectID, ownerID := fixture.ctx, fixture.db, fixture.projectID, fixture.ownerID
	statuses := indexingrepo.New(db)
	reading := func(step string) *canon.IndexingPreparation {
		t.Helper()
		response, err := statuses.GetStatus(ctx, indexingservice.GetStatusInput{ActorUserID: ownerID, ProjectID: projectID})
		if err != nil {
			t.Fatalf("%s: GetStatus() error = %v", step, err)
		}
		return response.Preparation
	}
	current := func(preparation *canon.IndexingPreparation) string {
		if preparation == nil || preparation.CurrentBookID == nil {
			return ""
		}
		return *preparation.CurrentBookID
	}

	first := fixture.load(testUUID(54_001), ownerID, "count-first", "Том 1", library.ImportModeNewBook, "", "Том 1, начало", "Том 1, конец")
	if fresh := reading("the first book imported"); fresh == nil || fresh.Askable || fresh.ChaptersRead != 0 || fresh.ChaptersTotal != 2 ||
		fresh.BooksRead != 0 || fresh.BooksTotal != 1 || current(fresh) != first.BookID {
		t.Fatalf("a book not searched yet = %+v", fresh)
	}
	// The text the site reads is indexed, its cards are not written yet: the chat may be asked.
	textClaim, textSubject := claimTextIndexForTest(t, db, projectID, governance.SourceViewPublished)
	indexCurrentTextForTest(t, fixture.indexing, textClaim)
	if err := fixture.indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
		t.Fatal(err)
	}
	if searched := reading("the first book indexed"); searched == nil || !searched.Askable || searched.ChaptersRead != 0 || current(searched) != first.BookID {
		t.Fatalf("a book searched before its retelling = %+v", searched)
	}
	fixture.settle()
	if ready := reading("the first book read"); ready != nil {
		t.Fatalf("a saga read through = %+v; want no preparation", ready)
	}

	// The second book: one of three chapters of the saga is left; the file of a third is still to be parsed.
	second := fixture.load(testUUID(54_002), ownerID, "count-second", "Том 2", library.ImportModeNewBook, "", "Том 2, начало")
	two := reading("the second book imported")
	if two == nil || two.ChaptersRead != 2 || two.ChaptersTotal != 3 || two.BooksRead != 1 || two.BooksTotal != 2 || !two.Askable ||
		current(two) != second.BookID || len(two.Books) != 2 || two.Books[0].BookID != first.BookID || two.Books[0].ChaptersRead != 2 ||
		two.Books[1].BookID != second.BookID || two.Books[1].ChaptersRead != 0 || two.Books[1].ChaptersTotal != 1 {
		t.Fatalf("two books, 2 of 3 chapters read = %+v", two)
	}
	fixture.stage(testUUID(54_003), ownerID, "count-third", library.ImportModeNewBook, "")
	fixture.settle()
	if parsed := reading("the third file staged"); parsed == nil || current(parsed) != "" || parsed.ChaptersRead != 3 || parsed.BooksRead != 2 {
		t.Fatalf("the file of a new book still to be parsed = %+v; want no current book", parsed)
	}
	fixture.importStaged(testUUID(54_003), ownerID, "count-third", "Том 3", library.ImportModeNewBook, "", "Том 3, начало")
	fixture.settle()
	if done := reading("the third book read"); done != nil {
		t.Fatalf("a saga of three books read = %+v; want no preparation", done)
	}

	// A book back from «Недавно удалённые» is read anew without a wave (the pass on TEST 11.11): the same reading.
	if _, err := fixture.store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: ownerID, ProjectID: projectID, BookID: second.BookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	if _, err := fixture.store.RestoreBook(ctx, library.RestoreBookStoreInput{ActorUserID: ownerID, ProjectID: projectID, BookID: second.BookID}); err != nil {
		t.Fatalf("RestoreBook() error = %v", err)
	}
	again := reading("the second book restored")
	if again == nil || current(again) != second.BookID || again.Books[1].ChaptersRead != 0 || again.ChaptersTotal != 4 ||
		again.RemainingSeconds == nil || *again.RemainingSeconds != 60 || again.LetterPromised {
		t.Fatalf("a book restored = %+v; want it read again with the usual time of its one chapter and without the letter of a wave", again)
	}
}

type readyWaveFixture struct {
	t                  *testing.T
	ctx                context.Context
	db                 *storedb.DB
	projectID          string
	ownerID, editorID  string
	editorMembershipID string
	ownerProof         auth.SessionWriteProof
	editorProof        auth.SessionWriteProof
	store              *Store
	claims             *workerClaimAuthorityForLibraryTest
	indexing           *indexingstore.Store
	model              *waveCardModel
	waves              *recordedWaves
	announce           *indexingservice.AnnounceReady
	cards              *memoryservice.BuildCards
}

// newReadyWaveFixture is a saga of an owner and an editor with the real stores of the import, the text index and the
// cards, whose cards Job announces the saga through the real repository of the waves.
func newReadyWaveFixture(t *testing.T, suffix string) *readyWaveFixture {
	t.Helper()
	db, projectID, ownerID, ownerProof := newImportQueueStartFixture(t, suffix)
	fixture := &readyWaveFixture{
		t: t, ctx: context.Background(), db: db, projectID: projectID, ownerID: ownerID, ownerProof: ownerProof,
		editorID: "00000000-0000-4000-8000-000000003" + suffix, editorMembershipID: "00000000-0000-4000-8000-000000004" + suffix,
		store: New(db), claims: newWorkerClaimAuthorityForTest(db), indexing: indexingstore.New(db),
		model: &waveCardModel{}, waves: &recordedWaves{inner: indexingrepo.New(db)},
	}
	fixture.editorProof = insertProjectStoreUserWithSessionForTest(t, db, fixture.editorID, "wave-editor-"+suffix+"@example.test")
	insertProjectStoreMembershipForTest(t, db, fixture.editorMembershipID, projectID, fixture.editorID, projects.RoleEditor, time.Now().UTC(), false)
	fixture.announce = indexingservice.NewAnnounceReady(fixture.waves, nil, func(_ string, stage string, err error) {
		t.Errorf("announce %s: %v", stage, err)
	})
	saga := sagarepo.New(db)
	fixture.cards = memoryservice.NewBuildCards(saga, saga, fixture.model, nil, "test-model", 2, nil, nil).WithReady(fixture.announce.Execute)
	return fixture
}

func (fixture *readyWaveFixture) finalize(
	claim jobs.WorkerClaim, actorID, key, file, title string, mode library.ImportMode, bookID string, order int, chapters ...string,
) library.FinalizedImport {
	t := fixture.t
	t.Helper()
	if err := fixture.store.MarkImportParsingForClaim(fixture.ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkImportSavingForClaim(fixture.ctx, claim, len(chapters)); err != nil {
		t.Fatal(err)
	}
	input := library.FinalizeImportStoreInput{
		JobID: claim.JobID, ProjectID: fixture.projectID, ActorUserID: actorID, StorageKey: key,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, ExpectedBookOrder: order, BookTitle: title, BookID: bookID,
		ImportMode: mode, InitialPublication: library.InitialPublicationPublishImported, SourceFileName: file,
	}
	for _, text := range chapters {
		input.Chapters = append(input.Chapters, library.FinalizeImportChapterInput{
			Title: "Глава. " + text, Paragraphs: []library.ParagraphInput{{Markdown: text}},
		})
	}
	finalized, err := fixture.store.FinalizeImport(fixture.ctx, input)
	if err != nil {
		t.Fatalf("FinalizeImport(%s) error = %v", key, err)
	}
	return finalized
}

// stage puts the file of one book loaded from the shelf, outside a queue, in line for the import.
func (fixture *readyWaveFixture) stage(jobID, actorID, key string, mode library.ImportMode, bookID string) {
	t := fixture.t
	t.Helper()
	proof := fixture.ownerProof
	if actorID == fixture.editorID {
		proof = fixture.editorProof
	}
	payload := []byte("<FictionBook>" + key + "</FictionBook>")
	digest := sha256.Sum256(payload)
	if _, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: actorID, SessionWriteProof: proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	}); err != nil {
		t.Fatalf("CreateImportStagingForActor(%s) error = %v", key, err)
	}
}

// load imports one book loaded from the shelf.
func (fixture *readyWaveFixture) load(jobID, actorID, key, title string, mode library.ImportMode, bookID string, chapters ...string) library.FinalizedImport {
	t := fixture.t
	t.Helper()
	fixture.stage(jobID, actorID, key, mode, bookID)
	return fixture.importStaged(jobID, actorID, key, title, mode, bookID, chapters...)
}

// importStaged imports the staged file that is next in line.
func (fixture *readyWaveFixture) importStaged(jobID, actorID, key, title string, mode library.ImportMode, bookID string, chapters ...string) library.FinalizedImport {
	t := fixture.t
	t.Helper()
	decision, err := fixture.claims.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != jobID {
		t.Fatalf("import claim %s = %+v, %v", key, decision.Claim, err)
	}
	return fixture.finalize(decision.Claim, actorID, key, key+".fb2", title, mode, bookID, 0, chapters...)
}

// prepare runs the text index of one view and the cards Job it queues.
func (fixture *readyWaveFixture) prepare(view governance.SourceView) jobs.Status {
	t := fixture.t
	t.Helper()
	textClaim, textSubject := claimTextIndexForTest(t, fixture.db, fixture.projectID, view)
	indexCurrentTextForTest(t, fixture.indexing, textClaim)
	if err := fixture.indexing.CompleteCanonTextIndexForClaim(fixture.ctx, textClaim, textSubject); err != nil {
		t.Fatal(err)
	}
	status, ran := fixture.card()
	if !ran {
		t.Fatalf("no cards Job after the text index of the %s text", view)
	}
	return status
}

// card runs the next cards Job in line, if there is one.
func (fixture *readyWaveFixture) card() (jobs.Status, bool) {
	t := fixture.t
	t.Helper()
	decision, err := fixture.claims.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindChapterCards})
	if errors.Is(err, jobs.ErrNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatalf("cards claim: %v", err)
	}
	var view string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT subject_json->>'sourceView' FROM jobs WHERE id = $1`, decision.Claim.JobID).Scan(&view); err != nil {
		t.Fatal(err)
	}
	subject := jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: fixture.projectID, SourceView: governance.SourceView(view)}
	status, err := fixture.cards.RunForClaim(fixture.ctx, decision.Claim, subject, true)
	if err != nil {
		t.Fatalf("cards Job of the %s text: %v", view, err)
	}
	return status, true
}

// settle runs the text index and the cards Jobs that wait, until none does.
func (fixture *readyWaveFixture) settle() {
	t := fixture.t
	t.Helper()
	for range 6 {
		worked := false
		for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
			var waits bool
			if err := fixture.db.Pool.QueryRow(fixture.ctx, `
				SELECT EXISTS (
				  SELECT 1 FROM jobs
				  WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued' AND subject_json->>'sourceView' = $2
				)
			`, fixture.projectID, string(view)).Scan(&waits); err != nil {
				t.Fatal(err)
			}
			if !waits {
				continue
			}
			textClaim, textSubject := claimTextIndexForTest(t, fixture.db, fixture.projectID, view)
			indexCurrentTextForTest(t, fixture.indexing, textClaim)
			if err := fixture.indexing.CompleteCanonTextIndexForClaim(fixture.ctx, textClaim, textSubject); err != nil {
				t.Fatal(err)
			}
			worked = true
		}
		if _, ran := fixture.card(); ran {
			worked = true
		}
		if !worked {
			return
		}
	}
	t.Fatal("the text index and the cards never settled")
}

func (fixture *readyWaveFixture) jobCreatedAt(jobID string) time.Time {
	fixture.t.Helper()
	var created time.Time
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT created_at FROM jobs WHERE id = $1`, jobID).Scan(&created); err != nil {
		fixture.t.Fatal(err)
	}
	return created
}

func (fixture *readyWaveFixture) assertOpen(step, startedBy string, startedAt time.Time) {
	fixture.t.Helper()
	wave := readyWaveRowForTest(fixture.t, fixture.db, fixture.projectID)
	if !wave.exists || wave.startedBy != startedBy || !wave.startedAt.Equal(startedAt) || wave.readyAt != nil {
		fixture.t.Fatalf("%s: wave = %+v, want an open one of %s started at %s", step, wave, startedBy, startedAt)
	}
}

type readyWaveRow struct {
	exists          bool
	startedAt       time.Time
	startedBy       string
	readyAt         *time.Time
	books, chapters int
}

func readyWaveRowForTest(t *testing.T, db *storedb.DB, projectID string) readyWaveRow {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
		SELECT started_at, COALESCE(started_by_user_id::text, ''), ready_at, COALESCE(ready_books, 0), COALESCE(ready_chapters, 0)
		FROM saga_ready_waves WHERE project_id = $1
	`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var row readyWaveRow
	for rows.Next() {
		row.exists = true
		if err := rows.Scan(&row.startedAt, &row.startedBy, &row.readyAt, &row.books, &row.chapters); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return row
}

// recordedWaves is the repository of the waves that remembers what it closed.
type recordedWaves struct {
	inner  indexingservice.ReadyWaves
	closed []indexingdomain.ReadyWave
}

func (waves *recordedWaves) CloseWave(ctx context.Context, projectID string) (indexingdomain.ReadyWave, bool, error) {
	wave, closed, err := waves.inner.CloseWave(ctx, projectID)
	if closed {
		waves.closed = append(waves.closed, wave)
	}
	return wave, closed, err
}

// waveCardModel writes a card of every chapter until it is told to fail.
type waveCardModel struct{ fail atomic.Bool }

func (model *waveCardModel) Complete(_ context.Context, instructions, _ string) (string, quota.ModelUsage, error) {
	if model.fail.Load() {
		return "", quota.ModelUsage{}, errors.New("model is down")
	}
	if instructions == memorydomain.NameInstructions {
		return `{"persons":[]}`, quota.ModelUsage{Calls: 1}, nil
	}
	return `{"summary": "Пересказ главы"}`, quota.ModelUsage{Calls: 1}, nil
}
