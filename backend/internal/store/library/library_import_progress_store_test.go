package librarystore

import (
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

func TestLibraryStoreImportProgressTransitionsStoreExactFacts(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	stored, err := jobstore.New(fixture.db).LoadJobForWorkerClaim(fixture.ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	progress := stored.ImportProgress
	if progress == nil || progress.Phase != jobs.ImportPhaseSaving ||
		progress.BytesTransferred != int64(len("<FictionBook>failpoint import</FictionBook>")) ||
		progress.BytesTotal != progress.BytesTransferred || progress.ParsedChapters == nil ||
		progress.TotalChapters == nil || *progress.ParsedChapters != 1 || *progress.TotalChapters != 1 ||
		progress.SavedChapters == nil || *progress.SavedChapters != 0 || progress.ParsingStartedAt == nil ||
		progress.ParsedAt == nil || progress.SavingStartedAt == nil {
		t.Fatalf("saving progress = %#v", progress)
	}
	if !progress.ParsedAt.Equal(*progress.SavingStartedAt) || progress.ParsedAt.Before(*progress.ParsingStartedAt) {
		t.Fatalf("progress timestamps are not monotonic: %#v", progress)
	}

	stale := fixture.claim
	stale.ClaimIncarnation++
	if err := New(fixture.db).MarkImportSavingForClaim(fixture.ctx, stale, 1); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("stale MarkImportSavingForClaim error = %v", err)
	}
}

func TestLibraryStoreImportRetryReturnsToQueuedWithoutLosingBytes(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	before, err := jobstore.New(fixture.db).LoadJobForWorkerClaim(fixture.ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	if before.ImportProgress == nil {
		t.Fatal("running import has no typed progress")
	}
	requeued, err := jobstore.New(fixture.db).RequeueWorkerClaim(fixture.ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: fixture.claim, Kind: jobs.KindImport, ExpectedRetryCount: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	progress := requeued.ImportProgress
	if requeued.Status != jobs.StatusQueued || progress == nil || progress.Phase != jobs.ImportPhaseQueued ||
		progress.BytesTransferred != before.ImportProgress.BytesTransferred ||
		progress.BytesTotal != before.ImportProgress.BytesTotal || progress.ParsedChapters != nil ||
		progress.TotalChapters != nil || progress.SavedChapters != nil || progress.ParsingStartedAt != nil ||
		progress.ParsedAt != nil || progress.SavingStartedAt != nil || progress.SavedAt != nil {
		t.Fatalf("requeued import progress = %#v", requeued)
	}
}

func TestLibraryStoreImportFailurePreservesLastPhase(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	status, err := New(fixture.db).FinalizeImportTerminalForClaim(fixture.ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: fixture.claim, Status: jobs.StatusFailed,
		Error: &jobs.Error{Code: "worker.handler_failed", Message: "The background job failed."},
	})
	if err != nil || status != jobs.StatusFailed {
		t.Fatalf("FinalizeImportTerminalForClaim() = %q, %v", status, err)
	}
	stored, err := jobstore.New(fixture.db).LoadJobForWorkerClaim(fixture.ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ImportProgress == nil || stored.ImportProgress.Phase != jobs.ImportPhaseSaving ||
		stored.ImportProgress.SavedChapters == nil || *stored.ImportProgress.SavedChapters != 0 {
		t.Fatalf("failed import progress = %#v", stored.ImportProgress)
	}
}

func TestLibraryStoreImportCancelPreservesLastPhase(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	jobsStore := jobstore.New(fixture.db)
	canceling, err := jobsStore.CancelJobStatus(fixture.ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: fixture.userID, ProjectID: fixture.projectID, JobID: fixture.jobID,
		From: jobs.StatusRunning, To: jobs.StatusCanceling, UpdatedAt: time.Now().UTC(),
	})
	if err != nil || canceling.ImportProgress == nil || canceling.ImportProgress.Phase != jobs.ImportPhaseSaving {
		t.Fatalf("CancelJobStatus() = %#v, %v", canceling, err)
	}
	status, err := New(fixture.db).FinalizeImportTerminalForClaim(fixture.ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: fixture.claim, Status: jobs.StatusCanceled,
	})
	if err != nil || status != jobs.StatusCanceled {
		t.Fatalf("FinalizeImportTerminalForClaim() = %q, %v", status, err)
	}
	stored, err := jobsStore.LoadJobForWorkerClaim(fixture.ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ImportProgress == nil || stored.ImportProgress.Phase != jobs.ImportPhaseSaving {
		t.Fatalf("canceled import progress = %#v", stored.ImportProgress)
	}
}
