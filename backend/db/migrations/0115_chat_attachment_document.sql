-- The chapter attached to a question of the chat keeps its parse whole (specs/2026-11-07-chat-attach-chapter.md, CA5.5):
-- its paragraphs with their runs and, for a Word file, the look of the file (specs/2026-11-07-writing-proofread.md,
-- PR1.7), so «Вычитать и вернуть .docx» gives the proofreading the chapter as the file had it, and the file with Word
-- revisions is written in the author's look. The paragraphs the model and the panel read stay in their column; the
-- attachments before have no parse.
ALTER TABLE public.chat_turn_attachments
    ADD COLUMN document jsonb,
    ADD CONSTRAINT chat_turn_attachments_document_check
        CHECK (document IS NULL OR (jsonb_typeof(document) = 'object' AND octet_length(document::text) <= 16777216));
-- The grants of the table are its own (0112): the runtime reads and writes the column with its row.
REVOKE ALL ON TABLE public.chat_turn_attachments FROM PUBLIC;
