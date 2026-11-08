-- name: CreateChatSession :one
INSERT INTO chat_sessions (
  id,
  project_id,
  source_view,
  scope,
  book_id,
  title,
  saved_assignment,
  agent_generation,
  kind,
  created_at,
  updated_at
)
SELECT
  sqlc.arg(id),
  project.id,
  sqlc.arg(source_view),
  sqlc.arg(scope),
  sqlc.narg(book_id),
  sqlc.arg(title),
  sqlc.narg(saved_assignment),
  sqlc.arg(agent_generation),
  sqlc.arg(kind),
  sqlc.arg(created_at),
  sqlc.arg(created_at)
FROM projects AS project
JOIN project_memberships AS membership
  ON membership.project_id = project.id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
 AND membership.role IN ('owner', 'admin', 'editor')
JOIN project_source_views AS source_view
  ON source_view.project_id = project.id
 AND source_view.source_view = sqlc.arg(source_view)
 AND source_view.project_lifecycle_generation = project.lifecycle_generation
WHERE project.id = sqlc.arg(project_id)
  AND project.deleted_at IS NULL
  AND (
    (sqlc.arg(scope)::text = 'project' AND sqlc.narg(book_id)::uuid IS NULL)
    OR (
      sqlc.arg(scope)::text = 'book'
      AND EXISTS (
        SELECT 1
        FROM book_source_members AS member
        JOIN books AS book
          ON book.id = member.book_id
         AND book.project_id = member.project_id
         AND book.lifecycle_generation = member.book_lifecycle_generation
         AND book.deleted_at IS NULL
        WHERE member.project_id = project.id
          AND member.source_view = source_view.source_view
          AND member.project_lifecycle_generation = source_view.project_lifecycle_generation
          AND member.book_id = sqlc.narg(book_id)::uuid
      )
    )
  )
RETURNING *;

-- name: ClearChatSessionSavedAssignment :one
UPDATE chat_sessions
SET saved_assignment = NULL
WHERE id = sqlc.arg(chat_id)
  AND project_id = sqlc.arg(project_id)
  AND lifecycle_epoch = sqlc.arg(lifecycle_epoch)
  AND deleted_at IS NULL
  AND saved_assignment = sqlc.arg(saved_assignment)
RETURNING id;

-- name: LockChatSessionForUpdate :one
SELECT sqlc.embed(chat), membership.role AS actor_role
FROM chat_sessions AS chat
JOIN projects AS project
  ON project.id = chat.project_id
 AND project.deleted_at IS NULL
JOIN project_memberships AS membership
  ON membership.project_id = chat.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
JOIN project_source_views AS source_view
  ON source_view.project_id = chat.project_id
 AND source_view.source_view = chat.source_view
 AND source_view.project_lifecycle_generation = project.lifecycle_generation
WHERE chat.id = sqlc.arg(chat_id)
  AND (
    chat.scope = 'project'
    OR EXISTS (
      SELECT 1
      FROM book_source_members AS member
      JOIN books AS book
        ON book.id = member.book_id
       AND book.project_id = member.project_id
       AND book.lifecycle_generation = member.book_lifecycle_generation
       AND book.deleted_at IS NULL
      WHERE member.project_id = chat.project_id
        AND member.source_view = chat.source_view
        AND member.project_lifecycle_generation = source_view.project_lifecycle_generation
        AND member.book_id = chat.book_id
    )
  )
FOR UPDATE OF chat;

-- name: UpdateChatSessionForActor :one
-- A new title: it moves the chat up the list. The toggle «Рисунки» is gone with the drawings of the chat
-- (specs/2026-11-02-image-workshop.md IW7.1); its column goes in the next release (IW5.6).
UPDATE chat_sessions AS chat
SET title = sqlc.arg(title)::text,
    updated_at = date_trunc('microseconds', clock_timestamp())
FROM projects AS project,
     project_memberships AS membership,
     project_source_views AS source_view
