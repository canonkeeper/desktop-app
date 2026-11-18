package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	quotastore "canonkeeper/backend/internal/store/quota"
)

// A book is loaded once (specs/2026-11-18-free-tier.md FT1): the writer's own book_reloads 0 stands for «Бесплатно» in
// the open core, which has no plan.

// stagedImport is an upload of a file staged and claimed, its Job saving: what the worker finalizes.
type stagedImport struct {
	key, jobID, projectID, actorID, bookID string
	mode                                   library.ImportMode
	claim                                  jobs.WorkerClaim
}

// stageIn stages an upload of a file by the actor into the saga; the error is the staging's.
func (fixture *quotaImportFixture) stageIn(projectID, actorID string, proof auth.SessionWriteProof, key, jobID string, mode library.ImportMode, bookID string) (stagedImport, error) {
	fixture.t.Helper()
	payload := []byte("<FictionBook>" + key + "</FictionBook>")
	digest := sha256.Sum256(payload)
	_, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: actorID, SessionWriteProof: proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	})
	return stagedImport{key: key, jobID: jobID, projectID: projectID, actorID: actorID, bookID: bookID, mode: mode}, err
}

// claimSaving claims the staged upload and brings its Job to saving.
func (fixture *quotaImportFixture) claimSaving(staged stagedImport) stagedImport {
	fixture.t.Helper()
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != staged.jobID {
		fixture.t.Fatalf("claim %s = %+v, %v", staged.key, decision.Claim, err)
	}
	if err := fixture.store.MarkImportParsingForClaim(fixture.ctx, decision.Claim); err != nil {
		fixture.t.Fatal(err)
	}
	if err := fixture.store.MarkImportSavingForClaim(fixture.ctx, decision.Claim, 1); err != nil {
		fixture.t.Fatal(err)
	}
	staged.claim = decision.Claim
	return staged
}

// finalizeStaged writes the book of a claimed upload as the worker does.
func (fixture *quotaImportFixture) finalizeStaged(staged stagedImport) (library.FinalizedImport, error) {
	text := strings.Repeat("я", 100)
	return fixture.store.FinalizeImport(fixture.ctx, library.FinalizeImportStoreInput{
		JobID: staged.jobID, ProjectID: staged.projectID, ActorUserID: staged.actorID, StorageKey: staged.key,
		ExpectedClaimIncarnation: staged.claim.ClaimIncarnation, BookTitle: "Loads " + staged.key, BookID: staged.bookID,
		ImportMode: staged.mode, SourceFileName: staged.key + ".fb2", InitialPublication: library.InitialPublicationPublishImported,
		Chapters: []library.FinalizeImportChapterInput{{
			Title: "Глава", Paragraphs: []library.ParagraphInput{{Markdown: text, Text: &text}},
		}},
	})
}

// loadOnce makes a book of the owner load once, with the books of his account.
func (fixture *quotaImportFixture) loadOnce(books int64) {
	fixture.t.Helper()
	fixture.limit(fixture.ownerID, quota.KeyBookReloads, 0)
	fixture.limit(fixture.ownerID, quota.KeyAccountBooks, books)
}

func (fixture *quotaImportFixture) uploadsToday() int64 {
	return fixture.count(`SELECT COALESCE(sum(imports), 0) FROM writer_usage_days WHERE user_id = $1`, fixture.actorID)
}

