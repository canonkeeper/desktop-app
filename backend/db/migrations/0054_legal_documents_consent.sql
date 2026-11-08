-- The terms and the personal data consent a user accepted: their edition and when.
-- Users registered before stay without them and accept at their next sign-in.
ALTER TABLE public.users
  ADD COLUMN terms_version text,
  ADD COLUMN personal_data_consent_version text,
  ADD COLUMN documents_accepted_at timestamptz,
  ADD CONSTRAINT users_documents_acceptance_complete CHECK (
    (terms_version IS NULL) = (documents_accepted_at IS NULL)
    AND (personal_data_consent_version IS NULL) = (documents_accepted_at IS NULL)
  );
REVOKE ALL ON TABLE public.users FROM PUBLIC;
