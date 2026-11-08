-- «Ваш текст: Переписать вашей рукой» (specs/2026-11-04-writing-plain-path.md P5.1): the press that opened the turn of the
-- helper asked to rewrite all of the working text, the author's too, once the helper wrote what the idea lacks. The end
-- of that turn reads it; a block another turn writes does not inherit it.
ALTER TABLE public.chapter_drafts
    ADD COLUMN run_after_rework boolean NOT NULL DEFAULT false;
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
