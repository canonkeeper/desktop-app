package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-13-desk-consent.md CN3.1: migration 0148 keeps the consents written before it — by tests alone — without a
// source and a host, so they do not act and the reading waits for a new consent; a paused one is paused by the author. The
// Jobs there are before it — the cards Jobs that wait, the Job that runs — are taken by
// indexingstore.TestMigration0148TakesTheJobsThereAre.
func TestMigration0148KeepsTheConsentsOfBeforeWithoutASource(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_consent", 147)
	const (
		author  = "00000000-0000-4000-8000-000000001481"
		allowed = "00000000-0000-4000-8000-000000001482"
		paused  = "00000000-0000-4000-8000-000000001483"
	)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ($1, 'consent@example.test', 'A', 'hash', now())`, author); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{allowed, paused} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at) VALUES ($1, $2, 'Северный предел', '', now(), now())`, project, author); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO card_build_permissions (project_id, state, budget_usd_micros, since, updated_at) VALUES ($1, 'allowed', 20000000, now(), now()),
		  ($2, 'paused', NULL, now(), now())`, allowed, paused); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 147); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Pool.Query(ctx, `SELECT project_id::text, state, coalesce(pause, ''), source IS NULL, provider_host IS NULL, stop_unit, stop_amount IS NULL
		FROM card_build_permissions ORDER BY project_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var project, state, pause, unit string
		var noSource, noHost, noStop bool
		if err := rows.Scan(&project, &state, &pause, &noSource, &noHost, &unit, &noStop); err != nil {
			t.Fatal(err)
		}
		seen++
		if !noSource || !noHost || unit != "tokens" || !noStop {
			t.Fatalf("the consent of %s after 0148: no source %v, no host %v, unit %s, no stop %v", project, noSource, noHost, unit, noStop)
		}
		if project == paused && (state != "paused" || pause != "author") || project == allowed && (state != "allowed" || pause != "") {
			t.Fatalf("the consent of %s after 0148 = %s %q", project, state, pause)
		}
	}
	if seen != 2 {
		t.Fatalf("consents after 0148 = %d", seen)
	}
}
