package jobstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/retention"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (authority *WorkerClaimAuthority) ClaimNextSourceHardPurgeForWorker(
	ctx context.Context,
) (retention.SourceHardPurgeClaim, error) {
	if authority == nil || authority.queryer == nil {
		return retention.SourceHardPurgeClaim{}, retention.ErrClaimAuthorityRequired
	}
	query := `
		SELECT project_id, entity_kind, entity_id
		FROM canonkeeper_worker.claim_next_source_hard_purge_for_worker()
	`
	var projectID, entityID pgtype.UUID
	var entityKind string
	err := authority.queryer.QueryRow(ctx, query).Scan(&projectID, &entityKind, &entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return retention.SourceHardPurgeClaim{}, retention.ErrNoWork
	}
	if err != nil {
		return retention.SourceHardPurgeClaim{}, err
	}
	claim := retention.SourceHardPurgeClaim{
		ProjectID: projectID.String(), EntityKind: governance.SourceEntityKind(entityKind), EntityID: entityID.String(),
	}
	if claim.Validate() != nil {
		return retention.SourceHardPurgeClaim{}, retention.ErrInvalidClaim
	}
	return claim, nil
}

func (authority *WorkerClaimAuthority) ClaimNextDeletionAuditGCForWorker(
	ctx context.Context,
) (retention.DeletionAuditGCClaim, error) {
	if authority == nil || authority.queryer == nil {
		return retention.DeletionAuditGCClaim{}, retention.ErrClaimAuthorityRequired
	}
	query := `
		SELECT project_id, tombstone_id
		FROM canonkeeper_worker.claim_next_deletion_audit_gc_for_worker()
	`
	var projectID, tombstoneID pgtype.UUID
	err := authority.queryer.QueryRow(ctx, query).Scan(&projectID, &tombstoneID)
	if errors.Is(err, pgx.ErrNoRows) {
		return retention.DeletionAuditGCClaim{}, retention.ErrNoWork
	}
	if err != nil {
		return retention.DeletionAuditGCClaim{}, err
	}
	claim := retention.DeletionAuditGCClaim{
		ProjectID: projectID.String(), TombstoneID: tombstoneID.String(),
	}
	if claim.Validate() != nil {
		return retention.DeletionAuditGCClaim{}, retention.ErrInvalidClaim
	}
	return claim, nil
}
