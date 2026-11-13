-- The permits of a linked app to work on the models of the site (specs/2026-11-13-desk-permits.md PM1, PM2): before a
-- turn of the chat, a reading of the author's materials or a reading of his books the app asks the site, and the site
-- counts the unit of the author's tier with the same counters as for its own work. A permit is a row of account, not a Job:
-- no worker claims it and no project owns it. The app names its work by an opaque ref; only its SHA-256 is kept, unique
-- for the app and the kind, so a repeat answers the same permit. The site decides a return by what it saw itself: the
-- requests that passed under the permit and whether an answer of the model came. The rows go with the account.
CREATE TABLE public.model_permits (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    device_id uuid NOT NULL,
    kind text NOT NULL,
    quota_key text,
    units integer NOT NULL,
    returned_units integer DEFAULT 0 NOT NULL,
    day date NOT NULL,
    ref_hash text NOT NULL,
    state text DEFAULT 'open' NOT NULL,
    outcome text,
    answered boolean DEFAULT false NOT NULL,
    requests integer DEFAULT 0 NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    cached_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    last_activity_at timestamp with time zone NOT NULL,
    valid_until timestamp with time zone NOT NULL,
    closed_at timestamp with time zone,
    CONSTRAINT model_permits_pkey PRIMARY KEY (id),
    CONSTRAINT model_permits_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT model_permits_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.app_devices(id) ON DELETE CASCADE,
    CONSTRAINT model_permits_ref_key UNIQUE (device_id, kind, ref_hash),
    CONSTRAINT model_permits_kind_check CHECK (
        (kind = 'chat_turn' AND quota_key = 'chat_turns_per_day' AND units = 1)
        OR (kind = 'materials' AND quota_key = 'imports_per_day' AND units = 1)
        OR (kind = 'reading' AND quota_key IS NULL AND units = 0)
    ),
    CONSTRAINT model_permits_returned_check CHECK (returned_units BETWEEN 0 AND units),
    CONSTRAINT model_permits_ref_hash_check CHECK (ref_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT model_permits_state_check CHECK (
        (state = 'open' AND outcome IS NULL AND closed_at IS NULL AND returned_units = 0)
        OR (state = 'closed' AND outcome IN ('answered', 'done', 'canceled', 'failed') AND closed_at IS NOT NULL)
        OR (state = 'expired' AND outcome IN ('answered', 'failed', 'expired_unused') AND closed_at IS NOT NULL)
    ),
    CONSTRAINT model_permits_counts_check CHECK (
        requests >= 0 AND input_tokens >= 0 AND cached_tokens >= 0 AND output_tokens >= 0
    ),
    CONSTRAINT model_permits_times_check CHECK (
        last_activity_at >= created_at AND valid_until > created_at AND (closed_at IS NULL OR closed_at >= created_at)
    )
);

-- The live permits of an author («разом» of the site counts his turns among them) and the permits the watch of their
-- lease looks for.
CREATE INDEX model_permits_user_open_idx ON public.model_permits USING btree (user_id) WHERE (state = 'open');
CREATE INDEX model_permits_lease_idx ON public.model_permits USING btree (valid_until) WHERE (state = 'open');
CREATE INDEX model_permits_closed_idx ON public.model_permits USING btree (closed_at) WHERE (closed_at IS NOT NULL);

-- A correction of a running turn under a permit (PM1.5): one more unit of the key of the permit, charged on the Moscow day
-- of the correction and given back to it once.
CREATE TABLE public.model_permit_units (
    permit_id uuid NOT NULL,
    ref_hash text NOT NULL,
    charged_at timestamp with time zone NOT NULL,
    returned_at timestamp with time zone,
    CONSTRAINT model_permit_units_pkey PRIMARY KEY (permit_id, ref_hash),
    CONSTRAINT model_permit_units_permit_id_fkey FOREIGN KEY (permit_id) REFERENCES public.model_permits(id) ON DELETE CASCADE,
    CONSTRAINT model_permit_units_ref_hash_check CHECK (ref_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT model_permit_units_returned_check CHECK (returned_at IS NULL OR returned_at >= charged_at)
);

REVOKE ALL ON TABLE public.model_permits FROM PUBLIC;
REVOKE ALL ON TABLE public.model_permit_units FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.model_permits TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.model_permit_units TO canonkeeper_product_runtime;

-- PM2.4: the returns a Moscow day of an account takes on the word of an app alone — a failure under which no request
-- passed — are a limit of the fence: the writer's own value, the platform's, the code's (5). The day counts them.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN app_returns_per_day integer CHECK (app_returns_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN app_returns_per_day integer CHECK (app_returns_per_day >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN app_returns integer NOT NULL DEFAULT 0 CHECK (app_returns >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;
