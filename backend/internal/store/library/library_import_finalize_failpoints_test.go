package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

func TestLibraryStoreFinalizePublishImportedRollsBackDualProjectionAtomically(t *testing.T) {
	fixture := newImportFinalizeFailpointFixtureForPublication(t, library.InitialPublicationPublishImported)
	injected := errors.New("stop after published import projection")
	store := New(fixture.db, withImportFinalizeFailpoint(func(point string) error {
		if point == "after_import_published_catalog_mutation" {
			return injected
		}
		return nil
	}))
	if _, err := store.FinalizeImport(fixture.ctx, fixture.input); !errors.Is(err, injected) {
		t.Fatalf("FinalizeImport() error = %v, want injected", err)
	}
	assertImportTreeCounts(t, fixture.db, fixture.projectID, 0, 0, 0)
	assertImportFinalizeRetryableState(t, fixture)
	var sourceRows, textIntents int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT
		  (SELECT count(*) FROM project_source_views WHERE project_id = $1),
		  (SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'canon_text_index')
	`, fixture.projectID).Scan(&sourceRows, &textIntents); err != nil {
		t.Fatal(err)
	}
	if sourceRows != 1 || textIntents != 0 {
		t.Fatalf("rolled-back source rows/intents = %d/%d, want 1/0", sourceRows, textIntents)
	}
}

func TestLibraryStoreFinalizeImportRollsBackAtEveryInternalBoundary(t *testing.T) {
	points := []string{
		"after_import_book_create",
		"after_import_chapter_create",
		"after_import_paragraphs_create",
		"after_import_catalog_mutation",
		"after_import_source_replace_job_after_owner",
		"after_import_source_replace_job_after_ref_0",
		"after_import_source_replace_job_after_ref_1",
		"after_import_source_replace_job_after_seal",
		"after_import_source_replace_import_job_request_after_owner",
		"after_import_source_replace_import_job_request_after_ref_0",
		"after_import_source_replace_import_job_request_after_ref_1",
		"after_import_source_replace_import_job_request_after_seal",
		"after_import_source_replace_import_upload_after_owner",
		"after_import_source_replace_import_upload_after_ref_0",
		"after_import_source_replace_import_upload_after_ref_1",
		"after_import_source_replace_import_upload_after_seal",
		"after_import_request_terminal",
		"after_import_job_terminal",
		"after_import_upload_scrub",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			fixture := newImportFinalizeFailpointFixture(t)
			injected := errors.New("injected import finalization failure")
			store := New(fixture.db, withImportFinalizeFailpoint(func(got string) error {
				if got == point {
					return injected
				}
				return nil
			}))
			if _, err := store.FinalizeImport(fixture.ctx, fixture.input); !errors.Is(err, injected) {
				t.Fatalf("FinalizeImport(%s) error = %v, want injected failure", point, err)
			}
			assertImportTreeCounts(t, fixture.db, fixture.projectID, 0, 0, 0)
			assertImportFinalizeRetryableState(t, fixture)

			finalized, err := New(fixture.db).FinalizeImport(fixture.ctx, fixture.input)
			if err != nil {
				t.Fatalf("FinalizeImport(%s retry) error = %v", point, err)
			}
			if finalized.BookID == "" || finalized.ChapterCount != 1 || finalized.ParagraphCount != 1 {
				t.Fatalf("FinalizeImport(%s retry) = %#v", point, finalized)
			}
			assertImportTreeCounts(t, fixture.db, fixture.projectID, 1, 1, 1)
		})
	}
}

func TestLibraryStoreFinalizeImportMutatesCatalogBeforeExactJobLocks(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	injected := errors.New("after catalog mutation")
	lockChecked := false
	store := New(fixture.db, withImportFinalizeFailpoint(func(point string) error {
		if point != "after_import_catalog_mutation" {
			return nil
		}
		var id string
		if err := fixture.db.Pool.QueryRow(fixture.ctx, `
			SELECT id::text FROM jobs WHERE id = $1 AND project_id = $2 FOR UPDATE NOWAIT
		`, fixture.jobID, fixture.projectID).Scan(&id); err != nil {
			return fmt.Errorf("job locked before catalog mutation completed: %w", err)
		}
		lockChecked = true
		return injected
	}))

	if _, err := store.FinalizeImport(fixture.ctx, fixture.input); !errors.Is(err, injected) {
		t.Fatalf("FinalizeImport() error = %v, want injected", err)
	}
	if !lockChecked {
		t.Fatal("catalog failpoint did not verify the exact Job lock")
	}
	assertImportTreeCounts(t, fixture.db, fixture.projectID, 0, 0, 0)
	assertImportFinalizeRetryableState(t, fixture)
	var revision int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT catalog_revision FROM project_source_views
		WHERE project_id = $1 AND source_view = 'draft' AND project_lifecycle_generation = 1
	`, fixture.projectID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("draft catalog revision after rollback = %d, want 1", revision)
	}
}

