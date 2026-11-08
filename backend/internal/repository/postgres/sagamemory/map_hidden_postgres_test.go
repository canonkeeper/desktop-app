package sagamemory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	reference "canonkeeper/backend/internal/services/sagareference"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

// mapWriterForTest gives a member of the project a session and the write proof of it.
func mapWriterForTest(t *testing.T, db *storedb.DB, user, role, sessionHash string) reference.MapWrite {
	t.Helper()
	ctx := context.Background()
	proof := auth.SessionWriteProof{SessionHash: strings.Repeat(sessionHash, 64), CSRFSecretHash: strings.Repeat("c", 64)}
	if user != memoryOwnerID {
		if _, err := db.Pool.Exec(ctx, "INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,'U','hash',now())", user, user+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO project_memberships (id,project_id,user_id,role) SELECT gen_random_uuid(),$1,$2,$3
		WHERE NOT EXISTS (SELECT 1 FROM project_memberships WHERE project_id=$1 AND user_id=$2)
	`, memoryProjectID, user, role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO sessions (id,user_id,session_hash,csrf_secret_hash,expires_at,created_at) VALUES (gen_random_uuid(),$1,$2,$3,$4,now())
	`, user, proof.SessionHash, proof.CSRFSecretHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return reference.MapWrite{Scope: reference.Scope{ActorUserID: user, ProjectID: memoryProjectID, View: "draft"}, Proof: proof}
}

// specs/2026-11-04-saga-map.md R11.3: a decision of the author shows at the next read without a rebuild, is one decision however
// many times it is made, needs an editor and goes when a source is deleted, as the rows of the map do.
func TestWhatTheAuthorHidLivesInPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_hidden")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	if status, err := mapCardsJob(t, repository, &namesCardModel{}).RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	owner := mapWriterForTest(t, db, memoryOwnerID, "owner", "a")
	viewer := mapWriterForTest(t, db, "93000000-0000-4000-8000-0000000000b1", "viewer", "b")
	hide := reference.NewHideOnMap(anyReader{}, repository, repository)
	graph, nodes := mapUseCases(repository)

	result, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	before := decodedMapRow(t, result)
	if len(before.Nodes) == 0 {
		t.Fatalf("no nodes: %+v", before)
	}
	target := before.Nodes[len(before.Nodes)-1]
	if _, err := hide.Execute(ctx, viewer, reference.HideInput{NodeKey: target.Key}); !errors.Is(err, reference.ErrDenied) {
		t.Fatalf("a viewer hides: %v", err)
	}
	stale := owner
	stale.Proof.CSRFSecretHash = strings.Repeat("d", 64)
	if _, err := hide.Execute(ctx, stale, reference.HideInput{NodeKey: target.Key}); err == nil {
		t.Fatal("a stale write proof hides")
	}
	first, err := hide.Execute(ctx, owner, reference.HideInput{NodeKey: target.Key})
	if err != nil {
		t.Fatal(err)
	}
	again, err := hide.Execute(ctx, owner, reference.HideInput{NodeKey: target.Key})
	if err != nil || again.ID != first.ID || first.NodeName != target.Name || first.CreatedAt.IsZero() {
		t.Fatalf("hide = %+v, again %+v, %v", first, again, err)
	}
	graphs, rowNodes, names := mapRowsForTest(t, db)
	result, err = graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	if after := decodedMapRow(t, result); len(after.Nodes) != len(before.Nodes)-1 || after.Revision != before.Revision {
		t.Fatalf("after a decision = %+v", after)
	}
	if g, n, m := mapRowsForTest(t, db); g != graphs || n != rowNodes || m != names {
		t.Fatalf("the rows of the map moved: %d %d %d", g, n, m)
	}
	if _, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: target.Key}); err != nil {
		t.Fatalf("the card of a hidden node: %v", err)
	}
	restore := reference.NewRestoreOnMap(repository)
	if err := restore.Execute(ctx, viewer, first.ID); !errors.Is(err, reference.ErrDenied) {
		t.Fatalf("a viewer returns: %v", err)
	}
	if err := restore.Execute(ctx, owner, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := restore.Execute(ctx, owner, first.ID); !errors.Is(err, reference.ErrNotFound) {
		t.Fatalf("returned twice: %v", err)
	}
	if items, err := repository.MapHiddenItems(ctx, memoryProjectID); err != nil || len(items) != 0 {
		t.Fatalf("items = %+v, %v", items, err)
	}

	if _, err := hide.Execute(ctx, owner, reference.HideInput{NodeKey: target.Key}); err != nil {
		t.Fatal(err)
	}
	deleteChapterForTest(t, db, memoryChapters[2])
	if items, err := repository.MapHiddenItems(ctx, memoryProjectID); err != nil || len(items) != 0 {
		t.Fatalf("after the deletion of a source = %+v, %v", items, err)
	}
}
