package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// The session of a helper of the site's workshops (the drawing helper, specs/2026-11-02-image-workshop.md IW4, and the
// chapter helper, specs/2026-11-02-writer-workshop.md W1) is a chat session of its kind, so a tool call of a helper
// goes through the chat's queue of tool calls, read authority and editor check. The helpers' stores live with the
// site (specs/2026-11-07-desktop-app.md DA16.4) and declare their own port over these methods.

// QueueToolCallInTx makes the transaction wait for the other tool calls of the turn's thread.
func (repository *Repository) QueueToolCallInTx(ctx context.Context, tx pgx.Tx, call service.ToolContext) error {
	return queueToolCallInTx(ctx, tx, call)
}

// ConfirmReadAuthorityInTx refuses a tool call whose authority changed since the tool resolved it.
func (repository *Repository) ConfirmReadAuthorityInTx(
	ctx context.Context, tx pgx.Tx, call service.ToolContext, authority researchdomain.ReadAuthority,
) error {
	return repository.confirmReadAuthorityInTx(ctx, tx, call, authority)
}

// MapSourceReadError maps an error of a tool transaction the way the chat's tools do.
func (repository *Repository) MapSourceReadError(err error) error {
	return mapSourceReadError(err)
}

// CanWriteInTx says the turn's author is an editor or above in the turn's project; false for a malformed authority,
// a former member and a reader.
func (repository *Repository) CanWriteInTx(ctx context.Context, tx pgx.Tx, authority researchdomain.ReadAuthority) (bool, error) {
	actor, err := storedb.ParseUUID(authority.ActorUserID)
	if err != nil {
		return false, nil
	}
	project, err := storedb.ParseUUID(authority.ProjectID)
	if err != nil {
		return false, nil
	}
	role, err := sqlc.New(tx).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ActorUserID: actor, ProjectID: project,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return projects.CanMutateProject(projects.Role(role)), nil
}

// SentTurnInTx reads what the author sent to start the turn the authority names.
func (repository *Repository) SentTurnInTx(ctx context.Context, tx pgx.Tx, authority researchdomain.ReadAuthority) (service.SentTurn, error) {
	var body []byte
	if err := tx.QueryRow(ctx, `
		SELECT input.body_json
		FROM chat_turns AS turn_row
		JOIN agent_workflow_inputs AS input ON input.project_id = turn_row.project_id AND input.id = turn_row.workflow_input_id
		WHERE turn_row.project_id = $1::uuid AND turn_row.id = $2::uuid AND turn_row.job_id = $3::uuid
	`, authority.ProjectID, authority.ChatTurnID, authority.JobID).Scan(&body); err != nil {
		return service.SentTurn{}, err
	}
	var input storedTurnInput
	err := json.Unmarshal(body, &input)
	return service.SentTurn{Question: input.Question, Button: input.Button}, err
}

// SessionQuestionsInTx reads the words of the questions of the turns of the authority's session, oldest first.
func (repository *Repository) SessionQuestionsInTx(ctx context.Context, tx pgx.Tx, authority researchdomain.ReadAuthority) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT input.body_json
		FROM chat_turns AS turn
		JOIN agent_workflow_inputs AS input ON input.id = turn.workflow_input_id AND input.project_id = turn.project_id
		WHERE turn.project_id = $1::uuid AND turn.chat_id = $2::uuid
		ORDER BY turn.created_at
	`, authority.ProjectID, authority.ChatID)
	if err != nil {
		return nil, err
	}
	bodies, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, err
	}
	var words []string
	for _, body := range bodies {
		var input storedTurnInput
		if json.Unmarshal(body, &input) == nil && input.Question != "" {
			words = append(words, input.Question)
		}
	}
	return words, nil
}

// CreateHelperSessionInTx opens the session of a helper: a chat session of its kind on the published text of the
// whole project, which the list of chats does not show.
func (repository *Repository) CreateHelperSessionInTx(ctx context.Context, tx pgx.Tx, projectID, actorUserID, title, kind string, at time.Time) (string, error) {
	var sessionID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&sessionID); err != nil {
		return "", err
	}
	ids, _, err := sessionInputIDs(sessionID, projectID, actorUserID, "")
	if err != nil {
		return "", researchdomain.ErrValidation
	}
	runes := []rune(title)
	if len(runes) > 180 {
		runes = runes[:180]
	}
	row, err := sqlc.New(tx).CreateChatSession(ctx, sqlc.CreateChatSessionParams{
		ID: ids[0], ProjectID: ids[1], ActorUserID: ids[2], SourceView: "published", Scope: string(researchdomain.ScopeProject),
		Title: string(runes), AgentGeneration: researchdomain.CurrentAgentGeneration, Kind: kind, CreatedAt: storedb.Timestamptz(at),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", researchdomain.ErrNotFound
	}
	if err != nil {
		return "", mapWriteError(err)
	}
	return storedb.UUIDString(row.ID), nil
}
