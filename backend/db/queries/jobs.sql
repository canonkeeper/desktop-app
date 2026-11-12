-- name: CreateIdempotencyRecord :one
INSERT INTO idempotency_records (
  id,
  scope_hash,
  operation_id,
  idempotency_key_hash,
  request_hash,
  status_code,
  response_json,
  created_at
)
VALUES (
  COALESCE(sqlc.narg(id)::uuid, gen_random_uuid()),
  sqlc.arg(scope_hash),
  sqlc.arg(operation_id),
  sqlc.arg(idempotency_key_hash),
  sqlc.arg(request_hash),
  sqlc.arg(status_code),
  sqlc.arg(response_json),
  sqlc.arg(created_at)
)
ON CONFLICT (scope_hash, operation_id, idempotency_key_hash) DO NOTHING
RETURNING id, scope_hash, operation_id, idempotency_key_hash, request_hash, status_code, response_json, created_at;

-- name: GetIdempotencyRecord :one
SELECT id, scope_hash, operation_id, idempotency_key_hash, request_hash, status_code, response_json, created_at
FROM idempotency_records
WHERE scope_hash = sqlc.arg(scope_hash)
  AND operation_id = sqlc.arg(operation_id)
  AND idempotency_key_hash = sqlc.arg(idempotency_key_hash);

-- name: UpdateIdempotencyRecordResponse :one
UPDATE idempotency_records
SET
  status_code = sqlc.arg(status_code),
  response_json = sqlc.arg(response_json)
WHERE id = sqlc.arg(id)
RETURNING id, scope_hash, operation_id, idempotency_key_hash, request_hash, status_code, response_json, created_at;

-- name: CreateJob :one
INSERT INTO jobs (
  id,
  project_id,
  kind,
  status,
  progress_json,
  subject_json,
  result_json,
  error_json,
  creator_user_id,
  can_cancel,
  queue_class,
  retry_count,
  max_attempts,
  next_attempt_at,
  artifact_source_owner_id,
  governance_payload_state,
  creator_request_id,
  created_at,
  updated_at
)
SELECT
  sqlc.arg(id),
  p.id,
  sqlc.arg(kind),
  sqlc.arg(status),
  CASE
    WHEN sqlc.arg(kind)::text = 'research_chat_turn'
      THEN jsonb_build_object('phase', 'waiting')
    ELSE jsonb_build_object('value', sqlc.arg(progress)::double precision)
  END,
  sqlc.arg(subject_json)::jsonb,
  sqlc.narg(result_json)::jsonb,
  sqlc.narg(error_json)::jsonb,
  sqlc.narg(creator_user_id),
  sqlc.arg(can_cancel),
  sqlc.arg(queue_class),
  sqlc.arg(retry_count),
  sqlc.arg(max_attempts),
  sqlc.arg(next_attempt_at),
  sqlc.narg(artifact_source_owner_id),
  CASE WHEN sqlc.narg(artifact_source_owner_id)::uuid IS NULL THEN NULL ELSE 'active' END,
  sqlc.narg(creator_request_id),
  sqlc.arg(created_at),
  sqlc.arg(updated_at)
FROM projects p
WHERE p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
RETURNING *;

-- name: LockCanonTextIndexActiveWork :one
SELECT job.*
FROM jobs AS job
WHERE job.project_id = sqlc.arg(project_id)
  AND job.kind = 'canon_text_index'
  AND job.status IN ('queued', 'running', 'canceling')
  AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
  AND job.can_cancel
  AND job.queue_class = 'user_demanded'
ORDER BY
  CASE
    WHEN job.status = 'queued' AND job.claim_incarnation = 0
      AND job.cancel_requested_at IS NULL THEN 0
    ELSE 1
  END,
  (job.subject_json->>'catalogRevision')::bigint DESC,
  job.created_at DESC,
  job.id DESC
LIMIT 1
FOR UPDATE OF job;

-- name: CreateCanonTextIndexIntent :one
INSERT INTO jobs (
  id, project_id, kind, status, progress_json, subject_json, result_json, error_json,
  creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at,
  artifact_source_owner_id, governance_payload_state, creator_request_id, created_at, updated_at
)
SELECT
  sqlc.arg(id), project.id, 'canon_text_index', 'queued', '{}'::jsonb,
  sqlc.arg(subject_json)::jsonb, NULL, NULL, sqlc.narg(creator_user_id), true,
  'user_demanded', 0, 3, sqlc.arg(created_at), sqlc.arg(artifact_source_owner_id),
  'active', sqlc.narg(creator_request_id), sqlc.arg(created_at), sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
