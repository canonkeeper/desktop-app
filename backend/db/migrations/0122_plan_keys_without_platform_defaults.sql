-- The keys a tier of the site always sets have no limit by default of the platform (specs/2026-11-08-admin-redesign.md
-- AR7.1, AR7.2): every account of the site has a tier, «Бесплатно» at least, and the quota fence puts it above the
-- platform, so a platform value of these keys never held. The admin panel neither shows nor takes them; their old
-- values go here. The columns stay: the fence of the open core reads every key, and a build without a plan lives by the
-- value of the code.
UPDATE public.platform_quota_defaults
SET projects = NULL, account_books = NULL, chat_turns_per_day = NULL, images_per_day = NULL
WHERE projects IS NOT NULL OR account_books IS NOT NULL OR chat_turns_per_day IS NOT NULL OR images_per_day IS NOT NULL;
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
