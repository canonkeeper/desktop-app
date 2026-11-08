package researchchat

import (
	"context"
	"strings"
	"unicode/utf8"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// UpdateSessionInput is updateChatSession: a new title. The toggle «Рисунки» is gone with the drawings of the chat
// (specs/2026-11-02-image-workshop.md IW7.1).
type UpdateSessionInput struct {
	Actor  researchdomain.Actor
	ChatID string
	Title  *string
}

type UpdateSessionStore interface {
	UpdateSessionForActor(context.Context, UpdateSessionInput) (researchdomain.Session, error)
}

// UpdateSession renames a chat for an editor or above.
type UpdateSession struct {
	store UpdateSessionStore
}

func NewUpdateSession(store UpdateSessionStore) *UpdateSession {
	if store == nil {
		panic("research chat: update-session store is required")
	}
	return &UpdateSession{store: store}
}

func (useCase *UpdateSession) Execute(ctx context.Context, input UpdateSessionInput) (researchdomain.Session, error) {
	input.Actor.UserID = strings.TrimSpace(input.Actor.UserID)
	input.ChatID = strings.TrimSpace(input.ChatID)
	if ctx == nil || input.Actor.UserID == "" || input.ChatID == "" {
		return researchdomain.Session{}, researchdomain.ErrNotFound
	}
	if input.Title == nil {
		return researchdomain.Session{}, researchdomain.ErrValidation
	}
	title := strings.TrimSpace(*input.Title)
	if title == "" {
		return researchdomain.Session{}, researchdomain.ErrTitleEmpty
	}
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > 120 {
		return researchdomain.Session{}, researchdomain.ErrTitleTooLong
	}
	input.Title = &title
	return useCase.store.UpdateSessionForActor(ctx, input)
}