// FT1.4: a new version of a book from a file — its preview and its update — is refused before the file is read and the
// upload of the day is spent; when a book loads again both pass, the desktop's «Обновить из файла» and «Обновить из
// Obsidian» among them (the same staging, no plan).
func TestABookLoadedOnceTakesNoNewVersionFromAFile(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	first, err := fixture.importText(library.ImportModeNewBook, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for index, mode := range []library.ImportMode{library.ImportModeUpdatePreview, library.ImportModeUpdateBook} {
		if _, err := fixture.stageIn(fixture.projectID, fixture.actorID, fixture.proof, "again-"+string(mode),
			"00000000-0000-4000-8000-000000000c1"+string(rune('0'+index)), mode, first.BookID); err != nil {
			t.Fatalf("%s when a book loads again: %v", mode, err)
		}
	}
	before := fixture.uploadsToday()
	fixture.loadOnce(1)
	for index, mode := range []library.ImportMode{library.ImportModeUpdatePreview, library.ImportModeUpdateBook} {
		_, err := fixture.stageIn(fixture.projectID, fixture.actorID, fixture.proof, "once-"+string(mode),
			"00000000-0000-4000-8000-000000000c2"+string(rune('0'+index)), mode, first.BookID)
		wantQuotaKey(t, err, quota.KeyBookReloads)
		if code := err.(*quota.Exceeded).Code(); code != "quota.book_reloads" {
			t.Fatalf("the code of the refusal = %s", code)
		}
	}
	if after := fixture.uploadsToday(); after != before {
		t.Fatalf("the refused new versions spent %d uploads of the day", after-before)
	}
	if staged := fixture.count(`SELECT count(*) FROM import_job_requests WHERE request_fingerprint LIKE 'once-%'`); staged != 0 {
		t.Fatalf("the refused new versions left %d requests", staged)
	}
}

// FT1.3: a book without text is refused when a book loads once — no text can come into it later —, whatever the count.
func TestABookLoadedOnceTakesNoBookWithoutText(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	fixture.loadOnce(5)
	_, err := fixture.store.CreateBook(fixture.ctx, library.CreateBookStoreInput{
		ID: "00000000-0000-4000-8000-000000000c31", ProjectID: fixture.projectID, Title: "Пустая", DisplayNumber: "1",
		CreatedAt: time.Now().UTC(),
	})
	wantQuotaKey(t, err, quota.KeyBookReloads)
	if books := fixture.count(`SELECT count(*) FROM books WHERE project_id = $1`, fixture.projectID); books != 0 {
		t.Fatalf("the refused empty book left %d books", books)
	}
}

// FT1.3: a deleted book keeps its place in the books of the account — a new one is refused with book_reloads —, and it
// comes back from the trash; a book that loads again takes the place a deleted one freed, as before.
func TestADeletedBookGivesNoPlaceBackWhenABookLoadsOnce(t *testing.T) {
	for _, once := range []bool{true, false} {
		fixture := newQuotaImportFixture(t, false)
		if once {
			fixture.loadOnce(1)
		} else {
			fixture.limit(fixture.ownerID, quota.KeyAccountBooks, 1)
		}
		first, err := fixture.importText(library.ImportModeNewBook, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
			ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: first.BookID,
			TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		_, err = fixture.stageIn(fixture.projectID, fixture.actorID, fixture.proof, "after-deletion", "00000000-0000-4000-8000-000000000c41",
			library.ImportModeNewBook, "")
		if !once {
			if err != nil {
				t.Fatalf("a new book in the place of a deleted one when a book loads again: %v", err)
			}
			continue
		}
		wantQuotaKey(t, err, quota.KeyBookReloads)
		if _, err := fixture.store.RestoreBook(fixture.ctx, library.RestoreBookStoreInput{
			ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: first.BookID,
		}); err != nil {
			t.Fatalf("the deleted book back from the trash: %v", err)
		}
	}
}

// FT1.3: two uploads at once both pass their staging — neither book is in the account yet —, and the second to be
// written is refused under the quota lock with the same code; the first stays.
func TestTwoUploadsAtOnceLoadOneBookWhenABookLoadsOnce(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	fixture.loadOnce(1)
	fixture.limit(fixture.ownerID, quota.KeyProjectBooks, 5)
	first, err := fixture.stageIn(fixture.projectID, fixture.actorID, fixture.proof, "at-once-1", "00000000-0000-4000-8000-000000000c51", library.ImportModeNewBook, "")
	if err != nil {
		t.Fatal(err)
	}
	first = fixture.claimSaving(first)
	second, err := fixture.stageIn(fixture.projectID, fixture.actorID, fixture.proof, "at-once-2", "00000000-0000-4000-8000-000000000c52", library.ImportModeNewBook, "")
	if err != nil {
		t.Fatal(err)
	}
	second = fixture.claimSaving(second)
	if _, err := fixture.finalizeStaged(first); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.finalizeStaged(second)
	wantQuotaKey(t, err, quota.KeyBookReloads)
	if books := fixture.count(`SELECT count(*) FROM books WHERE project_id = $1`, fixture.projectID); books != 1 {
		t.Fatalf("books after two uploads at once = %d, want 1", books)
	}
	if loads := fixture.count(`SELECT count(*) FROM book_loads WHERE owner_user_id = $1`, fixture.ownerID); loads != 1 {
		t.Fatalf("books of the account after the refused one = %d, want 1: its row went back with it", loads)
	}
}

// FT1.2, specs/2026-10-28-owner-tools-and-counters.md lock order: a real finalization of a new book in saga A writes the
// book of the owner before it takes his quota lock, while the owner uploads into saga B — the quota lock, then his day
// row. The book counts in a journal of inserts, not in a row of a day, so neither waits for the other in a cycle.
func TestFinishingANewBookAndAnUploadInAnotherSagaDoNotDeadlock(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	fixture.loadOnce(5)
	fixture.limit(fixture.ownerID, quota.KeyProjectBooks, 5)
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertImportStoreProjectForTest(t, fixture.db, lockOrderSagaB, fixture.ownerID, "Saga B", now)
	insertProjectStoreMembershipForTest(t, fixture.db, "00000000-0000-4000-8000-000000000a82", lockOrderSagaB, fixture.ownerID, projects.RoleOwner, now, false)
	staged, err := fixture.stageIn(fixture.projectID, fixture.actorID, fixture.proof, "finishing", "00000000-0000-4000-8000-000000000c61", library.ImportModeNewBook, "")
	if err != nil {
		t.Fatal(err)
	}
	staged = fixture.claimSaving(staged)

	upload, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Rollback(context.Background())
	fence := quotastore.Fence{}
	if err := fence.CheckNewBooksInTx(fixture.ctx, upload, lockOrderSagaB, 1, true); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := fixture.finalizeStaged(staged)
		finished <- err
	}()
	fixture.waitForAdvisoryWaiter()
	uploadErr := fence.ReserveDailyInTx(fixture.ctx, upload, fixture.ownerID, quota.KeyImportsPerDay, 1, time.Now())
	if uploadErr == nil {
		uploadErr = upload.Commit(fixture.ctx)
	}
	var finishErr error
	select {
	case finishErr = <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the finalization did not finish")
	}
	if finishErr != nil || uploadErr != nil {
		t.Fatalf("finishing the new book = %v, upload in saga B = %v (deadlock %v)", finishErr, uploadErr,
			deadlocked(finishErr) || deadlocked(uploadErr))
	}
}

// FT1.2: two authors are editors of each other's sagas and the ends of their uploads meet: each finalization writes the
// book of the owner of its saga and ends the Job of its uploader, whose day row the outcome counts. With the books in a
// journal of inserts each holds one day row, so the two never wait for each other in a cycle.
func TestTheEndsOfUploadsOfTwoAuthorsInEachOthersSagasDoNotDeadlock(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	now := time.Now().UTC().Truncate(time.Microsecond)
	const other, otherSaga = "00000000-0000-4000-8000-000000000c70", "00000000-0000-4000-8000-000000000c71"
	ownerProof := fixture.proof
	otherProof := insertProjectStoreUserWithSessionForTest(t, fixture.db, other, "loads-other@example.test")
	insertProjectStoreMembershipForTest(t, fixture.db, "00000000-0000-4000-8000-000000000c72", fixture.projectID, other, projects.RoleEditor, now, false)
	insertImportStoreProjectForTest(t, fixture.db, otherSaga, other, "Saga of the other", now)
	insertProjectStoreMembershipForTest(t, fixture.db, "00000000-0000-4000-8000-000000000c73", otherSaga, other, projects.RoleOwner, now, false)
	insertProjectStoreMembershipForTest(t, fixture.db, "00000000-0000-4000-8000-000000000c74", otherSaga, fixture.ownerID, projects.RoleEditor, now, false)
	for _, writer := range []string{fixture.ownerID, other} {
		fixture.limit(writer, quota.KeyBookReloads, 0)
	}

	intoOwners, err := fixture.stageIn(fixture.projectID, other, otherProof, "into-owners", "00000000-0000-4000-8000-000000000c75", library.ImportModeNewBook, "")
	if err != nil {
		t.Fatal(err)
	}
	intoOwners = fixture.claimSaving(intoOwners)
	intoOthers, err := fixture.stageIn(otherSaga, fixture.ownerID, ownerProof, "into-others", "00000000-0000-4000-8000-000000000c76", library.ImportModeNewBook, "")
	if err != nil {
		t.Fatal(err)
	}
	intoOthers = fixture.claimSaving(intoOthers)

	// Both wait for the quota locks of the owners of their sagas, held here, with their books written; then both go on at
	// once to end their Jobs.
	hold, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	fence := quotastore.Fence{}
	for _, saga := range []string{fixture.projectID, otherSaga} {
		if err := fence.CheckOwnerSourcesInTx(fixture.ctx, hold, saga, quota.MeasureCharacters); err != nil {
			t.Fatal(err)
		}
	}
	finished := make(chan error, 2)
	for _, staged := range []stagedImport{intoOwners, intoOthers} {
		go func() {
			_, err := fixture.finalizeStaged(staged)
			finished <- err
		}()
	}
	fixture.waitForAdvisoryWaiters(2)
	if err := hold.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatalf("an end of an upload = %v (deadlock %v)", err, deadlocked(err))
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the ends of the uploads did not finish")
		}
	}
	for _, writer := range []string{fixture.ownerID, other} {
		if loads := fixture.count(`SELECT count(*) FROM book_loads WHERE owner_user_id = $1`, writer); loads != 1 {
			t.Fatalf("books of %s = %d, want 1", writer, loads)
		}
	}
}

// waitForAdvisoryWaiters returns once so many sessions of the test database wait for an advisory lock.
func (fixture *quotaImportFixture) waitForAdvisoryWaiters(want int64) {
	fixture.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		if err := fixture.db.Pool.QueryRow(fixture.ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'
		`).Scan(&waiting); err != nil {
			fixture.t.Fatal(err)
		}
		if waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fixture.t.Fatalf("fewer than %d sessions waited for the quota locks", want)
}
