-- Drawings by the tier instead of the right to illustrations (specs/2026-11-08-subscription-tiers.md TR6): a writer
-- draws by the day while his images_per_day in force is above zero, else the trial drawings of his tier for the life of
-- the account; the administrator only blocks drawing. The requests for access and access until a day are gone.

-- TR5 (Tables), TR6.1: what an account used of the trials of its tiers for its whole life; the trial left is the value of
-- the tier in force less what was used, so a change of the tier in the admin panel holds for everyone at once. The
-- counters of an account are one row of subscription_usage (0118); a row appears on the first trial drawing or refused
-- key; no row — nothing used.
ALTER TABLE public.subscription_usage
  ADD COLUMN trial_images_used integer NOT NULL DEFAULT 0 CHECK (trial_images_used >= 0);

-- TR6.6: what an account spent of the old trial counts against the trial of 3 drawings of «Бесплатно»: 5 left — none
-- used, 1 left — 2 used, none left — 3 used.
INSERT INTO public.subscription_usage (user_id, trial_images_used, updated_at)
SELECT user_id, GREATEST(0, 3 - trial_left), now()
FROM public.illustration_access
ON CONFLICT (user_id) DO UPDATE SET trial_images_used = EXCLUDED.trial_images_used, updated_at = EXCLUDED.updated_at;

-- TR6.6: access the administrator turned on becomes none — drawing by the day is the tier's now; illustration_access
-- keeps only the block.
ALTER TABLE public.illustration_access DROP CONSTRAINT illustration_access_until_check;
UPDATE public.illustration_access SET state = 'none' WHERE state = 'active';
ALTER TABLE public.illustration_access DROP COLUMN trial_left;
ALTER TABLE public.illustration_access DROP COLUMN active_until;
ALTER TABLE public.illustration_access DROP CONSTRAINT illustration_access_state_check;
ALTER TABLE public.illustration_access ADD CONSTRAINT illustration_access_state_check CHECK (state IN ('none', 'blocked'));
REVOKE ALL ON TABLE public.illustration_access FROM PUBLIC;

-- TR6.4: the requests for access are gone with their pages and letters; the trial is the tier's, not the platform's.
DROP TABLE public.illustration_access_requests;
ALTER TABLE public.platform_quota_defaults DROP COLUMN trial_drawings;
REVOKE ALL ON TABLE public.platform_quota_defaults FROM PUBLIC;
