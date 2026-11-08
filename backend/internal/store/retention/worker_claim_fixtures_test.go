package retentionstore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/retention"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// workerClaimSchemaForTest возвращает изолированную worker-схему текущего теста.
func workerClaimSchemaForTest(t *testing.T, db *storedb.DB) string {
	t.Helper()
	var productSchema string
	if err := db.Pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&productSchema); err != nil {
		t.Fatal(err)
	}
	return pgx.Identifier{productSchema + "_worker"}.Sanitize()
}

func claimNextSourceHardPurgeForTest(t *testing.T, db *storedb.DB) (retention.SourceHardPurgeClaim, error) {
	t.Helper()
	var projectID, entityID pgtype.UUID
	var entityKind string
	err := db.Pool.QueryRow(context.Background(), `
		SELECT project_id, entity_kind, entity_id
		FROM `+workerClaimSchemaForTest(t, db)+`.claim_next_source_hard_purge_for_worker()
	`).Scan(&projectID, &entityKind, &entityID)
	if err != nil {
		return retention.SourceHardPurgeClaim{}, err
	}
	return retention.SourceHardPurgeClaim{
		ProjectID: projectID.String(), EntityKind: governance.SourceEntityKind(entityKind), EntityID: entityID.String(),
	}, nil
}

func claimNextDeletionAuditGCForTest(t *testing.T, db *storedb.DB) (retention.DeletionAuditGCClaim, error) {
	t.Helper()
	var projectID, tombstoneID pgtype.UUID
	err := db.Pool.QueryRow(context.Background(), `
		SELECT project_id, tombstone_id
		FROM `+workerClaimSchemaForTest(t, db)+`.claim_next_deletion_audit_gc_for_worker()
	`).Scan(&projectID, &tombstoneID)
	if err != nil {
		return retention.DeletionAuditGCClaim{}, err
	}
	return retention.DeletionAuditGCClaim{ProjectID: projectID.String(), TombstoneID: tombstoneID.String()}, nil
}