RETURNING jobs.*;

-- name: LockQueuedChapterCardsIntent :one
SELECT job.*
FROM jobs AS job
WHERE job.project_id = sqlc.arg(project_id)
  AND job.kind = 'chapter_cards'
  AND job.status = 'queued'
  AND job.claim_incarnation = 0
  AND job.cancel_requested_at IS NULL
  AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
  AND ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  )
FOR UPDATE OF job;

-- name: CreateChapterCardsIntent :one
INSERT INTO jobs (
  id, project_id, kind, status, progress_json, subject_json, result_json, error_json,
  creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at,
  artifact_source_owner_id, governance_payload_state, created_at, updated_at
)
SELECT
  sqlc.arg(id), project.id, 'chapter_cards', 'queued', '{}'::jsonb,
  sqlc.arg(subject_json)::jsonb, NULL, NULL, NULL, true,
  'automatic', 0, 3, sqlc.arg(created_at), sqlc.arg(artifact_source_owner_id),
  'active', sqlc.arg(created_at), sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
RETURNING jobs.*;

-- name: LockQueuedSagaLayerIntent :one
-- A waiting Job of the project builds whatever is current when it runs (R2.1).
SELECT job.*
FROM jobs AS job
WHERE job.project_id = sqlc.arg(project_id)
  AND job.kind = 'saga_layer'
  AND job.status = 'queued'
  AND job.cancel_requested_at IS NULL
  AND ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  )
ORDER BY job.created_at, job.id
LIMIT 1
FOR UPDATE OF job;

-- name: CreateSagaLayerIntent :one
INSERT INTO jobs (
  id, project_id, kind, status, progress_json, subject_json, result_json, error_json,
  creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at,
  artifact_source_owner_id, governance_payload_state, created_at, updated_at
)
SELECT
  sqlc.arg(id), project.id, 'saga_layer', 'queued', '{}'::jsonb,
  sqlc.arg(subject_json)::jsonb, NULL, NULL, NULL, true,
  'automatic', 0, 3, sqlc.arg(created_at), sqlc.arg(artifact_source_owner_id),
  'active', sqlc.arg(created_at), sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
RETURNING jobs.*;

-- name: CreateChapterDraftJob :one
INSERT INTO jobs (
  id, project_id, kind, status, progress_json, subject_json, result_json, error_json,
  creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at,
  artifact_source_owner_id, governance_payload_state, created_at, updated_at
)
SELECT
  sqlc.arg(id), project.id, 'chapter_draft', 'queued', '{}'::jsonb,
  sqlc.arg(subject_json)::jsonb, NULL, NULL, sqlc.arg(creator_user_id), true,
  'interactive', 0, 3, sqlc.arg(created_at), sqlc.arg(artifact_source_owner_id),
  'active', sqlc.arg(created_at), sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
RETURNING jobs.*;

-- name: CreateAuthorMaterialsImportJob :one
INSERT INTO jobs (
  id, project_id, kind, status, progress_json, subject_json, result_json, error_json,
  creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at,
  artifact_source_owner_id, governance_payload_state, created_at, updated_at
)
SELECT
  sqlc.arg(id), project.id, 'author_materials_import', 'queued', '{}'::jsonb,
  sqlc.arg(subject_json)::jsonb, NULL, NULL, sqlc.arg(creator_user_id), true,
  'user_demanded', 0, 3, sqlc.arg(created_at), sqlc.arg(artifact_source_owner_id),
  'active', sqlc.arg(created_at), sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
RETURNING jobs.*;

-- name: CreateIllustrationJob :one
INSERT INTO jobs (
  id, project_id, kind, status, progress_json, subject_json, result_json, error_json,
  creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at,
  artifact_source_owner_id, governance_payload_state, created_at, updated_at
)
SELECT
  sqlc.arg(id), project.id, 'illustration', 'queued', '{}'::jsonb,
  sqlc.arg(subject_json)::jsonb, NULL, NULL, sqlc.arg(creator_user_id), false,
  'interactive', 0, 3, sqlc.arg(next_attempt_at), sqlc.arg(artifact_source_owner_id),
  'active', sqlc.arg(created_at), sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
