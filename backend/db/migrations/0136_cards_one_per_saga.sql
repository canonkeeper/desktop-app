-- A chapter card is built once (specs/2026-11-11-cards-once.md).
--
-- CO1.1: the two cards workers of the site wake in the same tick and ask the queue at one moment. Each claim read the
-- queue as it was before the other's claim committed: the second worker skipped the Job the first had locked and took
-- the other cards Job of the same saga, because the rule «a saga runs one cards Job» (quota spec R12.4) saw no Job of
-- the saga in work yet. Both Jobs paid the model for the same chapters. Claims of chapter cards now take turns: the
-- second waits for the first to commit, and its statement reads the queue afresh. Everything else is as in 0114.
--
-- CO2.2: the cards Job of the published text goes after the draft text of its saga (the candidate rule at the end).

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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards', 'illustration', 'saga_layer', 'chapter_draft', 'author_materials_import', 'obsidian_export', 'saga_archive_export', 'saga_archive_import')
     ) THEN
    RETURN;
  END IF;

  -- CO1.1: a claim of chapter cards waits until the claim of another cards worker is committed, so the rule of one cards
  -- Job of a saga in work below reads the queue with that claim in it.
  IF 'chapter_cards' = ANY(wanted_kinds) THEN
    PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('canonkeeper chapter cards claim', 0));
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

-- R12.4 as in 0056: a saga runs one cards Job at a time, so the cards of a chapter are not built twice by the draft and
-- the published text or by a yielded Job and a new one; a Job of the saga in work without a heartbeat for 15 minutes
-- does not hold the others back.
-- CO2.2 (a): the cards Job of the published text waits while the draft text of the saga has a Job to index or to card,
-- queued or in work: the cards of the chapters both texts share are built by the Job of the draft text, from the input
-- the answers of the example saga were recorded for, and the Job of the published text finds them built. The cards
-- Jobs of the draft text never wait for the published one, so the order cannot lock.
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
      OR (
        NOT EXISTS (
          SELECT 1
          FROM public.jobs AS other
          WHERE other.project_id = job.project_id
            AND other.kind = 'chapter_cards'
            AND other.id <> job.id
            AND other.status IN ('running', 'canceling')
            AND other.updated_at >= pg_catalog.clock_timestamp() - interval '15 minutes'
        )
        AND (
          job.subject_json->>'sourceView' <> 'published'
          OR NOT EXISTS (
            SELECT 1
            FROM public.jobs AS draft
            WHERE draft.project_id = job.project_id
              AND draft.kind IN ('canon_text_index', 'chapter_cards')
              AND draft.subject_json->>'sourceView' = 'draft'
              AND draft.status IN ('queued', 'running', 'canceling')
              AND draft.governance_payload_state = 'active'
              AND public.ck_artifact_source_owner_is_readable(
                draft.artifact_source_owner_id, draft.project_id, 'job', draft.id
              )
          )
        )
      )
    )
$$;
