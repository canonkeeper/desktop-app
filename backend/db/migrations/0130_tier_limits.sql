-- The limits of the tiers as one grid (specs/2026-11-11-tier-limits.md TL1, TL2, TL3): a tier sets every key of the
-- quota fence that spends the model or holds text, not four, and a tier with a window allows N new chapters in any K
-- days instead of one. The prices, the offer, the keys, the periods, the trials and the voice stay as they are, and no
-- value of a paid tier goes down (TL1.2).

-- TL2.1: the keys of the fence a tier sets now. project_books — the books of one saga, what the page promises (the owner,
-- 11.11: the books count in every saga); account_books stays the guard of all the sagas. project_chapters and
-- project_characters have no columns: the plan sets them equal to the values of the account, so one saga holds all the
-- tier allows. TL3.1: chapters_per_window — the new chapters of a window of chapter_every_days days; both are NULL on a
-- tier without a window. The defaults only fill the rows of this migration and are dropped below.
ALTER TABLE public.subscription_tiers
  ADD COLUMN project_books integer NOT NULL DEFAULT 1 CHECK (project_books >= 0),
  ADD COLUMN active_turns integer NOT NULL DEFAULT 2 CHECK (active_turns >= 1),
  ADD COLUMN imports_per_day integer NOT NULL DEFAULT 30 CHECK (imports_per_day >= 1),
  ADD COLUMN account_chapters integer NOT NULL DEFAULT 5000 CHECK (account_chapters >= 0),
  ADD COLUMN account_characters bigint NOT NULL DEFAULT 25000000 CHECK (account_characters >= 0),
  ADD COLUMN chapter_drafts_per_day integer NOT NULL DEFAULT 20 CHECK (chapter_drafts_per_day >= 0),
  ADD COLUMN drawing_turns_per_day integer NOT NULL DEFAULT 60 CHECK (drawing_turns_per_day >= 0),
  ADD COLUMN draft_turns_per_day integer NOT NULL DEFAULT 60 CHECK (draft_turns_per_day >= 0),
  ADD COLUMN draft_rewrites_per_day integer NOT NULL DEFAULT 200 CHECK (draft_rewrites_per_day >= 0),
  ADD COLUMN chapters_per_window integer CHECK (chapters_per_window >= 1);

-- TL1.1: the values of the four tiers, one row per tier. The administrator changes them in «Тарифы и ключи» afterwards;
-- updated_by NULL — the values of a migration.
UPDATE public.subscription_tiers AS tier
SET projects = grid.projects, project_books = grid.project_books, account_books = grid.account_books, account_characters = grid.account_characters,
    account_chapters = grid.account_chapters, chat_turns_per_day = grid.chat_turns_per_day, active_turns = grid.active_turns,
    images_per_day = grid.images_per_day, trial_images = grid.trial_images, drawing_turns_per_day = grid.drawing_turns_per_day,
    chapters_per_window = grid.chapters_per_window, chapter_every_days = grid.chapter_every_days,
    trial_chapters = grid.trial_chapters, chapter_drafts_per_day = grid.chapter_drafts_per_day,
    draft_turns_per_day = grid.draft_turns_per_day, draft_rewrites_per_day = grid.draft_rewrites_per_day,
    imports_per_day = grid.imports_per_day, voice_minutes_per_day = grid.voice_minutes_per_day,
    trial_voice_calls = grid.trial_voice_calls, trial_voice_call_minutes = grid.trial_voice_call_minutes,
    updated_at = now(), updated_by = NULL
FROM (VALUES
  -- code,   sagas, books of a saga, books of all sagas, characters, chapters, questions, at once, drawings, trial drawings,
  --         drawing helper, chapters in a window, window days, trial chapters, writing, writing helper, rewrites, uploads,
  --         voice minutes, trial calls, trial call minutes
  ('free',   1,  1,   1,  3000000,   600,  5, 2,  0, 3,   5, NULL::integer, NULL::integer, 1,  5,  10,  20,  5,  0, 3, 5),
  ('artist', 2,  6,  12, 25000000,  5000, 15, 2, 10, 0,  60,  2, 7, 0, 20,  60, 200, 30,  0, 3, 5),
  ('writer', 3, 12,  36, 40000000,  8000, 30, 2, 15, 0,  60,  7, 7, 0, 30,  80, 200, 30, 60, 0, 5),
  ('pro',    5, 30, 150, 80000000, 15000, 60, 4, 30, 0, 100, 30, 7, 0, 50, 120, 300, 30, 60, 0, 5)
) AS grid (code, projects, project_books, account_books, account_characters, account_chapters, chat_turns_per_day, active_turns,
           images_per_day, trial_images, drawing_turns_per_day, chapters_per_window, chapter_every_days, trial_chapters,
           chapter_drafts_per_day, draft_turns_per_day, draft_rewrites_per_day, imports_per_day, voice_minutes_per_day,
           trial_voice_calls, trial_voice_call_minutes)
WHERE tier.code = grid.code;

ALTER TABLE public.subscription_tiers
  ALTER COLUMN project_books DROP DEFAULT,
  ALTER COLUMN active_turns DROP DEFAULT,
  ALTER COLUMN imports_per_day DROP DEFAULT,
  ALTER COLUMN account_chapters DROP DEFAULT,
  ALTER COLUMN account_characters DROP DEFAULT,
  ALTER COLUMN chapter_drafts_per_day DROP DEFAULT,
  ALTER COLUMN drawing_turns_per_day DROP DEFAULT,
  ALTER COLUMN draft_turns_per_day DROP DEFAULT,
  ALTER COLUMN draft_rewrites_per_day DROP DEFAULT,
  ADD CONSTRAINT subscription_tiers_window_check CHECK ((chapter_every_days IS NULL) = (chapters_per_window IS NULL));
REVOKE ALL ON TABLE public.subscription_tiers FROM PUBLIC;

-- TL2.2: the keys a tier sets have no platform default, as the first four since 0122: the tab «Лимиты» neither shows nor
-- takes them, and their old values go here. image_uploads_per_day alone stays the platform's: it spends no model.
UPDATE public.platform_quota_defaults
SET active_turns = NULL, imports_per_day = NULL, project_books = NULL, project_chapters = NULL, project_characters = NULL,
    account_chapters = NULL, account_characters = NULL, chapter_drafts_per_day = NULL, drawing_turns_per_day = NULL,
    draft_turns_per_day = NULL, draft_rewrites_per_day = NULL
WHERE active_turns IS NOT NULL OR imports_per_day IS NOT NULL OR project_books IS NOT NULL OR project_chapters IS NOT NULL
   OR project_characters IS NOT NULL OR account_chapters IS NOT NULL OR account_characters IS NOT NULL OR chapter_drafts_per_day IS NOT NULL
   OR drawing_turns_per_day IS NOT NULL OR draft_turns_per_day IS NOT NULL OR draft_rewrites_per_day IS NOT NULL;
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
