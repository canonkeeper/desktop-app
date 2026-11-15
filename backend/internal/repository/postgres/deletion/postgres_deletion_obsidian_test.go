// SPDX-License-Identifier: AGPL-3.0-only

package deletion

import (
	"strings"
	"testing"
	"time"

	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	obsidianservice "canonkeeper/backend/internal/services/obsidian"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-13-desk-obsidian.md OB1.6: the purge of a saga removes its link with a vault and the links of its books with
// the folders of the vault; the files of the vault are not the product's to touch.
func TestProjectHardPurgeRemovesTheLinkOfItsVault(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_project_purge_obsidian")
	ctx := t.Context()
	const (
		userID    = "10000000-0000-4000-8000-000000000041"
		projectID = "10000000-0000-4000-8000-000000000042"
		tombstone = "10000000-0000-4000-8000-000000000044"
		itemID    = "10000000-0000-4000-8000-000000000045"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, 'obsidian-purge@example.test', 'Author', 'hash', clock_timestamp())
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($2, $1, 'Северный предел', '', clock_timestamp(), clock_timestamp())
	`, userID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := obsidianrepo.New(db).Link(ctx, projectID, "/home/author/Хранилище"); err != nil {
		t.Fatal(err)
	}
	// specs/2026-11-14-desk-obsidian-books.md OK0.2: the links of the books with the folders of the vault go with the saga.
	if err := obsidianrepo.NewBooks(db).AddVolumes(ctx, projectID, []obsidianservice.VolumeLink{{
		Folder: "Книга 1", ItemID: itemID, Fingerprint: strings.Repeat("a", 64),
	}}, now); err != nil {
		t.Fatal(err)
	}
	outcome := purgeProjectForTest(t, db, projectID, tombstone, now)
	var left, books int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM obsidian_vaults WHERE project_id = $1), (SELECT count(*) FROM obsidian_books WHERE project_id = $1)
	`, projectID).Scan(&left, &books); err != nil {
		t.Fatal(err)
	}
	if outcome != deletionservice.PostgresStepSucceeded || left != 0 || books != 0 {
		t.Fatalf("the project purge = outcome:%s links left:%d, of books:%d", outcome, left, books)
	}
}
