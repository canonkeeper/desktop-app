DO $ck_import_queue_audited_progression_preflight$
BEGIN
  PERFORM 1
  FROM public.import_queues AS queue
  JOIN public.import_queue_items AS item
    ON item.queue_id = queue.id
   AND item.project_id = queue.project_id
  WHERE queue.status = 'running'
  FOR UPDATE OF queue, item;

  PERFORM 1
  FROM public.jobs AS job
  WHERE job.kind = 'canon_book_enrichment'
    AND job.subject_json->>'importQueueId' IS NOT NULL
  FOR UPDATE;

  IF EXISTS (
    SELECT 1
    FROM public.import_queue_items AS item
    JOIN public.import_queues AS queue
      ON queue.id = item.queue_id
     AND queue.project_id = item.project_id
    WHERE queue.status = 'running'
      AND (
        item.import_job_id IS NULL
        OR (
          item.book_id IS NOT NULL AND EXISTS (
          SELECT 1
          FROM public.jobs AS job
          WHERE job.project_id = item.project_id
            AND job.kind = 'canon_book_enrichment'
            AND job.subject_json->>'importQueueId' = item.queue_id::text
            AND job.subject_json->>'importQueueItemId' = item.id::text
          HAVING count(*) <> 1
          )
        )
      )
  ) THEN
    RAISE EXCEPTION 'ambiguous import queue lineage'
      USING ERRCODE = '22000';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM public.jobs AS job
    WHERE job.kind = 'canon_book_enrichment'
      AND job.subject_json->>'importQueueId' IS NOT NULL
      AND NOT EXISTS (
        SELECT 1
        FROM public.import_queue_items AS item
        JOIN public.import_queues AS queue
          ON queue.id = item.queue_id
         AND queue.project_id = item.project_id
        WHERE item.project_id = job.project_id
          AND item.queue_id::text = job.subject_json->>'importQueueId'
          AND item.id::text = job.subject_json->>'importQueueItemId'
      )
  ) THEN
    RAISE EXCEPTION 'non-queue enrichment lineage'
      USING ERRCODE = '22000';
  END IF;
END;
$ck_import_queue_audited_progression_preflight$;

ALTER TABLE public.import_queues
  ADD COLUMN progression_mode text NOT NULL DEFAULT 'automatic'
    CHECK (progression_mode IN ('automatic', 'audited'));

ALTER TABLE public.import_queue_items
  ADD COLUMN audit_state text NOT NULL DEFAULT 'not_required'
    CHECK (audit_state IN ('not_required', 'pending', 'awaiting_audit', 'approved', 'failed'));

ALTER TABLE public.import_queue_items
  ADD CONSTRAINT import_queue_items_audit_identity_key
    UNIQUE (id, queue_id, project_id);

CREATE TABLE public.import_queue_audit_verdicts (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
    queue_id uuid NOT NULL,
    item_id uuid NOT NULL,
    actor_user_id uuid NOT NULL REFERENCES public.users(id),
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    verdict text NOT NULL CHECK (verdict IN ('accept', 'reject')),
    expected_version bigint NOT NULL CHECK (expected_version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (project_id, queue_id, item_id, idempotency_key),
    FOREIGN KEY (queue_id, project_id) REFERENCES public.import_queues(id, project_id) ON DELETE CASCADE,
    FOREIGN KEY (item_id, queue_id, project_id)
      REFERENCES public.import_queue_items(id, queue_id, project_id) ON DELETE CASCADE
);

UPDATE public.import_queues AS queue
SET progression_mode = 'automatic'
WHERE progression_mode IS NULL;

UPDATE public.import_queue_items AS item
SET audit_state = 'not_required'
WHERE audit_state IS NULL;

WITH terminal_clock AS (
  SELECT date_trunc('microseconds', clock_timestamp()) AS value
)
UPDATE public.import_queues AS queue
SET status = 'succeeded', version = version + 1,
    updated_at = terminal_clock.value, completed_at = terminal_clock.value
FROM terminal_clock
WHERE queue.status = 'running'
  AND NOT EXISTS (
    SELECT 1
    FROM public.import_queue_items AS item
    JOIN public.jobs AS import_job
      ON import_job.id = item.import_job_id
     AND import_job.project_id = item.project_id
    LEFT JOIN public.books AS book
      ON book.id = item.book_id
     AND book.project_id = item.project_id
    LEFT JOIN public.jobs AS enrichment
      ON enrichment.project_id = item.project_id
       AND enrichment.kind = 'canon_book_enrichment'
       AND enrichment.subject_json->>'importQueueId' = item.queue_id::text
       AND enrichment.subject_json->>'importQueueItemId' = item.id::text
    WHERE item.queue_id = queue.id
      AND item.project_id = queue.project_id
      AND (
        import_job.status IS DISTINCT FROM 'succeeded'
        OR book.id IS NULL OR book.status IS DISTINCT FROM 'ready'
        OR enrichment.status IS DISTINCT FROM 'succeeded'
      )
  );

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
                CASE WHEN current_queue.progression_mode = 'audited' THEN
                  predecessor_queue_item.audit_state IS DISTINCT FROM 'approved'
                ELSE
                  predecessor_job.status IS DISTINCT FROM 'succeeded'
                  OR predecessor_book.status IS DISTINCT FROM 'ready'
                END
              )
          )
      )
    )
$$;

REVOKE ALL ON TABLE public.import_queues FROM PUBLIC;
REVOKE ALL ON TABLE public.import_queue_items FROM PUBLIC;
REVOKE ALL ON TABLE public.import_queue_audit_verdicts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.import_queues TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.import_queue_items TO canonkeeper_product_runtime;
GRANT SELECT, INSERT ON TABLE public.import_queue_audit_verdicts TO canonkeeper_product_runtime;
