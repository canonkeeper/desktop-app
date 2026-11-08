package jobstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	storedb "canonkeeper/backend/internal/store/db"
)

const (
	deletionClaimProjectID   = "a1111111-1111-4111-8111-111111111111"
	deletionClaimTombstoneID = "a2222222-2222-4222-8222-222222222222"
)

func TestWorkerClaimAuthorityClaimsAndReclaimsDeletionTarget(t *testing.T) {
	db := openWorkerClaimDeletionTestDB(t)
	seedDeletionTargetForClaimTest(t, db)
	authority := newWorkerClaimAuthorityForTest(db)
	store := deletionstore.New(db)
	ctx := context.Background()
	owner1 := deletionClaimDigestForTest(0x11)

	claim1, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		owner1,
		true,
	)
	if err != nil {
		t.Fatalf("ClaimNextDeletionTargetTaskForWorker() error = %v", err)
	}
	if claim1.State != deletion.TargetProcessing || claim1.Attempt != 1 ||
		claim1.Claim.ProjectID != deletionClaimProjectID ||
		claim1.Claim.TombstoneID != deletionClaimTombstoneID ||
		claim1.Claim.Target != governance.TargetRuntime ||
		claim1.Claim.ClaimIncarnation != 1 ||
		claim1.Claim.LeaseOwnerDigest != owner1 ||
		!claim1.Claim.ObservedLeaseExpiry.After(time.Now().UTC()) {
		t.Fatalf("first claim = %#v", claim1)
	}
	assertDeletionTombstoneStatusForTest(t, db, deletion.TombstoneProcessing)
	loaded, err := store.LoadDeletionTargetRequestForClaim(
		ctx,
		claim1.Claim,
	)
	if err != nil {
		t.Fatalf("LoadDeletionTargetRequestForClaim() error = %v", err)
	}
	if loaded.Attempt != 1 || loaded.Claim != claim1.Claim ||
		loaded.Tombstone.ProjectID != deletionClaimProjectID ||
		loaded.Tombstone.Identity.Origin != governance.DeletionOriginGovernedOnline {
		t.Fatalf("loaded request = %#v", loaded)
	}

	if _, err := db.Pool.Exec(ctx, `
		UPDATE deletion_target_tasks
		SET claim_expires_at = clock_timestamp() - interval '1 second'
		WHERE tombstone_id = $1 AND project_id = $2 AND target = 'runtime'
	`, deletionClaimTombstoneID, deletionClaimProjectID); err != nil {
		t.Fatalf("expire first target claim: %v", err)
	}
	owner2 := deletionClaimDigestForTest(0x22)
	claim2, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		owner2,
		true,
	)
	if err != nil {
		t.Fatalf("reclaim target error = %v", err)
	}
	if claim2.Attempt != 2 || claim2.Claim.ClaimIncarnation != 2 ||
		claim2.Claim.LeaseOwnerDigest != owner2 {
		t.Fatalf("reclaimed target = %#v", claim2)
	}
	if _, err := store.LoadDeletionTargetRequestForClaim(
		ctx, claim1.Claim,
	); !errors.Is(err, deletion.ErrLostTargetClaim) {
		t.Fatalf("stale load error = %v, want ErrLostTargetClaim", err)
	}
	if _, err := store.HeartbeatDeletionTargetClaim(ctx, claim1.Claim); !errors.Is(err, deletion.ErrLostTargetClaim) {
		t.Fatalf("stale heartbeat error = %v, want ErrLostTargetClaim", err)
	}
	if expiresAt, err := store.HeartbeatDeletionTargetClaim(ctx, claim2.Claim); err != nil {
		t.Fatalf("current heartbeat error = %v", err)
	} else if !expiresAt.After(time.Now().UTC()) {
		t.Fatalf("heartbeat expiry = %s, want future", expiresAt)
	}

	failure := deletion.TargetFailure{
		Reason: governance.ReasonTargetUnavailable, RetryAfter: time.Minute,
	}
	if err := store.FailDeletionTargetForClaim(ctx, claim1.Claim, failure); !errors.Is(err, deletion.ErrLostTargetClaim) {
		t.Fatalf("stale failure error = %v, want ErrLostTargetClaim", err)
	}
	staleResult := deletion.TargetResult{
		Disposition:    deletion.DispositionPurged,
		HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
		Reason:         governance.ReasonPurged,
	}
	if _, err := store.CompleteDeletionTargetForClaim(ctx, claim1.Claim, staleResult); !errors.Is(err, deletion.ErrLostTargetClaim) {
		t.Fatalf("stale completion error = %v, want ErrLostTargetClaim", err)
	}
	if err := store.FailDeletionTargetForClaim(ctx, claim2.Claim, failure); err != nil {
		t.Fatalf("FailDeletionTargetForClaim() error = %v", err)
	}
	assertDeletionTombstoneStatusForTest(t, db, deletion.TombstoneFailed)
	var state string
	var owner []byte
	var expiresAt *time.Time
	var retryAfterSeconds float64
	if err := db.Pool.QueryRow(ctx, `
		SELECT state, claimed_by_digest, claim_expires_at,
		       extract(epoch FROM (next_attempt_at - clock_timestamp()))
		FROM deletion_target_tasks
		WHERE tombstone_id = $1 AND project_id = $2 AND target = 'runtime'
	`, deletionClaimTombstoneID, deletionClaimProjectID).Scan(&state, &owner, &expiresAt, &retryAfterSeconds); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || owner != nil || expiresAt != nil || retryAfterSeconds < 58 || retryAfterSeconds > 60 {
		t.Fatalf("failed task state/owner/expiry/retry = %q/%x/%v/%f", state, owner, expiresAt, retryAfterSeconds)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE deletion_target_tasks
		SET next_attempt_at = clock_timestamp() - interval '1 second'
		WHERE tombstone_id = $1 AND project_id = $2 AND target = 'runtime'
	`, deletionClaimTombstoneID, deletionClaimProjectID); err != nil {
		t.Fatalf("make failed target retryable: %v", err)
	}
	claim3, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		deletionClaimDigestForTest(0x33),
		true,
	)
	if err != nil {
		t.Fatalf("claim failed target error = %v", err)
	}
	if claim3.Attempt != 3 || claim3.Claim.ClaimIncarnation != 3 {
		t.Fatalf("third claim = %#v", claim3)
	}
	assertDeletionTombstoneStatusForTest(t, db, deletion.TombstoneProcessing)
}

func TestWorkerClaimAuthoritySkipsRuntimeUnlessEnabled(t *testing.T) {
	db := openWorkerClaimDeletionTestDB(t)
	seedDeletionTargetForClaimTest(t, db)
	authority := newWorkerClaimAuthorityForTest(db)
	ctx := context.Background()

	if _, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		deletionClaimDigestForTest(0x3a),
		false,
	); !errors.Is(err, deletion.ErrNoDeletionTarget) {
		t.Fatalf("runtime-disabled claim error = %v, want ErrNoDeletionTarget", err)
	}
	var state string
	var attempts int
	if err := db.Pool.QueryRow(ctx, `
		SELECT state, attempts
		FROM deletion_target_tasks
		WHERE tombstone_id = $1 AND project_id = $2 AND target = 'runtime'
	`, deletionClaimTombstoneID, deletionClaimProjectID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || attempts != 0 {
		t.Fatalf("runtime-disabled task = %q/%d, want pending/0", state, attempts)
	}

	claimed, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		deletionClaimDigestForTest(0x3b),
		true,
	)
	if err != nil {
		t.Fatalf("runtime-enabled claim error = %v", err)
	}
	if claimed.Claim.Target != governance.TargetRuntime {
		t.Fatalf("runtime-enabled target = %q, want runtime", claimed.Claim.Target)
	}
}

func TestDeletionStoreCompletesAndReplaysTargetReceipt(t *testing.T) {
	db := openWorkerClaimDeletionTestDB(t)
	seedDeletionTargetForClaimTest(t, db)
	authority := newWorkerClaimAuthorityForTest(db)
	store := deletionstore.New(db)
	ctx := context.Background()
	claimed, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		deletionClaimDigestForTest(0x66),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.LoadDeletionTargetRequestForClaim(
		ctx, claimed.Claim,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := deletion.TargetResult{
		Disposition:    deletion.DispositionPurged,
		HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
		Reason:         governance.ReasonPurged,
	}
	receipt, err := store.CompleteDeletionTargetForClaim(ctx, claimed.Claim, result)
	if err != nil {
		t.Fatalf("CompleteDeletionTargetForClaim() error = %v", err)
	}
	if err := receipt.ValidateFor(request, result); err != nil {
		t.Fatalf("receipt validation error = %v", err)
	}
	replayed, err := store.CompleteDeletionTargetForClaim(ctx, claimed.Claim, result)
	if err != nil {
		t.Fatalf("duplicate completion error = %v", err)
	}
	if replayed != receipt {
		t.Fatalf("replayed receipt = %#v, want %#v", replayed, receipt)
	}
	found, err := store.FindCommittedDeletionTargetReceipt(ctx, claimed.Claim)
	if err != nil {
		t.Fatalf("FindCommittedDeletionTargetReceipt() error = %v", err)
	}
	if found != receipt {
		t.Fatalf("found receipt = %#v, want %#v", found, receipt)
	}
	var tombstoneStatus string
	var completionCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT tombstone.status,
		       (SELECT count(*) FROM deletion_completion_receipts receipt
		        WHERE receipt.tombstone_id = tombstone.id AND receipt.project_id = tombstone.project_id)
		FROM deletion_tombstones tombstone
		WHERE tombstone.id = $1 AND tombstone.project_id = $2
	`, deletionClaimTombstoneID, deletionClaimProjectID).Scan(&tombstoneStatus, &completionCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneStatus != "purged" || completionCount != 1 {
		t.Fatalf("terminal tombstone status/receipts = %q/%d, want purged/1", tombstoneStatus, completionCount)
	}
	forged := claimed.Claim
	forged.LeaseOwnerDigest = deletionClaimDigestForTest(0x88)
	if _, err := store.CompleteDeletionTargetForClaim(ctx, forged, result); !errors.Is(err, deletion.ErrLostTargetClaim) {
		t.Fatalf("forged completion error = %v, want ErrLostTargetClaim", err)
	}
}

