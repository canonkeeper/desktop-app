-- «Был» of the administrator's pages (specs/2026-11-08-admin-redesign.md AR3.2): the moment the writer himself last
-- opened the site. The read of his own session marks it, not oftener than once in ten minutes; a session an
-- administrator opened on his behalf never does. A session lives 30 sliding days, so the newest login says nothing of a
-- writer who works every day, and the day of the last question says nothing of one who writes, proofreads and draws.
ALTER TABLE public.users
    ADD COLUMN last_seen_at timestamp with time zone;

-- What is known of the visits before the mark: the newest session the writer opened himself and the noon of the last
-- Moscow day he spent anything.
UPDATE public.users AS account
SET last_seen_at = GREATEST(
    (SELECT max(session.created_at) FROM public.sessions AS session
     WHERE session.user_id = account.id AND session.impersonator_user_id IS NULL),
    (SELECT (max(usage.day) + time '12:00') AT TIME ZONE 'Europe/Moscow' FROM public.writer_usage_days AS usage
     WHERE usage.user_id = account.id
       AND usage.chat_turns + usage.imports + usage.images + usage.image_uploads + usage.chapter_drafts
           + usage.drawing_turns + usage.draft_turns + usage.draft_rewrites + usage.voice_seconds > 0)
);

REVOKE ALL ON TABLE public.users FROM PUBLIC;