func TestLibraryStoreRetryExhaustionScrubsImportBeforeSealingExactClaim(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, "UPDATE jobs SET retry_count = $2 WHERE id = $1", fixture.jobID, jobs.MaxJobAttempts-1); err != nil {
		t.Fatalf("set exhausted retry count: %v", err)
	}
	_, err := jobstore.New(fixture.db).RequeueWorkerClaim(fixture.ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: fixture.claim, Kind: jobs.KindImport, ExpectedRetryCount: jobs.MaxJobAttempts - 1,
	})
	if !errors.Is(err, jobs.ErrRetryExhausted) {
		t.Fatalf("RequeueWorkerClaim() error = %v, want ErrRetryExhausted", err)
	}
	var status string
	var incarnation int64
	var bodyRetained bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.status, job.claim_incarnation, upload.content IS NOT NULL
		FROM jobs AS job
		JOIN import_job_requests AS request ON request.project_id = job.project_id AND request.job_id = job.id
		JOIN import_uploads AS upload ON upload.project_id = request.project_id AND upload.storage_key = request.storage_key
		WHERE job.project_id = $1 AND job.id = $2
	`, fixture.projectID, fixture.jobID).Scan(&status, &incarnation, &bodyRetained); err != nil {
		t.Fatalf("read exhausted import before owner cleanup: %v", err)
	}
	if status != string(jobs.StatusRunning) || incarnation != fixture.claim.ClaimIncarnation || !bodyRetained {
		t.Fatalf("pre-cleanup import = %q/%d/body:%v, want unchanged exact running claim", status, incarnation, bodyRetained)
	}

	committed, err := New(fixture.db).FinalizeImportTerminalForClaim(fixture.ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: fixture.claim, Status: jobs.StatusFailed,
		Error: &jobs.Error{Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage},
	})
	if err != nil || committed != jobs.StatusFailed {
		t.Fatalf("FinalizeImportTerminalForClaim() = (%q, %v), want failed commit", committed, err)
	}
	var requestStatus, errorCode string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.status, request.status, job.error_json->>'code', upload.content IS NOT NULL
		FROM jobs AS job
		JOIN import_job_requests AS request ON request.project_id = job.project_id AND request.job_id = job.id
		JOIN import_uploads AS upload ON upload.project_id = request.project_id AND upload.storage_key = request.storage_key
		WHERE job.project_id = $1 AND job.id = $2
	`, fixture.projectID, fixture.jobID).Scan(&status, &requestStatus, &errorCode, &bodyRetained); err != nil {
		t.Fatalf("read exhausted import terminal: %v", err)
	}
	if status != string(jobs.StatusFailed) || requestStatus != string(jobs.StatusFailed) ||
		errorCode != jobs.RetryExhaustedCode || bodyRetained {
		t.Fatalf("terminal import = job:%q request:%q code:%q body:%v", status, requestStatus, errorCode, bodyRetained)
	}
}

