-- Two workers polling at once could claim the same Job twice: the second worker's candidate
-- set was computed before the first claim committed, and the row lock then re-checked only the
-- kind and owner of the claimed row. The fresh claim of a running Job looked like a stale
-- reclaim, fenced the first worker and spent a retry. The locked row now re-checks that it is
-- still claimable: queued and due, or running/canceling with a lapsed heartbeat. Enrichment
-- keeps its own usage-limit rules until it is removed.
CREATE OR REPLACE FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[])
RETURNS TABLE(project_id uuid, work_id uuid, kind text, state text, claim_incarnation bigint, exhausted_status text)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path TO 'pg_catalog'
AS $$
DECLARE
  selected_kind text;
  selected_project_id uuid;
  selected_source_view text;
  locked_enrichment_project_id uuid;
  locked_enrichment_source_view text;
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
       WHERE wanted.value NOT IN ('import', 'export', 'canon_text_index',
         'canon_book_enrichment', 'research_chat_turn', 'chapter_cards')
     ) THEN
    RETURN;
  END IF;

  WITH context_candidates AS MATERIALIZED (
    SELECT * FROM public.ck_import_job_claim_candidate(wanted_kinds)
    UNION ALL
    SELECT * FROM public.ck_canon_book_enrichment_job_claim_candidate(wanted_kinds)
    UNION ALL
    SELECT * FROM public.ck_simple_job_claim_candidate(wanted_kinds)
  )
  SELECT job.kind, job.project_id, job.subject_json->>'sourceView'
  INTO selected_kind, selected_project_id, selected_source_view
  FROM public.jobs AS job
  JOIN context_candidates AS candidate
    ON candidate.project_id = job.project_id
   AND candidate.id = job.id
   AND candidate.kind = job.kind
  ORDER BY
    CASE job.queue_class WHEN 'interactive' THEN 0 WHEN 'user_demanded' THEN 1 ELSE 2 END,
    job.next_attempt_at, job.created_at, job.id
  LIMIT 1;

  IF NOT FOUND THEN
    RETURN;
  END IF;

  IF selected_kind = 'canon_book_enrichment' THEN
    SELECT source.project_id, source.source_view
    INTO locked_enrichment_project_id, locked_enrichment_source_view
    FROM public.project_source_views AS source
    WHERE source.project_id = selected_project_id
      AND source.source_view = selected_source_view
    FOR UPDATE;
    IF NOT FOUND THEN
      RETURN;
    END IF;
  END IF;

  claim_time := pg_catalog.date_trunc('microseconds', pg_catalog.clock_timestamp());
  WITH context_candidates AS MATERIALIZED (
    SELECT * FROM public.ck_import_job_claim_candidate(wanted_kinds)
    UNION ALL
    SELECT * FROM public.ck_canon_book_enrichment_job_claim_candidate(wanted_kinds)
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
        job.kind = 'canon_book_enrichment'
        OR (job.status = 'queued' AND job.next_attempt_at <= claim_time)
        OR (
          job.status IN ('running', 'canceling')
          AND job.updated_at < claim_time - interval '15 minutes'
        )
      )
      AND (
        selected_kind <> 'canon_book_enrichment'
        OR (
          job.project_id = locked_enrichment_project_id
          AND job.subject_json->>'sourceView' = locked_enrichment_source_view
        )
      )
    ORDER BY
      CASE job.queue_class WHEN 'interactive' THEN 0 WHEN 'user_demanded' THEN 1 ELSE 2 END,
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

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[]) TO canonkeeper_worker_discovery;
