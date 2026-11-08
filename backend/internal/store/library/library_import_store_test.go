package librarystore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

func TestLibraryStoreImplementsImportStoreContract(t *testing.T) {
	var _ library.ImportStore = (*Store)(nil)
}

func mustImportProgressJSONForTest(t *testing.T, progress jobs.ImportProgress, status jobs.Status) []byte {
	t.Helper()
	encoded, err := jobstore.MarshalImportProgress(progress, status)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestLibraryStoreImportUploadAndIdempotencyRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000810"
	projectID := "00000000-0000-4000-8000-000000000811"
	membershipID := "00000000-0000-4000-8000-000000000812"
	jobID := "00000000-0000-4000-8000-000000000813"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import@example.com")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)

	payload := []byte("<FictionBook>stored</FictionBook>")
	stagingInput := library.CreateImportStagingForActorInput{
		JobID:              jobID,
		ProjectID:          projectID,
		ActorUserID:        userID,
		SessionWriteProof:  sessionProof,
		IdempotencyKey:     "import-request-1",
		RequestMethod:      "POST",
		RequestPath:        "/projects/" + projectID + "/imports",
		RequestFingerprint: "fingerprint-1",
		FileBytes:          payload,
		Metadata: library.ImportFileMetadata{
			StorageKey:     "import-storage-test",
			SourceFileName: "story.fb2",
			Extension:      ".fb2",
			MIMEType:       "application/x-fictionbook+xml",
			SizeBytes:      int64(len(payload)),
			FileSHA256:     "sha256-for-test",
			IdempotencyKey: "import-request-1",
			ImportMode:     library.ImportModeNewBook,
		},
	}
	beforeCreate := time.Now().UTC().Add(-time.Second)
	created, err := store.CreateImportStagingForActor(ctx, stagingInput)
	afterCreate := time.Now().UTC().Add(time.Second)
	if err != nil {
		t.Fatalf("CreateImportStagingForActor() error = %v", err)
	}
	if created.ID != jobID || created.Kind != jobs.KindImport || created.Status != jobs.StatusQueued ||
		created.Progress != nil || created.ImportProgress == nil ||
		created.ImportProgress.Phase != jobs.ImportPhaseQueued ||
		created.ImportProgress.BytesTransferred != int64(len(payload)) ||
		created.ImportProgress.BytesTotal != int64(len(payload)) {
		t.Fatalf("created import job = %#v, want current queued job", created)
	}
	var uploadCreatedAt, uploadExpiresAt, requestCreatedAt time.Time
	if err := db.Pool.QueryRow(ctx, `
		SELECT upload.created_at, upload.expires_at, request.created_at
		FROM import_uploads AS upload
		JOIN import_job_requests AS request
		  ON request.storage_key = upload.storage_key AND request.project_id = upload.project_id
		WHERE request.job_id = $1
	`, jobID).Scan(&uploadCreatedAt, &uploadExpiresAt, &requestCreatedAt); err != nil {
		t.Fatalf("read import staging timestamps: %v", err)
	}
	if created.CreatedAt.Before(beforeCreate) || created.CreatedAt.After(afterCreate) ||
		!created.CreatedAt.Equal(created.UpdatedAt) ||
		!created.CreatedAt.Equal(uploadCreatedAt) ||
		!created.CreatedAt.Equal(requestCreatedAt) ||
		!created.ImportProgress.QueuedAt.Equal(created.CreatedAt) ||
		!created.ImportProgress.TransferredAt.Equal(created.CreatedAt) {
		t.Fatalf("staging timestamps = job %v/%v upload %v request %v, want one DB timestamp", created.CreatedAt, created.UpdatedAt, uploadCreatedAt, requestCreatedAt)
	}
	if uploadExpiresAt.Sub(uploadCreatedAt) != library.ImportUploadRetention {
		t.Fatalf("upload retention = %v, want %v", uploadExpiresAt.Sub(uploadCreatedAt), library.ImportUploadRetention)
	}
	if created.CreatedAt.Nanosecond()%1_000 != 0 {
		t.Fatalf("created_at precision = %v, want whole PostgreSQL microsecond", created.CreatedAt)
	}
	execution, err := store.GetImportJobForExecution(ctx, projectID, jobID)
	if err != nil {
		t.Fatalf("GetImportJobForExecution() error = %v", err)
	}
	if execution.ID != jobID || execution.ProjectID != projectID || execution.Metadata.StorageKey != "import-storage-test" {
		t.Fatalf("execution import job = %#v, want exact project/job request", execution)
	}
	if _, err := store.GetImportJobForExecution(
		ctx, "00000000-0000-4000-8000-000000000899", jobID,
	); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetImportJobForExecution(other project) error = %v, want ErrNotFound", err)
	}
	replayed, err := importJobByIdempotencyKeyForTest(ctx, db, importReplayKeyForTest{
		ActorUserID:    userID,
		RequestMethod:  "POST",
		RequestPath:    "/projects/" + projectID + "/imports",
		IdempotencyKey: "import-request-1",
	})
	if err != nil {
		t.Fatalf("GetImportJobByIdempotencyKey() error = %v", err)
	}
	if replayed.ID.String() != jobID || replayed.StorageKey != "import-storage-test" || replayed.RequestFingerprint != "fingerprint-1" {
		t.Fatalf("replayed import job = %#v, want original job", replayed)
	}
	currentReplay := stagingInput
	currentReplay.JobID = "00000000-0000-4000-8000-000000000816"
	currentReplay.Metadata.StorageKey = "import-storage-current-replay"
	const resultBookID = "10000000-0000-4000-8000-000000000002"
	resultJSON := `{"type":"import","bookId":"` + resultBookID + `","chapterCount":2,"paragraphCount":3,"importMode":"new_book","sourceFileName":"story.fb2"}`
	parsingAt := created.CreatedAt.Add(time.Second)
	parsedAt := parsingAt.Add(time.Second)
	savedAt := parsedAt.Add(time.Second)
	chapters, allSaved := 2, 2
	savedProgress := *created.ImportProgress
	savedProgress.Phase = jobs.ImportPhaseSaved
	savedProgress.ParsingStartedAt = &parsingAt
	savedProgress.ParsedChapters = &chapters
	savedProgress.TotalChapters = &chapters
	savedProgress.ParsedAt = &parsedAt
	savedProgress.SavingStartedAt = &parsedAt
	savedProgress.SavedChapters = &allSaved
	savedProgress.SavedAt = &savedAt
	savedProgressJSON, err := jobstore.MarshalImportProgress(savedProgress, jobs.StatusSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'succeeded', progress_json = $3::jsonb,
		    result_json = $4::jsonb, error_json = NULL
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID, savedProgressJSON, resultJSON); err != nil {
		t.Fatalf("set current succeeded import state: %v", err)
	}
	current, err := store.CreateImportStagingForActor(ctx, currentReplay)
	if err != nil {
		t.Fatalf("CreateImportStagingForActor(succeeded replay) error = %v", err)
	}
	result, ok := current.Result.(jobs.ImportJobResult)
	if current.Status != jobs.StatusSucceeded || current.Progress != nil || current.ImportProgress == nil ||
		current.ImportProgress.Phase != jobs.ImportPhaseSaved || !ok ||
		result.BookID != resultBookID || result.ChapterCount != 2 || result.ParagraphCount != 3 ||
		result.ImportMode != "new_book" || result.SourceFileName != "story.fb2" {
		t.Fatalf("succeeded replay = %#v, want current progress and typed result", current)
	}
	errorJSON := `{"code":"worker.handler_failed","message":"The background job failed."}`
	failedProgress := *created.ImportProgress
	failedProgress.Phase = jobs.ImportPhaseParsing
	failedProgress.ParsingStartedAt = &parsingAt
	failedProgressJSON, err := jobstore.MarshalImportProgress(failedProgress, jobs.StatusFailed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'failed', progress_json = $3::jsonb,
		    result_json = NULL, error_json = $4::jsonb
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID, failedProgressJSON, errorJSON); err != nil {
		t.Fatalf("set current failed import state: %v", err)
	}
	current, err = store.CreateImportStagingForActor(ctx, currentReplay)
	if err != nil {
		t.Fatalf("CreateImportStagingForActor(failed replay) error = %v", err)
	}
	if current.Status != jobs.StatusFailed || current.Progress != nil || current.ImportProgress == nil ||
		current.ImportProgress.Phase != jobs.ImportPhaseParsing || current.Result != nil ||
		current.Error == nil || current.Error.Code != "worker.handler_failed" {
		t.Fatalf("failed replay = %#v, want current progress and error", current)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'succeeded', result_json = $3::jsonb || '{"unexpected":true}'::jsonb, error_json = NULL
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID, resultJSON); err != nil {
		t.Fatalf("poison stored import result: %v", err)
	}
	if _, err := store.CreateImportStagingForActor(ctx, currentReplay); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("CreateImportStagingForActor(malformed result replay) error = %v, want ErrConflict", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'failed', result_json = NULL,
		    error_json = $3::jsonb || '{"unexpected":true}'::jsonb
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID, errorJSON); err != nil {
		t.Fatalf("poison stored import error: %v", err)
	}
	if _, err := store.CreateImportStagingForActor(ctx, currentReplay); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("CreateImportStagingForActor(malformed error replay) error = %v, want ErrConflict", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'queued', progress_json = $3::jsonb,
		    result_json = NULL, error_json = NULL
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID, mustImportProgressJSONForTest(t, *created.ImportProgress, jobs.StatusQueued)); err != nil {
		t.Fatalf("restore queued import state: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET subject_json = subject_json || '{"unexpected":true}'::jsonb
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID); err != nil {
		t.Fatalf("poison stored import subject: %v", err)
	}
	if _, err := store.CreateImportStagingForActor(ctx, currentReplay); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("CreateImportStagingForActor(poisoned subject replay) error = %v, want ErrConflict", err)
	}
	if _, err := store.GetImportJobForExecution(ctx, projectID, jobID); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("GetImportJobForExecution(poisoned subject) error = %v, want ErrConflict", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET subject_json = subject_json - 'unexpected'
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID); err != nil {
		t.Fatalf("restore stored import subject: %v", err)
	}
	duplicate := stagingInput
	duplicate.JobID = "00000000-0000-4000-8000-000000000815"
	duplicate.Metadata.StorageKey = "import-storage-duplicate-attempt"
	duplicateJob, err := store.CreateImportStagingForActor(ctx, duplicate)
	if err != nil {
		t.Fatalf("CreateImportStagingForActor(duplicate) error = %v", err)
	}
	if duplicateJob.ID != jobID || duplicateJob.Status != jobs.StatusQueued {
		t.Fatalf("duplicate import result = %#v, want current original job", duplicateJob)
	}
	if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM import_uploads WHERE storage_key = $1", duplicate.Metadata.StorageKey); got != 0 {
		t.Fatalf("duplicate upload rows = %d, want 0", got)
	}
	conflicting := duplicate
	conflicting.RequestFingerprint = "different-fingerprint"
	if _, err := store.CreateImportStagingForActor(ctx, conflicting); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("CreateImportStagingForActor(conflict) error = %v, want ErrConflict", err)
	}

	mismatched := stagingInput
	mismatched.JobID = "00000000-0000-4000-8000-000000000814"
	mismatched.IdempotencyKey = "import-request-mismatch"
	mismatched.Metadata.StorageKey = "import-storage-mismatch"
	mismatched.Metadata.IdempotencyKey = mismatched.IdempotencyKey
	mismatched.Metadata.SizeBytes++
	if _, err := store.CreateImportStagingForActor(ctx, mismatched); !errors.Is(err, library.ErrValidation) {
		t.Fatalf("CreateImportStagingForActor(mismatched metadata) error = %v, want ErrValidation", err)
	}
	if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM jobs WHERE id = $1", mismatched.JobID); got != 0 {
		t.Fatalf("mismatched import jobs count = %d, want 0", got)
	}
	if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM import_uploads WHERE storage_key = $1", mismatched.Metadata.StorageKey); got != 0 {
		t.Fatalf("mismatched import uploads count = %d, want 0", got)
	}
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	claim := claimDecision.Claim
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	terminalStatus, err := store.FinalizeImportTerminalForClaim(ctx, library.FinalizeImportTerminalForClaimInput{
		Claim:  claim,
		Status: jobs.StatusFailed,
		Error:  &jobs.Error{Code: "worker.handler_failed", Message: "The background job failed."},
	})
	if err != nil {
		t.Fatalf("FinalizeImportTerminalForClaim() error = %v", err)
	}
	if terminalStatus != jobs.StatusFailed {
		t.Fatalf("terminal status = %q, want failed", terminalStatus)
	}
	var status, requestStatus, errorCode string
	var bodyCleared, resultIsNull bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT job.status, request.status, upload.content IS NULL,
		       job.result_json IS NULL, job.error_json->>'code'
		FROM jobs AS job
		JOIN import_job_requests AS request ON request.job_id = job.id AND request.project_id = job.project_id
		JOIN import_uploads AS upload ON upload.storage_key = request.storage_key AND upload.project_id = job.project_id
		WHERE job.id = $1
	`, jobID).Scan(&status, &requestStatus, &bodyCleared, &resultIsNull, &errorCode); err != nil {
		t.Fatalf("read terminal import state: %v", err)
	}
	if status != "failed" || requestStatus != "failed" || !bodyCleared || !resultIsNull || errorCode != "worker.handler_failed" {
		t.Fatalf("terminal import state = %q/%q/body-cleared=%v/result-null=%v/error-code=%q, want failed/failed/true/true/worker.handler_failed",
			status, requestStatus, bodyCleared, resultIsNull, errorCode)
	}
}

func TestLibraryStoreImportStartSessionFence(t *testing.T) {
	tests := []struct {
		name  string
		proof func(*testing.T, *storedb.DB, auth.SessionWriteProof, int) auth.SessionWriteProof
	}{
		{
			name: "mismatched actor",
			proof: func(t *testing.T, db *storedb.DB, _ auth.SessionWriteProof, base int) auth.SessionWriteProof {
				otherUserID := fmt.Sprintf("00000000-0000-4000-8000-%012d", base+5)
				cleanupProjectsStoreRows(t, db, otherUserID, fmt.Sprintf("00000000-0000-4000-8000-%012d", base+6))
				return insertProjectStoreUserWithSessionForTest(t, db, otherUserID, fmt.Sprintf("import-session-mismatch-%d@example.test", base))
			},
		},
		{
			name: "revoked session",
			proof: func(t *testing.T, db *storedb.DB, proof auth.SessionWriteProof, _ int) auth.SessionWriteProof {
				if _, err := db.Pool.Exec(context.Background(), `
					UPDATE sessions SET revoked_at = clock_timestamp() WHERE session_hash = $1
				`, proof.SessionHash); err != nil {
					t.Fatalf("revoke import session: %v", err)
				}
				return proof
			},
		},
		{
			name: "rotated csrf secret",
			proof: func(t *testing.T, db *storedb.DB, proof auth.SessionWriteProof, _ int) auth.SessionWriteProof {
				if _, err := db.Pool.Exec(context.Background(), `
					UPDATE sessions SET csrf_secret_hash = 'rotated' WHERE session_hash = $1
				`, proof.SessionHash); err != nil {
					t.Fatalf("rotate import session csrf secret: %v", err)
				}
				return proof
			},
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			base := 9800 + i*10
			userID := fmt.Sprintf("00000000-0000-4000-8000-%012d", base)
			projectID := fmt.Sprintf("00000000-0000-4000-8000-%012d", base+1)
			membershipID := fmt.Sprintf("00000000-0000-4000-8000-%012d", base+2)
			jobID := fmt.Sprintf("00000000-0000-4000-8000-%012d", base+3)
			storageKey := fmt.Sprintf("import-session-fence-%d", base)
			cleanupLibraryImportStoreRows(t, db, userID, projectID)
			proof := insertProjectStoreUserWithSessionForTest(t, db, userID, fmt.Sprintf("import-session-fence-%d@example.test", base))
			now := time.Now().UTC().Truncate(time.Microsecond)
			insertImportStoreProjectForTest(t, db, projectID, userID, "Import Session Fence", now)
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)
			proof = test.proof(t, db, proof, base)
			payload := []byte("<FictionBook>session fence</FictionBook>")

			_, err := New(db).CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
				JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: proof,
				IdempotencyKey: storageKey, RequestMethod: "POST",
				RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "session-fence-" + storageKey,
				FileBytes: payload,
				Metadata: library.ImportFileMetadata{
					StorageKey: storageKey, SourceFileName: "session-fence.fb2", Extension: ".fb2",
					MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), FileSHA256: "session-fence-sha256",
					IdempotencyKey: storageKey, ImportMode: library.ImportModeNewBook,
				},
			})
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("CreateImportStagingForActor() error = %v, want ErrUnauthenticated", err)
			}
			for label, query := range map[string]string{
				"job":     "SELECT count(*) FROM jobs WHERE id = $1",
				"request": "SELECT count(*) FROM import_job_requests WHERE job_id = $1",
			} {
				if got := countJobsStoreRowsForTest(t, db, query, jobID); got != 0 {
					t.Fatalf("session fence %s rows = %d, want 0", label, got)
				}
			}
			if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM import_uploads WHERE storage_key = $1", storageKey); got != 0 {
				t.Fatalf("session fence upload rows = %d, want 0", got)
			}
			if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM artifact_source_owners WHERE project_id = $1", projectID); got != 0 {
				t.Fatalf("session fence artifact owners = %d, want 0", got)
			}
			if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM artifact_source_refs WHERE project_id = $1", projectID); got != 0 {
				t.Fatalf("session fence artifact refs = %d, want 0", got)
			}
		})
	}
}

func TestLibraryStoreCreateImportStagingRollsBackAtEveryOwnerSealBoundary(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	userID := "00000000-0000-4000-8000-000000000820"
	projectID := "00000000-0000-4000-8000-000000000821"
	membershipID := "00000000-0000-4000-8000-000000000822"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import-staging-failpoints@example.com")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Staging Failpoints", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)

	points := []string{
		"import_upload.after_owner", "import_upload.after_ref_0", "import_upload.after_artifact", "import_upload.after_seal",
		"job.after_owner", "job.after_ref_0", "job.after_artifact", "job.after_seal",
		"import_job_request.after_owner", "import_job_request.after_ref_0", "import_job_request.after_artifact", "import_job_request.after_seal",
		"before_commit",
	}
	for index, point := range points {
		t.Run(point, func(t *testing.T) {
			jobID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 900+index)
			storageKey := fmt.Sprintf("import-staging-failpoint-%d", index)
			idempotencyKey := fmt.Sprintf("import-staging-failpoint-idempotency-%d", index)
			payload := []byte("<FictionBook>atomic staging</FictionBook>")
			failpointErr := errors.New("import staging failpoint")
			store := New(db, withImportStagingFailpoint(func(candidate string) error {
				if candidate == point {
					return failpointErr
				}
				return nil
			}))
			_, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
				JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: sessionProof,
				IdempotencyKey: idempotencyKey, RequestMethod: "POST",
				RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "failpoint-fingerprint",
				FileBytes: payload,
				Metadata: library.ImportFileMetadata{
					StorageKey: storageKey, SourceFileName: "story.fb2", Extension: ".fb2",
					MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), FileSHA256: "failpoint-sha256",
					IdempotencyKey: idempotencyKey, ImportMode: library.ImportModeNewBook,
				},
			})
			if !errors.Is(err, failpointErr) {
				t.Fatalf("CreateImportStagingForActor() error = %v, want failpoint", err)
			}
			for label, query := range map[string]string{
				"job":     "SELECT count(*) FROM jobs WHERE id = $1",
				"request": "SELECT count(*) FROM import_job_requests WHERE job_id = $1",
			} {
				if got := countJobsStoreRowsForTest(t, db, query, jobID); got != 0 {
					t.Fatalf("%s rows after %s = %d, want 0", label, point, got)
				}
			}
			if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM import_uploads WHERE storage_key = $1", storageKey); got != 0 {
				t.Fatalf("upload rows after %s = %d, want 0", point, got)
			}
			if got := countJobsStoreRowsForTest(t, db, "SELECT count(*) FROM artifact_source_owners WHERE project_id = $1", projectID); got != 0 {
				t.Fatalf("owner rows after %s = %d, want 0", point, got)
			}
		})
	}
}