func TestLibraryStoreRunningImportCancelStoresAbsentTerminalPayloadsAsNull(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	canceling, err := jobstore.New(fixture.db).CancelJobStatus(fixture.ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: fixture.userID,
		ProjectID:   fixture.projectID,
		JobID:       fixture.jobID,
		From:        jobs.StatusRunning,
		To:          jobs.StatusCanceling,
		UpdatedAt:   time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("CancelJobStatus() error = %v", err)
	}
	if canceling.Status != jobs.StatusCanceling {
		t.Fatalf("CancelJobStatus() status = %q, want canceling", canceling.Status)
	}

	committed, err := New(fixture.db).FinalizeImportTerminalForClaim(fixture.ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: fixture.claim, Status: jobs.StatusCanceled,
	})
	if err != nil || committed != jobs.StatusCanceled {
		t.Fatalf("FinalizeImportTerminalForClaim() = (%q, %v), want canceled commit", committed, err)
	}
	var jobStatus, requestStatus string
	var bodyCleared, resultIsNull, errorIsNull bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.status, request.status, upload.content IS NULL,
		       job.result_json IS NULL, job.error_json IS NULL
		FROM jobs AS job
		JOIN import_job_requests AS request
		  ON request.job_id = job.id AND request.project_id = job.project_id
		JOIN import_uploads AS upload
		  ON upload.storage_key = request.storage_key AND upload.project_id = request.project_id
		WHERE job.id = $1 AND job.project_id = $2
	`, fixture.jobID, fixture.projectID).Scan(
		&jobStatus, &requestStatus, &bodyCleared, &resultIsNull, &errorIsNull,
	); err != nil {
		t.Fatalf("read canceled import terminal state: %v", err)
	}
	if jobStatus != string(jobs.StatusCanceled) || requestStatus != string(jobs.StatusCanceled) ||
		!bodyCleared || !resultIsNull || !errorIsNull {
		t.Fatalf("canceled import = job:%q request:%q body-cleared:%v result-null:%v error-null:%v",
			jobStatus, requestStatus, bodyCleared, resultIsNull, errorIsNull)
	}
}

func TestLibraryStoreQueuedImportCancelScrubsStagingAtomically(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	resetClaimedImportToQueuedForTest(t, fixture)

	updatedAt := time.Now().UTC().Truncate(time.Microsecond)
	canceled, err := jobstore.New(fixture.db).CancelJobStatus(fixture.ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: fixture.userID,
		ProjectID:   fixture.projectID,
		JobID:       fixture.jobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		t.Fatalf("CancelJobStatus() error = %v", err)
	}
	if canceled.Status != jobs.StatusCanceled {
		t.Fatalf("CancelJobStatus() status = %q, want canceled", canceled.Status)
	}
	assertQueuedImportCancelState(t, fixture, jobs.StatusCanceled, "canceled", false)
}

func TestLibraryStoreFinalizeImportSerializesWithProjectDelete(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	store := New(fixture.db, withImportFinalizeFailpoint(func(point string) error {
		if point == "after_import_book_create" {
			close(entered)
			<-release
		}
		return nil
	}))
	finalizeDone := make(chan struct {
		finalized library.FinalizedImport
		err       error
	}, 1)
	go func() {
		finalized, err := store.FinalizeImport(fixture.ctx, fixture.input)
		finalizeDone <- struct {
			finalized library.FinalizedImport
			err       error
		}{finalized: finalized, err: err}
	}()
	<-entered
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- deleteProjectForLibraryTest(
			fixture.ctx, fixture.db, fixture.projectID,
			time.Now().UTC().Truncate(time.Microsecond),
		)
	}()
	select {
	case err := <-deleteDone:
		close(release)
		<-finalizeDone
		t.Fatalf("project delete completed while import finalization held the project lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	result := <-finalizeDone
	if result.err != nil {
		t.Fatalf("FinalizeImport() error = %v", result.err)
	}
	if result.finalized.BookID == "" || result.finalized.ChapterCount != 1 || result.finalized.ParagraphCount != 1 {
		t.Fatalf("FinalizeImport() = %#v", result.finalized)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("DeleteProject(after complete finalization) error = %v", err)
	}
	var deleted bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT deleted_at IS NOT NULL FROM projects WHERE id = $1
	`, fixture.projectID).Scan(&deleted); err != nil {
		t.Fatalf("read deleted project: %v", err)
	}
	if !deleted {
		t.Fatal("project delete did not commit after import finalization")
	}
}

