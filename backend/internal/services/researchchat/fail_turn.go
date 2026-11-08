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
	return useCase.repository.FailClaim(ctx, job)
}
