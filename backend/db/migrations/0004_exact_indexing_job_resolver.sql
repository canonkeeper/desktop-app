DO $ck_exact_indexing_job_preflight$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM public.jobs AS job
    JOIN public.import_queue_items AS item
      ON item.project_id = job.project_id
     AND item.book_id IS NOT NULL
     AND item.book_id::text = job.subject_json->>'bookId'
    JOIN public.import_queues AS queue
      ON queue.id = item.queue_id
     AND queue.project_id = item.project_id
     AND queue.created_by_user_id = job.creator_user_id
    JOIN public.books AS book
      ON book.project_id = item.project_id
     AND book.id = item.book_id
     AND book.deleted_at IS NULL
     AND book."order" = item.confirmed_book_order
    WHERE job.kind = 'canon_book_enrichment'
      AND job.subject_json->>'type' = 'canon_book_enrichment'
      AND job.subject_json->>'projectId' = job.project_id::text
      AND job.subject_json->>'sourceView' = 'draft'
      AND btrim(job.subject_json->>'bookManifest') <> ''
      AND job.subject_json->>'contractVersion' ~ '^[1-9][0-9]*$'
      AND job.subject_json->>'importQueueId' IS NULL
      AND job.subject_json->>'importQueueItemId' IS NULL
    GROUP BY job.project_id, job.id
    HAVING count(DISTINCT item.id) <> 1
  ) THEN
    RAISE EXCEPTION 'ambiguous queue-origin enrichment lineage'
      USING ERRCODE = '22000';
  END IF;
END;
$ck_exact_indexing_job_preflight$;

WITH exact_queue_job AS (
  SELECT
    job.id AS job_id,
    job.project_id AS job_project_id,
    min(item.queue_id::text) AS import_queue_id,
    min(item.id::text) AS import_queue_item_id
  FROM public.jobs AS job
  JOIN public.import_queue_items AS item
    ON item.project_id = job.project_id
   AND item.book_id IS NOT NULL
   AND item.book_id::text = job.subject_json->>'bookId'
  JOIN public.import_queues AS queue
    ON queue.id = item.queue_id
   AND queue.project_id = item.project_id
   AND queue.created_by_user_id = job.creator_user_id
  JOIN public.books AS book
    ON book.project_id = item.project_id
   AND book.id = item.book_id
   AND book.deleted_at IS NULL
   AND book."order" = item.confirmed_book_order
  WHERE job.kind = 'canon_book_enrichment'
    AND job.subject_json->>'type' = 'canon_book_enrichment'
    AND job.subject_json->>'projectId' = job.project_id::text
    AND job.subject_json->>'sourceView' = 'draft'
    AND btrim(job.subject_json->>'bookManifest') <> ''
    AND job.subject_json->>'contractVersion' ~ '^[1-9][0-9]*$'
    AND job.subject_json->>'importQueueId' IS NULL
    AND job.subject_json->>'importQueueItemId' IS NULL
  GROUP BY job.project_id, job.id
  HAVING count(DISTINCT item.id) = 1
)
UPDATE public.jobs AS job
SET subject_json = jsonb_set(
      jsonb_set(job.subject_json, '{importQueueId}', to_jsonb(exact.import_queue_id), true),
      '{importQueueItemId}', to_jsonb(exact.import_queue_item_id), true
    )
FROM exact_queue_job AS exact
WHERE job.project_id = exact.job_project_id
  AND job.id = exact.job_id;

CREATE FUNCTION public.ck_current_indexing_job(
  wanted_project_id uuid,
  wanted_source_view text,
  wanted_book_id uuid,
  wanted_book_manifest text,
  wanted_import_queue_id uuid DEFAULT NULL,
  wanted_import_queue_item_id uuid DEFAULT NULL
) RETURNS SETOF public.jobs
LANGUAGE sql STABLE PARALLEL SAFE
SET search_path TO 'pg_catalog', 'public'
AS $$
  SELECT job.*
  FROM public.jobs AS job
  WHERE (wanted_import_queue_id IS NULL) = (wanted_import_queue_item_id IS NULL)
    AND job.project_id = wanted_project_id
    AND job.kind = 'canon_book_enrichment'
    AND job.governance_payload_state = 'active'
    AND job.subject_json->>'type' = 'canon_book_enrichment'
    AND job.subject_json->>'projectId' = wanted_project_id::text
    AND job.subject_json->>'sourceView' = wanted_source_view
    AND job.subject_json->>'bookId' = wanted_book_id::text
    AND job.subject_json->>'bookManifest' = wanted_book_manifest
    AND (
      wanted_import_queue_id IS NULL
      OR (
        job.subject_json->>'importQueueId' = wanted_import_queue_id::text
        AND job.subject_json->>'importQueueItemId' = wanted_import_queue_item_id::text
      )
    )
    AND (
      wanted_import_queue_id IS NOT NULL
      OR (
        COALESCE(job.subject_json->>'importQueueId', '') = ''
        AND COALESCE(job.subject_json->>'importQueueItemId', '') = ''
      )
    )
    AND public.ck_artifact_source_owner_is_readable(
      job.artifact_source_owner_id, job.project_id, 'job', job.id
    )
  ORDER BY
    CASE WHEN job.status IN ('queued', 'running', 'canceling') THEN 0 ELSE 1 END,
    job.created_at DESC,
    job.id DESC
  LIMIT 1;
$$;

REVOKE ALL ON FUNCTION public.ck_current_indexing_job(uuid, text, uuid, text, uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_current_indexing_job(uuid, text, uuid, text, uuid, uuid)
  TO canonkeeper_product_runtime, canonkeeper_governance_owner, CURRENT_USER;
