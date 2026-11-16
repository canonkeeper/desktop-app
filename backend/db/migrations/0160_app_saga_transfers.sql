-- The transfer of a saga of the site to the linked app (specs/2026-11-16-desk-site-saga.md TR3): the app announces a
-- transfer under its pass with the PKCE challenge of its verifier, the author chooses his saga on the page /transfer under
-- his own sign-in, and the site keeps the SHA-256 of a one-time code bound to the request — its app, its author, the saga
-- and the challenge — for 10 minutes and one download. A newer request of an app puts out the one before it; the rows of
-- an app older than a day go with its next request. Neither a code nor the state of the app is kept. The rows go with the
-- account, the app and the saga.
CREATE TABLE public.app_saga_transfers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    device_id uuid NOT NULL,
    user_id uuid NOT NULL,
    challenge text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    project_id uuid,
    code_hash text,
    code_expires_at timestamp with time zone,
    used_at timestamp with time zone,
    CONSTRAINT app_saga_transfers_pkey PRIMARY KEY (id),
    CONSTRAINT app_saga_transfers_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.app_devices(id) ON DELETE CASCADE,
    CONSTRAINT app_saga_transfers_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT app_saga_transfers_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT app_saga_transfers_code_hash_key UNIQUE (code_hash),
    CONSTRAINT app_saga_transfers_code_hash_check CHECK (code_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT app_saga_transfers_challenge_check CHECK (challenge ~ '^[A-Za-z0-9_-]{43}$'),
    CONSTRAINT app_saga_transfers_term_check CHECK (expires_at > created_at),
    CONSTRAINT app_saga_transfers_code_check CHECK (
        (code_hash IS NULL AND project_id IS NULL AND code_expires_at IS NULL AND used_at IS NULL)
        OR (code_hash IS NOT NULL AND project_id IS NOT NULL AND code_expires_at > created_at)
    ),
    CONSTRAINT app_saga_transfers_used_check CHECK (used_at IS NULL OR used_at >= created_at)
);

CREATE INDEX app_saga_transfers_device_created_idx ON public.app_saga_transfers USING btree (device_id, created_at);
CREATE INDEX app_saga_transfers_user_idx ON public.app_saga_transfers USING btree (user_id);
CREATE INDEX app_saga_transfers_project_idx ON public.app_saga_transfers USING btree (project_id) WHERE (project_id IS NOT NULL);

REVOKE ALL ON TABLE public.app_saga_transfers FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.app_saga_transfers TO canonkeeper_product_runtime;
