-- The thread layer of a saga (specs/2026-11-01-writer-assistant-index.md, UC2): the arcs and threads of each
-- book and the registry of the open threads, built from the chapter cards by the saga_layer Job. One
-- row per project; it is derived data: any source deletion removes it in its fence (R2.8). The finished
-- build is digest, chapters_json, books_json, threads_json; the checkpoint columns hold a build under
-- way (R2.6), so a failed or restarted Job goes on from its last step and readers keep the finished one.
CREATE TABLE public.saga_threads (
    project_id uuid NOT NULL,
    digest text,
    chapters_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    books_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    threads_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    checkpoint_books_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    checkpoint_json jsonb,
    model text NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT saga_threads_pkey PRIMARY KEY (project_id),
    CONSTRAINT saga_threads_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_threads_digest_check CHECK (digest IS NULL OR char_length(digest) = 64),
    CONSTRAINT saga_threads_chapters_json_check CHECK (jsonb_typeof(chapters_json) = 'array'),
    CONSTRAINT saga_threads_books_json_check CHECK (jsonb_typeof(books_json) = 'array'),
    CONSTRAINT saga_threads_threads_json_check CHECK (jsonb_typeof(threads_json) = 'array'),
    CONSTRAINT saga_threads_checkpoint_books_json_check CHECK (jsonb_typeof(checkpoint_books_json) = 'array'),
    CONSTRAINT saga_threads_checkpoint_json_check CHECK (checkpoint_json IS NULL OR jsonb_typeof(checkpoint_json) = 'object'),
    CONSTRAINT saga_threads_model_check CHECK (model = btrim(model) AND char_length(model) BETWEEN 1 AND 200)
);

REVOKE ALL ON TABLE public.saga_threads FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_threads TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_threads TO canonkeeper_governance_owner;

-- R2.1: a long Job of its own kind, which only its one worker claims.
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text,
      'illustration'::text, 'saga_layer'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = ANY (ARRAY['research_chat_turn'::text, 'illustration'::text]))
      OR (queue_class = 'automatic'::text AND kind = ANY (ARRAY['chapter_cards'::text, 'saga_layer'::text]))
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'canon_text_index'::text
      ]))
    ),
    DROP CONSTRAINT jobs_governance_payload_state_check,
    ADD CONSTRAINT jobs_governance_payload_state_check CHECK ((
      (
        governance_payload_state = 'active'::text
        AND artifact_source_owner_id IS NOT NULL
        AND jsonb_typeof(subject_json) = 'object'::text
        AND (subject_json ->> 'type'::text) = kind
        AND (subject_json ->> 'projectId'::text) = (project_id)::text
        AND (creator_user_id IS NOT NULL OR kind = ANY (ARRAY['canon_text_index'::text, 'chapter_cards'::text, 'saga_layer'::text]))
      )
      OR (
        governance_payload_state = 'source_deleted'::text
        AND artifact_source_owner_id IS NULL
        AND status = 'expired'::text
        AND subject_json IS NULL
        AND creator_user_id IS NULL
        AND can_cancel = false
        AND progress_json = '{}'::jsonb
        AND result_json IS NULL
        AND error_json IS NULL
      )
    ) IS TRUE);
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;

-- R2.8: the spend of the layer is a source of its own.
ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check CHECK (source IN ('chat', 'scan', 'cards', 'images', 'layer'));
REVOKE ALL ON public.model_usage_days FROM PUBLIC;

-- The dispatcher claims layer Jobs too; everything else is as in 0061.
CREATE OR REPLACE FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(
  wanted_kinds text[], released_before timestamp with time zone
)
RETURNS TABLE(project_id uuid, work_id uuid, kind text, state text, claim_incarnation bigint, exhausted_status text)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path TO 'pg_catalog'
AS $$
DECLARE
  claim_time timestamp with time zone;
  claimed_project_id uuid;
  claimed_work_id uuid;
  claimed_kind text;
  claimed_state text;
  claimed_incarnation bigint;
  claimed_exhausted_status text;
