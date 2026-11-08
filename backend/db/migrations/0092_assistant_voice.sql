-- The voice of the voice co-author an author chose (specs/2026-11-03-voice-assistant.md V1.9): none is marin. The
-- list is the domain's (domain/voice.Voices); a voice leaves it only with a migration of the rows that chose it.
ALTER TABLE public.users
  ADD COLUMN assistant_voice text CHECK (assistant_voice IN ('marin', 'sage', 'cedar', 'ash'));
REVOKE ALL ON TABLE public.users FROM PUBLIC;
