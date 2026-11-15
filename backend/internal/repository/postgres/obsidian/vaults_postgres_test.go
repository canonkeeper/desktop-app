// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	materialsdomain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	materialsservice "canonkeeper/backend/internal/services/authormaterials"
	service "canonkeeper/backend/internal/services/obsidian"
	authstore "canonkeeper/backend/internal/store/auth"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5/pgtype"
)

// specs/2026-11-13-desk-obsidian.md OB1.4, OB1.5 (migration 0149): the first link of a saga writes its row without a folder
// of the export; a new path replaces the path of the same row; the unlink removes the row and leaves the books and the
// records of the saga; a reader reads the link and changes nothing, a stranger sees no saga.
func TestTheLinkOfASagaWithItsVault(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_obsidian_vaults")
	accounts := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	register := func(email string) (string, auth.SessionWriteProof) {
		user, err := accounts.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: email})
		if err != nil {
			t.Fatal(err)
		}
		session, csrf, err := accounts.CreateSession(ctx, auth.NewSession{UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		_, proof, err := accounts.AuthenticateWrite(ctx, session.CookieValue, csrf)
		if err != nil {
			t.Fatal(err)
		}
		return user.ID, proof
	}
	author, proof := register("author@example.test")
	reader, _ := register("reader@example.test")
	stranger, _ := register("stranger@example.test")
	saga, err := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{}).
		CreateProject(ctx, author, projects.CreateProjectInput{Title: "Северный предел"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'viewer', clock_timestamp(), clock_timestamp())
	`, saga.ID, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := librarystore.New(db).CreateBook(ctx, library.CreateBookStoreInput{
		ID: "10000000-0000-4000-8000-0000000000b1", ProjectID: saga.ID, Title: "Застава", Order: 1, DisplayNumber: "1",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := materialsrepo.New(db, jobstore.New(db)).CreateAuthorMaterial(ctx, materialsservice.CreateCommand{
		Write:  materialsservice.Write{Actor: materialsservice.Actor{UserID: author, ProjectID: saga.ID}, Proof: proof},
		Replay: materialsservice.Replay{ScopeHash: "scope", OperationID: "author_materials.create.v1", KeyHash: "key", RequestHash: "body"},
		Draft:  materialsdomain.Draft{Name: "Ольга Сватова", Kind: materialsdomain.KindCharacter},
		Key:    "key", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	vaults := New(db)
	linked, err := vaults.Vault(ctx, author, saga.ID, service.AccessWrite)
	if err != nil || linked.Title != "Северный предел" || linked.Path != "" {
		t.Fatalf("before the link = %+v, %v", linked, err)
	}
	for _, vaultPath := range []string{"/home/author/Хранилище", "/media/author/Хранилище"} {
		if err := vaults.Link(ctx, saga.ID, vaultPath); err != nil {
			t.Fatal(err)
		}
		if linked, err := vaults.Vault(ctx, reader, saga.ID, service.AccessRead); err != nil || linked.Path != vaultPath {
			t.Fatalf("the link = %+v, %v; want %s", linked, err, vaultPath)
		}
	}
	var rows int
	var exportDir pgtype.Text
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), max(export_dir) FROM obsidian_vaults WHERE project_id = $1`, saga.ID).Scan(&rows, &exportDir); err != nil || rows != 1 || exportDir.Valid {
		t.Fatalf("rows %d, export %v, %v", rows, exportDir, err)
	}
	if _, err := vaults.Vault(ctx, reader, saga.ID, service.AccessWrite); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a reader who would change: %v", err)
	}
	if err := vaults.Unlink(ctx, reader, saga.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a reader unlinks: %v", err)
	}
	if _, err := vaults.Vault(ctx, stranger, saga.ID, service.AccessRead); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a stranger: %v", err)
	}
	if err := vaults.Unlink(ctx, author, saga.ID); err != nil {
		t.Fatal(err)
	}
	if linked, err := vaults.Vault(ctx, author, saga.ID, service.AccessRead); err != nil || linked.Path != "" {
		t.Fatalf("after the unlink = %+v, %v", linked, err)
	}
	var books, records int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL),
		       (SELECT count(*) FROM author_materials WHERE project_id = $1)
	`, saga.ID).Scan(&books, &records); err != nil || books != 1 || records != 1 {
		t.Fatalf("after the unlink: books %d, records %d, %v", books, records, err)
	}
	if err := vaults.Unlink(ctx, author, saga.ID); err != nil {
		t.Fatalf("a second unlink: %v", err)
	}
}
