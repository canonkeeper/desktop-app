-- name: GetClaimedCanonTextIndexJob :one
SELECT
  job.project_id,
  job.id AS job_id,
  job.claim_incarnation,
  (job.subject_json->>'sourceView')::text AS source_view,
  (job.subject_json->>'catalogRevision')::bigint AS catalog_revision
FROM projects AS project
JOIN jobs AS job
  ON job.project_id = project.id
JOIN project_source_views AS marker
  ON marker.project_id = project.id
 AND marker.project_lifecycle_generation = project.lifecycle_generation
 AND marker.source_view = job.subject_json->>'sourceView'
 AND marker.catalog_revision = (job.subject_json->>'catalogRevision')::bigint
JOIN artifact_source_owners AS owner
  ON owner.id = job.artifact_source_owner_id
 AND owner.project_id = job.project_id
 AND owner.artifact_kind = 'job'
 AND owner.artifact_id = job.id
 AND owner.read_state = 'active'
 AND owner.fenced_at IS NULL
 AND owner.source_view = job.subject_json->>'sourceView'
 AND owner.manifest_version = 1
 AND owner.source_identity_state = 'scope_only'
WHERE project.id = sqlc.arg(project_id)
  AND project.deleted_at IS NULL
  AND job.id = sqlc.arg(job_id)
  AND job.kind = 'canon_text_index'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL
  AND job.governance_payload_state = 'active'
  AND job.subject_json->>'projectId' = job.project_id::text
  AND job.subject_json->>'sourceView' IN ('draft', 'published')
  AND EXISTS (
    SELECT 1 FROM artifact_source_refs AS ref
    WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
      AND ref.ref_kind = 'scope' AND ref.scope_kind = 'project'
      AND ref.project_generation = project.lifecycle_generation
      AND ref.source_view = job.subject_json->>'sourceView'
      AND NOT ref.source_independent
  );

-- name: ListClaimedCanonTextChapterDescriptors :many
WITH claimed AS (
  SELECT
    job.project_id,
    job.subject_json->>'sourceView' AS source_view,
    (job.subject_json->>'catalogRevision')::bigint AS catalog_revision
  FROM projects AS project
  JOIN jobs AS job ON job.project_id = project.id
  JOIN artifact_source_owners AS owner
    ON owner.id = job.artifact_source_owner_id
   AND owner.project_id = job.project_id
   AND owner.artifact_kind = 'job'
   AND owner.artifact_id = job.id
   AND owner.read_state = 'active'
   AND owner.fenced_at IS NULL
   AND owner.source_view = job.subject_json->>'sourceView'
   AND owner.manifest_version = 1
  WHERE project.id = sqlc.arg(project_id)
    AND project.deleted_at IS NULL
    AND job.id = sqlc.arg(job_id)
    AND job.kind = 'canon_text_index'
    AND job.status = 'running'
    AND job.claim_incarnation = sqlc.arg(claim_incarnation)
    AND job.cancel_requested_at IS NULL
    AND job.governance_payload_state = 'active'
    AND job.subject_json->>'projectId' = job.project_id::text
)
SELECT
  member.project_id,
  member.project_lifecycle_generation,
  member.source_view,
  marker.catalog_revision,
  member.book_id,
  member.chapter_id,
  member.source_revision,
  ck_source_generation_digest(
    member.project_id, member.project_lifecycle_generation,
    member.book_id, member.book_lifecycle_generation,
    member.chapter_id, member.chapter_lifecycle_generation
  ) AS source_generation
FROM claimed
JOIN project_source_views AS marker
  ON marker.project_id = claimed.project_id
 AND marker.source_view = claimed.source_view
 AND marker.catalog_revision = claimed.catalog_revision
JOIN chapter_source_members AS member
  ON member.project_id = marker.project_id
 AND member.source_view = marker.source_view
 AND member.project_lifecycle_generation = marker.project_lifecycle_generation
JOIN books AS book
  ON book.id = member.book_id
 AND book.project_id = member.project_id
 AND book.lifecycle_generation = member.book_lifecycle_generation
 AND book.deleted_at IS NULL
JOIN chapters AS chapter
  ON chapter.id = member.chapter_id
 AND chapter.project_id = member.project_id
 AND chapter.book_id = member.book_id
 AND chapter.lifecycle_generation = member.chapter_lifecycle_generation
 AND chapter.deleted_at IS NULL
ORDER BY book."order", member.chapter_order, member.chapter_id;