func TestDeletionStorePersistsNoDataReceiptAtomically(t *testing.T) {
	t.Run("persists and replays no-data", func(t *testing.T) {
		db := openWorkerClaimDeletionTestDB(t)
		seedDeletionTargetForClaimTest(t, db)
		ctx := context.Background()
		claimed, err := newWorkerClaimAuthorityForTest(db).ClaimNextDeletionTargetTaskForWorker(
			ctx, deletionClaimDigestForTest(0x81),
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		store := deletionstore.New(db)
		request, err := store.LoadDeletionTargetRequestForClaim(
			ctx, claimed.Claim,
		)
		if err != nil {
			t.Fatal(err)
		}
		result := deletionNoDataResultForStoreTest(t, request)
		receipt, err := store.CompleteDeletionTargetForClaim(ctx, claimed.Claim, result)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := store.CompleteDeletionTargetForClaim(ctx, claimed.Claim, result)
		if err != nil || replayed != receipt {
			t.Fatalf("replayed no-data completion = %#v, %v; want %#v", replayed, err, receipt)
		}

		var state, handlerVersion string
		var storedReceipt []byte
		var completionCount int
		if err := db.Pool.QueryRow(ctx, `
			SELECT task.state, task.handler_version, task.receipt_digest,
			       (SELECT count(*) FROM deletion_completion_receipts)
			FROM deletion_target_tasks AS task
			WHERE task.tombstone_id = $1
			  AND task.project_id = $2 AND task.target = 'runtime'
		`, deletionClaimTombstoneID, deletionClaimProjectID).Scan(
			&state, &handlerVersion, &storedReceipt, &completionCount,
		); err != nil {
			t.Fatal(err)
		}
		if state != "no_data" || handlerVersion != string(governance.HandlerRuntimeDirectSDKV1) ||
			len(storedReceipt) != 32 || completionCount != 1 {
			t.Fatalf("stored no-data receipt mismatch: %q/%q/%d/%d",
				state, handlerVersion, len(storedReceipt), completionCount)
		}
	})

	t.Run("scope mismatch rolls back every terminal write", func(t *testing.T) {
		db := openWorkerClaimDeletionTestDB(t)
		seedDeletionTargetForClaimTest(t, db)
		ctx := context.Background()
		claimed, err := newWorkerClaimAuthorityForTest(db).ClaimNextDeletionTargetTaskForWorker(
			ctx, deletionClaimDigestForTest(0x82),
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		store := deletionstore.New(db)
		request, err := store.LoadDeletionTargetRequestForClaim(
			ctx, claimed.Claim,
		)
		if err != nil {
			t.Fatal(err)
		}
		result := deletionNoDataResultForStoreTest(t, request)
		result.HandlerVersion = governance.HandlerFilesystemOSRootV1
		if request.ValidateResult(result) == nil {
			t.Fatal("target-mismatched handler must be invalid")
		}
		if _, err := store.CompleteDeletionTargetForClaim(ctx, claimed.Claim, result); !errors.Is(err, deletion.ErrInvalidTargetRequest) {
			t.Fatalf("scope-mismatched completion error = %v, want ErrInvalidTargetRequest", err)
		}
		var taskState string
		var completionCount int
		if err := db.Pool.QueryRow(ctx, `
			SELECT task.state,
			       (SELECT count(*) FROM deletion_completion_receipts)
			FROM deletion_target_tasks AS task
			WHERE task.tombstone_id = $1 AND task.project_id = $2 AND task.target = 'runtime'
		`, deletionClaimTombstoneID, deletionClaimProjectID).Scan(
			&taskState, &completionCount,
		); err != nil {
			t.Fatal(err)
		}
		if taskState != "processing" || completionCount != 0 {
			t.Fatalf("invalid no-data completion changed state: %q/%d", taskState, completionCount)
		}
	})

}

func deletionNoDataResultForStoreTest(t *testing.T, request deletion.TargetRequest) deletion.TargetResult {
	t.Helper()
	result := deletion.TargetResult{
		Disposition:    deletion.DispositionNoData,
		HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
		Reason:         governance.ReasonNoData,
	}
	if err := request.ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWorkerClaimAuthorityKeepsPostgreSQLLast(t *testing.T) {
	db := openWorkerClaimDeletionTestDB(t)
	seedDeletionTargetForClaimTest(t, db)
	preparePostgreSQLLastScenarioForTest(t, db, false, false, governance.Digest{})
	authority := newWorkerClaimAuthorityForTest(db)
	ctx := context.Background()

	if _, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		deletionClaimDigestForTest(0x91),
		false,
	); !errors.Is(err, deletion.ErrNoDeletionTarget) {
		t.Fatalf("premature PostgreSQL claim error = %v, want ErrNoDeletionTarget", err)
	}

	preparePostgreSQLLastScenarioForTest(t, db, true, false, governance.Digest{})
	claimed, err := authority.ClaimNextDeletionTargetTaskForWorker(
		ctx,
		deletionClaimDigestForTest(0x92),
		false,
	)
	if err != nil {
		t.Fatalf("last PostgreSQL claim error = %v", err)
	}
	if claimed.Claim.Target != governance.TargetPostgreSQL {
		t.Fatalf("claimed target = %q, want postgresql", claimed.Claim.Target)
	}
}

func TestDeletionStoreRejectsPrematurePostgreSQLCompletion(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	seedDeletionTargetForClaimTest(t, db)
	owner := deletionClaimDigestForTest(0x93)
	claim := preparePostgreSQLLastScenarioForTest(t, db, false, true, owner)
	_, err := deletionstore.New(db).CompleteDeletionTargetForClaim(context.Background(), claim, deletion.TargetResult{
		Disposition:    deletion.DispositionPurged,
		HandlerVersion: governance.HandlerPostgresExactInventoryV1,
		Reason:         governance.ReasonPurged,
	})
	if !errors.Is(err, deletion.ErrLostTargetClaim) {
		t.Fatalf("premature PostgreSQL completion error = %v, want ErrLostTargetClaim", err)
	}
}

func TestDeletionTargetTriggerRejectsPrematurePostgreSQLTransitions(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		db := openCurrentStoreTestDB(t)
		seedDeletionTargetForClaimTest(t, db)
		preparePostgreSQLLastScenarioForTest(t, db, false, false, governance.Digest{})
		claimOwner := deletionClaimDigestForTest(0x94)
		if _, err := db.Pool.Exec(context.Background(), `
			UPDATE deletion_target_tasks
			SET state = 'processing',
			    attempts = attempts + 1,
			    claim_incarnation = claim_incarnation + 1,
			    claimed_by_digest = $3,
			    claim_expires_at = clock_timestamp() + interval '5 minutes'
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		`, deletionClaimTombstoneID, deletionClaimProjectID, claimOwner[:]); err == nil {
			t.Fatal("direct SQL claimed PostgreSQL before earlier targets completed")
		}
	})

	t.Run("completion", func(t *testing.T) {
		db := openCurrentStoreTestDB(t)
		seedDeletionTargetForClaimTest(t, db)
		owner := deletionClaimDigestForTest(0x95)
		preparePostgreSQLLastScenarioForTest(t, db, false, true, owner)
		receiptDigest := deletionClaimDigestForTest(0x96)
		if _, err := db.Pool.Exec(context.Background(), `
			UPDATE deletion_target_tasks
			SET state = 'succeeded',
			    handler_version = 'postgres_exact_inventory_v1',
			    receipt_digest = $3,
			    completed_at = date_trunc('second', clock_timestamp()),
			    last_reason_code = NULL
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		`, deletionClaimTombstoneID, deletionClaimProjectID, receiptDigest[:]); err == nil {
			t.Fatal("direct SQL completed PostgreSQL before earlier targets completed")
		}
	})
}

