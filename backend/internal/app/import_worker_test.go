package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	"canonkeeper/backend/internal/library"
)

func TestStartImportWorkerRunnerProcessesQueuedImportJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storageKey := "import/api-file.fb2"
	store := &fakeImportWorkerStoreForAPI{
		job: jobs.Job{
			ID:        "job-1",
			ProjectID: "project-1",
			Kind:      jobs.KindImport,
			Status:    jobs.StatusQueued,
			Subject:   jobs.ImportSubject{Type: jobs.KindImport, ID: "project-1", ProjectID: "project-1"},
			CanCancel: true,
		},
	}
	loader := &fakeImportLoaderForAPI{
		request: library.ImportJob{
			ID:          "job-1",
			ProjectID:   "project-1",
			ActorUserID: "actor-1",
			Kind:        string(jobs.KindImport),
			Status:      string(jobs.StatusQueued),
			Subject:     library.ImportJobSubject{Type: "import", ID: "project-1", ProjectID: "project-1"},
			Metadata: library.ImportFileMetadata{
				StorageKey:     storageKey,
				SourceFileName: "api-upload.fb2",
				Extension:      ".fb2",
				MIMEType:       "application/xml",
				SizeBytes:      int64(len(smallFB2ForImportAPITest())),
				FileSHA256:     sha256HexForImportAPITest(smallFB2ForImportAPITest()),
				ImportMode:     library.ImportModeNewBook,
			},
		},
		file: smallFB2ForImportAPITest(),
	}
	service := &fakeImportLibraryServiceForAPI{store: store, finalizedCh: make(chan struct{})}

	startJobWorkerPool(ctx, store, []jobworker.Handler{library.NewImportHandler(loader, service)}, 3, "job", log.New(io.Discard, "", 0))

	select {
	case <-service.finalizedCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for import worker to process queued job")
	}
	if store.claimKind() != jobs.KindImport {
		t.Fatalf("claimed kind = %q, want import", store.claimKind())
	}
	if service.bookActorUserID() != "actor-1" {
		t.Fatalf("book actor = %q, want import request actor", service.bookActorUserID())
	}
	if service.chapterCount() != 1 {
		t.Fatalf("created chapters = %d, want 1", service.chapterCount())
	}
	if loader.loadedProjectID != "project-1" {
		t.Fatalf("loaded project ID = %q, want project-1", loader.loadedProjectID)
	}
}

type fakeImportWorkerStoreForAPI struct {
	mu      sync.Mutex
	job     jobs.Job
	claimed bool
	kind    jobs.Kind
}

func (s *fakeImportWorkerStoreForAPI) ClaimNextQueuedJobForWorker(_ context.Context, kinds []jobs.Kind) (jobs.WorkerClaimDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(kinds) != 1 {
		return jobs.WorkerClaimDecision{}, jobs.ErrValidation
	}
	s.kind = kinds[0]
	if s.claimed {
		return jobs.WorkerClaimDecision{}, jobs.ErrNotFound
	}
	s.claimed = true
	job := s.job
	job.Status = jobs.StatusRunning
	job.WorkerClaim = jobs.WorkerClaim{ProjectID: job.ProjectID, JobID: job.ID, ClaimIncarnation: 1}
	s.job = job
	return jobs.WorkerClaimDecision{Claim: job.WorkerClaim}, nil
}

func (s *fakeImportWorkerStoreForAPI) LoadJobForWorkerClaim(_ context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.WorkerClaim != claim {
		return jobs.Job{}, jobs.ErrNotFound
	}
	return s.job, nil
}

func (s *fakeImportWorkerStoreForAPI) HeartbeatWorkerClaim(_ context.Context, claim jobs.WorkerClaim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.WorkerClaim != claim || s.job.Status != jobs.StatusRunning {
		return jobs.ErrConflict
	}
	return nil
}

func (s *fakeImportWorkerStoreForAPI) RequeueWorkerClaim(_ context.Context, input jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.WorkerClaim != input.Claim || s.job.Kind != input.Kind || s.job.Status != jobs.StatusRunning ||
		s.job.RetryCount != input.ExpectedRetryCount {
		return jobs.Job{}, jobs.ErrConflict
	}
	if input.ExpectedRetryCount == jobs.MaxJobAttempts-1 {
		return jobs.Job{}, jobs.ErrRetryExhausted
	}
	delay, err := jobs.RetryDelay(input.ExpectedRetryCount + 1)
	if err != nil {
		return jobs.Job{}, err
	}
	s.job.Status = jobs.StatusQueued
	s.job.RetryCount++
	s.job.WorkerClaim.ClaimIncarnation++
	s.job.NextAttemptAt = time.Now().UTC().Add(delay)
	return s.job, nil
}

