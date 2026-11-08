// Package sagamemory delivers the chapter cards Job to the saga memory card builder.
package sagamemory

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	service "canonkeeper/backend/internal/services/sagamemory"
)

var ErrInvalidJob = errors.New("saga memory worker: invalid chapter cards job")

// CardBuilder runs, cancels and fails the cards Job of a claim.
type CardBuilder interface {
	RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, lastAttempt bool) (jobs.Status, error)
	CancelForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject) error
	FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, failure jobs.Error) error
}

type Handler struct {
	builder CardBuilder
}

func NewHandler(builder CardBuilder) Handler {
	if builder == nil {
		panic("saga memory worker: card builder is required")
	}
	return Handler{builder: builder}
}

func (Handler) Kind() jobs.Kind { return jobs.KindChapterCards }

func (handler Handler) HandleResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := cardsSubject(job, jobs.StatusRunning)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	status, err := handler.builder.RunForClaim(ctx, job.WorkerClaim, subject, job.RetryCount >= jobs.MaxJobAttempts-1)
	if err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	if status == jobs.StatusQueued {
		// The builder gave the Job back to the queue after a portion (quota spec R12.2).
		return jobworker.HandlerResult{Status: status, RequeueCause: service.ErrCardsPortion}, nil
	}
	return jobworker.HandlerResult{Status: status}, nil
}

func (handler Handler) HandleCanceledResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := cardsSubject(job, jobs.StatusCanceling)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := handler.builder.CancelForClaim(ctx, job.WorkerClaim, subject); err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (handler Handler) HandleFailedResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := cardsSubject(job, jobs.StatusRunning)
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

func cardsSubject(job jobs.Job, status jobs.Status) (jobs.ChapterCardsSubject, error) {
	subject, ok := job.Subject.(jobs.ChapterCardsSubject)
	if !ok || job.Kind != jobs.KindChapterCards || job.Status != status || job.ID == "" ||
		job.WorkerClaim.Validate() != nil || job.WorkerClaim.ProjectID != job.ProjectID ||
		job.WorkerClaim.JobID != job.ID || subject.ProjectID != job.ProjectID {
		return jobs.ChapterCardsSubject{}, ErrInvalidJob
	}
	return subject, nil
}

// handlerError keeps cancellation and invalid input as they are; chapters left without cards
// and storage failures are retried by the worker.
func handlerError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, service.ErrInvalid) {
		return err
	}
	return jobworker.ErrRetryableHandler
}

var _ jobworker.Handler = Handler{}
