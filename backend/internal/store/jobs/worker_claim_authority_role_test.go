package jobstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	storedb "canonkeeper/backend/internal/store/db"
)

func TestWorkerClaimAuthorityRoleHasOnlySixExecuteOnlyFunctionSignatures(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	var productSchema string
	if err := db.Pool.QueryRow(ctx, "SELECT current_schema()").Scan(&productSchema); err != nil {
		t.Fatal(err)
	}
	workerSchema := productSchema + "_worker"

	var functionCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_catalog.pg_proc AS procedure
		JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
		WHERE namespace.nspname = $1
		  AND procedure.proname LIKE 'claim_next_%_for_worker'
	`, workerSchema).Scan(&functionCount); err != nil {
		t.Fatal(err)
	}
	if functionCount != 5 {
		t.Fatalf("worker function count = %d, want 5", functionCount)
	}

	functions := []string{
		"claim_next_queued_job_for_worker(text[],timestamp with time zone)",
		"claim_next_deletion_target_task_for_worker(bytea,integer,boolean)",
		"claim_next_source_hard_purge_for_worker()",
		"claim_next_import_expiry_for_worker()",
		"claim_next_deletion_audit_gc_for_worker()",
	}
	for _, function := range functions {
		qualified := fmt.Sprintf(`%q.%s`, workerSchema, function)
		assertFunctionPrivilegeForRole(t, db, "canonkeeper_worker_discovery", qualified, true)
		assertFunctionPrivilegeForRole(t, db, "canonkeeper_product_runtime", qualified, false)
	}

	for _, table := range []string{"projects", "project_source_views", "artifact_source_owners"} {
		productTable := fmt.Sprintf(`%q.%s`, productSchema, table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			var allowed bool
			if err := db.Pool.QueryRow(ctx,
				"SELECT has_table_privilege('canonkeeper_worker_discovery', $1, $2)",
				productTable, privilege,
			).Scan(&allowed); err != nil {
				t.Fatal(err)
			}
			if allowed {
				t.Fatalf("worker role unexpectedly has %s on product table %s", privilege, table)
			}
		}
	}
}

func TestWorkerClaimAuthorityRoleSkipsUnreadableJobWithoutProductTableAccess(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC)
	readableJobID := "00000000-0000-4000-8000-000000000af1"
	readableOwnerID := "00000000-0000-4000-8000-000000000af2"
	readableRefID := "00000000-0000-4000-8000-000000000af3"
	var productSchema string
	if err := db.Pool.QueryRow(ctx, "SELECT current_schema()").Scan(&productSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, fmt.Sprintf("GRANT USAGE ON SCHEMA %q TO canonkeeper_governance_owner", productSchema)); err != nil {
		t.Fatal(err)
	}
	insertProjectStoreUserForTest(t, db, artifactConstraintUserID, "worker-owner-claim@example.com")
	insertProjectStoreProjectForTest(t, db, artifactConstraintProjectID, artifactConstraintUserID, "Worker Owner Claim", now)

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertProjectScopeArtifactOwner(t, tx, readableOwnerID, readableRefID, readableJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, readableOwnerID, artifactConstraintProjectID)
	if _, err := tx.Exec(ctx, `
		INSERT INTO jobs (
			id, project_id, kind, status, progress_json, subject_json,
			result_json, error_json, creator_user_id, can_cancel,
			queue_class, retry_count, max_attempts, next_attempt_at,
			artifact_source_owner_id, governance_payload_state,
			created_at, updated_at
		) VALUES
		(
			$1, $2, 'import', 'queued', jsonb_build_object('value', 0),
			jsonb_build_object('type', 'import', 'id', $2::uuid::text, 'projectId', $2::uuid::text),
			NULL, NULL, $3, true,
			'user_demanded', 0, 3, $4::timestamptz - interval '1 minute',
			$5, 'active', $4::timestamptz - interval '1 minute', $4::timestamptz - interval '1 minute'
		),
		(
			$6, $2, 'import', 'queued', jsonb_build_object('value', 0),
			jsonb_build_object('type', 'import', 'id', $2::uuid::text, 'projectId', $2::uuid::text),
			NULL, NULL, $3, true,
			'user_demanded', 0, 3, $4::timestamptz,
			$7, 'active', $4::timestamptz, $4::timestamptz
		)
	`, artifactConstraintJobID, artifactConstraintProjectID, artifactConstraintUserID, now,
		artifactConstraintOwnerID, readableJobID, readableOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE artifact_source_owners
		SET read_state = 'source_deleted', fenced_at = clock_timestamp()
		WHERE id = $1 AND project_id = $2
	`, artifactConstraintOwnerID, artifactConstraintProjectID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var unreadableBefore string
	var unreadableReadable, readableReadable bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT to_jsonb(row_value)::text FROM jobs AS row_value WHERE id = $1 AND project_id = $2),
		  ck_artifact_source_owner_is_readable($3, $2, 'job', $1),
		  ck_artifact_source_owner_is_readable($4, $2, 'job', $5)
	`, artifactConstraintJobID, artifactConstraintProjectID, artifactConstraintOwnerID,
		readableOwnerID, readableJobID).Scan(&unreadableBefore, &unreadableReadable, &readableReadable); err != nil {
		t.Fatal(err)
	}
	if unreadableReadable || !readableReadable {
		t.Fatalf("owner readability = unreadable:%t readable:%t", unreadableReadable, readableReadable)
	}

	workerSchema := productSchema + "_worker"
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET SESSION AUTHORIZATION canonkeeper_worker_discovery"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "RESET SESSION AUTHORIZATION") }()

	query := fmt.Sprintf(`
		SELECT project_id, work_id, kind, state, claim_incarnation
		FROM %q.claim_next_queued_job_for_worker(ARRAY['import']::text[], NULL)
	`, workerSchema)
	var projectID, jobID, kind, state string
	var incarnation int64
	if err := conn.QueryRow(ctx, query).Scan(&projectID, &jobID, &kind, &state, &incarnation); err != nil {
		t.Fatalf("execute-only worker claim failed: %v", err)
	}
	if projectID != artifactConstraintProjectID || jobID != readableJobID || kind != "import" || state != "running" || incarnation != 1 {
		t.Fatalf("unexpected worker claim: project=%s job=%s kind=%s state=%s incarnation=%d", projectID, jobID, kind, state, incarnation)
	}
	var unreadableAfter string
	if err := db.Pool.QueryRow(ctx, `
		SELECT to_jsonb(row_value)::text
		FROM jobs AS row_value
		WHERE id = $1 AND project_id = $2
	`, artifactConstraintJobID, artifactConstraintProjectID).Scan(&unreadableAfter); err != nil {
		t.Fatal(err)
	}
	if unreadableAfter != unreadableBefore {
		t.Fatal("unreadable job changed during worker discovery")
	}
}

func TestValidateWorkerDiscoveryDatabaseIdentitiesRequiresTheExecuteOnlyEnvelope(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	var productSchema string
	if err := db.Pool.QueryRow(ctx, "SELECT current_schema()").Scan(&productSchema); err != nil {
		t.Fatal(err)
	}
	projectsTable := fmt.Sprintf("%q.projects", productSchema)

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET SESSION AUTHORIZATION canonkeeper_worker_discovery"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "RESET SESSION AUTHORIZATION") }()
	workerSchema := productSchema + "_worker"
	if err := validateWorkerDiscoveryDatabaseIdentities(ctx, db.Pool, conn, workerSchema); err != nil {
		t.Fatalf("execute-only worker identity rejected: %v", err)
	}
	if _, err := conn.Exec(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerDiscoveryDatabaseIdentities(ctx, db.Pool, db.Pool, workerSchema); err == nil {
		t.Fatal("shared product/worker identity was accepted")
	}

	if _, err := db.Pool.Exec(ctx, "GRANT SELECT ON "+projectsTable+" TO canonkeeper_worker_discovery"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), "REVOKE SELECT ON "+projectsTable+" FROM canonkeeper_worker_discovery"); err != nil {
			t.Errorf("restore worker discovery table grants: %v", err)
		}
	})
	if _, err := conn.Exec(ctx, "SET SESSION AUTHORIZATION canonkeeper_worker_discovery"); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerDiscoveryDatabaseIdentities(ctx, db.Pool, conn, workerSchema); err == nil {
		t.Fatal("worker identity with direct product-table access was accepted")
	}
	if _, err := conn.Exec(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
}

func assertFunctionPrivilegeForRole(t *testing.T, db *storedb.DB, role, function string, want bool) {
	t.Helper()
	var allowed bool
	if err := db.Pool.QueryRow(context.Background(),
		"SELECT has_function_privilege($1, $2, 'EXECUTE')", role, function,
	).Scan(&allowed); err != nil {
		t.Fatal(err)
	}
	if allowed != want {
		t.Fatalf("role %s execute %s = %t, want %t", role, function, allowed, want)
	}
}
