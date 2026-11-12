package testutil

import (
	"context"
	"testing"
	"time"

	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// AtOnce runs the statement of two workers at one moment, the way two pollers that wake in the same tick ask on a live
// site: the first runs in a transaction that is still open when the second asks, and commits only when the second has
// answered or waits for a lock the first holds. first and second read the row of each worker; their errors come back
// as they are, pgx.ErrNoRows among them.
func AtOnce(t testing.TB, db *storedb.DB, query string, args []any, first, second func(pgx.Row) error) (firstErr, secondErr error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	firstErr = first(tx.QueryRow(ctx, query, args...))

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var pid uint32
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	secondCtx, stop := context.WithCancel(ctx)
	defer stop()
	answered := make(chan error, 1)
	go func() { answered <- second(conn.QueryRow(secondCtx, query, args...)) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case secondErr = <-answered:
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			return firstErr, secondErr
		case <-time.After(10 * time.Millisecond):
		}
		var waits bool
		if err := db.Pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock')
		`, pid).Scan(&waits); err != nil {
			t.Fatal(err)
		}
		if waits {
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			return firstErr, <-answered
		}
		if time.Now().After(deadline) {
			stop()
			<-answered
			t.Fatal("the second worker neither answered nor waited for the first")
		}
	}
}