func openWorkerClaimDeletionTestDB(t *testing.T) *storedb.DB {
	t.Helper()
	return openCurrentStoreTestDB(t)
}

func seedDeletionTargetForClaimTest(t *testing.T, db *storedb.DB) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SET session_replication_role = origin`); err != nil {
			t.Fatalf("restore replication role: %v", err)
		}
	}()
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := conn.Exec(ctx, `
		INSERT INTO deletion_tombstones (
		  id, project_id, origin, entity_kind, purge_mode, entity_id,
		  project_generation, status, fenced_at, purge_deadline
		) VALUES ($1, $2, 'governed_online', 'project', 'derived_generation', $2, 1,
		          'pending', $3, $4)
	`, deletionClaimTombstoneID, deletionClaimProjectID, fencedAt, fencedAt.Add(24*time.Hour)); err != nil {
		t.Fatalf("insert deletion tombstone: %v", err)
	}
	terminalAt := time.Now().UTC().Truncate(time.Second)
	if _, err := conn.Exec(ctx, `
		INSERT INTO deletion_target_tasks (
		  tombstone_id, project_id, target, state, attempts, claim_incarnation,
		  next_attempt_at, claimed_by_digest, claim_expires_at, handler_version,
		  receipt_digest, completed_at
		) VALUES
		  ($1, $2, 'runtime', 'pending', 0, 0, clock_timestamp(), NULL, NULL, NULL, NULL, NULL),
		  ($1, $2, 'filesystem', 'succeeded', 1, 1, clock_timestamp(), decode(repeat('11',32),'hex'),
		   clock_timestamp() + interval '5 minutes', 'filesystem_os_root_v1',
		   decode(repeat('13',32),'hex'), $3),
		  ($1, $2, 'postgresql', 'succeeded', 1, 1, clock_timestamp(), decode(repeat('31',32),'hex'),
		   clock_timestamp() + interval '5 minutes', 'postgres_exact_inventory_v1',
		   decode(repeat('33',32),'hex'), $3)
	`, deletionClaimTombstoneID, deletionClaimProjectID, terminalAt); err != nil {
		t.Fatalf("insert deletion target tasks: %v", err)
	}
	steps, err := deletion.PostgresStepRegistry(
		governance.DeletionSelectionProject,
		governance.PurgeModeDerivedGeneration,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if _, err := conn.Exec(ctx, `
			INSERT INTO deletion_target_step_progress (
			  tombstone_id, project_id, target, step_name, step_order, cursor_kind,
			  state, claim_incarnation, claimed_by_digest, cursor_digest,
			  processed_count, started_at, updated_at, completed_at
			) VALUES (
			  $1, $2, 'postgresql', $3, $4, $5, 'succeeded', 1,
			  decode(repeat('31',32),'hex'), decode(repeat('41',32),'hex'), 0, $6, $6, $6
			)
		`, deletionClaimTombstoneID, deletionClaimProjectID, step.Name, step.Order, string(step.CursorKind), terminalAt); err != nil {
			t.Fatalf("insert deletion target step %s: %v", step.Name, err)
		}
	}
}

func preparePostgreSQLLastScenarioForTest(
	t *testing.T,
	db *storedb.DB,
	runtimeTerminal bool,
	postgresProcessing bool,
	owner governance.Digest,
) deletion.TargetClaim {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SET session_replication_role = origin`); err != nil {
			t.Fatalf("restore replication role: %v", err)
		}
	}()

	now := time.Now().UTC().Truncate(time.Microsecond)
	tombstoneStatus := "pending"
	if postgresProcessing {
		tombstoneStatus = "processing"
	}
	if _, err := conn.Exec(ctx, `
		UPDATE deletion_tombstones
		SET status = $3, updated_at = clock_timestamp()
		WHERE id = $1 AND project_id = $2
	`, deletionClaimTombstoneID, deletionClaimProjectID, tombstoneStatus); err != nil {
		t.Fatalf("prepare tombstone: %v", err)
	}
	if runtimeTerminal {
		if _, err := conn.Exec(ctx, `
			UPDATE deletion_target_tasks
			SET state = 'succeeded', attempts = 1, claim_incarnation = 1,
			    next_attempt_at = $3::timestamptz, claimed_by_digest = decode(repeat('51',32),'hex'),
			    claim_expires_at = $3::timestamptz + interval '5 minutes',
			    handler_version = 'runtime_direct_sdk_v1',
			    receipt_digest = decode(repeat('53',32),'hex'), completed_at = date_trunc('second', $3::timestamptz),
			    last_reason_code = NULL
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'runtime'
		`, deletionClaimTombstoneID, deletionClaimProjectID, now); err != nil {
			t.Fatalf("prepare terminal runtime target: %v", err)
		}
	} else {
		if _, err := conn.Exec(ctx, `
			UPDATE deletion_target_tasks
			SET state = 'pending', attempts = 0, claim_incarnation = 0,
			    next_attempt_at = $3::timestamptz + interval '1 hour', claimed_by_digest = NULL,
			    claim_expires_at = NULL, handler_version = NULL,
			    receipt_digest = NULL, completed_at = NULL,
			    last_reason_code = NULL
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'runtime'
		`, deletionClaimTombstoneID, deletionClaimProjectID, now); err != nil {
			t.Fatalf("prepare pending runtime target: %v", err)
		}
	}

	claim := deletion.TargetClaim{}
	if postgresProcessing {
		expiresAt := now.Add(5 * time.Minute)
		if _, err := conn.Exec(ctx, `
			UPDATE deletion_target_tasks
			SET state = 'processing', attempts = 1, claim_incarnation = 1,
			    next_attempt_at = $3::timestamptz - interval '1 minute', claimed_by_digest = $4,
			    claim_expires_at = $5, handler_version = NULL,
			    receipt_digest = NULL, completed_at = NULL,
			    last_reason_code = NULL
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		`, deletionClaimTombstoneID, deletionClaimProjectID, now, owner[:], expiresAt); err != nil {
			t.Fatalf("prepare processing PostgreSQL target: %v", err)
		}
		claim = deletion.TargetClaim{
			ProjectID:           deletionClaimProjectID,
			TombstoneID:         deletionClaimTombstoneID,
			Target:              governance.TargetPostgreSQL,
			ClaimIncarnation:    1,
			LeaseOwnerDigest:    owner,
			ObservedLeaseExpiry: expiresAt,
		}
	} else {
		if _, err := conn.Exec(ctx, `
			UPDATE deletion_target_tasks
			SET state = 'pending', attempts = 0, claim_incarnation = 0,
			    next_attempt_at = $3::timestamptz - interval '1 minute', claimed_by_digest = NULL,
			    claim_expires_at = NULL, handler_version = NULL,
			    receipt_digest = NULL, completed_at = NULL,
			    last_reason_code = NULL
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		`, deletionClaimTombstoneID, deletionClaimProjectID, now); err != nil {
			t.Fatalf("prepare pending PostgreSQL target: %v", err)
		}
	}
	return claim
}

func deletionClaimDigestForTest(value byte) governance.Digest {
	var digest governance.Digest
	for index := range digest {
		digest[index] = value
	}
	return digest
}

func assertDeletionTombstoneStatusForTest(t *testing.T, db *storedb.DB, want deletion.TombstoneStatus) {
	t.Helper()
	var got string
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT status
		FROM deletion_tombstones
		WHERE id = $1 AND project_id = $2
	`, deletionClaimTombstoneID, deletionClaimProjectID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("tombstone status = %q, want %q", got, want)
	}
}
