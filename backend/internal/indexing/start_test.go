package indexing

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

func TestServiceStartIndexingBuildsExactViewRequest(t *testing.T) {
	store := &startStoreForTest{
		job: jobs.Job{
			ID: "job-1", ProjectID: "project-1", Kind: jobs.KindCanonTextIndex,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: "project-1",
				SourceView: governance.SourceViewDraft, CatalogRevision: 4,
			},
		},
	}
	service := NewService(store, &startIDsForTest{jobID: "job-1", recordID: "record-1"}, startClockForTest{})

	job, err := service.StartIndexing(context.Background(), Actor{UserID: " editor-1 "}, StartIndexingInput{
		ProjectID: "project-1", SourceView: governance.SourceViewDraft,
		RequestMethod: " post ", RequestPath: " /projects/project-1/indexing-runs ",
		IdempotencyKey: " same-key ",
	})
	if err != nil {
		t.Fatalf("StartIndexing() error = %v", err)
	}
	if job.ID != "job-1" || store.calls != 1 {
		t.Fatalf("job/calls = %#v/%d, want forwarded store result", job, store.calls)
	}
	input := store.input
	if input.CandidateJobID != "job-1" || input.IdempotencyRecordID != "record-1" ||
		input.ActorUserID != "editor-1" || input.ProjectID != "project-1" ||
		input.SourceView != governance.SourceViewDraft || input.OperationID != indexingStartOperationID ||
		input.IdempotencyStatusCode != indexingStartIdempotencyStatusOK || input.CreatedAt != startTestTime() {
		t.Fatalf("store input = %#v, want normalized exact-view request", input)
	}
	if len(input.ScopeHash) != 64 || len(input.IdempotencyKeyHash) != 64 || len(input.RequestHash) != 64 ||
		input.IdempotencyKeyHash == "same-key" {
		t.Fatalf("idempotency hashes = %#v, want SHA-256 values", input)
	}
}

func TestServiceStartIndexingRejectsInvalidInputBeforeStore(t *testing.T) {
	store := &startStoreForTest{}
	service := NewService(store, &startIDsForTest{jobID: "job-1", recordID: "record-1"}, startClockForTest{})

	_, err := service.StartIndexing(context.Background(), Actor{UserID: "editor-1"}, StartIndexingInput{
		ProjectID: "project-1", SourceView: "preview",
		RequestMethod: "POST", RequestPath: "/projects/project-1/indexing-runs", IdempotencyKey: "same-key",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("StartIndexing() error = %v, want ErrValidation", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestServiceStartIndexingMapsStoreError(t *testing.T) {
	store := &startStoreForTest{err: jobs.ErrForbidden}
	service := NewService(store, &startIDsForTest{jobID: "job-1", recordID: "record-1"}, startClockForTest{})

	_, err := service.StartIndexing(context.Background(), Actor{UserID: "viewer-1"}, StartIndexingInput{
		ProjectID: "project-1", SourceView: governance.SourceViewPublished,
		RequestMethod: "POST", RequestPath: "/projects/project-1/indexing-runs", IdempotencyKey: "same-key",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("StartIndexing() error = %v, want ErrForbidden", err)
	}
}

type startStoreForTest struct {
	// В этих тестах вызывается только StartIndexing; worker-методы Store не нужны.
	Store
	input StartIndexingStoreInput
	job   jobs.Job
	err   error
	calls int
}

func (store *startStoreForTest) StartIndexing(_ context.Context, input StartIndexingStoreInput) (jobs.Job, error) {
	store.calls++
	store.input = input
	return store.job, store.err
}

type startIDsForTest struct {
	jobID    string
	recordID string
}

func (ids *startIDsForTest) NewIndexingJobID() string       { return ids.jobID }
func (ids *startIDsForTest) NewIdempotencyRecordID() string { return ids.recordID }

type startClockForTest struct{}

func (startClockForTest) Now() time.Time { return startTestTime() }

func startTestTime() time.Time {
	return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
}
