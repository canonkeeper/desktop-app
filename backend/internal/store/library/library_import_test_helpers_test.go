package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

type importFinalizeFailpointFixture struct {
	ctx        context.Context
	db         *storedb.DB
	userID     string
	projectID  string
	jobID      string
	storageKey string
	claim      jobs.WorkerClaim
	input      library.FinalizeImportStoreInput
}

func newImportFinalizeFailpointFixture(t *testing.T) importFinalizeFailpointFixture {
	t.Helper()
	return newImportFinalizeFailpointFixtureWithDB(t, openCurrentStoreTestDB(t), library.InitialPublicationDraftOnly)
}

func newImportFinalizeFailpointFixtureForPublication(t *testing.T, publication library.InitialPublication) importFinalizeFailpointFixture {
	t.Helper()
	return newImportFinalizeFailpointFixtureWithDB(t, openCurrentStoreTestDB(t), publication)
}

func newImportFinalizeFailpointFixtureWithDB(t *testing.T, db *storedb.DB, publication library.InitialPublication) importFinalizeFailpointFixture {
	t.Helper()
	ctx := context.Background()
	userID := "00000000-0000-4000-8000-000000000960"
	projectID := "00000000-0000-4000-8000-000000000961"
	membershipID := "00000000-0000-4000-8000-000000000962"
	jobID := "00000000-0000-4000-8000-000000000963"
	storageKey := "import-finalize-failpoint"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import-failpoint@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Failpoint Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)
	payload := []byte("<FictionBook>failpoint import</FictionBook>")
	digest := sha256.Sum256(payload)
	if _, err := New(db).CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: sessionProof,
		IdempotencyKey: "import-finalize-failpoint", RequestMethod: "POST",
		RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "finalize-failpoint",
		FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: storageKey, SourceFileName: "failpoint.fb2", Extension: ".fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)),
			FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: "import-finalize-failpoint",
			ImportMode: library.ImportModeNewBook, InitialPublication: publication,
		},
	}); err != nil {
		t.Fatalf("CreateImportStagingForActor() error = %v", err)
	}
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	claim := claimDecision.Claim
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	store := New(db)
	if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
		t.Fatalf("MarkImportParsingForClaim() error = %v", err)
	}
	if err := store.MarkImportSavingForClaim(ctx, claim, 1); err != nil {
		t.Fatalf("MarkImportSavingForClaim() error = %v", err)
	}
	paragraphText := "Failpoint paragraph"
	return importFinalizeFailpointFixture{
		ctx: ctx, db: db, userID: userID, projectID: projectID, jobID: jobID, storageKey: storageKey, claim: claim,
		input: library.FinalizeImportStoreInput{
			JobID: jobID, ProjectID: projectID, ActorUserID: userID, StorageKey: storageKey,
			ExpectedClaimIncarnation: claim.ClaimIncarnation, BookTitle: "Failpoint Import",
			ImportMode: library.ImportModeNewBook, InitialPublication: publication, SourceFileName: "failpoint.fb2",
			Chapters: []library.FinalizeImportChapterInput{{
				Title: "Chapter", Paragraphs: []library.ParagraphInput{{Markdown: paragraphText, Text: &paragraphText}},
			}},
		},
	}
}

func assertImportFinalizeRetryableState(t *testing.T, fixture importFinalizeFailpointFixture) {
	t.Helper()
	var jobStatus, requestStatus string
	var bodyRetained bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.status, request.status, upload.content IS NOT NULL
		FROM jobs AS job
		JOIN import_job_requests AS request
		  ON request.job_id = job.id AND request.project_id = job.project_id
		JOIN import_uploads AS upload
		  ON upload.storage_key = request.storage_key AND upload.project_id = request.project_id
		WHERE job.id = $1 AND job.project_id = $2
	`, fixture.jobID, fixture.projectID).Scan(&jobStatus, &requestStatus, &bodyRetained); err != nil {
		t.Fatalf("read retryable import state: %v", err)
	}
	if jobStatus != string(jobs.StatusRunning) || requestStatus != "pending" || !bodyRetained {
		t.Fatalf("retryable import state = %q/%q/body-retained:%v", jobStatus, requestStatus, bodyRetained)
	}
	if got := countJobsStoreRowsForTest(t, fixture.db, `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'indexing'
	`, fixture.projectID); got != 0 {
		t.Fatalf("durable indexing jobs after rollback = %d, want 0", got)
	}
	if got := countJobsStoreRowsForTest(t, fixture.db, `
		SELECT count(*) FROM idempotency_records
		WHERE operation_id = 'indexing.project.reindex.start.v1'
	`); got != 0 {
		t.Fatalf("durable indexing idempotency records after rollback = %d, want 0", got)
	}
	if got := countJobsStoreRowsForTest(t, fixture.db, `
		SELECT count(*) FROM artifact_source_owners
		WHERE project_id = $1
		  AND artifact_kind IN ('job', 'import_job_request', 'import_upload')
		  AND ref_count = 1
	`, fixture.projectID); got != 3 {
		t.Fatalf("original import owners after rollback = %d, want 3", got)
	}
}

func resetClaimedImportToQueuedForTest(t *testing.T, fixture importFinalizeFailpointFixture) {
	t.Helper()
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE jobs
		SET status = 'queued', claimed_at = NULL, cancel_requested_at = NULL
		WHERE id = $1 AND project_id = $2
	`, fixture.jobID, fixture.projectID); err != nil {
		t.Fatalf("reset claimed import to queued: %v", err)
	}
}

