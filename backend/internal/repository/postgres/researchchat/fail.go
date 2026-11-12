package researchchat

import (
	"context"
	"errors"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) FailClaim(ctx context.Context, job jobs.Job) error {
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok || job.Error == nil {
		return researchdomain.ErrConflict
	}
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var rootThreadID *string
		var spent turnSpend
		if err := tx.QueryRow(ctx, `
			SELECT chat.thread_id, job.creator_user_id::text, job.created_at, chat.kind
			FROM jobs AS job
			JOIN chat_turns AS turn_row ON turn_row.job_id=job.id AND turn_row.project_id=job.project_id
			JOIN chat_sessions AS chat ON chat.id=turn_row.chat_id AND chat.project_id=turn_row.project_id
			WHERE job.project_id=$1::uuid AND job.id=$2::uuid AND job.kind='research_chat_turn'
			  AND job.status='running' AND job.claim_incarnation=$3::bigint
			  AND job.governance_payload_state='active' AND turn_row.id=$4::uuid
			  AND chat.id=$5::uuid AND chat.deleted_at IS NULL
			FOR UPDATE OF job, turn_row, chat
		`, job.ProjectID, job.ID, job.WorkerClaim.ClaimIncarnation, subject.TurnID, subject.ChatID).
			Scan(&rootThreadID, &spent.userID, &spent.at, &spent.kind); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return researchdomain.ErrConflict
			}
			return err
		}
		if err := repository.jobs.TerminalizeUserModelJobInTx(ctx, tx, jobstore.TerminalizeUserModelJobInput{
			Kind: jobs.KindResearchChatTurn, ProjectID: job.ProjectID, JobID: job.ID,
			ClaimIncarnation: job.WorkerClaim.ClaimIncarnation, From: jobs.StatusRunning, To: jobs.StatusFailed,
			Error: job.Error,
		}); err != nil {
			return err
		}
		return repository.giveBackTurnInTx(ctx, tx, spent)
	})
	return mapError(err)
}
