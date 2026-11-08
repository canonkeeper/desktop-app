package researchchat

import (
	"context"
	"errors"
	"testing"
	"time"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	renameOwnerID    = "00000000-0000-4000-8000-000000009001"
	renameViewerID   = "00000000-0000-4000-8000-000000009002"
	renameOutsiderID = "00000000-0000-4000-8000-000000009003"
	renameProjectID  = "00000000-0000-4000-8000-000000009004"
	renameChatID     = "00000000-0000-4000-8000-000000009005"
)

func titleOf(value string) *string { return &value }

func TestUpdateSessionForActorEnforcesRoleConcealmentAndDeletionRace(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_chat_rename")
	seedRenameSession(t, db)
	repository := New(db)
	ctx := context.Background()

	renamed, err := repository.UpdateSessionForActor(ctx, service.UpdateSessionInput{
		Actor: researchdomain.Actor{UserID: renameOwnerID}, ChatID: renameChatID, Title: titleOf("Renamed"),
	})
	if err != nil || renamed.Title != "Renamed" || renamed.ProjectID != renameProjectID || renamed.Scope != researchdomain.ScopeProject ||
		renamed.SourceView != "published" || renamed.LifecycleEpoch != 1 {
		t.Fatalf("owner rename = %#v, %v", renamed, err)
	}
	for name, actor := range map[string]string{"viewer": renameViewerID, "outsider": renameOutsiderID} {
		t.Run(name, func(t *testing.T) {
			_, err := repository.UpdateSessionForActor(ctx, service.UpdateSessionInput{
				Actor: researchdomain.Actor{UserID: actor}, ChatID: renameChatID, Title: titleOf("Denied"),
			})
			want := researchdomain.ErrForbidden
			if name == "outsider" {
				want = researchdomain.ErrNotFound
			}
			if !errors.Is(err, want) {
				t.Fatalf("rename error = %v, want %v", err, want)
			}
		})
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE chat_sessions SET created_at=created_at-interval '1 day' WHERE id=$1", renameChatID); err == nil {
		t.Fatal("rename migration allowed an immutable identity change")
	} else {
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Fatalf("identity update error = %v, want check violation", err)
		}
	}
	markResearchChatDeletedForTest(t, db, renameProjectID, renameChatID, "00000000-0000-4000-8000-000000009008", time.Now().UTC().Truncate(time.Microsecond))
	if _, err := repository.UpdateSessionForActor(ctx, service.UpdateSessionInput{
		Actor: researchdomain.Actor{UserID: renameOwnerID}, ChatID: renameChatID, Title: titleOf("Too late"),
	}); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("deleted rename error = %v, want ErrConflict", err)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE chat_sessions SET title='Too late' WHERE id=$1", renameChatID); err == nil {
		t.Fatal("rename migration allowed a deleted title change")
	} else {
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Fatalf("deleted title update error = %v, want check violation", err)
		}
	}
}

func seedRenameSession(t *testing.T, db *storedb.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,$3,'hash',$4)", []any{renameOwnerID, "rename-owner@example.test", "Owner", now}},
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,$3,'hash',$4)", []any{renameViewerID, "rename-viewer@example.test", "Viewer", now}},
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,$3,'hash',$4)", []any{renameOutsiderID, "rename-outsider@example.test", "Outsider", now}},
		{"INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Rename Project','',$3,$3)", []any{renameProjectID, renameOwnerID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ('00000000-0000-4000-8000-000000009006',$1,$2,'owner',$3,$3)", []any{renameProjectID, renameOwnerID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ('00000000-0000-4000-8000-000000009007',$1,$2,'viewer',$3,$3)", []any{renameProjectID, renameViewerID, now}},
		{"INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'published',1,1)", []any{renameProjectID}},
		{"INSERT INTO chat_sessions (id,project_id,source_view,scope,title,created_at,updated_at) VALUES ($1,$2,'published','project','Original',$3,$3)", []any{renameChatID, renameProjectID, now}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}