RETURNING jobs.*;

-- name: UpdateQueuedCanonTextIndexIntent :one
UPDATE jobs AS job
SET subject_json = sqlc.arg(subject_json)::jsonb,
    creator_user_id = COALESCE(job.creator_user_id, sqlc.narg(creator_user_id)),
    updated_at = sqlc.arg(updated_at)
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.artifact_source_owner_id = sqlc.arg(artifact_source_owner_id)
  AND job.kind = 'canon_text_index'
  AND job.status = 'queued'
  AND job.claim_incarnation = 0
  AND job.cancel_requested_at IS NULL
  AND job.can_cancel
  AND job.queue_class = 'user_demanded'
  AND ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  )
RETURNING job.*;

-- name: AdoptQueuedCanonTextIndexIntentCreator :one
UPDATE jobs AS job
SET creator_user_id = sqlc.arg(creator_user_id),
    updated_at = sqlc.arg(updated_at)
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.artifact_source_owner_id = sqlc.arg(artifact_source_owner_id)
  AND job.kind = 'canon_text_index'
  AND job.status = 'queued'
  AND job.claim_incarnation = 0
  AND job.cancel_requested_at IS NULL
  AND job.creator_user_id IS NULL
  AND job.can_cancel
  AND job.queue_class = 'user_demanded'
  AND ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  )
RETURNING job.*;

-- name: SupersedeCanonTextIndexPredecessors :execrows
UPDATE jobs AS job
SET status = CASE
      WHEN job.status = 'queued' THEN 'canceled'
      ELSE 'canceling'
    END,
    cancel_requested_at = sqlc.arg(updated_at),
    claimed_at = CASE WHEN job.status = 'queued' THEN NULL ELSE job.claimed_at END,
    updated_at = sqlc.arg(updated_at)
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id <> sqlc.arg(successor_job_id)
  AND job.kind = 'canon_text_index'
  AND job.status IN ('queued', 'running')
  AND job.subject_json->>'type' = 'canon_text_index'
  AND job.subject_json->>'projectId' = job.project_id::text
  AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
  AND (job.subject_json->>'catalogRevision')::bigint < sqlc.arg(catalog_revision)::bigint
  AND job.can_cancel
  AND job.queue_class = 'user_demanded'
  AND ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  );

-- name: ListProjectJobs :many
-- One page of Job candidates in list order (specs/2026-10-28-fast-job-list-and-postgres.md,
-- UC2). Tenant predicates, the kind and active filters and the cursor pick the page through
-- jobs_project_kind_recent_idx or jobs_project_active_recent_idx; only the rows of the page get
-- the read check, and the caller skips the unreadable ones. A Job without an owner is never
-- readable and calls no check.
SELECT
  sqlc.embed(j),
  (
    j.artifact_source_owner_id IS NOT NULL
    AND CASE WHEN j.kind = 'research_chat_turn'
      THEN ck_artifact_source_owner_is_lifecycle_readable(
        j.artifact_source_owner_id, j.project_id, 'job', j.id
      )
      ELSE ck_artifact_source_owner_is_readable(
        j.artifact_source_owner_id, j.project_id, 'job', j.id
      )
    END
  )::boolean AS readable
