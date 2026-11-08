-- «Обновить из файла» does what the author changed and shows it first (specs/2026-10-27-update-book-from-file.md).
-- An update_preview import reads the file and the book and writes nothing into the book (UC6); like update_book it
-- names its book.
ALTER TABLE public.import_job_requests
  DROP CONSTRAINT import_job_requests_import_mode_check,
  ADD CONSTRAINT import_job_requests_import_mode_check CHECK (
    (import_mode = 'new_book' AND target_book_id IS NULL)
    OR (import_mode IN ('update_book', 'update_preview') AND target_book_id IS NOT NULL)
  );

REVOKE ALL ON TABLE public.import_job_requests FROM PUBLIC;

-- A change of signs keeps the card of the chapter (R3.2): the update copies the card to the fingerprint of the new
-- text, and built_from names the text the model read, so the thread layer does not count the copy as a new card
-- (R3.3). Cards the model writes have none.
ALTER TABLE public.chapter_cards
  ADD COLUMN built_from bytea,
  ADD CONSTRAINT chapter_cards_built_from_check CHECK (
    built_from IS NULL OR (octet_length(built_from) = 32 AND built_from <> content_hash)
  );

REVOKE ALL ON TABLE public.chapter_cards FROM PUBLIC;
