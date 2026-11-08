package jobstore

import (
	"context"
	"os"
	"strings"
	"testing"

	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

func TestWorkerClaimQueriesUseOnlyTheFixedProductionSchema(t *testing.T) {
	wantFunctions := map[string][]string{
		"worker_claim_jobs.go": {
			"canonkeeper_worker.claim_next_queued_job_for_worker",
		},
		"worker_claim_deletion.go": {
			"canonkeeper_worker.claim_next_deletion_target_task_for_worker",
		},
		"worker_claim_discovery.go": {
			"canonkeeper_worker.claim_next_import_expiry_for_worker",
		},
		"worker_claim_retention.go": {
			"canonkeeper_worker.claim_next_source_hard_purge_for_worker",
			"canonkeeper_worker.claim_next_deletion_audit_gc_for_worker",
		},
	}
	for path, functions := range wantFunctions {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		for _, forbidden := range []string{"current_schema()", "deletionWorkerSchema", "fmt.Sprintf", "pgx.Identifier"} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s derives worker schema through %q", path, forbidden)
			}
		}
		for _, function := range functions {
			if strings.Count(source, function) != 1 {
				t.Fatalf("%s fixed call count for %s = %d, want 1", path, function, strings.Count(source, function))
			}
		}
	}
}

type fixedWorkerSchemaQueryerForTest struct {
	next   workerClaimQueryer
	schema string
}

func (queryer fixedWorkerSchemaQueryerForTest) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	query = strings.ReplaceAll(query, "canonkeeper_worker.", pgx.Identifier{queryer.schema}.Sanitize()+".")
	return queryer.next.QueryRow(ctx, query, args...)
}

func newWorkerClaimAuthorityForTest(db *storedb.DB) *WorkerClaimAuthority {
	var schema string
	if err := db.Pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		panic(err)
	}
	return newWorkerClaimAuthority(fixedWorkerSchemaQueryerForTest{next: db.Pool, schema: schema + "_worker"})
}
