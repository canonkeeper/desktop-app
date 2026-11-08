package researchchat

import (
	"context"
	"strings"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type GetSessionRepository interface {
	GetSession(context.Context, researchdomain.Actor, string) (researchdomain.Session, error)
}

type GetSession struct {
	repository GetSessionRepository
}

func NewGetSession(repository GetSessionRepository) *GetSession {
	if repository == nil {
		panic("research chat: get-session repository is required")
	}
	return &GetSession{repository: repository}
}

func (useCase *GetSession) Execute(ctx context.Context, actor researchdomain.Actor, chatID string) (researchdomain.Session, error) {
	actor.UserID = strings.TrimSpace(actor.UserID)
	chatID = strings.TrimSpace(chatID)
	if ctx == nil || actor.UserID == "" || chatID == "" {
		return researchdomain.Session{}, researchdomain.ErrNotFound
	}
	session, err := useCase.repository.GetSession(ctx, actor, chatID)
	if err != nil {
		return researchdomain.Session{}, err
	}
	if session.Scope != researchdomain.ScopeProject || session.BookID != "" {
		return researchdomain.Session{}, researchdomain.ErrNotFound
	}
	return session, nil
}
