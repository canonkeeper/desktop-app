// Package authormaterials delivers the author_materials_import Job to the run that maps the sections of a file.
package authormaterials

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	service "canonkeeper/backend/internal/services/authormaterials"
)

// ErrInvalidJob reports a Job that is not an import under its claim.
var ErrInvalidJob = errors.New("author materials worker: invalid job")

// ImportRun runs, cancels and fails the Job of a claim.
type ImportRun interface {
	RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.AuthorMaterialsImportSubject) (jobs.Status, error)
	CancelForClaim(ctx context.Context, claim jobs.WorkerClaim) error
	FailForClaim(ctx context.Context, claim jobs.WorkerClaim, failure jobs.Error) (jobs.Status, error)
}

type Handler struct{ run ImportRun }

func NewHandler(run ImportRun) Handler {
	if run == nil {
		panic("author materials worker: run is required")
	}
	return Handler{run: run}
}

func (Handler) Kind() jobs.Kind { return jobs.KindAuthorMaterialsImport }

func (handler Handler) HandleResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	subject, err := importSubject(job, jobs.StatusRunning)
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	status, err := handler.run.RunForClaim(ctx, job.WorkerClaim, subject)
	if err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: status}, nil
}

func (handler Handler) HandleCanceledResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if _, err := importSubject(job, jobs.StatusCanceling); err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := handler.run.CancelForClaim(ctx, job.WorkerClaim); err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (handler Handler) HandleFailedResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if _, err := importSubject(job, jobs.StatusRunning); err != nil {
		return jobworker.HandlerResult{}, err
	}
	failure := jobs.Error{Code: jobworker.HandlerFailedCode, Message: jobworker.HandlerFailedMessage}
	if job.Error != nil {
		failure = *job.Error
	}
	status, err := handler.run.FailForClaim(ctx, job.WorkerClaim, failure)
	if err != nil {
		return jobworker.HandlerResult{}, handlerError(err)
	}
	return jobworker.HandlerResult{Status: status}, nil
}

func importSubject(job jobs.Job, status jobs.Status) (jobs.AuthorMaterialsImportSubject, error) {
	subject, ok := job.Subject.(jobs.AuthorMaterialsImportSubject)
	if !ok || job.Kind != jobs.KindAuthorMaterialsImport || job.Status != status || job.ID == "" ||
		job.WorkerClaim.Validate() != nil || job.WorkerClaim.ProjectID != job.ProjectID ||
		job.WorkerClaim.JobID != job.ID || subject.ProjectID != job.ProjectID {
		return jobs.AuthorMaterialsImportSubject{}, ErrInvalidJob
	}
	return subject, nil
}

// handlerError keeps cancellation and an invalid Job as they are; everything else is retried by the worker.
func handlerError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, service.ErrInvalidJob) {
		return err
	}
	return jobworker.ErrRetryableHandler
}

var _ jobworker.Handler = Handler{}
