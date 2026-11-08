package storedb_test

import (
	"context"
	"strings"
	"testing"

	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

func TestOpenTestDatabase(t *testing.T) {
	dsn := testutil.DatabaseURL(t)

	db, err := storedb.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
}

// backend.json sets the pool size and the session limits of a role in its database URL
// (specs/2026-10-28-fast-job-list-and-postgres.md, UC5): pgxpool reads pool_max_conns and pgx
// sends the other parameters to PostgreSQL as session settings.
func TestOpenAppliesThePoolAndSessionSettingsOfTheURL(t *testing.T) {
	dsn := testutil.DatabaseURL(t)
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	ctx := context.Background()
	db, err := storedb.Open(ctx, dsn+separator+"pool_max_conns=3&statement_timeout=60s&idle_in_transaction_session_timeout=60s")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.Pool.Config().MaxConns; got != 3 {
		t.Fatalf("pool max conns = %d, want 3", got)
	}
	var statement, idle string
	if err := db.Pool.QueryRow(ctx, `
		SELECT current_setting('statement_timeout'), current_setting('idle_in_transaction_session_timeout')
	`).Scan(&statement, &idle); err != nil {
		t.Fatal(err)
	}
	if statement != "1min" || idle != "1min" {
		t.Fatalf("session limits = %s, %s, want 1min, 1min", statement, idle)
	}
}
