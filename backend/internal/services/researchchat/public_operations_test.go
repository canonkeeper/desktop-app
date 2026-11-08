package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

type sessionRepositoryStub struct {
	created CreateSessionCommand
	session researchdomain.Session
	listed  ListSessionsQuery
	rows    []researchdomain.Session
	pages   [][]researchdomain.Session
	queries []ListSessionsQuery
	actor   researchdomain.Actor
	chatID  string
	started SubmitTurnCommand
	job     jobs.Job
}

func (stub *sessionRepositoryStub) CreateSession(_ context.Context, command CreateSessionCommand) (researchdomain.Session, error) {
	stub.created = command
	return stub.session, nil
}

func (stub *sessionRepositoryStub) ListSessions(_ context.Context, query ListSessionsQuery) ([]researchdomain.Session, error) {
	stub.listed = query
	stub.queries = append(stub.queries, query)
	if len(stub.pages) > 0 {
		rows := stub.pages[0]
		stub.pages = stub.pages[1:]
		return rows, nil
	}
	return stub.rows, nil
}

func (stub *sessionRepositoryStub) GetSession(_ context.Context, actor researchdomain.Actor, chatID string) (researchdomain.Session, error) {
	stub.actor = actor
	stub.chatID = chatID
	return stub.session, nil
}

func (stub *sessionRepositoryStub) SubmitTurn(_ context.Context, command SubmitTurnCommand) (jobs.Job, error) {
	stub.started = command
	return stub.job, nil
}

type idStub struct{ value string }

func (stub idStub) NewID() string { return stub.value }

type clockStub struct{ value time.Time }

func (stub clockStub) Now() time.Time { return stub.value }

func TestCreateSessionPreservesActorSessionAndIdempotencyAuthority(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	proof := auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}
	repository := &sessionRepositoryStub{session: researchdomain.Session{ID: "chat-1", ProjectID: "project-1"}}
	input := CreateSessionInput{
		ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject,
		Title: "Исследование", SavedAssignment: "  Проверь первую главу  ",
		IdempotencyKey: "idempotency-1", RequestMethod: "post",
		RequestPath: "/api/v1/research-chats", SessionWriteProof: proof,
	}

	created, err := NewCreateSession(repository, idStub{value: "chat-1"}, clockStub{value: now}).
		Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "chat-1" || repository.created.ActorUserID != "user-1" ||
		repository.created.SavedAssignment != "Проверь первую главу" ||
		repository.created.SessionWriteProof != proof || repository.created.OperationID != "research_chat.session.create.v1" ||
		repository.created.ScopeHash == "" || repository.created.IdempotencyKeyHash == "" || repository.created.RequestHash == "" ||
		!repository.created.CreatedAt.Equal(now) {
		t.Fatalf("create command = %+v", repository.created)
	}
}

func TestSubmitTurnPreservesQuestionContextAndIdempotencyAuthority(t *testing.T) {
	proof := auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}
	projectID := "10000000-0000-4000-8000-000000000001"
	bookID := "20000000-0000-4000-8000-000000000002"
	chapterID := "30000000-0000-4000-8000-000000000003"
	session := researchdomain.Session{
		ID: "chat-1", ProjectID: projectID, SourceView: "draft", Scope: researchdomain.ScopeProject,
		LifecycleEpoch: 2, AgentGeneration: researchdomain.CurrentAgentGeneration, Kind: agent.SessionResearch,
	}
	repository := &sessionRepositoryStub{
		session: session,
		job:     jobs.Job{ID: "job-1", ProjectID: projectID, Kind: jobs.KindResearchChatTurn},
	}
	locator := governance.CanonicalChapterSourceLocatorV1{
		ProjectID: projectID, SourceView: governance.SourceViewDraft, BookID: bookID, ChapterID: chapterID, SourceRef: chapterID,
		SourceRevision: 4, SourceGeneration: governance.Digest{1}, ContentHash: governance.Digest{2},
	}
	input := SubmitTurnInput{
		Question: "Что произошло?", ContextLocators: []governance.CanonicalChapterSourceLocatorV1{locator},
		IdempotencyKey: "idempotency-2", RequestMethod: "post", RequestPath: "/api/v1/research-chats/chat-1/turns",
		SessionWriteProof: proof,
	}

	job, err := NewSubmitTurn(repository, clockStub{value: time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)}).
		Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, session.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	command := repository.started
	if job.ID != "job-1" || command.ActorUserID != "user-1" || command.Session != session ||
		command.Question != input.Question || len(command.ContextLocators) != 1 || command.ContextLocators[0] != locator ||
		command.SessionWriteProof != proof || command.OperationID != "research_chat.turn.create.v1" ||
		command.ScopeHash == "" || command.IdempotencyKeyHash == "" || command.RequestHash == "" {
		t.Fatalf("submit command = %+v", command)
	}
}

// R3.6: a chat of the summary/graph tool set stays readable but takes no new turn.
func TestSubmitTurnRejectsChatOfOutdatedAgentGeneration(t *testing.T) {
	repository := &sessionRepositoryStub{session: researchdomain.Session{
		ID: "chat-1", ProjectID: "10000000-0000-4000-8000-000000000001", SourceView: "draft",
		Scope: researchdomain.ScopeProject, LifecycleEpoch: 1, AgentGeneration: 1, Outdated: true, Kind: agent.SessionResearch,
	}}
	_, err := NewSubmitTurn(repository, clockStub{value: time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)}).
		Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", SubmitTurnInput{
			Question: "Что произошло?", IdempotencyKey: "idempotency-3", RequestMethod: "post",
			RequestPath:       "/api/v1/chats/chat-1/turns",
			SessionWriteProof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"},
		})
	if !errors.Is(err, researchdomain.ErrOutdatedAgent) || repository.started.ActorUserID != "" {
		t.Fatalf("turn in an outdated chat = %v, started %+v", err, repository.started)
	}
}

