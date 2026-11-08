-- Let workers reclaim stale native enrichment claims after a failed attempt.

CREATE OR REPLACE FUNCTION public.ck_canon_book_enrichment_job_claim_candidate(wanted_kinds text[])
RETURNS TABLE(project_id uuid, id uuid, kind text, source_view text, exhausted_status text)
LANGUAGE sql SECURITY DEFINER
SET search_path TO 'pg_catalog', 'public'
AS $$
  SELECT job.project_id, job.id, job.kind, job.subject_json->>'sourceView',
    CASE
      WHEN job.status = 'canceling' AND job.retry_count >= 2 THEN 'canceled'
      ELSE NULL
    END AS exhausted_status
  FROM public.jobs AS job
  JOIN public.books AS book
    ON book.id = NULLIF(job.subject_json->>'bookId', '')::uuid
   AND book.project_id = job.project_id
   AND book.deleted_at IS NULL
  WHERE 'canon_book_enrichment' = ANY(wanted_kinds)
    AND job.kind = 'canon_book_enrichment'
    AND job.governance_payload_state = 'active'
    AND public.ck_artifact_source_owner_is_readable(
      job.artifact_source_owner_id, job.project_id, 'job', job.id
    )
    AND (
      (
        job.status = 'queued'
        AND job.next_attempt_at <= pg_catalog.clock_timestamp()
        AND NOT EXISTS (
          SELECT 1 FROM public.jobs AS active
          WHERE active.project_id = job.project_id
            AND active.kind = 'canon_book_enrichment'
            AND active.subject_json->>'sourceView' = job.subject_json->>'sourceView'
            AND active.status IN ('running', 'canceling')
            AND public.ck_artifact_source_owner_is_readable(
              active.artifact_source_owner_id, active.project_id, 'job', active.id
            )
        )
        AND NOT EXISTS (
          SELECT 1
          FROM public.books AS predecessor_book
          JOIN public.book_source_members AS predecessor_source
            ON predecessor_source.project_id = predecessor_book.project_id
           AND predecessor_source.book_id = predecessor_book.id
           AND predecessor_source.book_lifecycle_generation = predecessor_book.lifecycle_generation
           AND predecessor_source.source_view = job.subject_json->>'sourceView'
          WHERE predecessor_book.project_id = job.project_id
            AND predecessor_book.deleted_at IS NULL
            AND (predecessor_book."order", predecessor_book.id) < (book."order", book.id)
            AND NOT EXISTS (
              SELECT 1
              FROM public.jobs AS predecessor
              WHERE predecessor.project_id = job.project_id
                AND predecessor.kind = 'canon_book_enrichment'
                AND predecessor.subject_json->>'sourceView' = job.subject_json->>'sourceView'
                AND predecessor.subject_json->>'bookId' = predecessor_book.id::text
                AND predecessor.status = 'succeeded'
                AND predecessor.result_json->>'chapterSummariesComplete' = 'true'
                AND predecessor.result_json->>'graphActivated' = 'true'
                AND predecessor.governance_payload_state = 'active'
                AND public.ck_artifact_source_owner_is_readable(
                  predecessor.artifact_source_owner_id,
                  predecessor.project_id,
                  'job',
                  predecessor.id
                )
                AND NOT EXISTS (
                  SELECT 1
                  FROM public.jobs AS newer
                  WHERE newer.project_id = predecessor.project_id
                    AND newer.kind = 'canon_book_enrichment'
                    AND newer.subject_json->>'sourceView' = predecessor.subject_json->>'sourceView'
                    AND newer.subject_json->>'bookId' = predecessor.subject_json->>'bookId'
                    AND newer.governance_payload_state = 'active'
                    AND public.ck_artifact_source_owner_is_readable(
                      newer.artifact_source_owner_id, newer.project_id, 'job', newer.id
                    )
                    AND (newer.created_at, newer.id) > (predecessor.created_at, predecessor.id)
                )
            )
        )
      )
      OR (
        job.status = 'canceling'
        AND job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
      )
    )
$$;

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
         'canon_book_enrichment', 'research_chat_turn', 'writer_agent', 'canon_correction')
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
        (job.status = 'queued' AND job.next_attempt_at <= pg_catalog.clock_timestamp())
        OR (
          (
            job.kind IN ('import', 'export', 'canon_text_index')
            AND job.status IN ('running', 'canceling')
          )
          OR (job.kind = 'canon_book_enrichment' AND job.status = 'canceling')
        )
        AND job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
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
