-- The chapter before a conversation with the voice (specs/2026-11-10-ux5-voice.md VO1.1, VO1.3): before the first change a
-- conversation makes in a draft, the draft as it stood is kept as a version of its own reason — its text and its idea, as
-- the voice writes the author's story into the idea; «Вернуть как было до разговора» gives both back, and the version kept
-- before that return keeps the idea too. Every other version keeps the text alone: its idea stays null.
ALTER TABLE public.chapter_draft_versions
    ADD COLUMN idea text,
    DROP CONSTRAINT chapter_draft_versions_reason_check,
    ADD CONSTRAINT chapter_draft_versions_reason_check CHECK (reason IN (
        'written', 'before_write', 'before_apply', 'before_take', 'before_restore', 'before_delete', 'switch',
        'before_upload', 'before_proofread', 'before_voice'
    )),
    ADD CONSTRAINT chapter_draft_versions_idea_check CHECK (
        idea IS NULL OR char_length(idea) <= 8000 AND reason IN ('before_voice', 'before_restore')
    );

REVOKE ALL ON TABLE public.chapter_draft_versions FROM PUBLIC;
