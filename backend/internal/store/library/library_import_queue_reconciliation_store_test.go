package librarystore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
)

func TestReconcileTerminalImportQueueJobsUsesImportTerminalStateMachine(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "b3b")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "legacy-terminal-queue", 1, 2, 3)
	if _, err := db.Pool.Exec(ctx, `
		UPDATE import_queues
		SET status='failed', version=version+1, completed_at=clock_timestamp(), updated_at=clock_timestamp()
		WHERE project_id=$1 AND id=$2 AND status='running'
	`, projectID, queue.ID); err != nil {
		t.Fatal(err)
	}

	reconciled, err := store.ReconcileTerminalImportQueueJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled != int64(len(queue.Items)) {
		t.Fatalf("reconciled jobs = %d, want %d", reconciled, len(queue.Items))
	}
	var queueStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM import_queues WHERE project_id=$1 AND id=$2`, projectID, queue.ID).Scan(&queueStatus); err != nil {
		t.Fatal(err)
	}
	if queueStatus != string(library.ImportQueueFailed) {
		t.Fatalf("queue status = %s, want failed", queueStatus)
	}
	for _, item := range queue.Items {
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
		if jobStatus != string(jobs.StatusCanceled) || requestStatus != string(jobs.StatusCanceled) || bodyPresent {
			t.Fatalf("reconciled state = %s/%s/body:%t, want canceled/canceled/false", jobStatus, requestStatus, bodyPresent)
		}
	}
	if second, err := store.ReconcileTerminalImportQueueJobs(ctx); err != nil || second != 0 {
		t.Fatalf("second reconciliation = %d, %v, want 0", second, err)
	}

}
