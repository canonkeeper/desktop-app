package deletion

import (
	"slices"
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	"canonkeeper/backend/internal/testutil"
)

func TestProjectHardPurgeReplaysSucceededJobsStepForOwnerlessJobs(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_hard_purge_jobs")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000011"
		projectID = "10000000-0000-4000-8000-000000000012"
		jobID     = "10000000-0000-4000-8000-000000000013"
		tombstone = "10000000-0000-4000-8000-000000000014"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, 'ownerless-job@example.test', 'Ownerless Job', 'hash', clock_timestamp())
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($2, $1, 'Ownerless Job Project', '', clock_timestamp(), clock_timestamp())
	`, userID, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO jobs (
			id, project_id, kind, status, progress_json, result_json, error_json,
			governance_payload_state, subject_json, creator_user_id, can_cancel,
			queue_class, retry_count, max_attempts, next_attempt_at, created_at, updated_at
		) VALUES (
			$1, $2, 'import', 'expired', '{}', NULL, NULL,
			'source_deleted', NULL, NULL, false,
			'user_demanded', 0, 3, clock_timestamp(), clock_timestamp(), clock_timestamp()
		)
	`, jobID, projectID); err != nil {
		t.Fatal(err)
	}

	leaseExpiresAt := now.Add(5 * time.Minute)
	deadline, err := governance.PresentPurgeDeadline(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	vector, err := governance.ProjectDeletionVector(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var owner governance.Digest
	for index := range owner {
		owner[index] = byte(index + 1)
	}
	request := deletiondomain.TargetRequest{
		Claim: deletiondomain.TargetClaim{
			ProjectID: projectID, TombstoneID: tombstone, Target: governance.TargetPostgreSQL,
			ClaimIncarnation: 1, LeaseOwnerDigest: owner, ObservedLeaseExpiry: leaseExpiresAt,
		},
		Tombstone: deletiondomain.Tombstone{
			ProjectID: projectID,
			Identity: governance.DeletionTombstoneInput{
				TombstoneID: tombstone, Origin: governance.DeletionOriginGovernedOnline,
				Mode: governance.PurgeModeSourceHardPurge, SelectedVector: vector,
				FencedAt: now, Deadline: deadline,
			},
			Status: deletiondomain.TombstoneProcessing,
		},
		Attempt: 1,
	}
	steps, err := deletiondomain.PostgresStepRegistry(
		governance.DeletionSelectionProject,
		governance.PurgeModeSourceHardPurge,
	)
	if err != nil {
		t.Fatal(err)
	}
	jobsIndex := slices.IndexFunc(steps, func(step deletiondomain.PostgresStep) bool { return step.Table == "jobs" })
	if jobsIndex < 0 {
		t.Fatal("project hard-purge registry has no jobs step")
	}
	jobsStep := steps[jobsIndex]
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`
			UPDATE projects
			SET deleted_at=$2, governance_retention_started_at=$2,
				purge_after=$3, purge_deadline=$4, hard_purge_started_at=$1
			WHERE id=$5
		`, []any{now, now.Add(-9 * 24 * time.Hour), now.Add(-2 * 24 * time.Hour), now.Add(-24 * time.Hour), projectID}},
		{`
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, status, fenced_at, purge_deadline, predecessor_set_digest
			) VALUES ($1, $2, 'governed_online', 'project', 'source_hard_purge', $2,
				1, 'processing', $3, $4, $5)
		`, []any{tombstone, projectID, now, now.Add(-24 * time.Hour), owner[:]}},
		{`
			INSERT INTO deletion_target_tasks (
				tombstone_id, project_id, target, state, attempts, claim_incarnation,
				next_attempt_at, claimed_by_digest, claim_expires_at
			) VALUES ($1, $2, 'postgresql', 'processing', 1, 1, $3, $4, $5)
		`, []any{tombstone, projectID, now, owner[:], leaseExpiresAt}},
		{`
			INSERT INTO deletion_target_step_progress (
				tombstone_id, project_id, target, step_name, step_order, cursor_kind,
				state, claim_incarnation, claimed_by_digest, cursor_digest,
				processed_count, started_at, updated_at, completed_at
			) VALUES ($1, $2, 'postgresql', $3, $4, $5, 'succeeded', 1, $6, $6, 0, $7, $7, $7)
		`, []any{tombstone, projectID, jobsStep.Name, jobsStep.Order, string(jobsStep.CursorKind), owner[:], now}},
	}
	var seedErr error
	for _, statement := range statements {
		if _, seedErr = conn.Exec(ctx, statement.query, statement.args...); seedErr != nil {
			break
		}
	}
	_, restoreErr := conn.Exec(ctx, `SET session_replication_role = origin`)
	conn.Release()
	if seedErr != nil {
		t.Fatal(seedErr)
	}
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}

	outcome, err := New(db).RunPostgresDeletionStep(ctx, request, jobsStep)
	if err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE project_id=$1`, projectID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if outcome != deletionservice.PostgresStepSucceeded || remaining != 0 {
		t.Fatalf("replayed project hard-purge jobs = outcome:%s remaining:%d, want succeeded/0", outcome, remaining)
	}
}
