package deletion

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

const (
	coordinatorProjectID   = "11111111-1111-4111-8111-111111111111"
	coordinatorTombstoneID = "22222222-2222-4222-8222-222222222222"
)

func TestTargetClaimValidateRejectsInvalidAuthority(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	valid := targetClaimForTest(now)

	tests := map[string]func(*deletiondomain.TargetClaim){
		"project":     func(claim *deletiondomain.TargetClaim) { claim.ProjectID = "not-a-uuid" },
		"tombstone":   func(claim *deletiondomain.TargetClaim) { claim.TombstoneID = "not-a-uuid" },
		"target":      func(claim *deletiondomain.TargetClaim) { claim.Target = "unknown" },
		"incarnation": func(claim *deletiondomain.TargetClaim) { claim.ClaimIncarnation = 0 },
		"owner":       func(claim *deletiondomain.TargetClaim) { claim.LeaseOwnerDigest = governance.Digest{} },
		"expired":     func(claim *deletiondomain.TargetClaim) { claim.ObservedLeaseExpiry = now },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			claim := valid
			mutate(&claim)
			if err := claim.Validate(now); err == nil {
				t.Fatal("Validate() error = nil, want rejection")
			}
		})
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
}

func TestCoordinatorRunOnceRecoversCommittedResponseLoss(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetFilesystem)
	result := deletiondomain.TargetResult{
		Disposition:    deletiondomain.DispositionPurged,
		HandlerVersion: governance.HandlerFilesystemOSRootV1,
		Reason:         governance.ReasonPurged,
	}
	store := &coordinatorStoreForTest{request: request, completeResponseLost: true}
	coordinator := newCoordinatorForTest(t, now, store, TargetHandlers{
		governance.TargetFilesystem: targetHandlerFunc(func(context.Context, deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
			return result, nil
		}),
	}, false)

	outcome, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if outcome != RunCompleted {
		t.Fatalf("RunOnce() outcome = %q, want %q", outcome, RunCompleted)
	}
	if store.completeCalls != 1 || store.lookupCalls != 1 {
		t.Fatalf("complete/lookup calls = %d/%d, want 1/1", store.completeCalls, store.lookupCalls)
	}
	if err := store.receipt.ValidateFor(request, result); err != nil {
		t.Fatalf("committed receipt validation failed: %v", err)
	}
}

func TestCoordinatorRunOnceSchedulesTypedRetryWithoutRawError(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetRuntime)
	raw := errors.New("private provider response: do not persist")
	store := &coordinatorStoreForTest{request: request}
	coordinator := newCoordinatorForTest(t, now, store, TargetHandlers{
		governance.TargetRuntime: targetHandlerFunc(func(context.Context, deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
			return deletiondomain.TargetResult{}, raw
		}),
	}, false)

	diagnostic, err := coordinator.RunOnceDiagnostic(context.Background())
	if err != nil {
		t.Fatalf("RunOnceDiagnostic() error = %v", err)
	}
	if diagnostic.Outcome != RunRetryScheduled {
		t.Fatalf("RunOnceDiagnostic() outcome = %q, want %q", diagnostic.Outcome, RunRetryScheduled)
	}
	if diagnostic.TaskID != coordinatorTombstoneID || diagnostic.ProjectID != coordinatorProjectID ||
		diagnostic.SourceKind != governance.DeletionSelectionProject || diagnostic.SourceID != coordinatorProjectID ||
		diagnostic.Target != governance.TargetRuntime || diagnostic.Attempt != 1 ||
		diagnostic.Reason != governance.ReasonValidationFailed || diagnostic.RetryAfter != 17*time.Second {
		t.Fatalf("RunOnceDiagnostic() = %#v", diagnostic)
	}
	if store.failure.Reason != governance.ReasonValidationFailed {
		t.Fatalf("failure reason = %q, want validation_failed", store.failure.Reason)
	}
	if store.failure.RetryAfter != 17*time.Second {
		t.Fatalf("retry delay = %s, want %s", store.failure.RetryAfter, 17*time.Second)
	}
	if bytes.Contains([]byte(store.failure.Reason), []byte(raw.Error())) {
		t.Fatal("typed failure persisted the raw handler error")
	}
}

func TestCoordinatorRequeuesDurablePostgresProgressOnNextTick(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetPostgreSQL)
	request.Attempt = 30
	store := &coordinatorStoreForTest{request: request}
	coordinator := newCoordinatorForTest(t, now, store, TargetHandlers{
		governance.TargetPostgreSQL: targetHandlerFunc(func(context.Context, deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
			return deletiondomain.TargetResult{
				Disposition: deletiondomain.DispositionRetry,
				Reason:      governance.ReasonStaleSource,
			}, nil
		}),
	}, false)

	diagnostic, err := coordinator.RunOnceDiagnostic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Outcome != RunRetryScheduled || diagnostic.Reason != governance.ReasonStaleSource ||
		diagnostic.RetryAfter != time.Second {
		t.Fatalf("durable progress diagnostic = %#v", diagnostic)
	}
	if store.failure.Reason != governance.ReasonStaleSource || store.failure.RetryAfter != time.Second {
		t.Fatalf("durable progress retry = %#v", store.failure)
	}
}

