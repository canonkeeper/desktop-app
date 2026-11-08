-- Limits by default of the platform (specs/2026-10-29-admin-panel.md UC7).

-- R7.1: the administrator's values for every writer without his own limit (writer_quota_overrides)
-- and the trial drawings of an account without its right to illustrations (illustration_access).
-- One row at most; it appears on the first change. NULL — and no row — keeps the value of the code
-- (domain/quota.Defaults, domain/illustrations.TrialDrawings). R7.2: a writer's limit in force is
-- his own value, else this row's, else the code's; the quota checks read both rows in one query.
CREATE TABLE public.platform_quota_defaults (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  chat_turns_per_day integer CHECK (chat_turns_per_day >= 0),
  active_turns integer CHECK (active_turns >= 0),
  imports_per_day integer CHECK (imports_per_day >= 0),
  projects integer CHECK (projects >= 0),
  project_books integer CHECK (project_books >= 0),
  project_chapters integer CHECK (project_chapters >= 0),
  project_characters bigint CHECK (project_characters >= 0),
  account_books integer CHECK (account_books >= 0),
  account_chapters integer CHECK (account_chapters >= 0),
  account_characters bigint CHECK (account_characters >= 0),
  images_per_day integer CHECK (images_per_day >= 0),
  trial_drawings integer CHECK (trial_drawings >= 0),
  updated_at timestamptz NOT NULL,
  updated_by uuid NOT NULL REFERENCES public.users(id)
);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.platform_quota_defaults TO canonkeeper_product_runtime;
