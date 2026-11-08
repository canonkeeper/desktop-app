package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// Legal R3.3: migration 0054 gives nobody a consent; everyone accepts at the next sign-in.
func TestMigration0054LeavesExistingUsersWithoutConsent(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_legal_consent", 53)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at, created_at, updated_at)
		VALUES ('00000000-0000-4000-8000-000000000201', 'existing@example.test', 'Existing', 'hash', '2026-09-01Z', '2026-09-01Z', '2026-09-01Z')
	`); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 53); err != nil {
		t.Fatal(err)
	}
	var missing bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT terms_version IS NULL AND personal_data_consent_version IS NULL AND documents_accepted_at IS NULL
		FROM users WHERE email = 'existing@example.test'
	`).Scan(&missing); err != nil || !missing {
		t.Fatalf("existing user consent missing = %t, %v", missing, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET terms_version = '2026-10-28' WHERE email = 'existing@example.test'`); err == nil {
		t.Fatal("a half-recorded acceptance passed the check")
	}
}
