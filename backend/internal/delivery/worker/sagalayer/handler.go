// Package sagalayer delivers the thread layer Job to the layer builder.
package sagalayer

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	service "canonkeeper/backend/internal/services/sagalayer"
)

var ErrInvalidJob = errors.New("saga layer worker: invalid layer job")

// LayerBuilder runs, cancels and fails the layer Job of a claim.
type LayerBuilder interface {
	RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject) (jobs.Status, error)
	CancelForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject) error
	FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject, failure jobs.Error) error
}

type Handler struct {
	builder LayerBuilder
}

func NewHandler(builder LayerBuilder) Handler {
	if builder == nil {
		panic("saga layer worker: builder is required")
	}
	return Handler{builder: builder}
}

func (Handler) Kind() jobs.Kind { return jobs.KindSagaLayer }

func (handler Handler) HandleResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := layerSubject(job, jobs.StatusRunning)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	status, err := handler.builder.RunForClaim(ctx, job.WorkerClaim, subject)
	if err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: status}, nil
}

func (handler Handler) HandleCanceledResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := layerSubject(job, jobs.StatusCanceling)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := handler.builder.CancelForClaim(ctx, job.WorkerClaim, subject); err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (handler Handler) HandleFailedResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := layerSubject(job, jobs.StatusRunning)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	failure := jobs.Error{Code: jobworker.HandlerFailedCode, Message: jobworker.HandlerFailedMessage}
	if job.Error != nil {
		failure = *job.Error
	}
	if err := handler.builder.FailForClaim(ctx, job.WorkerClaim, subject, failure); err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusFailed}, nil
}

func layerSubject(job jobs.Job, status jobs.Status) (jobs.SagaLayerSubject, error) {
	subject, ok := job.Subject.(jobs.SagaLayerSubject)
	if !ok || job.Kind != jobs.KindSagaLayer || job.Status != status || job.ID == "" ||
		job.WorkerClaim.Validate() != nil || job.WorkerClaim.ProjectID != job.ProjectID ||
		job.WorkerClaim.JobID != job.ID || subject.ProjectID != job.ProjectID {
		return jobs.SagaLayerSubject{}, ErrInvalidJob
	}
	return subject, nil
}

// handlerError keeps cancellation and invalid input as they are; everything else is retried by the
// worker, and the attempt goes on from the checkpoint.
func handlerError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, service.ErrInvalid) {
		return err
	}
	return jobworker.ErrRetryableHandler
}

var _ jobworker.Handler = Handler{}
