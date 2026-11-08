-- Restoring a book or chapter rebuilds the project's source views in one transaction
-- (delete, then insert with the next catalog revision). Chats point at those views, so
-- their keys are checked at commit, like book_source_members already are.
ALTER TABLE public.chat_sessions
  ALTER CONSTRAINT chat_sessions_project_view_fkey DEFERRABLE INITIALLY DEFERRED,
  ALTER CONSTRAINT chat_sessions_book_view_fkey DEFERRABLE INITIALLY DEFERRED;

REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;
