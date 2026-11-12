package jobstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/testutil"
)

func TestDormantModelKindRemainsFailClosed(t *testing.T) {
	t.Run("queued", func(t *testing.T) {
		fixture := newModelTerminalFixture(t, jobs.StatusQueued)
		authority := newWorkerClaimAuthorityForTest(fixture.db)
		before := modelRetrySnapshot(t, fixture)
		for _, kind := range []jobs.Kind{jobs.KindCanonTextIndex, jobs.KindImport} {
			if _, err := authority.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{kind}); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("ClaimNextQueuedJobForWorker(%q) error = %v, want ErrNotFound", kind, err)
			}
			assertModelRetrySnapshot(t, fixture, before)
		}
	})

}

func TestWorkerClaimSQLKeepsProductPredicatesOutOfGenericScheduler(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	schema := testutil.IsolatedPostgresSchema(t, db)
	var definition string
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT pg_get_functiondef(procedure.oid)
		FROM pg_catalog.pg_proc AS procedure
		JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
		WHERE namespace.nspname = $1
		  AND procedure.proname = 'claim_next_queued_job_for_worker'
	`, schema+"_worker").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"public.import_queue_items", "public.books", "public.book_source_members",
		"result_json", "chapterSummariesComplete", "graphActivated",
	} {
		if strings.Contains(definition, forbidden) {
			t.Fatalf("generic worker claim contains product predicate %q: %s", forbidden, definition)
		}
	}
}

func TestContextClaimFunctionsPreserveQueueProgression(t *testing.T) {
	t.Run("import_queue", TestQueueClaimWaitsForPredecessorBookReady)
	t.Run("terminal_exhausted_claim", func(t *testing.T) {
		ctx := context.Background()
		db := openCurrentStoreTestDB(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		actorID := "00000000-0000-4000-8000-00000000c511"
		projectID := "00000000-0000-4000-8000-00000000c512"
		membershipID := "00000000-0000-4000-8000-00000000c513"
		jobID := "00000000-0000-4000-8000-00000000c514"
		insertProjectStoreUserForTest(t, db, actorID, "claim-exhausted@example.test")
		insertProjectStoreProjectForTest(t, db, projectID, actorID, "Claim Exhausted", now)
		insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
		if _, err := New(db).createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID: jobID, ProjectID: projectID, Kind: jobs.KindCanonTextIndex, Status: jobs.StatusQueued,
			Subject: workerClaimSubjectForTest(jobs.KindCanonTextIndex, projectID), CreatorUserID: actorID,
			QueueClass: jobs.QueueClassUserDemanded, MaxAttempts: jobs.MaxJobAttempts,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		authority := newWorkerClaimAuthorityForTest(db)
		first, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
		if err != nil || first.Claim.JobID != jobID || first.Claim.ClaimIncarnation != 1 {
			t.Fatalf("first claim = %#v, err = %v", first, err)
		}
		for _, test := range []struct {
			status jobs.Status
			want   jobs.Status
		}{
			{status: jobs.StatusRunning, want: jobs.StatusFailed},
			{status: jobs.StatusCanceling, want: jobs.StatusCanceled},
		} {
			if _, err := db.Pool.Exec(ctx, `
				UPDATE jobs
				SET status=$3, retry_count=2, claim_incarnation=1,
				    claimed_at=clock_timestamp() - interval '16 minutes',
				    updated_at=clock_timestamp() - interval '16 minutes'
				WHERE project_id=$1 AND id=$2
			`, projectID, jobID, string(test.status)); err != nil {
				t.Fatal(err)
			}
			decision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
			if err != nil || decision.ExhaustedStatus != test.want ||
				decision.Claim.JobID != jobID || decision.Claim.ClaimIncarnation != 2 {
				t.Fatalf("%s exhausted claim = %#v, err = %v; want %s", test.status, decision, err, test.want)
			}
		}
	})
}

func TestCanonTextIndexClaimRequiresExplicitAllowlistAndReclaimsStaleRunning(t *testing.T) {
	t.Run("queued_allowlist", func(t *testing.T) {
		fixture := newCanonTextIntentFixture(t)
		jobID := "20000000-0000-4000-8000-000000000025"
		if _, err := fixture.upsert(t, fixture.input(t, jobID, governance.SourceViewDraft, 1, 1, fixture.userID)); err != nil {
			t.Fatal(err)
		}
		authority := newWorkerClaimAuthorityForTest(fixture.db)
		if _, err := authority.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport}); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("claim without canonical allowlist error = %v, want ErrNotFound", err)
		}
		decision, err := authority.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindCanonTextIndex})
		if err != nil || decision.Claim.JobID != jobID {
			t.Fatalf("canonical claim = %#v, %v", decision, err)
		}
	})

	t.Run("stale_running", func(t *testing.T) {
		fixture := newCanonTextIndexRetryFixture(t)
		if _, err := fixture.db.Pool.Exec(context.Background(), `
			UPDATE jobs
			SET claimed_at=clock_timestamp() - interval '16 minutes',
			    updated_at=clock_timestamp() - interval '16 minutes'
			WHERE project_id=$1 AND id=$2
		`, fixture.projectID, fixture.claim.JobID); err != nil {
			t.Fatal(err)
		}
		decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(context.Background(), []jobs.Kind{jobs.KindCanonTextIndex})
		if err != nil || decision.Claim.JobID != fixture.claim.JobID || decision.Claim.ClaimIncarnation != 2 {
			t.Fatalf("stale canonical claim = %#v, %v", decision, err)
		}
	})
}

func TestWorkerClaimUsesDBClockExactKindAndIncarnation(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	authority := newWorkerClaimAuthorityForTest(db)
	createdAt := time.Date(2020, 7, 13, 12, 0, 0, 0, time.UTC)
	actorID := "00000000-0000-4000-8000-000000004001"
	projectID := "00000000-0000-4000-8000-000000004002"
	membershipID := "00000000-0000-4000-8000-000000004003"
	textIndexJobID := "00000000-0000-4000-8000-000000004004"
	importJobID := "00000000-0000-4000-8000-000000004005"

	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "worker-claim@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Worker Claim", createdAt)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, createdAt, false)
	for _, fixture := range []struct {
		id   string
		kind jobs.Kind
	}{
		{id: textIndexJobID, kind: jobs.KindCanonTextIndex},
		{id: importJobID, kind: jobs.KindImport},
	} {
		if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID: fixture.id, ProjectID: projectID, Kind: fixture.kind,
			Status: jobs.StatusQueued, Subject: workerClaimSubjectForTest(fixture.kind, projectID),
			CreatorUserID: actorID, QueueClass: jobs.QueueClassUserDemanded, MaxAttempts: 3,
			NextAttemptAt: createdAt, CreatedAt: createdAt, UpdatedAt: createdAt,
		}); err != nil {
			t.Fatalf("CreateJob(%s): %v", fixture.id, err)
		}
	}

	claimDecision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	claim := claimDecision.Claim
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker(): %v", err)
	}
	if claim.ProjectID != projectID || claim.JobID != textIndexJobID || claim.ClaimIncarnation != 1 {
		t.Fatalf("claim = %#v", claim)
	}
	loaded, err := store.LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		t.Fatalf("LoadJobForWorkerClaim(): %v", err)
	}
	if loaded.ID != textIndexJobID || loaded.Kind != jobs.KindCanonTextIndex || loaded.Status != jobs.StatusRunning || loaded.WorkerClaim != claim {
		t.Fatalf("loaded job = %#v", loaded)
	}
	if got := jobStatusForStoreTest(t, db, importJobID); got != jobs.StatusQueued {
		t.Fatalf("different kind status = %q, want queued", got)
	}
	if _, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{"unknown"}); !errors.Is(err, jobs.ErrValidation) {
		t.Fatalf("unknown kind claim error = %v, want ErrValidation", err)
	}

	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET updated_at = clock_timestamp() - interval '16 minutes' WHERE id = $1`, textIndexJobID); err != nil {
		t.Fatal(err)
	}
	reclaimedDecision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	reclaimed := reclaimedDecision.Claim
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.JobID != textIndexJobID || reclaimed.ClaimIncarnation != 2 {
		t.Fatalf("reclaimed = %#v", reclaimed)
	}
	if err := store.HeartbeatWorkerClaim(ctx, claim); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("old heartbeat error = %v, want ErrConflict", err)
	}
	if err := store.HeartbeatWorkerClaim(ctx, reclaimed); err != nil {
		t.Fatalf("current heartbeat: %v", err)
	}
	if _, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("fresh heartbeat job was reclaimable: %v", err)
	}
}