WHERE chat.id = sqlc.arg(chat_id)
  AND chat.project_id = sqlc.arg(project_id)
  AND chat.lifecycle_epoch = sqlc.arg(lifecycle_epoch)
  AND chat.deleted_at IS NULL
  AND project.id = chat.project_id
  AND project.deleted_at IS NULL
  AND membership.project_id = chat.project_id
  AND membership.user_id = sqlc.arg(actor_user_id)
  AND membership.removed_at IS NULL
  AND membership.role IN ('owner', 'admin', 'editor')
  AND source_view.project_id = chat.project_id
  AND source_view.source_view = chat.source_view
  AND source_view.project_lifecycle_generation = project.lifecycle_generation
  AND (
    chat.scope = 'project'
    OR EXISTS (
      SELECT 1
      FROM book_source_members AS member
      JOIN books AS book
        ON book.id = member.book_id
       AND book.project_id = member.project_id
       AND book.lifecycle_generation = member.book_lifecycle_generation
       AND book.deleted_at IS NULL
      WHERE member.project_id = chat.project_id
        AND member.source_view = chat.source_view
        AND member.project_lifecycle_generation = source_view.project_lifecycle_generation
        AND member.book_id = chat.book_id
    )
  )
RETURNING chat.*;
-- name: ListReadableChatSessions :many
SELECT chat.*,
       latest_turn.turn_id AS latest_turn_id,
       latest_turn.job_id AS latest_turn_job_id,
       latest_turn.job_status AS latest_turn_status
FROM chat_sessions AS chat
LEFT JOIN LATERAL (
  SELECT turn_row.id AS turn_id, turn_row.job_id, turn_job.status AS job_status
  FROM chat_turns AS turn_row
  LEFT JOIN jobs AS turn_job
    ON turn_job.id = turn_row.job_id
   AND turn_job.project_id = turn_row.project_id
  WHERE turn_row.chat_id = chat.id
    AND turn_row.project_id = chat.project_id
    AND turn_row.source_view = chat.source_view
  ORDER BY turn_row.created_at DESC, turn_row.id DESC
  LIMIT 1
) AS latest_turn ON TRUE
JOIN projects AS project
  ON project.id = chat.project_id
 AND project.deleted_at IS NULL
JOIN project_source_views AS source_marker
  ON source_marker.project_id = chat.project_id
 AND source_marker.source_view = chat.source_view
 AND source_marker.project_lifecycle_generation = project.lifecycle_generation
JOIN project_memberships AS membership
  ON membership.project_id = chat.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE chat.project_id = sqlc.arg(project_id)
  AND chat.deleted_at IS NULL
  -- The sessions of the helpers of the drawing workshop are not chats (specs/2026-11-02-image-workshop.md IW4.2).
  AND chat.kind = 'research'
  AND (
    chat.scope = 'project'
    OR EXISTS (
      SELECT 1
      FROM book_source_members AS member
      JOIN books AS book
        ON book.id = member.book_id
       AND book.project_id = member.project_id
       AND book.lifecycle_generation = member.book_lifecycle_generation
       AND book.deleted_at IS NULL
      WHERE member.project_id = chat.project_id
        AND member.source_view = chat.source_view
        AND member.project_lifecycle_generation = source_marker.project_lifecycle_generation
        AND member.book_id = chat.book_id
    )
  )
  AND (
    NOT sqlc.arg(has_scope)::boolean
    OR chat.scope = sqlc.arg(scope)::text
  )
  AND (
    NOT sqlc.arg(has_book_id)::boolean
    OR chat.book_id = sqlc.arg(book_id)::uuid
  )
  AND (
    chat.source_view = 'published'
    OR membership.role IN ('owner', 'admin', 'editor')
  )
  AND (
    NOT sqlc.arg(has_cursor)::boolean
    OR (chat.updated_at, chat.id) < (
      sqlc.arg(cursor_updated_at)::timestamptz,
      sqlc.arg(cursor_id)::uuid
    )
  )
ORDER BY chat.updated_at DESC, chat.id DESC
LIMIT sqlc.arg(limit_count);

