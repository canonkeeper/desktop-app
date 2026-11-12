package deletion

import (
	"context"
	"errors"
	"time"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrChatNotLive means the chat a fence names is deleted already or holds another lifecycle epoch.
var ErrChatNotLive = errors.New("deletion: the chat is not live at the epoch named")

// fenceChatsEmptiedBySagaDeletionInTx deletes the chats a deletion of a saga emptied, in the transaction that ends it
// (specs/2026-11-11-ux7-trash.md TB2.3). The deletion of a saga takes the questions, answers and native root trees of
// its chats for good; once all its targets are done, a chat of the saga left without a turn would come back empty with
// a restored saga, so it goes by the deletion of a chat. A chat asked in after a return keeps its turn and stays; a new
// chat after the fence is not the saga's deletion's. The sessions of the helpers of drawings and chapters stay with
// what they belong to; a member who lost his place deletes nothing here.
func fenceChatsEmptiedBySagaDeletionInTx(ctx context.Context, tx pgx.Tx, tombstone deletion.Tombstone, projectID pgtype.UUID) error {
	fields, err := tombstone.Identity.SelectedVector.Fields()
	if err != nil {
		return err
	}
	if tombstone.Identity.Mode != governance.PurgeModeDerivedGeneration || fields.Kind != governance.DeletionSelectionProject ||
		tombstone.Identity.PermissionLossActorUserID != "" {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT chat.id, chat.lifecycle_epoch FROM chat_sessions AS chat
		WHERE chat.project_id = $1 AND chat.kind = 'research' AND chat.deleted_at IS NULL AND chat.created_at <= $2
		  AND NOT EXISTS (SELECT 1 FROM chat_turns AS turn WHERE turn.project_id = chat.project_id AND turn.chat_id = chat.id)
		ORDER BY chat.id
		FOR UPDATE OF chat
	`, projectID, tombstone.Identity.FencedAt)
	if err != nil {
		return err
	}
	type emptiedChat struct {
		id    pgtype.UUID
		epoch int64
	}
	chats, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (emptiedChat, error) {
		var chat emptiedChat
		return chat, row.Scan(&chat.id, &chat.epoch)
	})
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if err := FenceChatDeletionInTx(ctx, tx, projectID, chat.id, chat.epoch, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

// FenceChatDeletionInTx is the one deletion of a chat in the caller's transaction: the session takes its next lifecycle
// epoch and its 24-hour window, and the chat epoch tombstone of the epoch it had queues the deletion of its turns, its
// exact native root tree and its row. deleteChatSession fences one chat by it; the deletion of a saga fences every chat
// of the saga by it, so a chat does not come back empty with a restored saga (specs/2026-11-11-ux7-trash.md TB2.3).
func FenceChatDeletionInTx(ctx context.Context, tx pgx.Tx, projectID, chatID pgtype.UUID, epoch int64, at time.Time) error {
	deletedAt := at.UTC().Truncate(time.Microsecond)
	purgeAfter := deletedAt.Add(24 * time.Hour)
	if _, err := sqlc.New(tx).MarkChatSessionDeleted(ctx, sqlc.MarkChatSessionDeletedParams{
		DeletedAt: pgtype.Timestamptz{Time: deletedAt, Valid: true}, PurgeAfter: pgtype.Timestamptz{Time: purgeAfter, Valid: true},
		ChatID: chatID, ProjectID: projectID, LifecycleEpoch: epoch,
	}); errors.Is(err, pgx.ErrNoRows) {
		return ErrChatNotLive
	} else if err != nil {
		return err
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
	`, projectID, chatID, epoch, deletedAt, purgeAfter).Scan(&tombstoneID); err != nil {
		return err
	}
	for _, target := range deletion.SourceDeletionTargets() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_target_tasks (
				tombstone_id, project_id, target, state, attempts, claim_incarnation, next_attempt_at
			) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
		`, tombstoneID, projectID, string(target), deletedAt); err != nil {
			return err
		}
	}
	return InsertPostgresStepRegistry(ctx, tx, tombstoneID.String(), projectID.String(),
		governance.DeletionSelectionChat, governance.PurgeModeChatEpoch)
}