-- name: LoadClaimedCanonTextChapterSnapshot :many
WITH claimed AS (
  SELECT job.project_id
  FROM projects AS project
  JOIN jobs AS job ON job.project_id = project.id
  JOIN artifact_source_owners AS owner
    ON owner.id = job.artifact_source_owner_id
   AND owner.project_id = job.project_id
   AND owner.artifact_kind = 'job'
   AND owner.artifact_id = job.id
   AND owner.read_state = 'active'
   AND owner.fenced_at IS NULL
   AND owner.source_view = job.subject_json->>'sourceView'
   AND owner.manifest_version = 1
  WHERE project.id = sqlc.arg(project_id)
    AND project.deleted_at IS NULL
    AND job.id = sqlc.arg(job_id)
    AND job.kind = 'canon_text_index'
    AND job.status = 'running'
    AND job.claim_incarnation = sqlc.arg(claim_incarnation)
    AND job.cancel_requested_at IS NULL
    AND job.governance_payload_state = 'active'
    AND job.subject_json->>'projectId' = job.project_id::text
    AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
    AND (job.subject_json->>'catalogRevision')::bigint = sqlc.arg(catalog_revision)::bigint
)
SELECT
  paragraph.id AS paragraph_id,
  paragraph."order" AS paragraph_order,
  paragraph.plain_text AS paragraph_content,
  state.chapter_content_hash AS indexed_chapter_content_hash
FROM claimed
JOIN project_source_views AS marker
  ON marker.project_id = claimed.project_id
 AND marker.source_view = sqlc.arg(source_view)
 AND marker.project_lifecycle_generation = sqlc.arg(project_lifecycle_generation)
 AND marker.catalog_revision = sqlc.arg(catalog_revision)
JOIN chapter_source_members AS member
  ON member.project_id = marker.project_id
 AND member.source_view = marker.source_view
 AND member.project_lifecycle_generation = marker.project_lifecycle_generation
 AND member.book_id = sqlc.arg(book_id)
 AND member.chapter_id = sqlc.arg(chapter_id)
 AND member.source_revision = sqlc.arg(source_revision)
 AND ck_source_generation_digest(
   member.project_id, member.project_lifecycle_generation,
   member.book_id, member.book_lifecycle_generation,
   member.chapter_id, member.chapter_lifecycle_generation
 ) = sqlc.arg(source_generation)::bytea
LEFT JOIN chapter_paragraphs AS paragraph
  ON paragraph.chapter_id = member.chapter_id
 AND paragraph.revision = member.source_revision
 AND paragraph.variant = member.source_view
LEFT JOIN chapter_text_index_state AS state
  ON state.project_id = member.project_id
 AND state.source_view = member.source_view
 AND state.chapter_id = member.chapter_id
 AND state.source_revision = member.source_revision
 AND state.locator_source_generation = sqlc.arg(source_generation)::bytea
ORDER BY paragraph."order", paragraph.id;

-- name: LockMatchingCanonTextChapterState :one
SELECT state.indexed_at
FROM projects AS project
JOIN chapter_text_index_state AS state ON state.project_id = project.id
WHERE project.id = sqlc.arg(project_id)
  AND state.source_view = sqlc.arg(source_view)
  AND state.chapter_id = sqlc.arg(chapter_id)
  AND state.source_revision = sqlc.arg(source_revision)
  AND state.locator_source_generation = sqlc.arg(source_generation)
  AND state.chapter_content_hash = sqlc.arg(chapter_content_hash)
FOR UPDATE OF state;

-- name: GetClaimedCanonTextChapterSourceTuple :one
SELECT
  member.book_lifecycle_generation,
  member.chapter_lifecycle_generation,
  chapter.title,
  octet_length(to_tsvector('russian', sqlc.arg(content)::text)::text)::bigint AS search_vector_bytes
FROM projects AS project
JOIN jobs AS job
  ON job.project_id = project.id
 AND job.id = sqlc.arg(job_id)
 AND job.kind = 'canon_text_index'
 AND job.status = 'running'
 AND job.claim_incarnation = sqlc.arg(claim_incarnation)
 AND job.cancel_requested_at IS NULL
 AND job.subject_json->>'projectId' = job.project_id::text
 AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
 AND (job.subject_json->>'catalogRevision')::bigint = sqlc.arg(catalog_revision)::bigint
JOIN artifact_source_owners AS owner
  ON owner.id = job.artifact_source_owner_id
 AND owner.project_id = job.project_id
 AND owner.artifact_kind = 'job'
 AND owner.artifact_id = job.id
 AND owner.read_state = 'active'
 AND owner.fenced_at IS NULL
JOIN books AS book
  ON book.project_id = project.id
 AND book.id = sqlc.arg(book_id)
 AND book.deleted_at IS NULL
JOIN chapters AS chapter
  ON chapter.project_id = project.id
 AND chapter.book_id = book.id
 AND chapter.id = sqlc.arg(chapter_id)
 AND chapter.deleted_at IS NULL
JOIN project_source_views AS marker
  ON marker.project_id = project.id
 AND marker.source_view = sqlc.arg(source_view)
 AND marker.project_lifecycle_generation = project.lifecycle_generation
 AND marker.catalog_revision = sqlc.arg(catalog_revision)