FROM (
  SELECT candidate.id
  FROM jobs candidate
  JOIN projects p ON p.id = candidate.project_id
  JOIN project_memberships m ON m.project_id = candidate.project_id
  WHERE candidate.project_id = sqlc.arg(project_id)
    AND m.user_id = sqlc.arg(actor_user_id)
    AND m.removed_at IS NULL
    AND p.deleted_at IS NULL
    AND (sqlc.arg(kind)::text = '' OR candidate.kind = sqlc.arg(kind)::text)
    AND (NOT sqlc.arg(active_only)::boolean OR candidate.status IN ('queued', 'running', 'canceling'))
    AND (
      NOT sqlc.arg(has_cursor)::boolean
      OR (candidate.created_at, candidate.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
    )
  ORDER BY candidate.created_at DESC, candidate.id DESC
  LIMIT sqlc.arg(limit_count)
) AS page
JOIN jobs j ON j.id = page.id
ORDER BY j.created_at DESC, j.id DESC;

-- name: ListIndexingStatusJobs :many
-- The Jobs the indexing status projects (specs/2026-10-28-fast-job-list-and-postgres.md, UC3):
-- per current text version the latest readable text index Job of its catalog revision, the
-- latest readable cards Job in work and the latest readable cards Job; for the book in the
-- request (empty: none) its latest readable import. Each group walks
-- jobs_project_kind_recent_idx newest first and stops at its first readable Job.
WITH actor_project AS (
  SELECT p.id
  FROM projects p
  JOIN project_memberships m ON m.project_id = p.id
  WHERE p.id = sqlc.arg(project_id)
    AND p.deleted_at IS NULL
    AND m.user_id = sqlc.arg(actor_user_id)
    AND m.removed_at IS NULL
),
versions AS (
  SELECT marker.project_id, marker.source_view, marker.catalog_revision
  FROM project_source_views marker
  JOIN actor_project ON actor_project.id = marker.project_id
  JOIN projects project
    ON project.id = marker.project_id
   AND project.lifecycle_generation = marker.project_lifecycle_generation
),
latest AS (
  SELECT text_job.id
  FROM versions
  CROSS JOIN LATERAL (
    SELECT candidate.id
    FROM jobs candidate
    WHERE candidate.project_id = versions.project_id
      AND candidate.kind = 'canon_text_index'
      AND candidate.subject_json->>'sourceView' = versions.source_view
      AND (candidate.subject_json->>'catalogRevision')::bigint = versions.catalog_revision
      AND candidate.artifact_source_owner_id IS NOT NULL
      AND ck_artifact_source_owner_is_readable(
        candidate.artifact_source_owner_id, candidate.project_id, 'job', candidate.id
      )
    ORDER BY candidate.created_at DESC, candidate.id DESC
    LIMIT 1
  ) AS text_job
  UNION
  SELECT active_cards_job.id
  FROM versions
  CROSS JOIN LATERAL (
    SELECT candidate.id
    FROM jobs candidate
    WHERE candidate.project_id = versions.project_id
      AND candidate.kind = 'chapter_cards'
      AND candidate.status IN ('queued', 'running', 'canceling')
      AND candidate.subject_json->>'sourceView' = versions.source_view
      AND candidate.artifact_source_owner_id IS NOT NULL
      AND ck_artifact_source_owner_is_readable(
        candidate.artifact_source_owner_id, candidate.project_id, 'job', candidate.id
      )
    ORDER BY candidate.created_at DESC, candidate.id DESC
    LIMIT 1
  ) AS active_cards_job
  UNION
  SELECT cards_job.id
  FROM versions
  CROSS JOIN LATERAL (
    SELECT candidate.id
    FROM jobs candidate
    WHERE candidate.project_id = versions.project_id
      AND candidate.kind = 'chapter_cards'
      AND candidate.subject_json->>'sourceView' = versions.source_view
      AND candidate.artifact_source_owner_id IS NOT NULL
      AND ck_artifact_source_owner_is_readable(
        candidate.artifact_source_owner_id, candidate.project_id, 'job', candidate.id
      )
    ORDER BY candidate.created_at DESC, candidate.id DESC
    LIMIT 1
  ) AS cards_job
  UNION
  SELECT import_job.id
  FROM actor_project
  CROSS JOIN LATERAL (
    SELECT candidate.id
    FROM jobs candidate
    WHERE candidate.project_id = actor_project.id
      AND candidate.kind = 'import'
      AND candidate.result_json->>'bookId' = sqlc.arg(book_id)::text
      AND candidate.artifact_source_owner_id IS NOT NULL
      AND ck_artifact_source_owner_is_readable(
        candidate.artifact_source_owner_id, candidate.project_id, 'job', candidate.id
      )
    ORDER BY candidate.created_at DESC, candidate.id DESC
    LIMIT 1
  ) AS import_job
)
SELECT sqlc.embed(j)
FROM jobs j
WHERE j.id IN (SELECT latest.id FROM latest)
ORDER BY j.created_at DESC, j.id DESC;

-- name: ResolveJobAccessForActor :one
SELECT
  j.project_id,
  j.id AS job_id,
  m.role::text AS current_role,
  'active'::text AS visibility
FROM jobs j
JOIN projects p ON p.id = j.project_id
JOIN project_memberships m ON m.project_id = j.project_id
WHERE j.id = sqlc.arg(job_id)
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
  AND p.deleted_at IS NULL
  AND CASE WHEN j.kind = 'research_chat_turn'
    THEN ck_artifact_source_owner_is_lifecycle_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
    ELSE ck_artifact_source_owner_is_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
  END
  ;

-- name: GetJob :one
SELECT
  j.*
FROM jobs j
JOIN projects p ON p.id = j.project_id
JOIN project_memberships m ON m.project_id = j.project_id
WHERE j.id = sqlc.arg(job_id)
  AND j.project_id = sqlc.arg(project_id)
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
  AND p.deleted_at IS NULL
  AND CASE WHEN j.kind = 'research_chat_turn'
    THEN ck_artifact_source_owner_is_lifecycle_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
    ELSE ck_artifact_source_owner_is_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
  END
  ;

-- name: ChatTurnQueuePosition :one
-- UC10 of specs/2026-10-28-writer-quotas-and-fair-queue.md: the waiting questions the fair claim
-- takes before this one. The k-th waiting question of a writer counts with the writer's questions
-- in work plus k - 1; only the number leaves the query.
WITH busy AS (
  SELECT working.creator_user_id, count(*) AS working
  FROM jobs AS working
  WHERE working.kind = 'research_chat_turn' AND working.status IN ('running', 'canceling')
  GROUP BY working.creator_user_id
), waiting AS (
  SELECT queued.id, queued.next_attempt_at, queued.created_at,
         COALESCE(busy.working, 0) + row_number() OVER (
           PARTITION BY queued.creator_user_id
           ORDER BY queued.next_attempt_at, queued.created_at, queued.id
         ) - 1 AS round
  FROM jobs AS queued
  LEFT JOIN busy ON busy.creator_user_id = queued.creator_user_id
  WHERE queued.kind = 'research_chat_turn' AND queued.status = 'queued'
    AND queued.governance_payload_state = 'active'
), ranked AS (
  SELECT id, row_number() OVER (ORDER BY round, next_attempt_at, created_at, id) AS place
  FROM waiting
)
SELECT (place - 1)::bigint AS position
FROM ranked
WHERE id = sqlc.arg(job_id)::uuid;

-- name: LoadJobForWorkerClaim :one
SELECT
  j.*
FROM jobs j
JOIN projects p ON p.id = j.project_id
WHERE j.project_id = sqlc.arg(project_id)
  AND j.id = sqlc.arg(job_id)
  AND j.claim_incarnation = sqlc.arg(claim_incarnation)
  AND p.deleted_at IS NULL
  AND CASE
    WHEN j.kind = 'research_chat_turn'
      AND j.status IN ('succeeded', 'failed', 'canceled')
    THEN ck_artifact_source_owner_is_lifecycle_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
    ELSE ck_artifact_source_owner_is_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
  END;

-- name: HeartbeatWorkerClaim :one
UPDATE jobs AS job
SET updated_at = clock_timestamp()
FROM projects AS project
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.status = 'running'
  AND job.cancel_requested_at IS NULL
  AND project.id = job.project_id
  AND project.deleted_at IS NULL
  AND CASE WHEN job.kind = 'research_chat_turn' THEN ck_artifact_source_owner_is_lifecycle_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  ) ELSE ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  ) END
