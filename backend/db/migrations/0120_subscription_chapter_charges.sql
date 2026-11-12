-- A new chapter the writing agent writes counts against the writer's tier (specs/2026-11-08-subscription-tiers.md TR7):
-- a window of K days or a trial new chapter for the life of the account. A charge is one row per new chapter, written in
-- the transaction that starts the first Job of scenes of a draft; it counts once the Job wrote a scene (scene_at) or
-- while it is queued or runs, so a Job that ended without a scene gives the chapter back by itself. The open core does
-- not read this table; the desktop never has a row of it.

-- TR7.2, TR7.3, TR7.5: draft_id and job_id have no foreign key — a charge outlives the draft and the project, whose Jobs
-- go with it, so a deletion never resets the window; returned_at — the administrator gave the trial chapter back
-- (TR3.4). The trial new chapters used are the counted rows of kind trial: no counter of their own.
CREATE TABLE public.subscription_chapter_charges (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('window', 'trial')),
  draft_id uuid,
  job_id uuid NOT NULL,
  charged_at timestamptz NOT NULL,
  scene_at timestamptz,
  returned_at timestamptz,
  CONSTRAINT subscription_chapter_charges_scene_check CHECK (scene_at IS NULL OR scene_at >= charged_at),
  CONSTRAINT subscription_chapter_charges_returned_check CHECK (returned_at IS NULL OR returned_at >= charged_at)
);
CREATE INDEX subscription_chapter_charges_account_idx
  ON public.subscription_chapter_charges (user_id, kind, charged_at DESC) WHERE returned_at IS NULL;
CREATE INDEX subscription_chapter_charges_draft_idx
  ON public.subscription_chapter_charges (draft_id) WHERE returned_at IS NULL;
CREATE INDEX subscription_chapter_charges_job_idx ON public.subscription_chapter_charges (job_id);
REVOKE ALL ON TABLE public.subscription_chapter_charges FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.subscription_chapter_charges TO canonkeeper_product_runtime;
