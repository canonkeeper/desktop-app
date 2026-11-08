-- Promotion permissions (specs/2026-10-30-promo-permissions.md).

-- R1.2–R1.9, R2.2, R3.2, R5.1–R5.2: what the platform administrator asks the author of a saga to
-- allow for the promotion of CanonKeeper. The recipient is the writer of the saga or, for a saga
-- of the administrator, the author of its books by the address. The record keeps the exact
-- drawings (numbers of the saga gallery) and the exact text of the fragments the author answers
-- for, the places, the attribution and the edition of the text. A pending request expires
-- 30 days after it was made (expires_at; "expired" is derived). A link of a letter is kept as the
-- SHA-256 of its token only, and only while it may still be used: the answer link while the
-- request is pending, the revocation link while the permission is granted. Deleting the project
-- deletes its permissions (R7.1).
CREATE TABLE public.promo_permissions (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
  recipient_user_id uuid REFERENCES public.users(id),
  recipient_email text CHECK (
    recipient_email IS NULL
    OR (recipient_email = lower(btrim(recipient_email)) AND char_length(recipient_email) BETWEEN 3 AND 320)
  ),
  status text NOT NULL CHECK (status IN ('pending', 'granted', 'declined', 'revoked')),
  terms_version text NOT NULL CHECK (terms_version = btrim(terms_version) AND char_length(terms_version) BETWEEN 1 AND 32),
  illustration_numbers integer[] NOT NULL CHECK (
    cardinality(illustration_numbers) <= 24
    AND array_position(illustration_numbers, NULL) IS NULL
    AND 0 < ALL (illustration_numbers)
  ),
  fragments jsonb NOT NULL CHECK (jsonb_typeof(fragments) = 'array' AND jsonb_array_length(fragments) <= 10),
  places text[] NOT NULL CHECK (
    cardinality(places) BETWEEN 1 AND 3
    AND places <@ ARRAY['blog_author_today', 'canonkeeper_home', 'canonkeeper_social']::text[]
  ),
  proposed_attribution text CHECK (proposed_attribution IS NULL OR char_length(btrim(proposed_attribution)) BETWEEN 1 AND 100),
  attribution text CHECK (attribution IS NULL OR char_length(btrim(attribution)) BETWEEN 1 AND 100),
  message text CHECK (message IS NULL OR char_length(btrim(message)) BETWEEN 1 AND 1000),
  requested_by uuid NOT NULL REFERENCES public.users(id),
  created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  answered_at timestamptz,
  revoked_at timestamptz,
  answer_token_hash text UNIQUE CHECK (answer_token_hash ~ '^[0-9a-f]{64}$'),
  revoke_token_hash text UNIQUE CHECK (revoke_token_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT promo_permissions_recipient_check CHECK ((recipient_user_id IS NULL) <> (recipient_email IS NULL)),
  CONSTRAINT promo_permissions_materials_check CHECK (cardinality(illustration_numbers) > 0 OR jsonb_array_length(fragments) > 0),
  CONSTRAINT promo_permissions_term_check CHECK (expires_at > created_at),
  CONSTRAINT promo_permissions_answer_check CHECK ((status = 'pending') = (answered_at IS NULL)),
  CONSTRAINT promo_permissions_revoke_check CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
  CONSTRAINT promo_permissions_attribution_state_check CHECK (attribution IS NULL OR status IN ('granted', 'revoked')),
  CONSTRAINT promo_permissions_answer_link_check CHECK (
    answer_token_hash IS NULL OR (recipient_email IS NOT NULL AND status = 'pending')
  ),
  CONSTRAINT promo_permissions_revoke_link_check CHECK (
    revoke_token_hash IS NULL OR (recipient_email IS NOT NULL AND status = 'granted')
  )
);
CREATE INDEX promo_permissions_project_idx ON public.promo_permissions USING btree (project_id);
CREATE INDEX promo_permissions_recipient_idx ON public.promo_permissions USING btree (recipient_user_id, created_at DESC)
  WHERE recipient_user_id IS NOT NULL;
CREATE INDEX promo_permissions_created_idx ON public.promo_permissions USING btree (created_at DESC);

REVOKE ALL ON TABLE public.promo_permissions FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.promo_permissions TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.promo_permissions TO canonkeeper_governance_owner;