JOIN chapter_source_members AS member
  ON member.project_id = marker.project_id
 AND member.source_view = marker.source_view
 AND member.project_lifecycle_generation = marker.project_lifecycle_generation
 AND member.book_id = book.id
 AND member.book_lifecycle_generation = book.lifecycle_generation
 AND member.chapter_id = chapter.id
 AND member.chapter_lifecycle_generation = chapter.lifecycle_generation
 AND member.source_revision = sqlc.arg(source_revision)
WHERE project.id = sqlc.arg(project_id)
  AND project.lifecycle_generation = sqlc.arg(project_lifecycle_generation)
  AND project.deleted_at IS NULL
  AND ck_source_generation_digest(
    project.id, project.lifecycle_generation,
    book.id, book.lifecycle_generation,
    chapter.id, chapter.lifecycle_generation
  ) = sqlc.arg(source_generation)::bytea
;

-- name: LockExactCanonTextChapterCatalogMember :one
SELECT member.chapter_id
FROM projects AS project
JOIN project_source_views AS marker
  ON marker.project_id = project.id
 AND marker.source_view = sqlc.arg(source_view)
 AND marker.project_lifecycle_generation = sqlc.arg(project_lifecycle_generation)
 AND marker.catalog_revision = sqlc.arg(catalog_revision)
JOIN chapter_source_members AS member
  ON member.project_id = marker.project_id
 AND member.source_view = marker.source_view
 AND member.project_lifecycle_generation = marker.project_lifecycle_generation
 AND member.book_id = sqlc.arg(book_id)
 AND member.book_lifecycle_generation = sqlc.arg(book_lifecycle_generation)
 AND member.chapter_id = sqlc.arg(chapter_id)
 AND member.chapter_lifecycle_generation = sqlc.arg(chapter_lifecycle_generation)
 AND member.source_revision = sqlc.arg(source_revision)
WHERE project.id = sqlc.arg(project_id)
  AND project.lifecycle_generation = sqlc.arg(project_lifecycle_generation)
  AND project.deleted_at IS NULL
  AND ck_source_generation_digest(
    member.project_id, member.project_lifecycle_generation,
    member.book_id, member.book_lifecycle_generation,
    member.chapter_id, member.chapter_lifecycle_generation
  ) = sqlc.arg(source_generation)::bytea
FOR SHARE OF marker, member;

-- name: LockClaimedCanonTextIndexJob :one
SELECT job.id
FROM projects AS project
JOIN jobs AS job ON job.project_id = project.id
JOIN artifact_source_owners AS owner
  ON owner.id = job.artifact_source_owner_id
 AND owner.project_id = job.project_id
 AND owner.artifact_kind = 'job'
 AND owner.artifact_id = job.id
 AND owner.read_state = 'active'
 AND owner.fenced_at IS NULL
 AND owner.source_view = job.subject_json->>'sourceView'
 AND owner.manifest_version = 1
WHERE project.id = sqlc.arg(project_id)
  AND project.deleted_at IS NULL
  AND job.id = sqlc.arg(job_id)
  AND job.kind = 'canon_text_index'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL
  AND job.governance_payload_state = 'active'
  AND job.subject_json->>'projectId' = job.project_id::text
  AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
  AND (job.subject_json->>'catalogRevision')::bigint = sqlc.arg(catalog_revision)::bigint
FOR SHARE OF job;

-- name: UpsertCanonTextChapterState :exec
INSERT INTO chapter_text_index_state (
  project_id, source_view, chapter_id, source_revision,
  locator_source_generation, chapter_content_hash, indexed_at
) VALUES (
  sqlc.arg(project_id), sqlc.arg(source_view), sqlc.arg(chapter_id), sqlc.arg(source_revision),
  sqlc.arg(source_generation), sqlc.arg(chapter_content_hash), sqlc.arg(indexed_at)
)
ON CONFLICT (project_id, source_view, chapter_id) DO UPDATE SET
  source_revision = EXCLUDED.source_revision,
  locator_source_generation = EXCLUDED.locator_source_generation,
  chapter_content_hash = EXCLUDED.chapter_content_hash,
  indexed_at = EXCLUDED.indexed_at;

-- name: UpsertCanonTextChapterSearchRow :exec
INSERT INTO chapter_text_search_rows (
  project_id, source_view, book_id, chapter_id, source_revision,
  locator_source_generation, title, content, chapter_content_hash, indexed_at
)
VALUES (
  sqlc.arg(project_id), sqlc.arg(source_view), sqlc.arg(book_id), sqlc.arg(chapter_id),
  sqlc.arg(source_revision), sqlc.arg(source_generation), sqlc.arg(title),
  sqlc.arg(content), sqlc.arg(chapter_content_hash), sqlc.arg(indexed_at)
)
ON CONFLICT (project_id, source_view, chapter_id) DO UPDATE SET
  book_id = EXCLUDED.book_id,
  source_revision = EXCLUDED.source_revision,
  locator_source_generation = EXCLUDED.locator_source_generation,
  title = EXCLUDED.title,
  content = EXCLUDED.content,
  chapter_content_hash = EXCLUDED.chapter_content_hash,
  indexed_at = EXCLUDED.indexed_at;
