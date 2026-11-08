-- name: CreateBook :one
INSERT INTO books (
  id,
  project_id,
  title,
  subtitle,
  cover_color,
  "order",
  display_number,
  lifecycle_generation,
  created_at,
  updated_at
)
SELECT
  sqlc.arg(id),
  project.id,
  sqlc.arg(title),
  sqlc.arg(subtitle),
  sqlc.arg(cover_color),
  sqlc.arg(sort_order),
  sqlc.arg(display_number),
  1,
  sqlc.arg(created_at),
  sqlc.arg(created_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id)
  AND project.deleted_at IS NULL
RETURNING id, project_id, title, subtitle, cover_color, "order",
          display_number, status, revision, deleted_at, purge_after,
          created_at, updated_at, lifecycle_generation,
          hard_purge_started_at, governance_retention_started_at,
          purge_deadline;

-- name: ResolveBookAccessForActor :one
SELECT
  project.id AS project_id,
  book.id AS book_id,
  membership.role,
  CASE WHEN book.deleted_at IS NULL THEN 'active' ELSE 'deleted_source' END::text AS visibility
FROM books AS book
JOIN projects AS project
  ON project.id = book.project_id
JOIN project_memberships AS membership
  ON membership.project_id = project.id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE book.id = sqlc.arg(book_id)
  AND (
    (sqlc.arg(expected_visibility)::text = 'active'
      AND book.deleted_at IS NULL
      AND project.deleted_at IS NULL)
    OR
    (sqlc.arg(expected_visibility)::text = 'deleted_source' AND book.deleted_at IS NOT NULL)
  );

-- name: ResolveChapterAccessForActor :one
SELECT
  project.id AS project_id,
  book.id AS book_id,
  chapter.id AS chapter_id,
  membership.role,
  CASE WHEN chapter.deleted_at IS NULL THEN 'active' ELSE 'deleted_source' END::text AS visibility
FROM chapters AS chapter
JOIN books AS book
  ON book.id = chapter.book_id
 AND book.project_id = chapter.project_id
JOIN projects AS project
  ON chapter.project_id = project.id
JOIN project_memberships AS membership
  ON membership.project_id = project.id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE chapter.id = sqlc.arg(chapter_id)
  AND (
    (sqlc.arg(expected_visibility)::text = 'active'
      AND chapter.deleted_at IS NULL
      AND book.deleted_at IS NULL
      AND project.deleted_at IS NULL)
    OR
    (sqlc.arg(expected_visibility)::text = 'deleted_source' AND chapter.deleted_at IS NOT NULL)
  );

-- name: LoadDeletedBookLifecycle :one
SELECT CASE
  WHEN project.deleted_at IS NOT NULL THEN 'parent_deleted'
  WHEN book.governance_retention_started_at IS NULL
    OR book.purge_after IS NULL
    OR book.hard_purge_started_at IS NOT NULL THEN 'terminal'
  WHEN clock_timestamp() >= book.purge_after THEN 'expired'
  ELSE 'restorable'
END::text
FROM books AS book
JOIN projects AS project ON project.id = book.project_id
WHERE book.project_id = sqlc.arg(project_id)
  AND book.id = sqlc.arg(book_id)
  AND book.deleted_at IS NOT NULL;

-- name: LoadDeletedChapterLifecycle :one
SELECT CASE
  WHEN project.deleted_at IS NOT NULL OR book.deleted_at IS NOT NULL THEN 'parent_deleted'
  WHEN chapter.governance_retention_started_at IS NULL
    OR chapter.purge_after IS NULL
    OR chapter.hard_purge_started_at IS NOT NULL THEN 'terminal'
  WHEN clock_timestamp() >= chapter.purge_after THEN 'expired'
  ELSE 'restorable'
END::text
FROM chapters AS chapter
JOIN books AS book
  ON book.id = chapter.book_id
 AND book.project_id = chapter.project_id
JOIN projects AS project ON project.id = chapter.project_id
WHERE chapter.project_id = sqlc.arg(project_id)
  AND chapter.book_id = sqlc.arg(book_id)
  AND chapter.id = sqlc.arg(chapter_id)
  AND chapter.deleted_at IS NOT NULL;

-- name: GetBookSummaryByID :one
SELECT
  b.id,
  b.project_id,
  b.title,
  b.subtitle,
  b.cover_color,
  b."order",
  b.display_number,
  b.status,
  b.revision,
  b.deleted_at,
  b.purge_after,
  b.created_at,
  b.updated_at,
  COALESCE(cover.version, 0)::bigint AS cover_version,
  COALESCE(book_stats.chapter_count, 0)::integer AS chapter_count,
  COALESCE(book_stats.word_count, 0)::integer AS word_count
FROM books b
JOIN projects p ON p.id = b.project_id
LEFT JOIN LATERAL (
  SELECT artifact.version
  FROM book_cover_artifacts AS artifact
  WHERE artifact.book_id = b.id
    AND artifact.book_lifecycle_generation = b.lifecycle_generation
    AND artifact.state = 'active'
  LIMIT 1
) cover ON true
LEFT JOIN LATERAL (
  SELECT
    COUNT(c.id)::integer AS chapter_count,
    COALESCE(SUM(chapter_words.word_count), 0)::integer AS word_count
  FROM chapters c
  LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(cp.word_count), 0)::integer AS word_count
    FROM chapter_paragraphs cp
    WHERE cp.chapter_id = c.id
      AND cp.revision = c.revision
      AND cp.variant = 'draft'
  ) chapter_words ON true
  WHERE c.book_id = b.id
    AND c.deleted_at IS NULL
) book_stats ON true
WHERE b.project_id = sqlc.arg(project_id)
  AND b.id = sqlc.arg(book_id)
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL;

