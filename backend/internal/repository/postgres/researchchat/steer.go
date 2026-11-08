package researchchat

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	service "canonkeeper/backend/internal/services/researchchat"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// The status of a correction's key in idempotency_records (specs/2026-10-30-chat-steering.md R1.5,
// R1.6): being sent, accepted by the native turn, or let go after a refusal, so the same key may try
// again. The runtime has no DELETE on the table, and it needs none.
const (
	steerKeySending  int32 = 102
	steerKeyAccepted int32 = 202
	steerKeyReleased int32 = 0
)

// BeginSteer checks in one transaction the session, the right, the turn and the images, and takes the
// key. A repeat of an accepted key is answered before the state of the turn: it may have ended since.
func (repository *Repository) BeginSteer(ctx context.Context, command service.BeginSteerCommand) (service.SteerTarget, error) {
	project, projectErr := storedb.ParseUUID(command.Session.ProjectID)
	actor, actorErr := storedb.ParseUUID(command.ActorUserID)
	if projectErr != nil || actorErr != nil {
		return service.SteerTarget{}, researchdomain.ErrNotFound
	}
	var target service.SteerTarget
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockVerifiedSessionWriteProof(ctx, tx, actor, command.SessionWriteProof); err != nil {
			return err
		}
		if _, err := jobstore.LockMutationMembership(ctx, tx, project, actor); err != nil {
			return err
		}
		if _, err := repository.lockWritableSessionInTx(ctx, tx, command.Session.ProjectID, command.ChatID, command.ActorUserID); err != nil {
			return err
		}
		loaded, status, err := repository.steerTurnInTx(ctx, tx, command.Session.ProjectID, command.ChatID, command.TurnID)
		if err != nil {
			return err
		}
		target = loaded
		queries := sqlc.New(tx)
		record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.IdempotencyKeyHash,
			RequestHash: command.RequestHash, StatusCode: steerKeySending, ResponseJson: []byte("{}"),
			CreatedAt: storedb.Timestamptz(command.CreatedAt),
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			record, err = queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.IdempotencyKeyHash,
			})
			if err != nil {
				return err
			}
			if record.RequestHash != command.RequestHash {
				return researchdomain.ErrConflict
			}
			switch record.StatusCode {
			case steerKeyAccepted:
				target.Replayed = true
				return nil
			case steerKeyReleased:
				if _, err := queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{
					ID: record.ID, StatusCode: steerKeySending, ResponseJson: []byte("{}"),
				}); err != nil {
					return err
				}
			default:
				return researchdomain.ErrSteerRetry
			}
		case err != nil:
			return err
		}
		target.RecordID = storedb.UUIDString(record.ID)
		if !steerableJob(status) {
			return researchdomain.ErrTurnEnded
		}
		var accepted int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM idempotency_records
			WHERE scope_hash = $1 AND operation_id = $2 AND status_code <> $3 AND id <> $4
		`, command.ScopeHash, command.OperationID, steerKeyReleased, record.ID).Scan(&accepted); err != nil {
			return err
		}
		if accepted >= researchdomain.MaxSteersPerTurn {
			return researchdomain.ErrSteerLimit
		}
		if err := readyImagesInTx(ctx, tx, command.Session.ProjectID, command.Images); err != nil {
			return err
		}
		target.ImageTitles, err = imageTitlesOf(ctx, tx, command.Session.ProjectID, command.Images)
		return err
	})
	if err != nil {
		return service.SteerTarget{}, mapError(err)
	}
	return target, nil
}

// ReloadSteerTarget reads the turn of a correction that waits (R2.2).
func (repository *Repository) ReloadSteerTarget(ctx context.Context, target service.SteerTarget) (service.SteerTarget, error) {
	var status steerJobState
	var reloaded service.SteerTarget
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		reloaded, status, err = repository.steerTurnInTx(ctx, tx, target.ProjectID, target.ChatID, target.TurnID)
		return err
	})
	if err != nil {
		return service.SteerTarget{}, mapError(err)
	}
	if !steerableJob(status) {
		return service.SteerTarget{}, researchdomain.ErrTurnEnded
	}
	reloaded.RecordID, reloaded.ImageTitles = target.RecordID, target.ImageTitles
	return reloaded, nil
}

// FinishSteer keeps the key of an accepted correction and lets go of any other (R1.6).
func (repository *Repository) FinishSteer(ctx context.Context, target service.SteerTarget, accepted bool) error {
	record, err := storedb.ParseUUID(target.RecordID)
	if err != nil {
		return researchdomain.ErrConflict
	}
	status := steerKeyReleased
	if accepted {
		status = steerKeyAccepted
	}
	_, err = sqlc.New(repository.db.Pool).UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{
		ID: record, StatusCode: status, ResponseJson: []byte("{}"),
	})
	return mapError(err)
}

type steerJobState struct {
	status          string
	cancelRequested bool
}

// steerableJob: a queued turn has not started yet and may still take the correction after a retry;
// a turn being stopped takes none.
func steerableJob(state steerJobState) bool {
	return !state.cancelRequested && (state.status == "queued" || state.status == "running")
}

// steerTurnInTx reads the turn of the chat with its Job and native identity; a turn of another chat is
// not found.
func (repository *Repository) steerTurnInTx(ctx context.Context, tx pgx.Tx, projectID, chatID, turnID string) (service.SteerTarget, steerJobState, error) {
	project, projectErr := storedb.ParseUUID(projectID)
	chat, chatErr := storedb.ParseUUID(chatID)
	turn, turnErr := storedb.ParseUUID(turnID)
	if projectErr != nil || chatErr != nil || turnErr != nil {
		return service.SteerTarget{}, steerJobState{}, researchdomain.ErrNotFound
	}
	target := service.SteerTarget{ProjectID: projectID, ChatID: chatID, TurnID: turnID}
	var state steerJobState
	var root, provider *string
	var kind string
	err := tx.QueryRow(ctx, `
		SELECT job.id::text, job.status, job.cancel_requested_at IS NOT NULL, chat.thread_id, turn_row.provider_turn_id, chat.kind
		FROM chat_turns AS turn_row
		JOIN chat_sessions AS chat ON chat.id = turn_row.chat_id AND chat.project_id = turn_row.project_id
		JOIN jobs AS job ON job.id = turn_row.job_id AND job.project_id = turn_row.project_id
		WHERE turn_row.project_id = $1 AND turn_row.chat_id = $2 AND turn_row.id = $3
		  AND job.kind = 'research_chat_turn' AND chat.deleted_at IS NULL
	`, project, chat, turn).Scan(&target.JobID, &state.status, &state.cancelRequested, &root, &provider, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.SteerTarget{}, steerJobState{}, researchdomain.ErrNotFound
	}
	if err != nil {
		return service.SteerTarget{}, steerJobState{}, err
	}
	if root != nil && provider != nil {
		target.RootThreadID, target.ProviderTurnID = *root, *provider
	}
	flow, known := agent.SessionFlow(kind)
	if !known {
		return service.SteerTarget{}, steerJobState{}, researchdomain.ErrNotFound
	}
	target.Flow = flow
	return target, state, nil
}

// imageTitlesOf reads the captions of the images of a correction, for its anchors (R15.19).
func imageTitlesOf(ctx context.Context, tx pgx.Tx, projectID string, numbers []int) (map[int]string, error) {
	if len(numbers) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT number, COALESCE(title, '') FROM saga_images WHERE project_id = $1::uuid AND number = ANY($2::integer[])
	`, projectID, numbers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	titles := make(map[int]string, len(numbers))
	for rows.Next() {
		var number int
		var title string
		if err := rows.Scan(&number, &title); err != nil {
			return nil, err
		}
		titles[number] = title
	}
	return titles, rows.Err()
}
