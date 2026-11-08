-- Saga memory chat. A chat remembers the agent tool generation it was created with:
-- 1 is the summary/graph tool set, 2 is the saga memory set (search, read, chapters).
-- Chats of an older generation stay readable but take no new turns. The default is the
-- current generation; existing chats are the summary/graph generation.
ALTER TABLE public.chat_sessions
    ADD COLUMN agent_generation integer NOT NULL DEFAULT 1,
    ADD CONSTRAINT chat_sessions_agent_generation_check CHECK (agent_generation >= 1);
ALTER TABLE public.chat_sessions ALTER COLUMN agent_generation SET DEFAULT 2;

-- Chat answers cite chapters by key and paragraph range; issued source refs, their
-- reference snapshots and the projected timeline references are gone.
UPDATE public.jobs
   SET result_json = result_json - 'timelineReferences'
 WHERE kind = 'research_chat_turn'
   AND jsonb_typeof(result_json) = 'object'
   AND result_json ? 'timelineReferences';
ALTER TABLE public.agent_tool_refs DROP COLUMN reference_snapshot;

REVOKE ALL ON public.chat_sessions FROM PUBLIC;
REVOKE ALL ON public.agent_tool_refs FROM PUBLIC;

-- A chat turn left running or canceling by a stopped backend is reclaimed once its
-- heartbeat lapses, like export and text index Jobs. The reclaimed run reconciles the
-- persisted native turn of the same root thread and finishes the Job.
CREATE OR REPLACE FUNCTION public.ck_simple_job_claim_candidate(wanted_kinds text[])
RETURNS TABLE(project_id uuid, id uuid, kind text, source_view text, exhausted_status text)
LANGUAGE sql SECURITY DEFINER
SET search_path TO 'pg_catalog', 'public'
AS $$
  SELECT job.project_id, job.id, job.kind, job.subject_json->>'sourceView',
    CASE
      WHEN job.status = 'canceling' AND job.retry_count >= 2 THEN 'canceled'
      WHEN job.status = 'running' AND job.retry_count >= 2 THEN 'failed'
      ELSE NULL
    END AS exhausted_status
  FROM public.jobs AS job
  WHERE job.kind = ANY(wanted_kinds)
    AND job.kind NOT IN ('import', 'canon_book_enrichment')
    AND job.governance_payload_state = 'active'
    AND public.ck_artifact_source_owner_is_readable(
      job.artifact_source_owner_id, job.project_id, 'job', job.id
    )
    AND (
      (job.status = 'queued' AND job.next_attempt_at <= pg_catalog.clock_timestamp())
      OR (
        job.kind IN ('export', 'canon_text_index', 'research_chat_turn')
        AND job.status IN ('running', 'canceling')
        AND job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
      )
    )
$$;

REVOKE ALL ON FUNCTION public.ck_simple_job_claim_candidate(text[]) FROM PUBLIC;
