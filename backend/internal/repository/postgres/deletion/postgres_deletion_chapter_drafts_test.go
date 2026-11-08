package deletion

import (
	"testing"
	"time"

	deletionservice "canonkeeper/backend/internal/services/deletion"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-01-writer-assistant-index.md, R7.2: the project purge removes the drafts with the project row.
func TestProjectHardPurgeRemovesTheChapterDrafts(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_purge_chapter_drafts")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000031"
		projectID = "10000000-0000-4000-8000-000000000032"
		tombstone = "10000000-0000-4000-8000-000000000034"
	)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, 'draft-purge@example.test', 'Author', 'hash', clock_timestamp())
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($2, $1, 'Draft Project', '', clock_timestamp(), clock_timestamp())
	`, userID, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_drafts (project_id, title, kind, body, created_by, updated_by)
		VALUES ($1, 'Глава', 'sketch', 'набросок', $2, $2)
	`, projectID, userID); err != nil {
		t.Fatal(err)
	}
	outcome := purgeProjectForTest(t, db, projectID, tombstone, time.Now().UTC().Truncate(time.Microsecond))
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM chapter_drafts WHERE project_id=$1`, projectID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if outcome != deletionservice.PostgresStepSucceeded || left != 0 {
		t.Fatalf("the project purge = outcome:%s drafts left:%d", outcome, left)
	}
}
