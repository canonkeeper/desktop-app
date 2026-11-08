-- Promote existing readable book chats to saga scope without replacing history.
-- The numbered runner executes this migration atomically with admission closed.
-- Only the scope immutability trigger is suspended; it is restored before commit.
REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;

ALTER TABLE public.chat_sessions DISABLE TRIGGER chat_sessions_lifecycle_commit_guard;

UPDATE public.chat_sessions AS chat
SET scope = 'project', book_id = NULL
FROM public.projects AS project, public.books AS book
WHERE chat.scope = 'book'
  AND chat.deleted_at IS NULL
  AND project.id = chat.project_id AND project.deleted_at IS NULL
  AND book.id = chat.book_id AND book.project_id = chat.project_id AND book.deleted_at IS NULL;

ALTER TABLE public.chat_sessions ENABLE TRIGGER chat_sessions_lifecycle_commit_guard;
