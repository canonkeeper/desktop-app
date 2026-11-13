-- The link of the CanonKeeper app on the computer of an author with his account of the site (specs/2026-11-13-desk-link.md
-- LK1, LK2, LK4): the one-time code the page /link gives the app back through 127.0.0.1, for 5 minutes and once, bound to
-- the PKCE challenge of the app and the port of its return; and the linked app with its pass. Only the SHA-256 of a code
-- and of a pass is kept, with the first 6 signs of the pass for the author's list; the address of the return is built from
-- the port and is not kept, nor is the state of the app. The site writes the rows; they go with the account.
CREATE TABLE public.app_link_codes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    code_hash text NOT NULL,
    challenge text NOT NULL,
    port integer NOT NULL,
    platform text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    CONSTRAINT app_link_codes_pkey PRIMARY KEY (id),
    CONSTRAINT app_link_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT app_link_codes_code_hash_key UNIQUE (code_hash),
    CONSTRAINT app_link_codes_code_hash_check CHECK (code_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT app_link_codes_challenge_check CHECK (challenge ~ '^[A-Za-z0-9_-]{43}$'),
    CONSTRAINT app_link_codes_port_check CHECK (port BETWEEN 20000 AND 29999),
    CONSTRAINT app_link_codes_platform_check CHECK (platform IN ('windows', 'linux')),
    CONSTRAINT app_link_codes_term_check CHECK (expires_at > created_at),
    CONSTRAINT app_link_codes_used_check CHECK (used_at IS NULL OR used_at >= created_at)
);

CREATE INDEX app_link_codes_user_created_idx ON public.app_link_codes USING btree (user_id, created_at);

-- A linked app: the name of its computer, its system and version, when it was linked and last seen (at most once in 10
-- minutes), and when and why it was unlinked. The code it was issued for is named, so a second exchange of the code
-- unlinks it (LK2.3).
CREATE TABLE public.app_devices (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    link_code_id uuid,
    pass_hash text NOT NULL,
    pass_prefix text NOT NULL,
    name text NOT NULL,
    platform text NOT NULL,
    app_version text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone,
    revoked_reason text,
    CONSTRAINT app_devices_pkey PRIMARY KEY (id),
    CONSTRAINT app_devices_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT app_devices_link_code_id_fkey FOREIGN KEY (link_code_id)
        REFERENCES public.app_link_codes(id) ON DELETE SET NULL,
    CONSTRAINT app_devices_pass_hash_key UNIQUE (pass_hash),
    CONSTRAINT app_devices_pass_hash_check CHECK (pass_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT app_devices_pass_prefix_check CHECK (pass_prefix ~ '^ckd_[A-Za-z0-9_-]{2}$'),
    CONSTRAINT app_devices_name_check CHECK (char_length(name) BETWEEN 1 AND 80),
    CONSTRAINT app_devices_platform_check CHECK (platform IN ('windows', 'linux')),
    CONSTRAINT app_devices_app_version_check CHECK (char_length(app_version) BETWEEN 1 AND 32),
    CONSTRAINT app_devices_used_check CHECK (last_used_at IS NULL OR last_used_at >= created_at),
    CONSTRAINT app_devices_revoked_check CHECK (
        (revoked_at IS NULL AND revoked_reason IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_reason IN ('owner', 'app', 'logout_everywhere', 'password', 'reused_code', 'admin'))
    )
);

CREATE UNIQUE INDEX app_devices_link_code_idx ON public.app_devices USING btree (link_code_id) WHERE (link_code_id IS NOT NULL);
CREATE INDEX app_devices_user_live_idx ON public.app_devices USING btree (user_id, created_at) WHERE (revoked_at IS NULL);

REVOKE ALL ON TABLE public.app_link_codes FROM PUBLIC;
REVOKE ALL ON TABLE public.app_devices FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.app_link_codes TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.app_devices TO canonkeeper_product_runtime;
