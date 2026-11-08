package jobstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
)

func TestImportQueueInfrastructureRetryStopsOnExactItem(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	actorID := "00000000-0000-4000-8000-00000000c501"
	projectID := "00000000-0000-4000-8000-00000000c502"
	queueID := "00000000-0000-4000-8000-00000000c503"
	firstItemID := "00000000-0000-4000-8000-00000000c504"
	secondItemID := "00000000-0000-4000-8000-00000000c505"
	firstJobID := "00000000-0000-4000-8000-00000000c506"
	secondJobID := "00000000-0000-4000-8000-00000000c507"

	insertProjectStoreUserForTest(t, db, actorID, "queue-retry@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Queue Retry", now)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO import_queues (
			id, project_id, created_by_user_id, idempotency_key, request_fingerprint,
			status, version, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 'queue-retry-idem', 'queue-retry-fingerprint', 'running', 2, $4, $4, $4)
	`, queueID, projectID, actorID, now); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	for _, jobID := range []string{firstJobID, secondJobID} {
		if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID: jobID, ProjectID: projectID, Kind: jobs.KindImport, Status: jobs.StatusQueued,
			Subject:       jobs.ImportSubject{Type: jobs.KindImport, ID: projectID, ProjectID: projectID},
			CreatorUserID: actorID, QueueClass: jobs.QueueClassUserDemanded, MaxAttempts: jobs.MaxJobAttempts,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO import_queue_items (
			id, queue_id, project_id, client_item_id, position, source_file_name,
			confirmed_book_order, preflight_status, import_job_id, created_at, updated_at
		)
		VALUES
			($1, $3, $4, 'first', 1, 'volume-1.fb2', 1, 'ready', $5, $6, $6),
			($2, $3, $4, 'second', 2, 'volume-2.fb2', 2, 'ready', $7, $6, $6)
	`, firstItemID, secondItemID, queueID, projectID, firstJobID, now, secondJobID); err != nil {
		t.Fatal(err)
	}

	authority := newWorkerClaimAuthorityForTest(db)
	decision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != firstJobID {
		t.Fatalf("first claim = %#v, err = %v", decision.Claim, err)
	}
	requeued, err := store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: decision.Claim, Kind: jobs.KindImport, ExpectedRetryCount: 0,
	})
	if err != nil || requeued.Status != jobs.StatusQueued || requeued.RetryCount != 1 {
		t.Fatalf("requeue = %#v, err = %v, want queued retry 1", requeued, err)
	}
	if _, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("second claim during retry error = %v, want ErrNotFound", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs SET next_attempt_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND project_id = $2
	`, firstJobID, projectID); err != nil {
		t.Fatal(err)
	}
	reclaimedDecision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || reclaimedDecision.Claim.JobID != firstJobID {
		t.Fatalf("reclaimed first = %#v, err = %v", reclaimedDecision.Claim, err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs SET retry_count = $3
		WHERE id = $1 AND project_id = $2
	`, firstJobID, projectID, jobs.MaxJobAttempts-1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: reclaimedDecision.Claim, Kind: jobs.KindImport,
		ExpectedRetryCount: jobs.MaxJobAttempts - 1,
	}); !errors.Is(err, jobs.ErrRetryExhausted) {
		t.Fatalf("exhausted requeue error = %v, want ErrRetryExhausted", err)
	}
	if _, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("second claim after exhaustion error = %v, want ErrNotFound", err)
	}
}
