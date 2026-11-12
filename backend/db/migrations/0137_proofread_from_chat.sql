-- The proofreading of a chapter attached in the chat remembers where it began (specs/2026-11-11-ux5-proofread-mode.md
-- PM3.3): the original of the draft names the attachment it was read from — the chat and the number of the attachment in
-- it. The card of the chat finds its proofreading by it and starts no second one; startChapterProofread of the same
-- attachment answers the draft it has. One draft an attachment. The chat being deleted takes its attachments and the tie
-- goes with them: the draft stays. A file loaded into the draft later is another chapter, and the tie goes too.
ALTER TABLE public.chapter_draft_originals
    ADD COLUMN attachment_chat_id uuid,
    ADD COLUMN attachment_number integer,
    ADD CONSTRAINT chapter_draft_originals_attachment_check CHECK ((attachment_chat_id IS NULL) = (attachment_number IS NULL)),
    ADD CONSTRAINT chapter_draft_originals_attachment_fkey FOREIGN KEY (attachment_chat_id, attachment_number)
        REFERENCES public.chat_turn_attachments(chat_id, number) ON DELETE SET NULL;
CREATE UNIQUE INDEX chapter_draft_originals_attachment_key ON public.chapter_draft_originals (attachment_chat_id, attachment_number)
    WHERE attachment_chat_id IS NOT NULL;

REVOKE ALL ON TABLE public.chapter_draft_originals FROM PUBLIC;
