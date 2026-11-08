package deletion

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

func TestNativeTargetDeletesExactRootSelectors(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	tests := []struct {
		name       string
		candidates []deletiondomain.RootCandidate
		deleted    []string
		want       deletiondomain.Disposition
	}{
		{name: "no matching product roots", want: deletiondomain.DispositionNoData},
		{
			name: "chat without a bound root",
			candidates: []deletiondomain.RootCandidate{{
				ProjectID: "10000000-0000-4000-8000-000000000001",
				Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat-0"},
			}},
			want: deletiondomain.DispositionNoData,
		},
		{
			name: "bound chat roots",
			candidates: []deletiondomain.RootCandidate{
				{
					ProjectID: "10000000-0000-4000-8000-000000000001",
					Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat-2", ThreadID: "root-job"},
				},
				{
					ProjectID: "10000000-0000-4000-8000-000000000001",
					Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat-1", ThreadID: "root-chat"},
				},
			},
			deleted: []string{"root-job", "root-chat"},
			want:    deletiondomain.DispositionPurged,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := &nativeRootLookupStub{deletionCandidates: test.candidates}
			deleter := &nativeTreeDeleterStub{deletedBySelector: map[deletiondomain.RootSelector]string{
				{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat-2", ThreadID: "root-job"}:  "root-job",
				{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat-1", ThreadID: "root-chat"}: "root-chat",
			}}
			target, err := NewNativeTarget(lookup, deleter)
			if err != nil {
				t.Fatal(err)
			}
			result, err := target.Apply(t.Context(), nativeTargetRequest(t, now))
			if err != nil {
				t.Fatal(err)
			}
			if result.Disposition != test.want || !reflect.DeepEqual(deleter.deleted, test.deleted) {
				t.Fatalf("result/deleted = %#v/%v, want %q/%v", result, deleter.deleted, test.want, test.deleted)
			}
		})
	}
}

func TestNativeTargetConflictRemainsRetryable(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	conflict := errors.New("multiple native roots")
	lookup := &nativeRootLookupStub{deletionCandidates: []deletiondomain.RootCandidate{{
		ProjectID: "10000000-0000-4000-8000-000000000001",
		Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "job-1"},
	}}}
	deleter := &nativeTreeDeleterStub{err: conflict}
	target, err := NewNativeTarget(lookup, deleter)
	if err != nil {
		t.Fatal(err)
	}
	request := nativeTargetRequest(t, now)
	store := newCoordinatorStoreStub(request)
	coordinator, err := NewCoordinator(CoordinatorConfig{
		RuntimeTargetEnabled: true,
		Claims:               store,
		Store:                store,
		Handlers:             TargetHandlers{governance.TargetRuntime: target},
		Clock:                fixedClock{now: now},
		Entropy:              repeatingReader(0x5a),
		RetryDelay:           func(uint32, governance.Digest) time.Duration { return time.Second },
		HeartbeatInterval:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := coordinator.RunOnce(t.Context())
	if err != nil || outcome != RunRetryScheduled || store.failure.Reason != governance.ReasonValidationFailed {
		t.Fatalf("RunOnce() = %q, %v, failure %#v", outcome, err, store.failure)
	}
}

func TestNativeTargetReleasesChatBindingAfterDelete(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := deletiondomain.RootCandidate{
		ProjectID: "10000000-0000-4000-8000-000000000001",
		Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "chat-1", ThreadID: "root-chat"},
	}
	events := []string{}
	lookup := &nativeRootLookupStub{
		deletionCandidates: []deletiondomain.RootCandidate{candidate},
		events:             &events,
	}
	deleter := &nativeTreeDeleterStub{
		deletedBySelector: map[deletiondomain.RootSelector]string{candidate.Selector: "root-chat"},
		events:            &events,
	}
	target, err := NewNativeTarget(lookup, deleter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Apply(t.Context(), nativeTargetRequest(t, now)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"delete:root-chat", "release:root-chat"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestCoordinatorRecoversPermissionLossIntent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	request := nativeTargetRequest(t, now)
	request.Tombstone.Identity.PermissionLossActorUserID = "20000000-0000-4000-8000-000000000001"
	candidate := deletiondomain.RootCandidate{
		ProjectID: request.Claim.ProjectID,
		Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "job-actor", ThreadID: "root-actor"},
	}
	lookup := &nativeRootLookupStub{
		deletionCandidates: []deletiondomain.RootCandidate{{
			ProjectID: request.Claim.ProjectID,
			Selector:  deletiondomain.RootSelector{Flow: agentdomain.FlowResearchChat, CorrelationID: "wrong-job", ThreadID: "wrong-root"},
		}},
		permissionCandidates: []deletiondomain.RootCandidate{candidate},
	}
	deleter := &nativeTreeDeleterStub{deletedBySelector: map[deletiondomain.RootSelector]string{
		candidate.Selector: "root-actor",
	}}
	target, err := NewNativeTarget(lookup, deleter)
	if err != nil {
		t.Fatal(err)
	}
	store := &coordinatorStoreForTest{request: request}
	coordinator := newCoordinatorForTest(t, now, store, TargetHandlers{
		governance.TargetRuntime: target,
	}, true)

	outcome, err := coordinator.RunOnce(t.Context())
	if err != nil || outcome != RunCompleted {
		t.Fatalf("RunOnce() = %q, %v", outcome, err)
	}
	if !reflect.DeepEqual(deleter.deleted, []string{"root-actor"}) {
		t.Fatalf("deleted roots = %v, want recovered actor root", deleter.deleted)
	}
}

type nativeRootLookupStub struct {
	deletionCandidates   []deletiondomain.RootCandidate
	permissionCandidates []deletiondomain.RootCandidate
	events               *[]string
}

func (stub *nativeRootLookupStub) ListDeletionRoots(context.Context, deletiondomain.Tombstone) ([]deletiondomain.RootCandidate, error) {
	return append([]deletiondomain.RootCandidate(nil), stub.deletionCandidates...), nil
}

func (stub *nativeRootLookupStub) ListPermissionLossRoots(context.Context, deletiondomain.PermissionLossScope) ([]deletiondomain.RootCandidate, error) {
	return append([]deletiondomain.RootCandidate(nil), stub.permissionCandidates...), nil
}

func (stub *nativeRootLookupStub) ReleaseDeletedRoot(_ context.Context, _ deletiondomain.RootCandidate, rootID string) error {
	if stub.events != nil {
		*stub.events = append(*stub.events, "release:"+rootID)
	}
	return nil
}

type nativeTreeDeleterStub struct {
	deletedBySelector map[deletiondomain.RootSelector]string
	deleted           []string
	err               error
	events            *[]string
}

func (stub *nativeTreeDeleterStub) DeleteTree(_ context.Context, selector deletiondomain.RootSelector) (string, error) {
	if stub.err != nil {
		return "", stub.err
	}
	root := stub.deletedBySelector[selector]
	if stub.events != nil {
		*stub.events = append(*stub.events, "delete:"+root)
	}
	if root != "" {
		stub.deleted = append(stub.deleted, root)
	}
	return root, nil
}

func nativeTargetRequest(t *testing.T, now time.Time) deletiondomain.TargetRequest {
	t.Helper()
	vector, err := governance.ProjectDeletionVector(governance.SourceGenerationVector{
		ProjectID: "10000000-0000-4000-8000-000000000001", ProjectGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := governance.PresentPurgeDeadline(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var owner governance.Digest
	owner[0] = 1
	return deletiondomain.TargetRequest{
		Claim: deletiondomain.TargetClaim{
			ProjectID: "10000000-0000-4000-8000-000000000001", TombstoneID: "30000000-0000-4000-8000-000000000001",
			Target: governance.TargetRuntime, ClaimIncarnation: 1, LeaseOwnerDigest: owner,
			ObservedLeaseExpiry: now.Add(time.Minute),
		},
		Tombstone: deletiondomain.Tombstone{
			ProjectID: "10000000-0000-4000-8000-000000000001",
			Identity: governance.DeletionTombstoneInput{
				TombstoneID: "30000000-0000-4000-8000-000000000001", Origin: governance.DeletionOriginGovernedOnline,
				Mode: governance.PurgeModeDerivedGeneration, SelectedVector: vector, FencedAt: now, Deadline: deadline,
			},
			Status: deletiondomain.TombstoneProcessing,
		},
		Attempt: 1,
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type byteReader byte

func repeatingReader(value byte) byteReader { return byteReader(value) }

func (reader byteReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(reader)
	}
	return len(buffer), nil
}

type coordinatorStoreStub struct {
	request deletiondomain.TargetRequest
	failure deletiondomain.TargetFailure
}

func newCoordinatorStoreStub(request deletiondomain.TargetRequest) *coordinatorStoreStub {
	return &coordinatorStoreStub{request: request}
}

func (stub *coordinatorStoreStub) ClaimNextDeletionTargetTaskForWorker(_ context.Context, owner governance.Digest, _ bool) (deletiondomain.ClaimedTarget, error) {
	stub.request.Claim.LeaseOwnerDigest = owner
	return deletiondomain.ClaimedTarget{Claim: stub.request.Claim, Attempt: stub.request.Attempt, State: deletiondomain.TargetProcessing}, nil
}

func (stub *coordinatorStoreStub) LoadDeletionTargetRequestForClaim(context.Context, deletiondomain.TargetClaim) (deletiondomain.TargetRequest, error) {
	return stub.request, nil
}

func (stub *coordinatorStoreStub) HeartbeatDeletionTargetClaim(context.Context, deletiondomain.TargetClaim) (time.Time, error) {
	return stub.request.Claim.ObservedLeaseExpiry, nil
}

func (stub *coordinatorStoreStub) CompleteDeletionTargetForClaim(context.Context, deletiondomain.TargetClaim, deletiondomain.TargetResult) (deletiondomain.TargetReceipt, error) {
	return deletiondomain.TargetReceipt{}, errors.New("unexpected completion")
}

func (stub *coordinatorStoreStub) FailDeletionTargetForClaim(_ context.Context, _ deletiondomain.TargetClaim, failure deletiondomain.TargetFailure) error {
	stub.failure = failure
	return nil
}

func (stub *coordinatorStoreStub) FindCommittedDeletionTargetReceipt(context.Context, deletiondomain.TargetClaim) (deletiondomain.TargetReceipt, error) {
	return deletiondomain.TargetReceipt{}, deletiondomain.ErrDeletionReceiptUnavailable
}