// specs/2026-11-02-image-workshop.md IW4.2: the session of the helper of a drawing takes messages only from its
// workshop; a question of the chat to it finds no chat.
func TestSubmitTurnRejectsTheSessionOfAHelper(t *testing.T) {
	repository := &sessionRepositoryStub{session: researchdomain.Session{
		ID: "chat-1", ProjectID: "10000000-0000-4000-8000-000000000001", SourceView: "published",
		Scope: researchdomain.ScopeProject, LifecycleEpoch: 1, AgentGeneration: researchdomain.CurrentAgentGeneration, Kind: agent.SessionDrawing,
	}}
	_, err := NewSubmitTurn(repository, clockStub{value: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}).
		Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", SubmitTurnInput{
			Question: "Что произошло?", IdempotencyKey: "idempotency-4", RequestMethod: "post",
			RequestPath:       "/api/v1/chats/chat-1/turns",
			SessionWriteProof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"},
		})
	if !errors.Is(err, researchdomain.ErrNotFound) || repository.started.ActorUserID != "" {
		t.Fatalf("a question of the chat to a helper = %v, started %+v", err, repository.started)
	}
}

func TestListAndGetSessionsKeepExactActorScope(t *testing.T) {
	session := researchdomain.Session{ID: "chat-1", ProjectID: "project-1", SourceView: "published", Scope: researchdomain.ScopeProject}
	repository := &sessionRepositoryStub{
		session: session,
		rows:    []researchdomain.Session{session},
	}
	actor := researchdomain.Actor{UserID: "user-1"}
	input := ListSessionsInput{
		ProjectID: "project-1", Scope: researchdomain.ScopeProject,
		Page: researchdomain.Page{Limit: 25},
	}
	page, err := NewListSessions(repository).Execute(context.Background(), actor, input)
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("list = (%+v, %v)", page, err)
	}
	if repository.listed.Actor != actor || repository.listed.ProjectID != input.ProjectID ||
		repository.listed.Scope != input.Scope ||
		repository.listed.Limit != 26 {
		t.Fatalf("list query = %+v", repository.listed)
	}
	got, err := NewGetSession(repository).Execute(context.Background(), actor, session.ID)
	if err != nil || got != session || repository.actor != actor || repository.chatID != session.ID {
		t.Fatalf("get = (%+v, %v), actor=%+v chat=%q", got, err, repository.actor, repository.chatID)
	}
}

func TestListSessionsAtMaximumLimitProbesForNextRow(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	rows := make([]researchdomain.Session, 100)
	for index := range rows {
		rows[index] = researchdomain.Session{ID: "chat-" + string(rune('a'+index%26)), UpdatedAt: now.Add(-time.Duration(index) * time.Minute)}
	}
	repository := &sessionRepositoryStub{pages: [][]researchdomain.Session{
		rows,
		{{ID: "chat-next", UpdatedAt: now.Add(-101 * time.Minute)}},
	}}
	page, err := NewListSessions(repository).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, ListSessionsInput{
		ProjectID: "project-1", Scope: researchdomain.ScopeProject,
		Page: researchdomain.Page{Limit: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Meta.HasMore || len(page.Data) != 100 || len(repository.queries) != 2 ||
		repository.queries[0].Limit != 100 || repository.queries[1].Limit != 1 {
		t.Fatalf("max page = (%+v, queries=%+v)", page.Meta, repository.queries)
	}
}

// specs/2026-11-02-image-workshop.md IW7.2: a question of the chat carries no images — the author's images live in
// the drawing workshop — and hashes as a question did before images came to the chat and left it.
func TestSubmitTurnHashesAQuestionWithoutImages(t *testing.T) {
	session := researchdomain.Session{
		ID: "chat-1", ProjectID: "10000000-0000-4000-8000-000000000001", SourceView: "published",
		Scope: researchdomain.ScopeProject, LifecycleEpoch: 1, AgentGeneration: researchdomain.CurrentAgentGeneration, Kind: agent.SessionResearch,
	}
	repository := &sessionRepositoryStub{session: session}
	_, err := NewSubmitTurn(repository, clockStub{value: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)}).
		Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", SubmitTurnInput{
			Question: "Нарисуй его так", IdempotencyKey: "idempotency-4", RequestMethod: "post",
			RequestPath:       "/api/v1/chats/chat-1/turns",
			SessionWriteProof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"},
		})
	if err != nil {
		t.Fatal(err)
	}
	command := repository.started
	encoded, _ := json.Marshal(struct {
		ChatID          string                                       `json:"chatId"`
		LifecycleEpoch  int64                                        `json:"lifecycleEpoch"`
		Question        string                                       `json:"question"`
		ContextLocators []governance.CanonicalChapterSourceLocatorV1 `json:"contextLocators"`
	}{"chat-1", 1, "Нарисуй его так", nil})
	if len(command.Images) != 0 || command.RequestHash != hashText(string(encoded)) {
		t.Fatalf("question = %+v", command)
	}
}
