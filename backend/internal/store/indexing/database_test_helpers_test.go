package indexingstore

import (
	"testing"

	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// openCurrentStoreTestDB поднимает изолированную схему ровно из текущего baseline.
// Старые цепочки миграций намеренно не поддерживаются даже в тестах.
func openCurrentStoreTestDB(t *testing.T, tracers ...pgx.QueryTracer) *storedb.DB {
	if len(tracers) > 1 {
		t.Fatal("openCurrentStoreTestDB accepts at most one query tracer")
	}
	var tracer pgx.QueryTracer
	if len(tracers) == 1 {
		tracer = tracers[0]
	}
	return testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store", tracer)
}
