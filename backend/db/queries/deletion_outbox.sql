-- name: LoadDeletionTargetRequestForClaim :one
SELECT
  tombstone.id AS tombstone_id,
  tombstone.project_id,
  tombstone.origin,
  tombstone.entity_kind,
  tombstone.purge_mode,
  tombstone.permission_loss_actor_user_id,
  tombstone.project_generation,
  tombstone.book_id,
  tombstone.book_generation,
  tombstone.chapter_id,
  tombstone.chapter_generation,
  tombstone.chat_session_id,
  tombstone.chat_lifecycle_epoch,
  tombstone.status AS tombstone_status,
  tombstone.fenced_at,
  tombstone.purge_deadline,
  task.target,
  task.state AS target_state,
  task.attempts AS attempt,
  task.claim_incarnation,
  task.claimed_by_digest AS lease_owner_digest,
  task.claim_expires_at AS lease_expires_at
FROM deletion_target_tasks AS task
JOIN deletion_tombstones AS tombstone
  ON tombstone.id = task.tombstone_id
 AND tombstone.project_id = task.project_id
WHERE task.project_id = sqlc.arg(project_id)::uuid
  AND task.tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND task.target = sqlc.arg(target)::text
  AND task.state = 'processing'
  AND task.claim_incarnation = sqlc.arg(claim_incarnation)::bigint
  AND task.claimed_by_digest = sqlc.arg(lease_owner_digest)::bytea
  AND task.claim_expires_at > clock_timestamp()
  AND tombstone.origin = 'governed_online';

-- name: HeartbeatDeletionTargetClaim :one
UPDATE deletion_target_tasks AS task
SET claim_expires_at = clock_timestamp() + interval '5 minutes'
FROM deletion_tombstones AS tombstone
WHERE task.project_id = sqlc.arg(project_id)::uuid
  AND task.tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND task.target = sqlc.arg(target)::text
  AND task.state = 'processing'
  AND task.claim_incarnation = sqlc.arg(claim_incarnation)::bigint
  AND task.claimed_by_digest = sqlc.arg(lease_owner_digest)::bytea
  AND task.claim_expires_at > clock_timestamp()
  AND tombstone.id = task.tombstone_id
  AND tombstone.project_id = task.project_id
  AND tombstone.origin = 'governed_online'
RETURNING task.claim_expires_at;

-- name: FailDeletionTargetForClaim :one
WITH db_clock AS MATERIALIZED (
  SELECT clock_timestamp() AS now_at
), retry AS MATERIALIZED (
  SELECT sqlc.arg(retry_after_milliseconds)::bigint AS milliseconds
  WHERE sqlc.arg(retry_after_milliseconds)::bigint BETWEEN 1 AND 900000
), failed AS (
  UPDATE deletion_target_tasks AS task
  SET
    state = 'failed',
    next_attempt_at = db_clock.now_at + retry.milliseconds * interval '1 millisecond',
    claimed_by_digest = NULL,
    claim_expires_at = NULL,
    handler_version = NULL,
    receipt_digest = NULL,
    completed_at = NULL,
    last_reason_code = sqlc.arg(reason_code)::text
  FROM deletion_tombstones AS tombstone
  CROSS JOIN db_clock
  CROSS JOIN retry
  WHERE task.project_id = sqlc.arg(project_id)::uuid
    AND task.tombstone_id = sqlc.arg(tombstone_id)::uuid
    AND task.target = sqlc.arg(target)::text
    AND task.state = 'processing'
    AND task.claim_incarnation = sqlc.arg(claim_incarnation)::bigint
    AND task.claimed_by_digest = sqlc.arg(lease_owner_digest)::bytea
    AND task.claim_expires_at > db_clock.now_at
    AND tombstone.id = task.tombstone_id
    AND tombstone.project_id = task.project_id
    AND tombstone.origin = 'governed_online'
  RETURNING task.tombstone_id, task.project_id
)
UPDATE deletion_tombstones AS tombstone
SET
  status = 'failed',
  last_reason_code = sqlc.arg(reason_code)::text,
  updated_at = db_clock.now_at
FROM failed, db_clock
WHERE tombstone.id = failed.tombstone_id
  AND tombstone.project_id = failed.project_id
  AND tombstone.status IN ('pending', 'processing', 'failed')
RETURNING tombstone.id;

