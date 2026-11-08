package retentionstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"

	"github.com/jackc/pgx/v5"
)

func TestSourceHardPurgeCompletionPinsRetainedDerivedProofExpiry(t *testing.T) {
	tracer := &retentionCompletionQueryTracer{}
	db := openCurrentStoreTestDB(t, tracer)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedRetentionProjectForTest(t, db, true, now)
	seedRetentionPurgedDerivedProjectForTest(t, db, now)

	claim, err := claimNextSourceHardPurgeForTest(t, db)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := New(db).EnqueueSourceHardPurgeForClaim(context.Background(), claim); err != nil || !applied {
		t.Fatalf("EnqueueSourceHardPurgeForClaim() = %v, %v", applied, err)
	}

	var hardTombstoneID string
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT id
		FROM deletion_tombstones
		WHERE project_id = $1 AND purge_mode = 'source_hard_purge'
	`, retentionProjectID).Scan(&hardTombstoneID); err != nil {
		t.Fatal(err)
	}
	owner := deletionClaimDigestForRetentionTest(0x61)
	completedAt := now.Truncate(time.Second)
	expiresAt := completedAt.Add(5 * time.Minute)
	withRetentionReplicaRole(t, db, func(ctx context.Context, tx pgx.Tx) {
		mustRetentionExec(t, ctx, tx, `
			UPDATE deletion_tombstones
			SET status = 'processing', updated_at = clock_timestamp()
			WHERE id = $1 AND project_id = $2
		`, hardTombstoneID, retentionProjectID)
		mustRetentionExec(t, ctx, tx, `
			UPDATE deletion_target_tasks
			SET state = CASE target WHEN 'postgresql' THEN 'processing' ELSE 'succeeded' END,
			    attempts = 1,
			    claim_incarnation = 1,
			    next_attempt_at = $3::timestamptz,
			    claimed_by_digest = $4::bytea,
			    claim_expires_at = $5::timestamptz,
			    handler_version = CASE target
			      WHEN 'runtime' THEN 'runtime_direct_sdk_v1'
			      WHEN 'filesystem' THEN 'filesystem_os_root_v1'
			      ELSE NULL
			    END,
			    receipt_digest = CASE target
			      WHEN 'runtime' THEN decode(repeat('13', 32), 'hex')
			      WHEN 'filesystem' THEN decode(repeat('23', 32), 'hex')
			      ELSE NULL
			    END,
			    completed_at = CASE WHEN target = 'postgresql' THEN NULL ELSE $3::timestamptz END,
			    last_reason_code = NULL
			WHERE tombstone_id = $1 AND project_id = $2
		`, hardTombstoneID, retentionProjectID, completedAt, owner[:], expiresAt)
		mustRetentionExec(t, ctx, tx, `
			UPDATE deletion_target_step_progress
			SET state = 'succeeded',
			    claim_incarnation = 1,
			    claimed_by_digest = $3::bytea,
			    cursor_digest = decode(repeat('71', 32), 'hex'),
			    processed_count = 0,
			    started_at = $4::timestamptz,
			    updated_at = $4::timestamptz,
			    completed_at = $4::timestamptz
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		`, hardTombstoneID, retentionProjectID, owner[:], completedAt)
	})

	completionStore := deletionstore.New(db)
	completionClaim := deletion.TargetClaim{
		ProjectID:           retentionProjectID,
		TombstoneID:         hardTombstoneID,
		Target:              governance.TargetPostgreSQL,
		ClaimIncarnation:    1,
		LeaseOwnerDigest:    owner,
		ObservedLeaseExpiry: expiresAt,
	}
	completionResult := deletion.TargetResult{
		Disposition:    deletion.DispositionPurged,
		HandlerVersion: governance.HandlerPostgresExactInventoryV1,
		Reason:         governance.ReasonPurged,
	}
	tracer.reset()
	_, err = completionStore.CompleteDeletionTargetForClaim(context.Background(), completionClaim, completionResult)
	if err != nil {
		t.Fatalf("complete PostgreSQL target: %v", err)
	}
	lockIndex := tracer.indexContaining("pg_advisory_xact_lock(hashtextextended")
	timestampIndex := tracer.indexContaining("CurrentDeletionTimestampSecond")
	if lockIndex < 0 || timestampIndex < 0 || lockIndex > timestampIndex {
		t.Fatalf("completion query order lock/timestamp = %d/%d, want lock first", lockIndex, timestampIndex)
	}

	var hardPurgedAt, hardExpiry, derivedExpiry, derivedReceiptExpiry, hardReceiptExpiry time.Time
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT hard.purged_at, hard.audit_expires_at, derived.audit_expires_at,
		       derived_receipt.audit_expires_at, hard_receipt.audit_expires_at
		FROM deletion_tombstones AS hard
		JOIN deletion_tombstones AS derived
		  ON derived.id = $2 AND derived.project_id = hard.project_id
		JOIN deletion_completion_receipts AS derived_receipt
		  ON derived_receipt.tombstone_id = derived.id AND derived_receipt.project_id = derived.project_id
		JOIN deletion_completion_receipts AS hard_receipt
		  ON hard_receipt.tombstone_id = hard.id AND hard_receipt.project_id = hard.project_id
		WHERE hard.id = $1 AND hard.project_id = $3
	`, hardTombstoneID, retentionDerivedID, retentionProjectID).Scan(
		&hardPurgedAt, &hardExpiry, &derivedExpiry, &derivedReceiptExpiry, &hardReceiptExpiry,
	); err != nil {
		t.Fatal(err)
	}
	wantExpiry := hardPurgedAt.Add(30 * 24 * time.Hour)
	if !hardExpiry.Equal(wantExpiry) || !derivedExpiry.Equal(wantExpiry) ||
		!derivedReceiptExpiry.Equal(wantExpiry) || !hardReceiptExpiry.Equal(wantExpiry) {
		t.Fatalf("proof expiry = hard %v, derived %v/%v, hard receipt %v; want %v",
			hardExpiry, derivedExpiry, derivedReceiptExpiry, hardReceiptExpiry, wantExpiry)
	}
}

type retentionCompletionQueryTracer struct {
	queries []string
}

func (tracer *retentionCompletionQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	tracer.queries = append(tracer.queries, strings.TrimSpace(data.SQL))
	return ctx
}

func (*retentionCompletionQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func (tracer *retentionCompletionQueryTracer) reset() {
	tracer.queries = nil
}

func (tracer *retentionCompletionQueryTracer) indexContaining(part string) int {
	for index, query := range tracer.queries {
		if strings.Contains(query, part) {
			return index
		}
	}
	return -1
}

func deletionClaimDigestForRetentionTest(value byte) governance.Digest {
	var digest governance.Digest
	for index := range digest {
		digest[index] = value
	}
	return digest
}
