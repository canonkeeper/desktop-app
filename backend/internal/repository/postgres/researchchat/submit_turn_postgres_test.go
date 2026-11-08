package researchchat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

const (
	submitTurnActorID      = "91000000-0000-4000-8000-000000000001"
	submitTurnAuthSession  = "91000000-0000-4000-8000-000000000002"
	submitTurnProjectID    = "91000000-0000-4000-8000-000000000003"
	submitTurnMembershipID = "91000000-0000-4000-8000-000000000004"
	submitTurnBookID       = "91000000-0000-4000-8000-000000000005"
	submitTurnChapterID    = "91000000-0000-4000-8000-000000000006"
	submitTurnParagraphID  = "91000000-0000-4000-8000-000000000007"
	submitTurnChatID       = "91000000-0000-4000-8000-000000000008"
)

func TestSubmitTurnCreatesQueuedJobWithSessionChatArmRefs(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_submit_turn")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	repository := New(db)

	create := service.CreateSessionCommand{
		ID: submitTurnChatID, ProjectID: submitTurnProjectID, ActorUserID: submitTurnActorID,
		SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject,
		Title: "Research", SavedAssignment: "Что известно о событии?",
		SessionWriteProof: proof, ScopeHash: "create-scope", OperationID: "research_chat.session.create.v1",
		IdempotencyKeyHash: "create-key", RequestHash: "create-request", StatusCode: 201, CreatedAt: now,
	}
	session, err := repository.CreateSession(ctx, create)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if session.SavedAssignment == nil || session.SavedAssignment.Body != "Что известно о событии?" ||
		len(session.SavedAssignment.AvailableActions) != 1 || session.SavedAssignment.AvailableActions[0] != researchdomain.SavedAssignmentStart {
		t.Fatalf("created saved assignment = %#v", session.SavedAssignment)
	}
	if _, err := repository.SubmitTurn(ctx, service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Другая задача",
		SessionWriteProof: proof, ScopeHash: "turn-mismatch-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: "turn-mismatch-key", RequestHash: "turn-mismatch-request", CreatedAt: now.Add(time.Second),
	}); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("mismatched saved assignment error = %v, want ErrConflict", err)
	}

	job, err := repository.SubmitTurn(ctx, service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Что известно о событии?",
		SessionWriteProof: proof, ScopeHash: "turn-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: "turn-key", RequestHash: "turn-request", CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if job.ID == "" || job.Kind != jobs.KindResearchChatTurn || job.Status != jobs.StatusQueued {
		t.Fatalf("SubmitTurn() job = %+v", job)
	}
	reloaded, err := repository.GetSession(ctx, researchdomain.Actor{UserID: submitTurnActorID}, session.ID)
	if err != nil || reloaded.SavedAssignment != nil || reloaded.LatestTurnJobID != job.ID ||
		reloaded.LatestTurnStatus != string(jobs.StatusQueued) {
		t.Fatalf("reloaded committed session = %#v, %v", reloaded, err)
	}
	listed, err := repository.ListSessions(ctx, service.ListSessionsQuery{
		Actor: researchdomain.Actor{UserID: submitTurnActorID}, ProjectID: submitTurnProjectID,
		Scope: researchdomain.ScopeProject, Limit: 10,
	})
	if err != nil || len(listed) != 1 || listed[0].LatestTurnJobID != job.ID || listed[0].LatestTurnStatus != string(jobs.StatusQueued) {
		t.Fatalf("listed sessions = %#v, %v; want the chat with its queued turn", listed, err)
	}
	replayed, err := repository.CreateSession(ctx, create)
	if err != nil || replayed.ID != session.ID || replayed.SavedAssignment != nil {
		t.Fatalf("create replay after Start = %#v, %v", replayed, err)
	}
	renamedTitle := "Renamed after Start"
	if _, err := repository.UpdateSessionForActor(ctx, service.UpdateSessionInput{
		Actor: researchdomain.Actor{UserID: submitTurnActorID}, ChatID: session.ID, Title: &renamedTitle,
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err = repository.CreateSession(ctx, create)
	if err != nil || replayed.ID != session.ID || replayed.Title != "Renamed after Start" || replayed.SavedAssignment != nil {
		t.Fatalf("create replay after rename = %#v, %v", replayed, err)
	}
	changedRequest := create
	changedRequest.RequestHash = "different-create-request"
	if _, err := repository.CreateSession(ctx, changedRequest); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("changed create request error = %v, want ErrConflict", err)
	}
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok || subject.ProjectID != submitTurnProjectID || subject.ChatID != session.ID ||
		subject.SourceView != governance.SourceViewDraft || subject.Scope != string(researchdomain.ScopeProject) ||
		subject.BookID != nil || subject.TurnID == "" {
		t.Fatalf("SubmitTurn() subject = %#v", job.Subject)
	}

	var workflowInputID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT workflow_input_id::text
		FROM chat_turns
		WHERE id=$1::uuid AND chat_id=$2::uuid AND project_id=$3::uuid AND job_id=$4::uuid
	`, subject.TurnID, session.ID, submitTurnProjectID, job.ID).Scan(&workflowInputID); err != nil {
		t.Fatalf("created chat turn: %v", err)
	}

	wantArtifacts := map[string]string{
		string(governance.ArtifactJob):                job.ID,
		string(governance.ArtifactAgentWorkflowInput): workflowInputID,
		string(governance.ArtifactChatTurn):           subject.TurnID,
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT owner.artifact_kind, owner.artifact_id::text,
		       ref.chat_session_id::text, ref.chat_lifecycle_epoch, ref.chapter_id::text
		FROM artifact_source_owners AS owner
		JOIN artifact_source_refs AS ref
		  ON ref.owner_id=owner.id AND ref.project_id=owner.project_id
		WHERE owner.project_id=$1::uuid
		  AND ((owner.artifact_kind='job' AND owner.artifact_id=$2::uuid)
		    OR (owner.artifact_kind='agent_workflow_input' AND owner.artifact_id=$3::uuid)
		    OR (owner.artifact_kind='chat_turn' AND owner.artifact_id=$4::uuid))
	`, submitTurnProjectID, job.ID, workflowInputID, subject.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(wantArtifacts))
	for rows.Next() {
		var kind, artifactID, chatID, chapterID string
		var chatEpoch int64
		if err := rows.Scan(&kind, &artifactID, &chatID, &chatEpoch, &chapterID); err != nil {
			t.Fatal(err)
		}
		if wantArtifacts[kind] != artifactID || chatID != session.ID ||
			chatEpoch != int64(session.LifecycleEpoch) || chapterID != submitTurnChapterID {
			t.Fatalf("%s source ref = artifact:%s chat:%s/%d chapter:%s", kind, artifactID, chatID, chatEpoch, chapterID)
		}
		seen[kind] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(wantArtifacts) {
		t.Fatalf("chat-bound artifact kinds = %v, want %v", seen, wantArtifacts)
	}
}

