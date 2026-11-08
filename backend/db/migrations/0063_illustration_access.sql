-- Access to illustrations (specs/2026-10-29-saga-illustrations.md UC11): a right of the account,
-- the chat toggle «Рисунки» and the requests for access.

-- R11.1: the right of an account as the administrator left it (none, active, blocked) and the
-- trial drawings not spent yet. Like writer_quota_overrides, a row appears on the first change:
-- an account without a row has the default of the code (domain/illustrations.DefaultAccess: none
-- with 5 trial drawings), so every existing and new account starts with the same trial. An active
-- right lasts through active_until, a Moscow calendar day, or without end when it is NULL.
CREATE TABLE public.illustration_access (
  user_id uuid PRIMARY KEY REFERENCES public.users(id),
  state text NOT NULL CHECK (state IN ('none', 'active', 'blocked')),
  trial_left integer NOT NULL CHECK (trial_left >= 0),
  active_until date,
  granted_by uuid REFERENCES public.users(id),
  updated_at timestamptz NOT NULL,
  CONSTRAINT illustration_access_until_check CHECK (state = 'active' OR active_until IS NULL)
);
REVOKE ALL ON TABLE public.illustration_access FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.illustration_access TO canonkeeper_product_runtime;

-- R11.5: a request for access. One pending request per account; a repeat while it is pending
-- writes to support again at most once a day (sent_at). The administrator grants or declines it.
CREATE TABLE public.illustration_access_requests (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES public.users(id),
  status text NOT NULL CHECK (status IN ('pending', 'granted', 'declined')),
  message text CHECK (message IS NULL OR char_length(btrim(message)) BETWEEN 1 AND 1000),
  created_at timestamptz NOT NULL,
  sent_at timestamptz NOT NULL,
  decided_at timestamptz,
  decided_by uuid REFERENCES public.users(id),
  CONSTRAINT illustration_access_requests_decided_check CHECK ((status = 'pending') = (decided_at IS NULL))
);
CREATE UNIQUE INDEX illustration_access_requests_pending_key
  ON public.illustration_access_requests USING btree (user_id) WHERE status = 'pending';
CREATE INDEX illustration_access_requests_created_idx
  ON public.illustration_access_requests USING btree (created_at DESC);
REVOKE ALL ON TABLE public.illustration_access_requests FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.illustration_access_requests TO canonkeeper_product_runtime;

-- R11.2 (decision 29.10): the four style sketches of the first style choice of a saga are free
-- once per project: free_sketches_at marks the set that was free, so deleting it gives no other.
ALTER TABLE public.project_illustration_settings
  ADD COLUMN free_sketches_at timestamptz;
REVOKE ALL ON TABLE public.project_illustration_settings FROM PUBLIC;

-- R11.3: the toggle «Рисунки» under the chat input: the model may draw in the chat only while it
-- is on (and the author of the turn has the right).
ALTER TABLE public.chat_sessions
  ADD COLUMN illustrations_enabled boolean NOT NULL DEFAULT true;
REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;
