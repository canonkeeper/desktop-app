package deletion

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

const (
	coordinatorProjectID   = "11111111-1111-4111-8111-111111111111"
	coordinatorTombstoneID = "22222222-2222-4222-8222-222222222222"
)

func targetRequestForTest(t *testing.T, now time.Time, target governance.DeletionTarget) TargetRequest {
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
	return TargetRequest{
		Claim: targetClaimForTest(now, target),
		Tombstone: Tombstone{
			ProjectID: coordinatorProjectID,
			Identity: governance.DeletionTombstoneInput{
				TombstoneID: coordinatorTombstoneID, Origin: governance.DeletionOriginGovernedOnline,
				Mode: governance.PurgeModeDerivedGeneration, SelectedVector: vector, FencedAt: now, Deadline: deadline,
			},
			Status: TombstoneProcessing,
		},
		Attempt: 1,
	}
}

func targetClaimForTest(now time.Time, targets ...governance.DeletionTarget) TargetClaim {
	target := governance.TargetRuntime
	if len(targets) > 0 {
		target = targets[0]
	}
	var digest governance.Digest
	for index := range digest {
		digest[index] = byte(index + 1)
	}
	return TargetClaim{
		ProjectID: coordinatorProjectID, TombstoneID: coordinatorTombstoneID, Target: target,
		ClaimIncarnation: 1, LeaseOwnerDigest: digest, ObservedLeaseExpiry: now.Add(5 * time.Minute),
	}
}
