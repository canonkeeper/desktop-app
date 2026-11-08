package retentionstore

import (
	"context"
	"testing"
	"time"

	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

func seedRetentionPurgedPermissionLossForTest(t *testing.T, db *storedb.DB, now time.Time) {
	t.Helper()
	withRetentionReplicaRole(t, db, func(ctx context.Context, conn pgx.Tx) {
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_tombstones (
			  id, project_id, origin, entity_kind, purge_mode, entity_id,
			  permission_loss_actor_user_id, project_generation, status,
			  fenced_at, purge_deadline, purged_at
			) VALUES ($1, $2, 'governed_online', 'project', 'derived_generation', $2,
			          $3, 1, 'purged', $4::timestamptz - interval '8 days 1 hour',
			          $4::timestamptz - interval '7 days 1 hour', $4::timestamptz - interval '7 days')
		`, retentionPermissionID, retentionProjectID, retentionUserID, now)
		mustRetentionExec(t, ctx, conn, `
			WITH tombstone AS MATERIALIZED (
			  SELECT *,
			    ck_deletion_tombstone_digest(
			      id, origin, purge_mode, entity_kind, project_id, project_generation,
			      book_id, book_generation, chapter_id, chapter_generation,
			      chat_session_id, chat_lifecycle_epoch, fenced_at, purge_deadline,
			      permission_loss_actor_user_id) AS tombstone_digest,
			    ck_deletion_selected_vector_digest(
			      entity_kind, project_id, project_generation, book_id, book_generation,
			      chapter_id, chapter_generation, chat_session_id,
			      chat_lifecycle_epoch) AS vector_digest
			  FROM deletion_tombstones
			  WHERE id = $1 AND project_id = $2
			), target(target, owner_digest, handler_version) AS (VALUES
			  ('runtime', decode(repeat('51',32),'hex'), 'runtime_direct_sdk_v1'),
			  ('filesystem', decode(repeat('61',32),'hex'), 'filesystem_os_root_v1'),
			  ('postgresql', decode(repeat('71',32),'hex'), 'postgres_exact_inventory_v1')
			)
			INSERT INTO deletion_target_tasks (
			  tombstone_id, project_id, target, state, attempts, claim_incarnation,
			  next_attempt_at, claimed_by_digest, claim_expires_at, handler_version,
			  receipt_digest, completed_at
			)
			SELECT tombstone.id, tombstone.project_id, target.target, 'no_data', 1, 1,
			  $3, target.owner_digest, $3, target.handler_version,
			  ck_deletion_target_receipt_digest(
			    target.target, tombstone.tombstone_digest, tombstone.vector_digest,
			    target.handler_version, 'no_data', $3,
			    tombstone.permission_loss_actor_user_id),
			  $3
			FROM tombstone CROSS JOIN target
		`, retentionPermissionID, retentionProjectID, now.Add(-7*24*time.Hour).Truncate(time.Second))
		mustRetentionExec(t, ctx, conn, `
			INSERT INTO deletion_completion_receipts (
			  tombstone_id, project_id, vector_digest, target_receipts_digest,
			  completed_at, audit_expires_at, schema_version
			)
			SELECT tombstone.id, tombstone.project_id,
			  ck_deletion_selected_vector_digest(
			    tombstone.entity_kind, tombstone.project_id, tombstone.project_generation,
			    tombstone.book_id, tombstone.book_generation, tombstone.chapter_id,
			    tombstone.chapter_generation, tombstone.chat_session_id,
			    tombstone.chat_lifecycle_epoch),
			  ck_deletion_target_receipt_set_digest(
			    tombstone.purge_mode,
			    ck_deletion_tombstone_digest(
			      tombstone.id, tombstone.origin, tombstone.purge_mode, tombstone.entity_kind,
			      tombstone.project_id, tombstone.project_generation, tombstone.book_id,
			      tombstone.book_generation, tombstone.chapter_id, tombstone.chapter_generation,
			      tombstone.chat_session_id, tombstone.chat_lifecycle_epoch,
			      tombstone.fenced_at, tombstone.purge_deadline,
			      tombstone.permission_loss_actor_user_id),
			    ck_deletion_selected_vector_digest(
			      tombstone.entity_kind, tombstone.project_id, tombstone.project_generation,
			      tombstone.book_id, tombstone.book_generation, tombstone.chapter_id,
			      tombstone.chapter_generation, tombstone.chat_session_id,
			      tombstone.chat_lifecycle_epoch),
			    ARRAY(
			      SELECT task.receipt_digest
			      FROM deletion_target_tasks AS task
			      WHERE task.tombstone_id = tombstone.id
			        AND task.project_id = tombstone.project_id
			      ORDER BY CASE task.target
			        WHEN 'runtime' THEN 1 WHEN 'filesystem' THEN 2 WHEN 'postgresql' THEN 4
			      END)),
			  $3, NULL, 'CKDeletionReceiptV1'
			FROM deletion_tombstones AS tombstone
			WHERE tombstone.id = $1 AND tombstone.project_id = $2
		`, retentionPermissionID, retentionProjectID, now.Add(-7*24*time.Hour).Truncate(time.Second))
	})
}
