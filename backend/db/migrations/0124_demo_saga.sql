-- The demo saga (specs/2026-11-09-demo-saga.md DS3.1): a saga the site makes for an author who has no manuscript at hand,
-- with a built-in book. The mark lives with the saga and is set only when it is made; the quota fence reads it here:
-- a demo saga is out of the limits of its owner and takes no text but its seed.
ALTER TABLE public.projects
  ADD COLUMN demo boolean NOT NULL DEFAULT false;

-- An owner has one live demo saga: the second «Попробовать на примере» leads to the first, and a deleted one comes
-- back only to a free place.
CREATE UNIQUE INDEX projects_one_live_demo_per_owner
  ON public.projects (owner_user_id)
  WHERE demo AND deleted_at IS NULL;

REVOKE ALL ON public.projects FROM PUBLIC;
