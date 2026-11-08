package researchchat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

const fairWriterB = "91000000-0000-4000-8000-0000000000b1"

// addWriter adds a second editor of the project with a session.
func (chats *quotaChats) addWriter(userID string) auth.SessionWriteProof {
	chats.t.Helper()
	proof := auth.SessionWriteProof{SessionHash: strings.Repeat("c", 64), CSRFSecretHash: strings.Repeat("d", 64)}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,'fair-b@example.test','B','hash',$2)", []any{userID, chats.now}},
		{"INSERT INTO sessions (id,user_id,session_hash,csrf_secret_hash,expires_at,created_at) VALUES ('91000000-0000-4000-8000-0000000000b2',$1,$2,$3,$4,$5)", []any{userID, proof.SessionHash, proof.CSRFSecretHash, sessionExpiry(chats.now), chats.now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ('91000000-0000-4000-8000-0000000000b3',$1,$2,'editor',$3,$3)", []any{submitTurnProjectID, userID, chats.now}},
	} {
		if _, err := chats.db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			chats.t.Fatal(err)
		}
	}
	return proof
}

func (chats *quotaChats) askAs(userID string, proof auth.SessionWriteProof, key string, at time.Time) jobs.Job {
	chats.t.Helper()
	chats.next++
	session, err := chats.repository.CreateSession(context.Background(), service.CreateSessionCommand{
		ID: fmt.Sprintf("92000000-0000-4000-8000-%012d", chats.next), ProjectID: submitTurnProjectID,
		ActorUserID: userID, SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject,
		Title: "Fair", SessionWriteProof: proof, ScopeHash: "fair-scope-" + userID,
		OperationID: "research_chat.session.create.v1", IdempotencyKeyHash: "fair-chat-" + key,
		RequestHash: "fair-chat-request-" + key, StatusCode: 201, CreatedAt: at,
	})
	if err != nil {
		chats.t.Fatalf("CreateSession(%s) error = %v", key, err)
	}
	job, err := chats.repository.SubmitTurn(context.Background(), service.SubmitTurnCommand{
		ActorUserID: userID, Session: session, Question: "Кто такой Мирон?",
		SessionWriteProof: proof, ScopeHash: "fair-turn-" + userID, OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: key, RequestHash: key + "-request", CreatedAt: at,
	})
	if err != nil {
		chats.t.Fatalf("SubmitTurn(%s) error = %v", key, err)
	}
	return job
}

func (chats *quotaChats) claimTurn() string {
	chats.t.Helper()
	var schema string
	if err := chats.db.Pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		chats.t.Fatal(err)
	}
	var jobID string
	if err := chats.db.Pool.QueryRow(context.Background(), `
		SELECT work_id::text FROM `+pgx.Identifier{schema + "_worker"}.Sanitize()+`.claim_next_queued_job_for_worker($1, NULL)
	`, []string{string(jobs.KindResearchChatTurn)}).Scan(&jobID); err != nil {
		chats.t.Fatalf("claim: %v", err)
	}
	return jobID
}

func (chats *quotaChats) position(userID, jobID string) *int64 {
	chats.t.Helper()
	job, err := jobstore.New(chats.db).GetJob(context.Background(), jobs.GetJobStoreInput{
		ActorUserID: userID, ProjectID: submitTurnProjectID, JobID: jobID,
	})
	if err != nil {
		chats.t.Fatalf("GetJob(%s) error = %v", jobID, err)
	}
	return job.QueuePosition
}

// R10.1, R10.3: a writer with a question in work waits behind a writer without one, even when
// the latter asked later; the waiting questions know their places, a running one has none.
func TestFairQueueClaimsWriterWithFewerQuestionsInWork(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_fair_queue")
	chats.setLimits(map[quota.Key]int64{quota.KeyActiveTurns: 5})
	proofB := chats.addWriter(fairWriterB)
	// Claims compare next_attempt_at with the database clock, so the questions are asked just now.
	asked := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	a1 := chats.askAs(submitTurnActorID, chats.proof, "fair-a1", asked)
	a2 := chats.askAs(submitTurnActorID, chats.proof, "fair-a2", asked.Add(time.Second))
	b1 := chats.askAs(fairWriterB, proofB, "fair-b1", asked.Add(2*time.Second))

	if claimed := chats.claimTurn(); claimed != a1.ID {
		t.Fatalf("first claim = %s, want A1 %s", claimed, a1.ID)
	}
	if got := chats.position(submitTurnActorID, a1.ID); got != nil {
		t.Fatalf("running question has place %d", *got)
	}
	if got := chats.position(fairWriterB, b1.ID); got == nil || *got != 0 {
		t.Fatalf("B1 place = %v, want 0", got)
	}
	if got := chats.position(submitTurnActorID, a2.ID); got == nil || *got != 1 {
		t.Fatalf("A2 place = %v, want 1", got)
	}
	if claimed := chats.claimTurn(); claimed != b1.ID {
		t.Fatalf("second claim = %s, want B1 %s (A2 %s waits behind A's question in work)", claimed, b1.ID, a2.ID)
	}
	if got := chats.position(submitTurnActorID, a2.ID); got == nil || *got != 0 {
		t.Fatalf("A2 place after B1 = %v, want 0", got)
	}
}
