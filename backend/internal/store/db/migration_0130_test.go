package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-11-tier-limits.md TL1.1, TL2.2, TL3.1: migration 0130 writes the grid over the values a tier had — «Профи»
// changed by the administrator to a new chapter every day becomes 30 new chapters a week — and clears the platform's
// values of the keys a tier sets now, keeping image_uploads_per_day.
func TestMigration0130WritesTheGridAndClearsThePlatformKeysOfATier(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_tier_grid", 129)
	const admin = "00000000-0000-4000-8000-000000001291"
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at, created_at, updated_at)
		VALUES ($1, 'grid-admin@example.test', 'Admin', 'hash', '2026-11-01Z', '2026-11-01Z', '2026-11-01Z')
	`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE subscription_tiers SET chapter_every_days = 1, updated_by = $1 WHERE code = 'pro'`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO platform_quota_defaults (singleton, active_turns, imports_per_day, project_books, project_chapters, project_characters,
		  account_chapters, account_characters, image_uploads_per_day, chapter_drafts_per_day, drawing_turns_per_day, draft_turns_per_day,
		  draft_rewrites_per_day, updated_at, updated_by)
		VALUES (true, 3, 7, 4, 900, 900000, 1000, 1000000, 9, 11, 22, 33, 44, '2026-11-01Z', $1)
	`, admin); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 129); err != nil {
		t.Fatal(err)
	}
	var cleared, uploads int
	if err := db.Pool.QueryRow(ctx, `
		SELECT num_nonnulls(active_turns, imports_per_day, project_books, project_chapters, project_characters, account_chapters,
		         account_characters, chapter_drafts_per_day, drawing_turns_per_day, draft_turns_per_day, draft_rewrites_per_day),
		       image_uploads_per_day
		FROM platform_quota_defaults
	`).Scan(&cleared, &uploads); err != nil || cleared != 0 || uploads != 9 {
		t.Fatalf("the platform after the grid: %d values of the keys of a tier, pictures %d (%v)", cleared, uploads, err)
	}
	var books int
	if err := db.Pool.QueryRow(ctx, `SELECT project_books FROM subscription_tiers WHERE code = 'writer'`).Scan(&books); err != nil || books != 12 {
		t.Fatalf("the books of a saga of «Писатель» = %d, %v", books, err)
	}
	var perWindow, everyDays int
	var by *string
	if err := db.Pool.QueryRow(ctx, `
		SELECT chapters_per_window, chapter_every_days, updated_by::text FROM subscription_tiers WHERE code = 'pro'
	`).Scan(&perWindow, &everyDays, &by); err != nil || perWindow != 30 || everyDays != 7 || by != nil {
		t.Fatalf("«Профи» after the grid = %d in %d days by %v (%v)", perWindow, everyDays, by, err)
	}
	var windowless int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM subscription_tiers WHERE chapters_per_window IS NULL AND chapter_every_days IS NULL
	`).Scan(&windowless); err != nil || windowless != 1 {
		t.Fatalf("tiers without a window = %d, %v; want «Бесплатно» alone", windowless, err)
	}
}