-- name: ListBookSummariesByProject :many
SELECT
  b.id,
  b.project_id,
  b.title,
  b.subtitle,
  b.cover_color,
  b."order",
  b.display_number,
  b.status,
  b.revision,
  b.deleted_at,
  b.purge_after,
  b.created_at,
  b.updated_at,
  COALESCE(cover.version, 0)::bigint AS cover_version,
  COALESCE(book_stats.chapter_count, 0)::integer AS chapter_count,
  COALESCE(book_stats.word_count, 0)::integer AS word_count
FROM books b
JOIN projects p ON p.id = b.project_id
LEFT JOIN LATERAL (
  SELECT artifact.version
  FROM book_cover_artifacts AS artifact
  WHERE artifact.book_id = b.id
    AND artifact.book_lifecycle_generation = b.lifecycle_generation
    AND artifact.state = 'active'
  LIMIT 1
) cover ON true
LEFT JOIN LATERAL (
  SELECT
    COUNT(c.id)::integer AS chapter_count,
    COALESCE(SUM(chapter_words.word_count), 0)::integer AS word_count
  FROM chapters c
  LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(cp.word_count), 0)::integer AS word_count
    FROM chapter_paragraphs cp
    WHERE cp.chapter_id = c.id
      AND cp.revision = c.revision
      AND cp.variant = 'draft'
  ) chapter_words ON true
  WHERE c.book_id = b.id
    AND c.deleted_at IS NULL
) book_stats ON true
WHERE b.project_id = sqlc.arg(project_id)
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND (NOT sqlc.arg(has_cursor)::boolean
    OR (b."order", b.id) > (sqlc.arg(cursor_order)::integer, sqlc.arg(cursor_id)::uuid))
ORDER BY b."order" ASC, b.id ASC
LIMIT sqlc.arg(limit_count);

-- name: LockBookCoverForActor :one
SELECT book.project_id, book.id AS book_id, book.lifecycle_generation, membership.role
FROM books AS book
JOIN projects AS project ON project.id = book.project_id
JOIN project_memberships AS membership
  ON membership.project_id = book.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE book.id = sqlc.arg(book_id)
  AND book.deleted_at IS NULL
  AND project.deleted_at IS NULL
FOR UPDATE OF book;

-- name: NextBookCoverVersion :one
SELECT (COALESCE(MAX(version), 0) + 1)::bigint AS version
FROM book_cover_artifacts
WHERE book_id = sqlc.arg(book_id)
  AND book_lifecycle_generation = sqlc.arg(book_lifecycle_generation);

-- name: SupersedeActiveBookCover :many
UPDATE book_cover_artifacts
SET state = 'superseded', superseded_at = sqlc.arg(superseded_at)
WHERE book_id = sqlc.arg(book_id)
  AND book_lifecycle_generation = sqlc.arg(book_lifecycle_generation)
  AND state = 'active'
RETURNING *;

-- name: InsertActiveBookCover :one
INSERT INTO book_cover_artifacts (
  id, project_id, book_id, book_lifecycle_generation, version,
  storage_key, mime_type, byte_size, width, height, content_sha256,
  state, created_at, activated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(book_id), sqlc.arg(book_lifecycle_generation), sqlc.arg(version),
  sqlc.arg(storage_key), sqlc.arg(mime_type), sqlc.arg(byte_size), sqlc.arg(width), sqlc.arg(height), sqlc.arg(content_sha256),
  'active', sqlc.arg(created_at), sqlc.arg(created_at)
)
RETURNING *;

-- name: FindActiveBookCoverForActor :one
SELECT sqlc.embed(artifact)
FROM book_cover_artifacts AS artifact
JOIN books AS book
  ON book.id = artifact.book_id
 AND book.project_id = artifact.project_id
 AND book.lifecycle_generation = artifact.book_lifecycle_generation
JOIN projects AS project ON project.id = artifact.project_id
JOIN project_memberships AS membership
  ON membership.project_id = artifact.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE artifact.book_id = sqlc.arg(book_id)
  AND artifact.version = sqlc.arg(version)
  AND artifact.state = 'active'
  AND book.deleted_at IS NULL
  AND project.deleted_at IS NULL;

-- name: RemoveActiveBookCover :one
UPDATE book_cover_artifacts
SET state = 'removed', removed_at = sqlc.arg(removed_at)
WHERE book_id = sqlc.arg(book_id)
  AND book_lifecycle_generation = sqlc.arg(book_lifecycle_generation)
  AND state = 'active'
RETURNING *;

-- name: NextBookCoverDeletion :one
SELECT *
FROM book_cover_artifacts
WHERE state IN ('superseded', 'removed')
  AND object_deleted_at IS NULL
ORDER BY COALESCE(superseded_at, removed_at), id
LIMIT 1;