func assertQueuedImportCancelState(
	t *testing.T,
	fixture importFinalizeFailpointFixture,
	wantJob jobs.Status,
	wantRequest string,
	wantBody bool,
) {
	t.Helper()
	var jobStatus, requestStatus string
	var bodyPresent bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.status, request.status, upload.content IS NOT NULL
		FROM jobs AS job
		JOIN import_job_requests AS request
		  ON request.job_id = job.id AND request.project_id = job.project_id
		JOIN import_uploads AS upload
		  ON upload.storage_key = request.storage_key AND upload.project_id = request.project_id
		WHERE job.id = $1 AND job.project_id = $2
	`, fixture.jobID, fixture.projectID).Scan(&jobStatus, &requestStatus, &bodyPresent); err != nil {
		t.Fatalf("read queued import cancel state: %v", err)
	}
	if jobStatus != string(wantJob) || requestStatus != wantRequest || bodyPresent != wantBody {
		t.Fatalf("queued import state = %q/%q/body:%t, want %q/%q/body:%t",
			jobStatus, requestStatus, bodyPresent, wantJob, wantRequest, wantBody)
	}
}

func assertImportTreeCounts(t *testing.T, db *storedb.DB, projectID string, wantBooks, wantChapters, wantParagraphs int) {
	t.Helper()
	var books, chapters, paragraphs int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT COUNT(DISTINCT book.id), COUNT(DISTINCT chapter.id), COUNT(paragraph.id)
		FROM projects project
		LEFT JOIN books book ON book.project_id = project.id
		LEFT JOIN chapters chapter ON chapter.project_id = project.id AND chapter.book_id = book.id
		LEFT JOIN chapter_paragraphs paragraph ON paragraph.chapter_id = chapter.id
		WHERE project.id = $1
	`, projectID).Scan(&books, &chapters, &paragraphs); err != nil {
		t.Fatalf("count import tree: %v", err)
	}
	if books != wantBooks || chapters != wantChapters || paragraphs != wantParagraphs {
		t.Fatalf("import tree counts = %d/%d/%d, want %d/%d/%d", books, chapters, paragraphs, wantBooks, wantChapters, wantParagraphs)
	}
}

func cleanupLibraryImportStoreRows(t *testing.T, db *storedb.DB, userID, projectID string) {
	t.Helper()
	cleanup := func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM import_job_requests WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM jobs WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM import_uploads WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_memberships WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	}
	t.Cleanup(cleanup)
	cleanup()
}

func insertImportStoreProjectForTest(t *testing.T, db *storedb.DB, projectID, ownerID, name string, createdAt time.Time) {
	t.Helper()
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, name, createdAt)
}

// importReplayKeyForTest — ключ идемпотентного повтора импорта.
type importReplayKeyForTest struct {
	ActorUserID    string
	RequestMethod  string
	RequestPath    string
	IdempotencyKey string
}

// importJobByIdempotencyKeyForTest выполняет тот же поиск повтора, что и
// постановка импорта (CreateImportStagingForActor), без создания нового задания.
func importJobByIdempotencyKeyForTest(ctx context.Context, db *storedb.DB, key importReplayKeyForTest) (sqlc.GetImportJobByIdempotencyKeyRow, error) {
	actorUserID, err := storedb.ParseUUID(key.ActorUserID)
	if err != nil {
		return sqlc.GetImportJobByIdempotencyKeyRow{}, library.ErrNotFound
	}
	row, err := sqlc.New(db.Pool).GetImportJobByIdempotencyKey(ctx, sqlc.GetImportJobByIdempotencyKeyParams{
		ActorUserID: actorUserID, RequestMethod: key.RequestMethod,
		RequestPath: key.RequestPath, IdempotencyKey: key.IdempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.GetImportJobByIdempotencyKeyRow{}, library.ErrNotFound
	}
	return row, err
}