func TestQueueClaimWaitsForPredecessorBookReady(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	actorID := "00000000-0000-4000-8000-000000004101"
	projectID := "00000000-0000-4000-8000-000000004102"
	otherProjectID := "00000000-0000-4000-8000-000000004103"
	queueID := "00000000-0000-4000-8000-000000004104"
	firstItemID := "00000000-0000-4000-8000-000000004105"
	secondItemID := "00000000-0000-4000-8000-000000004106"
	firstBookID := "00000000-0000-4000-8000-000000004107"
	firstJobID := "00000000-0000-4000-8000-000000004108"
	secondJobID := "00000000-0000-4000-8000-000000004109"
	legacyJobID := "00000000-0000-4000-8000-00000000410a"

	insertProjectStoreUserForTest(t, db, actorID, "queue-claim@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Queue Claim", now)
	insertProjectStoreProjectForTest(t, db, otherProjectID, actorID, "Other Queue Claim", now.Add(time.Second))
	insertBookForExportStoreTest(t, db, firstBookID, projectID, "Queue Volume 3", now)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO import_queues (
			id, project_id, created_by_user_id, idempotency_key, request_fingerprint,
			status, version, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 'queue-claim-idem', 'queue-claim-fingerprint', 'running', 2, $4, $4, $4)
	`, queueID, projectID, actorID, now); err != nil {
		t.Fatal(err)
	}

	store := New(db)
	for _, job := range []struct {
		id        string
		projectID string
		createdAt time.Time
	}{
		{id: firstJobID, projectID: projectID, createdAt: now},
		{id: secondJobID, projectID: projectID, createdAt: now.Add(time.Millisecond)},
		{id: legacyJobID, projectID: otherProjectID, createdAt: now.Add(2 * time.Millisecond)},
	} {
		if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID: job.id, ProjectID: job.projectID, Kind: jobs.KindImport, Status: jobs.StatusQueued,
			Subject:       jobs.ImportSubject{Type: jobs.KindImport, ID: job.projectID, ProjectID: job.projectID},
			CreatorUserID: actorID, QueueClass: jobs.QueueClassUserDemanded, MaxAttempts: 3,
			NextAttemptAt: job.createdAt, CreatedAt: job.createdAt, UpdatedAt: job.createdAt,
		}); err != nil {
			t.Fatalf("create import job %s: %v", job.id, err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET subject_json = subject_json || jsonb_build_object(
			'importQueueId', $2::text,
			'importQueueItemId', CASE id WHEN $3 THEN $4::text ELSE $5::text END,
			'expectedBookOrder', CASE id WHEN $3 THEN 3 ELSE 4 END
		)
		WHERE project_id = $1 AND id IN ($6, $7)
	`, projectID, queueID, firstJobID, firstItemID, secondItemID, firstJobID, secondJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO import_queue_items (
			id, queue_id, project_id, client_item_id, position, source_file_name,
			confirmed_book_order, preflight_status, import_job_id, book_id, created_at, updated_at
		)
		VALUES
			($1, $3, $4, 'first', 1, 'volume-3.fb2', 3, 'ready', $5, $7, $8, $8),
			($2, $3, $4, 'second', 2, 'volume-4.fb2', 4, 'ready', $6, NULL, $8, $8)
	`, firstItemID, secondItemID, queueID, projectID, firstJobID, secondJobID, firstBookID, now); err != nil {
		t.Fatal(err)
	}

	authority := newWorkerClaimAuthorityForTest(db)
	claimImport := func() (jobs.WorkerClaim, error) {
		decision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		return decision.Claim, err
	}
	first, err := claimImport()
	if err != nil || first.JobID != firstJobID {
		t.Fatalf("first queue claim = %#v, err = %v", first, err)
	}
	legacy, err := claimImport()
	if err != nil || legacy.JobID != legacyJobID || legacy.ProjectID != otherProjectID {
		t.Fatalf("legacy other-project claim = %#v, err = %v", legacy, err)
	}
	if _, err := claimImport(); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("second queue item while predecessor runs error = %v, want ErrNotFound", err)
	}

	// An imported predecessor holds the next volume back until its book is read (R2.4).
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status = 'succeeded' WHERE id = $1`, firstJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := claimImport(); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("second claim after predecessor succeeded error = %v, want ErrNotFound", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE books SET status = 'ready' WHERE id = $1`, firstBookID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status = 'failed' WHERE id = $1`, firstJobID); err != nil {
		t.Fatal(err)
	}
	// A predecessor that did not read waits for the author at its place and holds nothing back
	// (specs/2026-11-11-ux7-upload.md UP3.2).
	second, err := claimImport()
	if err != nil || second.JobID != secondJobID {
		t.Fatalf("second claim after predecessor did not read = %#v, err = %v", second, err)
	}
}