-- name: GetDeletionTargetTaskForCompletion :one
SELECT
  task.state,
  task.attempts AS attempt,
  task.claim_incarnation,
  task.claimed_by_digest AS lease_owner_digest,
  task.claim_expires_at AS lease_expires_at,
  task.handler_version,
  task.receipt_digest,
  task.completed_at
FROM deletion_target_tasks AS task
WHERE task.project_id = sqlc.arg(project_id)::uuid
  AND task.tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND task.target = sqlc.arg(target)::text
  AND task.claim_incarnation = sqlc.arg(claim_incarnation)::bigint
  AND task.claimed_by_digest = sqlc.arg(lease_owner_digest)::bytea
  AND (
    (task.state = 'processing' AND task.claim_expires_at > clock_timestamp())
    OR task.state IN ('succeeded', 'no_data')
  )
FOR UPDATE;

-- name: GetDeletionTombstoneForTarget :one
SELECT
  tombstone.id AS tombstone_id,
  tombstone.project_id,
  tombstone.origin,
  tombstone.entity_kind,
  tombstone.purge_mode,
  tombstone.permission_loss_actor_user_id,
  tombstone.project_generation,
  tombstone.book_id,
  tombstone.book_generation,
  tombstone.chapter_id,
  tombstone.chapter_generation,
  tombstone.chat_session_id,
  tombstone.chat_lifecycle_epoch,
  tombstone.status AS tombstone_status,
  tombstone.fenced_at,
  tombstone.purge_deadline
FROM deletion_tombstones AS tombstone
WHERE tombstone.id = sqlc.arg(tombstone_id)::uuid
  AND tombstone.project_id = sqlc.arg(project_id)::uuid
FOR UPDATE;

-- name: CurrentDeletionTimestampSecond :one
SELECT date_trunc('second', clock_timestamp())::timestamptz AS completed_at;

-- name: CompleteDeletionTargetTask :one
UPDATE deletion_target_tasks AS task
SET
  state = sqlc.arg(terminal_state)::text,
  handler_version = sqlc.arg(handler_version)::text,
  receipt_digest = sqlc.arg(receipt_digest)::bytea,
  completed_at = sqlc.arg(completed_at)::timestamptz,
  last_reason_code = NULL
WHERE task.project_id = sqlc.arg(project_id)::uuid
  AND task.tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND task.target = sqlc.arg(target)::text
  AND task.state = 'processing'
  AND task.claim_incarnation = sqlc.arg(claim_incarnation)::bigint
  AND task.claimed_by_digest = sqlc.arg(lease_owner_digest)::bytea
  AND task.claim_expires_at > clock_timestamp()
  AND (
    task.target <> 'postgresql'
    OR NOT EXISTS (
      SELECT 1
      FROM deletion_target_tasks AS sibling
      WHERE sibling.tombstone_id = task.tombstone_id
        AND sibling.project_id = task.project_id
        AND sibling.target <> 'postgresql'
        AND sibling.state NOT IN ('succeeded', 'no_data')
    )
  )
RETURNING task.tombstone_id;

-- name: CountIncompleteDeletionTargets :one
SELECT count(*)
FROM deletion_target_tasks
WHERE tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND project_id = sqlc.arg(project_id)::uuid
  AND state NOT IN ('succeeded', 'no_data');

-- name: ListDeletionTargetReceiptDigests :many
SELECT target, receipt_digest
FROM deletion_target_tasks
WHERE tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND project_id = sqlc.arg(project_id)::uuid
  AND state IN ('succeeded', 'no_data')
ORDER BY CASE target
  WHEN 'runtime' THEN 1
  WHEN 'filesystem' THEN 2
  WHEN 'postgresql' THEN 4
END;

-- name: MarkDeletionTombstonePurged :one
UPDATE deletion_tombstones AS tombstone
SET
  status = 'purged',
  purged_at = sqlc.arg(completed_at)::timestamptz,
  audit_expires_at = CASE tombstone.purge_mode
    WHEN 'derived_generation' THEN (
      SELECT hard.purged_at + interval '30 days'
      FROM deletion_tombstones AS hard
      WHERE hard.status = 'purged'
        AND ck_source_hard_purge_contains_derived(hard, tombstone)
      ORDER BY hard.purged_at, uuid_send(hard.id)
      LIMIT 1
    )
    ELSE sqlc.arg(completed_at)::timestamptz + interval '30 days'
  END,
  last_reason_code = NULL,
  updated_at = clock_timestamp()
