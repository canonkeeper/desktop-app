-- Columns no code reads or writes any more, kept only so that a switch back to the release before them could run
-- (specs/2026-11-04-writing-plain-path.md P5.2, specs/2026-11-02-image-workshop.md IW5.6): the choice of the account and
-- the mark of an edited chapter of the writing, the chat of the drawing buttons and the toggle «Рисунки» of a chat.
-- From here a release before this migration cannot run on this database: only a restore from a copy goes back.
ALTER TABLE public.users DROP COLUMN rework_own_text;
ALTER TABLE public.chapter_drafts DROP COLUMN chapter_edited;
ALTER TABLE public.project_illustration_settings DROP COLUMN chat_id;
ALTER TABLE public.chat_sessions DROP COLUMN illustrations_enabled;
REVOKE ALL ON TABLE public.users FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
REVOKE ALL ON TABLE public.project_illustration_settings FROM PUBLIC;
REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;
