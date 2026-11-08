package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
)

type fakeImportStagingExpiryStore struct {
	mu            sync.Mutex
	claims        []jobs.WorkerClaim
	claimErr      error
	expireStatus  jobs.Status
	expireErr     error
	expiredClaims []jobs.WorkerClaim
	claimCallsCh  chan struct{}
}

func (f *fakeImportStagingExpiryStore) ClaimNextImportExpiryForWorker(context.Context) (jobs.WorkerClaim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimCallsCh != nil {
		select {
		case f.claimCallsCh <- struct{}{}:
		default:
		}
	}
	if f.claimErr != nil {
		return jobs.WorkerClaim{}, f.claimErr
	}
	if len(f.claims) == 0 {
		return jobs.WorkerClaim{}, jobs.ErrNotFound
	}
	claim := f.claims[0]
	f.claims = f.claims[1:]
	return claim, nil
}

func (f *fakeImportStagingExpiryStore) ExpireImportStagingForClaim(_ context.Context, claim jobs.WorkerClaim) (jobs.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expiredClaims = append(f.expiredClaims, claim)
	if f.expireErr != nil {
		return "", f.expireErr
	}
	return f.expireStatus, nil
}

func (f *fakeImportStagingExpiryStore) expirations() []jobs.WorkerClaim {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]jobs.WorkerClaim(nil), f.expiredClaims...)
}

func TestExpireNextImportStagingUsesExactClaimAndLogsCommittedExpiry(t *testing.T) {
	claim := jobs.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 7}
	store := &fakeImportStagingExpiryStore{
		claims:       []jobs.WorkerClaim{claim},
		expireStatus: jobs.StatusExpired,
	}
	var logs bytes.Buffer

	if !expireNextImportStaging(context.Background(), store, log.New(&logs, "", 0)) {
		t.Fatal("expireNextImportStaging() = false, want claimed work")
	}
	if got := store.expirations(); len(got) != 1 || got[0] != claim {
		t.Fatalf("expired claims = %#v, want exact claim %#v", got, claim)
	}
	if output := logs.String(); !strings.Contains(output, "import staging expiry completed") {
		t.Fatalf("log output = %q, want completed marker", output)
	}
}

func TestExpireNextImportStagingStopsQuietlyWhenNoClaimExists(t *testing.T) {
	store := &fakeImportStagingExpiryStore{}
	var logs bytes.Buffer

	if expireNextImportStaging(context.Background(), store, log.New(&logs, "", 0)) {
		t.Fatal("expireNextImportStaging() = true, want no work")
	}
	if len(store.expirations()) != 0 {
		t.Fatalf("expired claims = %#v, want none", store.expirations())
	}
	if logs.Len() != 0 {
		t.Fatalf("log output = %q, want quiet no-work result", logs.String())
	}
}

func TestExpireNextImportStagingLogsGenericFailureWithoutRawDetail(t *testing.T) {
	store := &fakeImportStagingExpiryStore{claimErr: errors.New("raw-upload-secret")}
	var logs bytes.Buffer

	if expireNextImportStaging(context.Background(), store, log.New(&logs, "", 0)) {
		t.Fatal("expireNextImportStaging() = true, want failed claim")
	}
	output := logs.String()
	if !strings.Contains(output, "failed") {
		t.Fatalf("log output = %q, want generic failure", output)
	}
	if strings.Contains(output, "raw-upload-secret") {
		t.Fatalf("log output leaked raw error detail: %q", output)
	}
}

func TestStartImportStagingExpiryRunnerClaimsOneAtStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claimCalls := make(chan struct{}, 1)
	store := &fakeImportStagingExpiryStore{claimCallsCh: claimCalls}

	startImportStagingExpiryRunnerWithInterval(ctx, store, time.Hour, log.New(io.Discard, "", 0))

	select {
	case <-claimCalls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for startup expiry claim")
	}
}