func TestLibraryStoreImportFinalizeAndManualAppendCannotCommitSameOrder(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	store := New(fixture.db, withImportFinalizeFailpoint(func(point string) error {
		if point == "after_import_book_create" {
			close(entered)
			<-release
		}
		return nil
	}))
	finalizeDone := make(chan error, 1)
	go func() {
		_, err := store.FinalizeImport(fixture.ctx, fixture.input)
		finalizeDone <- err
	}()
	<-entered

	appendDone := make(chan error, 1)
	go func() {
		_, err := New(fixture.db).CreateBook(fixture.ctx, library.CreateBookStoreInput{
			ID:            "00000000-0000-4000-8000-000000009971",
			ProjectID:     fixture.projectID,
			Title:         "Concurrent manual append",
			Order:         1,
			DisplayNumber: "1",
			CreatedAt:     time.Now().UTC().Truncate(time.Microsecond),
		})
		appendDone <- err
	}()
	select {
	case err := <-appendDone:
		close(release)
		<-finalizeDone
		t.Fatalf("manual append completed while import finalization held the project lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-finalizeDone; err != nil {
		t.Fatalf("FinalizeImport() error = %v", err)
	}
	if err := <-appendDone; !errors.Is(err, library.ErrConflict) {
		t.Fatalf("CreateBook(stale order) error = %v, want ErrConflict", err)
	}
	if got := countJobsStoreRowsForTest(t, fixture.db, `
		SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL
	`, fixture.projectID); got != 1 {
		t.Fatalf("active books after serialized race = %d, want 1", got)
	}
}

func TestLibraryStoreClaimedImportCannotFinalizeAfterProjectDeletion(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	libraryStore := New(db)
	userID := "00000000-0000-4000-8000-000000000850"
	projectID := "00000000-0000-4000-8000-000000000851"
	membershipID := "00000000-0000-4000-8000-000000000852"
	jobID := "00000000-0000-4000-8000-000000000853"
	storageKey := "import-finalize-after-project-delete"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import-deleted-project@example.com")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Deleted Import Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)

	payload := []byte("<FictionBook>already parsed bytes</FictionBook>")
	payloadSHA256 := sha256.Sum256(payload)
	fileSHA256 := hex.EncodeToString(payloadSHA256[:])
	stagingInput := library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: sessionProof,
		IdempotencyKey: "import-after-delete", RequestMethod: "POST",
		RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "after-delete-fingerprint",
		FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: storageKey, SourceFileName: "parsed.fb2", Extension: ".fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), FileSHA256: fileSHA256,
			IdempotencyKey: "import-after-delete", ImportMode: library.ImportModeNewBook,
		},
	}
	if _, err := libraryStore.CreateImportStagingForActor(ctx, stagingInput); err != nil {
		t.Fatalf("CreateImportStagingForActor() error = %v", err)
	}
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	claim := claimDecision.Claim
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	paragraphText := "held after parsing"
	finalization := library.FinalizeImportStoreInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: userID, StorageKey: storageKey,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, BookTitle: "Must Not Commit",
		ImportMode: library.ImportModeNewBook, SourceFileName: "parsed.fb2",
		Chapters: []library.FinalizeImportChapterInput{{
			Title: "Held Chapter", Paragraphs: []library.ParagraphInput{{Markdown: paragraphText, Text: &paragraphText}},
		}},
	}
	if err := deleteProjectForLibraryTest(ctx, db, projectID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if _, err := libraryStore.FinalizeImport(ctx, finalization); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("FinalizeImport(after project delete) error = %v, want ErrNotFound", err)
	}
	assertImportTreeCounts(t, db, projectID, 0, 0, 0)
	var status string
	var resultIsNull bool
	if err := db.Pool.QueryRow(ctx, `SELECT status, result_json IS NULL FROM jobs WHERE id = $1`, jobID).Scan(&status, &resultIsNull); err != nil {
		t.Fatalf("read fenced import job: %v", err)
	}
	if status != string(jobs.StatusRunning) || !resultIsNull {
		t.Fatalf("fenced job status/result-null = %q/%v, want running with SQL NULL result", status, resultIsNull)
	}
}
