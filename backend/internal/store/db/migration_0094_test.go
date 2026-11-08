package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-03-voice-assistant.md V4.6: migration 0094 moves an author's voice of gpt-realtime to the closest
// voice of GPT-Live, keeps «not chosen» and takes only the voices of GPT-Live afterwards.
func TestMigration0094MovesTheVoicesToGPTLive(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_voice_live", 93)
	chosen := map[string]string{"marin": "breeze", "sage": "vale", "cedar": "spruce", "ash": "arbor", "": ""}
	index := 0
	for before := range chosen {
		index++
		id := "00000000-0000-4000-8000-00000000094" + string(rune('0'+index))
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO users (id, email, display_name, password_hash, email_verified_at, created_at, updated_at, assistant_voice)
			VALUES ($1, $2, 'Author', 'hash', '2026-11-01Z', '2026-11-01Z', '2026-11-01Z', NULLIF($3, ''))
		`, id, "voice-"+before+"@example.test", before); err != nil {
			t.Fatal(err)
		}
	}
	if err := testutil.ApplyNextMigration(t, db, 93); err != nil {
		t.Fatal(err)
	}
	for before, after := range chosen {
		var voice string
		if err := db.Pool.QueryRow(ctx, `SELECT COALESCE(assistant_voice, '') FROM users WHERE email = $1`,
			"voice-"+before+"@example.test").Scan(&voice); err != nil || voice != after {
			t.Fatalf("%q became %q, want %q (%v)", before, voice, after, err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET assistant_voice = 'ember' WHERE email = 'voice-@example.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET assistant_voice = 'cedar' WHERE email = 'voice-@example.test'`); err == nil {
		t.Fatal("a voice of gpt-realtime was taken after the migration")
	}
}