-- name: CompleteBookCoverDeletion :one
UPDATE book_cover_artifacts
SET object_deleted_at = COALESCE(object_deleted_at, sqlc.arg(object_deleted_at))
WHERE id = sqlc.arg(id)
  AND project_id = sqlc.arg(project_id)
  AND book_id = sqlc.arg(book_id)
  AND book_lifecycle_generation = sqlc.arg(book_lifecycle_generation)
  AND version = sqlc.arg(version)
  AND storage_key = sqlc.arg(storage_key)
  AND state = sqlc.arg(state)
  AND state IN ('superseded', 'removed')
RETURNING *;

-- name: UpdateBook :one
UPDATE books b
SET title = sqlc.arg(title),
    subtitle = sqlc.arg(subtitle),
    cover_color = sqlc.arg(cover_color),
    revision = b.revision + 1,
    updated_at = sqlc.arg(updated_at)
FROM projects p
WHERE b.project_id = sqlc.arg(project_id)
  AND b.id = sqlc.arg(book_id)
  AND b.project_id = p.id
  AND p.lifecycle_generation = sqlc.arg(project_generation)
  AND b.lifecycle_generation = sqlc.arg(book_generation)
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
RETURNING b.id, b.project_id, b.title, b.subtitle, b.cover_color, b."order",
          b.display_number, b.status, b.revision, b.deleted_at, b.purge_after,
          b.created_at, b.updated_at, b.lifecycle_generation,
          b.hard_purge_started_at, b.governance_retention_started_at,
          b.purge_deadline;

-- name: CreateChapter :one
INSERT INTO chapters (
  id,
  project_id,
  book_id,
  title,
  "order",
  lifecycle_generation,
  created_at,
  updated_at
)
SELECT
  sqlc.arg(id),
  project.id,
  book.id,
  sqlc.arg(title),
  sqlc.arg(sort_order),
  1,
  sqlc.arg(created_at),
  sqlc.arg(created_at)
FROM books AS book
JOIN projects AS project ON project.id = book.project_id
WHERE book.project_id = sqlc.arg(project_id)
  AND book.id = sqlc.arg(book_id)
  AND book.deleted_at IS NULL
  AND project.deleted_at IS NULL
RETURNING id, book_id, title, "order", status, revision,
          published_revision, published_at, publish_error, deleted_at,
          purge_after, created_at, updated_at, lifecycle_generation,
          hard_purge_started_at, governance_retention_started_at,
          purge_deadline, project_id;

-- name: ListChaptersByBook :many
SELECT c.id, c.book_id, c.title, c."order", c.status, c.revision,
       c.published_revision, c.published_at, c.publish_error, c.deleted_at,
       c.purge_after, c.created_at, c.updated_at, c.lifecycle_generation,
       c.hard_purge_started_at, c.governance_retention_started_at,
       c.purge_deadline, c.project_id
FROM chapters c
JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id
JOIN projects p ON p.id = c.project_id
WHERE c.project_id = sqlc.arg(project_id)
  AND c.book_id = sqlc.arg(book_id)
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND (NOT sqlc.arg(has_cursor)::boolean
    OR (c."order", c.id) > (sqlc.arg(cursor_order)::integer, sqlc.arg(cursor_id)::uuid))
ORDER BY c."order" ASC, c.id ASC
LIMIT sqlc.arg(limit_count);

-- name: GetChapterByID :one
SELECT c.id, c.book_id, c.title, c."order", c.status, c.revision,
       c.published_revision, c.published_at, c.publish_error, c.deleted_at,
       c.purge_after, c.created_at, c.updated_at, c.lifecycle_generation,
       c.hard_purge_started_at, c.governance_retention_started_at,
       c.purge_deadline, c.project_id
FROM chapters c
JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id
JOIN projects p ON p.id = c.project_id
WHERE c.project_id = sqlc.arg(project_id)
  AND c.book_id = sqlc.arg(book_id)
  AND c.id = sqlc.arg(chapter_id)
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND p.deleted_at IS NULL;

-- name: GetChapterWithNavigationByID :one
WITH navigation AS (
  SELECT c.id,
         row_number() OVER (ORDER BY c."order", c.id)::int AS position,
         count(*) OVER ()::int AS total,
         lag(c.id) OVER (ORDER BY c."order", c.id)::uuid AS previous_id,
         coalesce(lag(c.title) OVER (ORDER BY c."order", c.id), '')::text AS previous_title,
         coalesce(lag(c."order") OVER (ORDER BY c."order", c.id), 0)::int AS previous_order,
         lead(c.id) OVER (ORDER BY c."order", c.id)::uuid AS next_id,
         coalesce(lead(c.title) OVER (ORDER BY c."order", c.id), '')::text AS next_title,
         coalesce(lead(c."order") OVER (ORDER BY c."order", c.id), 0)::int AS next_order
  FROM chapters c
  JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id
  JOIN projects p ON p.id = c.project_id
  WHERE c.project_id = sqlc.arg(project_id)
    AND c.book_id = sqlc.arg(book_id)
    AND c.deleted_at IS NULL
    AND b.deleted_at IS NULL
    AND p.deleted_at IS NULL
)
SELECT sqlc.embed(c),
       nav.position, nav.total,
       nav.previous_id, nav.previous_title, nav.previous_order,
       nav.next_id, nav.next_title, nav.next_order
