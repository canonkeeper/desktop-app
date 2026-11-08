package librarystore

import (
	"context"
	"testing"

	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openCurrentStoreTestDB поднимает изолированную схему ровно из текущего baseline.
// Старые цепочки миграций намеренно не поддерживаются даже в тестах.
func openCurrentStoreTestDB(t *testing.T) *storedb.DB {
	return testutil.OpenIsolatedPostgresTestDB(t, "ck_store")
}

// withTestReplicaRole позволяет тесту подготовить недостижимое через публичный API состояние.
// Роль всегда восстанавливается до возврата соединения в пул.
func withTestReplicaRole(t *testing.T, db *storedb.DB, run func(context.Context, *pgxpool.Conn)) {
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
	defer func() { _, _ = conn.Exec(context.Background(), `SET session_replication_role = origin`) }()
	run(ctx, conn)
}
