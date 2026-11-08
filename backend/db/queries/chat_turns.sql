-- name: CreateChatTurn :one
INSERT INTO chat_turns (
  id,
  chat_id,
  project_id,
  source_view,
  job_id,
  job_kind,
  workflow_input_id,
  artifact_source_owner_id,
  created_at,
  updated_at
)
VALUES (
  sqlc.arg(id),
  sqlc.arg(chat_id),
  sqlc.arg(project_id),
  sqlc.arg(source_view),
  sqlc.arg(job_id),
  'research_chat_turn',
  sqlc.arg(workflow_input_id),
  sqlc.arg(artifact_source_owner_id),
  sqlc.arg(created_at),
  sqlc.arg(created_at)
)
RETURNING *;

-- name: LockChatTurnForSubmit :one
SELECT turn_row.*
FROM chat_turns AS turn_row
JOIN chat_sessions AS chat
  ON chat.id = turn_row.chat_id
 AND chat.project_id = turn_row.project_id
 AND chat.source_view = turn_row.source_view
WHERE turn_row.id = sqlc.arg(turn_id)
  AND turn_row.project_id = sqlc.arg(project_id)
  AND turn_row.job_id = sqlc.arg(job_id)
  AND chat.deleted_at IS NULL
FOR UPDATE OF turn_row, chat;

-- name: GetReadableChatTurn :one
SELECT
  turn_row.id,
  turn_row.chat_id,
  turn_row.project_id,
  turn_row.source_view,
  turn_row.job_id,
  job.subject_json AS job_subject_json
FROM chat_turns AS turn_row
JOIN chat_sessions AS chat
  ON chat.id = turn_row.chat_id
 AND chat.project_id = turn_row.project_id
 AND chat.source_view = turn_row.source_view
JOIN projects AS project
  ON project.id = turn_row.project_id
 AND project.deleted_at IS NULL
JOIN project_memberships AS membership
  ON membership.project_id = turn_row.project_id
 AND membership.user_id = sqlc.arg(actor_user_id)
 AND membership.removed_at IS NULL
JOIN jobs AS job
  ON job.id = turn_row.job_id
 AND job.project_id = turn_row.project_id
 AND job.kind = turn_row.job_kind
WHERE turn_row.id = sqlc.arg(turn_id)
  AND chat.deleted_at IS NULL
  AND (
    turn_row.source_view = 'published'
    OR membership.role IN ('owner', 'admin', 'editor')
  )
  AND ck_artifact_source_owner_is_lifecycle_readable(
        turn_row.artifact_source_owner_id,
        turn_row.project_id,
        'chat_turn',
        turn_row.id
      );

-- name: BindChatProviderTurn :execrows
UPDATE chat_turns AS turn_row
SET provider_turn_id = sqlc.arg(provider_turn_id),
    updated_at = clock_timestamp()
FROM chat_sessions AS chat
WHERE turn_row.chat_id = chat.id
  AND turn_row.project_id = chat.project_id
  AND turn_row.source_view = chat.source_view
  AND turn_row.project_id = sqlc.arg(project_id)
  AND turn_row.chat_id = sqlc.arg(chat_id)
  AND turn_row.id = sqlc.arg(turn_id)
  AND turn_row.job_id = sqlc.arg(job_id)
  AND chat.thread_id = sqlc.arg(thread_id)
  AND chat.deleted_at IS NULL
  AND (turn_row.provider_turn_id IS NULL OR turn_row.provider_turn_id = sqlc.arg(provider_turn_id));