func workerClaimSubjectForTest(kind jobs.Kind, projectID string) jobs.Subject {
	if kind == jobs.KindImport {
		return jobs.ImportSubject{Type: kind, ID: projectID, ProjectID: projectID}
	}
	return jobs.CanonTextIndexSubject{
		Type: kind, ProjectID: projectID, SourceView: governance.SourceViewDraft, CatalogRevision: 1,
	}
}

// R3.8: a chat turn whose heartbeat lapsed (backend stopped mid-turn) is reclaimed with a
// new claim incarnation instead of staying running forever.
func TestStaleResearchChatTurnIsReclaimed(t *testing.T) {
	for _, status := range []jobs.Status{jobs.StatusRunning, jobs.StatusCanceling} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newModelTerminalFixture(t, status)
			turnJobID := fixture.jobID
			if _, err := fixture.db.Pool.Exec(fixture.ctx, `
				UPDATE jobs SET updated_at = clock_timestamp() - interval '16 minutes'
				WHERE project_id=$1 AND id=$2
			`, fixture.projectID, turnJobID); err != nil {
				t.Fatal(err)
			}
			decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(
				fixture.ctx, []jobs.Kind{jobs.KindResearchChatTurn},
			)
			if err != nil || decision.Claim.JobID != turnJobID || decision.Claim.ClaimIncarnation != 2 || decision.ExhaustedStatus != "" {
				t.Fatalf("stale %s chat turn claim = %#v, %v", status, decision, err)
			}
			claimed, err := New(fixture.db).LoadJobForWorkerClaim(fixture.ctx, decision.Claim)
			if err != nil || claimed.Status != status || claimed.RetryCount != 1 {
				t.Fatalf("reclaimed chat turn = %#v, %v", claimed, err)
			}
		})
	}
}