FROM navigation AS nav
JOIN chapters c ON c.id = nav.id
WHERE c.id = sqlc.arg(chapter_id);

-- name: CreateChapterParagraph :one
INSERT INTO chapter_paragraphs (
  id,
  chapter_id,
  revision,
  variant,
  kind,
  "order",
  markdown,
  plain_text,
  word_count,
  created_at,
  updated_at
)
SELECT
  sqlc.arg(id),
  c.id,
  sqlc.arg(revision),
  sqlc.arg(variant),
  sqlc.arg(kind),
  sqlc.arg(sort_order),
  sqlc.arg(markdown),
  sqlc.arg(plain_text),
  sqlc.arg(word_count),
  sqlc.arg(created_at),
  sqlc.arg(created_at)
FROM chapters c
JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id
JOIN projects pr ON pr.id = c.project_id
WHERE c.project_id = sqlc.arg(project_id)
  AND c.book_id = sqlc.arg(book_id)
  AND c.id = sqlc.arg(chapter_id)
  AND pr.lifecycle_generation = sqlc.arg(project_generation)
  AND b.lifecycle_generation = sqlc.arg(book_generation)
  AND c.lifecycle_generation = sqlc.arg(chapter_generation)
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND pr.deleted_at IS NULL
RETURNING id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at;

-- name: ListChapterParagraphsByChapterRevisionVariant :many
SELECT p.id, p.chapter_id, p.revision, p.variant, p.kind, p."order", p.markdown, p.plain_text, p.word_count, p.created_at, p.updated_at
FROM chapter_paragraphs p
JOIN chapters c ON c.id = p.chapter_id
JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id
JOIN projects pr ON pr.id = c.project_id
WHERE c.project_id = sqlc.arg(project_id)
  AND c.book_id = sqlc.arg(book_id)
  AND p.chapter_id = sqlc.arg(chapter_id)
  AND p.revision = sqlc.arg(revision)
  AND p.variant = sqlc.arg(variant)
  AND c.deleted_at IS NULL
  AND b.deleted_at IS NULL
  AND pr.deleted_at IS NULL
ORDER BY p."order" ASC, p.id ASC;

-- name: StoreImportFile :execrows
INSERT INTO import_uploads (
  storage_key,
  project_id,
  mime_type,
  size_bytes,
  sha256,
  content,
  expires_at,
  created_at,
  artifact_id,
  artifact_source_owner_id
)
SELECT
  sqlc.arg(storage_key),
  p.id,
  sqlc.arg(mime_type),
  sqlc.arg(size_bytes),
  sqlc.arg(sha256),
  sqlc.arg(content),
  sqlc.arg(expires_at),
  sqlc.arg(created_at),
  sqlc.narg(artifact_id),
  sqlc.narg(artifact_source_owner_id)
FROM projects p
WHERE p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL;

-- name: LoadImportBodyForClaim :one
SELECT upload.content
FROM jobs AS job
JOIN import_job_requests AS request
  ON request.job_id = job.id
 AND request.project_id = job.project_id
JOIN import_uploads AS upload
  ON upload.storage_key = request.storage_key
 AND upload.project_id = job.project_id
JOIN projects AS project ON project.id = job.project_id
WHERE job.id = sqlc.arg(job_id)
  AND job.project_id = sqlc.arg(project_id)
  AND job.kind = 'import'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL
  AND request.storage_key = sqlc.arg(storage_key)
  AND request.status = 'pending'
  AND request.governance_payload_state = 'active'
  AND upload.content IS NOT NULL
  AND clock_timestamp() < upload.expires_at
  AND project.deleted_at IS NULL
  AND job.artifact_source_owner_id IS NOT NULL
  AND request.artifact_source_owner_id IS NOT NULL
  AND upload.artifact_source_owner_id IS NOT NULL
  AND ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id,
        job.project_id,
        'job',
        job.id
      )
  AND ck_artifact_source_owner_is_readable(
        request.artifact_source_owner_id,
        request.project_id,
        'import_job_request',
        request.job_id
      )
  AND ck_artifact_source_owner_is_readable(
        upload.artifact_source_owner_id,
        upload.project_id,
        'import_upload',
        upload.artifact_id
      );

-- name: IsImportBodyExpiredForClaim :one
SELECT true
FROM jobs AS job
JOIN import_job_requests AS request
  ON request.job_id = job.id
 AND request.project_id = job.project_id
JOIN import_uploads AS upload
  ON upload.storage_key = request.storage_key
 AND upload.project_id = job.project_id
JOIN projects AS project ON project.id = job.project_id
WHERE job.id = sqlc.arg(job_id)
  AND job.project_id = sqlc.arg(project_id)
  AND job.kind = 'import'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL
  AND request.storage_key = sqlc.arg(storage_key)
  AND request.status = 'pending'
  AND request.governance_payload_state = 'active'
  AND clock_timestamp() >= upload.expires_at
  AND project.deleted_at IS NULL
  AND job.artifact_source_owner_id IS NOT NULL
  AND request.artifact_source_owner_id IS NOT NULL
  AND upload.artifact_source_owner_id IS NOT NULL
  AND ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id,
        job.project_id,
        'job',
        job.id
      )
  AND ck_artifact_source_owner_is_readable(
        request.artifact_source_owner_id,
        request.project_id,
        'import_job_request',
        request.job_id
      )
  AND ck_artifact_source_owner_is_readable(
        upload.artifact_source_owner_id,
        upload.project_id,
        'import_upload',
        upload.artifact_id
      );

