package researchchat

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// MarkDeleted fences a chat session of the kind asked and queues the deletion of its native root tree: a chat
// by deleteChatSession, the session of a helper by the deletion of its drawing (specs/2026-11-02-image-workshop.md
// IW6.4); a session of another kind is not found.
func (repository *Repository) MarkDeleted(ctx context.Context, actor researchdomain.Actor, chatID, kind string) (researchdomain.DeleteTarget, error) {
	current, err := repository.getSession(ctx, actor.UserID, chatID)
	if err != nil {
		return researchdomain.DeleteTarget{}, mapError(err)
	}
	flow, known := agent.SessionFlow(current.Kind)
	if !known || current.Kind != kind {
		return researchdomain.DeleteTarget{}, researchdomain.ErrNotFound
	}
	var target researchdomain.DeleteTarget
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		locked, err := repository.lockWritableSessionInTx(ctx, tx, current.ProjectID, chatID, actor.UserID)
		if err != nil {
			return err
		}
		projectID, _ := storedb.ParseUUID(locked.ProjectID)
		chatUUID, _ := storedb.ParseUUID(locked.ID)
		if err := deletionstore.FenceChatDeletionInTx(ctx, tx, projectID, chatUUID, int64(locked.LifecycleEpoch), time.Now()); errors.Is(err, deletionstore.ErrChatNotLive) {
			return researchdomain.ErrConflict
		} else if err != nil {
			return mapWriteError(err)
		}
		target = researchdomain.DeleteTarget{Flow: flow, ProjectID: locked.ProjectID, ChatID: locked.ID, RootThreadID: locked.ThreadID}
		return nil
	})
	return target, mapError(err)
}
