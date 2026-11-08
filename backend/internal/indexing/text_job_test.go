package indexing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
)

const (
	canonTextWorkerProjectID = "20000000-0000-4000-8000-000000000001"
	canonTextWorkerJobID     = "20000000-0000-4000-8000-000000000002"
)

func TestCanonTextIndexHandlerCommitsSuccessCancelAndSafeFailure(t *testing.T) {
	service := &canonTextIndexServiceForWorkerTest{}
	handler := NewTextIndexHandler(service)
	job := canonTextIndexJobForWorkerTest()

	result, err := handler.HandleResult(context.Background(), job)
	if err != nil || result.Status != jobs.StatusSucceeded {
		t.Fatalf("HandleResult() = %#v, %v", result, err)
	}
	result, err = handler.HandleCanceledResult(context.Background(), job)
	if err != nil || result.Status != jobs.StatusCanceled {
		t.Fatalf("HandleCanceledResult() = %#v, %v", result, err)
	}
	job.Error = &jobs.Error{Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage}
	result, err = handler.HandleFailedResult(context.Background(), job)
	if err != nil || result.Status != jobs.StatusFailed {
		t.Fatalf("HandleFailedResult() = %#v, %v", result, err)
	}
	if service.failure != *job.Error {
		t.Fatalf("committed failure = %#v, want %#v", service.failure, *job.Error)
	}
}

func TestCanonTextIndexHandlerRequiresExactKindClaimAndSubject(t *testing.T) {
	handler := NewTextIndexHandler(&canonTextIndexServiceForWorkerTest{})
	valid := canonTextIndexJobForWorkerTest()
	invalid := []jobs.Job{valid, valid, valid}
	invalid[0].Kind = jobs.KindImport
	invalid[1].WorkerClaim.ClaimIncarnation = 0
	invalid[2].Subject = jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: canonTextWorkerProjectID, SourceView: governance.SourceViewDraft}
	for _, job := range invalid {
		if _, err := handler.HandleResult(context.Background(), job); !errors.Is(err, ErrInvalidTextIndexJob) {
			t.Fatalf("HandleResult(%#v) error = %v, want ErrInvalidTextIndexJob", job, err)
		}
	}
}

func TestCanonTextIndexHandlerRedactsUnexpectedServiceError(t *testing.T) {
	service := &canonTextIndexServiceForWorkerTest{runErr: errors.New("private chapter body and database detail")}
	handler := NewTextIndexHandler(service)

	_, err := handler.HandleResult(context.Background(), canonTextIndexJobForWorkerTest())
	if !errors.Is(err, jobworker.ErrRetryableHandler) || strings.Contains(err.Error(), "private") {
		t.Fatalf("HandleResult() error = %v, want redacted retryable error", err)
	}
}

func TestCanonTextIndexHandlerReturnsSafeConflictWithoutRetry(t *testing.T) {
	conflict := canonTextIndexConflictForWorkerTest{}
	service := &canonTextIndexServiceForWorkerTest{runErr: conflict}
	handler := NewTextIndexHandler(service)

	_, err := handler.HandleResult(context.Background(), canonTextIndexJobForWorkerTest())
	if !errors.Is(err, conflict) || errors.Is(err, jobworker.ErrRetryableHandler) {
		t.Fatalf("HandleResult() error = %v, want non-retryable conflict", err)
	}
}

type canonTextIndexConflictForWorkerTest struct{}

func (canonTextIndexConflictForWorkerTest) Error() string { return "canonical text index conflict" }

func (canonTextIndexConflictForWorkerTest) NonRetryableCanonTextIndexError() {}

type canonTextIndexServiceForWorkerTest struct {
	runErr  error
	failure jobs.Error
}

func (s *canonTextIndexServiceForWorkerTest) RunForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error {
	return s.runErr
}

func (*canonTextIndexServiceForWorkerTest) CancelForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error {
	return nil
}

func (s *canonTextIndexServiceForWorkerTest) FailForClaim(_ context.Context, _ jobs.WorkerClaim, _ jobs.CanonTextIndexSubject, failure jobs.Error) error {
	s.failure = failure
	return nil
}

func canonTextIndexJobForWorkerTest() jobs.Job {
	subject := jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: canonTextWorkerProjectID, SourceView: governance.SourceViewDraft, CatalogRevision: 3}
	return jobs.Job{
		ID: canonTextWorkerJobID, ProjectID: canonTextWorkerProjectID, Kind: jobs.KindCanonTextIndex,
		Status: jobs.StatusRunning, Subject: subject,
		WorkerClaim: jobs.WorkerClaim{ProjectID: canonTextWorkerProjectID, JobID: canonTextWorkerJobID, ClaimIncarnation: 1},
	}
}
