package researchchat

import (
	"context"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
)

type RootSelector struct {
	// Flow is the app-server of the chat session's kind.
	Flow          agent.Flow
	CorrelationID string
	ThreadID      string
}

type CancellationRepository interface {
	BeginTargetCancel(context.Context, jobs.TargetCancelInput) (researchdomain.CancelTarget, jobs.Job, error)
	CancelTargetForJob(context.Context, jobs.Job) (researchdomain.CancelTarget, error)
	CompleteCancel(context.Context, researchdomain.CancelTarget) (jobs.Job, error)
	CanReadJobTarget(context.Context, jobs.Actor, jobs.Subject) (bool, error)
}

type RootInterrupt func(context.Context, RootSelector) error

type CancelTurn struct {
	repository CancellationRepository
	interrupt  RootInterrupt
	work       TurnWork
}

// WithWork makes a stopped turn close the work a site counts once its cancel is written
// (specs/2026-11-13-desk-account-ai.md AI2.7).
func (useCase *CancelTurn) WithWork(work TurnWork) *CancelTurn {
	useCase.work = work
	return useCase
}

// ended closes the work of a turn whose Job the cancel ended.
func (useCase *CancelTurn) ended(ctx context.Context, job jobs.Job) {
	if useCase.work != nil && job.Status == jobs.StatusCanceled {
		useCase.work.End(ctx, job.ID, researchdomain.TurnCanceled)
	}
}

func NewCancelTurn(repository CancellationRepository, interrupt RootInterrupt) *CancelTurn {
	if repository == nil || interrupt == nil {
		panic("research chat: cancel dependencies are required")
	}
	return &CancelTurn{repository: repository, interrupt: interrupt}
}

func (useCase *CancelTurn) CanReadJobTarget(ctx context.Context, actor jobs.Actor, subject jobs.Subject) (bool, error) {
	return useCase.repository.CanReadJobTarget(ctx, actor, subject)
}

func (useCase *CancelTurn) CancelJobTarget(ctx context.Context, input jobs.TargetCancelInput) (jobs.CancelResult, error) {
	target, updated, err := useCase.repository.BeginTargetCancel(ctx, input)
	if err != nil {
		return jobs.CancelResult{}, err
	}
	if target.JobID != input.Job.ID || target.ProjectID != input.Job.ProjectID || target.ChatID == "" {
		return jobs.CancelResult{}, researchdomain.ErrConflict
	}
	if target.NeedsInterrupt {
		if err := useCase.interrupt(ctx, RootSelector{Flow: target.Flow, CorrelationID: target.ChatID, ThreadID: target.RootThreadID}); err != nil {
			return jobs.CancelResult{}, err
		}
		updated, err = useCase.repository.CompleteCancel(ctx, target)
		if err != nil {
			return jobs.CancelResult{}, err
		}
	}
	useCase.ended(ctx, updated)
	if !input.TargetReadable {
		return jobs.CancelResult{Opaque: true}, nil
	}
	return jobs.CancelResult{Job: updated}, nil
}

func (useCase *CancelTurn) CancelClaim(ctx context.Context, job jobs.Job) error {
	target, err := useCase.repository.CancelTargetForJob(ctx, job)
	if err != nil {
		return err
	}
	if err := useCase.interrupt(ctx, RootSelector{Flow: target.Flow, CorrelationID: target.ChatID, ThreadID: target.RootThreadID}); err != nil {
		return err
	}
	canceled, err := useCase.repository.CompleteCancel(ctx, target)
	if err == nil {
		useCase.ended(ctx, canceled)
	}
	return err
}

var (
	_ jobs.TargetAccessResolver = (*CancelTurn)(nil)
	_ jobs.TargetCancelResolver = (*CancelTurn)(nil)
)
