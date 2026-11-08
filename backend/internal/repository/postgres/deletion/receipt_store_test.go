package deletion

import (
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestDeletionTombstoneFieldsLoadPermissionLossActor(t *testing.T) {
	const (
		projectID   = "10000000-0000-4000-8000-000000000001"
		tombstoneID = "20000000-0000-4000-8000-000000000001"
		actorUserID = "30000000-0000-4000-8000-000000000001"
	)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	tombstone, err := deletionTombstoneFromFields(deletionTombstoneFields{
		TombstoneID: mustTestUUID(t, tombstoneID), ProjectID: mustTestUUID(t, projectID),
		Origin: string(governance.DeletionOriginGovernedOnline), EntityKind: string(governance.DeletionSelectionProject),
		PurgeMode: string(governance.PurgeModeDerivedGeneration), PermissionLossActorUserID: mustTestUUID(t, actorUserID),
		ProjectGeneration: pgtype.Int8{Int64: 1, Valid: true}, TombstoneStatus: string(deletiondomain.TombstoneProcessing),
		FencedAt: storedb.Timestamptz(now), PurgeDeadline: storedb.Timestamptz(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tombstone.Identity.PermissionLossActorUserID != actorUserID {
		t.Fatalf("permission-loss actor = %q, want %q", tombstone.Identity.PermissionLossActorUserID, actorUserID)
	}
}

func TestPersistedPermissionLossNoDataReceiptReplaysDigest(t *testing.T) {
	const (
		projectID   = "10000000-0000-4000-8000-000000000002"
		tombstoneID = "20000000-0000-4000-8000-000000000002"
		actorUserID = "30000000-0000-4000-8000-000000000002"
	)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	tombstone, err := deletionTombstoneFromFields(deletionTombstoneFields{
		TombstoneID: mustTestUUID(t, tombstoneID), ProjectID: mustTestUUID(t, projectID),
		Origin: string(governance.DeletionOriginGovernedOnline), EntityKind: string(governance.DeletionSelectionProject),
		PurgeMode: string(governance.PurgeModeDerivedGeneration), PermissionLossActorUserID: mustTestUUID(t, actorUserID),
		ProjectGeneration: pgtype.Int8{Int64: 1, Valid: true}, TombstoneStatus: string(deletiondomain.TombstonePurged),
		FencedAt: storedb.Timestamptz(now), PurgeDeadline: storedb.Timestamptz(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	var owner governance.Digest
	owner[0] = 1
	request := deletiondomain.TargetRequest{
		Claim: deletiondomain.TargetClaim{
			ProjectID: projectID, TombstoneID: tombstoneID, Target: governance.TargetFilesystem,
			ClaimIncarnation: 1, LeaseOwnerDigest: owner,
			ObservedLeaseExpiry: now.Add(time.Minute),
		},
		Tombstone: tombstone, Attempt: 1,
	}
	result := deletiondomain.TargetResult{
		Disposition:    deletiondomain.DispositionNoData,
		HandlerVersion: governance.HandlerFilesystemOSRootV1,
		Reason:         governance.ReasonNoData,
	}
	receipt, err := deletiondomain.NewTargetReceipt(request, result, now)
	if err != nil {
		t.Fatal(err)
	}
	task := sqlc.GetDeletionTargetTaskForCompletionRow{
		State: string(deletiondomain.TargetNoData), Attempt: 1, ClaimIncarnation: 1,
		LeaseOwnerDigest: owner[:], LeaseExpiresAt: storedb.Timestamptz(now.Add(time.Minute)),
		HandlerVersion: pgtype.Text{String: string(result.HandlerVersion), Valid: true},
		ReceiptDigest:  receipt.Digest[:], CompletedAt: storedb.Timestamptz(now),
	}

	persistedReceipt, persistedResult, err := persistedDeletionTargetReceipt(request, task)
	if err != nil {
		t.Fatal(err)
	}
	if persistedReceipt != receipt || persistedResult != result {
		t.Fatalf("persisted receipt/result = %#v, %#v; want %#v, %#v", persistedReceipt, persistedResult, receipt, result)
	}
}

func mustTestUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := storedb.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