func TestCoordinatorCancelsHandlerWhenHeartbeatLosesClaim(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetRuntime)
	store := &coordinatorStoreForTest{
		request:      request,
		heartbeatErr: deletiondomain.ErrLostTargetClaim,
	}
	handlerCanceled := make(chan struct{})
	coordinator := newCoordinatorForTest(t, now, store, TargetHandlers{
		governance.TargetRuntime: targetHandlerFunc(func(ctx context.Context, _ deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
			<-ctx.Done()
			close(handlerCanceled)
			return deletiondomain.TargetResult{}, ctx.Err()
		}),
	}, false)
	coordinator.heartbeatInterval = time.Millisecond

	_, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, deletiondomain.ErrLostTargetClaim) {
		t.Fatalf("RunOnce() error = %v, want deletiondomain.ErrLostTargetClaim", err)
	}
	select {
	case <-handlerCanceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled after claim loss")
	}
	if store.failureCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("lost claim wrote failure/completion: %d/%d", store.failureCalls, store.completeCalls)
	}
}

func TestTargetResultValidationMatrix(t *testing.T) {
	tests := []struct {
		name    string
		target  governance.DeletionTarget
		result  deletiondomain.TargetResult
		wantErr bool
	}{
		{
			name:   "purged",
			target: governance.TargetPostgreSQL,
			result: deletiondomain.TargetResult{Disposition: deletiondomain.DispositionPurged, HandlerVersion: governance.HandlerPostgresExactInventoryV1,
				Reason: governance.ReasonPurged},
		},
		{
			name:   "retry",
			target: governance.TargetRuntime,
			result: deletiondomain.TargetResult{Disposition: deletiondomain.DispositionRetry, Reason: governance.ReasonTargetUnavailable},
		},
		{
			name:   "retry carries handler",
			target: governance.TargetRuntime,
			result: deletiondomain.TargetResult{Disposition: deletiondomain.DispositionRetry, HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
				Reason: governance.ReasonTargetUnavailable},
			wantErr: true,
		},
		{
			name:   "wrong disposition",
			target: governance.TargetPostgreSQL,
			result: deletiondomain.TargetResult{Disposition: deletiondomain.DispositionNoData, HandlerVersion: governance.HandlerPostgresExactInventoryV1,
				Reason: governance.ReasonNoData},
			wantErr: true,
		},
	}
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := targetRequestForTest(t, now, test.target).ValidateResult(test.result)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateResult() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestCoordinatorPassesRuntimeTargetCapabilityToClaimer(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	for _, enabled := range []bool{false, true} {
		store := &coordinatorStoreForTest{request: targetRequestForTest(t, now, governance.TargetRuntime)}
		coordinator := newCoordinatorForTest(t, now, store, TargetHandlers{
			governance.TargetRuntime: targetHandlerFunc(func(context.Context, deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
				return deletiondomain.TargetResult{Disposition: deletiondomain.DispositionRetry, Reason: governance.ReasonTargetUnavailable}, nil
			}),
		}, enabled)

		if _, err := coordinator.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce(runtime enabled %t) error = %v", enabled, err)
		}
		if store.runtimeTargetEnabled != enabled {
			t.Fatalf("claimer runtime capability = %t, want %t", store.runtimeTargetEnabled, enabled)
		}
	}
}

func newCoordinatorForTest(
	t *testing.T,
	now time.Time,
	store *coordinatorStoreForTest,
	handlers TargetHandlers,
	runtimeTargetEnabled ...bool,
) *Coordinator {
	t.Helper()
	enabled := len(runtimeTargetEnabled) == 1 && runtimeTargetEnabled[0]
	coordinator, err := NewCoordinator(CoordinatorConfig{
		RuntimeTargetEnabled: enabled,
		Claims:               store,
		Store:                store,
		Handlers:             handlers,
		Clock:                fixedCoordinatorClock{now: now},
		Entropy:              bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64)),
		RetryDelay: func(uint32, governance.Digest) time.Duration {
			return 17 * time.Second
		},
		HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	return coordinator
}

