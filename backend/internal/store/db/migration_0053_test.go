package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// R1.5.1: migration 0053 verifies every enabled user and leaves disabled ones alone.
func TestMigration0053VerifiesEnabledUsersOnly(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_account_mail", 52)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at, disabled_at, created_at, updated_at) VALUES
		  ('00000000-0000-4000-8000-000000000101', 'enabled@example.test', 'Enabled', 'hash', NULL, NULL, '2026-09-01Z', '2026-09-01Z'),
		  ('00000000-0000-4000-8000-000000000102', 'verified@example.test', 'Verified', 'hash', '2026-09-03Z', NULL, '2026-09-02Z', '2026-09-03Z'),
		  ('00000000-0000-4000-8000-000000000103', 'disabled@example.test', 'Disabled', 'hash', NULL, '2026-09-05Z', '2026-09-04Z', '2026-09-05Z')
	`); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 52); err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]string{
		"enabled@example.test":  "2026-09-01",
		"verified@example.test": "2026-09-03",
		"disabled@example.test": "",
	} {
		var got string
		if err := db.Pool.QueryRow(ctx, `
			SELECT coalesce(to_char(email_verified_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), '') FROM users WHERE email = $1
		`, email).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s email_verified_at = %q, want %q", email, got, want)
		}
	}
	var persistent bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT column_default = 'false' AND is_nullable = 'NO'
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'sessions' AND column_name = 'persistent'
	`).Scan(&persistent); err != nil || !persistent {
		t.Fatalf("sessions.persistent default false NOT NULL = %t, %v", persistent, err)
	}
}
