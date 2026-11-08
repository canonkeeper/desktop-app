-- Account data rights (specs/2026-10-28-account-data-rights.md).
-- The child session an administrator opens on behalf of a user names that administrator: the
-- session row is the whole impersonation, its created_at, revoked_at and expires_at are the
-- journal (UC5).
ALTER TABLE public.sessions
  ADD COLUMN impersonator_user_id uuid REFERENCES public.users(id),
  ADD CONSTRAINT sessions_impersonator_is_another_user CHECK (impersonator_user_id <> user_id);
CREATE INDEX sessions_impersonation_journal_idx
  ON public.sessions (created_at DESC)
  WHERE impersonator_user_id IS NOT NULL;
REVOKE ALL ON TABLE public.sessions FROM PUBLIC;

-- A deleted account keeps its row without personal data: sessions, turns and imports in other
-- projects and deletion tombstones still reference its id (UC3). A deleted account stays disabled.
ALTER TABLE public.users
  ADD COLUMN deleted_at timestamptz,
  ADD CONSTRAINT users_deleted_account_is_disabled CHECK (deleted_at IS NULL OR disabled_at IS NOT NULL);
REVOKE ALL ON TABLE public.users FROM PUBLIC;
