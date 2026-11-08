package researchchat

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type DeletionRepository interface {
	// MarkDeleted fences a chat session of the kind and names its native root tree; another kind is not found.
	MarkDeleted(ctx context.Context, actor researchdomain.Actor, chatID, kind string) (researchdomain.DeleteTarget, error)
}

type GovernedDelete func(context.Context, researchdomain.DeleteTarget) error

// DeleteSession deletes a chat session of one kind with its native root tree: deleteChatSession deletes a chat;
// the deletion of a drawing deletes the session of its helper the same way (specs/2026-11-02-image-workshop.md
// IW6.4).
type DeleteSession struct {
	repository DeletionRepository
	deleteRoot GovernedDelete
	kind       string
}

func NewDeleteSession(repository DeletionRepository, deleteRoot GovernedDelete) *DeleteSession {
	return newDeleteSession(repository, deleteRoot, agent.SessionResearch)
}

// NewDeleteDrawingSession deletes the session of the helper of a drawing.
func NewDeleteDrawingSession(repository DeletionRepository, deleteRoot GovernedDelete) *DeleteSession {
	return newDeleteSession(repository, deleteRoot, agent.SessionDrawing)
}

// NewDeleteDraftSession deletes the session of the helper of a chapter draft (specs/2026-11-02-writer-workshop.md W7.5).
func NewDeleteDraftSession(repository DeletionRepository, deleteRoot GovernedDelete) *DeleteSession {
	return newDeleteSession(repository, deleteRoot, agent.SessionDraft)
}

func newDeleteSession(repository DeletionRepository, deleteRoot GovernedDelete, kind string) *DeleteSession {
	if repository == nil || deleteRoot == nil {
		panic("research chat: deletion dependencies are required")
	}
	return &DeleteSession{repository: repository, deleteRoot: deleteRoot, kind: kind}
}

func (useCase *DeleteSession) Execute(ctx context.Context, actor researchdomain.Actor, chatID string) error {
	actor.UserID = strings.TrimSpace(actor.UserID)
	chatID = strings.TrimSpace(chatID)
	if ctx == nil || actor.UserID == "" || chatID == "" {
		return researchdomain.ErrValidation
	}
	target, err := useCase.repository.MarkDeleted(ctx, actor, chatID, useCase.kind)
	if err != nil {
		return err
	}
	if target.ChatID != chatID || target.ProjectID == "" {
		return researchdomain.ErrConflict
	}
	return useCase.deleteRoot(ctx, target)
}
