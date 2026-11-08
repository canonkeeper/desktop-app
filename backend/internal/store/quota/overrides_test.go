package quotastore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-10-29-admin-panel.md R3.2: the port of SetOverrides changes a live writer in one
// transaction; a deleted, unknown or malformed id is not found and nothing is written.
func TestStoreSetOverridesChangesOnlyALiveWriter(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_overrides")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "writer-a@example.test")
	seed.user(quotaWriterB, "writer-b@example.test")
	seed.exec(`UPDATE users SET disabled_at = $2, deleted_at = $2 WHERE id = $1`, quotaWriterB, seed.now)
	store := New(db)
	ctx := context.Background()
	sixty := int64(60)
	changes := []quota.OverrideChange{{Key: quota.KeyChatTurnsPerDay, Value: &sixty}}

	changed, err := store.SetOverrides(ctx, quotaWriterA, changes, seed.now)
	if err != nil || len(changed) != 1 || changed[0].Before != nil || *changed[0].After != 60 {
		t.Fatalf("changed = %+v, %v", changed, err)
	}
	overrides, err := store.Overrides(ctx, quotaWriterA)
	if err != nil || overrides[quota.KeyChatTurnsPerDay] == nil || *overrides[quota.KeyChatTurnsPerDay] != 60 {
		t.Fatalf("overrides = %v, %v", overrides, err)
	}
	for _, id := range []string{quotaWriterB, "93000000-0000-4000-8000-0000000000ff", "not-an-id"} {
		if _, err := store.SetOverrides(ctx, id, changes, seed.now); !errors.Is(err, quota.ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", id, err)
		}
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM writer_quota_overrides`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("override rows = %d, %v; want only writer A", rows, err)
	}
}