-- name: CreateImportJobRecord :one
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
  created_at,
  updated_at,
  artifact_source_owner_id,
  governance_payload_state,
  creator_request_id
)
SELECT
  sqlc.arg(id),
  p.id,
  sqlc.arg(kind),
  sqlc.arg(status),
  sqlc.arg(progress_json)::jsonb,
  jsonb_build_object('type', 'import', 'id', p.id::text, 'projectId', p.id::text)
    || CASE
      WHEN sqlc.narg(book_id)::uuid IS NULL THEN jsonb_build_object('expectedBookOrder', sqlc.arg(expected_book_order)::integer)
      ELSE jsonb_build_object('bookId', sqlc.narg(book_id)::uuid::text)
    END
    || CASE WHEN sqlc.arg(preview)::boolean THEN jsonb_build_object('preview', true) ELSE '{}'::jsonb END,
  NULL,
  NULL,
  sqlc.arg(creator_user_id),
  sqlc.arg(can_cancel),
  'user_demanded',
  0,
  3,
  sqlc.arg(created_at),
  sqlc.arg(created_at),
  sqlc.arg(updated_at),
  sqlc.narg(artifact_source_owner_id),
  CASE
    WHEN sqlc.narg(artifact_source_owner_id)::uuid IS NULL THEN NULL
    ELSE 'active'
  END,
  sqlc.narg(creator_request_id)
FROM projects p
WHERE p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
RETURNING *;

-- name: CreateImportQueueJobRecord :one
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
  created_at,
  updated_at,
  artifact_source_owner_id,
  governance_payload_state,
  creator_request_id
)
SELECT
  sqlc.arg(id),
  p.id,
  sqlc.arg(kind),
  sqlc.arg(status),
  sqlc.arg(progress_json)::jsonb,
  jsonb_build_object(
    'type', 'import',
    'id', p.id::text,
    'projectId', p.id::text,
    'expectedBookOrder', sqlc.arg(expected_book_order)::integer,
    'importQueueId', sqlc.arg(import_queue_id)::text,
    'importQueueItemId', sqlc.arg(import_queue_item_id)::text
  ),
  NULL,
  NULL,
  sqlc.arg(creator_user_id),
  sqlc.arg(can_cancel),
  'user_demanded',
  0,
  3,
  sqlc.arg(created_at),
  sqlc.arg(created_at),
  sqlc.arg(updated_at),
  sqlc.arg(artifact_source_owner_id)::uuid,
  'active',
  sqlc.narg(creator_request_id)
FROM projects p
WHERE p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
RETURNING *;

-- name: CreateImportJobRequest :exec
INSERT INTO import_job_requests (
  job_id,
  project_id,
  actor_user_id,
  storage_key,
  idempotency_key,
  request_method,
  request_path,
  request_fingerprint,
  source_file_name,
  extension,
  mime_type,
  size_bytes,
  file_sha256,
  import_mode,
  target_book_id,
  initial_publication,
  title,
  can_cancel,
  created_at,
  updated_at,
  artifact_source_owner_id
)
VALUES (
  sqlc.arg(job_id),
  sqlc.arg(project_id),
  sqlc.arg(actor_user_id),
  sqlc.arg(storage_key),
  sqlc.arg(idempotency_key),
  sqlc.arg(request_method),
  sqlc.arg(request_path),
  sqlc.arg(request_fingerprint),
  sqlc.arg(source_file_name),
  sqlc.arg(extension),
  sqlc.arg(mime_type),
  sqlc.arg(size_bytes),
  sqlc.arg(file_sha256),
  sqlc.arg(import_mode),
  sqlc.narg(target_book_id),
  sqlc.arg(initial_publication),
  sqlc.arg(title),
  sqlc.arg(can_cancel),
  sqlc.arg(created_at),
  sqlc.arg(updated_at),
  sqlc.narg(artifact_source_owner_id)
);

-- name: GetImportJobByIdempotencyKey :one
SELECT
  j.id,
  j.project_id,
  j.kind,
  j.status,
  j.subject_json,
  j.expires_at,
  j.created_at,
  j.updated_at,
  r.storage_key,
  r.source_file_name,
  r.extension,
  r.mime_type,
  r.size_bytes,
  r.file_sha256,
  r.idempotency_key,
  r.import_mode,
  r.initial_publication,
  r.title,
  r.can_cancel,
  r.request_fingerprint
FROM import_job_requests r
JOIN jobs j
  ON j.id = r.job_id
 AND j.project_id = r.project_id
JOIN import_uploads u
  ON u.storage_key = r.storage_key
 AND u.project_id = r.project_id
