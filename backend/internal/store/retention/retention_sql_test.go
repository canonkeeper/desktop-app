package retentionstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	retentionUserID       = "b0000000-0000-4000-8000-000000000001"
	retentionProjectID    = "b0000000-0000-4000-8000-000000000002"
	retentionDerivedID    = "b0000000-0000-4000-8000-000000000006"
	retentionChatProofID  = "b0000000-0000-4000-8000-000000000007"
	retentionChatID       = "b0000000-0000-4000-8000-000000000008"
	retentionPermissionID = "b0000000-0000-4000-8000-000000000009"
)

func TestSourceHardPurgeEnqueueFencesCurrentVectorAndIsIdempotent(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedRetentionProjectForTest(t, db, true, now)
	seedRetentionPurgedDerivedProjectForTest(t, db, now)
	seedRetentionPurgedPermissionLossForTest(t, db, now)

	claim, err := claimNextSourceHardPurgeForTest(t, db)
	if err != nil {
		t.Fatalf("ClaimNextSourceHardPurgeForWorker() error = %v", err)
	}
	if claim.ProjectID != retentionProjectID || claim.EntityKind != governance.SourceEntityProject || claim.EntityID != retentionProjectID {
		t.Fatalf("source hard-purge claim = %#v", claim)
	}
	config := db.Pool.Config().Copy()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE canonkeeper_product_runtime")
		return err
	}
	runtimePool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtimePool.Close)
	store := New(&storedb.DB{Pool: runtimePool})
	applied, err := store.EnqueueSourceHardPurgeForClaim(context.Background(), claim)
	if err != nil || !applied {
		t.Fatalf("EnqueueSourceHardPurgeForClaim() = %v, %v", applied, err)
	}
	var generation int64
	var hardStartedAt *time.Time
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT lifecycle_generation, hard_purge_started_at FROM projects WHERE id = $1
	`, retentionProjectID).Scan(&generation, &hardStartedAt); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || hardStartedAt == nil {
		t.Fatalf("source fence generation/start = %d/%v, want 2/non-nil", generation, hardStartedAt)
	}
	if got := countRetentionRowsForTest(t, db, `
		SELECT count(*) FROM deletion_tombstones
		WHERE project_id = $1 AND purge_mode = 'source_hard_purge'
	`, retentionProjectID); got != 1 {
		t.Fatalf("hard purge tombstones = %d, want 1", got)
	}
	if got := countRetentionRowsForTest(t, db, `
		SELECT count(*) FROM deletion_hard_purge_predecessors WHERE project_id = $1
	`, retentionProjectID); got != 2 {
		t.Fatalf("hard purge predecessors = %d, want 2", got)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE projects
		SET deleted_at = NULL, governance_retention_started_at = NULL,
		    purge_after = NULL, purge_deadline = NULL
		WHERE id = $1
	`, retentionProjectID); err == nil {
		t.Fatal("restore unexpectedly succeeded after terminal hard-purge fence")
	}
	applied, err = store.EnqueueSourceHardPurgeForClaim(context.Background(), claim)
	if err != nil || applied {
		t.Fatalf("duplicate hard-purge enqueue = %v, %v, want no mutation", applied, err)
	}
}

func TestSourceHardPurgeRejectsReceiptThatDoesNotMatchTerminalTargets(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedRetentionProjectForTest(t, db, true, now)
	seedRetentionPurgedDerivedProjectForTest(t, db, now)
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE deletion_completion_receipts
		SET target_receipts_digest = decode(repeat('99', 32), 'hex')
		WHERE tombstone_id = $1 AND project_id = $2
	`, retentionDerivedID, retentionProjectID); err != nil {
		t.Fatal(err)
	}

	claim, err := claimNextSourceHardPurgeForTest(t, db)
	if err != nil {
		t.Fatalf("ClaimNextSourceHardPurgeForWorker() error = %v", err)
	}
	applied, err := New(db).EnqueueSourceHardPurgeForClaim(context.Background(), claim)
	if err != nil || applied {
		t.Fatalf("mismatched receipt enqueue = %v, %v, want no mutation", applied, err)
	}
	var hardStartedAt *time.Time
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT hard_purge_started_at FROM projects WHERE id = $1
	`, retentionProjectID).Scan(&hardStartedAt); err != nil {
		t.Fatal(err)
	}
	if hardStartedAt != nil {
		t.Fatalf("hard-purge fence = %v, want NULL", hardStartedAt)
	}
	if got := countRetentionRowsForTest(t, db, `
		SELECT count(*) FROM deletion_tombstones
		WHERE project_id = $1 AND purge_mode = 'source_hard_purge'
	`, retentionProjectID); got != 0 {
		t.Fatalf("hard-purge tombstones = %d, want 0", got)
	}
}

