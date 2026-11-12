-- One way from a file to a book (specs/2026-11-11-ux7-upload.md).
--
-- UP3.2: a volume of a queue that did not read no longer stops its queue. It waits for the author at its place —
-- «Заменить файл» gives it a new file and a new import Job, «Пропустить» takes it out of the upload — and the volumes
-- after it go on by themselves. The queue still goes by itself: a volume starts once the volumes before it are imported
-- and their books are ready, or wait for the author, or were skipped; no volume is confirmed by hand.

-- A queue stopped on a volume by the earlier rule stays history as it is: its volumes after the failed one were canceled
-- with it, and since 0022 it holds no place of the saga.

ALTER TABLE public.import_queue_items ADD COLUMN skipped_at timestamptz;

-- UP2.3: the order of the volumes is the order of the rows; the number found in a file name is no second order.
ALTER TABLE public.import_queue_items DROP COLUMN detected_book_order;

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
    AND job.status IN ('queued', 'running', 'canceling')
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
          AND current_queue_item.skipped_at IS NULL
          AND current_queue.status = 'running'
          -- A volume before it holds it back while it is in work or its book is read; one that did not read or whose
          -- file did not pass the check waits for the author, and neither it nor a skipped one holds anything back.
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
              AND predecessor_queue_item.skipped_at IS NULL
              AND (
                (predecessor_job.status IS NULL AND predecessor_queue_item.preflight_status <> 'invalid')
                OR predecessor_job.status IN ('queued', 'running', 'canceling')
                OR (predecessor_job.status = 'succeeded'
                    AND predecessor_book.id IS NOT NULL AND predecessor_book.status <> 'ready')
              )
          )
      )
    )
$$;

REVOKE ALL ON TABLE public.import_queue_items FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_import_job_claim_candidate(text[]) FROM PUBLIC;
