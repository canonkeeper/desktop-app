package indexing

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
)

var ErrInvalidTextIndexJob = errors.New("indexing: invalid canonical text index job metadata")

type TextIndexService interface {
	RunForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error
	CancelForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error
	FailForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject, jobs.Error) error
}

type TextIndexHandler struct {
	service TextIndexService
}

func NewTextIndexHandler(service TextIndexService) TextIndexHandler {
	if service == nil {
		panic("indexing: TextIndexService is required")
	}
	return TextIndexHandler{service: service}
}

func (TextIndexHandler) Kind() jobs.Kind { return jobs.KindCanonTextIndex }

func (h TextIndexHandler) HandleResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if err := ctx.Err(); err != nil {
		return jobworker.HandlerResult{}, err
	}
	subject, err := canonTextIndexSubjectFromJob(job)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := h.service.RunForClaim(ctx, job.WorkerClaim, subject); err != nil {
		return jobworker.HandlerResult{}, canonTextIndexHandlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusSucceeded}, nil
}

func (h TextIndexHandler) HandleCanceledResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if err := ctx.Err(); err != nil {
		return jobworker.HandlerResult{}, err
	}
	subject, err := canonTextIndexSubjectFromJob(job)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := h.service.CancelForClaim(ctx, job.WorkerClaim, subject); err != nil {
		return jobworker.HandlerResult{}, canonTextIndexHandlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (h TextIndexHandler) HandleFailedResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if err := ctx.Err(); err != nil {
		return jobworker.HandlerResult{}, err
	}
	subject, err := canonTextIndexSubjectFromJob(job)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	failure := job.Error
	if failure == nil {
		failure = &jobs.Error{Code: jobworker.HandlerFailedCode, Message: jobworker.HandlerFailedMessage}
	}
	if err := h.service.FailForClaim(ctx, job.WorkerClaim, subject, *failure); err != nil {
		return jobworker.HandlerResult{}, canonTextIndexHandlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusFailed}, nil
}

func canonTextIndexSubjectFromJob(job jobs.Job) (jobs.CanonTextIndexSubject, error) {
	subject, ok := job.Subject.(jobs.CanonTextIndexSubject)
	if !ok || job.ID == "" || job.ProjectID == "" || job.Kind != jobs.KindCanonTextIndex ||
		job.WorkerClaim.Validate() != nil || job.WorkerClaim.ProjectID != job.ProjectID ||
		job.WorkerClaim.JobID != job.ID || subject.ProjectID != job.ProjectID {
		return jobs.CanonTextIndexSubject{}, ErrInvalidTextIndexJob
	}
	if _, err := jobs.EncodeSubject(jobs.KindCanonTextIndex, subject); err != nil {
		return jobs.CanonTextIndexSubject{}, ErrInvalidTextIndexJob
	}
	return subject, nil
}

type nonRetryableCanonTextIndexError interface {
	error
	NonRetryableCanonTextIndexError()
}

func canonTextIndexHandlerError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var nonRetryable nonRetryableCanonTextIndexError
	if errors.As(err, &nonRetryable) {
		return err
	}
	return jobworker.ErrRetryableHandler
}

var _ jobworker.Handler = TextIndexHandler{}