JOIN projects p ON p.id = j.project_id
WHERE r.actor_user_id = sqlc.arg(actor_user_id)
  AND r.request_method = sqlc.arg(request_method)
  AND r.request_path = sqlc.arg(request_path)
  AND r.idempotency_key = sqlc.arg(idempotency_key)
  AND j.kind = 'import'
  AND j.governance_payload_state = 'active'
  AND r.governance_payload_state = 'active'
  AND p.deleted_at IS NULL
  AND j.artifact_source_owner_id IS NOT NULL
  AND r.artifact_source_owner_id IS NOT NULL
  AND u.artifact_source_owner_id IS NOT NULL
  AND ck_artifact_source_owner_is_readable(
        j.artifact_source_owner_id,
        j.project_id,
        'job',
        j.id
      )
  AND ck_artifact_source_owner_is_readable(
        r.artifact_source_owner_id,
        r.project_id,
        'import_job_request',
        r.job_id
      )
  AND ck_artifact_source_owner_is_readable(
        u.artifact_source_owner_id,
        u.project_id,
        'import_upload',
        u.artifact_id
      );

-- name: GetImportJobForExecution :one
SELECT
  j.id,
  r.project_id AS request_project_id,
  j.kind,
  j.status,
  j.subject_json,
  j.expires_at,
  j.created_at,
  j.updated_at,
  r.actor_user_id,
  r.storage_key,
  r.source_file_name,
  r.extension,
  r.mime_type,
  r.size_bytes,
  r.file_sha256,
  r.idempotency_key,
  r.import_mode,
  r.target_book_id,
  r.initial_publication,
  r.title,
  r.can_cancel,
  r.request_fingerprint
FROM import_job_requests r
JOIN jobs j ON j.id = r.job_id
JOIN import_uploads u ON u.storage_key = r.storage_key
  AND u.project_id = r.project_id
  AND r.project_id = j.project_id
JOIN projects p ON p.id = j.project_id
JOIN projects rp ON rp.id = r.project_id
WHERE r.job_id = sqlc.arg(job_id)
  AND r.project_id = sqlc.arg(project_id)
  AND j.project_id = sqlc.arg(project_id)
  AND u.project_id = sqlc.arg(project_id)
  AND j.kind = 'import'
  AND j.governance_payload_state = 'active'
  AND r.governance_payload_state = 'active'
  AND p.deleted_at IS NULL
  AND rp.deleted_at IS NULL
  AND j.artifact_source_owner_id IS NOT NULL
  AND r.artifact_source_owner_id IS NOT NULL
  AND u.artifact_source_owner_id IS NOT NULL
  AND ck_artifact_source_owner_is_readable(
        j.artifact_source_owner_id,
        j.project_id,
        'job',
        j.id
      )
  AND ck_artifact_source_owner_is_readable(
        r.artifact_source_owner_id,
        r.project_id,
        'import_job_request',
        r.job_id
      )
  AND ck_artifact_source_owner_is_readable(
        u.artifact_source_owner_id,
        u.project_id,
        'import_upload',
        u.artifact_id
      );

-- name: MarkImportJobQueued :execrows
UPDATE jobs j
SET status = 'queued',
    updated_at = sqlc.arg(enqueued_at)
WHERE j.id = sqlc.arg(job_id)
  AND j.project_id = sqlc.arg(project_id)
  AND EXISTS (
    SELECT 1
    FROM import_job_requests r
    JOIN import_uploads u ON u.storage_key = r.storage_key
      AND u.project_id = r.project_id
      AND r.project_id = j.project_id
    WHERE r.job_id = j.id
      AND r.storage_key = sqlc.arg(storage_key)
  );

-- name: LockImportProgressForClaim :one
SELECT job.progress_json,
       date_trunc('microseconds', clock_timestamp())::timestamptz AS transitioned_at
FROM jobs AS job
JOIN projects AS project ON project.id = job.project_id
WHERE job.id = sqlc.arg(job_id)
  AND job.project_id = sqlc.arg(project_id)
  AND job.kind = 'import'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL
  AND job.governance_payload_state = 'active'
  AND job.subject_json->>'type' = 'import'
  AND job.subject_json->>'projectId' = job.project_id::text
  AND project.deleted_at IS NULL
  AND ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id, job.project_id, 'job', job.id
      )
FOR UPDATE OF job;

-- name: UpdateImportProgressForClaim :execrows
UPDATE jobs AS job
SET progress_json = sqlc.arg(progress_json)::jsonb,
    updated_at = sqlc.arg(transitioned_at)
FROM projects AS project
WHERE job.id = sqlc.arg(job_id)
  AND job.project_id = sqlc.arg(project_id)
  AND job.kind = 'import'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL
  AND job.governance_payload_state = 'active'
  AND job.subject_json->>'type' = 'import'
  AND job.subject_json->>'projectId' = job.project_id::text
  AND job.progress_json = sqlc.arg(expected_progress_json)::jsonb
  AND project.id = job.project_id
  AND project.deleted_at IS NULL
  AND ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id, job.project_id, 'job', job.id
      );

-- name: CreateImportQueueRow :one
INSERT INTO import_queues (
  id, project_id, created_by_user_id, idempotency_key, request_fingerprint, status, version, created_at, updated_at
)
VALUES (
  COALESCE(sqlc.narg(id)::uuid, gen_random_uuid()),
  sqlc.arg(project_id),
  sqlc.arg(created_by_user_id),
  sqlc.arg(idempotency_key),
  sqlc.arg(request_fingerprint),
  'uploading',
  1,
  sqlc.arg(created_at),
  sqlc.arg(created_at)
)
RETURNING *;

