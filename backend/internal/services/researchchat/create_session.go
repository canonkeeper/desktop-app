package researchchat

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type CreateSessionInput struct {
	ProjectID         string
	SourceView        string
	Scope             researchdomain.Scope
	BookID            string
	Title             string
	SavedAssignment   string
	IdempotencyKey    string
	RequestMethod     string
	RequestPath       string
	SessionWriteProof auth.SessionWriteProof
}

type CreateSessionCommand struct {
	ID                 string
	ProjectID          string
	ActorUserID        string
	SourceView         string
	Scope              researchdomain.Scope
	BookID             string
	Title              string
	SavedAssignment    string
	SessionWriteProof  auth.SessionWriteProof
	ScopeHash          string
	OperationID        string
	IdempotencyKeyHash string
	RequestHash        string
	StatusCode         int
	CreatedAt          time.Time
}

type CreateSessionRepository interface {
	CreateSession(context.Context, CreateSessionCommand) (researchdomain.Session, error)
}

type CreateSession struct {
	repository CreateSessionRepository
	ids        IDGenerator
	clock      Clock
}

func NewCreateSession(repository CreateSessionRepository, ids IDGenerator, clock Clock) *CreateSession {
	if repository == nil || ids == nil || clock == nil {
		panic("research chat: create-session dependencies are required")
	}
	return &CreateSession{repository: repository, ids: ids, clock: clock}
}

func (useCase *CreateSession) Execute(ctx context.Context, actor researchdomain.Actor, input CreateSessionInput) (researchdomain.Session, error) {
	actor.UserID = strings.TrimSpace(actor.UserID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.BookID = strings.TrimSpace(input.BookID)
	input.Title = strings.TrimSpace(input.Title)
	input.SavedAssignment = strings.TrimSpace(input.SavedAssignment)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.RequestMethod = strings.ToUpper(strings.TrimSpace(input.RequestMethod))
	input.RequestPath = strings.TrimSpace(input.RequestPath)
	if input.Title == "" {
		input.Title = defaultSessionTitle
	}
	if ctx == nil || actor.UserID == "" || input.ProjectID == "" || input.RequestMethod == "" || input.RequestPath == "" ||
		!validIdempotencyKey(input.IdempotencyKey) || !input.SessionWriteProof.Valid() ||
		len([]rune(input.Title)) > 180 || !validOptionalAssignment(input.SavedAssignment) ||
		!validSessionShape(input.SourceView, input.Scope, input.BookID) {
		return researchdomain.Session{}, researchdomain.ErrValidation
	}
	request, err := json.Marshal(struct {
		ProjectID       string               `json:"projectId"`
		SourceView      string               `json:"sourceView"`
		Scope           researchdomain.Scope `json:"scope"`
		BookID          string               `json:"bookId,omitempty"`
		Title           string               `json:"title"`
		SavedAssignment string               `json:"savedAssignment,omitempty"`
	}{input.ProjectID, input.SourceView, input.Scope, input.BookID, input.Title, input.SavedAssignment})
	if err != nil {
		return researchdomain.Session{}, err
	}
	chatID := strings.TrimSpace(useCase.ids.NewID())
	if chatID == "" {
		return researchdomain.Session{}, researchdomain.ErrValidation
	}
	return useCase.repository.CreateSession(ctx, CreateSessionCommand{
		ID: chatID, ProjectID: input.ProjectID, ActorUserID: actor.UserID,
		SourceView: input.SourceView, Scope: input.Scope, BookID: input.BookID, Title: input.Title,
		SavedAssignment:   input.SavedAssignment,
		SessionWriteProof: input.SessionWriteProof,
		ScopeHash:         requestScopeHash(actor.UserID, input.RequestMethod, input.RequestPath),
		OperationID:       createSessionOperation, IdempotencyKeyHash: hashText(input.IdempotencyKey),
		RequestHash: hashText(string(request)), StatusCode: 201, CreatedAt: useCase.clock.Now(),
	})
}

func validOptionalAssignment(value string) bool {
	return value == "" || utf8.ValidString(value) && utf8.RuneCountInString(value) <= 8000
}

func validSessionShape(view string, scope researchdomain.Scope, bookID string) bool {
	if view != "draft" && view != "published" {
		return false
	}
	return scope == researchdomain.ScopeProject && bookID == ""
}