func targetRequestForTest(t *testing.T, now time.Time, target governance.DeletionTarget) deletiondomain.TargetRequest {
	t.Helper()
	vector, err := governance.ProjectDeletionVector(governance.SourceGenerationVector{
		ProjectID: coordinatorProjectID, ProjectGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := governance.PresentPurgeDeadline(now.Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return deletiondomain.TargetRequest{
		Claim: targetClaimForTest(now, target),
		Tombstone: deletiondomain.Tombstone{
			ProjectID: coordinatorProjectID,
			Identity: governance.DeletionTombstoneInput{
				TombstoneID:    coordinatorTombstoneID,
				Origin:         governance.DeletionOriginGovernedOnline,
				Mode:           governance.PurgeModeDerivedGeneration,
				SelectedVector: vector,
				FencedAt:       now,
				Deadline:       deadline,
			},
			Status: deletiondomain.TombstoneProcessing,
		},
		Attempt: 1,
	}
}

func targetClaimForTest(now time.Time, targets ...governance.DeletionTarget) deletiondomain.TargetClaim {
	target := governance.TargetRuntime
	if len(targets) > 0 {
		target = targets[0]
	}
	var digest governance.Digest
	for index := range digest {
		digest[index] = byte(index + 1)
	}
	return deletiondomain.TargetClaim{
		ProjectID:           coordinatorProjectID,
		TombstoneID:         coordinatorTombstoneID,
		Target:              target,
		ClaimIncarnation:    1,
		LeaseOwnerDigest:    digest,
		ObservedLeaseExpiry: now.Add(5 * time.Minute),
	}
}

type fixedCoordinatorClock struct{ now time.Time }

func (clock fixedCoordinatorClock) Now() time.Time { return clock.now }

type targetHandlerFunc func(context.Context, deletiondomain.TargetRequest) (deletiondomain.TargetResult, error)

func (handler targetHandlerFunc) Apply(ctx context.Context, request deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
	return handler(ctx, request)
}

type coordinatorStoreForTest struct {
	mu                   sync.Mutex
	request              deletiondomain.TargetRequest
	heartbeatErr         error
	completeResponseLost bool
	receipt              deletiondomain.TargetReceipt
	failure              deletiondomain.TargetFailure
	completeCalls        int
	lookupCalls          int
	failureCalls         int
	runtimeTargetEnabled bool
}

func (store *coordinatorStoreForTest) ClaimNextDeletionTargetTaskForWorker(
	_ context.Context,
	ownerDigest governance.Digest,
	runtimeTargetEnabled bool,
) (deletiondomain.ClaimedTarget, error) {
	store.runtimeTargetEnabled = runtimeTargetEnabled
	store.request.Claim.LeaseOwnerDigest = ownerDigest
	return deletiondomain.ClaimedTarget{Claim: store.request.Claim, Attempt: store.request.Attempt, State: deletiondomain.TargetProcessing}, nil
}

func (store *coordinatorStoreForTest) LoadDeletionTargetRequestForClaim(
	_ context.Context,
	_ deletiondomain.TargetClaim,
) (deletiondomain.TargetRequest, error) {
	return store.request, nil
}

func (store *coordinatorStoreForTest) HeartbeatDeletionTargetClaim(context.Context, deletiondomain.TargetClaim) (time.Time, error) {
	if store.heartbeatErr != nil {
		return time.Time{}, store.heartbeatErr
	}
	return store.request.Claim.ObservedLeaseExpiry.Add(5 * time.Minute), nil
}

func (store *coordinatorStoreForTest) CompleteDeletionTargetForClaim(
	_ context.Context,
	claim deletiondomain.TargetClaim,
	result deletiondomain.TargetResult,
) (deletiondomain.TargetReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.completeCalls++
	receipt, err := deletiondomain.NewTargetReceipt(store.request, result, time.Date(2026, 7, 13, 10, 0, 1, 0, time.UTC))
	if err != nil {
		return deletiondomain.TargetReceipt{}, err
	}
	store.receipt = receipt
	if store.completeResponseLost {
		return deletiondomain.TargetReceipt{}, errors.New("response lost after commit")
	}
	return receipt, nil
}

func (store *coordinatorStoreForTest) FailDeletionTargetForClaim(
	_ context.Context,
	_ deletiondomain.TargetClaim,
	failure deletiondomain.TargetFailure,
) error {
	store.failureCalls++
	store.failure = failure
	return nil
}

func (store *coordinatorStoreForTest) FindCommittedDeletionTargetReceipt(
	_ context.Context,
	_ deletiondomain.TargetClaim,
) (deletiondomain.TargetReceipt, error) {
	store.lookupCalls++
	if store.receipt.Digest == (governance.Digest{}) {
		return deletiondomain.TargetReceipt{}, deletiondomain.ErrDeletionReceiptUnavailable
	}
	return store.receipt, nil
}
