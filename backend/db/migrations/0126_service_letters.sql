-- The letters the site writes by time (specs/2026-11-10-service-letters.md M3, M4): one reminder of an address nobody
-- confirmed, «тариф закончится» and «тариф закончился». Each goes at most once: its mark is set under the lock of the
-- account in the transaction that decides it, and the letter is queued after the commit. Nothing is written for the
-- time before this migration: what was due by now is marked as told.

-- M3.2: when the one reminder of an unconfirmed address was decided; NULL — not yet.
ALTER TABLE public.users ADD COLUMN verification_reminder_at timestamp with time zone;
-- M4.5: the author's choice to get the letters of the term of his tier, on until he turns it off in «Кабинет».
ALTER TABLE public.users ADD COLUMN tier_letters boolean DEFAULT true NOT NULL;
REVOKE ALL ON TABLE public.users FROM PUBLIC;

-- M4.4: when «тариф закончится» and «тариф закончился» of a period were decided; NULL — not yet.
ALTER TABLE public.subscription_periods ADD COLUMN ending_letter_at timestamp with time zone;
ALTER TABLE public.subscription_periods ADD COLUMN ended_letter_at timestamp with time zone;
REVOKE ALL ON TABLE public.subscription_periods FROM PUBLIC;

-- M3.3: an address unconfirmed by now gets no reminder; the reminder is for those who register after the release.
UPDATE public.users SET verification_reminder_at = now() WHERE email_verified_at IS NULL;
-- M4.4: no letter of a term that has ended or ends within three days by now.
UPDATE public.subscription_periods SET ending_letter_at = now(), ended_letter_at = now()
WHERE ends_at IS NOT NULL AND ends_at <= now();
UPDATE public.subscription_periods SET ending_letter_at = now()
WHERE ends_at IS NOT NULL AND ends_at > now() AND ends_at < now() + interval '3 days';

-- The pass of the site (M4.6) reads what is due by these.
CREATE INDEX users_verification_reminder_due_idx ON public.users (created_at)
WHERE email_verified_at IS NULL AND verification_reminder_at IS NULL AND deleted_at IS NULL;
CREATE INDEX subscription_periods_letters_due_idx ON public.subscription_periods (ends_at)
WHERE ends_at IS NOT NULL AND ended_by IS NULL AND ended_letter_at IS NULL;
