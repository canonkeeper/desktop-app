-- name: ListAuthorMemory :many
WITH filtered AS (
  SELECT record.id, record.project_id, record.kind, record.body, record.chapter_id, record.name, record.revision,
         record.created_by, record.created_at, record.updated_at
  FROM author_memory AS record
  WHERE record.project_id = sqlc.arg(project_id)
    AND (sqlc.arg(kinds)::text[] = '{}' OR record.kind = ANY(sqlc.arg(kinds)::text[]))
    AND (sqlc.narg(job_id)::uuid IS NULL OR record.job_id = sqlc.narg(job_id)::uuid)
    AND (sqlc.arg(search_query)::text = ''
      OR strpos(lower(record.body), lower(sqlc.arg(search_query))) > 0
      OR strpos(lower(coalesce(record.name, '')), lower(sqlc.arg(search_query))) > 0)
)
SELECT record.id, record.project_id, record.kind, record.body, record.chapter_id, record.name, record.revision,
       record.created_by, record.created_at, record.updated_at, author.display_name AS author_name,
       (SELECT count(*) FROM filtered) AS total_count
FROM filtered AS record
JOIN users AS author ON author.id = record.created_by
WHERE NOT sqlc.arg(has_cursor)::boolean
  OR (record.created_at, record.id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY record.created_at DESC, record.id DESC
LIMIT sqlc.arg(limit_count);

-- name: CreateAuthorMemory :one
INSERT INTO author_memory (project_id, kind, body, chapter_id, name, job_id, created_by, updated_by)
VALUES (sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(body), sqlc.narg(chapter_id), sqlc.narg(name), sqlc.narg(job_id), sqlc.arg(actor), sqlc.arg(actor))
RETURNING id;

-- name: GetAuthorMemory :one
SELECT record.id, record.project_id, record.kind, record.body, record.chapter_id, record.name, record.revision,
       record.created_by, record.created_at, record.updated_at, author.display_name AS author_name
FROM author_memory AS record
JOIN users AS author ON author.id = record.created_by
WHERE record.project_id = sqlc.arg(project_id) AND record.id = sqlc.arg(id);

-- name: LockAuthorMemoryRevision :one
SELECT revision FROM author_memory
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateAuthorMemory :execrows
UPDATE author_memory
SET kind = sqlc.arg(kind), body = sqlc.arg(body), chapter_id = sqlc.narg(chapter_id), name = sqlc.narg(name),
    revision = revision + 1, updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision);

-- name: DeleteAuthorMemory :execrows
DELETE FROM author_memory
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision);
