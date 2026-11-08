package researchchat

import (
	"context"
	"errors"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
)

type turnRunnerStub struct {
	claim researchdomain.WorkerClaim
	err   error
}

type turnCancelerStub struct {
	job jobs.Job
}

func (stub *turnCancelerStub) CancelClaim(_ context.Context, job jobs.Job) error {
	stub.job = job
	return nil
}

type turnFailerStub struct {
	job jobs.Job
}

func (stub *turnFailerStub) FailClaim(_ context.Context, job jobs.Job) error {
	stub.job = job
	return nil
}

func (stub *turnRunnerStub) Execute(_ context.Context, claim researchdomain.WorkerClaim) (researchdomain.TurnResult, error) {
	stub.claim = claim
	return researchdomain.TurnResult{Response: "Ответ"}, stub.err
}

func TestHandlerDoesNotRetryUnconfirmedRunError(t *testing.T) {
	runErr := errors.New("stream disconnected")
	handler := NewHandler(
		&turnRunnerStub{err: runErr},
		&turnCancelerStub{}, &turnFailerStub{},
	)
	job := jobs.Job{
		ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn, Status: jobs.StatusRunning,
		WorkerClaim: jobs.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 4},
	}
	_, err := handler.HandleResult(context.Background(), job)
	if !errors.Is(err, runErr) || errors.Is(err, jobworker.ErrRetryableHandler) {
		t.Fatalf("run error = %v", err)
	}
}

func TestHandlerCommitsExactFailureOnRetryExhaustion(t *testing.T) {
	failer := &turnFailerStub{}
	handler := NewHandler(&turnRunnerStub{}, &turnCancelerStub{}, failer)
	job := jobs.Job{
		ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn, Status: jobs.StatusRunning,
		WorkerClaim: jobs.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 4},
		Error:       &jobs.Error{Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage},
	}
	result, err := handler.HandleFailedResult(context.Background(), job)
	if err != nil || result.Status != jobs.StatusFailed || failer.job.ID != job.ID || failer.job.Error != job.Error {
		t.Fatalf("failure = (%+v, %v, committed=%+v)", result, err, failer.job)
	}
}

func TestHandlerRunsExactResearchChatWorkerClaim(t *testing.T) {
	runner := &turnRunnerStub{}
	handler := NewHandler(runner, &turnCancelerStub{}, &turnFailerStub{})
	job := jobs.Job{
		ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn, Status: jobs.StatusRunning,
		WorkerClaim: jobs.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 4},
	}
	result, err := handler.HandleResult(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != jobs.StatusSucceeded || runner.claim.ProjectID != job.ProjectID ||
		runner.claim.JobID != job.ID || runner.claim.ClaimIncarnation != 4 {
		t.Fatalf("handler = (%+v, claim=%+v)", result, runner.claim)
	}
}

func TestHandlerInterruptsExactRootBeforeCompletingCancelingClaim(t *testing.T) {
	canceler := &turnCancelerStub{}
	handler := NewHandler(&turnRunnerStub{}, canceler, &turnFailerStub{})
	job := jobs.Job{
		ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn, Status: jobs.StatusCanceling,
		WorkerClaim: jobs.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 4},
	}
	result, err := handler.HandleCanceledResult(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != jobs.StatusCanceled || canceler.job.ID != job.ID || canceler.job.WorkerClaim != job.WorkerClaim {
		t.Fatalf("cancel handler = (%+v, job=%+v)", result, canceler.job)
	}
}
