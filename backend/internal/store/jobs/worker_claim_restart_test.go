package jobstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
)

type restartClaimFixture struct {
	ctx       context.Context
	db        *storedb.DB
	store     *Store
	projectID string
}

func newRestartClaimFixture(t *testing.T) restartClaimFixture {
	t.Helper()
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-00000000d101"
	projectID := "00000000-0000-4000-8000-00000000d102"
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "restart-claims@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restart Claims", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-00000000d103", projectID, actorID, projects.RoleOwner, now, false)
	fixture := restartClaimFixture{ctx: ctx, db: db, store: New(db), projectID: projectID}
	return fixture
}

// seedRunning creates a running canon_text_index Job of claim incarnation 1, claimed and
// heartbeated at claimedAgo before the database clock.
func (fixture restartClaimFixture) seedRunning(t *testing.T, jobID string, status jobs.Status, claimedAgo time.Duration) jobs.WorkerClaim {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := fixture.store.createJobForTest(fixture.ctx, jobs.CreateJobStoreInput{
		ID: jobID, ProjectID: fixture.projectID, Kind: jobs.KindCanonTextIndex, Status: jobs.StatusRunning,
		Subject:       workerClaimSubjectForTest(jobs.KindCanonTextIndex, fixture.projectID),
		CreatorUserID: "00000000-0000-4000-8000-00000000d101", CanCancel: true,
		QueueClass: jobs.QueueClassUserDemanded, MaxAttempts: jobs.MaxJobAttempts,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE jobs
		SET status = $2, claim_incarnation = 1,
		    claimed_at = clock_timestamp() - make_interval(secs => $3::double precision),
		    updated_at = clock_timestamp(),
		    cancel_requested_at = CASE WHEN $2 = 'canceling' THEN clock_timestamp() END
		WHERE id = $1
	`, jobID, string(status), claimedAgo.Seconds()); err != nil {
		t.Fatal(err)
	}
	return jobs.WorkerClaim{ProjectID: fixture.projectID, JobID: jobID, ClaimIncarnation: 1}
}

// R1.1: a claim made before the process started is free at once, by the stale-claim rules.
func TestClaimNextQueuedJobFreesClaimOfEarlierProcess(t *testing.T) {
	fixture := newRestartClaimFixture(t)
	orphanID := "00000000-0000-4000-8000-00000000d111"
	fixture.seedRunning(t, orphanID, jobs.StatusRunning, time.Minute)

	authority := newWorkerClaimAuthorityForTest(fixture.db)
	if err := authority.releaseClaimsOfEarlierProcesses(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	liveID := "00000000-0000-4000-8000-00000000d112"
	fixture.seedRunning(t, liveID, jobs.StatusRunning, 0)

	decision, err := authority.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	if err != nil {
		t.Fatalf("claim of the earlier process claim: %v", err)
	}
	if decision.Claim.JobID != orphanID || decision.Claim.ClaimIncarnation != 2 || decision.ExhaustedStatus != "" {
		t.Fatalf("decision = %+v, want the orphan at incarnation 2", decision)
	}
	var retryCount int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT retry_count FROM jobs WHERE id = $1`, orphanID).Scan(&retryCount); err != nil {
		t.Fatal(err)
	}
	if retryCount != 1 {
		t.Fatalf("orphan retry_count = %d, want 1: a crash counts as an attempt", retryCount)
	}
	if _, err := authority.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindCanonTextIndex}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("claim after the orphan = %v, want ErrNotFound: a claim of this process stays", err)
	}
}