func TestDeletionProofGCRechecksExactTypedExpiry(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedRetentionProjectForTest(t, db, false, now)
	withRetentionReplicaRole(t, db, func(ctx context.Context, conn pgx.Tx) {
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_tombstones (
			  id, project_id, origin, entity_kind, purge_mode, entity_id,
			  chat_session_id, chat_lifecycle_epoch, status, fenced_at,
			  purge_deadline, purged_at, audit_expires_at
			) VALUES ($1, $2, 'governed_online', 'chat', 'chat_epoch', $3,
			          $3, 1, 'purged', $4::timestamptz - interval '31 days',
			          $4::timestamptz - interval '30 days', $4::timestamptz - interval '30 days 1 second', $4::timestamptz - interval '1 second')
		`, retentionChatProofID, retentionProjectID, retentionChatID, now)
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_completion_receipts (
			  tombstone_id, project_id, vector_digest, target_receipts_digest,
			  completed_at, audit_expires_at, schema_version
			) VALUES ($1, $2, decode(repeat('71',32),'hex'), decode(repeat('72',32),'hex'),
			          $3::timestamptz - interval '30 days 1 second', $3::timestamptz - interval '1 second', 'CKDeletionReceiptV1')
		`, retentionChatProofID, retentionProjectID, now)
	})

	claim, err := claimNextDeletionAuditGCForTest(t, db)
	if err != nil {
		t.Fatalf("ClaimNextDeletionAuditGCForWorker() error = %v", err)
	}
	if claim.ProjectID != retentionProjectID || claim.TombstoneID != retentionChatProofID {
		t.Fatalf("GC claim = %#v", claim)
	}
	store := New(db)
	applied, err := store.GarbageCollectDeletionProofForClaim(context.Background(), claim)
	if err != nil || !applied {
		t.Fatalf("GarbageCollectDeletionProofForClaim() = %v, %v", applied, err)
	}
	applied, err = store.GarbageCollectDeletionProofForClaim(context.Background(), claim)
	if err != nil || applied {
		t.Fatalf("duplicate proof GC = %v, %v, want no mutation", applied, err)
	}
}

func seedRetentionProjectForTest(t *testing.T, db *storedb.DB, deleted bool, now time.Time) {
	t.Helper()
	withRetentionReplicaRole(t, db, func(ctx context.Context, conn pgx.Tx) {
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO users (id, email, password_hash, display_name)
			VALUES ($1::uuid, ($1::uuid)::text || '@example.test', 'hash', 'Retention')
		`, retentionUserID)
		if !deleted {
			mustRetentionExec(t, ctx, conn, `
				INSERT INTO projects (id, owner_user_id, name, lifecycle_generation)
				VALUES ($1, $2, 'Retention', 1)
			`, retentionProjectID, retentionUserID)
			return
		}
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO projects (
			  id, owner_user_id, name, lifecycle_generation, deleted_at,
			  governance_retention_started_at, purge_after, purge_deadline
			) VALUES (
			  $1, $2, 'Deleted retention', 2, $3::timestamptz - interval '8 days 2 hours',
			  $3::timestamptz - interval '8 days 2 hours', $3::timestamptz - interval '1 day 2 hours',
			  $3::timestamptz - interval '2 hours'
			)
		`, retentionProjectID, retentionUserID, now)
	})
}

