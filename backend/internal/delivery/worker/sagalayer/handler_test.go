package sagalayer

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	service "canonkeeper/backend/internal/services/sagalayer"
)

const (
	testProjectID = "96000000-0000-4000-8000-000000000001"
	testJobID     = "96000000-0000-4000-8000-000000000002"
)

type builderStub struct {
	err      error
	canceled int
	failed   *jobs.Error
}

func (builder *builderStub) RunForClaim(context.Context, jobs.WorkerClaim, jobs.SagaLayerSubject) (jobs.Status, error) {
	return jobs.StatusSucceeded, builder.err
}

func (builder *builderStub) CancelForClaim(context.Context, jobs.WorkerClaim, jobs.SagaLayerSubject) error {
	builder.canceled++
	return nil
}

func (builder *builderStub) FailForClaim(_ context.Context, _ jobs.WorkerClaim, _ jobs.SagaLayerSubject, failure jobs.Error) error {
	builder.failed = &failure
	return nil
}

func layerJob(status jobs.Status) jobs.Job {
	return jobs.Job{
		ID: testJobID, ProjectID: testProjectID, Kind: jobs.KindSagaLayer, Status: status,
		Subject:     jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
		WorkerClaim: jobs.WorkerClaim{ProjectID: testProjectID, JobID: testJobID, ClaimIncarnation: 1},
	}
}

func TestHandlerRunsTheBuilderAndRetriesItsFailures(t *testing.T) {
	builder := &builderStub{}
	handler := NewHandler(builder)
	if handler.Kind() != jobs.KindSagaLayer {
		t.Fatalf("Kind() = %q", handler.Kind())
	}
	result, err := handler.HandleResult(context.Background(), layerJob(jobs.StatusRunning))
	if err != nil || result.Status != jobs.StatusSucceeded {
		t.Fatalf("HandleResult() = %+v, %v", result, err)
	}
	builder.err = errors.New("the model is down")
	if _, err := handler.HandleResult(context.Background(), layerJob(jobs.StatusRunning)); !errors.Is(err, jobworker.ErrRetryableHandler) {
		t.Fatalf("a failed run = %v, want a retry", err)
	}
	builder.err = service.ErrInvalid
	if _, err := handler.HandleResult(context.Background(), layerJob(jobs.StatusRunning)); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("an invalid run = %v", err)
	}
	if _, err := handler.HandleResult(context.Background(), layerJob(jobs.StatusCanceling)); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("a Job in another state = %v", err)
	}
}

func TestHandlerEndsACanceledAndAFailedJob(t *testing.T) {
	builder := &builderStub{}
	handler := NewHandler(builder)
	if result, err := handler.HandleCanceledResult(context.Background(), layerJob(jobs.StatusCanceling)); err != nil || result.Status != jobs.StatusCanceled || builder.canceled != 1 {
		t.Fatalf("HandleCanceledResult() = %+v, %v", result, err)
	}
	if result, err := handler.HandleFailedResult(context.Background(), layerJob(jobs.StatusRunning)); err != nil || result.Status != jobs.StatusFailed || builder.failed == nil || builder.failed.Code != jobworker.HandlerFailedCode {
		t.Fatalf("HandleFailedResult() = %+v, %v, failure %+v", result, err, builder.failed)
	}
}
