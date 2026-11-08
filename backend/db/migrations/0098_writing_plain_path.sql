-- One text and one main button (specs/2026-11-04-writing-plain-path.md P3, P4): the block of the helper the next run
-- brings to prose, the turn of the helper whose end starts that run, and what the last work on the text did, which the
-- line of the outcome reads: how many words it brought and where the block it wrote stands in the text.
ALTER TABLE public.chapter_drafts
    ADD COLUMN pending_block text NOT NULL DEFAULT '',
    ADD COLUMN run_after_job uuid,
    ADD COLUMN last_run text NOT NULL DEFAULT '',
    ADD COLUMN last_run_words integer NOT NULL DEFAULT 0,
    ADD COLUMN last_run_offset integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT chapter_drafts_last_run_check CHECK (last_run IN ('', 'started', 'continued', 'covered', 'reworked', 'blocked')),
    ADD CONSTRAINT chapter_drafts_last_run_words_check CHECK (last_run_words >= 0),
    ADD CONSTRAINT chapter_drafts_last_run_offset_check CHECK (last_run_offset >= 0);
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
