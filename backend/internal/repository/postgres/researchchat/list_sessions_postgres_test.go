package researchchat

import (
	"context"
	"testing"
	"time"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/researchchat"
	"canonkeeper/backend/internal/testutil"
)

// The chat list is one for the whole project: chats of both text versions, each still readable only
// by the roles that may read its version (specs/2026-10-27-one-text-version-in-ui.md).
func TestListSessionsShowsChatsOfBothTextVersions(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_list_views")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	const viewerID = "91000000-0000-4000-8000-0000000000a1"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'published',1,1)", []any{submitTurnProjectID}},
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,'rch-viewer@example.test','Viewer','hash',$2)", []any{viewerID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ('91000000-0000-4000-8000-0000000000a2',$1,$2,'viewer',$3,$3)", []any{submitTurnProjectID, viewerID, now}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	repository := New(db)
	for index, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if _, err := repository.CreateSession(ctx, service.CreateSessionCommand{
			ID: []string{submitTurnChatID, "91000000-0000-4000-8000-000000000009"}[index], ProjectID: submitTurnProjectID,
			ActorUserID: submitTurnActorID, SourceView: string(view), Scope: researchdomain.ScopeProject, Title: string(view),
			SessionWriteProof: proof, ScopeHash: "scope-" + string(view), OperationID: "research_chat.session.create.v1",
			IdempotencyKeyHash: "key-" + string(view), RequestHash: "request-" + string(view), StatusCode: 201,
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatalf("CreateSession(%s) error = %v", view, err)
		}
	}

	list := func(actorID string) []string {
		t.Helper()
		sessions, err := repository.ListSessions(ctx, service.ListSessionsQuery{
			Actor: researchdomain.Actor{UserID: actorID}, ProjectID: submitTurnProjectID, Scope: researchdomain.ScopeProject, Limit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		titles := make([]string, len(sessions))
		for index, session := range sessions {
			titles[index] = session.Title
		}
		return titles
	}
	if got := list(submitTurnActorID); len(got) != 2 || got[0] != "published" || got[1] != "draft" {
		t.Fatalf("owner chats = %v, want both versions newest first", got)
	}
	if got := list(viewerID); len(got) != 1 || got[0] != "published" {
		t.Fatalf("viewer chats = %v, want only the published one", got)
	}
}