RETURNING job.id;

-- name: RequeueWorkerClaim :one
UPDATE jobs AS job
SET
  status = 'queued',
  progress_json = CASE
    WHEN job.kind = 'import' THEN jsonb_build_object(
      'phase', 'queued',
      'bytesTransferred', job.progress_json->'bytesTransferred',
      'bytesTotal', job.progress_json->'bytesTotal',
      'queuedAt', job.progress_json->'queuedAt',
      'transferredAt', job.progress_json->'transferredAt'
    )
    ELSE job.progress_json
  END,
  retry_count = job.retry_count + 1,
  claim_incarnation = job.claim_incarnation + 1,
  claimed_at = NULL,
  next_attempt_at = clock_timestamp() + make_interval(secs => sqlc.arg(retry_delay_seconds)::double precision),
  updated_at = clock_timestamp()
FROM projects AS project
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.kind = sqlc.arg(kind)
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.status = 'running'
  AND job.cancel_requested_at IS NULL
  AND job.retry_count = sqlc.arg(expected_retry_count)
  AND job.subject_json->>'type' = job.kind
  AND job.subject_json->>'projectId' = job.project_id::text
  AND project.id = job.project_id
  AND project.deleted_at IS NULL
  AND ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  )
RETURNING job.*;

-- name: ReleaseWorkerClaim :one
-- A graceful stop returns the exact running claim to the queue without spending an attempt;
-- the next claim increments claim_incarnation again.
UPDATE jobs AS job
SET
  status = 'queued',
  progress_json = CASE
    WHEN job.kind = 'import' THEN jsonb_build_object(
      'phase', 'queued',
      'bytesTransferred', job.progress_json->'bytesTransferred',
      'bytesTotal', job.progress_json->'bytesTotal',
      'queuedAt', job.progress_json->'queuedAt',
      'transferredAt', job.progress_json->'transferredAt'
    )
    WHEN job.kind = 'research_chat_turn' THEN jsonb_build_object('phase', 'waiting')
    ELSE job.progress_json
  END,
  claim_incarnation = job.claim_incarnation + 1,
  claimed_at = NULL,
  next_attempt_at = clock_timestamp(),
  updated_at = clock_timestamp()
