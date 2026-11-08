package deletion

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/authormemory"
	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	authormemoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-01-writer-assistant-index.md, R4.6: the project purge removes the records of the author with
// the project row.
func TestProjectHardPurgeRemovesTheMemoryOfTheAuthor(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_purge_author_memory")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000021"
		projectID = "10000000-0000-4000-8000-000000000022"
		tombstone = "10000000-0000-4000-8000-000000000024"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, 'memory-purge@example.test', 'Author', 'hash', clock_timestamp())
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($2, $1, 'Memory Project', '', clock_timestamp(), clock_timestamp())
	`, userID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := authormemoryrepo.CreateInTx(ctx, tx, projectID, userID, authormemory.Draft{Kind: authormemory.KindPlan, Text: "Кира узнает правду"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// specs/2026-11-04-saga-map.md R9.1: the materials of the author go the same way.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO author_materials (project_id, name, name_key, kind, source_kind, created_by, updated_by)
		VALUES ($1, 'Ужас', 'ужас', 'skill', 'text', $2, $2)
	`, projectID, userID); err != nil {
		t.Fatal(err)
	}

	outcome := purgeProjectForTest(t, db, projectID, tombstone, now)
	var left int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM author_memory WHERE project_id=$1) + (SELECT count(*) FROM author_materials WHERE project_id=$1)
	`, projectID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if outcome != deletionservice.PostgresStepSucceeded || left != 0 {
		t.Fatalf("the project purge = outcome:%s records left:%d", outcome, left)
	}
}

// purgeProjectForTest runs the step of the project hard purge that deletes its row.
func purgeProjectForTest(t *testing.T, db *storedb.DB, projectID, tombstone string, now time.Time) deletionservice.PostgresStepOutcome {
	t.Helper()
	ctx := t.Context()
	leaseExpiresAt := now.Add(5 * time.Minute)
	deadline, err := governance.PresentPurgeDeadline(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	vector, err := governance.ProjectDeletionVector(governance.SourceGenerationVector{ProjectID: projectID, ProjectGeneration: 1})
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
	steps, err := deletiondomain.PostgresStepRegistry(governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge)
	if err != nil {
		t.Fatal(err)
	}
	var purge *deletiondomain.PostgresStep
	for index := range steps {
		if steps[index].Table == "projects" && steps[index].Operation == deletiondomain.PostgresOperationDelete {
			purge = &steps[index]
		}
	}
	if purge == nil {
		t.Fatal("the project hard-purge registry deletes no project row")
	}

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
	}
	for _, step := range steps {
		if step.Order < purge.Order {
			statements = append(statements, struct {
				query string
				args  []any
			}{`
				INSERT INTO deletion_target_step_progress (
					tombstone_id, project_id, target, step_name, step_order, cursor_kind,
					state, claim_incarnation, claimed_by_digest, cursor_digest,
					processed_count, started_at, updated_at, completed_at
				) VALUES ($1, $2, 'postgresql', $3, $4, $5, 'succeeded', 1, $6, $6, 0, $7, $7, $7)
			`, []any{tombstone, projectID, step.Name, step.Order, string(step.CursorKind), owner[:], now}})
		}
	}
	statements = append(statements, struct {
		query string
		args  []any
	}{`
		INSERT INTO deletion_target_step_progress (
			tombstone_id, project_id, target, step_name, step_order, cursor_kind, state, claim_incarnation,
			claimed_by_digest, processed_count, started_at, updated_at
		) VALUES ($1, $2, 'postgresql', $3, $4, $5, 'running', 1, $6, 0, $7, $7)
	`, []any{tombstone, projectID, purge.Name, purge.Order, string(purge.CursorKind), owner[:], now}})
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

	outcome, err := New(db).RunPostgresDeletionStep(ctx, request, *purge)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}
