-- Every backend process registered and heartbeated a writer lease, but no fence, claim,
-- deletion step or migration ever read it; a failed heartbeat only stopped the process.
-- The leases, their fleet fence key and lifecycle functions are removed.

REVOKE ALL ON TABLE public.governance_writer_leases FROM PUBLIC;

DROP FUNCTION public.ck_register_governance_writer_lease(bytea, text, bytea);
DROP FUNCTION public.ck_heartbeat_governance_writer_lease(bytea, text, bytea);
DROP FUNCTION public.ck_expire_governance_writer_lease(bytea, text, bytea);
DROP FUNCTION public.ck_purge_expired_governance_writer_leases();
DROP FUNCTION public.ck_writer_lease_identity_is_valid(bytea, text, bytea);
DROP FUNCTION public.governance_writer_fleet_fence_key();
DROP TABLE public.governance_writer_leases;
