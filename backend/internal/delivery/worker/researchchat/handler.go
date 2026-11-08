package researchchat

import (
	"context"
	"errors"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
)

var ErrInvalidJob = errors.New("research chat worker: invalid job")

type TurnRunner interface {
	Execute(context.Context, researchdomain.WorkerClaim) (researchdomain.TurnResult, error)
}

type TurnCanceler interface {
	CancelClaim(context.Context, jobs.Job) error
}

type TurnFailer interface {
	FailClaim(context.Context, jobs.Job) error
}

type Handler struct {
	runner   TurnRunner
	canceler TurnCanceler
	failer   TurnFailer
}

func NewHandler(runner TurnRunner, canceler TurnCanceler, failer TurnFailer) Handler {
	if runner == nil || canceler == nil || failer == nil {
		panic("research chat worker: dependencies are required")
	}
	return Handler{runner: runner, canceler: canceler, failer: failer}
}

func (Handler) Kind() jobs.Kind { return jobs.KindResearchChatTurn }

func (handler Handler) HandleResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if !validJob(job, jobs.StatusRunning) {
		return jobworker.HandlerResult{}, ErrInvalidJob
	}
	_, err := handler.runner.Execute(ctx, researchdomain.WorkerClaim{
		ProjectID: job.ProjectID, JobID: job.ID, ClaimIncarnation: job.WorkerClaim.ClaimIncarnation,
	})
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	return jobworker.HandlerResult{Status: jobs.StatusSucceeded}, nil
}

func (handler Handler) HandleCanceledResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if !validJob(job, jobs.StatusCanceling) {
		return jobworker.HandlerResult{}, ErrInvalidJob
	}
	if err := handler.canceler.CancelClaim(ctx, job); err != nil {
		return jobworker.HandlerResult{}, err
	}
	return jobworker.HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (handler Handler) HandleFailedResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if !validJob(job, jobs.StatusRunning) {
		return jobworker.HandlerResult{}, ErrInvalidJob
	}
	if err := handler.failer.FailClaim(ctx, job); err != nil {
		return jobworker.HandlerResult{}, err
	}
	return jobworker.HandlerResult{Status: jobs.StatusFailed}, nil
}

func validJob(job jobs.Job, status jobs.Status) bool {
	return job.Kind == jobs.KindResearchChatTurn && job.Status == status && job.ID != "" && job.ProjectID != "" &&
		job.WorkerClaim.ProjectID == job.ProjectID && job.WorkerClaim.JobID == job.ID && job.WorkerClaim.ClaimIncarnation > 0
}
