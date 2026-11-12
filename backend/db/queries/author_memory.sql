-- name: ListAuthorMemory :many
WITH filtered AS (
  SELECT record.id, record.project_id, record.kind, record.body, record.chapter_id, record.name, record.revision,
         record.created_by, record.created_at, record.updated_at, record.job_id
  FROM author_memory AS record
  WHERE record.project_id = sqlc.arg(project_id)
    AND (sqlc.arg(kinds)::text[] = '{}' OR record.kind = ANY(sqlc.arg(kinds)::text[]))
    AND (sqlc.narg(job_id)::uuid IS NULL OR record.job_id = sqlc.narg(job_id)::uuid)
    AND (sqlc.arg(search_query)::text = ''
      OR strpos(lower(record.body), lower(sqlc.arg(search_query))) > 0
      OR strpos(lower(coalesce(record.name, '')), lower(sqlc.arg(search_query))) > 0)
)
SELECT record.id, record.project_id, record.kind, record.body, record.chapter_id, record.name, record.revision,
       record.created_by, record.created_at, record.updated_at, record.job_id, author.display_name AS author_name,
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
       record.created_by, record.created_at, record.updated_at, record.job_id, author.display_name AS author_name
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

-- name: ListAuthorMemoryConversations :many
-- What is left of the conversations the records of a page were made in (specs/2026-11-10-ux8-memory-records.md MR1.5):
-- the session of the turn of each Job, alive or deleted, and the title of the draft its helper writes. A chat of the draft
-- text version is named only to an editor, as the list of chats does.
SELECT turn.job_id, chat.id AS chat_id, chat.kind AS session_kind, chat.title AS chat_title,
       (chat.deleted_at IS NULL)::boolean AS alive,
       (chat.source_view = 'published' OR sqlc.arg(editor)::boolean)::boolean AS visible,
       coalesce((SELECT draft.title FROM chapter_drafts AS draft
                 WHERE draft.project_id = chat.project_id AND draft.chat_id = chat.id
                 ORDER BY draft.created_at, draft.id LIMIT 1), '')::text AS draft_title
FROM chat_turns AS turn
JOIN chat_sessions AS chat ON chat.id = turn.chat_id AND chat.project_id = turn.project_id
WHERE turn.project_id = sqlc.arg(project_id) AND turn.job_id = ANY(sqlc.arg(job_ids)::uuid[]);

-- name: CountAuthorMemoryMembers :one
-- How many members the saga has now: the page signs a record with its author only in a saga of several (MR1.5).
SELECT count(*) FROM project_memberships AS member
WHERE member.project_id = sqlc.arg(project_id) AND member.removed_at IS NULL;
