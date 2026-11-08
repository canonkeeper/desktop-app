-- A place of a note is a scene of any length, and so is its replacement (specs/2026-11-04-writing-plain-path.md P5.5; a review
-- of an author 05.11): up to a whole chapter. The other notes keep their short quotes by the checks of the domain.
-- On the author's direct request the helper replaces a place itself (P5.6, the owner 05.11): the note of the kind
-- `replaced` holds what stood there, and what stood right around it, so that «Вернуть как было» finds it at any time.
ALTER TABLE public.chapter_draft_notes
    ADD COLUMN anchor_before text,
    ADD COLUMN anchor_after text,
    DROP CONSTRAINT chapter_draft_notes_kind_check,
    DROP CONSTRAINT chapter_draft_notes_quote_check,
    DROP CONSTRAINT chapter_draft_notes_replacement_check,
    ADD CONSTRAINT chapter_draft_notes_kind_check
        CHECK (kind IN ('brief', 'canon', 'question', 'variant', 'replaced', 'meaning', 'repeat', 'machine')),
    ADD CONSTRAINT chapter_draft_notes_quote_check CHECK (quote IS NULL OR char_length(quote) BETWEEN 1 AND 400000),
    ADD CONSTRAINT chapter_draft_notes_replacement_check CHECK (replacement IS NULL OR char_length(replacement) BETWEEN 1 AND 400000),
    ADD CONSTRAINT chapter_draft_notes_anchors_check CHECK (
        (anchor_before IS NULL OR char_length(anchor_before) <= 200) AND (anchor_after IS NULL OR char_length(anchor_after) <= 200)
    );
REVOKE ALL ON TABLE public.chapter_draft_notes FROM PUBLIC;
