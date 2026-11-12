-- The chapter of the author attached to a question of the chat (specs/2026-11-07-chat-attach-chapter.md, CA6): one row
-- a turn, numbered in its chat.

-- name: NextChatAttachmentNumber :one
-- The number of a new attachment of the chat, read under the lock of its session.
SELECT (COALESCE(max(number), 0) + 1)::integer AS number
FROM chat_turn_attachments
WHERE project_id = sqlc.arg(project_id) AND chat_id = sqlc.arg(chat_id);

-- name: CreateChatTurnAttachment :exec
INSERT INTO chat_turn_attachments (turn_id, chat_id, project_id, number, title, file_name, format, paragraphs, words, document, created_at)
VALUES (
    sqlc.arg(turn_id), sqlc.arg(chat_id), sqlc.arg(project_id), sqlc.arg(number), sqlc.arg(title), sqlc.arg(file_name),
    sqlc.arg(format), sqlc.arg(paragraphs)::text[], sqlc.arg(words), sqlc.narg(document)::jsonb, sqlc.arg(created_at)
);

-- name: GetChatTurnAttachmentHeader :one
-- What the input of the turn says of its attachment: no word of its text.
SELECT number, title, cardinality(paragraphs)::integer AS paragraph_count, words
FROM chat_turn_attachments
WHERE project_id = sqlc.arg(project_id) AND turn_id = sqlc.arg(turn_id);

-- name: GetChatAttachment :one
-- An attachment of a live chat by its number; 0 names the latest one.
SELECT attachment.number, attachment.title, attachment.file_name, attachment.format, attachment.paragraphs,
       attachment.words, attachment.created_at
FROM chat_turn_attachments AS attachment
JOIN chat_sessions AS chat ON chat.id = attachment.chat_id AND chat.project_id = attachment.project_id
WHERE attachment.project_id = sqlc.arg(project_id) AND attachment.chat_id = sqlc.arg(chat_id)
  AND chat.deleted_at IS NULL
  AND (sqlc.arg(number)::integer = 0 OR attachment.number = sqlc.arg(number)::integer)
ORDER BY attachment.number DESC
LIMIT 1;

-- name: ListChatAttachmentNumbers :many
-- The numbers of the attachments of a chat, for the model that named another one.
SELECT number
FROM chat_turn_attachments
WHERE project_id = sqlc.arg(project_id) AND chat_id = sqlc.arg(chat_id)
ORDER BY number;

-- name: GetChatAttachmentDocument :one
-- The parse of an attachment of a live chat for the proofreading started from it (CA5.5): NULL for the attachments
-- before it was kept.
SELECT attachment.document
FROM chat_turn_attachments AS attachment
JOIN chat_sessions AS chat ON chat.id = attachment.chat_id AND chat.project_id = attachment.project_id
WHERE attachment.project_id = sqlc.arg(project_id) AND attachment.chat_id = sqlc.arg(chat_id)
  AND chat.deleted_at IS NULL AND attachment.number = sqlc.arg(number);
