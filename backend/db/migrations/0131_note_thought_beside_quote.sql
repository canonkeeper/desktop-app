-- A note of a place keeps the quote apart from the thought of the author (specs/2026-11-11-ux7-notes.md NT1.2): a note
-- from a selection of the book or of an answer is saved without a thought, so its body may be empty beside the source it
-- keeps; a note of its own is its thought and still needs one. The notes written before keep their bodies as they are.
ALTER TABLE public.personal_notes
    DROP CONSTRAINT personal_notes_body_check,
    ADD CONSTRAINT personal_notes_body_check CHECK (
        (length(btrim(body)) > 0 OR source_json IS NOT NULL) AND char_length(body) <= 100000
    );

REVOKE ALL ON TABLE public.personal_notes FROM PUBLIC;
