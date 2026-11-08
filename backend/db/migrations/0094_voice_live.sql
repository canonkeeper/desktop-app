-- The voice co-author on GPT-Live (specs/2026-11-03-voice-assistant.md V4): its voices are the owner's choice by ear
-- (V4.6, Decision log 55), and an author's earlier choice moves to the closest of them: marin → breeze, sage → vale,
-- cedar → spruce, ash → arbor. The spend of the assistant of the voice is a source of its own (V4.3).
ALTER TABLE public.users DROP CONSTRAINT users_assistant_voice_check;
UPDATE public.users SET assistant_voice = CASE assistant_voice
  WHEN 'marin' THEN 'breeze' WHEN 'sage' THEN 'vale' WHEN 'cedar' THEN 'spruce' WHEN 'ash' THEN 'arbor' END
WHERE assistant_voice IS NOT NULL;
ALTER TABLE public.users
  ADD CONSTRAINT users_assistant_voice_check CHECK (assistant_voice IN ('breeze', 'vale', 'spruce', 'arbor', 'ember'));
REVOKE ALL ON TABLE public.users FROM PUBLIC;

ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check
    CHECK (source IN ('chat', 'scan', 'cards', 'images', 'layer', 'draft', 'drawing', 'draft_helper', 'voice'));
REVOKE ALL ON public.model_usage_days FROM PUBLIC;