func (s *fakeImportWorkerStoreForAPI) ReleaseWorkerClaim(_ context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.WorkerClaim != claim || s.job.Status != jobs.StatusRunning {
		return jobs.Job{}, jobs.ErrConflict
	}
	s.job.Status = jobs.StatusQueued
	s.job.WorkerClaim.ClaimIncarnation++
	return s.job, nil
}

func (s *fakeImportWorkerStoreForAPI) commitSucceeded(jobID string, incarnation int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.ID == jobID && s.job.WorkerClaim.ClaimIncarnation == incarnation {
		s.job.Status = jobs.StatusSucceeded
	}
}

func (s *fakeImportWorkerStoreForAPI) commitTerminal(jobID string, incarnation int64, status jobs.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.ID == jobID && s.job.WorkerClaim.ClaimIncarnation == incarnation {
		s.job.Status = status
	}
}

func (s *fakeImportWorkerStoreForAPI) claimKind() jobs.Kind {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kind
}

type fakeImportLoaderForAPI struct {
	request         library.ImportJob
	file            []byte
	loadedProjectID string
}

func (l *fakeImportLoaderForAPI) GetImportJobForExecution(_ context.Context, projectID, _ string) (library.ImportJob, error) {
	l.loadedProjectID = projectID
	return l.request, nil
}

func (l *fakeImportLoaderForAPI) GetImportFile(_ context.Context, projectID, _ string) ([]byte, error) {
	l.loadedProjectID = projectID
	return append([]byte(nil), l.file...), nil
}

func (l *fakeImportLoaderForAPI) LoadImportBodyForClaim(ctx context.Context, claim jobs.WorkerClaim, storageKey string) ([]byte, error) {
	return l.GetImportFile(ctx, claim.ProjectID, storageKey)
}

type fakeImportLibraryServiceForAPI struct {
	mu          sync.Mutex
	store       *fakeImportWorkerStoreForAPI
	actorID     string
	chapters    int
	bookTitle   string
	finalizedCh chan struct{}
}

func (*fakeImportLibraryServiceForAPI) MarkImportParsingForClaim(context.Context, jobs.WorkerClaim) error {
	return nil
}

func (*fakeImportLibraryServiceForAPI) MarkImportSavingForClaim(context.Context, jobs.WorkerClaim, int) error {
	return nil
}

func (s *fakeImportLibraryServiceForAPI) FinalizeImport(_ context.Context, input library.FinalizeImportStoreInput) (library.FinalizedImport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actorID = input.ActorUserID
	s.bookTitle = input.BookTitle
	s.chapters = len(input.Chapters)
	if s.store != nil {
		s.store.commitSucceeded(input.JobID, input.ExpectedClaimIncarnation)
	}
	if s.finalizedCh == nil {
		s.finalizedCh = make(chan struct{})
	}
	select {
	case <-s.finalizedCh:
	default:
		close(s.finalizedCh)
	}
	return library.FinalizedImport{
		BookID: "book-1", ChapterCount: len(input.Chapters), ParagraphCount: 1,
		ImportMode: input.ImportMode, SourceFileName: input.SourceFileName,
	}, nil
}

func (s *fakeImportLibraryServiceForAPI) FinalizeImportTerminalForClaim(_ context.Context, input library.FinalizeImportTerminalForClaimInput) (jobs.Status, error) {
	if s.store != nil {
		s.store.commitTerminal(input.Claim.JobID, input.Claim.ClaimIncarnation, input.Status)
	}
	return input.Status, nil
}

func (s *fakeImportLibraryServiceForAPI) ExpireImportStagingForClaim(_ context.Context, claim jobs.WorkerClaim) (jobs.Status, error) {
	if s.store != nil {
		s.store.commitTerminal(claim.JobID, claim.ClaimIncarnation, jobs.StatusExpired)
	}
	return jobs.StatusExpired, nil
}

func (s *fakeImportLibraryServiceForAPI) bookActorUserID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actorID
}

func (s *fakeImportLibraryServiceForAPI) chapterCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chapters
}

func smallFB2ForImportAPITest() []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description><title-info><book-title>API Parsed Title</book-title></title-info></description>
  <body><section><title><p>Only Chapter</p></title><p>Only paragraph</p></section></body>
</FictionBook>`)
}

func sha256HexForImportAPITest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
