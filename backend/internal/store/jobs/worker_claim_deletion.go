package jobstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (authority *WorkerClaimAuthority) ClaimNextDeletionTargetTaskForWorker(
	ctx context.Context,
	ownerDigest governance.Digest,
	runtimeTargetEnabled bool,
) (deletion.ClaimedTarget, error) {
	if authority == nil || authority.queryer == nil || ownerDigest == (governance.Digest{}) {
		return deletion.ClaimedTarget{}, deletion.ErrInvalidTargetRequest
	}

	query := `
		SELECT project_id, tombstone_id, target, state, attempt,
		       claim_incarnation, lease_owner_digest, lease_expires_at
		FROM canonkeeper_worker.claim_next_deletion_target_task_for_worker($1, $2, $3)
	`

	var (
		projectID      pgtype.UUID
		tombstoneID    pgtype.UUID
		target         string
		state          string
		attempt        int32
		incarnation    int64
		persistedOwner []byte
		leaseExpiresAt pgtype.Timestamptz
	)
	err := authority.queryer.QueryRow(
		ctx,
		query,
		ownerDigest[:],
		int32(deletion.DeletionTargetLeaseTTL.Seconds()),
		runtimeTargetEnabled,
	).Scan(
		&projectID,
		&tombstoneID,
		&target,
		&state,
		&attempt,
		&incarnation,
		&persistedOwner,
		&leaseExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return deletion.ClaimedTarget{}, deletion.ErrNoDeletionTarget
	}
	if err != nil {
		return deletion.ClaimedTarget{}, err
	}
	if attempt <= 0 || incarnation <= 0 || len(persistedOwner) != len(ownerDigest) || !leaseExpiresAt.Valid {
		return deletion.ClaimedTarget{}, deletion.ErrInvalidTargetRequest
	}

	var storedOwner governance.Digest
	copy(storedOwner[:], persistedOwner)
	claimed := deletion.ClaimedTarget{
		Claim: deletion.TargetClaim{
			ProjectID:           projectID.String(),
			TombstoneID:         tombstoneID.String(),
			Target:              governance.DeletionTarget(target),
			ClaimIncarnation:    uint64(incarnation),
			LeaseOwnerDigest:    storedOwner,
			ObservedLeaseExpiry: leaseExpiresAt.Time.UTC(),
		},
		Attempt: uint32(attempt),
		State:   deletion.TargetState(state),
	}
	if claimed.Claim.LeaseOwnerDigest != ownerDigest || claimed.Validate(time.Now().UTC()) != nil {
		return deletion.ClaimedTarget{}, deletion.ErrInvalidTargetRequest
	}
	return claimed, nil
}