FROM projects AS project
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.status = 'running'
  AND job.cancel_requested_at IS NULL
  AND project.id = job.project_id
  AND project.deleted_at IS NULL
  AND CASE WHEN job.kind = 'research_chat_turn' THEN ck_artifact_source_owner_is_lifecycle_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  ) ELSE ck_artifact_source_owner_is_readable(
    job.artifact_source_owner_id, job.project_id, 'job', job.id
  ) END
RETURNING job.*;

-- name: PreviewModelJobForStart :one
SELECT job.*
FROM jobs AS job
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id);

-- name: LockModelClaimJobOwner :one
SELECT
  owner.id,
  owner.source_view,
  owner.manifest_version,
  owner.manifest_hash,
  owner.ref_count
FROM artifact_source_owners AS owner
WHERE owner.project_id = sqlc.arg(project_id)
  AND owner.id = sqlc.arg(owner_id)
  AND owner.artifact_kind = 'job'
  AND owner.artifact_id = sqlc.arg(job_id)
  AND ck_artifact_source_owner_is_readable(
    owner.id, owner.project_id, 'job', owner.artifact_id
  )
FOR UPDATE;

-- name: CancelJobStatus :one
UPDATE jobs j
SET
  status = sqlc.arg(to_status),
  progress_json = CASE
    WHEN j.kind IN ('import', 'chapter_draft') THEN j.progress_json
    WHEN j.kind = 'research_chat_turn'
      THEN '{}'::jsonb
    ELSE jsonb_set(
      COALESCE(j.progress_json, '{}'::jsonb),
      '{value}',
      to_jsonb(sqlc.arg(progress)::double precision),
      true
    )
  END,
  result_json = sqlc.narg(result_json)::jsonb,
  error_json = sqlc.narg(error_json)::jsonb,
  cancel_requested_at = CASE
    WHEN sqlc.arg(to_status)::text IN ('canceling', 'canceled') THEN sqlc.arg(updated_at)
    ELSE j.cancel_requested_at
  END,
  claimed_at = CASE
    WHEN sqlc.arg(to_status)::text IN ('canceled', 'expired') THEN NULL
    ELSE j.claimed_at
  END,
  completed_at = CASE
    WHEN sqlc.arg(to_status)::text IN ('succeeded', 'failed', 'canceled', 'expired')
      THEN COALESCE(j.completed_at, sqlc.arg(updated_at)::timestamptz)
    ELSE j.completed_at
  END,
  updated_at = sqlc.arg(updated_at)
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
WHERE j.project_id = sqlc.arg(project_id)
  AND j.id = sqlc.arg(job_id)
  AND j.project_id = p.id
  AND j.status = sqlc.arg(from_status)
  AND p.deleted_at IS NULL
  AND CASE
    WHEN j.kind = 'research_chat_turn'
      AND sqlc.arg(to_status)::text IN ('canceling', 'canceled')
    THEN ck_artifact_source_owner_is_lifecycle_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
    ELSE ck_artifact_source_owner_is_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
  END
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
  AND (
    j.creator_user_id = sqlc.arg(actor_user_id)
    OR m.role IN ('admin', 'owner')
  )
