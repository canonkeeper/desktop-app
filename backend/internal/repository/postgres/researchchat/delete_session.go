package researchchat

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
		deletedAt := time.Now().UTC().Truncate(time.Microsecond)
		projectID, _ := storedb.ParseUUID(locked.ProjectID)
		chatUUID, _ := storedb.ParseUUID(locked.ID)
		if _, err := sqlc.New(tx).MarkChatSessionDeleted(ctx, sqlc.MarkChatSessionDeletedParams{
			DeletedAt: storedb.Timestamptz(deletedAt), PurgeAfter: storedb.Timestamptz(deletedAt.Add(24 * time.Hour)),
			ChatID: chatUUID, ProjectID: projectID, LifecycleEpoch: int64(locked.LifecycleEpoch),
		}); errors.Is(err, pgx.ErrNoRows) {
			return researchdomain.ErrConflict
		} else if err != nil {
			return mapWriteError(err)
		}
		var tombstoneID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				chat_session_id, chat_lifecycle_epoch, status, fenced_at, purge_deadline,
				created_at, updated_at
			) VALUES (gen_random_uuid(), $1, 'governed_online', 'chat', 'chat_epoch', $2,
				$2, $3, 'pending', $4, $5, $4, $4)
			RETURNING id
		`, projectID, chatUUID, int64(locked.LifecycleEpoch), deletedAt, deletedAt.Add(24*time.Hour)).Scan(&tombstoneID); err != nil {
			return err
		}
		for _, deletionTarget := range deletiondomain.SourceDeletionTargets() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_target_tasks (
					tombstone_id, project_id, target, state, attempts, claim_incarnation, next_attempt_at
				) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
			`, tombstoneID, projectID, string(deletionTarget), deletedAt); err != nil {
				return err
			}
		}
		if err := deletionstore.InsertPostgresStepRegistry(ctx, tx, tombstoneID.String(), projectID.String(),
			governance.DeletionSelectionChat, governance.PurgeModeChatEpoch); err != nil {
			return err
		}
		target = researchdomain.DeleteTarget{Flow: flow, ProjectID: locked.ProjectID, ChatID: locked.ID, RootThreadID: locked.ThreadID}
		return nil
	})
	return target, mapError(err)
}
