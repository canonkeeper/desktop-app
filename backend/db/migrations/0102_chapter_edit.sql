-- A chapter that is already in the book is edited in a draft of «Письмо» and replaced in the book by the author's button
-- (specs/2026-11-05-voice-full-access.md A8.7). The draft names the chapter it edits and the revision of the book text it
-- stands on; the revision before its last replacement keeps «Вернуть прежний текст». One draft per chapter: «Править
-- главу» opens the same one. A chapter purged at the end of its deletion window clears the link and leaves the draft,
-- as for the place of any draft.
ALTER TABLE public.chapter_drafts
  ADD COLUMN edits_chapter_id uuid,
  ADD COLUMN edits_revision bigint,
  ADD COLUMN edits_previous_revision bigint,
  ADD CONSTRAINT chapter_drafts_edits_chapter_fkey FOREIGN KEY (edits_chapter_id, project_id)
    REFERENCES public.chapters(id, project_id) ON DELETE SET NULL (edits_chapter_id),
  ADD CONSTRAINT chapter_drafts_edits_revision_check CHECK (
    (edits_revision IS NULL OR edits_revision >= 1)
    AND (edits_previous_revision IS NULL OR (edits_revision IS NOT NULL AND edits_previous_revision >= 1))
  );
CREATE UNIQUE INDEX chapter_drafts_edits_chapter_idx ON public.chapter_drafts (edits_chapter_id, project_id)
  WHERE edits_chapter_id IS NOT NULL;
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