-- name: GetReadableChatSession :one
SELECT
  chat.*,
  membership.role AS actor_role,
  latest_turn.turn_id AS latest_turn_id,
  latest_turn.job_id AS latest_turn_job_id,
  latest_turn.job_status AS latest_turn_status
FROM chat_sessions AS chat
LEFT JOIN LATERAL (
  SELECT turn_row.id AS turn_id, turn_row.job_id, turn_job.status AS job_status
  FROM chat_turns AS turn_row
  LEFT JOIN jobs AS turn_job
    ON turn_job.id = turn_row.job_id
   AND turn_job.project_id = turn_row.project_id
  WHERE turn_row.chat_id = chat.id
    AND turn_row.project_id = chat.project_id
    AND turn_row.source_view = chat.source_view
  ORDER BY turn_row.created_at DESC, turn_row.id DESC
  LIMIT 1
) AS latest_turn ON TRUE
JOIN projects AS project
  ON project.id = chat.project_id
 AND project.deleted_at IS NULL
JOIN project_source_views AS source_marker
  ON source_marker.project_id = chat.project_id
 AND source_marker.source_view = chat.source_view
 AND source_marker.project_lifecycle_generation = project.lifecycle_generation
JOIN project_memberships AS membership
  ON membership.project_id = chat.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
WHERE chat.id = sqlc.arg(chat_id)
  AND chat.deleted_at IS NULL
  AND (
    chat.scope = 'project'
    OR EXISTS (
      SELECT 1
      FROM book_source_members AS member
      JOIN books AS book
        ON book.id = member.book_id
       AND book.project_id = member.project_id
       AND book.lifecycle_generation = member.book_lifecycle_generation
       AND book.deleted_at IS NULL
      WHERE member.project_id = chat.project_id
        AND member.source_view = chat.source_view
        AND member.project_lifecycle_generation = source_marker.project_lifecycle_generation
        AND member.book_id = chat.book_id
    )
  )
  AND (
    chat.source_view = 'published'
    OR membership.role IN ('owner', 'admin', 'editor')
  );

-- name: LockWritableChatSession :one
SELECT
  chat.*,
  membership.role AS actor_role,
  source_view.project_lifecycle_generation
FROM chat_sessions AS chat
JOIN projects AS project
  ON project.id = chat.project_id
 AND project.deleted_at IS NULL
JOIN project_memberships AS membership
  ON membership.project_id = chat.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
 AND membership.role IN ('owner', 'admin', 'editor')
JOIN project_source_views AS source_view
  ON source_view.project_id = chat.project_id
 AND source_view.source_view = chat.source_view
 AND source_view.project_lifecycle_generation = project.lifecycle_generation
WHERE chat.id = sqlc.arg(chat_id)
  AND chat.project_id = sqlc.arg(project_id)
  AND chat.deleted_at IS NULL
  AND (
    chat.scope = 'project'
    OR EXISTS (
      SELECT 1
      FROM book_source_members AS member
      JOIN books AS book
        ON book.id = member.book_id
       AND book.project_id = member.project_id
       AND book.lifecycle_generation = member.book_lifecycle_generation
       AND book.deleted_at IS NULL
      WHERE member.project_id = chat.project_id
        AND member.source_view = chat.source_view
        AND member.project_lifecycle_generation = source_view.project_lifecycle_generation
        AND member.book_id = chat.book_id
    )
  )
FOR UPDATE OF chat;

-- name: MarkChatSessionDeleted :one
UPDATE chat_sessions
SET deleted_at = sqlc.arg(deleted_at),
    purge_after = sqlc.arg(purge_after),
    lifecycle_epoch = lifecycle_epoch + 1,
    updated_at = sqlc.arg(deleted_at)
WHERE id = sqlc.arg(chat_id)
  AND project_id = sqlc.arg(project_id)
  AND lifecycle_epoch = sqlc.arg(lifecycle_epoch)
  AND deleted_at IS NULL
RETURNING *;

