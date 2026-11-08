-- Park quota-limited native enrichment until an actor explicitly requests resume.

CREATE OR REPLACE FUNCTION public.ck_canon_book_enrichment_job_claim_candidate(wanted_kinds text[])
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
        AND (
          job.error_json IS NULL
          OR job.error_json->>'code' <> 'model.usage_limit_exceeded'
        )
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
        job.status IN ('running', 'canceling')
        AND (
          (job.status = 'canceling' AND job.error_json->>'code' IN (
            'model.usage_limit_exceeded',
            'model.usage_limit_resume_requested'
          ))
          OR job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
        )
      )
    )
$$;

REVOKE ALL ON FUNCTION public.ck_canon_book_enrichment_job_claim_candidate(text[]) FROM PUBLIC;