func seedRetentionPurgedDerivedProjectForTest(t *testing.T, db *storedb.DB, now time.Time) {
	t.Helper()
	withRetentionReplicaRole(t, db, func(ctx context.Context, conn pgx.Tx) {
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_tombstones (
			  id, project_id, origin, entity_kind, purge_mode, entity_id,
			  project_generation, status, fenced_at, purge_deadline, purged_at
			) VALUES ($1, $2, 'governed_online', 'project', 'derived_generation', $2,
			          1, 'purged', $3::timestamptz - interval '8 days 2 hours',
			          $3::timestamptz - interval '7 days 2 hours', $3::timestamptz - interval '7 days')
		`, retentionDerivedID, retentionProjectID, now)
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_target_tasks (
			  tombstone_id, project_id, target, state, attempts, claim_incarnation,
			  next_attempt_at, claimed_by_digest, claim_expires_at, handler_version,
			  receipt_digest, completed_at
			) VALUES
			  ($1,$2,'runtime','succeeded',1,1,$3,decode(repeat('11',32),'hex'),$3,'runtime_direct_sdk_v1',decode(repeat('13',32),'hex'),$3),
			  ($1,$2,'filesystem','succeeded',1,1,$3,decode(repeat('21',32),'hex'),$3,'filesystem_os_root_v1',decode(repeat('23',32),'hex'),$3),
			  ($1,$2,'postgresql','succeeded',1,1,$3,decode(repeat('41',32),'hex'),$3,'postgres_exact_inventory_v1',decode(repeat('43',32),'hex'),$3)
		`, retentionDerivedID, retentionProjectID, now.Add(-7*24*time.Hour).Truncate(time.Second))
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_completion_receipts (
			  tombstone_id, project_id, vector_digest, target_receipts_digest,
			  completed_at, audit_expires_at, schema_version
			)
			SELECT tombstone.id, tombstone.project_id,
			  ck_deletion_selected_vector_digest(
			    tombstone.entity_kind, tombstone.project_id, tombstone.project_generation,
			    tombstone.book_id, tombstone.book_generation, tombstone.chapter_id,
			    tombstone.chapter_generation, tombstone.chat_session_id,
			    tombstone.chat_lifecycle_epoch),
			  ck_deletion_target_receipt_set_digest(
			    tombstone.purge_mode,
			    ck_deletion_tombstone_digest(
			      tombstone.id, tombstone.origin, tombstone.purge_mode, tombstone.entity_kind,
			      tombstone.project_id, tombstone.project_generation, tombstone.book_id,
			      tombstone.book_generation, tombstone.chapter_id, tombstone.chapter_generation,
			      tombstone.chat_session_id, tombstone.chat_lifecycle_epoch,
			      tombstone.fenced_at, tombstone.purge_deadline),
			    ck_deletion_selected_vector_digest(
			      tombstone.entity_kind, tombstone.project_id, tombstone.project_generation,
			      tombstone.book_id, tombstone.book_generation, tombstone.chapter_id,
			      tombstone.chapter_generation, tombstone.chat_session_id,
			      tombstone.chat_lifecycle_epoch),
			    ARRAY[
			      decode(repeat('13',32),'hex'), decode(repeat('23',32),'hex'),
			      decode(repeat('43',32),'hex')]),
			  $3, NULL, 'CKDeletionReceiptV1'
			FROM deletion_tombstones AS tombstone
			WHERE tombstone.id = $1 AND tombstone.project_id = $2
		`, retentionDerivedID, retentionProjectID, now.Add(-7*24*time.Hour).Truncate(time.Second))
	})
}

func withRetentionReplicaRole(t *testing.T, db *storedb.DB, run func(context.Context, pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	run(ctx, tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func mustRetentionExec(t *testing.T, ctx context.Context, tx pgx.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func countRetentionRowsForTest(t *testing.T, db *storedb.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
