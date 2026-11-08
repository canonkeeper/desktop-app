-- Account mail: email verification and password reset links. Only the SHA-256 of a
-- token is stored; a token works while users.email still equals the address it went to.
CREATE TABLE public.account_tokens (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES public.users(id),
 purpose text NOT NULL CHECK (purpose IN ('email_verification', 'password_reset')),
 token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
 email text NOT NULL,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 used_at timestamptz,
 CHECK (expires_at > created_at)
);
CREATE INDEX account_tokens_user_purpose_created_idx ON public.account_tokens(user_id, purpose, created_at DESC);
REVOKE ALL ON public.account_tokens FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.account_tokens TO canonkeeper_product_runtime;

-- A persistent ("remember me") session lives 30 days and slides on use.
ALTER TABLE public.sessions ADD COLUMN persistent boolean NOT NULL DEFAULT false;
REVOKE ALL ON TABLE public.sessions FROM PUBLIC;

-- Everyone enabled before email verification counts as verified (owner decision).
UPDATE public.users
SET email_verified_at = created_at
WHERE email_verified_at IS NULL
  AND disabled_at IS NULL;
