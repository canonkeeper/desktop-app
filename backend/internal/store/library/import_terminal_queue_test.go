package librarystore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
)

func TestImportQueueTerminalFailureBlocksLaterItems(t *testing.T) {
	tests := []struct {
		name      string
		errorCode string
		wantCode  string
	}{
		{
			name:      "unexpected deterministic failure",
			errorCode: "import_sequence_conflict",
			wantCode:  "unexpected_deterministic_failure",
		},
		{
			name:      "exhausted infrastructure failure",
			errorCode: jobs.RetryExhaustedCode,
			wantCode:  jobs.RetryExhaustedCode,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newImportFinalizeFailpointFixture(t)
			queueID := "00000000-0000-4000-8000-00000000a301"
			if _, err := fixture.db.Pool.Exec(ctx, `
				INSERT INTO import_queues (
					id, project_id, created_by_user_id, idempotency_key, request_fingerprint,
					status, version, created_at, updated_at, started_at
				)
				VALUES ($1, $2, $3, $4, $5, 'running', 2, clock_timestamp(), clock_timestamp(), clock_timestamp())
			`, queueID, fixture.projectID, fixture.userID, t.Name(), t.Name()+"-fingerprint"); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Pool.Exec(ctx, `
				INSERT INTO import_queue_items (
					id, queue_id, project_id, client_item_id, position, source_file_name,
					storage_key, mime_type, size_bytes, sha256, detected_book_order,
					confirmed_book_order, preflight_status, import_job_id, created_at, updated_at
				)
				SELECT gen_random_uuid(), $1, $2, 'terminal-current', 1,
				       $3::text, upload.storage_key, upload.mime_type, upload.size_bytes,
				       upload.sha256, 1, 1, 'ready', $4::uuid, clock_timestamp(), clock_timestamp()
				FROM import_uploads AS upload
				WHERE upload.project_id = $2 AND upload.storage_key = $5
			`, queueID, fixture.projectID,
				fixture.input.SourceFileName, fixture.jobID, fixture.storageKey); err != nil {
				t.Fatal(err)
			}

			committed, err := New(fixture.db).FinalizeImportTerminalForClaim(ctx, library.FinalizeImportTerminalForClaimInput{
				Claim: fixture.claim, Status: jobs.StatusFailed,
				Error: &jobs.Error{Code: test.errorCode, Message: test.name},
			})
			if err != nil || committed != jobs.StatusFailed {
				t.Fatalf("FinalizeImportTerminalForClaim() = (%q, %v), want failed", committed, err)
			}

			var status, errorCode string
			var bodyPresent bool
			if err := fixture.db.Pool.QueryRow(ctx, `
				SELECT queue.status, job.error_json->>'code', upload.content IS NOT NULL
				FROM import_queues AS queue
				JOIN import_queue_items AS item
				  ON item.queue_id = queue.id AND item.project_id = queue.project_id
				JOIN jobs AS job
				  ON job.id = item.import_job_id AND job.project_id = item.project_id
				JOIN import_uploads AS upload
				  ON upload.storage_key = item.storage_key AND upload.project_id = item.project_id
				WHERE queue.id = $1 AND item.position = 1
			`, queueID).Scan(&status, &errorCode, &bodyPresent); err != nil {
				t.Fatal(err)
			}
			if status != string(library.ImportQueueFailed) || errorCode != test.wantCode || bodyPresent {
				t.Fatalf("queue/code/body = %q/%q/%t, want failed/%q/false", status, errorCode, bodyPresent, test.wantCode)
			}
			if _, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport}); err != jobs.ErrNotFound {
				t.Fatalf("later queue claim error = %v, want exact ErrNotFound", err)
			}
		})
	}
}

func TestImportFailureCancelsQueuedImportSuffix(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "b3a")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "import-failure-suffix", 1, 2, 3)
	decision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := store.FinalizeImportTerminalForClaim(ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: decision.Claim, Status: jobs.StatusFailed,
		Error: &jobs.Error{Code: "invalid_import", Message: "volume 1 failed"},
	})
	if err != nil || committed != jobs.StatusFailed {
		t.Fatalf("FinalizeImportTerminalForClaim() = %s, %v", committed, err)
	}

	var queueStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM import_queues WHERE project_id=$1 AND id=$2`, projectID, queue.ID).Scan(&queueStatus); err != nil {
		t.Fatal(err)
	}
	if queueStatus != string(library.ImportQueueFailed) {
		t.Fatalf("queue status = %s, want failed", queueStatus)
	}
	for index, item := range queue.Items {
		var jobStatus, requestStatus string
		var bodyPresent bool
		if err := db.Pool.QueryRow(ctx, `
			SELECT job.status, request.status, upload.content IS NOT NULL
			FROM jobs AS job
			JOIN import_job_requests AS request
			  ON request.project_id=job.project_id AND request.job_id=job.id
			JOIN import_uploads AS upload
			  ON upload.project_id=request.project_id AND upload.storage_key=request.storage_key
			WHERE job.project_id=$1 AND job.id=$2
		`, projectID, item.ImportJobID).Scan(&jobStatus, &requestStatus, &bodyPresent); err != nil {
			t.Fatal(err)
		}
		wantJob, wantRequest := string(jobs.StatusCanceled), string(jobs.StatusCanceled)
		if index == 0 {
			wantJob, wantRequest = string(jobs.StatusFailed), string(jobs.StatusFailed)
		}
		if jobStatus != wantJob || requestStatus != wantRequest || bodyPresent {
			t.Fatalf("item %d state = %s/%s/body:%t, want %s/%s/body:false", index+1, jobStatus, requestStatus, bodyPresent, wantJob, wantRequest)
		}
	}
}
