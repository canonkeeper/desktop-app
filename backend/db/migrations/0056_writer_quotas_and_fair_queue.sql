-- Writer quotas, model usage accounting and the fair chat queue
-- (specs/2026-10-28-writer-quotas-and-fair-queue.md).

-- UC1: a writer's own limits. NULL keeps the default from the code (domain/quota.Defaults);
-- the operator command changes a row, a new default is a release.
CREATE TABLE public.writer_quota_overrides (
  user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
  chat_turns_per_day integer CHECK (chat_turns_per_day >= 0),
  active_turns integer CHECK (active_turns >= 0),
  imports_per_day integer CHECK (imports_per_day >= 0),
  projects integer CHECK (projects >= 0),
  project_books integer CHECK (project_books >= 0),
  project_chapters integer CHECK (project_chapters >= 0),
  project_characters bigint CHECK (project_characters >= 0),
  account_books integer CHECK (account_books >= 0),
  account_chapters integer CHECK (account_chapters >= 0),
  account_characters bigint CHECK (account_characters >= 0),
  updated_at timestamptz NOT NULL
);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.writer_quota_overrides TO canonkeeper_product_runtime;

-- UC2, UC3: accepted questions and uploads of a writer per Moscow calendar day.
CREATE TABLE public.writer_usage_days (
  user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  day date NOT NULL,
  chat_turns integer NOT NULL DEFAULT 0 CHECK (chat_turns >= 0),
  imports integer NOT NULL DEFAULT 0 CHECK (imports >= 0),
  PRIMARY KEY (user_id, day)
);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.writer_usage_days TO canonkeeper_product_runtime;

-- UC2, UC10: active Jobs of one creator are counted without reading the Job history.
CREATE INDEX jobs_active_by_creator_idx ON public.jobs USING btree (creator_user_id, kind)
  WHERE status IN ('queued', 'running', 'canceling');

-- UC10: the fair chat queue. Within a queue class a Job of the creator with fewer Jobs of the
-- same kind in work is claimed first; a Job without a creator counts 0. The count reads only
-- active Jobs through jobs_active_by_creator_idx. Claim rights, retries and stale claims are as
-- before (0055).
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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards')
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

-- UC12: a project runs one chapter cards Job at a time, so the cards of a chapter are not built
-- twice by the draft and the published version or by a yielded Job and a new one. A cards Job of
-- the project in work without a heartbeat for 15 minutes does not hold the others back.
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
    AND job.kind <> 'import'
    AND job.governance_payload_state = 'active'
    AND public.ck_artifact_source_owner_is_readable(
      job.artifact_source_owner_id, job.project_id, 'job', job.id
    )
    AND job.status IN ('queued', 'running', 'canceling')
    AND (
      job.kind <> 'chapter_cards'
      OR NOT EXISTS (
        SELECT 1
        FROM public.jobs AS other
        WHERE other.project_id = job.project_id
          AND other.kind = 'chapter_cards'
          AND other.id <> job.id
          AND other.status IN ('running', 'canceling')
          AND other.updated_at >= pg_catalog.clock_timestamp() - interval '15 minutes'
      )
    )
$$;

-- UC7, UC8: model spend per Moscow day, writer, project, source and model. A row keeps only ids
-- and numbers; project_id has no foreign key so the spend outlives the project's purge.
CREATE TABLE public.model_usage_days (
  day date NOT NULL,
  user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  project_id uuid NOT NULL,
  source text NOT NULL CHECK (source IN ('chat', 'scan', 'cards')),
  model text NOT NULL CHECK (model = btrim(model) AND char_length(model) BETWEEN 1 AND 120),
  calls bigint NOT NULL DEFAULT 0 CHECK (calls >= 0),
  input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  cached_input_tokens bigint NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
  output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  reasoning_output_tokens bigint NOT NULL DEFAULT 0 CHECK (reasoning_output_tokens >= 0),
  PRIMARY KEY (day, user_id, project_id, source, model)
);
REVOKE ALL ON public.model_usage_days FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.model_usage_days TO canonkeeper_product_runtime;

-- UC8: the token totals of a chat's root thread already counted; NULL before the first counted
-- turn.
ALTER TABLE public.chat_sessions
  ADD COLUMN thread_input_tokens bigint CHECK (thread_input_tokens >= 0),
  ADD COLUMN thread_cached_input_tokens bigint CHECK (thread_cached_input_tokens >= 0),
  ADD COLUMN thread_output_tokens bigint CHECK (thread_output_tokens >= 0),
  ADD COLUMN thread_reasoning_output_tokens bigint CHECK (thread_reasoning_output_tokens >= 0);
REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;

-- UC9: readings of the provider's usage window, kept for 60 days.
CREATE TABLE public.model_limit_samples (
  observed_at timestamptz PRIMARY KEY,
  limit_id text CHECK (limit_id IS NULL OR char_length(limit_id) <= 120),
  window_minutes integer CHECK (window_minutes IS NULL OR window_minutes > 0),
  used_percent numeric(5,2) NOT NULL CHECK (used_percent >= 0),
  resets_at timestamptz
);
REVOKE ALL ON public.model_limit_samples FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.model_limit_samples TO canonkeeper_product_runtime;
