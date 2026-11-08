package jobstore

import (
	"testing"

	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

// openCurrentStoreTestDB поднимает изолированную схему ровно из текущего baseline.
// Старые цепочки миграций намеренно не поддерживаются даже в тестах.
func openCurrentStoreTestDB(t *testing.T) *storedb.DB {
	return testutil.OpenIsolatedPostgresTestDB(t, "ck_jobs")
}
