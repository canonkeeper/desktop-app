// SPDX-License-Identifier: AGPL-3.0-only

package deletion

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/sagamemory"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-07-desktop-app.md, DA15.1: the project purge removes the consent to build the cards of the saga with
// the project row.
func TestProjectHardPurgeRemovesTheDesktopRowsOfTheSaga(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_purge_desktop")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000031"
		projectID = "10000000-0000-4000-8000-000000000032"
		tombstone = "10000000-0000-4000-8000-000000000034"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, 'desktop-purge@example.test', 'Author', 'hash', clock_timestamp())
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($2, $1, 'Desktop Project', '', clock_timestamp(), clock_timestamp())
	`, userID, projectID); err != nil {
		t.Fatal(err)
	}
	cards := sagamemoryrepo.New(db)
	if err := cards.SetCardBuildPermission(ctx, projectID, sagamemory.CardBuildPermission{
		State: sagamemory.CardBuildAllowed, Since: now,
	}, now); err != nil {
		t.Fatal(err)
	}
	if allowed, err := cards.CardModelAllowed(ctx, projectID); err != nil || !allowed {
		t.Fatalf("the consent before the purge = %v, %v", allowed, err)
	}

	outcome := purgeProjectForTest(t, db, projectID, tombstone, now)
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM card_build_permissions WHERE project_id=$1`, projectID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if outcome != deletionservice.PostgresStepSucceeded || left != 0 {
		t.Fatalf("the project purge = outcome:%s consents left:%d", outcome, left)
	}
}
