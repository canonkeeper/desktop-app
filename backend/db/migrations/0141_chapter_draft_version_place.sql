-- A version of the text of a chapter is named by what it holds, not by why it was kept (specs/2026-11-11-ux9-writing-b.md
-- WB3.1): the first words of the place the action after it changed — «До замены места «шрамом через губу»», «До того, как
-- убрали «…»» — kept with the version when it is made, from the text before and after the action. Older versions have none.
ALTER TABLE public.chapter_draft_versions
    ADD COLUMN place text,
    ADD CONSTRAINT chapter_draft_versions_place_check CHECK (place IS NULL OR char_length(place) BETWEEN 1 AND 200);

REVOKE ALL ON TABLE public.chapter_draft_versions FROM PUBLIC;
