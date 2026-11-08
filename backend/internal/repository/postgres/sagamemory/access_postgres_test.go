package sagamemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/services/sagareference"
	"canonkeeper/backend/internal/testutil"
)

// The saga reference reads the published text for any member and the draft for editors and
// above; a stranger does not see the project at all.
func TestReadableViewFollowsTheProjectRole(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_reference_access")
	seedMemoryProject(t, db)
	ctx := context.Background()
	now := time.Now().UTC()
	const viewerID, strangerID = "93000000-0000-4000-8000-0000000000a1", "93000000-0000-4000-8000-0000000000a2"
	for _, user := range []string{viewerID, strangerID} {
		if _, err := db.Pool.Exec(ctx, "INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,'U','hash',$3)", user, user+"@example.test", now); err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range []struct{ user, role string }{{memoryOwnerID, "owner"}, {viewerID, "viewer"}} {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO project_memberships (id,project_id,user_id,role) SELECT gen_random_uuid(),$1,$2,$3
			WHERE NOT EXISTS (SELECT 1 FROM project_memberships WHERE project_id=$1 AND user_id=$2)
		`, memoryProjectID, member.user, member.role); err != nil {
			t.Fatal(err)
		}
	}
	repository := New(db)
	for _, check := range []struct {
		actor, view string
		want        error
	}{
		{memoryOwnerID, "draft", nil},
		{viewerID, "published", nil},
		{viewerID, "draft", sagareference.ErrDenied},
		{strangerID, "published", sagareference.ErrNotFound},
		{"not-a-uuid", "published", sagareference.ErrNotFound},
	} {
		if err := repository.ReadableView(ctx, check.actor, memoryProjectID, check.view); !errors.Is(err, check.want) {
			t.Fatalf("ReadableView(%s, %s) = %v, want %v", check.actor, check.view, err, check.want)
		}
	}
}
