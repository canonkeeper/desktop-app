package researchchat

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/researchchat"
	"canonkeeper/backend/internal/testutil"
)

// R1.4.1, R1.4.2: an unverified writer still creates a chat but asks nothing:
// no job and no turn appear.
func TestSubmitTurnRequiresVerifiedEmail(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_submit_unverified")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	if _, err := db.Pool.Exec(ctx, "UPDATE users SET email_verified_at = NULL WHERE id = $1", submitTurnActorID); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	session, err := repository.CreateSession(ctx, service.CreateSessionCommand{
		ID: submitTurnChatID, ProjectID: submitTurnProjectID, ActorUserID: submitTurnActorID,
		SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject, Title: "Research",
		SessionWriteProof: proof, ScopeHash: "create-scope", OperationID: "research_chat.session.create.v1",
		IdempotencyKeyHash: "create-key", RequestHash: "create-request", StatusCode: 201, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSession() for an unverified writer error = %v", err)
	}
	_, err = repository.SubmitTurn(ctx, service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Что известно о событии?",
		SessionWriteProof: proof, ScopeHash: "turn-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: "turn-key", RequestHash: "turn-request", CreatedAt: now.Add(time.Second),
	})
	if !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("SubmitTurn() error = %v, want ErrEmailNotVerified", err)
	}
	for _, table := range []string{"jobs", "chat_turns"} {
		var count int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE project_id = $1", submitTurnProjectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows after unverified turn = %d, want 0", table, count)
		}
	}
}
