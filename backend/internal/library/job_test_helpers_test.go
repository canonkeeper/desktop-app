package library

import (
	"context"
	"slices"
	"sync"
	"time"

	"canonkeeper/backend/internal/jobs"
)

type libraryJobStoreForTest struct {
	mu   sync.Mutex
	jobs map[string]jobs.Job
}

func newLibraryJobStoreForTest(records ...jobs.Job) *libraryJobStoreForTest {
	store := &libraryJobStoreForTest{jobs: make(map[string]jobs.Job)}
	for _, record := range records {
		store.jobs[record.ID] = record
	}
	return store
}

func (s *libraryJobStoreForTest) ClaimNextQueuedJobForWorker(ctx context.Context, kinds []jobs.Kind) (jobs.WorkerClaimDecision, error) {
	if err := ctx.Err(); err != nil {
		return jobs.WorkerClaimDecision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var claimedID string
	for id, record := range s.jobs {
		if !slices.Contains(kinds, record.Kind) || (record.Status != jobs.StatusQueued && record.Status != jobs.StatusCanceling) {
			continue
		}
		if claimedID == "" || record.CreatedAt.Before(s.jobs[claimedID].CreatedAt) ||
			(record.CreatedAt.Equal(s.jobs[claimedID].CreatedAt) && record.ID < s.jobs[claimedID].ID) {
			claimedID = id
		}
	}
	if claimedID == "" {
		return jobs.WorkerClaimDecision{}, jobs.ErrNotFound
	}
	claimed := s.jobs[claimedID]
	if claimed.Status == jobs.StatusQueued {
		claimed.Status = jobs.StatusRunning
	}
	claim := jobs.WorkerClaim{
		ProjectID:        claimed.ProjectID,
		JobID:            claimed.ID,
		ClaimIncarnation: claimed.WorkerClaim.ClaimIncarnation + 1,
	}
	claimed.WorkerClaim = claim
	s.jobs[claimedID] = claimed
	return jobs.WorkerClaimDecision{Claim: claim}, nil
}

func (s *libraryJobStoreForTest) LoadJobForWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	if err := ctx.Err(); err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[claim.JobID]
	if !ok || record.ProjectID != claim.ProjectID || record.WorkerClaim != claim {
		return jobs.Job{}, jobs.ErrNotFound
	}
	return record, nil
}

func (s *libraryJobStoreForTest) HeartbeatWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[claim.JobID]
	if !ok || record.ProjectID != claim.ProjectID || record.WorkerClaim != claim || record.Status != jobs.StatusRunning {
		return jobs.ErrConflict
	}
	return nil
}

func (s *libraryJobStoreForTest) ReleaseWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	if err := ctx.Err(); err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[claim.JobID]
	if !ok || record.ProjectID != claim.ProjectID || record.WorkerClaim != claim || record.Status != jobs.StatusRunning {
		return jobs.Job{}, jobs.ErrConflict
	}
	record.Status = jobs.StatusQueued
	record.WorkerClaim.ClaimIncarnation++
	s.jobs[claim.JobID] = record
	return record, nil
}

func (s *libraryJobStoreForTest) RequeueWorkerClaim(ctx context.Context, input jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error) {
	if err := ctx.Err(); err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[input.Claim.JobID]
	if !ok || record.ProjectID != input.Claim.ProjectID || record.WorkerClaim != input.Claim ||
		record.Kind != input.Kind || record.Status != jobs.StatusRunning || record.RetryCount != input.ExpectedRetryCount {
		return jobs.Job{}, jobs.ErrConflict
	}
	if input.ExpectedRetryCount == jobs.MaxJobAttempts-1 {
		return jobs.Job{}, jobs.ErrRetryExhausted
	}
	delay, err := jobs.RetryDelay(input.ExpectedRetryCount + 1)
	if err != nil {
		return jobs.Job{}, err
	}
	record.Status = jobs.StatusQueued
	record.RetryCount++
	record.NextAttemptAt = libraryJobTestNow().Add(delay)
	record.WorkerClaim.ClaimIncarnation++
	s.jobs[input.Claim.JobID] = record
	return record, nil
}

func (s *libraryJobStoreForTest) forceStatusForTest(jobID string, status jobs.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.jobs[jobID]
	record.Status = status
	s.jobs[jobID] = record
}

func libraryJobTestNow() time.Time {
	return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
}