WHERE tombstone.id = sqlc.arg(tombstone_id)::uuid
  AND tombstone.project_id = sqlc.arg(project_id)::uuid
  AND tombstone.status IN ('pending', 'processing', 'failed')
  AND NOT EXISTS (
    SELECT 1
    FROM deletion_target_tasks AS task
    WHERE task.tombstone_id = tombstone.id
      AND task.project_id = tombstone.project_id
      AND task.state NOT IN ('succeeded', 'no_data')
  )
RETURNING tombstone.audit_expires_at;

-- name: PinContainedDerivedProofRetentionForSourceHardPurge :exec
WITH hard AS (
  SELECT tombstone.*
  FROM deletion_tombstones AS tombstone
  WHERE tombstone.id = sqlc.arg(tombstone_id)::uuid
    AND tombstone.project_id = sqlc.arg(project_id)::uuid
    AND tombstone.purge_mode = 'source_hard_purge'
    AND tombstone.status = 'purged'
), contained AS MATERIALIZED (
  SELECT
    derived.id,
    derived.project_id,
    (
      SELECT first_hard.purged_at + interval '30 days'
      FROM deletion_tombstones AS first_hard
      WHERE first_hard.status = 'purged'
        AND ck_source_hard_purge_contains_derived(first_hard, derived)
      ORDER BY first_hard.purged_at, uuid_send(first_hard.id)
      LIMIT 1
    ) AS audit_expires_at
  FROM hard
  JOIN deletion_tombstones AS derived
    ON derived.status = 'purged'
   AND ck_source_hard_purge_contains_derived(hard, derived)
), pinned_tombstones AS (
  UPDATE deletion_tombstones AS derived
  SET
    audit_expires_at = contained.audit_expires_at,
    updated_at = clock_timestamp()
  FROM contained
  WHERE derived.id = contained.id
    AND derived.project_id = contained.project_id
    AND derived.audit_expires_at IS DISTINCT FROM contained.audit_expires_at
)
UPDATE deletion_completion_receipts AS receipt
SET audit_expires_at = contained.audit_expires_at
FROM contained
WHERE receipt.tombstone_id = contained.id
  AND receipt.project_id = contained.project_id
  AND receipt.audit_expires_at IS DISTINCT FROM contained.audit_expires_at;

-- name: InsertDeletionCompletionReceipt :one
INSERT INTO deletion_completion_receipts (
  tombstone_id,
  project_id,
  vector_digest,
  target_receipts_digest,
  completed_at,
  audit_expires_at,
  schema_version
)
SELECT
  tombstone.id,
  tombstone.project_id,
  sqlc.arg(vector_digest)::bytea,
  sqlc.arg(target_receipts_digest)::bytea,
  sqlc.arg(completed_at)::timestamptz,
  tombstone.audit_expires_at,
  'CKDeletionReceiptV1'
FROM deletion_tombstones AS tombstone
WHERE tombstone.id = sqlc.arg(tombstone_id)::uuid
  AND tombstone.project_id = sqlc.arg(project_id)::uuid
  AND tombstone.status = 'purged'
  AND sqlc.arg(target_receipts_digest)::bytea = ck_deletion_target_receipt_set_digest(
    tombstone.purge_mode,
    sqlc.arg(tombstone_digest)::bytea,
    sqlc.arg(vector_digest)::bytea,
    ARRAY(
      SELECT task.receipt_digest
      FROM deletion_target_tasks AS task
      WHERE task.tombstone_id = tombstone.id
        AND task.project_id = tombstone.project_id
        AND task.state IN ('succeeded', 'no_data')
      ORDER BY CASE task.target
        WHEN 'runtime' THEN 1
        WHEN 'filesystem' THEN 2
        WHEN 'postgresql' THEN 4
      END
    )
  )
RETURNING target_receipts_digest;

-- name: GetRetainedDeletionCompletionReceipt :one
SELECT vector_digest, target_receipts_digest, completed_at
FROM deletion_completion_receipts
WHERE tombstone_id = sqlc.arg(tombstone_id)::uuid
  AND project_id = sqlc.arg(project_id)::uuid;
