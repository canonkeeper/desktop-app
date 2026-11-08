package deletion

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

func TestNewTargetReceiptUsesCanonicalGovernanceDigest(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetFilesystem)
	result := TargetResult{
		Disposition:    DispositionPurged,
		HandlerVersion: governance.HandlerFilesystemOSRootV1,
		Reason:         governance.ReasonPurged,
	}
	completedAt := now.Add(time.Second)

	receipt, err := NewTargetReceipt(request, result, completedAt)
	if err != nil {
		t.Fatalf("NewTargetReceipt() error = %v", err)
	}
	tombstoneDigest, err := governance.DeletionTombstoneDigest(request.Tombstone.Identity)
	if err != nil {
		t.Fatal(err)
	}
	vectorDigest, err := governance.SelectedVectorDigest(request.Tombstone.Identity.SelectedVector)
	if err != nil {
		t.Fatal(err)
	}
	want, err := governance.DeletionTargetReceiptDigest(governance.DeletionTargetReceiptInput{
		Target:               governance.TargetFilesystem,
		TombstoneDigest:      tombstoneDigest,
		SelectedVectorDigest: vectorDigest,
		HandlerVersion:       governance.HandlerFilesystemOSRootV1,
		Disposition:          governance.ReceiptPurged,
		CompletedAt:          completedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Digest != governance.Digest(want) {
		t.Fatalf("receipt digest = %x, want %x", receipt.Digest, want)
	}
	if err := receipt.ValidateFor(request, result); err != nil {
		t.Fatalf("ValidateFor() error = %v", err)
	}
}

func TestNewTargetReceiptRejectsNoncanonicalCompletionTime(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetFilesystem)
	result := TargetResult{
		Disposition:    DispositionPurged,
		HandlerVersion: governance.HandlerFilesystemOSRootV1,
		Reason:         governance.ReasonPurged,
	}
	if _, err := NewTargetReceipt(request, result, now.Add(time.Nanosecond)); err == nil {
		t.Fatal("NewTargetReceipt() error = nil, want non-whole-second rejection")
	}
}

func TestPermissionLossTargetReceiptBindsExactActor(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetRuntime)
	request.Tombstone.Identity.PermissionLossActorUserID = "33333333-3333-4333-8333-333333333333"
	result := TargetResult{
		Disposition:    DispositionNoData,
		HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
		Reason:         governance.ReasonNoData,
	}
	receipt, err := NewTargetReceipt(request, result, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	otherActor := request
	otherActor.Tombstone.Identity.PermissionLossActorUserID = "44444444-4444-4444-8444-444444444444"
	if err := receipt.ValidateFor(otherActor, result); err == nil {
		t.Fatal("permission-loss receipt accepted a different actor")
	}
}

func TestPermissionLossNoDataReceiptPolicyIsRequestScoped(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		target  governance.DeletionTarget
		handler governance.DeletionHandlerVersion
	}{
		{target: governance.TargetFilesystem, handler: governance.HandlerFilesystemOSRootV1},
		{target: governance.TargetPostgreSQL, handler: governance.HandlerPostgresExactInventoryV1},
	}
	for _, test := range tests {
		t.Run(string(test.target), func(t *testing.T) {
			request := targetRequestForTest(t, now, test.target)
			request.Tombstone.Identity.PermissionLossActorUserID = "33333333-3333-4333-8333-333333333333"
			result := TargetResult{
				Disposition: DispositionNoData, HandlerVersion: test.handler, Reason: governance.ReasonNoData,
			}
			if _, err := NewTargetReceipt(request, result, now.Add(time.Second)); err != nil {
				t.Fatalf("permission-loss no_data receipt: %v", err)
			}
			request.Tombstone.Identity.PermissionLossActorUserID = ""
			if _, err := NewTargetReceipt(request, result, now.Add(time.Second)); err == nil {
				t.Fatal("ordinary tombstone accepted external no_data receipt")
			}
		})
	}
}
