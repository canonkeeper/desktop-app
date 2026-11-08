package researchchat

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

func TestCreateSessionRejectsBookScope(t *testing.T) {
	for _, scope := range []researchdomain.Scope{researchdomain.ScopeBook, researchdomain.ScopeProject} {
		t.Run(string(scope), func(t *testing.T) {
			repository := &sessionRepositoryStub{}
			_, err := NewCreateSession(repository, idStub{value: "chat-1"}, clockStub{value: time.Now()}).Execute(
				context.Background(), researchdomain.Actor{UserID: "actor-1"}, CreateSessionInput{
					ProjectID: "project-1", SourceView: "draft", Scope: scope, BookID: "book-1",
					IdempotencyKey: "saga-only-request", RequestMethod: "POST", RequestPath: "/projects/project-1/chats",
					SessionWriteProof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"},
				},
			)
			if !errors.Is(err, researchdomain.ErrValidation) || repository.created.ID != "" {
				t.Fatalf("book-scoped create accepted: command=%#v, err=%v", repository.created, err)
			}
		})
	}
}

func TestListSessionsRejectsBookFilter(t *testing.T) {
	repository := &sessionRepositoryStub{}
	_, err := NewListSessions(repository).Execute(context.Background(), researchdomain.Actor{UserID: "actor-1"}, ListSessionsInput{
		ProjectID: "project-1", Scope: researchdomain.ScopeBook, BookID: "book-1",
	})
	if !errors.Is(err, researchdomain.ErrValidation) || len(repository.queries) != 0 {
		t.Fatalf("book filter accepted: %v", err)
	}
}

func TestUnmigratedBookChatCannotOpenOrStartANewTurn(t *testing.T) {
	repository := &sessionRepositoryStub{session: researchdomain.Session{
		ID: "chat-1", ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeBook, BookID: "book-1",
	}}
	actor := researchdomain.Actor{UserID: "actor-1"}
	if _, err := NewGetSession(repository).Execute(context.Background(), actor, "chat-1"); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("legacy book chat exposed by GET: %v", err)
	}
	_, err := NewSubmitTurn(repository, clockStub{value: time.Now()}).Execute(context.Background(), actor, "chat-1", SubmitTurnInput{
		Question: "New question", IdempotencyKey: "saga-only-request", RequestMethod: "POST", RequestPath: "/chats/chat-1/turns",
		SessionWriteProof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"},
	})
	if !errors.Is(err, researchdomain.ErrNotFound) || repository.started.Question != "" {
		t.Fatalf("legacy book chat started a turn: %v", err)
	}
}
