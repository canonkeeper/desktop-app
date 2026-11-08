package researchchat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	service "canonkeeper/backend/internal/services/researchchat"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// R3.2, R3.3: saga tools act only for the bound running turn and retain every chapter
// they return, so deleting such a chapter purges the chat thread and answer.
func TestSagaToolsRetainReturnedChaptersForGovernedPurge(t *testing.T) {
	for _, scope := range []researchdomain.Scope{researchdomain.ScopeBook, researchdomain.ScopeProject} {
		t.Run(string(scope), func(t *testing.T) {
			turn := startSagaToolsTurn(t, "ck_rch_saga_reads", scope)
			db, ctx, repository, tools, call := turn.db, context.Background(), turn.repository, turn.tools, turn.call
			stranger := service.ToolContext{Caller: agent.ToolCaller{ThreadID: "root-other", RootThreadID: "root-other"}, TurnID: call.TurnID}
			if _, err := tools.Search(ctx, stranger, service.SearchInput{Queries: []string{"additional"}}); !errors.Is(err, researchdomain.ErrForbidden) {
				t.Fatalf("search from another root thread = %v, want forbidden", err)
			}
			dependencies := func() int {
				t.Helper()
				var count int
				if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM artifact_source_refs ref
					JOIN artifact_source_owners owner ON owner.id=ref.owner_id AND owner.project_id=ref.project_id
					WHERE ref.project_id=$1::uuid AND ref.chapter_id=$2::uuid
					  AND ((owner.artifact_kind='job' AND owner.artifact_id=$3::uuid)
					    OR (owner.artifact_kind='chat_turn' AND owner.artifact_id=$4::uuid))
					  AND ref.chat_session_id=$5::uuid`, submitTurnProjectID, turn.chapterID, turn.job.ID, turn.run.TurnID, turn.session.ID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				return count
			}
			found, err := tools.Search(ctx, call, service.SearchInput{Queries: []string{"additional"}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(found, "1 абзацев в 1 главах") || !strings.Contains(found, "- [т. 1, «Additional Chapter» ¶1](source:b1c1/1): additional chapter text") {
				t.Fatalf("search = %q", found)
			}
			if got := dependencies(); got != 2 {
				t.Fatalf("search dependencies = %d, want Job and turn", got)
			}
			for repeat := 0; repeat < 2; repeat++ {
				read, err := tools.Read(ctx, call, service.ReadInput{Chapter: "b1c1"})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(read, "¶1 additional chapter text") {
					t.Fatalf("read = %q", read)
				}
				if got := dependencies(); got != 2 {
					t.Fatalf("read dependencies after repeat %d = %d, want Job and turn", repeat, got)
				}
			}
			if replay, err := repository.SubmitTurn(ctx, turn.command); err != nil || replay.ID != turn.job.ID {
				t.Fatalf("request replay after source read = %q, %v", replay.ID, err)
			}
			run := turn.run
			if err := repository.FinishResponse(ctx, researchdomain.FinishResponse{
				ProjectID: run.ProjectID, ChatID: run.ChatID, TurnID: run.TurnID, JobID: run.JobID,
				ClaimIncarnation: 1, RootThreadID: call.Caller.RootThreadID, ProviderTurnID: call.TurnID, Response: "Exact final answer.",
			}); err != nil {
				t.Fatalf("FinishResponse after saga reads: %v", err)
			}
			if _, err := tools.Read(ctx, call, service.ReadInput{Chapter: "b1c1"}); !errors.Is(err, researchdomain.ErrForbidden) {
				t.Fatalf("read after the turn finished = %v, want forbidden", err)
			}
			var status, response string
			if err := db.Pool.QueryRow(ctx, `SELECT status, result_json->>'response' FROM jobs WHERE id=$1::uuid`, turn.job.ID).Scan(&status, &response); err != nil {
				t.Fatal(err)
			}
			if status != "succeeded" || response != "Exact final answer." {
				t.Fatalf("final Job = %q, response = %q", status, response)
			}
			assertSourceDeletionKeepsChat(t, db, turn.chapterID, turn.job, turn.session.ID, call.Caller.RootThreadID)
		})
	}
}

// Codex 0.164 and later run the tool calls of one model step at once: every call of the turn
// succeeds and retains its chapters (specs/2026-10-28-codex-0165.md UC9).
func TestSagaToolsOfOneStepRunAtOnce(t *testing.T) {
	turn := startSagaToolsTurn(t, "ck_rch_saga_reads_at_once", researchdomain.ScopeProject)
	ctx := context.Background()
	var wait sync.WaitGroup
	failures := make([]error, 8)
	for index := range failures {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if index%2 == 0 {
				_, failures[index] = turn.tools.Read(ctx, turn.call, service.ReadInput{Chapter: "b1c1"})
			} else {
				_, failures[index] = turn.tools.Search(ctx, turn.call, service.SearchInput{Queries: []string{"additional"}})
			}
		}()
	}
	wait.Wait()
	for index, err := range failures {
		if err != nil {
			t.Errorf("call %d of one step: %v", index, err)
		}
	}
}

// sagaToolsTurn is a running chat turn bound to its native root thread and turn, with the saga
// tools of the backend.
type sagaToolsTurn struct {
	db         *storedb.DB
	repository *Repository
	tools      *service.ReadTools
	call       service.ToolContext
	command    service.SubmitTurnCommand
	job        jobs.Job
	session    researchdomain.Session
	run        researchdomain.TurnRun
	chapterID  string
	proof      auth.SessionWriteProof
	now        time.Time
}

func startSagaToolsTurn(t *testing.T, database string, scope researchdomain.Scope) sagaToolsTurn {
	t.Helper()
	return startSagaToolsTurnAsking(t, database, scope, "Read the next chapter")
}

func startSagaToolsTurnAsking(t *testing.T, database string, scope researchdomain.Scope, question string) sagaToolsTurn {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, database)
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	chapterID := seedAdditionalTimelineChapter(t, db, now)
	return startTurnInChat(t, db, proof, now, chapterID, submitTurnChatID, scope, question)
}

// startTurnInChat creates a chat of the seeded project and a running turn of it asking question.
func startTurnInChat(
	t *testing.T, db *storedb.DB, proof auth.SessionWriteProof, now time.Time, chapterID, chatID string, scope researchdomain.Scope, question string,
) sagaToolsTurn {
	t.Helper()
	ctx := context.Background()
	repository := New(db)
	bookID := submitTurnBookID
	if scope == researchdomain.ScopeProject {
		bookID = ""
	}
	session, err := repository.CreateSession(ctx, service.CreateSessionCommand{
		ID: chatID, ProjectID: submitTurnProjectID, ActorUserID: submitTurnActorID,
		SourceView: string(governance.SourceViewDraft), Scope: scope, BookID: bookID,
		Title: "Research", SessionWriteProof: proof, ScopeHash: "read-session-scope" + chatID,
		OperationID: "research_chat.session.create.v1", IdempotencyKeyHash: "read-session-key" + chatID,
		RequestHash: "read-session-request" + chatID, StatusCode: 201, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: question, SessionWriteProof: proof,
		ScopeHash: "read-turn-scope" + chatID, OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: "read-turn-key" + chatID, RequestHash: "read-turn-request" + chatID, CreatedAt: now.Add(time.Second),
	}
	job, err := repository.SubmitTurn(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	subject := job.Subject.(jobs.ResearchChatTurnSubject)
	rootThreadID, providerTurnID := "root-read-sources-"+chatID, "turn-read-sources-"+chatID
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE jobs SET status='running', progress_json='{"phase":"working"}',
			claimed_at=$2, started_at=$2, claim_incarnation=1, updated_at=$2 WHERE id=$1::uuid`, job.ID, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	run := researchdomain.TurnRun{
		ProjectID: submitTurnProjectID, ChatID: session.ID, TurnID: subject.TurnID, JobID: job.ID,
		ActorUserID: submitTurnActorID, ClaimIncarnation: 1,
	}
	if err := repository.BindRoot(ctx, run, rootThreadID); err != nil {
		t.Fatal(err)
	}
	if err := repository.BindTurn(ctx, run, rootThreadID, providerTurnID); err != nil {
		t.Fatal(err)
	}
	return sagaToolsTurn{
		db: db, repository: repository,
		tools:   service.NewReadTools(repository, sagamemoryservice.NewSaga(sagamemoryrepo.New(db), sagamemoryrepo.New(db)), noScanModel{}, nil, service.ScanSettings{}, sagamemoryrepo.New(db)),
		call:    service.ToolContext{Caller: agent.ToolCaller{ThreadID: rootThreadID, RootThreadID: rootThreadID}, TurnID: providerTurnID},
		command: command, job: job, session: session, run: run, chapterID: chapterID, proof: proof, now: now,
	}
}

func seedAdditionalTimelineChapter(t *testing.T, db *storedb.DB, now time.Time) string {
	t.Helper()
	const chapterID = "91000000-0000-4000-8000-000000000009"
	const paragraphID = "91000000-0000-4000-8000-00000000000a"
	generation, err := governance.SourceGenerationDigest(governance.SourceGenerationVector{
		ProjectID: submitTurnProjectID, ProjectGeneration: 1, BookID: submitTurnBookID, BookGeneration: 1,
		ChapterID: chapterID, ChapterGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	contentHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: paragraphID, Order: 0, Content: "additional chapter text",
	}})
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO chapters (id,project_id,book_id,title,"order",status,revision,created_at,updated_at) VALUES ($1,$2,$3,'Additional Chapter',1,'draft',1,$4,$4)`, []any{chapterID, submitTurnProjectID, submitTurnBookID, now}},
		{`INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count) VALUES ($1,$2,1,'draft','paragraph',0,'additional chapter text','additional chapter text',3)`, []any{paragraphID, chapterID}},
		{`INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,'draft',1,$2,1,$3,1,1,'Additional Chapter',1)`, []any{submitTurnProjectID, submitTurnBookID, chapterID}},
		{`INSERT INTO chapter_text_search_rows (project_id,source_view,book_id,chapter_id,source_revision,locator_source_generation,title,content,chapter_content_hash,indexed_at) VALUES ($1,'draft',$2,$3,1,$4,'Additional Chapter','additional chapter text',$5,$6)`, []any{submitTurnProjectID, submitTurnBookID, chapterID, generation[:], contentHash[:], now}},
		{`INSERT INTO chapter_text_index_state (project_id,source_view,chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at) VALUES ($1,'draft',$2,1,$3,$4,$5)`, []any{submitTurnProjectID, chapterID, generation[:], contentHash[:], now}},
	}
	for _, statement := range statements {
		if _, err := db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	testutil.SeedSagaSearchRows(t, db, submitTurnProjectID)
	return chapterID
}

// R3.6: new chats are created with the saga memory tool set; older chats read as outdated.
func TestNewChatsUseCurrentAgentGenerationPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_agent_generation")
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	repository := New(db)
	session, err := repository.CreateSession(ctx, service.CreateSessionCommand{
		ID: submitTurnChatID, ProjectID: submitTurnProjectID, ActorUserID: submitTurnActorID,
		SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject,
		Title: "Research", SessionWriteProof: proof, ScopeHash: "generation-scope",
		OperationID: "research_chat.session.create.v1", IdempotencyKeyHash: "generation-key",
		RequestHash: "generation-request", StatusCode: 201, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := researchdomain.Actor{UserID: submitTurnActorID}
	current, err := repository.GetSession(ctx, actor, session.ID)
	if err != nil || current.AgentGeneration != researchdomain.CurrentAgentGeneration || current.Outdated {
		t.Fatalf("new chat = generation %d outdated %v, %v", current.AgentGeneration, current.Outdated, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chat_sessions SET agent_generation=1 WHERE id=$1::uuid`, session.ID); err != nil {
		t.Fatal(err)
	}
	old, err := repository.GetSession(ctx, actor, session.ID)
	if err != nil || old.AgentGeneration != 1 || !old.Outdated {
		t.Fatalf("summary/graph chat = generation %d outdated %v, %v", old.AgentGeneration, old.Outdated, err)
	}
}

// noScanModel stands in for the scan model, which these reads never call.
type noScanModel struct{}

func (noScanModel) Complete(context.Context, string, string) (string, quota.ModelUsage, error) {
	return "", quota.ModelUsage{}, errors.New("scan model is not used by these reads")
}