// sessionExpiry keeps the seeded session alive for an hour of the database clock too: the
// session fence compares expires_at with the real time, while tests may fix their own now.
func sessionExpiry(now time.Time) time.Time {
	expires := now.Add(time.Hour)
	if real := time.Now().UTC().Add(time.Hour); real.After(expires) {
		return real
	}
	return expires
}

func seedSubmitTurnCanonicalBaseline(t *testing.T, db *storedb.DB, now time.Time) auth.SessionWriteProof {
	t.Helper()
	proof := auth.SessionWriteProof{SessionHash: strings.Repeat("a", 64), CSRFSecretHash: strings.Repeat("b", 64)}
	vector := governance.SourceGenerationVector{
		ProjectID: submitTurnProjectID, ProjectGeneration: 1,
		BookID: submitTurnBookID, BookGeneration: 1,
		ChapterID: submitTurnChapterID, ChapterGeneration: 1,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	contentHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: submitTurnParagraphID, Order: 0, Content: "canonical text",
	}})
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,'rch-submit@example.test','RCH','hash',$2)", []any{submitTurnActorID, now}},
		{"INSERT INTO sessions (id,user_id,session_hash,csrf_secret_hash,expires_at,created_at) VALUES ($1,$2,$3,$4,$5,$6)", []any{submitTurnAuthSession, submitTurnActorID, proof.SessionHash, proof.CSRFSecretHash, sessionExpiry(now), now}},
		{"INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'RCH Submit','',$3,$3)", []any{submitTurnProjectID, submitTurnActorID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ($1,$2,$3,'owner',$4,$4)", []any{submitTurnMembershipID, submitTurnProjectID, submitTurnActorID, now}},
		{"INSERT INTO books (id,project_id,title,\"order\",display_number,status,revision,created_at,updated_at) VALUES ($1,$2,'RCH Book',1,'1','draft',1,$3,$3)", []any{submitTurnBookID, submitTurnProjectID, now}},
		{"INSERT INTO chapters (id,project_id,book_id,title,\"order\",status,revision,created_at,updated_at) VALUES ($1,$2,$3,'RCH Chapter',0,'draft',1,$4,$4)", []any{submitTurnChapterID, submitTurnProjectID, submitTurnBookID, now}},
		{"INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,\"order\",markdown,plain_text,word_count) VALUES ($1,$2,1,'draft','paragraph',0,'canonical text','canonical text',2)", []any{submitTurnParagraphID, submitTurnChapterID}},
		{"INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'draft',1,1)", []any{submitTurnProjectID}},
		{"INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,'draft',1,$2,1,'RCH Book')", []any{submitTurnProjectID, submitTurnBookID}},
		{"INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,'draft',1,$2,1,$3,1,1,'RCH Chapter',0)", []any{submitTurnProjectID, submitTurnBookID, submitTurnChapterID}},
		{"INSERT INTO chapter_text_search_rows (project_id,source_view,book_id,chapter_id,source_revision,locator_source_generation,title,content,chapter_content_hash,indexed_at) VALUES ($1,'draft',$2,$3,1,$4,'RCH Chapter','canonical text',$5,$6)", []any{submitTurnProjectID, submitTurnBookID, submitTurnChapterID, generation[:], contentHash[:], now}},
		{"INSERT INTO chapter_text_index_state (project_id,source_view,chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at) VALUES ($1,'draft',$2,1,$3,$4,$5)", []any{submitTurnProjectID, submitTurnChapterID, generation[:], contentHash[:], now}},
	}
	for _, statement := range statements {
		if _, err := db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return proof
}