-- name: LoadImportQueueByIdempotencyKey :one
SELECT q.*
FROM import_queues AS q
JOIN projects AS project ON project.id = q.project_id
WHERE q.project_id = sqlc.arg(project_id)
  AND q.created_by_user_id = sqlc.arg(created_by_user_id)
  AND q.idempotency_key = sqlc.arg(idempotency_key)
  AND project.deleted_at IS NULL;

-- name: CreateImportQueueItemRow :one
INSERT INTO import_queue_items (
  id, queue_id, project_id, client_item_id, position, source_file_name,
  detected_book_order, preflight_status, created_at, updated_at
)
VALUES (
  COALESCE(sqlc.narg(id)::uuid, gen_random_uuid()),
  sqlc.arg(queue_id),
  sqlc.arg(project_id),
  sqlc.arg(client_item_id),
  sqlc.arg(position),
  sqlc.arg(source_file_name),
  sqlc.narg(detected_book_order),
  'uploading',
  sqlc.arg(created_at),
  sqlc.arg(created_at)
)
RETURNING id, queue_id, project_id, client_item_id, position, source_file_name,
          storage_key, mime_type, size_bytes, sha256, detected_book_order,
          confirmed_book_order, preflight_status, preflight_error_json,
          import_job_id, book_id, created_at, updated_at;

-- name: LoadImportQueueForActor :one
SELECT q.*
FROM import_queues AS q
JOIN projects AS project ON project.id = q.project_id
JOIN project_memberships AS membership
  ON membership.project_id = q.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE q.id = sqlc.arg(queue_id)
  AND q.project_id = sqlc.arg(project_id)
  AND project.deleted_at IS NULL;

-- name: ListImportQueueRowsForActor :many
SELECT q.*
FROM import_queues AS q
JOIN projects AS project ON project.id = q.project_id
JOIN project_memberships AS membership
  ON membership.project_id = q.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE q.project_id = sqlc.arg(project_id)
  AND project.deleted_at IS NULL
ORDER BY q.updated_at DESC, q.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListImportQueueItems :many
SELECT item.id, item.queue_id, item.project_id, item.client_item_id, item.position, item.source_file_name,
       item.storage_key, item.mime_type, item.size_bytes, item.sha256, item.detected_book_order,
       item.confirmed_book_order, item.preflight_status, item.preflight_error_json,
       item.import_job_id, item.book_id, item.created_at, item.updated_at,
       COALESCE(import_job.status, '')::text AS import_job_status,
       COALESCE(import_job.retry_count, 0)::integer AS import_job_retry_count,
       import_job.error_json AS import_job_error_json
FROM import_queue_items AS item
JOIN import_queues AS queue
  ON queue.id = item.queue_id
 AND queue.project_id = item.project_id
LEFT JOIN jobs AS import_job
  ON import_job.id = item.import_job_id
 AND import_job.project_id = item.project_id
WHERE item.queue_id = sqlc.arg(queue_id)
  AND item.project_id = sqlc.arg(project_id)
ORDER BY item.position, item.id;

-- name: ListImportQueueItemsForStart :many
SELECT item.id, item.queue_id, item.project_id, item.client_item_id, item.position,
       item.source_file_name, item.storage_key, item.mime_type, item.size_bytes, item.sha256,
       item.detected_book_order, item.confirmed_book_order, item.preflight_status,
       item.preflight_error_json, item.import_job_id, item.book_id, item.created_at, item.updated_at,
       COALESCE(upload.content IS NOT NULL, false)::boolean AS upload_present,
       COALESCE(upload.size_bytes = item.size_bytes AND octet_length(upload.content) = item.size_bytes, false)::boolean AS upload_size_matches,
       COALESCE(
         upload.sha256 = item.sha256
         AND upload.sha256 = encode(sha256(upload.content), 'hex'),
         false
       )::boolean AS upload_checksum_matches,
       COALESCE(ck_artifact_source_owner_is_readable(
         upload.artifact_source_owner_id, upload.project_id, 'import_upload', upload.artifact_id
       ), false)::boolean AS upload_owner_active,
       request.idempotency_key AS request_idempotency_key,
       request.request_method AS request_method,
       request.request_path AS request_path,
       request.request_fingerprint AS request_fingerprint,
       COALESCE(job.subject_json->>'importQueueId', '')::text AS subject_queue_id,
       COALESCE(job.subject_json->>'importQueueItemId', '')::text AS subject_item_id,
       COALESCE((job.subject_json->>'expectedBookOrder')::integer, 0)::integer AS subject_book_order
FROM import_queue_items AS item
LEFT JOIN import_uploads AS upload
  ON upload.storage_key = item.storage_key
 AND upload.project_id = item.project_id
LEFT JOIN jobs AS job
  ON job.id = item.import_job_id
 AND job.project_id = item.project_id
LEFT JOIN import_job_requests AS request
  ON request.job_id = job.id
 AND request.project_id = item.project_id
WHERE item.queue_id = sqlc.arg(queue_id)
  AND item.project_id = sqlc.arg(project_id)
ORDER BY item.position, item.id
FOR UPDATE OF item;

-- name: LoadImportQueueItemForUpdate :one
SELECT id, queue_id, project_id, client_item_id, position, source_file_name,
       storage_key, mime_type, size_bytes, sha256, detected_book_order,
       confirmed_book_order, preflight_status, preflight_error_json,
       import_job_id, book_id, created_at, updated_at
