package sagamemory

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
)

type yieldingBuilder struct{}

func (yieldingBuilder) RunForClaim(context.Context, jobs.WorkerClaim, jobs.ChapterCardsSubject, bool) (jobs.Status, error) {
	return jobs.StatusQueued, nil
}

func (yieldingBuilder) CancelForClaim(context.Context, jobs.WorkerClaim, jobs.ChapterCardsSubject) error {
	return nil
}

func (yieldingBuilder) FailForClaim(context.Context, jobs.WorkerClaim, jobs.ChapterCardsSubject, jobs.Error) error {
	return nil
}

// Quota spec R12.2: a builder that yielded its Job reports the requeue the worker verifies.
func TestHandleResultReportsCardsPortionRequeue(t *testing.T) {
	const projectID, jobID = "94000000-0000-4000-8000-000000000001", "94000000-0000-4000-8000-000000000002"
	result, err := NewHandler(yieldingBuilder{}).HandleResult(context.Background(), jobs.Job{
		ID: jobID, ProjectID: projectID, Kind: jobs.KindChapterCards, Status: jobs.StatusRunning,
		Subject:     jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: governance.SourceViewDraft},
		WorkerClaim: jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: 1},
	})
	if err != nil || result.Status != jobs.StatusQueued || !errors.Is(result.RequeueCause, service.ErrCardsPortion) {
		t.Fatalf("HandleResult() = %+v, %v; want queued after a portion", result, err)
	}
}
