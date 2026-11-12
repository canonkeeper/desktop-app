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
-- The catalog of the notes of an account (specs/2026-11-11-ux7-notes.md NT1.3, NT3.2): a note of a place belongs to the
-- saga it was written in while the author is in it, also when its chapter or chat is gone; its place — the chapter of the
-- same lifecycle or the readable chat — is named only while it lives. A saga filter keeps the own thoughts, which belong
-- to no saga; the search reads the thought, the title and the quote.
WITH readable_projects AS (
 SELECT project.id, project.name, project.lifecycle_generation, membership.role
 FROM projects AS project
 JOIN project_memberships AS membership ON membership.project_id=project.id
  AND membership.user_id=sqlc.arg(actor_user_id) AND membership.removed_at IS NULL
 WHERE project.deleted_at IS NULL
), personal AS (
 SELECT note.*, coalesce(note.source_json#>>'{source,kind}','free') AS source_kind,
  coalesce(note.source_json#>>'{source,quote}','') AS quote,
  coalesce(note.source_json#>>'{source,locator,projectId}',note.source_json#>>'{chat,projectId}')::uuid AS saga_id
 FROM personal_notes AS note WHERE note.owner_user_id=sqlc.arg(actor_user_id)
), entries AS (
 SELECT note.id,note.updated_at,note.source_kind,
  note.title,note.body AS search_body,note.quote AS search_quote,left(note.body,240) AS preview,left(note.quote,240) AS quote_preview,
  saga.id AS project_id,saga.name AS project_title,
  book.id AS book_id,book.title AS book_title,
  place.title AS chapter_title,place."order" AS chapter_order,
  chat.title AS chat_title
 FROM personal AS note
 LEFT JOIN readable_projects AS saga ON saga.id=note.saga_id
 LEFT JOIN books AS book ON note.source_kind='chapter' AND saga.id IS NOT NULL
  AND book.id=(note.source_json#>>'{source,locator,bookId}')::uuid AND book.project_id=saga.id AND book.deleted_at IS NULL
 LEFT JOIN chapters AS place ON book.id IS NOT NULL
  AND place.id=(note.source_json#>>'{source,locator,chapterId}')::uuid
  AND place.book_id=book.id AND place.project_id=saga.id
  AND place.deleted_at IS NULL AND place.published_revision IS NOT NULL
  AND encode(ck_source_generation_digest(saga.id,saga.lifecycle_generation,book.id,book.lifecycle_generation,place.id,place.lifecycle_generation),'hex')
   =note.source_json#>>'{chapter,sourceGeneration}'
 LEFT JOIN chat_sessions AS chat ON note.source_kind='chat' AND saga.id IS NOT NULL
  AND chat.id=(note.source_json#>>'{source,chatId}')::uuid AND chat.project_id=saga.id AND chat.deleted_at IS NULL
  AND chat.scope='project' AND chat.book_id IS NULL
  AND chat.lifecycle_epoch=(note.source_json#>>'{chat,epoch}')::bigint
  AND chat.thread_id=note.source_json#>>'{chat,threadId}'
  AND (chat.source_view='published' OR saga.role IN ('owner','admin','editor'))
  AND EXISTS (SELECT 1 FROM project_source_views AS view
   WHERE view.project_id=saga.id AND view.source_view=chat.source_view
    AND view.project_lifecycle_generation=saga.lifecycle_generation)
), filtered AS (
 SELECT id,updated_at,source_kind,title,preview,quote_preview,project_id,project_title,book_id,book_title,chapter_title,chapter_order,chat_title
 FROM entries
 WHERE (sqlc.arg(search_query)::text='' OR strpos(lower(title),lower(sqlc.arg(search_query)))>0
  OR strpos(lower(search_body),lower(sqlc.arg(search_query)))>0
  OR strpos(lower(search_quote),lower(sqlc.arg(search_query)))>0)
  AND (sqlc.arg(source_kind)::text='' OR source_kind=sqlc.arg(source_kind))
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id=sqlc.narg(project_id) OR source_kind='free')
  AND (sqlc.narg(book_id)::uuid IS NULL OR book_id=sqlc.narg(book_id))
), page AS (
 SELECT id,updated_at,
  jsonb_build_object('id',id,'sourceKind',source_kind,'title',title,'preview',preview,'updatedAt',updated_at)
   ||jsonb_strip_nulls(jsonb_build_object('quote',nullif(quote_preview,''),'projectTitle',project_title,'bookTitle',book_title,
     'projectId',project_id,'bookId',book_id,'chapterTitle',chapter_title,'chapterOrder',chapter_order,'chatTitle',chat_title)) AS payload
 FROM filtered
 WHERE NOT sqlc.arg(has_cursor)::boolean
  OR (updated_at,id)<(sqlc.arg(cursor_updated_at)::timestamptz,sqlc.arg(cursor_id)::uuid)
 ORDER BY updated_at DESC,id DESC LIMIT sqlc.arg(limit_count)
)
SELECT (SELECT count(*) FROM filtered) AS total_count,
 coalesce((SELECT jsonb_agg(payload ORDER BY updated_at DESC,id DESC) FROM page),'[]'::jsonb)::jsonb AS entries;
