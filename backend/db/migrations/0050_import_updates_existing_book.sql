-- An import can rewrite an existing book from a new file of the same volume
-- (specs/2026-10-27-update-book-from-file.md): an update_book request names that book, a
-- new_book request names none.
ALTER TABLE public.import_job_requests
  ADD COLUMN target_book_id uuid,
  DROP CONSTRAINT import_job_requests_import_mode_check,
  ADD CONSTRAINT import_job_requests_import_mode_check CHECK (
    (import_mode = 'new_book' AND target_book_id IS NULL)
    OR (import_mode = 'update_book' AND target_book_id IS NOT NULL)
  );

-- The table keeps its runtime privileges; PUBLIC stays without access.
REVOKE ALL ON TABLE public.import_job_requests FROM PUBLIC;
