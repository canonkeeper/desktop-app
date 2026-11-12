-- The subscription tiers of the site and the periods of a writer's tier (specs/2026-11-08-subscription-tiers.md TR1,
-- TR3). The open core does not read them: the site's plan of the quota fence does (TR2.3). The desktop never has a row
-- of periods, so its writer stays without a plan.

-- TR1.1, TR1.2: the four tiers, the link to their lot and their ten limits. The price lives only on the page of the lot
-- on FunPay, the site keeps none. The migration sets the tiers; the administrator changes their values and never adds
-- or removes one. chapter_every_days NULL — no window of new chapters, only the trial ones; updated_by NULL — the values
-- of this migration.
CREATE TABLE public.subscription_tiers (
  code text PRIMARY KEY CHECK (code IN ('free', 'artist', 'writer', 'pro')),
  rank smallint NOT NULL UNIQUE CHECK (rank BETWEEN 0 AND 3),
  title text NOT NULL CHECK (title = btrim(title) AND char_length(title) BETWEEN 1 AND 40),
  lot_url text CHECK (lot_url ~ '^https://funpay\.com/' AND char_length(lot_url) <= 300),
  projects integer NOT NULL CHECK (projects >= 0),
  account_books integer NOT NULL CHECK (account_books >= 0),
  chat_turns_per_day integer NOT NULL CHECK (chat_turns_per_day >= 0),
  images_per_day integer NOT NULL CHECK (images_per_day >= 0),
  trial_images integer NOT NULL CHECK (trial_images >= 0),
  chapter_every_days integer CHECK (chapter_every_days >= 1),
  trial_chapters integer NOT NULL CHECK (trial_chapters >= 0),
  voice_minutes_per_day integer NOT NULL CHECK (voice_minutes_per_day >= 0),
  trial_voice_calls integer NOT NULL CHECK (trial_voice_calls >= 0),
  trial_voice_call_minutes integer NOT NULL CHECK (trial_voice_call_minutes >= 1),
  updated_at timestamptz NOT NULL,
  updated_by uuid REFERENCES public.users(id),
  CONSTRAINT subscription_tiers_free_lot_check CHECK (code <> 'free' OR lot_url IS NULL)
);
REVOKE ALL ON TABLE public.subscription_tiers FROM PUBLIC;
GRANT SELECT, UPDATE ON TABLE public.subscription_tiers TO canonkeeper_product_runtime;

-- TR1.5: the values the owner agreed on 08.11; the links of the lots stay empty until the launch (S6).
INSERT INTO public.subscription_tiers (
  code, rank, title, projects, account_books, chat_turns_per_day, images_per_day, trial_images,
  chapter_every_days, trial_chapters, voice_minutes_per_day, trial_voice_calls, trial_voice_call_minutes, updated_at
) VALUES
  ('free', 0, 'Бесплатно', 1, 1, 5, 0, 3, NULL, 1, 0, 3, 5, now()),
  ('artist', 1, 'Художник', 2, 6, 10, 3, 0, 10, 0, 0, 3, 5, now()),
  ('writer', 2, 'Писатель', 3, 12, 20, 10, 0, 5, 0, 60, 0, 5, now()),
  ('pro', 3, 'Профи', 5, 30, 30, 20, 0, 2, 0, 60, 0, 5, now());

-- TR3.1: a period of a tier. Every grant is its own row; periods never merge, and the time of an earlier one runs in
-- parallel with a later one: the tier in force is the highest of those running (TR2.1). ends_at NULL — without term;
-- ends_at = starts_at — a future period ended before it began. granted_by is the administrator, NULL for a key and the
-- operator's commands; ended_by the administrator who ended it early.
CREATE TABLE public.subscription_periods (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  tier text NOT NULL REFERENCES public.subscription_tiers(code) CHECK (tier <> 'free'),
  starts_at timestamptz NOT NULL,
  ends_at timestamptz CHECK (ends_at IS NULL OR ends_at >= starts_at),
  source text NOT NULL CHECK (source IN ('key', 'admin', 'launch')),
  granted_by uuid REFERENCES public.users(id),
  ended_by uuid REFERENCES public.users(id),
  created_at timestamptz NOT NULL
);
CREATE INDEX subscription_periods_user_idx ON public.subscription_periods (user_id, starts_at);
-- TR3.6: one period of the launch per account, so a repeated launch adds nothing.
CREATE UNIQUE INDEX subscription_periods_launch_key ON public.subscription_periods (user_id) WHERE source = 'launch';
REVOKE ALL ON TABLE public.subscription_periods FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.subscription_periods TO canonkeeper_product_runtime;
