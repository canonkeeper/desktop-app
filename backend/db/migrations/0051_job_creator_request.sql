-- A Job keeps the id of the HTTP request that created it (specs/2026-10-27-stage-0-protection.md,
-- R3.1): the support code of a failed Job leads to that request and to the Job's own log lines.
-- Jobs queued by workers and Jobs created before this migration have none; their support code is
-- the Job id.
ALTER TABLE public.jobs ADD COLUMN creator_request_id uuid;

-- The table keeps its runtime privileges; PUBLIC stays without access.
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;
