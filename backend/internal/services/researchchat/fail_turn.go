package researchchat

import (
	"context"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
)

type FailureRepository interface {
	FailClaim(context.Context, jobs.Job) error
}

type FailTurn struct {
	repository FailureRepository
	work       TurnWork
}

func NewFailTurn(repository FailureRepository) *FailTurn {
	if repository == nil {
		panic("research chat: failure repository is required")
	}
	return &FailTurn{repository: repository}
}

func (useCase *FailTurn) FailClaim(ctx context.Context, job jobs.Job) error {
	if ctx == nil || job.Kind != jobs.KindResearchChatTurn || job.Status != jobs.StatusRunning || job.Error == nil ||
		job.WorkerClaim.ProjectID != job.ProjectID || job.WorkerClaim.JobID != job.ID ||
		job.WorkerClaim.ClaimIncarnation < 1 {
		return researchdomain.ErrConflict
	}
	if err := useCase.repository.FailClaim(ctx, job); err != nil {
		return err
	}
	if useCase.work != nil {
		useCase.work.End(ctx, job.ID, researchdomain.TurnFailed)
	}
	return nil
}

// WithWork makes a turn the worker failed close the work a site counts once its failure is written
// (specs/2026-11-13-desk-account-ai.md AI2.7).
func (useCase *FailTurn) WithWork(work TurnWork) *FailTurn {
	useCase.work = work
	return useCase
}
