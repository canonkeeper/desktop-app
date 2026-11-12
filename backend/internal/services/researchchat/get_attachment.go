package researchchat

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/chapterfile"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// GetAttachmentRepository reads the chat with the reader's rights and then its attachment.
type GetAttachmentRepository interface {
	GetSessionRepository
	ChatAttachment(ctx context.Context, projectID, chatID string, number int) (researchdomain.Attachment, error)
	ChatAttachmentDocument(ctx context.Context, projectID, chatID string, number int) (*chapterfile.Document, error)
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
	session, err := useCase.chat(ctx, actor, chatID, number)
	if err != nil {
		return researchdomain.Attachment{}, err
	}
	return useCase.repository.ChatAttachment(ctx, session.ProjectID, session.ID, number)
}

// Chapter is the attachment with its parse whole and the project of its chat, for the proofreading the author starts
// from it in «Письмо» (CA5.5): the same reader's rights as Execute.
func (useCase *GetAttachment) Chapter(ctx context.Context, actor researchdomain.Actor, chatID string, number int) (researchdomain.Attachment, string, error) {
	session, err := useCase.chat(ctx, actor, chatID, number)
	if err != nil {
		return researchdomain.Attachment{}, "", err
	}
	attachment, err := useCase.repository.ChatAttachment(ctx, session.ProjectID, session.ID, number)
	if err != nil {
		return researchdomain.Attachment{}, "", err
	}
	if attachment.Document, err = useCase.repository.ChatAttachmentDocument(ctx, session.ProjectID, session.ID, number); err != nil {
		return researchdomain.Attachment{}, "", err
	}
	return attachment, session.ProjectID, nil
}

// chat is the chat of the reader whose attachment is asked; the sessions of the helpers are not chats and have none.
func (useCase *GetAttachment) chat(ctx context.Context, actor researchdomain.Actor, chatID string, number int) (researchdomain.Session, error) {
	actor.UserID, chatID = strings.TrimSpace(actor.UserID), strings.TrimSpace(chatID)
	if ctx == nil || actor.UserID == "" || chatID == "" || number < 1 || number > researchdomain.MaxAttachmentNumber {
		return researchdomain.Session{}, researchdomain.ErrNotFound
	}
	session, err := useCase.repository.GetSession(ctx, actor, chatID)
	if err != nil {
		return researchdomain.Session{}, err
	}
	if session.Scope != researchdomain.ScopeProject || session.BookID != "" || session.Kind != agent.SessionResearch {
		return researchdomain.Session{}, researchdomain.ErrNotFound
	}
	return session, nil
}