// R1.1: without the process start the old 15-minute rule alone applies; a canceling Job without
// a claim (a queued Job whose cancel is in flight) is never freed by the process start.
func TestClaimNextQueuedJobKeepsUnclaimedAndRecentClaims(t *testing.T) {
	fixture := newRestartClaimFixture(t)
	recentID := "00000000-0000-4000-8000-00000000d121"
	fixture.seedRunning(t, recentID, jobs.StatusRunning, time.Minute)
	if _, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(
		fixture.ctx, []jobs.Kind{jobs.KindCanonTextIndex},
	); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("claim without the process start = %v, want ErrNotFound", err)
	}

	unclaimedID := "00000000-0000-4000-8000-00000000d122"
	fixture.seedRunning(t, unclaimedID, jobs.StatusCanceling, time.Minute)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE jobs SET claimed_at = NULL WHERE id = $1`, unclaimedID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE jobs SET claimed_at = clock_timestamp() WHERE id = $1`, recentID); err != nil {
		t.Fatal(err)
	}
	authority := newWorkerClaimAuthorityForTest(fixture.db)
	if err := authority.releaseClaimsOfEarlierProcesses(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE jobs SET claimed_at = clock_timestamp() WHERE id = $1`, recentID); err != nil {
		t.Fatal(err)
	}
	if decision, err := authority.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindCanonTextIndex}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("claim = %+v, %v, want ErrNotFound", decision, err)
	}
}

// R1.2: a graceful stop returns the exact running claim to the queue without spending an attempt.
func TestReleaseWorkerClaimRequeuesWithoutRetry(t *testing.T) {
	fixture := newRestartClaimFixture(t)
	jobID := "00000000-0000-4000-8000-00000000d131"
	claim := fixture.seedRunning(t, jobID, jobs.StatusRunning, 0)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE jobs SET retry_count = 1 WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}

	released, err := fixture.store.ReleaseWorkerClaim(fixture.ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != jobs.StatusQueued || released.RetryCount != 1 || released.WorkerClaim.ClaimIncarnation != 2 {
		t.Fatalf("released = status %q retry %d claim %+v", released.Status, released.RetryCount, released.WorkerClaim)
	}
	var claimedAtNull, due bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT claimed_at IS NULL, next_attempt_at <= clock_timestamp() FROM jobs WHERE id = $1
	`, jobID).Scan(&claimedAtNull, &due); err != nil {
		t.Fatal(err)
	}
	if !claimedAtNull || !due {
		t.Fatalf("released claimed_at NULL = %v, due now = %v", claimedAtNull, due)
	}
	if _, err := fixture.store.ReleaseWorkerClaim(fixture.ctx, claim); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("second release of the old claim = %v, want ErrConflict", err)
	}
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	if err != nil || decision.Claim.JobID != jobID || decision.Claim.ClaimIncarnation != 3 {
		t.Fatalf("claim after release = %+v, %v", decision, err)
	}
}

// R1.2: a Job with a cancel request is not returned to the queue.
func TestReleaseWorkerClaimLeavesCancelingJob(t *testing.T) {
	fixture := newRestartClaimFixture(t)
	jobID := "00000000-0000-4000-8000-00000000d141"
	claim := fixture.seedRunning(t, jobID, jobs.StatusCanceling, 0)
	if _, err := fixture.store.ReleaseWorkerClaim(fixture.ctx, claim); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("release of a canceling Job = %v, want ErrConflict", err)
	}
	if got := jobStatusForStoreTest(t, fixture.db, jobID); got != jobs.StatusCanceling {
		t.Fatalf("status = %q, want canceling", got)
	}
}

// R1.2: a released chat turn waits in the queue with the waiting phase.
func TestReleaseWorkerClaimPutsChatTurnInWaitingPhase(t *testing.T) {
	fixture := newModelTerminalFixture(t, jobs.StatusRunning)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE jobs SET progress_json = '{"phase":"working"}'::jsonb WHERE id = $1
	`, fixture.jobID); err != nil {
		t.Fatal(err)
	}
	released, err := New(fixture.db).ReleaseWorkerClaim(fixture.ctx, jobs.WorkerClaim{
		ProjectID: fixture.projectID, JobID: fixture.jobID, ClaimIncarnation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != jobs.StatusQueued || released.Phase == nil ||
		*released.Phase != jobs.ResearchChatPhaseWaiting || released.RetryCount != 0 {
		t.Fatalf("released chat turn = status %q phase %v retry %d", released.Status, released.Phase, released.RetryCount)
	}
}
