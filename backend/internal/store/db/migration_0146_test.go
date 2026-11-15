package storedb_test

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// specs/2026-11-13-desk-sagafile.md SF5.1: the saga file is written and opened at once, so the kinds 0114 kept for its Jobs
// are gone: the base refuses a Job of such a kind by the check of the kind, and a worker that asks for one gets nothing.
// A Job of a kind of today passes the check of the kind.
func TestMigration0146RefusesTheJobsOfTheSagaFile(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_file_kinds", 146)
	insert := func(kind, class string) error {
		// The shape of a Job whose source is deleted passes every other check; the project is checked after the kind.
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO jobs (id, project_id, kind, status, progress_json, result_json, error_json, governance_payload_state,
			                  can_cancel, queue_class, retry_count, max_attempts, next_attempt_at)
			VALUES (gen_random_uuid(), gen_random_uuid(), $1, 'expired', '{}', NULL, NULL, 'source_deleted',
			        false, $2, 0, 3, now())
		`, kind, class)
		return err
	}
	for _, kind := range []string{"saga_archive_export", "saga_archive_import"} {
		var refused *pgconn.PgError
		if err := insert(kind, "user_demanded"); !errors.As(err, &refused) || refused.ConstraintName != "jobs_kind_check" {
			t.Fatalf("a Job of kind %s = %v; want it refused by the check of the kind", kind, err)
		}
	}
	var refused *pgconn.PgError
	if err := insert("import", "user_demanded"); !errors.As(err, &refused) || refused.Code != "23503" {
		t.Fatalf("a Job of kind import = %v; want it past the checks, refused only for its project", err)
	}
	var claimed int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM `+pgx.Identifier{testutil.IsolatedPostgresSchema(t, db) + "_worker"}.Sanitize()+`.claim_next_queued_job_for_worker(ARRAY['saga_archive_import'], NULL)
	`).Scan(&claimed); err != nil || claimed != 0 {
		t.Fatalf("a claim of the saga file = %d rows, %v; want none", claimed, err)
	}
}
