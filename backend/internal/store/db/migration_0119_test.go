package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-08-subscription-tiers.md TR6.6: migration 0119 moves the right to illustrations to the tiers. Active
// access becomes none — drawing by the day is the tier's now; what an account spent of the old trial of 3 becomes his
// trial drawings used (5 left — none used, 1 left — 2 used, 0 left — 3 used), a block stays a block; the requests for
// access and the platform's trial for new accounts are gone. The trials used join the counters of the account of 0118 in
// its one row: a count of refused keys already there stays.
func TestMigration0119MovesTheRightToIllustrationsToTheTiers(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_tier_drawings", 118)
	rights := []struct {
		id, state string
		trialLeft int
		until     string
		wantState string
		wantUsed  int
	}{
		{"00000000-0000-4000-8000-000000001181", "active", 5, "2026-12-01", "none", 0},
		{"00000000-0000-4000-8000-000000001182", "none", 1, "", "none", 2},
		{"00000000-0000-4000-8000-000000001183", "blocked", 0, "", "blocked", 3},
		{"00000000-0000-4000-8000-000000001184", "active", 20, "", "none", 0},
	}
	for index, right := range rights {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO users (id, email, display_name, password_hash, email_verified_at, created_at, updated_at)
			VALUES ($1, $2, 'Author', 'hash', '2026-11-01Z', '2026-11-01Z', '2026-11-01Z')
		`, right.id, "drawings-"+string(rune('a'+index))+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO illustration_access (user_id, state, trial_left, active_until, updated_at)
			VALUES ($1, $2, $3, NULLIF($4, '')::date, '2026-11-01Z')
		`, right.id, right.state, right.trialLeft, right.until); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO illustration_access_requests (id, user_id, status, message, created_at, sent_at)
		VALUES ('00000000-0000-4000-8000-0000000011a1', $1, 'pending', 'Для глав', '2026-11-01Z', '2026-11-01Z')
	`, rights[1].id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO platform_quota_defaults (singleton, trial_drawings, updated_at, updated_by) VALUES (true, 9, '2026-11-01Z', $1)
	`, rights[0].id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO subscription_usage (user_id, key_failures, key_failures_day, updated_at) VALUES ($1, 4, '2026-11-01', '2026-11-01Z')
	`, rights[1].id); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 118); err != nil {
		t.Fatal(err)
	}
	var failures int
	if err := db.Pool.QueryRow(ctx, `SELECT key_failures FROM subscription_usage WHERE user_id = $1`, rights[1].id).Scan(&failures); err != nil || failures != 4 {
		t.Fatalf("refused keys after the migration = %d, %v; want 4", failures, err)
	}
	for _, right := range rights {
		var state string
		var used int
		if err := db.Pool.QueryRow(ctx, `
			SELECT access.state, usage.trial_images_used
			FROM illustration_access AS access JOIN subscription_usage AS usage ON usage.user_id = access.user_id
			WHERE access.user_id = $1
		`, right.id).Scan(&state, &used); err != nil || state != right.wantState || used != right.wantUsed {
			t.Fatalf("%s with %d left became %s with %d used, want %s with %d (%v)",
				right.state, right.trialLeft, state, used, right.wantState, right.wantUsed, err)
		}
	}
	var columns int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND (
			(table_name = 'illustration_access' AND column_name IN ('trial_left', 'active_until')) OR
			(table_name = 'platform_quota_defaults' AND column_name = 'trial_drawings'))
	`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("columns of the old right left = %d, %v", columns, err)
	}
	var requests *string
	if err := db.Pool.QueryRow(ctx, `SELECT to_regclass('public.illustration_access_requests')::text`).Scan(&requests); err != nil || requests != nil {
		t.Fatalf("the requests for access stayed: %v, %v", requests, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE illustration_access SET state = 'active' WHERE user_id = $1`, rights[0].id); err == nil {
		t.Fatal("access by the administrator was taken after the migration")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE subscription_usage SET trial_images_used = -1 WHERE user_id = $1`, rights[0].id); err == nil {
		t.Fatal("a negative count of trial drawings was taken")
	}
}
