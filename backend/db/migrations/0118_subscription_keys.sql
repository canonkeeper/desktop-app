-- The keys of a tier sold on FunPay and the count of refused keys of an account (specs/2026-11-08-subscription-tiers.md
-- TR4, TR5). A key is a secret: the base keeps only the SHA-256 of its 25 characters; its text leaves the server once,
-- in the file of its batch. The open core does not read these tables; the desktop never has a row of them.

-- TR4.2: a batch of keys of one paid tier, made by the administrator; revoked_at — the unredeemed keys of the batch were
-- revoked (TR4.5).
CREATE TABLE public.subscription_key_batches (
  id uuid PRIMARY KEY,
  tier text NOT NULL REFERENCES public.subscription_tiers(code) CHECK (tier <> 'free'),
  days integer NOT NULL CHECK (days BETWEEN 1 AND 366),
  keys integer NOT NULL CHECK (keys BETWEEN 1 AND 500),
  note text CHECK (char_length(note) BETWEEN 1 AND 120 AND note = btrim(note) AND note !~ '[\r\n]'),
  created_at timestamptz NOT NULL,
  created_by uuid NOT NULL REFERENCES public.users(id),
  revoked_at timestamptz,
  revoked_by uuid REFERENCES public.users(id),
  CONSTRAINT subscription_key_batches_revoked_check CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);
CREATE INDEX subscription_key_batches_created_idx ON public.subscription_key_batches (created_at DESC);
REVOKE ALL ON TABLE public.subscription_key_batches FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON TABLE public.subscription_key_batches TO canonkeeper_product_runtime;

-- TR4.2, TR5.2: a key by its hash. redeemed_at — the key is redeemed, for good; redeemed_by — by whom, cleared by the
-- deletion of the account (TR10.1) while the key stays redeemed; period_id — the period it gave. A key is redeemed or
-- revoked, never both.
CREATE TABLE public.subscription_keys (
  id uuid PRIMARY KEY,
  batch_id uuid NOT NULL REFERENCES public.subscription_key_batches(id),
  key_hash bytea NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32),
  redeemed_by uuid REFERENCES public.users(id),
  redeemed_at timestamptz,
  period_id uuid REFERENCES public.subscription_periods(id) ON DELETE SET NULL,
  revoked_at timestamptz,
  CONSTRAINT subscription_keys_redeemed_or_revoked_check CHECK (redeemed_at IS NULL OR revoked_at IS NULL),
  CONSTRAINT subscription_keys_redeemer_check CHECK (redeemed_by IS NULL OR redeemed_at IS NOT NULL),
  CONSTRAINT subscription_keys_period_check CHECK (period_id IS NULL OR redeemed_at IS NOT NULL)
);
CREATE INDEX subscription_keys_batch_idx ON public.subscription_keys (batch_id);
CREATE INDEX subscription_keys_redeemer_idx ON public.subscription_keys (redeemed_by) WHERE redeemed_by IS NOT NULL;
REVOKE ALL ON TABLE public.subscription_keys FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON TABLE public.subscription_keys TO canonkeeper_product_runtime;

-- TR5.4: the counters of the tiers of an account; this migration brings the count of refused keys of a Moscow day
-- (key_failures_day — the day key_failures belongs to). The trial counters come with their slices.
CREATE TABLE public.subscription_usage (
  user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
  key_failures integer NOT NULL DEFAULT 0 CHECK (key_failures >= 0),
  key_failures_day date,
  updated_at timestamptz NOT NULL
);
REVOKE ALL ON TABLE public.subscription_usage FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.subscription_usage TO canonkeeper_product_runtime;
