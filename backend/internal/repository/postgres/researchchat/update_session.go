package researchchat

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// UpdateSessionForActor renames a chat for an editor or above; a deleted chat is a conflict.
func (repository *Repository) UpdateSessionForActor(ctx context.Context, input service.UpdateSessionInput) (researchdomain.Session, error) {
	actorID, actorErr := storedb.ParseUUID(input.Actor.UserID)
	chatID, chatErr := storedb.ParseUUID(input.ChatID)
	if actorErr != nil || chatErr != nil || input.Title == nil || strings.TrimSpace(*input.Title) == "" {
		return researchdomain.Session{}, researchdomain.ErrNotFound
	}
	var renamed sessionRecord
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		locked, err := queries.LockChatSessionForUpdate(ctx, sqlc.LockChatSessionForUpdateParams{
			ActorUserID: actorID,
			ChatID:      chatID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return researchdomain.ErrNotFound
		}
		if err != nil {
			return err
		}
		// A session of the helper of a drawing is not a chat: its operations do not change it (IW4.2).
		if locked.ChatSession.Scope != string(researchdomain.ScopeProject) || locked.ChatSession.BookID.Valid ||
			locked.ChatSession.Kind != agent.SessionResearch {
			return researchdomain.ErrNotFound
		}
		if locked.ActorRole != "owner" && locked.ActorRole != "admin" && locked.ActorRole != "editor" {
			return researchdomain.ErrForbidden
		}
		if locked.ChatSession.DeletedAt.Valid {
			return researchdomain.ErrConflict
		}
		updated, err := queries.UpdateChatSessionForActor(ctx, sqlc.UpdateChatSessionForActorParams{
			Title:          *input.Title,
			ChatID:         chatID,
			ProjectID:      locked.ChatSession.ProjectID,
			LifecycleEpoch: locked.ChatSession.LifecycleEpoch,
			ActorUserID:    actorID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return researchdomain.ErrConflict
		}
		if err != nil {
			return mapWriteError(err)
		}
		renamed = sessionFromSQL(updated)
		lockedSession := sessionFromSQL(locked.ChatSession)
		if renamed.ID != lockedSession.ID || renamed.ProjectID != lockedSession.ProjectID ||
			renamed.SourceView != lockedSession.SourceView || renamed.Scope != lockedSession.Scope ||
			renamed.BookID != lockedSession.BookID || renamed.LifecycleEpoch != lockedSession.LifecycleEpoch ||
			renamed.ThreadID != lockedSession.ThreadID {
			return researchdomain.ErrConflict
		}
		return nil
	})
	if err != nil {
		return researchdomain.Session{}, err
	}
	return sessionValue(renamed), nil
}
