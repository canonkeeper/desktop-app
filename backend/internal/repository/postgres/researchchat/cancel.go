package researchchat

import (
	"context"
	"errors"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) CanReadJobTarget(ctx context.Context, actor jobs.Actor, subject jobs.Subject) (bool, error) {
	chatSubject, ok := subject.(jobs.ResearchChatTurnSubject)
	if !ok || actor.UserID == "" {
		return false, nil
	}
	stored, err := repository.getTurn(ctx, actor.UserID, chatSubject.TurnID)
	if errors.Is(err, researchdomain.ErrNotFound) || errors.Is(err, researchdomain.ErrForbidden) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	decoded, err := jobs.DecodeSubject(jobs.KindResearchChatTurn, stored.JobSubjectJSON)
	storedSubject, decodedOK := decoded.(jobs.ResearchChatTurnSubject)
	return err == nil && decodedOK && sameSubjects(storedSubject, chatSubject) &&
		stored.ProjectID == chatSubject.ProjectID && stored.ChatID == chatSubject.ChatID &&
		stored.SourceView == string(chatSubject.SourceView), nil
}

func (repository *Repository) BeginTargetCancel(
	ctx context.Context,
	input jobs.TargetCancelInput,
) (researchdomain.CancelTarget, jobs.Job, error) {
	subject, ok := input.Job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok {
		return researchdomain.CancelTarget{}, jobs.Job{}, researchdomain.ErrNotFound
	}
	if input.Job.Status == jobs.StatusQueued && input.Job.RetryCount > 0 {
		target, found, err := repository.beginRequeuedCancel(ctx, input, subject)
		if err != nil {
			return researchdomain.CancelTarget{}, jobs.Job{}, err
		}
		if found {
			updated := input.Job
			updated.Status = jobs.StatusCanceling
			updated.WorkerClaim = jobs.WorkerClaim{
				ProjectID: input.Job.ProjectID, JobID: input.Job.ID, ClaimIncarnation: target.ClaimIncarnation,
			}
			return target, updated, nil
		}
	}
	updated, err := repository.cancelTurn(ctx, input)
	if err != nil {
		return researchdomain.CancelTarget{}, jobs.Job{}, mapError(err)
	}
	rootThreadID, flow, err := repository.rootThread(ctx, input.Job.ProjectID, subject.ChatID, input.ActorUserID)
	if err != nil {
		return researchdomain.CancelTarget{}, jobs.Job{}, mapError(err)
	}
	return researchdomain.CancelTarget{
		Flow: flow, ProjectID: input.Job.ProjectID, JobID: input.Job.ID, ChatID: subject.ChatID, RootThreadID: rootThreadID,
		ClaimIncarnation: updated.WorkerClaim.ClaimIncarnation,
		NeedsInterrupt:   updated.Status == jobs.StatusCanceling,
		ActorUserID:      input.ActorUserID, TargetReadable: input.TargetReadable,
	}, updated, nil
}

func (repository *Repository) beginRequeuedCancel(
	ctx context.Context,
	input jobs.TargetCancelInput,
	subject jobs.ResearchChatTurnSubject,
) (researchdomain.CancelTarget, bool, error) {
	var target researchdomain.CancelTarget
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var claimIncarnation int64
		var rootThreadID string
		err := tx.QueryRow(ctx, `
			SELECT job.claim_incarnation, COALESCE(chat.thread_id, '')
			FROM jobs AS job
			JOIN chat_turns AS turn_row ON turn_row.job_id=job.id AND turn_row.project_id=job.project_id
			JOIN chat_sessions AS chat ON chat.id=turn_row.chat_id AND chat.project_id=turn_row.project_id
			JOIN project_memberships AS membership
			  ON membership.project_id=job.project_id AND membership.user_id=$6::uuid AND membership.removed_at IS NULL
			WHERE job.project_id=$1::uuid AND job.id=$2::uuid AND job.kind='research_chat_turn'
			  AND job.status='queued' AND job.retry_count=$3::integer AND job.cancel_requested_at IS NULL
			  AND job.creator_user_id=$4::uuid AND turn_row.id=$5::uuid AND chat.id=$7::uuid
			  AND chat.deleted_at IS NULL
			  AND (job.creator_user_id=$6::uuid OR membership.role IN ('owner','admin'))
			FOR UPDATE OF job, turn_row, chat
		`, input.Job.ProjectID, input.Job.ID, input.Job.RetryCount, input.Job.CreatorUserID,
			subject.TurnID, input.ActorUserID, subject.ChatID).Scan(&claimIncarnation, &rootThreadID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if claimIncarnation < 1 {
			return researchdomain.ErrConflict
		}
		command, err := tx.Exec(ctx, `
			UPDATE jobs
			SET status='canceling', progress_json='{}'::jsonb, cancel_requested_at=$1, updated_at=$1
			WHERE project_id=$2::uuid AND id=$3::uuid AND status='queued'
			  AND claim_incarnation=$4::bigint AND retry_count=$5::integer
		`, input.UpdatedAt, input.Job.ProjectID, input.Job.ID, claimIncarnation, input.Job.RetryCount)
		if err != nil || command.RowsAffected() != 1 {
			return errors.Join(researchdomain.ErrConflict, err)
		}
		target = researchdomain.CancelTarget{
			ProjectID: input.Job.ProjectID, JobID: input.Job.ID, ChatID: subject.ChatID,
			RootThreadID: rootThreadID, ClaimIncarnation: claimIncarnation, NeedsInterrupt: true,
			ActorUserID: input.ActorUserID, TargetReadable: input.TargetReadable,
		}
		return nil
	})
	return target, target.JobID != "", mapError(err)
}