BEGIN
  IF wanted_kinds IS NULL
     OR pg_catalog.cardinality(wanted_kinds) = 0
     OR pg_catalog.cardinality(wanted_kinds) <> (
       SELECT pg_catalog.count(DISTINCT wanted.value)
       FROM pg_catalog.unnest(wanted_kinds) AS wanted(value)
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.unnest(wanted_kinds) AS wanted(value)
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards', 'illustration', 'saga_layer')
     ) THEN
    RETURN;
  END IF;

  claim_time := pg_catalog.date_trunc('microseconds', pg_catalog.clock_timestamp());
  WITH context_candidates AS MATERIALIZED (
    SELECT * FROM public.ck_import_job_claim_candidate(wanted_kinds)
    UNION ALL
    SELECT * FROM public.ck_simple_job_claim_candidate(wanted_kinds)
  ), candidate AS (
    SELECT job.id, job.project_id, context_candidate.exhausted_status
    FROM public.jobs AS job
    JOIN context_candidates AS context_candidate
      ON context_candidate.project_id = job.project_id
     AND context_candidate.id = job.id
     AND context_candidate.kind = job.kind
    WHERE job.kind = ANY(wanted_kinds)
      AND job.governance_payload_state = 'active'
      AND public.ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id, job.project_id, 'job', job.id
      )
      AND (
        (job.status = 'queued' AND job.next_attempt_at <= claim_time)
        OR (
          job.status IN ('running', 'canceling')
          AND (
            job.updated_at < claim_time - interval '15 minutes'
            OR job.claimed_at < released_before
          )
        )
      )
    ORDER BY
      CASE job.queue_class WHEN 'interactive' THEN 0 WHEN 'user_demanded' THEN 1 ELSE 2 END,
      (
        SELECT pg_catalog.count(*)
        FROM public.jobs AS busy
        WHERE busy.creator_user_id = job.creator_user_id
          AND busy.kind = job.kind
          AND busy.status IN ('running', 'canceling')
          AND busy.id <> job.id
      ),
      job.next_attempt_at, job.created_at, job.id
    LIMIT 1
    FOR UPDATE OF job SKIP LOCKED
  )
  UPDATE public.jobs AS job
  SET status = CASE WHEN job.status = 'queued' THEN 'running' ELSE job.status END,
      progress_json = CASE
        WHEN job.kind = 'research_chat_turn' AND job.status IN ('queued', 'running')
          THEN pg_catalog.jsonb_build_object('phase', 'preparing_context')
        ELSE job.progress_json
      END,
      claimed_at = claim_time,
      started_at = COALESCE(job.started_at, claim_time),
      claim_incarnation = job.claim_incarnation + 1,
      retry_count = CASE
        WHEN job.status IN ('running', 'canceling') AND job.retry_count < 2 THEN job.retry_count + 1
        ELSE job.retry_count
      END,
      updated_at = claim_time
  FROM candidate
  WHERE job.id = candidate.id AND job.project_id = candidate.project_id
  RETURNING job.project_id, job.id, job.kind, job.status, job.claim_incarnation,
    candidate.exhausted_status
  INTO claimed_project_id, claimed_work_id, claimed_kind, claimed_state,
    claimed_incarnation, claimed_exhausted_status;

  IF NOT FOUND THEN
    RETURN;
  END IF;

  RETURN QUERY SELECT claimed_project_id, claimed_work_id, claimed_kind,
    claimed_state, claimed_incarnation, claimed_exhausted_status;
END;
$$;

ALTER FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[], timestamp with time zone)
  OWNER TO canonkeeper_governance_owner;
REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[], timestamp with time zone) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[], timestamp with time zone)
  TO canonkeeper_worker_discovery, canonkeeper_governance_owner;
