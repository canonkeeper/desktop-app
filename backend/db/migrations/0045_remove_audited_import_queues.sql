-- Import Queues always progress automatically (specs/2026-10-09-saga-memory.md, R6.4): the
-- next volume starts after the previous import succeeded and its book is ready. The legacy
-- audited mode, its per-item audit state and verdicts leave the product. A running audited
-- queue continues as an automatic one, which is what its single legacy accept did.

REVOKE ALL ON TABLE public.import_queue_audit_verdicts FROM PUBLIC;
DROP TABLE public.import_queue_audit_verdicts;

CREATE OR REPLACE FUNCTION public.ck_import_job_claim_candidate(wanted_kinds text[])
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
  WHERE 'import' = ANY(wanted_kinds)
    AND job.kind = 'import'
    AND job.governance_payload_state = 'active'
    AND public.ck_artifact_source_owner_is_readable(
      job.artifact_source_owner_id, job.project_id, 'job', job.id
    )
    AND (
      (job.status = 'queued' AND job.next_attempt_at <= pg_catalog.clock_timestamp())
      OR (
        job.status IN ('running', 'canceling')
        AND job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
      )
    )
    AND (
      NOT EXISTS (
        SELECT 1
        FROM public.import_queue_items AS current_queue_item
        WHERE current_queue_item.project_id = job.project_id
          AND current_queue_item.import_job_id = job.id
      )
      OR EXISTS (
        SELECT 1
        FROM public.import_queue_items AS current_queue_item
        JOIN public.import_queues AS current_queue
          ON current_queue.id = current_queue_item.queue_id
         AND current_queue.project_id = current_queue_item.project_id
        WHERE current_queue_item.project_id = job.project_id
          AND current_queue_item.import_job_id = job.id
          AND current_queue.status = 'running'
          AND NOT EXISTS (
            SELECT 1
            FROM public.import_queue_items AS predecessor_queue_item
            LEFT JOIN public.jobs AS predecessor_job
              ON predecessor_job.id = predecessor_queue_item.import_job_id
             AND predecessor_job.project_id = predecessor_queue_item.project_id
            LEFT JOIN public.books AS predecessor_book
              ON predecessor_book.id = predecessor_queue_item.book_id
             AND predecessor_book.project_id = predecessor_queue_item.project_id
             AND predecessor_book.deleted_at IS NULL
            WHERE predecessor_queue_item.queue_id = current_queue_item.queue_id
              AND predecessor_queue_item.project_id = current_queue_item.project_id
              AND predecessor_queue_item.position < current_queue_item.position
              AND (
                predecessor_job.status IS DISTINCT FROM 'succeeded'
                OR (predecessor_book.id IS NOT NULL AND predecessor_book.status <> 'ready')
              )
          )
      )
    )
$$;

ALTER TABLE public.import_queue_items DROP CONSTRAINT import_queue_items_audit_identity_key;
ALTER TABLE public.import_queue_items DROP COLUMN audit_state;
ALTER TABLE public.import_queues DROP COLUMN progression_mode;
