-- The Job of the writing agent (specs/2026-11-06-writing-agent.md, WA5, WA6, WA7): what it finds is a note like the
-- helper's. A scene it writes leaves «новое, не канон» and the questions it decided by itself; a selection leaves its
-- variants; the check leaves canon findings with their level and the book's quote, style findings with their category,
-- the yes-or-no questions of layer C, the seams and what the plan was not followed in. The notes of the earlier writing
-- (meaning, repeat, machine) stay the author's data until he closes them.
ALTER TABLE public.chapter_draft_notes
    ADD COLUMN level text,
    ADD COLUMN category text,
    ADD COLUMN book_quote text,
    ADD COLUMN scene integer,
    ADD COLUMN pack_id uuid,
    DROP CONSTRAINT chapter_draft_notes_kind_check,
    DROP CONSTRAINT chapter_draft_notes_origin_check,
    ADD CONSTRAINT chapter_draft_notes_kind_check CHECK (kind IN (
        'brief', 'canon', 'question', 'variant', 'replaced', 'meaning', 'repeat', 'machine',
        'new', 'style', 'craft', 'seam', 'plan_follow'
    )),
    ADD CONSTRAINT chapter_draft_notes_origin_check CHECK (
        origin IN ('helper', 'job')
        AND (origin = 'job') = (job_id IS NOT NULL)
        AND (origin = 'job' OR kind IN ('brief', 'canon', 'question', 'variant', 'replaced'))
        AND (origin = 'helper' OR kind IN (
            'meaning', 'repeat', 'machine', 'new', 'question', 'variant', 'canon', 'style', 'craft', 'seam', 'plan_follow'
        ))
    ),
    ADD CONSTRAINT chapter_draft_notes_check_fields_check CHECK (
        (level IS NULL OR level IN ('blocking', 'warning', 'info'))
        AND (category IS NULL OR char_length(category) BETWEEN 1 AND 80)
        AND (book_quote IS NULL OR char_length(book_quote) BETWEEN 1 AND 4000)
        AND (scene IS NULL OR scene >= 1)
    ),
    ADD CONSTRAINT chapter_draft_notes_pack_fkey FOREIGN KEY (pack_id, draft_id)
        REFERENCES public.chapter_draft_packs(id, draft_id) ON DELETE CASCADE;
REVOKE ALL ON TABLE public.chapter_draft_notes FROM PUBLIC;

-- WA5.13: a rewrite of a selection and a check of the seams are cheap and frequent («снова, пока не придём к
-- результату»): their own limit per Moscow day, 200 by default (the coordinator 06.11).
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN draft_rewrites_per_day integer CHECK (draft_rewrites_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN draft_rewrites_per_day integer CHECK (draft_rewrites_per_day >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN draft_rewrites integer NOT NULL DEFAULT 0 CHECK (draft_rewrites >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;