FROM import_queue_items AS item
WHERE item.id = sqlc.arg(item_id)
  AND item.queue_id = sqlc.arg(queue_id)
  AND item.project_id = sqlc.arg(project_id)
FOR UPDATE;

-- name: UpdateImportQueueItemUpload :execrows
UPDATE import_queue_items
SET storage_key = sqlc.arg(storage_key),
    source_file_name = sqlc.arg(source_file_name),
    mime_type = sqlc.arg(mime_type),
    size_bytes = sqlc.arg(size_bytes),
    sha256 = sqlc.arg(sha256),
    detected_book_order = sqlc.narg(detected_book_order),
    confirmed_book_order = sqlc.narg(confirmed_book_order),
    preflight_status = sqlc.arg(preflight_status),
    preflight_error_json = sqlc.narg(preflight_error_json)::jsonb,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(item_id)
  AND queue_id = sqlc.arg(queue_id)
  AND project_id = sqlc.arg(project_id);

-- name: LinkImportQueueItemJob :execrows
UPDATE import_queue_items
SET import_job_id = sqlc.arg(job_id),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(item_id)
  AND queue_id = sqlc.arg(queue_id)
  AND project_id = sqlc.arg(project_id)
  AND import_job_id IS NULL;

-- name: ListImportQueueLinkedJobsForCancel :many
SELECT job.id, job.kind, job.status
FROM import_queue_items AS item
JOIN import_queues AS queue
  ON queue.id = item.queue_id
 AND queue.project_id = item.project_id
JOIN jobs AS job
  ON job.project_id = item.project_id
 AND job.id = item.import_job_id
WHERE item.queue_id = sqlc.arg(queue_id)
  AND item.project_id = sqlc.arg(project_id)
ORDER BY item.position, item.id
FOR UPDATE OF job;

-- name: SaveImportQueueVersion :one
UPDATE import_queues
SET version = version + 1,
    status = CASE
      WHEN status = 'uploading' AND NOT EXISTS (
        SELECT 1
        FROM import_queue_items
        WHERE queue_id = import_queues.id
          AND project_id = import_queues.project_id
          AND (storage_key IS NULL OR preflight_status <> 'ready')
      ) THEN 'draft'
      ELSE status
    END,
    updated_at = sqlc.arg(updated_at)
WHERE import_queues.id = sqlc.arg(queue_id)
  AND import_queues.project_id = sqlc.arg(project_id)
  AND import_queues.version = sqlc.arg(expected_version)
  AND import_queues.status IN ('uploading', 'draft')
RETURNING *;

-- name: CancelImportQueueRow :one
UPDATE import_queues
SET status = 'canceled',
    version = version + 1,
    updated_at = sqlc.arg(updated_at),
    completed_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(queue_id)
  AND project_id = sqlc.arg(project_id)
  AND version = sqlc.arg(expected_version)
  AND status IN ('uploading', 'draft', 'running', 'failed')
RETURNING *;

-- name: StartImportQueueRow :one
UPDATE import_queues
SET status = 'running',
    version = version + 1,
    updated_at = sqlc.arg(updated_at),
    started_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(queue_id)
  AND project_id = sqlc.arg(project_id)
  AND version = sqlc.arg(expected_version)
  AND status = 'draft'
RETURNING *;

-- name: ScrubImportQueuePendingUploads :execrows
UPDATE import_uploads AS upload
SET content = NULL
WHERE upload.project_id = sqlc.arg(project_id)
  AND upload.content IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM import_queue_items AS item
    WHERE item.queue_id = sqlc.arg(queue_id)
      AND item.project_id = upload.project_id
      AND item.storage_key = upload.storage_key
      AND item.import_job_id IS NULL
  );

-- name: ScrubImportQueueItemUpload :execrows
UPDATE import_uploads
SET content = NULL
WHERE project_id = sqlc.arg(project_id)
  AND storage_key = sqlc.arg(storage_key)
  AND content IS NOT NULL;

-- name: GetBookHeadForActor :one
SELECT book.project_id, book.title, membership.role, owner.display_name AS owner_name,
       COALESCE(cover.version, 0)::bigint AS cover_version
FROM books AS book
JOIN projects AS project ON project.id = book.project_id
JOIN project_memberships AS membership
  ON membership.project_id = book.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
JOIN users AS owner ON owner.id = project.owner_user_id
LEFT JOIN LATERAL (
  SELECT artifact.version
  FROM book_cover_artifacts AS artifact
  WHERE artifact.book_id = book.id
    AND artifact.book_lifecycle_generation = book.lifecycle_generation
    AND artifact.state = 'active'
  LIMIT 1
) cover ON true
WHERE book.id = sqlc.arg(book_id)
  AND book.deleted_at IS NULL
  AND project.deleted_at IS NULL;

-- name: ListBookExportChapters :many
SELECT c.id, c.title, c.published_revision::bigint AS published_revision
FROM chapters c
WHERE c.project_id = sqlc.arg(project_id)
  AND c.book_id = sqlc.arg(book_id)
  AND c.deleted_at IS NULL
  AND c.published_revision IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM chapter_paragraphs p
    WHERE p.chapter_id = c.id
      AND p.revision = c.published_revision
      AND p.variant = 'published'
      AND p.plain_text <> ''
  )
ORDER BY c."order" ASC, c.id ASC;
