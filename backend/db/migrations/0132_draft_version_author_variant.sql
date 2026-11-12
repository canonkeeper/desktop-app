-- The author's variant (specs/2026-11-11-ux5-writing-path.md WP5.4): a text of the author the draft could not take — the
-- chapter changed in another place where he typed, in another tab or by the voice — is not thrown away but kept as a
-- version of its own reason, «Ваш вариант»; when it was the idea he typed, the version keeps that idea too.
ALTER TABLE public.chapter_draft_versions
    DROP CONSTRAINT chapter_draft_versions_reason_check,
    ADD CONSTRAINT chapter_draft_versions_reason_check CHECK (reason IN (
        'written', 'before_write', 'before_apply', 'before_take', 'before_restore', 'before_delete', 'switch',
        'before_upload', 'before_proofread', 'before_voice', 'author_variant'
    )),
    DROP CONSTRAINT chapter_draft_versions_idea_check,
    ADD CONSTRAINT chapter_draft_versions_idea_check CHECK (
        idea IS NULL OR char_length(idea) <= 8000 AND reason IN ('before_voice', 'before_restore', 'author_variant')
    );

REVOKE ALL ON TABLE public.chapter_draft_versions FROM PUBLIC;
