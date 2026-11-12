-- The voice co-author by the tier (specs/2026-11-08-subscription-tiers.md TR8): «Писатель» and «Профи» talk minutes of a
-- Moscow day, the others a few trial calls for the life of the account. Neither the sound nor what was said is kept:
-- only how long the author talked.

-- TR8.4: the seconds of the calls of a writer, by the Moscow day each call began. The row goes with the other counters of
-- the day, and the deletion of the account moves it to a pseudonym (ck_anonymize_writer_usage, TR10.1).
ALTER TABLE public.writer_usage_days
  ADD COLUMN voice_seconds integer NOT NULL DEFAULT 0 CHECK (voice_seconds >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;

-- TR8.1, TR8.2: the trial calls an account used in its life, in the same row of the counters of the tiers (0118); the
-- trial left is the value of the tier in force less what was used. No row — nothing used.
ALTER TABLE public.subscription_usage
  ADD COLUMN trial_voice_calls_used integer NOT NULL DEFAULT 0 CHECK (trial_voice_calls_used >= 0);
REVOKE ALL ON TABLE public.subscription_usage FROM PUBLIC;
