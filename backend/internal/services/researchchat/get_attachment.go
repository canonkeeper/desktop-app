package researchchat

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// GetAttachmentRepository reads the chat with the reader's rights and then its attachment.
type GetAttachmentRepository interface {
	GetSessionRepository
	ChatAttachment(ctx context.Context, projectID, chatID string, number int) (researchdomain.Attachment, error)
}

// GetAttachment is getChatAttachment (specs/2026-11-07-chat-attach-chapter.md CA4.3): the chapter attached to a
// question, for whoever reads the chat — the panel «Показать текст» and the proofreading the author starts from it.
type GetAttachment struct {
	repository GetAttachmentRepository
}

func NewGetAttachment(repository GetAttachmentRepository) *GetAttachment {
	if repository == nil {
		panic("research chat: get-attachment repository is required")
	}
	return &GetAttachment{repository: repository}
}

func (useCase *GetAttachment) Execute(ctx context.Context, actor researchdomain.Actor, chatID string, number int) (researchdomain.Attachment, error) {
	actor.UserID, chatID = strings.TrimSpace(actor.UserID), strings.TrimSpace(chatID)
	if ctx == nil || actor.UserID == "" || chatID == "" || number < 1 || number > researchdomain.MaxAttachmentNumber {
		return researchdomain.Attachment{}, researchdomain.ErrNotFound
	}
	session, err := useCase.repository.GetSession(ctx, actor, chatID)
	if err != nil {
		return researchdomain.Attachment{}, err
	}
	// The sessions of the helpers are not chats: they have no attachments to show.
	if session.Scope != researchdomain.ScopeProject || session.BookID != "" || session.Kind != agent.SessionResearch {
		return researchdomain.Attachment{}, researchdomain.ErrNotFound
	}
	return useCase.repository.ChatAttachment(ctx, session.ProjectID, session.ID, number)
}
