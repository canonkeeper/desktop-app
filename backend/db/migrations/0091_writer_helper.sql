-- The helper of the chapter workshop (specs/2026-11-02-writer-workshop.md W1): an agent of its own with one thread
-- per chapter draft. Its conversation is a chat session of the kind draft with the turns and the lifecycle of every
-- turn; a session of that kind is not a chat of the list. The draft names its session; no foreign key, as a drawing
-- names its own: the session goes by the deletion of the draft, which deletes the chat first (W7.5).
ALTER TABLE public.chat_sessions
  DROP CONSTRAINT chat_sessions_kind_check,
  ADD CONSTRAINT chat_sessions_kind_check CHECK (kind IN ('research', 'drawing', 'draft'));
REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;

ALTER TABLE public.chapter_drafts ADD COLUMN chat_id uuid;
CREATE INDEX chapter_drafts_chat_idx ON public.chapter_drafts (chat_id) WHERE chat_id IS NOT NULL;
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;

-- W1.5: the turns of the helper per Moscow day, the writer's own limit and the platform default.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN draft_turns_per_day integer CHECK (draft_turns_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN draft_turns_per_day integer CHECK (draft_turns_per_day >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN draft_turns integer NOT NULL DEFAULT 0 CHECK (draft_turns >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;

-- W1.5: the spend of the helper's model is a source of its own; draft stays the Job writing a chapter.
ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check
    CHECK (source IN ('chat', 'scan', 'cards', 'images', 'layer', 'draft', 'drawing', 'draft_helper'));
REVOKE ALL ON public.model_usage_days FROM PUBLIC;