func (repository *Repository) CancelTargetForJob(ctx context.Context, job jobs.Job) (researchdomain.CancelTarget, error) {
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok || job.Kind != jobs.KindResearchChatTurn || job.Status != jobs.StatusCanceling ||
		job.WorkerClaim.ProjectID != job.ProjectID || job.WorkerClaim.JobID != job.ID ||
		job.WorkerClaim.ClaimIncarnation < 1 || job.CreatorUserID == "" {
		return researchdomain.CancelTarget{}, researchdomain.ErrConflict
	}
	rootThreadID, flow, err := repository.rootThread(ctx, job.ProjectID, subject.ChatID, job.CreatorUserID)
	if err != nil {
		return researchdomain.CancelTarget{}, mapError(err)
	}
	return researchdomain.CancelTarget{
		Flow: flow, ProjectID: job.ProjectID, JobID: job.ID, ChatID: subject.ChatID, RootThreadID: rootThreadID,
		ClaimIncarnation: job.WorkerClaim.ClaimIncarnation, NeedsInterrupt: true,
		ActorUserID: job.CreatorUserID, TargetReadable: true,
	}, nil
}

func (repository *Repository) CompleteCancel(ctx context.Context, target researchdomain.CancelTarget) (jobs.Job, error) {
	if !target.NeedsInterrupt {
		return jobs.Job{}, nil
	}
	if target.ClaimIncarnation < 1 {
		return jobs.Job{}, researchdomain.ErrConflict
	}
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := repository.jobs.TerminalizeUserModelJobInTx(ctx, tx, jobstore.TerminalizeUserModelJobInput{
			Kind: jobs.KindResearchChatTurn, ProjectID: target.ProjectID, JobID: target.JobID,
			ClaimIncarnation: target.ClaimIncarnation, From: jobs.StatusCanceling, To: jobs.StatusCanceled,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return jobs.Job{}, mapError(err)
	}
	if !target.TargetReadable {
		return jobs.Job{}, nil
	}
	job, err := repository.jobs.GetJob(ctx, jobs.GetJobStoreInput{
		ActorUserID: target.ActorUserID, ProjectID: target.ProjectID, JobID: target.JobID,
	})
	return job, mapError(err)
}

func sameSubjects(left, right jobs.ResearchChatTurnSubject) bool {
	leftBookID, rightBookID := "", ""
	if left.BookID != nil {
		leftBookID = *left.BookID
	}
	if right.BookID != nil {
		rightBookID = *right.BookID
	}
	return left.Type == right.Type && left.ProjectID == right.ProjectID && left.ChatID == right.ChatID &&
		left.TurnID == right.TurnID && left.SourceView == right.SourceView && left.Scope == right.Scope &&
		leftBookID == rightBookID
}

type turnCommand struct {
	ID             string
	ChatID         string
	ProjectID      string
	SourceView     string
	JobID          string
	JobSubjectJSON []byte
}

func (repository *Repository) getTurn(ctx context.Context, actorUserID, turnID string) (turnCommand, error) {
	actor, actorErr := storedb.ParseUUID(actorUserID)
	turn, turnErr := storedb.ParseUUID(turnID)
	if actorErr != nil || turnErr != nil {
		return turnCommand{}, researchdomain.ErrValidation
	}
	row, err := sqlc.New(repository.db.Pool).GetReadableChatTurn(ctx, sqlc.GetReadableChatTurnParams{
		ActorUserID: actor, TurnID: turn,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return turnCommand{}, researchdomain.ErrNotFound
	}
	if err != nil {
		return turnCommand{}, err
	}
	return turnCommand{
		ID: storedb.UUIDString(row.ID), ChatID: storedb.UUIDString(row.ChatID), ProjectID: storedb.UUIDString(row.ProjectID),
		SourceView: row.SourceView, JobID: storedb.UUIDString(row.JobID), JobSubjectJSON: append([]byte(nil), row.JobSubjectJson...),
	}, nil
}

func (repository *Repository) cancelTurn(ctx context.Context, input jobs.TargetCancelInput) (jobs.Job, error) {
	subject, ok := input.Job.Subject.(jobs.ResearchChatTurnSubject)
	projectID, projectErr := storedb.ParseUUID(input.Job.ProjectID)
	jobID, jobErr := storedb.ParseUUID(input.Job.ID)
	actorID, actorErr := storedb.ParseUUID(input.ActorUserID)
	turnID, turnErr := storedb.ParseUUID(subject.TurnID)
	if !ok || projectErr != nil || jobErr != nil || actorErr != nil || turnErr != nil ||
		input.Job.Kind != jobs.KindResearchChatTurn || subject.Type != jobs.KindResearchChatTurn ||
		subject.ProjectID != input.Job.ProjectID || input.UpdatedAt.IsZero() || !input.Job.CanCancel ||
		(input.Job.Status != jobs.StatusQueued && input.Job.Status != jobs.StatusRunning) ||
		(!input.TargetReadable && (input.Job.Status != jobs.StatusRunning || input.Job.CreatorUserID != input.ActorUserID)) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	var result jobs.Job
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var session writableSession
		if input.TargetReadable {
			locked, err := repository.lockWritableSessionInTx(ctx, tx, input.Job.ProjectID, subject.ChatID, input.ActorUserID)
			if err != nil {
				return cancelTargetError(err)
			}
			session = locked
			sameScope := string(locked.Scope) == subject.Scope && locked.BookID == optionalSubjectBookID(subject.BookID)
			// Saga promotion preserves the immutable target of an already accepted Job.
			promotedBookChat := locked.Scope == researchdomain.ScopeProject && locked.BookID == "" &&
				subject.Scope == string(researchdomain.ScopeBook) && subject.BookID != nil
			if locked.SourceView != subject.SourceView || (!sameScope && !promotedBookChat) {
				return jobs.ErrNotFound
			}
		}

		queries := sqlc.New(tx)
		turn, err := queries.LockChatTurnForSubmit(ctx, sqlc.LockChatTurnForSubmitParams{
			TurnID: turnID, ProjectID: projectID, JobID: jobID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.ErrNotFound
		}
		if err != nil {
			return err
		}
		if storedb.UUIDString(turn.ChatID) != subject.ChatID || turn.JobKind != string(jobs.KindResearchChatTurn) ||
			turn.SourceView != string(subject.SourceView) {
			return jobs.ErrNotFound
		}

		preview, err := queries.PreviewJobForCancel(ctx, sqlc.PreviewJobForCancelParams{ProjectID: projectID, JobID: jobID})
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.ErrNotFound
		}
		if err != nil {
			return err
		}
		stored, decodeErr := jobs.DecodeSubject(jobs.Kind(preview.Kind), preview.SubjectJson)
		storedSubject, storedOK := stored.(jobs.ResearchChatTurnSubject)
		if decodeErr != nil || !storedOK || !sameSubjects(storedSubject, subject) ||
			storedb.UUIDString(preview.CreatorUserID) != input.Job.CreatorUserID {
			return jobs.ErrNotFound
		}
		if input.TargetReadable {
			if input.Job.CreatorUserID != input.ActorUserID && !projects.CanManageMembers(projects.Role(session.ActorRole)) {
				return jobs.ErrForbidden
			}
		} else if input.Job.CreatorUserID != input.ActorUserID {
			return jobs.ErrNotFound
		}

		switch input.Job.Status {
		case jobs.StatusQueued:
			if err := repository.jobs.TerminalizeUserModelJobInTx(ctx, tx, jobstore.TerminalizeUserModelJobInput{
				Kind: jobs.KindResearchChatTurn, ProjectID: input.Job.ProjectID, JobID: input.Job.ID,
				From: jobs.StatusQueued, To: jobs.StatusCanceled,
			}); err != nil {
				return cancelTargetError(err)
			}
			row, err := queries.LockJobForCancel(ctx, sqlc.LockJobForCancelParams{ProjectID: projectID, JobID: jobID})
			if err != nil {
				return err
			}
			result, err = jobstore.JobFromSQLJob(row)
			return err
		case jobs.StatusRunning:
			row, err := queries.CancelJobStatus(ctx, sqlc.CancelJobStatusParams{
				ToStatus: string(jobs.StatusCanceling), Progress: 0, UpdatedAt: storedb.Timestamptz(input.UpdatedAt),
				ProjectID: projectID, JobID: jobID, FromStatus: string(jobs.StatusRunning), ActorUserID: actorID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return jobs.ErrConflict
			}
			if err != nil {
				return err
			}
			result, err = jobstore.JobFromSQLJob(row)
			return err
		default:
			return jobs.ErrConflict
		}
	})
	return result, err
}

func optionalSubjectBookID(bookID *string) string {
	if bookID == nil {
		return ""
	}
	return *bookID
}

func cancelTargetError(err error) error {
	switch {
	case errors.Is(err, researchdomain.ErrNotFound):
		return jobs.ErrNotFound
	case errors.Is(err, researchdomain.ErrForbidden):
		return jobs.ErrForbidden
	case errors.Is(err, researchdomain.ErrValidation), errors.Is(err, researchdomain.ErrConflict),
		errors.Is(err, jobs.ErrValidation), errors.Is(err, jobs.ErrConflict):
		return jobs.ErrConflict
	default:
		return err
	}
}