RETURNING j.*;

-- name: PreviewJobForCancel :one
SELECT j.kind, j.subject_json, j.creator_user_id
FROM jobs AS j
WHERE j.project_id = sqlc.arg(project_id)
  AND j.id = sqlc.arg(job_id);

-- name: LockJobForCancel :one
SELECT j.*
FROM jobs AS j
WHERE j.project_id = sqlc.arg(project_id)
  AND j.id = sqlc.arg(job_id)
  AND CASE WHEN j.kind = 'research_chat_turn'
    THEN ck_artifact_source_owner_is_lifecycle_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
    ELSE ck_artifact_source_owner_is_readable(
      j.artifact_source_owner_id, j.project_id, 'job', j.id
    )
  END
FOR UPDATE;

-- name: LockJobForStatusUpdate :one
SELECT j.*
FROM jobs AS j
WHERE j.project_id = sqlc.arg(project_id)
  AND j.id = sqlc.arg(job_id)
  AND j.claim_incarnation = sqlc.arg(claim_incarnation)
  AND ck_artifact_source_owner_is_readable(
    j.artifact_source_owner_id, j.project_id, 'job', j.id
  )
FOR UPDATE;

-- name: LockJobModelBinding :one
SELECT binding.*
FROM job_model_bindings AS binding
WHERE binding.project_id = sqlc.arg(project_id)
  AND binding.job_id = sqlc.arg(job_id)
FOR UPDATE;

-- name: CreateJobModelBinding :one
INSERT INTO job_model_bindings (
  job_id,
  project_id,
  artifact_source_owner_id,
  job_kind,
  origin,
  actor_user_id,
  initiating_auth_session_id,
  input_contract_version,
  created_at
)
VALUES (
  sqlc.arg(job_id),
  sqlc.arg(project_id),
  sqlc.arg(artifact_source_owner_id),
  sqlc.arg(job_kind),
  sqlc.arg(origin),
  sqlc.narg(actor_user_id),
  sqlc.narg(initiating_auth_session_id),
  sqlc.arg(input_contract_version),
  sqlc.arg(created_at)
)
RETURNING *;

-- name: DeleteJobModelBinding :one
DELETE FROM job_model_bindings AS binding
WHERE binding.project_id = sqlc.arg(project_id)
  AND binding.job_id = sqlc.arg(job_id)
  AND binding.artifact_source_owner_id = sqlc.arg(artifact_source_owner_id)
RETURNING binding.*;

-- name: CreateAgentWorkflowInput :one
INSERT INTO agent_workflow_inputs (
  id,
  project_id,
  job_id,
  job_kind,
  target_id,
  source_view,
  body_json,
  body_digest,
  context_warning,
  artifact_source_owner_id,
  created_at
)
VALUES (
  sqlc.arg(id),
  sqlc.arg(project_id),
  sqlc.arg(job_id),
  sqlc.arg(job_kind),
  sqlc.arg(target_id),
  sqlc.arg(source_view),
  sqlc.arg(body_json),
  sqlc.arg(body_digest),
  sqlc.narg(context_warning),
  sqlc.arg(artifact_source_owner_id),
  sqlc.arg(created_at)
)
RETURNING *;

-- name: GetAgentWorkflowInputByJob :one
SELECT *
FROM agent_workflow_inputs
WHERE job_id = sqlc.arg(job_id)
  AND project_id = sqlc.arg(project_id)
  AND ck_artifact_source_owner_is_readable(
        artifact_source_owner_id,
        project_id,
        'agent_workflow_input',
        id
      );

-- name: LockAgentWorkflowInputForSeal :one
SELECT *
FROM agent_workflow_inputs
WHERE id = sqlc.arg(input_id)
  AND project_id = sqlc.arg(project_id)
  AND job_id = sqlc.arg(job_id)
  AND ck_artifact_source_owner_is_readable(
        artifact_source_owner_id,
        project_id,
        'agent_workflow_input',
        id
      )
FOR UPDATE;

-- name: DeleteAgentWorkflowInput :one
DELETE FROM agent_workflow_inputs
WHERE id = sqlc.arg(input_id)
  AND project_id = sqlc.arg(project_id)
  AND job_id = sqlc.arg(job_id)
RETURNING id;
