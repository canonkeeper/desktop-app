package librarystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
)

func TestLibraryStoreImportUploadContentExpiresAndPurgesWithoutDeletingMetadata(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000830"
	projectID := "00000000-0000-4000-8000-000000000831"
	membershipID := "00000000-0000-4000-8000-000000000832"
	jobID := "00000000-0000-4000-8000-000000000833"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import-retention@example.com")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Retention Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)

	payload := []byte("<FictionBook>expired raw bytes</FictionBook>")
	stagingInput := library.CreateImportStagingForActorInput{
		JobID:              jobID,
		ProjectID:          projectID,
		ActorUserID:        userID,
		SessionWriteProof:  sessionProof,
		IdempotencyKey:     "import-request-expired",
		RequestMethod:      "POST",
		RequestPath:        "/projects/" + projectID + "/imports",
		RequestFingerprint: "fingerprint-expired",
		FileBytes:          payload,
		Metadata: library.ImportFileMetadata{
			StorageKey:     "expired-storage-test",
			SourceFileName: "story.fb2",
			Extension:      ".fb2",
			MIMEType:       "application/x-fictionbook+xml",
			SizeBytes:      int64(len(payload)),
			FileSHA256:     "sha256-expired",
			IdempotencyKey: "import-request-expired",
			ImportMode:     library.ImportModeNewBook,
		},
	}
	if _, err := store.CreateImportStagingForActor(ctx, stagingInput); err != nil {
		t.Fatalf("CreateImportStagingForActor(expired) error = %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE import_uploads
		SET expires_at = clock_timestamp() - interval '1 second'
		WHERE storage_key = $1
	`, stagingInput.Metadata.StorageKey); err != nil {
		t.Fatalf("expire staged import upload: %v", err)
	}

	claim, err := newWorkerClaimAuthorityForTest(db).ClaimNextImportExpiryForWorker(ctx)
	if err != nil {
		t.Fatalf("ClaimNextImportExpiryForWorker() error = %v", err)
	}
	if claim.ProjectID != projectID || claim.JobID != jobID || claim.ClaimIncarnation != 1 {
		t.Fatalf("expiry claim = %#v, want exact import claim", claim)
	}
	if _, err := store.LoadImportBodyForClaim(ctx, claim, "expired-storage-test"); !errors.Is(err, library.ErrImportUploadExpired) {
		t.Fatalf("LoadImportBodyForClaim(expired) error = %v, want ErrImportUploadExpired", err)
	}
	terminalStatus, err := store.ExpireImportStagingForClaim(ctx, claim)
	if err != nil {
		t.Fatalf("ExpireImportStagingForClaim() error = %v", err)
	}
	if terminalStatus != jobs.StatusExpired {
		t.Fatalf("expired status = %q, want expired", terminalStatus)
	}
	if _, err := store.LoadImportBodyForClaim(ctx, claim, "expired-storage-test"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("LoadImportBodyForClaim(purged) error = %v, want ErrNotFound", err)
	}
	if _, err := importJobByIdempotencyKeyForTest(ctx, db, importReplayKeyForTest{
		ActorUserID:    userID,
		RequestMethod:  "POST",
		RequestPath:    "/projects/" + projectID + "/imports",
		IdempotencyKey: "import-request-expired",
	}); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetImportJobByIdempotencyKey() after purge error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetImportJobForExecution(ctx, projectID, jobID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetImportJobForExecution() after purge error = %v, want ErrNotFound", err)
	}
	var contentPresent bool
	if err := db.Pool.QueryRow(ctx, "SELECT content IS NOT NULL FROM import_uploads WHERE storage_key = $1", "expired-storage-test").Scan(&contentPresent); err != nil {
		t.Fatalf("query purged upload content: %v", err)
	}
	if contentPresent {
		t.Fatal("purged upload content is still present")
	}
	var jobStatus, requestStatus, jobPayloadState, requestPayloadState string
	var jobOwnerPresent, requestOwnerPresent, uploadOwnerPresent bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT job.status, request.status, job.governance_payload_state, request.governance_payload_state,
		       job.artifact_source_owner_id IS NOT NULL,
		       request.artifact_source_owner_id IS NOT NULL,
		       upload.artifact_source_owner_id IS NOT NULL
		FROM jobs AS job
		JOIN import_job_requests AS request ON request.job_id = job.id AND request.project_id = job.project_id
		JOIN import_uploads AS upload ON upload.storage_key = request.storage_key AND upload.project_id = job.project_id
		WHERE job.id = $1
	`, jobID).Scan(
		&jobStatus, &requestStatus, &jobPayloadState, &requestPayloadState,
		&jobOwnerPresent, &requestOwnerPresent, &uploadOwnerPresent,
	); err != nil {
		t.Fatalf("read expired import state: %v", err)
	}
	if jobStatus != "expired" || requestStatus != "expired" ||
		jobPayloadState != "source_deleted" || requestPayloadState != "source_deleted" ||
		jobOwnerPresent || requestOwnerPresent || uploadOwnerPresent {
		t.Fatalf("expired import state = %q/%q %q/%q owners=%v/%v/%v",
			jobStatus, requestStatus, jobPayloadState, requestPayloadState,
			jobOwnerPresent, requestOwnerPresent, uploadOwnerPresent)
	}
}

func TestLibraryStoreImportReplayStaysConcealedAfterProjectRestore(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000870"
	projectID := "00000000-0000-4000-8000-000000000871"
	membershipID := "00000000-0000-4000-8000-000000000872"
	jobID := "00000000-0000-4000-8000-000000000873"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import-restore-replay@example.com")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Restored Import Replay Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)

	payload := []byte("<FictionBook>must stay deleted</FictionBook>")
	stagingInput := library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: sessionProof,
		IdempotencyKey: "import-restore-replay", RequestMethod: "POST",
		RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "restore-replay-fingerprint",
		FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: "import-restore-replay", SourceFileName: "story.fb2", Extension: ".fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)),
			FileSHA256: "restore-replay-sha256", IdempotencyKey: "import-restore-replay",
			ImportMode: library.ImportModeNewBook,
		},
	}
	if _, err := store.CreateImportStagingForActor(ctx, stagingInput); err != nil {
		t.Fatalf("CreateImportStagingForActor() error = %v", err)
	}

	advanceProjectLifecycleForLibraryTest(t, db, projectID, now.Add(time.Second))

	replayInput := importReplayKeyForTest{
		ActorUserID: userID, RequestMethod: stagingInput.RequestMethod,
		RequestPath: stagingInput.RequestPath, IdempotencyKey: stagingInput.IdempotencyKey,
	}
	if _, err := importJobByIdempotencyKeyForTest(ctx, db, replayInput); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetImportJobByIdempotencyKey() after restore error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetImportJobForExecution(ctx, projectID, jobID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetImportJobForExecution() after restore error = %v, want ErrNotFound", err)
	}
	duplicate := stagingInput
	duplicate.JobID = "00000000-0000-4000-8000-000000000879"
	duplicate.Metadata.StorageKey = "import-restore-replay-duplicate"
	if _, err := store.CreateImportStagingForActor(ctx, duplicate); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("CreateImportStagingForActor(replay after restore) error = %v, want ErrConflict", err)
	}
	if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM jobs WHERE id = $1", duplicate.JobID); got != 0 {
		t.Fatalf("replay created replacement Job rows = %d, want 0", got)
	}
	var jobOwnerReadable, requestOwnerReadable, uploadOwnerReadable bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT
			COALESCE(ck_artifact_source_owner_is_readable(
				job.artifact_source_owner_id, job.project_id, 'job', job.id
			), false),
			COALESCE(ck_artifact_source_owner_is_readable(
				request.artifact_source_owner_id, request.project_id, 'import_job_request', request.job_id
			), false),
			COALESCE(ck_artifact_source_owner_is_readable(
				upload.artifact_source_owner_id, upload.project_id, 'import_upload', upload.artifact_id
			), false)
		FROM jobs AS job
		JOIN import_job_requests AS request ON request.job_id = job.id AND request.project_id = job.project_id
		JOIN import_uploads AS upload ON upload.storage_key = request.storage_key AND upload.project_id = job.project_id
		WHERE job.id = $1 AND job.project_id = $2
	`, jobID, projectID).Scan(&jobOwnerReadable, &requestOwnerReadable, &uploadOwnerReadable); err != nil {
		t.Fatalf("read restored import owner fences: %v", err)
	}
	if jobOwnerReadable || requestOwnerReadable || uploadOwnerReadable {
		t.Fatalf("restored import owner readability = job %v request %v upload %v, want all false",
			jobOwnerReadable, requestOwnerReadable, uploadOwnerReadable)
	}
}

func TestLibraryStoreImportExpiryClaimSkipsStaleRestoredProjectAndAdvances(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)

	poisonUserID := "00000000-0000-4000-8000-000000000850"
	poisonProjectID := "00000000-0000-4000-8000-000000000851"
	poisonMembershipID := "00000000-0000-4000-8000-000000000852"
	poisonJobID := "00000000-0000-4000-8000-000000000853"
	validUserID := "00000000-0000-4000-8000-000000000860"
	validProjectID := "00000000-0000-4000-8000-000000000861"
	validMembershipID := "00000000-0000-4000-8000-000000000862"
	validJobID := "00000000-0000-4000-8000-000000000863"
	cleanupLibraryImportStoreRows(t, db, poisonUserID, poisonProjectID)
	cleanupLibraryImportStoreRows(t, db, validUserID, validProjectID)

	stage := func(userID, projectID, membershipID, jobID, storageKey string) {
		t.Helper()
		sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, storageKey+"@example.com")
		insertImportStoreProjectForTest(t, db, projectID, userID, storageKey+" project", now)
		insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)
		payload := []byte("<FictionBook>expired raw bytes</FictionBook>")
		if _, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
			JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: sessionProof,
			IdempotencyKey: storageKey + "-request", RequestMethod: "POST",
			RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: storageKey + "-fingerprint",
			FileBytes: payload,
			Metadata: library.ImportFileMetadata{
				StorageKey: storageKey, SourceFileName: "story.fb2", Extension: ".fb2",
				MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)),
				FileSHA256: storageKey + "-sha256", IdempotencyKey: storageKey + "-request",
				ImportMode: library.ImportModeNewBook,
			},
		}); err != nil {
			t.Fatalf("stage %s: %v", storageKey, err)
		}
	}

	stage(poisonUserID, poisonProjectID, poisonMembershipID, poisonJobID, "poison-expiry-storage")
	stage(validUserID, validProjectID, validMembershipID, validJobID, "valid-expiry-storage")
	if _, err := db.Pool.Exec(ctx, `
		UPDATE import_uploads
		SET expires_at = CASE storage_key
			WHEN 'poison-expiry-storage' THEN clock_timestamp() - interval '2 seconds'
			ELSE clock_timestamp() - interval '1 second'
		END
		WHERE storage_key IN ('poison-expiry-storage', 'valid-expiry-storage')
	`); err != nil {
		t.Fatalf("expire staged uploads: %v", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		UPDATE projects
		SET lifecycle_generation = 2
		WHERE id = $1
	`, poisonProjectID); err != nil {
		t.Fatalf("advance poison project to a restored lifecycle generation: %v", err)
	}

	claim, err := newWorkerClaimAuthorityForTest(db).ClaimNextImportExpiryForWorker(ctx)
	if err != nil {
		t.Fatalf("ClaimNextImportExpiryForWorker() error = %v", err)
	}
	if claim.ProjectID != validProjectID || claim.JobID != validJobID {
		t.Fatalf("expiry claim = %#v, want later eligible active project job", claim)
	}
}
