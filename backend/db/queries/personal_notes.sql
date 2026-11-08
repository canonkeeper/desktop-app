-- name: GetPersonalNote :one
SELECT * FROM personal_notes WHERE owner_user_id = sqlc.arg(owner_user_id) AND id = sqlc.arg(id);

-- name: LockPersonalNote :one
SELECT * FROM personal_notes WHERE owner_user_id = sqlc.arg(owner_user_id) AND id = sqlc.arg(id) FOR UPDATE;

-- name: CreatePersonalNote :one
INSERT INTO personal_notes (owner_user_id, title, body, source_json)
VALUES (sqlc.arg(owner_user_id), sqlc.arg(title), sqlc.arg(body), sqlc.narg(source_json))
RETURNING *;

-- name: UpdatePersonalNote :one
UPDATE personal_notes SET body = coalesce(sqlc.narg(body),body), title = coalesce(sqlc.narg(title),title),
 revision = revision + 1, updated_at = clock_timestamp()
WHERE owner_user_id = sqlc.arg(owner_user_id) AND id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: DeletePersonalNote :execrows
DELETE FROM personal_notes WHERE owner_user_id = sqlc.arg(owner_user_id) AND id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision);

-- name: ListNotes :one
WITH readable_projects AS (
 SELECT project.id, project.name, project.lifecycle_generation, membership.role
 FROM projects AS project
 JOIN project_memberships AS membership ON membership.project_id=project.id
  AND membership.user_id=sqlc.arg(actor_user_id) AND membership.removed_at IS NULL
 WHERE project.deleted_at IS NULL
), personal AS (
 SELECT note.*, coalesce(note.source_json#>>'{source,kind}','free') AS source_kind
 FROM personal_notes AS note WHERE note.owner_user_id=sqlc.arg(actor_user_id)
), entries AS (
 SELECT note.id,note.updated_at,note.source_kind,
  note.title,note.body AS search_body,left(note.body,240) AS preview,
  project.id AS project_id,CASE WHEN project.id IS NOT NULL THEN book.id END AS book_id,
  project.name AS project_title,CASE WHEN project.id IS NOT NULL THEN book.title END AS book_title,
  CASE WHEN project.id IS NOT NULL THEN chapter.title END AS chapter_title
 FROM personal AS note
 LEFT JOIN chat_sessions AS chat ON note.source_kind='chat'
  AND chat.id=(note.source_json#>>'{source,chatId}')::uuid AND chat.deleted_at IS NULL
  AND chat.scope='project' AND chat.book_id IS NULL
  AND chat.lifecycle_epoch=(note.source_json#>>'{chat,epoch}')::bigint
  AND chat.thread_id=note.source_json#>>'{chat,threadId}'
 LEFT JOIN chapters AS chapter ON note.source_kind='chapter'
  AND chapter.id=(note.source_json#>>'{source,locator,chapterId}')::uuid
  AND chapter.book_id=(note.source_json#>>'{source,locator,bookId}')::uuid
  AND chapter.project_id=(note.source_json#>>'{source,locator,projectId}')::uuid
  AND chapter.deleted_at IS NULL AND chapter.published_revision IS NOT NULL
 LEFT JOIN books AS book ON book.id=chapter.book_id AND book.project_id=chapter.project_id AND book.deleted_at IS NULL
 LEFT JOIN readable_projects AS project ON project.id=coalesce(chat.project_id,book.project_id)
  AND (
   (note.source_kind='chat'
    AND (chat.source_view='published' OR project.role IN ('owner','admin','editor'))
    AND EXISTS (SELECT 1 FROM project_source_views AS view
     WHERE view.project_id=project.id AND view.source_view=chat.source_view
      AND view.project_lifecycle_generation=project.lifecycle_generation))
   OR (note.source_kind='chapter' AND book.id IS NOT NULL
    AND encode(ck_source_generation_digest(project.id,project.lifecycle_generation,book.id,book.lifecycle_generation,chapter.id,chapter.lifecycle_generation),'hex')
      =note.source_json#>>'{chapter,sourceGeneration}')
  )
), filtered AS (
 SELECT id,updated_at,source_kind,title,preview,project_title,book_title,chapter_title
 FROM entries
 WHERE (sqlc.arg(search_query)::text='' OR strpos(lower(title),lower(sqlc.arg(search_query)))>0
  OR strpos(lower(search_body),lower(sqlc.arg(search_query)))>0)
  AND (sqlc.arg(source_kind)::text='' OR source_kind=sqlc.arg(source_kind))
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id=sqlc.narg(project_id))
  AND (sqlc.narg(book_id)::uuid IS NULL OR book_id=sqlc.narg(book_id))
), page AS (
 SELECT id,updated_at,
  jsonb_build_object('id',id,'sourceKind',source_kind,'title',title,'preview',preview,'updatedAt',updated_at)
   ||jsonb_strip_nulls(jsonb_build_object('projectTitle',project_title,'bookTitle',book_title,'chapterTitle',chapter_title)) AS payload
 FROM filtered
 WHERE NOT sqlc.arg(has_cursor)::boolean
  OR (updated_at,id)<(sqlc.arg(cursor_updated_at)::timestamptz,sqlc.arg(cursor_id)::uuid)
 ORDER BY updated_at DESC,id DESC LIMIT sqlc.arg(limit_count)
)
SELECT (SELECT count(*) FROM filtered) AS total_count,
 coalesce((SELECT jsonb_agg(payload ORDER BY updated_at DESC,id DESC) FROM page),'[]'::jsonb)::jsonb AS entries;
