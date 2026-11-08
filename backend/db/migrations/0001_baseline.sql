-- Постоянные роли чистой установки. Пользователей с LOGIN и привязку ролей
-- создаёт bootstrap конкретного окружения, а не эта baseline-схема.
DO $canonkeeper_roles$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_roles
    WHERE rolname = 'canonkeeper_governance_owner'
  ) THEN
    EXECUTE 'CREATE ROLE canonkeeper_governance_owner NOLOGIN';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_roles
    WHERE rolname = 'canonkeeper_worker_discovery'
  ) THEN
    EXECUTE 'CREATE ROLE canonkeeper_worker_discovery NOLOGIN';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_roles
    WHERE rolname = 'canonkeeper_product_runtime'
  ) THEN
    EXECUTE 'CREATE ROLE canonkeeper_product_runtime NOLOGIN';
  END IF;
END;
$canonkeeper_roles$;

--
-- PostgreSQL database dump
--

-- Dumped from database version 18.4 (Debian 18.4-1.pgdg13+1)
-- Dumped by pg_dump version 18.4 (Debian 18.4-1.pgdg13+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: canonkeeper_worker; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA canonkeeper_worker;


--
-- Name: claim_next_deletion_audit_gc_for_worker(); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_deletion_audit_gc_for_worker() RETURNS TABLE(project_id uuid, tombstone_id uuid)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT
    tombstone.project_id,
    tombstone.id
  FROM public.deletion_tombstones AS tombstone
  JOIN public.deletion_completion_receipts AS receipt
    ON receipt.tombstone_id = tombstone.id
   AND receipt.project_id = tombstone.project_id
  WHERE tombstone.status = 'purged'
    AND tombstone.audit_expires_at IS NOT NULL
    AND tombstone.audit_expires_at <= pg_catalog.clock_timestamp()
    AND receipt.audit_expires_at = tombstone.audit_expires_at
    AND NOT EXISTS (
      SELECT 1
      FROM public.deletion_hard_purge_predecessors AS predecessor
      WHERE predecessor.predecessor_tombstone_id = tombstone.id
        AND predecessor.project_id = tombstone.project_id
    )
  ORDER BY
    CASE tombstone.purge_mode WHEN 'source_hard_purge' THEN 1 WHEN 'chat_epoch' THEN 2 ELSE 3 END,
    tombstone.audit_expires_at, tombstone.id, tombstone.project_id
  LIMIT 1
  FOR UPDATE OF tombstone SKIP LOCKED
$$;


--
-- Name: claim_next_deletion_target_task_for_worker(bytea, integer, boolean); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_deletion_target_task_for_worker(worker_digest bytea, lease_seconds integer, runtime_target_enabled boolean) RETURNS TABLE(project_id uuid, tombstone_id uuid, target text, state text, attempt integer, claim_incarnation bigint, lease_owner_digest bytea, lease_expires_at timestamp with time zone)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
  WITH candidate AS (
    SELECT task.tombstone_id, task.project_id, task.target
    FROM public.deletion_target_tasks AS task
    JOIN public.deletion_tombstones AS tombstone
      ON tombstone.id = task.tombstone_id
     AND tombstone.project_id = task.project_id
    WHERE (
        (task.state IN ('pending', 'failed')
          AND task.next_attempt_at <= pg_catalog.clock_timestamp())
        OR
        (task.state = 'processing'
          AND task.claim_expires_at <= pg_catalog.clock_timestamp())
      )
      AND tombstone.origin = 'governed_online'
      AND pg_catalog.octet_length(worker_digest) = 32
      AND worker_digest <> pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex')
      AND lease_seconds BETWEEN 30 AND 300
      AND (task.target <> 'runtime' OR runtime_target_enabled)
      AND (
        task.target <> 'postgresql'
        OR NOT EXISTS (
          SELECT 1
          FROM public.deletion_target_tasks AS sibling
          WHERE sibling.tombstone_id = task.tombstone_id
            AND sibling.project_id = task.project_id
            AND sibling.target <> 'postgresql'
            AND sibling.state NOT IN ('succeeded', 'no_data')
        )
      )
    ORDER BY
      CASE WHEN task.state = 'processing' THEN task.claim_expires_at ELSE task.next_attempt_at END,
      task.tombstone_id, task.project_id, task.target
    LIMIT 1
    FOR UPDATE SKIP LOCKED
  ), claimed AS (
    UPDATE public.deletion_target_tasks AS task
    SET
      state = 'processing',
      attempts = task.attempts + 1,
      claim_incarnation = task.claim_incarnation + 1,
      claimed_by_digest = worker_digest,
      claim_expires_at = pg_catalog.clock_timestamp()
        + pg_catalog.make_interval(secs => lease_seconds),
      handler_version = NULL,
      receipt_digest = NULL,
      completed_at = NULL,
      last_reason_code = NULL
    FROM candidate
    WHERE task.tombstone_id = candidate.tombstone_id
      AND task.project_id = candidate.project_id
      AND task.target = candidate.target
    RETURNING task.*
  ), aggregate AS (
    UPDATE public.deletion_tombstones AS tombstone
    SET status = 'processing', last_reason_code = NULL, updated_at = pg_catalog.clock_timestamp()
    FROM claimed
    WHERE tombstone.id = claimed.tombstone_id
      AND tombstone.project_id = claimed.project_id
      AND tombstone.status IN ('pending', 'processing', 'failed')
    RETURNING tombstone.id
  )
  SELECT
    claimed.project_id,
    claimed.tombstone_id,
    claimed.target,
    claimed.state,
    claimed.attempts,
    claimed.claim_incarnation,
    claimed.claimed_by_digest,
    claimed.claim_expires_at
  FROM claimed
  WHERE EXISTS (SELECT 1 FROM aggregate)
$$;


--
-- Name: claim_next_export_intent_for_worker(); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_export_intent_for_worker() RETURNS TABLE(project_id uuid, work_id uuid, kind text, state text)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT
    artifact.project_id,
    artifact.id,
    COALESCE(artifact.delete_reason, 'expiry'),
    artifact.object_state
  FROM public.export_artifacts AS artifact
  WHERE artifact.artifact_source_owner_id IS NOT NULL
    AND public.ck_artifact_source_owner_is_readable(
      artifact.artifact_source_owner_id,
      artifact.project_id,
      'export_artifact',
      artifact.id
    )
    AND (
      artifact.object_state = 'deleting'
      OR (
        artifact.object_state = 'active'
        AND artifact.expires_at <= pg_catalog.clock_timestamp()
      )
      OR (
        artifact.object_state = 'creating'
        AND artifact.create_started_at <= pg_catalog.clock_timestamp() - interval '15 minutes'
      )
    )
  ORDER BY artifact.updated_at, artifact.id, artifact.project_id
  LIMIT 1
  FOR UPDATE OF artifact SKIP LOCKED
$$;


--
-- Name: claim_next_import_expiry_for_worker(); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_import_expiry_for_worker() RETURNS TABLE(project_id uuid, job_id uuid, claim_incarnation bigint)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
  WITH db_clock AS (
    SELECT pg_catalog.date_trunc('microseconds', pg_catalog.clock_timestamp()) AS now_at
  ), candidate AS (
    SELECT job.id, job.project_id
    FROM public.import_uploads AS upload
    JOIN public.import_job_requests AS request
      ON request.storage_key = upload.storage_key
     AND request.project_id = upload.project_id
    JOIN public.jobs AS job
      ON job.id = request.job_id
     AND job.project_id = request.project_id
    JOIN public.projects AS project
      ON project.id = job.project_id
     AND project.deleted_at IS NULL
     AND project.lifecycle_generation >= 1
    WHERE upload.content IS NOT NULL
      AND upload.expires_at <= pg_catalog.clock_timestamp()
      AND request.status = 'pending'
      AND job.kind = 'import'
      AND job.artifact_source_owner_id IS NOT NULL
      AND request.artifact_source_owner_id IS NOT NULL
      AND upload.artifact_source_owner_id IS NOT NULL
      AND public.ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id, job.project_id, 'job', job.id
      )
      AND public.ck_artifact_source_owner_is_readable(
        request.artifact_source_owner_id, request.project_id,
        'import_job_request', request.job_id
      )
      AND public.ck_artifact_source_owner_is_readable(
        upload.artifact_source_owner_id, upload.project_id,
        'import_upload', upload.artifact_id
      )
      AND (
        job.status = 'queued'
        OR (
          job.status IN ('running', 'canceling')
          AND job.updated_at <= pg_catalog.clock_timestamp() - interval '15 minutes'
        )
      )
    ORDER BY upload.expires_at, job.id, job.project_id
    LIMIT 1
    FOR UPDATE OF job SKIP LOCKED
  ), adopted AS (
    UPDATE public.jobs AS job
    SET status = 'running',
        claim_incarnation = COALESCE(job.claim_incarnation, 0) + 1,
        claimed_at = db_clock.now_at,
        started_at = COALESCE(job.started_at, db_clock.now_at),
        updated_at = db_clock.now_at
    FROM candidate, db_clock
    WHERE job.id = candidate.id AND job.project_id = candidate.project_id
    RETURNING job.project_id, job.id, job.claim_incarnation
  )
  SELECT adopted.project_id, adopted.id, adopted.claim_incarnation
  FROM adopted
$$;


--
-- Name: claim_next_queued_job_for_worker(text[]); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[]) RETURNS TABLE(project_id uuid, work_id uuid, kind text, state text, claim_incarnation bigint, exhausted_status text)
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

  SELECT job.kind, job.project_id, job.subject_json->>'sourceView'
  INTO selected_kind, selected_project_id, selected_source_view
  FROM public.jobs AS job
  LEFT JOIN public.books AS book
    ON job.kind = 'canon_book_enrichment'
    AND book.id = NULLIF(job.subject_json->>'bookId', '')::uuid
    AND book.project_id = job.project_id
    AND book.deleted_at IS NULL
  WHERE job.kind = ANY(wanted_kinds)
    AND job.governance_payload_state = 'active'
    AND public.ck_artifact_source_owner_is_readable(
      job.artifact_source_owner_id, job.project_id, 'job', job.id
    )
    AND (
      (job.status = 'queued' AND job.next_attempt_at <= pg_catalog.clock_timestamp())
      OR (
        job.kind IN ('import', 'export', 'canon_text_index')
        AND job.status IN ('running', 'canceling')
        AND job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
      )
    )
    AND (
      job.kind <> 'import'
      OR NOT EXISTS (
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
                OR predecessor_book.status IS DISTINCT FROM 'ready'
              )
          )
      )
    )
    AND (
      job.kind <> 'canon_book_enrichment'
      OR (
        book.id IS NOT NULL
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
                      newer.artifact_source_owner_id,
                      newer.project_id,
                      'job',
                      newer.id
                    )
                    AND (newer.created_at, newer.id) > (predecessor.created_at, predecessor.id)
                )
            )
        )
      )
    )
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
  WITH candidate AS (
    SELECT job.id, job.project_id,
      CASE
        WHEN job.status = 'canceling' AND job.retry_count >= 2 THEN 'canceled'
        WHEN job.status = 'running' AND job.retry_count >= 2 THEN 'failed'
        ELSE NULL
      END AS exhausted_status
    FROM public.jobs AS job
    LEFT JOIN public.books AS book
      ON job.kind = 'canon_book_enrichment'
      AND book.id = NULLIF(job.subject_json->>'bookId', '')::uuid
      AND book.project_id = job.project_id
      AND book.deleted_at IS NULL
    WHERE job.kind = ANY(wanted_kinds)
      AND job.governance_payload_state = 'active'
      AND public.ck_artifact_source_owner_is_readable(
        job.artifact_source_owner_id, job.project_id, 'job', job.id
      )
      AND (
        (job.status = 'queued' AND job.next_attempt_at <= pg_catalog.clock_timestamp())
        OR (
          job.kind IN ('import', 'export', 'canon_text_index')
          AND job.status IN ('running', 'canceling')
          AND job.updated_at < pg_catalog.clock_timestamp() - interval '15 minutes'
        )
      )
      AND (
        job.kind <> 'import'
        OR NOT EXISTS (
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
                  OR predecessor_book.status IS DISTINCT FROM 'ready'
                )
            )
        )
      )
      AND (
        job.kind <> 'canon_book_enrichment'
        OR (
          job.project_id = locked_enrichment_project_id
          AND job.subject_json->>'sourceView' = locked_enrichment_source_view
          AND book.id IS NOT NULL
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
            FROM public.jobs AS predecessor
            JOIN public.books AS predecessor_book
              ON predecessor_book.id = NULLIF(predecessor.subject_json->>'bookId', '')::uuid
              AND predecessor_book.project_id = predecessor.project_id
              AND predecessor_book.deleted_at IS NULL
            WHERE predecessor.project_id = job.project_id
              AND predecessor.kind = 'canon_book_enrichment'
              AND predecessor.subject_json->>'sourceView' = job.subject_json->>'sourceView'
              AND predecessor.status IN ('queued', 'running', 'canceling')
              AND (predecessor_book."order", predecessor_book.id) < (book."order", book.id)
          )
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

  IF claimed_kind = 'canon_book_enrichment' THEN
    DELETE FROM public.canon_book_enrichment_steps AS step
    WHERE step.project_id = claimed_project_id
      AND step.job_id = claimed_work_id;
    INSERT INTO public.canon_book_enrichment_steps (
      project_id, job_id, step, started_at
    ) VALUES (
      claimed_project_id, claimed_work_id, 'enrichment', claim_time
    );
  END IF;

  RETURN QUERY SELECT claimed_project_id, claimed_work_id, claimed_kind,
    claimed_state, claimed_incarnation, claimed_exhausted_status;
END;
$$;


--
-- Name: claim_next_source_hard_purge_for_worker(); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_source_hard_purge_for_worker() RETURNS TABLE(project_id uuid, entity_kind text, entity_id uuid)
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
  RETURN QUERY
  SELECT project.id, 'project'::text, project.id
  FROM public.projects AS project
  WHERE project.deleted_at IS NOT NULL
    AND project.governance_retention_started_at IS NOT NULL
    AND project.purge_after <= pg_catalog.clock_timestamp()
    AND project.purge_deadline = project.purge_after + interval '24 hours'
    AND project.hard_purge_started_at IS NULL
    AND EXISTS (
      SELECT 1
      FROM public.deletion_tombstones AS derived
      JOIN public.deletion_completion_receipts AS receipt
        ON receipt.tombstone_id = derived.id
       AND receipt.project_id = derived.project_id
      WHERE derived.project_id = project.id
        AND derived.purge_mode = 'derived_generation'
        AND derived.status = 'purged'
    )
    AND NOT EXISTS (
      SELECT 1
      FROM public.deletion_tombstones AS derived
      LEFT JOIN public.deletion_completion_receipts AS receipt
        ON receipt.tombstone_id = derived.id
       AND receipt.project_id = derived.project_id
      WHERE derived.project_id = project.id
        AND derived.purge_mode = 'derived_generation'
        AND (derived.status <> 'purged' OR receipt.tombstone_id IS NULL)
    )
    AND NOT EXISTS (
      SELECT 1 FROM public.deletion_tombstones AS hard
      WHERE hard.project_id = project.id
        AND hard.entity_kind = 'project'
        AND hard.purge_mode = 'source_hard_purge'
        AND hard.project_generation = project.lifecycle_generation
    )
  ORDER BY project.purge_after, project.id
  LIMIT 1
  FOR UPDATE OF project SKIP LOCKED;
  IF FOUND THEN RETURN; END IF;

  RETURN QUERY
  SELECT book.project_id, 'book'::text, book.id
  FROM public.books AS book
  JOIN public.projects AS project ON project.id = book.project_id
  WHERE project.deleted_at IS NULL
    AND book.deleted_at IS NOT NULL
    AND book.governance_retention_started_at IS NOT NULL
    AND book.purge_after <= pg_catalog.clock_timestamp()
    AND book.purge_deadline = book.purge_after + interval '24 hours'
    AND book.hard_purge_started_at IS NULL
    AND EXISTS (
      SELECT 1
      FROM public.deletion_tombstones AS derived
      JOIN public.deletion_completion_receipts AS receipt
        ON receipt.tombstone_id = derived.id
       AND receipt.project_id = derived.project_id
      WHERE derived.project_id = book.project_id
        AND derived.purge_mode = 'derived_generation'
        AND derived.entity_kind IN ('book', 'chapter')
        AND derived.book_id = book.id
        AND derived.status = 'purged'
    )
    AND NOT EXISTS (
      SELECT 1
      FROM public.deletion_tombstones AS derived
      LEFT JOIN public.deletion_completion_receipts AS receipt
        ON receipt.tombstone_id = derived.id
       AND receipt.project_id = derived.project_id
      WHERE derived.project_id = book.project_id
        AND derived.purge_mode = 'derived_generation'
        AND derived.entity_kind IN ('book', 'chapter')
        AND derived.book_id = book.id
        AND (derived.status <> 'purged' OR receipt.tombstone_id IS NULL)
    )
    AND NOT EXISTS (
      SELECT 1 FROM public.deletion_tombstones AS hard
      WHERE hard.project_id = book.project_id
        AND hard.entity_kind = 'book'
        AND hard.purge_mode = 'source_hard_purge'
        AND hard.project_generation = project.lifecycle_generation
        AND hard.book_id = book.id
        AND hard.book_generation = book.lifecycle_generation
    )
  ORDER BY book.purge_after, book.id, book.project_id
  LIMIT 1
  FOR UPDATE OF book SKIP LOCKED;
  IF FOUND THEN RETURN; END IF;

  RETURN QUERY
  SELECT chapter.project_id, 'chapter'::text, chapter.id
  FROM public.chapters AS chapter
  JOIN public.books AS book
    ON book.id = chapter.book_id AND book.project_id = chapter.project_id
  JOIN public.projects AS project ON project.id = chapter.project_id
  WHERE project.deleted_at IS NULL
    AND book.deleted_at IS NULL
    AND chapter.deleted_at IS NOT NULL
    AND chapter.governance_retention_started_at IS NOT NULL
    AND chapter.purge_after <= pg_catalog.clock_timestamp()
    AND chapter.purge_deadline = chapter.purge_after + interval '24 hours'
    AND chapter.hard_purge_started_at IS NULL
    AND EXISTS (
      SELECT 1
      FROM public.deletion_tombstones AS derived
      JOIN public.deletion_completion_receipts AS receipt
        ON receipt.tombstone_id = derived.id
       AND receipt.project_id = derived.project_id
      WHERE derived.project_id = chapter.project_id
        AND derived.purge_mode = 'derived_generation'
        AND derived.entity_kind = 'chapter'
        AND derived.chapter_id = chapter.id
        AND derived.status = 'purged'
    )
    AND NOT EXISTS (
      SELECT 1
      FROM public.deletion_tombstones AS derived
      LEFT JOIN public.deletion_completion_receipts AS receipt
        ON receipt.tombstone_id = derived.id
       AND receipt.project_id = derived.project_id
      WHERE derived.project_id = chapter.project_id
        AND derived.purge_mode = 'derived_generation'
        AND derived.entity_kind = 'chapter'
        AND derived.chapter_id = chapter.id
        AND (derived.status <> 'purged' OR receipt.tombstone_id IS NULL)
    )
    AND NOT EXISTS (
      SELECT 1 FROM public.deletion_tombstones AS hard
      WHERE hard.project_id = chapter.project_id
        AND hard.entity_kind = 'chapter'
        AND hard.purge_mode = 'source_hard_purge'
        AND hard.project_generation = project.lifecycle_generation
        AND hard.book_id = chapter.book_id
        AND hard.book_generation = book.lifecycle_generation
        AND hard.chapter_id = chapter.id
        AND hard.chapter_generation = chapter.lifecycle_generation
    )
  ORDER BY chapter.purge_after, chapter.id, chapter.project_id
  LIMIT 1
  FOR UPDATE OF chapter SKIP LOCKED;
END;
$$;


--
-- Name: claim_next_tenant_retention_for_worker(); Type: FUNCTION; Schema: canonkeeper_worker; Owner: -
--

CREATE FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() RETURNS TABLE(project_id uuid, arm text, reader_annotation_id uuid, audit_id uuid)
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
  RETURN QUERY
  SELECT annotation.project_id, 'soft_deleted_reader_annotation'::text,
         annotation.id, NULL::uuid
  FROM public.reader_annotations AS annotation
  WHERE annotation.deleted_at IS NOT NULL
    AND annotation.purge_after <= pg_catalog.clock_timestamp()
  ORDER BY annotation.purge_after, annotation.id, annotation.project_id
  LIMIT 1
  FOR UPDATE OF annotation SKIP LOCKED;
  IF FOUND THEN RETURN; END IF;

  RETURN QUERY
  SELECT audit.project_id, 'expired_project_audit'::text,
         NULL::uuid, audit.id
  FROM public.audit_logs AS audit
  WHERE audit.project_id IS NOT NULL
    AND audit.created_at < pg_catalog.clock_timestamp() - interval '365 days'
    AND audit.audit_scope_kind IN ('book', 'scrubbed')
    AND audit.audit_payload_state IN ('safe', 'source_scrubbed', 'project_scrubbed')
    AND audit.audit_payload_schema IS NOT NULL
  ORDER BY audit.created_at, audit.id, audit.project_id
  LIMIT 1
  FOR UPDATE OF audit SKIP LOCKED;
END;
$$;


--
-- Name: ck_artifact_kind_tag(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_kind_tag(kind text) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    AS $$
  SELECT CASE kind
    WHEN 'canon_book_enrichment_input' THEN 2
    WHEN 'job' THEN 3
    WHEN 'import_upload' THEN 4
    WHEN 'import_job_request' THEN 5
    WHEN 'chat_turn' THEN 6
    WHEN 'writer_proposal' THEN 8
    WHEN 'export_artifact' THEN 10
    WHEN 'runtime_session' THEN 11
    WHEN 'chapter_summary' THEN 12
    WHEN 'canon_author_row' THEN 13
    WHEN 'canon_graph_contribution' THEN 14
    WHEN 'canon_correction_proposal' THEN 15
    WHEN 'agent_workflow_input' THEN 16
    ELSE 0 END
$$;


--
-- Name: ck_artifact_manifest_digest(text, uuid, uuid, text, bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_manifest_digest(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKArtifactSourceManifestV1', 'UTF8') || decode('00', 'hex')
    || ck_artifact_manifest_projection(
      artifact_kind, project_id, artifact_id, owner_source_view,
      ref_projections
    )
  )
$$;


--
-- Name: ck_artifact_manifest_digest_v2(text, uuid, uuid, text, bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_manifest_digest_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKArtifactSourceManifestV2', 'UTF8') || decode('00', 'hex')
    || public.ck_artifact_manifest_projection_v2(
      artifact_kind, project_id, artifact_id, owner_source_view, ref_projections
    )
  )
$$;


--
-- Name: ck_artifact_manifest_projection(text, uuid, uuid, text, bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_manifest_projection(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
  ref bytea;
  ref_count integer;
  kind_tag integer;
BEGIN
  kind_tag := ck_artifact_kind_tag(artifact_kind);
  ref_count := array_length(ref_projections, 1);
  IF kind_tag = 0 OR ref_count IS NULL OR ref_count < 1
     OR (SELECT count(DISTINCT item) FROM unnest(ref_projections) item) <> ref_count THEN
    RAISE EXCEPTION 'invalid artifact manifest projection input';
  END IF;
  result := ck_tag_byte(kind_tag) || uuid_send(project_id) || uuid_send(artifact_id)
    || ck_source_view_arm(owner_source_view) || int4send(1) || int4send(ref_count);
  FOR ref IN SELECT item FROM unnest(ref_projections) item ORDER BY item LOOP
    result := result || int8send(octet_length(ref)) || ref;
  END LOOP;
  RETURN result;
END;
$$;


--
-- Name: ck_artifact_manifest_projection_v2(text, uuid, uuid, text, bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_manifest_projection_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
  ref bytea;
  ref_count integer;
  kind_tag integer;
BEGIN
  kind_tag := public.ck_artifact_kind_tag(artifact_kind);
  ref_count := array_length(ref_projections, 1);
  IF kind_tag = 0 OR ref_count IS NULL OR ref_count < 1
     OR (SELECT count(DISTINCT item) FROM unnest(ref_projections) item) <> ref_count THEN
    RAISE EXCEPTION 'invalid V2 artifact manifest projection input';
  END IF;
  result := public.ck_tag_byte(kind_tag) || uuid_send(project_id) || uuid_send(artifact_id)
    || public.ck_source_view_arm(owner_source_view) || int4send(2) || int4send(ref_count);
  FOR ref IN SELECT item FROM unnest(ref_projections) item ORDER BY item LOOP
    result := result || int8send(octet_length(ref)) || ref;
  END LOOP;
  RETURN result;
END;
$$;


--
-- Name: ck_artifact_source_owner_is_current(uuid, uuid, text, uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_source_owner_is_current(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT EXISTS (
    SELECT 1
    FROM public.artifact_source_owners AS owner
    JOIN public.projects AS project
      ON project.id = owner.project_id AND project.deleted_at IS NULL
    WHERE owner.id = wanted_owner_id
      AND owner.project_id = wanted_project_id
      AND owner.artifact_kind = wanted_artifact_kind
      AND owner.artifact_id = wanted_artifact_id
      AND owner.read_state = 'active'
      AND owner.ref_count >= 1
      AND owner.ref_count = (
        SELECT count(*)::integer
        FROM public.artifact_source_refs AS counted_ref
        WHERE counted_ref.owner_id = owner.id
          AND counted_ref.project_id = owner.project_id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM public.artifact_source_refs AS ref
        LEFT JOIN public.books AS book
          ON book.id = ref.book_id AND book.project_id = ref.project_id
        LEFT JOIN public.chapters AS chapter
          ON chapter.id = ref.chapter_id AND chapter.book_id = ref.book_id
         AND chapter.project_id = ref.project_id
        LEFT JOIN public.chat_sessions AS chat
          ON chat.id = ref.chat_session_id AND chat.project_id = ref.project_id
        LEFT JOIN public.project_source_views AS source_project
          ON source_project.project_id = ref.project_id
         AND source_project.source_view = ref.source_view
         AND source_project.project_lifecycle_generation = ref.project_generation
        LEFT JOIN public.book_source_members AS source_book
          ON source_book.project_id = ref.project_id
         AND source_book.source_view = ref.source_view
         AND source_book.project_lifecycle_generation = ref.project_generation
         AND source_book.book_id = ref.book_id
         AND source_book.book_lifecycle_generation = ref.book_generation
        LEFT JOIN public.chapter_source_members AS source_chapter
          ON source_chapter.project_id = ref.project_id
         AND source_chapter.source_view = ref.source_view
         AND source_chapter.project_lifecycle_generation = ref.project_generation
         AND source_chapter.book_id = ref.book_id
         AND source_chapter.book_lifecycle_generation = ref.book_generation
         AND source_chapter.chapter_id = ref.chapter_id
         AND source_chapter.chapter_lifecycle_generation = ref.chapter_generation
         AND source_chapter.source_revision = ref.source_revision
        LEFT JOIN public.chapter_text_search_rows AS chapter_text
          ON chapter_text.project_id = ref.project_id
         AND chapter_text.source_view = ref.source_view
         AND chapter_text.book_id = ref.book_id
         AND chapter_text.chapter_id = ref.chapter_id
         AND chapter_text.source_revision = ref.source_revision
         AND chapter_text.locator_source_generation = ref.locator_source_generation
         AND chapter_text.chapter_content_hash = ref.content_hash
        WHERE ref.owner_id = owner.id
          AND ref.project_id = owner.project_id
          AND NOT (
            ref.project_generation = project.lifecycle_generation
            AND CASE
              WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'project' THEN
                ref.book_id IS NULL AND ref.chapter_id IS NULL
              WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'book' THEN
                book.id IS NOT NULL AND book.deleted_at IS NULL
                AND ref.book_generation = book.lifecycle_generation
                AND ref.chapter_id IS NULL
              WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'chapter' THEN
                book.id IS NOT NULL AND book.deleted_at IS NULL
                AND ref.book_generation = book.lifecycle_generation
                AND chapter.id IS NOT NULL AND chapter.deleted_at IS NULL
                AND ref.chapter_generation = chapter.lifecycle_generation
              WHEN ref.ref_kind = 'chapter' AND ref.text_identity_state = 'text_derived_current' THEN
                owner.manifest_version = 2
                AND source_project.project_id IS NOT NULL
                AND source_book.book_id IS NOT NULL
                AND source_chapter.chapter_id IS NOT NULL
                AND chapter_text.chapter_id IS NOT NULL
              ELSE false
            END
            AND (
              ref.chat_session_id IS NULL
              OR (
                chat.id IS NOT NULL AND chat.deleted_at IS NULL
                AND ref.chat_lifecycle_epoch = chat.lifecycle_epoch
              )
            )
          )
      )
  )
$$;


--
-- Name: ck_artifact_source_owner_is_lifecycle_readable(uuid, uuid, text, uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_source_owner_is_lifecycle_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT EXISTS (
    SELECT 1
    FROM public.artifact_source_owners AS owner
    JOIN public.projects AS project
      ON project.id = owner.project_id AND project.deleted_at IS NULL
    WHERE owner.id = wanted_owner_id
      AND owner.project_id = wanted_project_id
      AND owner.artifact_kind = wanted_artifact_kind
      AND owner.artifact_id = wanted_artifact_id
      AND owner.read_state = 'active'
      AND owner.fenced_at IS NULL
      AND owner.ref_count >= 1
      AND owner.ref_count = (
        SELECT count(*)::integer
        FROM public.artifact_source_refs AS counted_ref
        WHERE counted_ref.owner_id = owner.id
          AND counted_ref.project_id = owner.project_id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM public.artifact_source_refs AS ref
        LEFT JOIN public.books AS book
          ON book.id = ref.book_id AND book.project_id = ref.project_id
        LEFT JOIN public.chapters AS chapter
          ON chapter.id = ref.chapter_id AND chapter.book_id = ref.book_id
         AND chapter.project_id = ref.project_id
        LEFT JOIN public.chat_sessions AS chat
          ON chat.id = ref.chat_session_id AND chat.project_id = ref.project_id
        WHERE ref.owner_id = owner.id
          AND ref.project_id = owner.project_id
          AND NOT (
            ref.project_generation = project.lifecycle_generation
            AND (
              ref.book_id IS NULL
              OR (
                book.id IS NOT NULL AND book.deleted_at IS NULL
                AND ref.book_generation = book.lifecycle_generation
              )
            )
            AND (
              ref.chapter_id IS NULL
              OR (
                chapter.id IS NOT NULL AND chapter.deleted_at IS NULL
                AND ref.chapter_generation = chapter.lifecycle_generation
              )
            )
            AND (
              ref.chat_session_id IS NULL
              OR (
                chat.id IS NOT NULL AND chat.deleted_at IS NULL
                AND ref.chat_lifecycle_epoch = chat.lifecycle_epoch
              )
            )
          )
      )
  )
$$;


--
-- Name: ck_artifact_source_owner_is_readable(uuid, uuid, text, uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_source_owner_is_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT public.ck_artifact_source_owner_is_current(
    wanted_owner_id, wanted_project_id, wanted_artifact_kind, wanted_artifact_id
  )
$$;


--
-- Name: ck_artifact_source_ref_projection(text, text, uuid, bigint, uuid, bigint, uuid, bigint, text, boolean, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_source_ref_projection(ref_kind text, scope_kind text, project_id uuid, project_generation bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint, source_view text DEFAULT NULL::text, source_independent boolean DEFAULT false, chat_session_id uuid DEFAULT NULL::uuid, chat_lifecycle_epoch bigint DEFAULT NULL::bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
BEGIN
  IF ref_kind = 'scope' AND scope_kind = 'project' THEN
    IF (source_independent AND source_view IS NOT NULL)
       OR (NOT source_independent AND source_view IS NULL) THEN
      RAISE EXCEPTION 'invalid project-scope source view';
    END IF;
    result := ck_tag_byte(2)
      || ck_raw_source_vector('project', project_id, project_generation)
      || ck_source_view_arm(source_view);
  ELSIF ref_kind = 'scope' AND scope_kind = 'book' THEN
    IF source_independent OR source_view IS NULL THEN
      RAISE EXCEPTION 'invalid book-scope source view';
    END IF;
    result := ck_tag_byte(3)
      || ck_raw_source_vector(
        'book', project_id, project_generation, book_id, book_generation
      ) || ck_source_view_arm(source_view);
  ELSIF ref_kind = 'scope' AND scope_kind = 'chapter' THEN
    IF source_independent OR source_view IS NULL THEN
      RAISE EXCEPTION 'invalid chapter-scope source view';
    END IF;
    result := ck_tag_byte(4)
      || ck_raw_source_vector(
        'chapter', project_id, project_generation, book_id,
        book_generation, chapter_id, chapter_generation
      ) || ck_source_view_arm(source_view);
  ELSE
    RAISE EXCEPTION 'unknown artifact source-ref arm';
  END IF;
  RETURN result || ck_chat_arm(chat_session_id, chat_lifecycle_epoch);
END;
$$;


-- Name: ck_artifact_source_ref_projection_v2(text, text, uuid, bigint, uuid, bigint, uuid, bigint, bigint, text, bytea, bytea, text, boolean, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_artifact_source_ref_projection_v2(ref_kind text, scope_kind text, project_id uuid, project_generation bigint, book_id uuid, book_generation bigint, chapter_id uuid, chapter_generation bigint, source_revision bigint, source_view text, locator_source_generation bytea, content_hash bytea, text_identity_state text, source_independent boolean, chat_session_id uuid, chat_lifecycle_epoch bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
BEGIN
  IF ref_kind = 'chapter' THEN
    IF scope_kind IS NOT NULL OR source_view IS NULL
       OR source_revision < 1 OR octet_length(content_hash) <> 32
       OR content_hash = decode(repeat('00', 32), 'hex')
       OR octet_length(locator_source_generation) <> 32
       OR locator_source_generation <> public.ck_source_generation_digest(
         project_id, project_generation, book_id, book_generation,
         chapter_id, chapter_generation
       )
       OR text_identity_state <> 'text_derived_current' OR source_independent THEN
      RAISE EXCEPTION 'invalid exact chapter projection input';
    END IF;
    result := public.ck_tag_byte(5)
      || public.ck_raw_source_vector(
        'chapter', project_id, project_generation, book_id,
        book_generation, chapter_id, chapter_generation
      )
      || int8send(source_revision)
      || public.ck_source_view_arm(source_view)
      || locator_source_generation || content_hash;
    RETURN result || public.ck_chat_arm(chat_session_id, chat_lifecycle_epoch);
  END IF;
  IF ref_kind = 'scope' THEN
    IF text_identity_state IS NOT NULL OR source_revision IS NOT NULL
       OR content_hash IS NOT NULL OR locator_source_generation IS NOT NULL THEN
      RAISE EXCEPTION 'V2 scope ref carries text identity';
    END IF;
    RETURN public.ck_artifact_source_ref_projection(
      ref_kind, scope_kind, project_id, project_generation,
      book_id, book_generation, chapter_id, chapter_generation,
      source_view, source_independent,
      chat_session_id, chat_lifecycle_epoch
    );
  END IF;
  RAISE EXCEPTION 'unknown artifact source-ref arm';
END;
$$;
--
-- Name: ck_audit_payload_shape_valid(text, text, text, text, jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_audit_payload_shape_valid(operation_id text, resource_type text, outcome text, payload_schema text, data jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $_$
DECLARE
  uuid_pattern constant text :=
    '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
BEGIN
  IF data IS NULL OR pg_catalog.jsonb_typeof(data) <> 'object' THEN
    RETURN false;
  END IF;

  IF operation_id = 'getJobResult'
     AND resource_type = 'job'
     AND outcome = 'succeeded'
     AND payload_schema = 'export_downloaded_v1' THEN
    RETURN ck_jsonb_object_key_count(data) = 3
      AND data ?& ARRAY['requestId', 'bookId', 'format']
      AND pg_catalog.jsonb_typeof(data->'requestId') = 'string'
      AND pg_catalog.jsonb_typeof(data->'bookId') = 'string'
      AND pg_catalog.jsonb_typeof(data->'format') = 'string'
      AND data->>'requestId' ~ uuid_pattern
      AND data->>'bookId' ~ uuid_pattern
      AND data->>'format' IN ('epub', 'fb2');
  END IF;
  RETURN false;
END;
$_$;


--
-- Name: ck_audit_scrub_is_terminal(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_audit_scrub_is_terminal() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF OLD.audit_payload_state IN ('source_scrubbed', 'project_scrubbed')
     AND (
       NEW.audit_payload_state = 'safe'
       OR NEW.audit_scope_kind <> 'scrubbed'
       OR NEW.data_json <> '{}'::jsonb
       OR (OLD.audit_payload_state = 'project_scrubbed' AND NEW.project_id IS NOT NULL)
     ) THEN
    RAISE EXCEPTION 'scrubbed audit row cannot regain source identity'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_canon_graph_draft_valid(text, boolean); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_canon_graph_draft_valid(draft text, has_revision boolean) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE STRICT
    SET search_path TO 'pg_catalog'
    AS $_$
DECLARE
  document jsonb;
  resolved_rows jsonb;
  resolved_row jsonb;
  previous_binding jsonb;
  correction_row jsonb;
  action text;
BEGIN
  document := draft::jsonb;
  IF pg_catalog.jsonb_path_exists(document, '$.**.previousRowHandle') THEN
    RETURN false;
  END IF;

  IF has_revision THEN
    IF pg_catalog.jsonb_typeof(document) IS DISTINCT FROM 'object'
       OR public.ck_jsonb_object_length(document) <> 3
       OR NOT (document ? 'version' AND document ? 'revision' AND document ? 'rows')
       OR pg_catalog.jsonb_typeof(document->'version') IS DISTINCT FROM 'string'
       OR document->>'version' <> '1'
       OR pg_catalog.jsonb_typeof(document->'revision') IS DISTINCT FROM 'number'
       OR document->>'revision' !~ '^[1-9][0-9]*$'
       OR pg_catalog.jsonb_typeof(document->'rows') IS DISTINCT FROM 'array' THEN
      RETURN false;
    END IF;
    resolved_rows := document->'rows';
  ELSE
    IF document <> '[]'::jsonb THEN
      RETURN false;
    END IF;
    RETURN true;
  END IF;

  FOR resolved_row IN
    SELECT value FROM pg_catalog.jsonb_array_elements(resolved_rows) AS item(value)
  LOOP
    IF pg_catalog.jsonb_typeof(resolved_row) IS DISTINCT FROM 'object'
       OR pg_catalog.jsonb_typeof(resolved_row->'action') IS DISTINCT FROM 'string' THEN
      RETURN false;
    END IF;
    action := resolved_row->>'action';
    IF (action = 'retain' AND (
          public.ck_jsonb_object_length(resolved_row) <> 2
          OR NOT (resolved_row ? 'action' AND resolved_row ? 'previousBinding')
       ))
       OR (action = 'update' AND (
          public.ck_jsonb_object_length(resolved_row) <> 3
          OR NOT (
            resolved_row ? 'action'
            AND resolved_row ? 'previousBinding'
            AND resolved_row ? 'row'
          )
       ))
       OR (action = 'create' AND (
          public.ck_jsonb_object_length(resolved_row) <> 2
          OR NOT (resolved_row ? 'action' AND resolved_row ? 'row')
       ))
       OR action NOT IN ('retain', 'update', 'create') THEN
      RETURN false;
    END IF;

    IF resolved_row ? 'previousBinding' THEN
      previous_binding := resolved_row->'previousBinding';
      IF pg_catalog.jsonb_typeof(previous_binding) IS DISTINCT FROM 'number'
         OR previous_binding::text !~ '^(0|[1-9][0-9]*)$' THEN
        RETURN false;
      END IF;
    END IF;

    IF resolved_row ? 'row' THEN
      correction_row := resolved_row->'row';
      IF pg_catalog.jsonb_typeof(correction_row) IS DISTINCT FROM 'object'
         OR public.ck_jsonb_object_length(correction_row) <> 4
         OR NOT (
           correction_row ? 'version'
           AND correction_row ? 'kind'
           AND correction_row ? 'evidence'
           AND correction_row ? 'value'
         )
         OR pg_catalog.jsonb_typeof(correction_row->'version') IS DISTINCT FROM 'string'
         OR correction_row->>'version' <> '1'
         OR pg_catalog.jsonb_typeof(correction_row->'kind') IS DISTINCT FROM 'string'
         OR correction_row->>'kind' NOT IN (
           'entity', 'alias', 'fact', 'relationship',
           'event', 'event_link', 'character_state', 'dialogue_attribution'
         )
         OR pg_catalog.jsonb_typeof(correction_row->'evidence') IS DISTINCT FROM 'array'
         OR pg_catalog.jsonb_typeof(correction_row->'value') IS DISTINCT FROM 'object' THEN
        RETURN false;
      END IF;
    END IF;
  END LOOP;
  RETURN true;
EXCEPTION
  WHEN invalid_text_representation THEN
    RETURN false;
END;
$_$;


--
-- Name: ck_canon_graph_handle_bindings_valid(jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_canon_graph_handle_bindings_valid(bindings jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE STRICT
    SET search_path TO 'pg_catalog'
    AS $_$
DECLARE
  entry jsonb;
  binding jsonb;
BEGIN
  IF pg_catalog.jsonb_typeof(bindings) IS DISTINCT FROM 'array' THEN
    RETURN false;
  END IF;
  FOR entry IN SELECT value FROM pg_catalog.jsonb_array_elements(bindings) AS item(value)
  LOOP
    IF pg_catalog.jsonb_typeof(entry) IS DISTINCT FROM 'object'
       OR public.ck_jsonb_object_length(entry) <> 2
       OR NOT (entry ? 'tokenHash' AND entry ? 'binding')
       OR pg_catalog.jsonb_typeof(entry->'tokenHash') IS DISTINCT FROM 'string'
       OR entry->>'tokenHash' !~ '^[0-9a-f]{64}$'
       OR pg_catalog.jsonb_typeof(entry->'binding') IS DISTINCT FROM 'object' THEN
      RETURN false;
    END IF;
    binding := entry->'binding';
    IF public.ck_jsonb_object_length(binding) <> 7
       OR NOT (
         binding ? 'previousContributionId'
         AND binding ? 'targetManifest'
         AND binding ? 'jobId'
         AND binding ? 'claimIncarnation'
         AND binding ? 'runtimeSessionId'
         AND binding ? 'stableRowId'
         AND binding ? 'effectiveFingerprint'
       )
       OR pg_catalog.jsonb_typeof(binding->'previousContributionId') IS DISTINCT FROM 'string'
       OR binding->>'previousContributionId' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       OR pg_catalog.jsonb_typeof(binding->'targetManifest') IS DISTINCT FROM 'string'
       OR binding->>'targetManifest' !~ '^[0-9a-f]{64}$'
       OR binding->>'targetManifest' ~ '^0{64}$'
       OR pg_catalog.jsonb_typeof(binding->'jobId') IS DISTINCT FROM 'string'
       OR binding->>'jobId' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       OR pg_catalog.jsonb_typeof(binding->'runtimeSessionId') IS DISTINCT FROM 'string'
       OR binding->>'runtimeSessionId' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       OR pg_catalog.jsonb_typeof(binding->'stableRowId') IS DISTINCT FROM 'string'
       OR binding->>'stableRowId' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       OR pg_catalog.jsonb_typeof(binding->'effectiveFingerprint') IS DISTINCT FROM 'string'
       OR binding->>'effectiveFingerprint' !~ '^[0-9a-f]{64}$'
       OR binding->>'effectiveFingerprint' ~ '^0{64}$'
       OR pg_catalog.jsonb_typeof(binding->'claimIncarnation') IS DISTINCT FROM 'number'
       OR binding->>'claimIncarnation' !~ '^[1-9][0-9]*$' THEN
      RETURN false;
    END IF;
  END LOOP;
  RETURN true;
END;
$_$;


--
-- Name: ck_chat_arm(uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_chat_arm(chat_session_id uuid, chat_lifecycle_epoch bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
BEGIN
  IF chat_session_id IS NULL AND chat_lifecycle_epoch IS NULL THEN
    RETURN ck_tag_byte(0);
  END IF;
  IF chat_session_id IS NULL OR chat_lifecycle_epoch < 1 THEN
    RAISE EXCEPTION 'invalid chat arm';
  END IF;
  RETURN ck_tag_byte(1) || uuid_send(chat_session_id)
    || int8send(chat_lifecycle_epoch);
END;
$$;


--
-- Name: ck_chat_lifecycle_commit_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_chat_lifecycle_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF NOT EXISTS (
      SELECT 1
      FROM deletion_tombstones t
      JOIN deletion_target_tasks task
        ON task.tombstone_id = t.id
       AND task.project_id = t.project_id
       AND task.target = 'postgresql'
      JOIN deletion_target_step_progress step
        ON step.tombstone_id = task.tombstone_id
       AND step.project_id = task.project_id
       AND step.target = task.target
      WHERE t.project_id = OLD.project_id
        AND (
          (t.entity_kind = 'chat'
            AND t.entity_id = OLD.id
            AND t.chat_session_id = OLD.id
            AND t.chat_lifecycle_epoch = OLD.lifecycle_epoch - 1
            AND t.purge_deadline IS NOT DISTINCT FROM OLD.purge_after
            AND t.purge_mode = 'chat_epoch')
          OR
          (t.purge_mode = 'source_hard_purge'
            AND (
              (t.entity_kind = 'project'
                AND t.entity_id = OLD.project_id
                AND EXISTS (
                  SELECT 1 FROM projects selected
                  WHERE selected.id = t.project_id
                    AND selected.lifecycle_generation = t.project_generation
                    AND selected.deleted_at IS NOT NULL
                    AND selected.hard_purge_started_at = t.fenced_at
                ))
              OR
              (t.entity_kind = 'book'
                AND t.entity_id = OLD.book_id
                AND t.book_id = OLD.book_id
                AND EXISTS (
                  SELECT 1 FROM books selected
                  WHERE selected.id = t.book_id
                    AND selected.project_id = t.project_id
                    AND selected.lifecycle_generation = t.book_generation
                    AND selected.deleted_at IS NOT NULL
                    AND selected.hard_purge_started_at = t.fenced_at
                ))
            ))
        )
        AND t.status = 'processing'
        AND task.state = 'processing'
        AND task.claim_expires_at > clock_timestamp()
        AND step.state IN ('running', 'succeeded')
        AND step.claim_incarnation = task.claim_incarnation
        AND step.claimed_by_digest = task.claimed_by_digest
        AND step.step_name = lpad(step.step_order::text, 3, '0')
          || ':chat_sessions:delete'
        AND NOT EXISTS (
          SELECT 1 FROM deletion_target_tasks external
          WHERE external.tombstone_id = t.id
            AND external.project_id = t.project_id
            AND external.target IN ('runtime', 'filesystem')
            AND external.state NOT IN ('succeeded', 'no_data')
        )
    ) THEN
      RAISE EXCEPTION 'chat hard delete lacks an exact live PostgreSQL step claim'
        USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.project_id IS DISTINCT FROM OLD.project_id
     OR NEW.source_view IS DISTINCT FROM OLD.source_view
     OR NEW.scope IS DISTINCT FROM OLD.scope
     OR NEW.book_id IS DISTINCT FROM OLD.book_id
     OR NEW.title IS DISTINCT FROM OLD.title
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'chat session identity, view, scope and title are immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND (
       NEW.deleted_at IS DISTINCT FROM OLD.deleted_at
       OR NEW.purge_after IS DISTINCT FROM OLD.purge_after
       OR NEW.lifecycle_epoch IS DISTINCT FROM OLD.lifecycle_epoch
     ) THEN
    RAISE EXCEPTION 'deleted chat session lifecycle cannot change in place'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at
     AND (
       NEW.lifecycle_epoch IS DISTINCT FROM OLD.lifecycle_epoch
       OR NEW.purge_after IS DISTINCT FROM OLD.purge_after
     ) THEN
    RAISE EXCEPTION 'chat lifecycle fields change only during governed deletion'
      USING ERRCODE = 'check_violation';
  END IF;

  IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
    IF NEW.purge_after IS DISTINCT FROM NEW.deleted_at + interval '24 hours' THEN
      RAISE EXCEPTION 'governed chat delete has a noncanonical purge deadline'
        USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.lifecycle_epoch IS NULL
       OR NEW.lifecycle_epoch <> OLD.lifecycle_epoch + 1
       OR NOT EXISTS (
         SELECT 1 FROM deletion_tombstones t
         WHERE t.project_id = OLD.project_id
           AND t.entity_kind = 'chat'
           AND t.entity_id = OLD.id
           AND t.purge_mode = 'chat_epoch'
           AND t.origin = 'governed_online'
           AND t.chat_lifecycle_epoch = OLD.lifecycle_epoch
           AND t.purge_deadline IS NOT DISTINCT FROM NEW.purge_after
       ) THEN
      RAISE EXCEPTION 'chat delete lacks its exact incremented epoch tombstone'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_deletion_predecessor_set_digest(bytea, uuid[], bytea[], bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_predecessor_set_digest(hard_purge_selected_vector_digest bytea, tombstone_ids uuid[], tombstone_digests bytea[], target_receipt_set_digests bytea[]) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionPredecessorSetV1', 'UTF8') || decode('00', 'hex')
    || ck_deletion_predecessor_set_projection(
      hard_purge_selected_vector_digest, tombstone_ids,
      tombstone_digests, target_receipt_set_digests
    )
  )
$$;


--
-- Name: ck_deletion_predecessor_set_projection(bytea, uuid[], bytea[], bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_predecessor_set_projection(hard_purge_selected_vector_digest bytea, tombstone_ids uuid[], tombstone_digests bytea[], target_receipt_set_digests bytea[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
  item_count integer;
  index integer;
BEGIN
  item_count := array_length(tombstone_ids, 1);
  IF NOT ck_is_nonzero_digest(hard_purge_selected_vector_digest)
     OR item_count IS NULL OR item_count < 1
     OR array_length(tombstone_digests, 1) IS DISTINCT FROM item_count
     OR array_length(target_receipt_set_digests, 1) IS DISTINCT FROM item_count
     OR array_lower(tombstone_ids, 1) IS DISTINCT FROM 1
     OR array_lower(tombstone_digests, 1) IS DISTINCT FROM 1
     OR array_lower(target_receipt_set_digests, 1) IS DISTINCT FROM 1 THEN
    RAISE EXCEPTION 'invalid predecessor-set projection input';
  END IF;
  result := hard_purge_selected_vector_digest || int4send(item_count);
  FOR index IN 1..item_count LOOP
    IF tombstone_ids[index] IS NULL
       OR NOT ck_is_nonzero_digest(tombstone_digests[index])
       OR NOT ck_is_nonzero_digest(target_receipt_set_digests[index])
       OR (index > 1 AND uuid_send(tombstone_ids[index - 1]) >= uuid_send(tombstone_ids[index])) THEN
      RAISE EXCEPTION 'predecessor set is unordered or malformed';
    END IF;
    result := result || uuid_send(tombstone_ids[index])
      || tombstone_digests[index] || target_receipt_set_digests[index];
  END LOOP;
  RETURN result;
END;
$$;


--
-- Name: ck_deletion_selected_vector_digest(text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_selected_vector_digest(entity_kind text, project_id uuid, project_generation bigint DEFAULT NULL::bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint, chat_session_id uuid DEFAULT NULL::uuid, chat_lifecycle_epoch bigint DEFAULT NULL::bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionSelectedVectorV1', 'UTF8') || decode('00', 'hex')
    || ck_deletion_selected_vector_projection(
      entity_kind, project_id, project_generation, book_id, book_generation,
      chapter_id, chapter_generation, chat_session_id, chat_lifecycle_epoch
    )
  )
$$;


--
-- Name: ck_deletion_selected_vector_projection(text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_selected_vector_projection(entity_kind text, project_id uuid, project_generation bigint DEFAULT NULL::bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint, chat_session_id uuid DEFAULT NULL::uuid, chat_lifecycle_epoch bigint DEFAULT NULL::bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
BEGIN
  IF entity_kind = 'project'
     AND project_id IS NOT NULL
     AND project_generation >= 1
     AND book_id IS NULL AND book_generation IS NULL
     AND chapter_id IS NULL AND chapter_generation IS NULL
     AND chat_session_id IS NULL AND chat_lifecycle_epoch IS NULL THEN
    RETURN ck_tag_byte(1) || ck_source_generation_digest(
      project_id, project_generation, NULL, NULL, NULL, NULL
    );
  ELSIF entity_kind = 'book'
     AND project_id IS NOT NULL AND project_generation >= 1
     AND book_id IS NOT NULL AND book_generation >= 1
     AND chapter_id IS NULL AND chapter_generation IS NULL
     AND chat_session_id IS NULL AND chat_lifecycle_epoch IS NULL THEN
    RETURN ck_tag_byte(2) || ck_source_generation_digest(
      project_id, project_generation, book_id, book_generation, NULL, NULL
    );
  ELSIF entity_kind = 'chapter'
     AND project_id IS NOT NULL AND project_generation >= 1 AND book_id IS NOT NULL
     AND book_generation >= 1 AND chapter_id IS NOT NULL
     AND chapter_generation >= 1
     AND chat_session_id IS NULL AND chat_lifecycle_epoch IS NULL THEN
    RETURN ck_tag_byte(3) || ck_source_generation_digest(
      project_id, project_generation, book_id, book_generation,
      chapter_id, chapter_generation
    );
  ELSIF entity_kind = 'chat'
     AND project_id IS NOT NULL AND project_generation IS NULL
     AND book_id IS NULL AND book_generation IS NULL
     AND chapter_id IS NULL AND chapter_generation IS NULL
     AND chat_session_id IS NOT NULL AND chat_lifecycle_epoch >= 1 THEN
    RETURN ck_tag_byte(4) || uuid_send(project_id)
      || uuid_send(chat_session_id) || int8send(chat_lifecycle_epoch);
  END IF;
  RAISE EXCEPTION 'invalid selected deletion vector';
END;
$$;


--
-- Name: ck_deletion_target_receipt_digest(text, bytea, bytea, text, text, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_target_receipt_digest(target text, tombstone_digest bytea, selected_vector_digest bytea, handler_version text, disposition text, completed_at timestamp with time zone) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionTargetReceiptV1', 'UTF8') || decode('00', 'hex')
    || ck_deletion_target_receipt_projection(
      target, tombstone_digest, selected_vector_digest, handler_version,
      disposition, completed_at
    )
  )
$$;


--
-- Name: ck_deletion_target_receipt_projection(text, bytea, bytea, text, text, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_target_receipt_projection(target text, tombstone_digest bytea, selected_vector_digest bytea, handler_version text, disposition text, completed_at timestamp with time zone) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  target_tag integer;
  disposition_tag integer;
  handler_allowed boolean;
BEGIN
  target_tag := CASE target
    WHEN 'runtime' THEN 1 WHEN 'filesystem' THEN 2
    WHEN 'postgresql' THEN 4 ELSE 0 END;
  disposition_tag := CASE disposition
    WHEN 'purged' THEN 1 WHEN 'no_data' THEN 2 ELSE 0 END;
  handler_allowed :=
    (target = 'runtime'
      AND handler_version = 'runtime_direct_sdk_v1'
      AND disposition IN ('purged', 'no_data'))
    OR (target = 'filesystem'
      AND handler_version = 'filesystem_os_root_v1' AND disposition = 'purged')
    OR (target = 'postgresql'
      AND handler_version = 'postgres_exact_inventory_v1' AND disposition = 'purged');
  IF target_tag = 0 OR disposition_tag = 0
     OR handler_allowed IS DISTINCT FROM true OR completed_at IS NULL
     OR NOT ck_is_nonzero_digest(tombstone_digest)
     OR NOT ck_is_nonzero_digest(selected_vector_digest) THEN
    RAISE EXCEPTION 'invalid deletion target receipt projection input';
  END IF;
  RETURN ck_tag_byte(target_tag) || tombstone_digest || selected_vector_digest
    || int8send(octet_length(convert_to(handler_version, 'UTF8')))
    || convert_to(handler_version, 'UTF8')
    || ck_tag_byte(disposition_tag) || int8send(ck_unix_seconds(completed_at));
END;
$$;


--
-- Name: ck_deletion_target_receipt_set_digest(text, bytea, bytea, bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_target_receipt_set_digest(purge_mode text, tombstone_digest bytea, selected_vector_digest bytea, receipt_digests bytea[]) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionTargetReceiptSetV1', 'UTF8') || decode('00', 'hex')
    || ck_deletion_target_receipt_set_projection(
      purge_mode, tombstone_digest, selected_vector_digest, receipt_digests
    )
  )
$$;


--
-- Name: ck_deletion_target_receipt_set_projection(text, bytea, bytea, bytea[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_target_receipt_set_projection(purge_mode text, tombstone_digest bytea, selected_vector_digest bytea, receipt_digests bytea[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
  index integer;
  receipt_count integer;
  target_tags integer[];
BEGIN
  IF purge_mode NOT IN ('derived_generation', 'source_hard_purge', 'chat_epoch') THEN
    RAISE EXCEPTION 'invalid deletion receipt-set purge mode';
  END IF;
  target_tags := ARRAY[1, 2, 4];
  receipt_count := cardinality(target_tags);
  IF NOT ck_is_nonzero_digest(tombstone_digest)
     OR NOT ck_is_nonzero_digest(selected_vector_digest)
     OR array_length(receipt_digests, 1) IS DISTINCT FROM receipt_count
     OR array_lower(receipt_digests, 1) IS DISTINCT FROM 1 THEN
    RAISE EXCEPTION 'invalid deletion receipt-set projection input';
  END IF;
  result := tombstone_digest || selected_vector_digest || int4send(receipt_count);
  FOR index IN 1..receipt_count LOOP
    IF NOT ck_is_nonzero_digest(receipt_digests[index]) THEN
      RAISE EXCEPTION 'invalid deletion receipt digest';
    END IF;
    result := result || ck_tag_byte(target_tags[index]) || receipt_digests[index];
  END LOOP;
  RETURN result;
END;
$$;


--
-- Name: ck_deletion_tombstone_digest(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_tombstone_digest(tombstone_id uuid, origin text, purge_mode text, selected_entity_kind text, selected_project_id uuid, selected_project_generation bigint, selected_book_id uuid, selected_book_generation bigint, selected_chapter_id uuid, selected_chapter_generation bigint, selected_chat_session_id uuid, selected_chat_lifecycle_epoch bigint, fenced_at timestamp with time zone, purge_deadline timestamp with time zone) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionTombstoneV1', 'UTF8') || decode('00', 'hex')
    || ck_deletion_tombstone_projection(
      tombstone_id, origin, purge_mode,
      selected_entity_kind,
      selected_project_id,
      selected_project_generation,
      selected_book_id,
      selected_book_generation,
      selected_chapter_id,
      selected_chapter_generation,
      selected_chat_session_id,
      selected_chat_lifecycle_epoch,
      fenced_at, purge_deadline
    )
  )
$$;


--
-- Name: ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_deletion_tombstone_projection(tombstone_id uuid, origin text, purge_mode text, selected_entity_kind text, selected_project_id uuid, selected_project_generation bigint, selected_book_id uuid, selected_book_generation bigint, selected_chapter_id uuid, selected_chapter_generation bigint, selected_chat_session_id uuid, selected_chat_lifecycle_epoch bigint, fenced_at timestamp with time zone, purge_deadline timestamp with time zone) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  origin_tag integer;
  mode_tag integer;
  selected_vector_digest bytea;
  result bytea;
BEGIN
  origin_tag := CASE origin WHEN 'governed_online' THEN 1 ELSE 0 END;
  mode_tag := CASE purge_mode
    WHEN 'derived_generation' THEN 1
    WHEN 'source_hard_purge' THEN 2
    WHEN 'chat_epoch' THEN 3
    ELSE 0 END;
  IF tombstone_id IS NULL OR fenced_at IS NULL
     OR origin_tag = 0 OR mode_tag = 0
     OR (purge_mode = 'chat_epoch') IS DISTINCT FROM (selected_entity_kind = 'chat')
     OR purge_deadline IS NULL THEN
    RAISE EXCEPTION 'invalid deletion tombstone projection input';
  END IF;
  selected_vector_digest := ck_deletion_selected_vector_digest(
    selected_entity_kind,
    selected_project_id,
    selected_project_generation,
    selected_book_id,
    selected_book_generation,
    selected_chapter_id,
    selected_chapter_generation,
    selected_chat_session_id,
    selected_chat_lifecycle_epoch
  );
  result := uuid_send(tombstone_id) || ck_tag_byte(origin_tag)
    || ck_tag_byte(mode_tag) || selected_vector_digest
    || int8send(ck_unix_micros(fenced_at));
  RETURN result || ck_tag_byte(1) || int8send(ck_unix_micros(purge_deadline));
END;
$$;


--
-- Name: ck_enforce_export_artifact_transition(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_enforce_export_artifact_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.id <> OLD.id
     OR NEW.project_id <> OLD.project_id
     OR NEW.job_id <> OLD.job_id
     OR NEW.book_id <> OLD.book_id
     OR NEW.project_generation <> OLD.project_generation
     OR NEW.book_generation <> OLD.book_generation
     OR NEW.chat_session_id IS DISTINCT FROM OLD.chat_session_id
     OR NEW.chat_lifecycle_epoch IS DISTINCT FROM OLD.chat_lifecycle_epoch
     OR NEW.create_operation_id <> OLD.create_operation_id
     OR NEW.storage_object_key <> OLD.storage_object_key
     OR NEW.expected_content_digest <> OLD.expected_content_digest
     OR NEW.expected_size <> OLD.expected_size
     OR NEW.create_started_at <> OLD.create_started_at THEN
    RAISE EXCEPTION 'export creation identity is immutable'
      USING ERRCODE = 'check_violation';
  END IF;

  IF NOT (
    (OLD.object_state = 'creating' AND NEW.object_state IN ('creating', 'active', 'deleting'))
    OR (OLD.object_state = 'active' AND NEW.object_state IN ('active', 'deleting'))
    OR (OLD.object_state = 'deleting' AND NEW.object_state IN ('deleting', 'deleted'))
    OR (OLD.object_state = 'deleted' AND NEW.object_state = 'deleted')
  ) THEN
    RAISE EXCEPTION 'invalid export object-state transition'
      USING ERRCODE = 'check_violation';
  END IF;

  IF OLD.delete_operation_id IS NOT NULL AND (
    NEW.delete_operation_id IS DISTINCT FROM OLD.delete_operation_id
    OR NEW.delete_reason IS DISTINCT FROM OLD.delete_reason
    OR NEW.delete_started_at IS DISTINCT FROM OLD.delete_started_at
    OR NEW.delete_tombstone_id IS DISTINCT FROM OLD.delete_tombstone_id
    OR NEW.delete_project_id IS DISTINCT FROM OLD.delete_project_id
    OR NEW.delete_vector_digest IS DISTINCT FROM OLD.delete_vector_digest
  ) THEN
    RAISE EXCEPTION 'export deletion identity is immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.object_state = 'creating' AND NEW.object_state = 'active'
     AND OLD.pending_object_key IS NULL THEN
    RAISE EXCEPTION 'published export has no original pending object key'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_enforce_postgresql_step_order(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_enforce_postgresql_step_order() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.state IN ('running', 'succeeded')
     AND EXISTS (
       SELECT 1
       FROM deletion_target_step_progress prior
       WHERE prior.tombstone_id = NEW.tombstone_id
         AND prior.project_id = NEW.project_id
         AND prior.target = NEW.target
         AND prior.step_order < NEW.step_order
         AND prior.state <> 'succeeded'
     ) THEN
    RAISE EXCEPTION 'postgresql deletion steps must run in registry order'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_enforce_postgresql_target_order(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_enforce_postgresql_target_order() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
  IF NEW.target = 'postgresql'
     AND NEW.state IN ('processing', 'succeeded', 'no_data')
     AND OLD.state IS DISTINCT FROM NEW.state
     AND EXISTS (
       SELECT 1
       FROM public.deletion_target_tasks prior
       WHERE prior.tombstone_id = NEW.tombstone_id
         AND prior.project_id = NEW.project_id
         AND prior.target IN ('runtime', 'filesystem')
         AND prior.state NOT IN ('succeeded', 'no_data')
     ) THEN
    RAISE EXCEPTION 'postgresql target cannot start or finish before external targets'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_enforce_runtime_receipt_mode(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_enforce_runtime_receipt_mode() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
  IF NEW.target <> 'runtime' OR NEW.state NOT IN ('succeeded', 'no_data') THEN
    RETURN NEW;
  END IF;
  IF TG_OP = 'UPDATE'
     AND OLD.state IN ('succeeded', 'no_data')
     AND OLD.handler_version IS NOT DISTINCT FROM NEW.handler_version THEN
    RETURN NEW;
  END IF;

  IF NEW.handler_version <> 'runtime_direct_sdk_v1' THEN
    RAISE EXCEPTION 'runtime receipt requires the direct SDK handler'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_enforce_target_claim_cas(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_enforce_target_claim_cas() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    RETURN NEW;
  END IF;

  IF NEW.state = 'processing'
     AND (OLD.state <> 'processing'
       OR NEW.claim_incarnation <> OLD.claim_incarnation) THEN
    IF NEW.claim_incarnation <> OLD.claim_incarnation + 1
       OR NEW.claimed_by_digest IS NULL
       OR NEW.claimed_by_digest IS NOT DISTINCT FROM OLD.claimed_by_digest
       OR NEW.claim_expires_at <= clock_timestamp()
       OR NEW.attempts <> OLD.attempts + 1 THEN
      RAISE EXCEPTION 'target claim/reclaim must install a fresh incarnation lease'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF OLD.state = 'processing' AND NEW.state = 'failed' THEN
    IF NEW.claim_incarnation <> OLD.claim_incarnation
       OR NEW.attempts <> OLD.attempts
       OR NEW.claimed_by_digest IS NOT NULL
       OR NEW.claim_expires_at IS NOT NULL
       OR NEW.last_reason_code IS NULL
       OR NEW.next_attempt_at <= clock_timestamp() THEN
      RAISE EXCEPTION 'failed target transition must release the current fenced lease'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF OLD.state = 'processing' THEN
    IF NEW.claim_incarnation <> OLD.claim_incarnation
       OR NEW.claimed_by_digest IS DISTINCT FROM OLD.claimed_by_digest THEN
      RAISE EXCEPTION 'target transition is fenced by incarnation and owner digest'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF OLD.state IN ('succeeded', 'no_data')
     AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'terminal deletion target proof is immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_enforce_tombstone_immutability(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_enforce_tombstone_immutability() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.id <> OLD.id
     OR NEW.project_id <> OLD.project_id
     OR NEW.origin <> OLD.origin
     OR NEW.entity_kind <> OLD.entity_kind
     OR NEW.purge_mode <> OLD.purge_mode
     OR NEW.entity_id <> OLD.entity_id
     OR NEW.project_generation IS DISTINCT FROM OLD.project_generation
     OR NEW.book_id IS DISTINCT FROM OLD.book_id
     OR NEW.book_generation IS DISTINCT FROM OLD.book_generation
     OR NEW.chapter_id IS DISTINCT FROM OLD.chapter_id
     OR NEW.chapter_generation IS DISTINCT FROM OLD.chapter_generation
     OR NEW.chat_session_id IS DISTINCT FROM OLD.chat_session_id
     OR NEW.chat_lifecycle_epoch IS DISTINCT FROM OLD.chat_lifecycle_epoch
     OR NEW.fenced_at <> OLD.fenced_at
     OR NEW.purge_deadline IS DISTINCT FROM OLD.purge_deadline
     OR NEW.predecessor_set_digest IS DISTINCT FROM OLD.predecessor_set_digest THEN
    RAISE EXCEPTION 'typed tombstone identity is immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.audit_expires_at IS NOT NULL
     AND (NEW.audit_expires_at IS NULL OR NEW.audit_expires_at < OLD.audit_expires_at) THEN
    RAISE EXCEPTION 'typed proof expiry cannot be shortened'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.status = 'purged' AND NEW.status <> 'purged' THEN
    RAISE EXCEPTION 'purged tombstone is terminal'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_expire_governance_writer_lease(bytea, text, bytea); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_expire_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) RETURNS boolean
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  now_at timestamptz;
  affected bigint;
BEGIN
  PERFORM pg_catalog.pg_advisory_xact_lock_shared(
    public.governance_writer_fleet_fence_key()
  );
  now_at := pg_catalog.clock_timestamp();
  UPDATE public.governance_writer_leases AS lease
  SET last_seen_at = now_at - interval '1 microsecond',
      lease_expires_at = now_at
  WHERE lease.instance_id_digest = wanted_instance_id_digest
    AND lease.capability_version = wanted_capability_version
    AND lease.build_digest = wanted_build_digest
    AND lease.lease_expires_at > now_at;
  GET DIAGNOSTICS affected = ROW_COUNT;
  RETURN affected = 1;
END;
$$;


--
-- Name: ck_forbid_artifact_source_owner_identity_update(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_forbid_artifact_source_owner_identity_update() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF OLD.id IS DISTINCT FROM NEW.id
     OR OLD.project_id IS DISTINCT FROM NEW.project_id
     OR OLD.artifact_kind IS DISTINCT FROM NEW.artifact_kind
     OR OLD.artifact_id IS DISTINCT FROM NEW.artifact_id THEN
    RAISE EXCEPTION 'artifact source owner identity is immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_forbid_artifact_source_ref_update(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_forbid_artifact_source_ref_update() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  RAISE EXCEPTION 'sealed artifact source references are immutable'
    USING ERRCODE = 'check_violation';
END;
$$;


--
-- Name: ck_heartbeat_governance_writer_lease(bytea, text, bytea); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_heartbeat_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) RETURNS TABLE(last_seen_at timestamp with time zone, lease_expires_at timestamp with time zone)
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  now_at timestamptz;
BEGIN
  PERFORM pg_catalog.pg_advisory_xact_lock_shared(
    public.governance_writer_fleet_fence_key()
  );
  now_at := pg_catalog.clock_timestamp();
  IF NOT public.ck_writer_lease_identity_is_valid(
    wanted_instance_id_digest, wanted_capability_version, wanted_build_digest
  ) THEN
    RAISE EXCEPTION 'writer lease is not compatible'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN QUERY
  UPDATE public.governance_writer_leases AS lease
  SET last_seen_at = now_at,
      lease_expires_at = now_at + interval '90 seconds'
  WHERE lease.instance_id_digest = wanted_instance_id_digest
    AND lease.capability_version = wanted_capability_version
    AND lease.build_digest = wanted_build_digest
    AND lease.lease_expires_at > now_at
  RETURNING lease.last_seen_at, lease.lease_expires_at;
END;
$$;


--
-- Name: ck_is_nonzero_digest(bytea); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_is_nonzero_digest(value bytea) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT value IS NOT NULL
    AND octet_length(value) = 32
    AND value <> decode(repeat('00', 32), 'hex')
$$;


--
-- Name: ck_jsonb_object_key_count(jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_jsonb_object_key_count(data jsonb) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    AS $$
  SELECT count(*)::integer FROM jsonb_object_keys(data)
$$;


--
-- Name: ck_jsonb_object_length(jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_jsonb_object_length(candidate jsonb) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT pg_catalog.count(*)::integer
  FROM pg_catalog.jsonb_object_keys(candidate);
$$;


--
-- Name: ck_purge_expired_governance_writer_leases(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_purge_expired_governance_writer_leases() RETURNS bigint
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  affected bigint;
BEGIN
  PERFORM pg_catalog.pg_advisory_xact_lock_shared(
    public.governance_writer_fleet_fence_key()
  );
  WITH db_clock AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now_at
  ), candidates AS MATERIALIZED (
    SELECT lease.instance_id_digest
    FROM public.governance_writer_leases AS lease
    CROSS JOIN db_clock
    WHERE lease.lease_expires_at <= db_clock.now_at
    ORDER BY lease.lease_expires_at, lease.instance_id_digest
    LIMIT 100
    FOR UPDATE OF lease SKIP LOCKED
  )
  DELETE FROM public.governance_writer_leases AS lease
  USING candidates, db_clock
  WHERE lease.instance_id_digest = candidates.instance_id_digest
    AND lease.lease_expires_at <= db_clock.now_at;
  GET DIAGNOSTICS affected = ROW_COUNT;
  RETURN affected;
END;
$$;


--
-- Name: ck_raw_source_vector(text, uuid, bigint, uuid, bigint, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_raw_source_vector(scope_kind text, project_id uuid, project_generation bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
BEGIN
  IF project_generation < 1 THEN RAISE EXCEPTION 'invalid project generation'; END IF;
  result := uuid_send(project_id) || int8send(project_generation);
  IF scope_kind IN ('book', 'chapter') THEN
    IF book_id IS NULL OR book_generation < 1 THEN RAISE EXCEPTION 'invalid book vector'; END IF;
    result := result || uuid_send(book_id) || int8send(book_generation);
  ELSIF book_id IS NOT NULL OR book_generation IS NOT NULL THEN
    RAISE EXCEPTION 'unexpected book vector';
  END IF;
  IF scope_kind = 'chapter' THEN
    IF chapter_id IS NULL OR chapter_generation < 1 THEN RAISE EXCEPTION 'invalid chapter vector'; END IF;
    result := result || uuid_send(chapter_id) || int8send(chapter_generation);
  ELSIF chapter_id IS NOT NULL OR chapter_generation IS NOT NULL THEN
    RAISE EXCEPTION 'unexpected chapter vector';
  END IF;
  RETURN result;
END;
$$;


--
-- Name: ck_register_governance_writer_lease(bytea, text, bytea); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_register_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) RETURNS TABLE(last_seen_at timestamp with time zone, lease_expires_at timestamp with time zone)
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  now_at timestamptz;
BEGIN
  PERFORM pg_catalog.pg_advisory_xact_lock_shared(
    public.governance_writer_fleet_fence_key()
  );
  now_at := pg_catalog.clock_timestamp();
  IF NOT public.ck_writer_lease_identity_is_valid(
    wanted_instance_id_digest, wanted_capability_version, wanted_build_digest
  ) THEN
    RAISE EXCEPTION 'writer lease is not compatible'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN QUERY
  INSERT INTO public.governance_writer_leases AS lease (
    instance_id_digest, capability_version, build_digest,
    last_seen_at, lease_expires_at
  ) VALUES (
    wanted_instance_id_digest, wanted_capability_version, wanted_build_digest,
    now_at, now_at + interval '90 seconds'
  )
  ON CONFLICT (instance_id_digest) DO UPDATE
  SET last_seen_at = EXCLUDED.last_seen_at,
      lease_expires_at = EXCLUDED.lease_expires_at
  WHERE lease.capability_version = EXCLUDED.capability_version
    AND lease.build_digest = EXCLUDED.build_digest
    AND lease.lease_expires_at > EXCLUDED.last_seen_at
  RETURNING lease.last_seen_at, lease.lease_expires_at;
END;
$$;


--
-- Name: ck_reject_restore_after_hard_purge(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_reject_restore_after_hard_purge() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF OLD.deleted_at IS NOT NULL
     AND NEW.deleted_at IS NULL
     AND OLD.hard_purge_started_at IS NOT NULL THEN
    RAISE EXCEPTION 'restore is forbidden after hard purge has started'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL THEN
    IF NEW.governance_retention_started_at IS NOT NULL
       OR NEW.purge_after IS NOT NULL
       OR NEW.purge_deadline IS NOT NULL
       OR NEW.hard_purge_started_at IS NOT NULL THEN
      RAISE EXCEPTION 'restore must clear all governance retention fields'
        USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.lifecycle_generation IS DISTINCT FROM OLD.lifecycle_generation THEN
      RAISE EXCEPTION 'restore must not change lifecycle generation'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_source_generation_digest(uuid, bigint, uuid, bigint, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_source_generation_digest(project_id uuid, project_generation bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKSourceGenerationV1', 'UTF8') || decode('00', 'hex')
    || public.ck_source_generation_projection(
      project_id, project_generation, book_id, book_generation,
      chapter_id, chapter_generation
    )
  )
$$;


--
-- Name: ck_source_generation_projection(uuid, bigint, uuid, bigint, uuid, bigint); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_source_generation_projection(project_id uuid, project_generation bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
BEGIN
  IF project_id IS NULL OR project_generation < 1
     OR ((book_id IS NULL) <> (book_generation IS NULL))
     OR ((chapter_id IS NULL) <> (chapter_generation IS NULL))
     OR (chapter_id IS NOT NULL AND book_id IS NULL)
     OR (book_generation IS NOT NULL AND book_generation < 1)
     OR (chapter_generation IS NOT NULL AND chapter_generation < 1) THEN
    RAISE EXCEPTION 'invalid source generation vector';
  END IF;
  result := int8send(16) || uuid_send(project_id) || int8send(project_generation);
  IF book_id IS NOT NULL THEN
    result := result || int8send(16) || uuid_send(book_id) || int8send(book_generation);
  END IF;
  IF chapter_id IS NOT NULL THEN
    result := result || int8send(16) || uuid_send(chapter_id) || int8send(chapter_generation);
  END IF;
  RETURN result;
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: deletion_tombstones; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deletion_tombstones (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    origin text NOT NULL,
    entity_kind text NOT NULL,
    purge_mode text NOT NULL,
    entity_id uuid NOT NULL,
    project_generation bigint,
    book_id uuid,
    book_generation bigint,
    chapter_id uuid,
    chapter_generation bigint,
    chat_session_id uuid,
    chat_lifecycle_epoch bigint,
    status text NOT NULL,
    fenced_at timestamp with time zone NOT NULL,
    purge_deadline timestamp with time zone,
    retention_deadline_breached_at timestamp with time zone,
    purged_at timestamp with time zone,
    audit_expires_at timestamp with time zone,
    predecessor_set_digest bytea,
    last_reason_code text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT deletion_tombstones_breach_shape CHECK (((retention_deadline_breached_at IS NULL) OR (retention_deadline_breached_at >= purge_deadline))),
    CONSTRAINT deletion_tombstones_check CHECK ((((entity_kind = 'project'::text) AND (purge_mode = ANY (ARRAY['derived_generation'::text, 'source_hard_purge'::text])) AND (entity_id = project_id) AND (project_generation >= 1) AND (book_id IS NULL) AND (book_generation IS NULL) AND (chapter_id IS NULL) AND (chapter_generation IS NULL) AND (chat_session_id IS NULL) AND (chat_lifecycle_epoch IS NULL)) OR ((entity_kind = 'book'::text) AND (purge_mode = ANY (ARRAY['derived_generation'::text, 'source_hard_purge'::text])) AND (entity_id = book_id) AND (project_generation >= 1) AND (book_id IS NOT NULL) AND (book_generation >= 1) AND (chapter_id IS NULL) AND (chapter_generation IS NULL) AND (chat_session_id IS NULL) AND (chat_lifecycle_epoch IS NULL)) OR ((entity_kind = 'chapter'::text) AND (purge_mode = ANY (ARRAY['derived_generation'::text, 'source_hard_purge'::text])) AND (entity_id = chapter_id) AND (project_generation >= 1) AND (book_id IS NOT NULL) AND (book_generation >= 1) AND (chapter_id IS NOT NULL) AND (chapter_generation >= 1) AND (chat_session_id IS NULL) AND (chat_lifecycle_epoch IS NULL)) OR ((entity_kind = 'chat'::text) AND (purge_mode = 'chat_epoch'::text) AND (entity_id = chat_session_id) AND (project_generation IS NULL) AND (book_id IS NULL) AND (book_generation IS NULL) AND (chapter_id IS NULL) AND (chapter_generation IS NULL) AND (chat_session_id IS NOT NULL) AND (chat_lifecycle_epoch >= 1)))),
    CONSTRAINT deletion_tombstones_check1 CHECK ((((purge_mode = 'source_hard_purge'::text) AND (predecessor_set_digest IS NOT NULL) AND (octet_length(predecessor_set_digest) = 32) AND (predecessor_set_digest <> decode(repeat('00'::text, 32), 'hex'::text))) OR ((purge_mode <> 'source_hard_purge'::text) AND (predecessor_set_digest IS NULL)))),
    CONSTRAINT deletion_tombstones_check5 CHECK ((((status <> 'purged'::text) AND (purged_at IS NULL) AND (audit_expires_at IS NULL)) OR ((status = 'purged'::text) AND (purged_at IS NOT NULL) AND (((purge_mode = ANY (ARRAY['source_hard_purge'::text, 'chat_epoch'::text])) AND (audit_expires_at = (purged_at + '30 days'::interval))) OR ((purge_mode = 'derived_generation'::text) AND ((audit_expires_at IS NULL) OR (audit_expires_at >= (purged_at + '30 days'::interval)))))))),
    CONSTRAINT deletion_tombstones_deadline_required CHECK ((purge_deadline IS NOT NULL)),
    CONSTRAINT deletion_tombstones_deadline_shape CHECK (((purge_mode = 'source_hard_purge'::text) OR (purge_deadline = (fenced_at + '24:00:00'::interval)))),
    CONSTRAINT deletion_tombstones_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['project'::text, 'book'::text, 'chapter'::text, 'chat'::text]))),
    CONSTRAINT deletion_tombstones_last_reason_code_check CHECK (((last_reason_code IS NULL) OR (last_reason_code = ANY (ARRAY['receipt_missing'::text, 'target_unavailable'::text, 'stale_source'::text, 'expired_lease'::text, 'validation_failed'::text])))),
    CONSTRAINT deletion_tombstones_origin_check CHECK ((origin = 'governed_online'::text)),
    CONSTRAINT deletion_tombstones_purge_mode_check CHECK ((purge_mode = ANY (ARRAY['derived_generation'::text, 'source_hard_purge'::text, 'chat_epoch'::text]))),
    CONSTRAINT deletion_tombstones_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'purged'::text, 'failed'::text])))
);


--
-- Name: ck_source_hard_purge_contains_derived(public.deletion_tombstones, public.deletion_tombstones); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_source_hard_purge_contains_derived(hard_tombstone public.deletion_tombstones, derived_tombstone public.deletion_tombstones) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT
    (hard_tombstone).purge_mode = 'source_hard_purge'
    AND (derived_tombstone).purge_mode = 'derived_generation'
    AND (hard_tombstone).project_id = (derived_tombstone).project_id
    AND (
      (hard_tombstone).entity_kind = 'project'
      OR (
        (hard_tombstone).entity_kind = 'book'
        AND (derived_tombstone).entity_kind IN ('book', 'chapter')
        AND (hard_tombstone).book_id = (derived_tombstone).book_id
      )
      OR (
        (hard_tombstone).entity_kind = 'chapter'
        AND (derived_tombstone).entity_kind = 'chapter'
        AND (hard_tombstone).chapter_id = (derived_tombstone).chapter_id
      )
    )
$$;


--
-- Name: ck_source_hard_purge_predecessor_set_is_valid(uuid, uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_source_hard_purge_predecessor_set_is_valid(requested_hard_tombstone_id uuid, requested_project_id uuid) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $$
DECLARE
  hard_tombstone deletion_tombstones%ROWTYPE;
  hard_selected_vector_digest bytea;
  expected_count bigint;
  actual_count bigint;
  predecessor_ids uuid[];
  predecessor_tombstone_digests bytea[];
  predecessor_target_receipt_digests bytea[];
  computed_set_digest bytea;
BEGIN
  SELECT * INTO hard_tombstone
  FROM deletion_tombstones AS candidate
  WHERE candidate.id = requested_hard_tombstone_id
    AND candidate.project_id = requested_project_id;

  IF NOT FOUND
     OR hard_tombstone.purge_mode <> 'source_hard_purge'
     OR hard_tombstone.origin <> 'governed_online'
     OR hard_tombstone.entity_kind = 'chat' THEN
    RETURN false;
  END IF;

  hard_selected_vector_digest := ck_deletion_selected_vector_digest(
    hard_tombstone.entity_kind,
    hard_tombstone.project_id,
    hard_tombstone.project_generation,
    hard_tombstone.book_id,
    hard_tombstone.book_generation,
    hard_tombstone.chapter_id,
    hard_tombstone.chapter_generation,
    hard_tombstone.chat_session_id,
    hard_tombstone.chat_lifecycle_epoch
  );

  SELECT count(*) INTO expected_count
  FROM deletion_tombstones AS derived
  WHERE derived.status = 'purged'
    AND derived.fenced_at <= hard_tombstone.fenced_at
    AND ck_source_hard_purge_contains_derived(hard_tombstone, derived);

  SELECT count(*) INTO actual_count
  FROM deletion_hard_purge_predecessors AS predecessor
  WHERE predecessor.hard_tombstone_id = hard_tombstone.id
    AND predecessor.project_id = hard_tombstone.project_id;

  IF expected_count = 0 OR actual_count <> expected_count THEN
    RETURN false;
  END IF;

  IF EXISTS (
    SELECT 1
    FROM deletion_hard_purge_predecessors AS predecessor
    LEFT JOIN deletion_tombstones AS derived
      ON derived.id = predecessor.predecessor_tombstone_id
     AND derived.project_id = predecessor.project_id
    LEFT JOIN deletion_completion_receipts AS receipt
      ON receipt.tombstone_id = predecessor.predecessor_tombstone_id
     AND receipt.project_id = predecessor.project_id
    WHERE predecessor.hard_tombstone_id = hard_tombstone.id
      AND predecessor.project_id = hard_tombstone.project_id
      AND (
        derived.id IS NULL
        OR derived.purge_mode <> 'derived_generation'
        OR derived.status <> 'purged'
        OR derived.fenced_at > hard_tombstone.fenced_at
        OR NOT ck_source_hard_purge_contains_derived(hard_tombstone, derived)
        OR receipt.tombstone_id IS NULL
        OR receipt.vector_digest IS DISTINCT FROM ck_deletion_selected_vector_digest(
          derived.entity_kind,
          derived.project_id,
          derived.project_generation,
          derived.book_id,
          derived.book_generation,
          derived.chapter_id,
          derived.chapter_generation,
          derived.chat_session_id,
          derived.chat_lifecycle_epoch
        )
        OR predecessor.predecessor_vector_digest IS DISTINCT FROM receipt.vector_digest
        OR predecessor.predecessor_tombstone_digest IS DISTINCT FROM ck_deletion_tombstone_digest(
          derived.id,
          derived.origin,
          derived.purge_mode,
          derived.entity_kind,
          derived.project_id,
          derived.project_generation,
          derived.book_id,
          derived.book_generation,
          derived.chapter_id,
          derived.chapter_generation,
          derived.chat_session_id,
          derived.chat_lifecycle_epoch,
          derived.fenced_at,
          derived.purge_deadline
        )
        OR predecessor.predecessor_target_receipts_digest
          IS DISTINCT FROM receipt.target_receipts_digest
      )
  ) THEN
    RETURN false;
  END IF;

  SELECT
    array_agg(predecessor.predecessor_tombstone_id
      ORDER BY uuid_send(predecessor.predecessor_tombstone_id)),
    array_agg(predecessor.predecessor_tombstone_digest
      ORDER BY uuid_send(predecessor.predecessor_tombstone_id)),
    array_agg(predecessor.predecessor_target_receipts_digest
      ORDER BY uuid_send(predecessor.predecessor_tombstone_id))
  INTO predecessor_ids, predecessor_tombstone_digests,
       predecessor_target_receipt_digests
  FROM deletion_hard_purge_predecessors AS predecessor
  WHERE predecessor.hard_tombstone_id = hard_tombstone.id
    AND predecessor.project_id = hard_tombstone.project_id;

  computed_set_digest := ck_deletion_predecessor_set_digest(
    hard_selected_vector_digest,
    predecessor_ids,
    predecessor_tombstone_digests,
    predecessor_target_receipt_digests
  );
  RETURN computed_set_digest = hard_tombstone.predecessor_set_digest;
END;
$$;


--
-- Name: ck_source_lifecycle_commit_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_source_lifecycle_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  source_entity_kind text;
  source_entity_id uuid;
  source_project_id uuid;
  source_book_id uuid;
  selected_generation bigint;
BEGIN
  IF TG_TABLE_NAME = 'projects' THEN
    source_entity_kind := 'project';
    source_entity_id := OLD.id;
    source_project_id := OLD.id;
  ELSIF TG_TABLE_NAME = 'books' THEN
    source_entity_kind := 'book';
    source_entity_id := OLD.id;
    source_project_id := OLD.project_id;
    source_book_id := OLD.id;
  ELSIF TG_TABLE_NAME = 'chapters' THEN
    source_entity_kind := 'chapter';
    source_entity_id := OLD.id;
    source_project_id := OLD.project_id;
    source_book_id := OLD.book_id;
  ELSE
    RAISE EXCEPTION 'unregistered source lifecycle table';
  END IF;

  IF TG_OP = 'DELETE' THEN
    IF NOT EXISTS (
      SELECT 1
      FROM deletion_tombstones t
      JOIN deletion_target_tasks task
        ON task.tombstone_id = t.id
       AND task.project_id = t.project_id
       AND task.target = 'postgresql'
      JOIN deletion_target_step_progress step
        ON step.tombstone_id = task.tombstone_id
       AND step.project_id = task.project_id
       AND step.target = task.target
      WHERE t.project_id = source_project_id
        AND t.purge_mode = 'source_hard_purge'
        AND t.status = 'processing'
        AND task.state = 'processing'
        AND task.claim_expires_at > clock_timestamp()
        AND step.state IN ('running', 'succeeded')
        AND step.claim_incarnation = task.claim_incarnation
        AND step.claimed_by_digest = task.claimed_by_digest
        AND step.step_name = lpad(step.step_order::text, 3, '0')
          || ':' || TG_TABLE_NAME || ':delete'
        AND NOT EXISTS (
          SELECT 1 FROM deletion_target_tasks external
          WHERE external.tombstone_id = t.id
            AND external.project_id = t.project_id
            AND external.target IN ('runtime', 'filesystem')
            AND external.state NOT IN ('succeeded', 'no_data')
        )
        AND (
          (t.entity_kind = 'project'
            AND t.entity_id = t.project_id
            AND source_project_id = t.project_id
            AND (
              (TG_TABLE_NAME = 'projects'
                AND source_entity_id = t.project_id
                AND OLD.lifecycle_generation = t.project_generation
                AND OLD.deleted_at IS NOT NULL
                AND OLD.hard_purge_started_at = t.fenced_at)
              OR
              (TG_TABLE_NAME IN ('books', 'chapters')
                AND EXISTS (
                  SELECT 1 FROM projects selected
                  WHERE selected.id = t.project_id
                    AND selected.lifecycle_generation = t.project_generation
                    AND selected.deleted_at IS NOT NULL
                    AND selected.hard_purge_started_at = t.fenced_at
                ))
            ))
          OR
          (t.entity_kind = 'book'
            AND t.entity_id = t.book_id
            AND source_project_id = t.project_id
            AND source_book_id = t.book_id
            AND (
              (TG_TABLE_NAME = 'books'
                AND source_entity_id = t.book_id
                AND OLD.lifecycle_generation = t.book_generation
                AND OLD.deleted_at IS NOT NULL
                AND OLD.hard_purge_started_at = t.fenced_at)
              OR
              (TG_TABLE_NAME = 'chapters'
                AND EXISTS (
                  SELECT 1 FROM books selected
                  WHERE selected.id = t.book_id
                    AND selected.project_id = t.project_id
                    AND selected.lifecycle_generation = t.book_generation
                    AND selected.deleted_at IS NOT NULL
                    AND selected.hard_purge_started_at = t.fenced_at
                ))
            ))
          OR
          (t.entity_kind = 'chapter'
            AND t.entity_id = t.chapter_id
            AND TG_TABLE_NAME = 'chapters'
            AND source_entity_id = t.chapter_id
            AND source_book_id = t.book_id
            AND OLD.lifecycle_generation = t.chapter_generation
            AND OLD.deleted_at IS NOT NULL
            AND OLD.hard_purge_started_at = t.fenced_at)
        )
    ) THEN
      RAISE EXCEPTION 'source hard delete lacks an exact live PostgreSQL step claim'
        USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
    selected_generation := OLD.lifecycle_generation;
    IF NEW.governance_retention_started_at IS DISTINCT FROM NEW.deleted_at
       OR NEW.purge_after IS DISTINCT FROM NEW.deleted_at + interval '7 days'
       OR NEW.purge_deadline IS DISTINCT FROM NEW.purge_after + interval '24 hours'
       OR NEW.hard_purge_started_at IS NOT NULL THEN
      RAISE EXCEPTION 'governed source delete has a noncanonical retention clock'
        USING ERRCODE = 'check_violation';
    END IF;

    IF selected_generation IS NULL
       OR NEW.lifecycle_generation <> selected_generation + 1
       OR NOT EXISTS (
         SELECT 1 FROM deletion_tombstones t
         WHERE t.project_id = source_project_id
           AND t.entity_kind = source_entity_kind
           AND t.entity_id = source_entity_id
           AND t.purge_mode = 'derived_generation'
           AND t.origin = 'governed_online'
           AND (
             (source_entity_kind = 'project' AND t.project_generation = selected_generation)
             OR (source_entity_kind = 'book' AND t.book_generation = selected_generation)
             OR (source_entity_kind = 'chapter' AND t.chapter_generation = selected_generation)
           )
       ) THEN
      RAISE EXCEPTION 'source delete lacks its exact incremented tombstone fence'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL THEN
    IF OLD.lifecycle_generation IS NULL
       OR OLD.purge_after IS NULL
       OR clock_timestamp() >= OLD.purge_after THEN
      RAISE EXCEPTION 'source restore is outside its governed retention window'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF OLD.hard_purge_started_at IS NULL
     AND NEW.hard_purge_started_at IS NOT NULL
     AND (
       NEW.purge_after IS NULL
       OR clock_timestamp() < NEW.purge_after
       OR NOT EXISTS (
       SELECT 1 FROM deletion_tombstones t
       WHERE t.project_id = source_project_id
         AND t.entity_kind = source_entity_kind
         AND t.entity_id = source_entity_id
         AND t.purge_mode = 'source_hard_purge'
         AND t.origin = 'governed_online'
         AND (
           (source_entity_kind = 'project' AND t.project_generation = NEW.lifecycle_generation)
           OR (source_entity_kind = 'book' AND t.book_generation = NEW.lifecycle_generation)
           OR (source_entity_kind = 'chapter' AND t.chapter_generation = NEW.lifecycle_generation)
         )
         AND t.purge_deadline = NEW.purge_deadline
         AND ck_source_hard_purge_predecessor_set_is_valid(t.id, t.project_id)
       )
     ) THEN
    RAISE EXCEPTION 'hard-purge fence lacks its typed predecessor proof'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_source_view_arm(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_source_view_arm(source_view text) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
BEGIN
  IF source_view IS NULL THEN RETURN ck_tag_byte(0); END IF;
  IF source_view = 'draft' THEN RETURN ck_tag_byte(1) || ck_tag_byte(1); END IF;
  IF source_view = 'published' THEN RETURN ck_tag_byte(1) || ck_tag_byte(2); END IF;
  RAISE EXCEPTION 'invalid source view arm';
END;
$$;


--
-- Name: ck_tag_byte(integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_tag_byte(value integer) RETURNS bytea
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    AS $$ SELECT decode(lpad(to_hex(value), 2, '0'), 'hex') $$;


--
-- Name: ck_tombstone_target_set_complete(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_tombstone_target_set_complete() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  wanted_id uuid;
  wanted_project uuid;
  target_count integer;
BEGIN
  IF TG_TABLE_NAME = 'deletion_tombstones' THEN
    wanted_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
    wanted_project := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  ELSE
    wanted_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.tombstone_id ELSE NEW.tombstone_id END;
    wanted_project := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM public.deletion_tombstones AS tombstone
    WHERE tombstone.id = wanted_id AND tombstone.project_id = wanted_project
  ) THEN
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
  END IF;

  SELECT count(*)
  INTO target_count
  FROM public.deletion_target_tasks AS task
  WHERE task.tombstone_id = wanted_id
    AND task.project_id = wanted_project
    AND task.target IN ('runtime', 'filesystem', 'postgresql');
  IF target_count <> 3 THEN
    RAISE EXCEPTION 'typed deletion requires the exact target set for its entity kind'
      USING ERRCODE = 'check_violation';
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;


--
-- Name: ck_unix_micros(timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_unix_micros(value timestamp with time zone) RETURNS bigint
    LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
    AS $$
DECLARE
  epoch_micros numeric;
BEGIN
  epoch_micros := extract(epoch FROM value)::numeric * 1000000;
  IF epoch_micros < 0 OR trunc(epoch_micros) <> epoch_micros THEN
    RAISE EXCEPTION 'timestamp must be a nonnegative whole UTC microsecond';
  END IF;
  RETURN epoch_micros::bigint;
END;
$$;


--
-- Name: ck_unix_seconds(timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_unix_seconds(value timestamp with time zone) RETURNS bigint
    LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
    AS $$
DECLARE
  epoch_seconds numeric;
BEGIN
  epoch_seconds := extract(epoch FROM value)::numeric;
  IF epoch_seconds < 0 OR trunc(epoch_seconds) <> epoch_seconds THEN
    RAISE EXCEPTION 'timestamp must be a nonnegative whole UTC second';
  END IF;
  RETURN epoch_seconds::bigint;
END;
$$;


--
-- Name: ck_claim_artifact_source_ref_validation(text, uuid, uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_claim_artifact_source_ref_validation(wanted_kind text, wanted_owner uuid, wanted_project uuid) RETURNS boolean
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public'
    AS $$
DECLARE
  queue_oid oid;
BEGIN
  IF pg_catalog.pg_trigger_depth() = 0 THEN
    RAISE EXCEPTION 'artifact source validation claims are trigger-only'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  queue_oid := pg_catalog.to_regclass('pg_temp.ck_artifact_source_ref_validation_queue');
  IF queue_oid IS NULL THEN RETURN false; END IF;
  IF NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_roles AS owner ON owner.oid = relation.relowner
    WHERE relation.oid = queue_oid
      AND relation.relpersistence = 't'
      AND owner.rolname = CURRENT_USER
  ) THEN
    RAISE EXCEPTION 'artifact source validation queue has an invalid owner'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  DELETE FROM pg_temp.ck_artifact_source_ref_validation_queue AS pending
  WHERE pending.transaction_id = pg_catalog.pg_current_xact_id()
    AND pending.owner_id = wanted_owner
    AND pending.project_id = wanted_project
    AND pending.validation_kind = wanted_kind;
  RETURN FOUND;
END;
$$;


--
-- Name: ck_mark_artifact_source_ref_validation(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_mark_artifact_source_ref_validation() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public'
    AS $$
DECLARE
  wanted_owner uuid := CASE WHEN TG_OP = 'DELETE' THEN OLD.owner_id ELSE NEW.owner_id END;
  wanted_project uuid := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  queue_oid oid;
BEGIN
  queue_oid := pg_catalog.to_regclass('pg_temp.ck_artifact_source_ref_validation_queue');
  IF queue_oid IS NULL THEN
    CREATE TEMPORARY TABLE ck_artifact_source_ref_validation_queue (
      transaction_id xid8 NOT NULL,
      owner_id uuid NOT NULL,
      project_id uuid NOT NULL,
      validation_kind text NOT NULL CHECK (
        validation_kind IN ('currentness', 'owner_ref_set')
      ),
      PRIMARY KEY (transaction_id, owner_id, project_id, validation_kind)
    ) ON COMMIT DELETE ROWS;
    queue_oid := pg_catalog.to_regclass('pg_temp.ck_artifact_source_ref_validation_queue');
  END IF;
  IF NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_roles AS owner ON owner.oid = relation.relowner
    WHERE relation.oid = queue_oid
      AND relation.relpersistence = 't'
      AND owner.rolname = CURRENT_USER
  ) THEN
    RAISE EXCEPTION 'artifact source validation queue has an invalid owner'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  -- В транзакции один владелец проверяется один раз, независимо от числа его ссылок.
  INSERT INTO pg_temp.ck_artifact_source_ref_validation_queue (
    transaction_id, owner_id, project_id, validation_kind
  )
  SELECT pg_catalog.pg_current_xact_id(), wanted_owner, wanted_project, kind
  FROM (VALUES ('currentness'::text), ('owner_ref_set'::text)) AS validation(kind)
  ON CONFLICT DO NOTHING;
  RETURN NULL;
END;
$$;


--
-- Name: ck_validate_active_artifact_source_owner_currentness(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_active_artifact_source_owner_currentness() RETURNS trigger
    LANGUAGE plpgsql
    -- Deferred-проверки могут планироваться на пустой таблице и обязаны использовать owner-индекс.
    -- Настройка действует только внутри этой функции и не меняет глобальный planner state.
    SET enable_seqscan TO 'off'
    AS $$
DECLARE
  wanted_owner uuid;
  wanted_project uuid;
  owner_row public.artifact_source_owners%ROWTYPE;
  exact_count integer;
  locked_chapter_count integer;
BEGIN
  IF TG_TABLE_NAME = 'artifact_source_owners' THEN
    wanted_owner := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
    wanted_project := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  ELSE
    wanted_owner := CASE WHEN TG_OP = 'DELETE' THEN OLD.owner_id ELSE NEW.owner_id END;
    wanted_project := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  END IF;
  IF TG_TABLE_NAME = 'artifact_source_refs'
     AND NOT public.ck_claim_artifact_source_ref_validation(
       'currentness', wanted_owner, wanted_project
     ) THEN
    RETURN NULL;
  END IF;
  SELECT * INTO owner_row
  FROM public.artifact_source_owners AS owner
  WHERE owner.id = wanted_owner AND owner.project_id = wanted_project;
  IF NOT FOUND OR owner_row.read_state <> 'active' THEN
    RETURN NULL;
  END IF;

  PERFORM project.id FROM public.projects AS project
  WHERE project.id = owner_row.project_id FOR SHARE OF project;
  PERFORM book.id
  FROM public.books AS book
  JOIN (
    SELECT DISTINCT ref.book_id FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id AND ref.book_id IS NOT NULL
  ) AS target ON target.book_id = book.id
  WHERE book.project_id = owner_row.project_id ORDER BY book.id FOR SHARE OF book;
  PERFORM chapter.id
  FROM public.chapters AS chapter
  JOIN (
    SELECT DISTINCT ref.chapter_id FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id AND ref.chapter_id IS NOT NULL
  ) AS target ON target.chapter_id = chapter.id
  WHERE chapter.project_id = owner_row.project_id ORDER BY chapter.id FOR SHARE OF chapter;
  PERFORM chat.id
  FROM public.chat_sessions AS chat
  JOIN (
    SELECT DISTINCT ref.chat_session_id FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id AND ref.chat_session_id IS NOT NULL
  ) AS target ON target.chat_session_id = chat.id
  WHERE chat.project_id = owner_row.project_id ORDER BY chat.id FOR SHARE OF chat;

  SELECT count(*) INTO exact_count
  FROM public.artifact_source_refs AS ref
  WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
    AND ref.text_identity_state = 'text_derived_current';
  SELECT count(*) INTO locked_chapter_count
  FROM (
    SELECT document.chapter_id
    FROM public.chapter_text_search_rows AS document
    JOIN public.artifact_source_refs AS ref
      ON ref.project_id = document.project_id
     AND ref.source_view = document.source_view
     AND ref.book_id = document.book_id
     AND ref.chapter_id = document.chapter_id
     AND ref.source_revision = document.source_revision
     AND ref.locator_source_generation = document.locator_source_generation
     AND ref.content_hash = document.chapter_content_hash
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
      AND ref.ref_kind = 'chapter'
      AND ref.text_identity_state = 'text_derived_current'
    ORDER BY document.chapter_id
    FOR SHARE OF document
  ) AS locked;
  IF exact_count <> locked_chapter_count THEN
    RAISE EXCEPTION 'active V2 artifact source owner contains a missing exact source reference'
      USING ERRCODE = 'check_violation';
  END IF;

  IF NOT public.ck_artifact_source_owner_is_current(
    owner_row.id, owner_row.project_id, owner_row.artifact_kind, owner_row.artifact_id
  ) THEN
    RAISE EXCEPTION 'active artifact source owner contains a stale lifecycle reference'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END;
$$;


--
-- Name: ck_validate_audit_payload_row(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_audit_payload_row() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.audit_payload_state IN ('source_scrubbed', 'project_scrubbed') THEN
    IF NEW.audit_scope_kind <> 'scrubbed'
       OR NEW.resource_type <> 'deleted'
       OR NEW.resource_id <> '00000000-0000-0000-0000-000000000000'
       OR NEW.actor_user_id IS NOT NULL
       OR NEW.data_json <> '{}'::jsonb
       OR NEW.audit_payload_schema <> 'empty_v1'
       OR (NEW.audit_payload_state = 'source_scrubbed' AND NEW.project_id IS NULL)
       OR (NEW.audit_payload_state = 'project_scrubbed' AND NEW.project_id IS NOT NULL) THEN
      RAISE EXCEPTION 'audit scrub sentinel is malformed'
        USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.operation_id <> 'getJobResult' THEN
      RAISE EXCEPTION 'unknown audit tuple survived scrub'
        USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;

	IF NOT ck_audit_payload_shape_valid(
	  NEW.operation_id, NEW.resource_type, NEW.outcome,
	  NEW.audit_payload_schema, NEW.data_json
	) THEN
    RAISE EXCEPTION 'audit payload is outside the closed registry'
		USING ERRCODE = 'check_violation';
	END IF;

	IF NEW.audit_scope_kind <> 'book' OR NOT EXISTS (
	  SELECT 1
	  FROM projects project
	  JOIN books book
	    ON book.project_id = project.id
	  WHERE project.id = NEW.project_id
	    AND project.lifecycle_generation = NEW.project_generation
	    AND project.deleted_at IS NULL
	    AND book.id = NEW.book_id
	    AND book.lifecycle_generation = NEW.book_generation
	    AND book.deleted_at IS NULL
	) THEN
	  RAISE EXCEPTION 'audit payload source scope does not match current lifecycle'
	    USING ERRCODE = 'check_violation';
	END IF;

  IF NEW.operation_id = 'getJobResult' THEN
    IF NEW.audit_scope_kind <> 'book'
       OR NEW.book_id::text <> NEW.data_json->>'bookId'
       OR NOT EXISTS (
         SELECT 1 FROM jobs job
		 WHERE job.id::text = NEW.resource_id
		   AND job.project_id = NEW.project_id
		   AND job.kind = 'export'
		   AND job.status = 'succeeded'
		   AND job.result_json->>'type' = 'export'
		   AND job.result_json->>'bookId' = NEW.data_json->>'bookId'
		   AND job.result_json->>'format' = NEW.data_json->>'format'
		 ) THEN
      RAISE EXCEPTION 'export audit payload does not match relational state'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_validate_canon_graph_contribution_slot(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_canon_graph_contribution_slot() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  row_data jsonb;
  old_data jsonb;
  scope_row record;
BEGIN
  IF TG_OP <> 'DELETE' THEN
    row_data := pg_catalog.to_jsonb(NEW);
  END IF;
  IF TG_OP <> 'INSERT' THEN
    old_data := pg_catalog.to_jsonb(OLD);
  END IF;

  FOR scope_row IN
    SELECT DISTINCT
      NULLIF(scope_data->>'project_id', '')::uuid AS project_id,
      NULLIF(scope_data->>'source_view', '') AS source_view,
      NULLIF(scope_data->>'book_id', '')::uuid AS book_id
    FROM pg_catalog.unnest(ARRAY[row_data, old_data]) AS scopes(scope_data)
    WHERE scope_data IS NOT NULL
  LOOP
    IF EXISTS (
      SELECT 1
      FROM public.canon_graph_contributions AS contribution
      WHERE contribution.project_id = scope_row.project_id
        AND contribution.source_view = scope_row.source_view
        AND contribution.book_id = scope_row.book_id
        AND NOT EXISTS (
          SELECT 1
          FROM public.canon_graph_book_slots AS slot
          WHERE slot.project_id = contribution.project_id
            AND slot.source_view = contribution.source_view
            AND slot.book_id = contribution.book_id
            AND (
              slot.active_contribution_id = contribution.id
              OR slot.previous_contribution_id = contribution.id
            )
        )
    ) THEN
      RAISE EXCEPTION 'canon graph contribution is not held by its book slot'
        USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF EXISTS (
      SELECT 1
      FROM public.canon_graph_book_slots AS slot
      JOIN public.canon_graph_contributions AS contribution
        ON contribution.project_id = slot.project_id
       AND contribution.source_view = slot.source_view
       AND contribution.book_id = slot.book_id
       AND contribution.id = slot.active_contribution_id
      WHERE slot.project_id = scope_row.project_id
        AND slot.source_view = scope_row.source_view
        AND slot.book_id = scope_row.book_id
        AND contribution.book_manifest IS DISTINCT FROM slot.target_manifest
    ) THEN
      RAISE EXCEPTION 'active canon graph contribution manifest differs from slot target'
        USING ERRCODE = 'check_violation';
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$;


--
-- Name: ck_validate_canon_graph_overlay_link(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_canon_graph_overlay_link() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  row_data jsonb;
  old_data jsonb;
  scope_row record;
BEGIN
  IF TG_OP <> 'DELETE' THEN
    row_data := pg_catalog.to_jsonb(NEW);
  END IF;
  IF TG_OP <> 'INSERT' THEN
    old_data := pg_catalog.to_jsonb(OLD);
  END IF;

  FOR scope_row IN
    SELECT DISTINCT
      NULLIF(scope_data->>'project_id', '')::uuid AS project_id,
      NULLIF(scope_data->>'source_view', '') AS source_view,
      NULLIF(scope_data->>'book_id', '')::uuid AS book_id
    FROM pg_catalog.unnest(ARRAY[row_data, old_data]) AS scopes(scope_data)
    WHERE scope_data IS NOT NULL
  LOOP
    IF EXISTS (
      SELECT 1
      FROM public.canon_graph_overlays AS overlay
      WHERE overlay.project_id = scope_row.project_id
        AND overlay.source_view = scope_row.source_view
        AND overlay.book_id = scope_row.book_id
        AND (
          NOT EXISTS (
            SELECT 1
            FROM public.canon_graph_book_slots AS slot
            JOIN public.canon_graph_rows AS graph_row
              ON graph_row.project_id = slot.project_id
             AND graph_row.source_view = slot.source_view
             AND graph_row.book_id = slot.book_id
             AND graph_row.contribution_id IN (
               slot.active_contribution_id, slot.previous_contribution_id
             )
             AND graph_row.stable_row_id = overlay.target_stable_row_id
             AND graph_row.effective_fingerprint = overlay.target_fingerprint
            WHERE slot.project_id = overlay.project_id
              AND slot.source_view = overlay.source_view
              AND slot.book_id = overlay.book_id
          )
          OR (
            overlay.kind = 'replacement'
            AND NOT EXISTS (
              SELECT 1
              FROM public.canon_author_rows AS author_row
              WHERE author_row.project_id = overlay.project_id
                AND author_row.source_view = overlay.source_view
                AND author_row.anchor_book_id = overlay.book_id
                AND author_row.id = overlay.replacement_author_stable_row_id
            )
          )
        )
    ) THEN
      RAISE EXCEPTION 'canon graph overlay has no exact active or previous target'
        USING ERRCODE = 'foreign_key_violation';
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$;


--
-- Name: ck_validate_canon_graph_row_evidence(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_canon_graph_row_evidence() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  row_data jsonb;
  old_data jsonb;
  target record;
  evidence_count integer;
  ordinal_count integer;
  first_ordinal integer;
  last_ordinal integer;
  ref_count integer;
  invalid_count integer;
BEGIN
  IF TG_OP <> 'DELETE' THEN
    row_data := pg_catalog.to_jsonb(NEW);
  END IF;
  IF TG_OP <> 'INSERT' THEN
    old_data := pg_catalog.to_jsonb(OLD);
  END IF;

  FOR target IN
    SELECT DISTINCT
      NULLIF(target_data->>'project_id', '')::uuid AS project_id,
      NULLIF(target_data->>'source_view', '') AS source_view,
      NULLIF(target_data->>'book_id', '')::uuid AS book_id,
      NULLIF(target_data->>'contribution_id', '')::uuid AS contribution_id,
      NULLIF(target_data->>'stable_row_id', '')::uuid AS stable_row_id
    FROM pg_catalog.unnest(ARRAY[row_data, old_data]) AS targets(target_data)
    WHERE target_data IS NOT NULL
  LOOP
    IF NOT EXISTS (
      SELECT 1
      FROM public.canon_graph_rows AS graph_row
      WHERE graph_row.project_id = target.project_id
        AND graph_row.source_view = target.source_view
        AND graph_row.book_id = target.book_id
        AND graph_row.contribution_id = target.contribution_id
        AND graph_row.stable_row_id = target.stable_row_id
    ) THEN
      CONTINUE;
    END IF;

    SELECT
      pg_catalog.count(*),
      pg_catalog.count(DISTINCT evidence.evidence_ordinal),
      pg_catalog.min(evidence.evidence_ordinal),
      pg_catalog.max(evidence.evidence_ordinal),
      pg_catalog.count(DISTINCT evidence.artifact_source_ref_id),
      pg_catalog.count(*) FILTER (
        WHERE source_ref.id IS NULL
          OR source_ref.owner_id IS DISTINCT FROM contribution.artifact_source_owner_id
          OR source_ref.project_id IS DISTINCT FROM target.project_id
          OR source_ref.source_view IS DISTINCT FROM target.source_view
          OR source_ref.book_id IS DISTINCT FROM target.book_id
          OR source_ref.ref_kind IS DISTINCT FROM 'chapter'
          OR source_ref.text_identity_state IS DISTINCT FROM 'text_derived_current'
          OR source_ref.source_independent
          OR source_ref.chat_session_id IS NOT NULL
          OR source_ref.chat_lifecycle_epoch IS NOT NULL
      )
    INTO
      evidence_count, ordinal_count, first_ordinal, last_ordinal,
      ref_count, invalid_count
    FROM public.canon_graph_row_evidence AS evidence
    JOIN public.canon_graph_contributions AS contribution
      ON contribution.project_id = evidence.project_id
     AND contribution.source_view = evidence.source_view
     AND contribution.book_id = evidence.book_id
     AND contribution.id = evidence.contribution_id
    LEFT JOIN public.artifact_source_refs AS source_ref
      ON source_ref.id = evidence.artifact_source_ref_id
     AND source_ref.project_id = evidence.project_id
    WHERE evidence.project_id = target.project_id
      AND evidence.source_view = target.source_view
      AND evidence.book_id = target.book_id
      AND evidence.contribution_id = target.contribution_id
      AND evidence.stable_row_id = target.stable_row_id;

    IF evidence_count NOT BETWEEN 1 AND 8
       OR ordinal_count <> evidence_count
       OR first_ordinal <> 0
       OR last_ordinal <> evidence_count - 1
       OR ref_count <> evidence_count
       OR invalid_count <> 0 THEN
      RAISE EXCEPTION 'canon graph row has an invalid exact Evidence set'
        USING ERRCODE = 'check_violation';
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$;


--
-- Name: ck_validate_deletion_receipt_retention(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_deletion_receipt_retention() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  current_receipt deletion_completion_receipts%ROWTYPE;
  tombstone deletion_tombstones%ROWTYPE;
BEGIN
  SELECT * INTO current_receipt
  FROM deletion_completion_receipts
  WHERE tombstone_id = NEW.tombstone_id AND project_id = NEW.project_id;
  IF NOT FOUND THEN
    RETURN NEW;
  END IF;
  SELECT * INTO tombstone
  FROM deletion_tombstones
  WHERE id = current_receipt.tombstone_id
    AND project_id = current_receipt.project_id;
  IF NOT FOUND
     OR tombstone.status <> 'purged'
     OR tombstone.audit_expires_at IS DISTINCT FROM current_receipt.audit_expires_at THEN
    RAISE EXCEPTION 'completion receipt is not pinned to terminal typed proof retention'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_validate_deletion_tombstone_retention(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_deletion_tombstone_retention() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public'
    AS $$
DECLARE
  current_tombstone deletion_tombstones%ROWTYPE;
  matching_expiry timestamptz;
BEGIN
  SELECT * INTO current_tombstone
  FROM deletion_tombstones
  WHERE id = NEW.id AND project_id = NEW.project_id;
  IF NOT FOUND OR current_tombstone.status <> 'purged' THEN
    RETURN NEW;
  END IF;

  IF current_tombstone.purge_mode = 'derived_generation' THEN
    SELECT hard.purged_at + interval '30 days'
    INTO matching_expiry
    FROM deletion_tombstones AS hard
    WHERE hard.status = 'purged'
      AND ck_source_hard_purge_contains_derived(hard, current_tombstone)
    ORDER BY hard.purged_at, uuid_send(hard.id)
    LIMIT 1;
    IF current_tombstone.audit_expires_at IS DISTINCT FROM matching_expiry THEN
      RAISE EXCEPTION 'derived proof expiry does not match its first source hard purge'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF current_tombstone.purge_mode = 'source_hard_purge'
     AND EXISTS (
       SELECT 1
       FROM deletion_tombstones AS derived
       WHERE derived.status = 'purged'
         AND ck_source_hard_purge_contains_derived(current_tombstone, derived)
         AND derived.audit_expires_at IS DISTINCT FROM (
           SELECT hard.purged_at + interval '30 days'
           FROM deletion_tombstones AS hard
           WHERE hard.status = 'purged'
             AND ck_source_hard_purge_contains_derived(hard, derived)
           ORDER BY hard.purged_at, uuid_send(hard.id)
           LIMIT 1
         )
     ) THEN
    RAISE EXCEPTION 'source hard purge did not pin every retained derived proof expiry'
      USING ERRCODE = 'check_violation';
  END IF;

  IF NOT EXISTS (
    SELECT 1
    FROM deletion_completion_receipts AS receipt
    WHERE receipt.tombstone_id = current_tombstone.id
      AND receipt.project_id = current_tombstone.project_id
  ) THEN
    RAISE EXCEPTION 'purged tombstone lacks its completion receipt'
      USING ERRCODE = 'check_violation';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM deletion_completion_receipts AS receipt
    WHERE receipt.tombstone_id = current_tombstone.id
      AND receipt.project_id = current_tombstone.project_id
      AND receipt.audit_expires_at IS DISTINCT FROM current_tombstone.audit_expires_at
  ) THEN
    RAISE EXCEPTION 'completion receipt expiry differs from its typed tombstone'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_validate_import_staging_shape(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_import_staging_shape() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  wanted_project uuid;
  wanted_job uuid;
  wanted_storage_key text;
BEGIN
  IF TG_TABLE_NAME = 'import_uploads' THEN
    wanted_project := COALESCE(NEW.project_id, OLD.project_id);
    wanted_storage_key := COALESCE(NEW.storage_key, OLD.storage_key);
    SELECT r.job_id INTO wanted_job
    FROM import_job_requests r
    WHERE r.project_id = wanted_project
      AND r.storage_key = wanted_storage_key;
  ELSIF TG_TABLE_NAME = 'import_queue_items' THEN
    wanted_project := COALESCE(NEW.project_id, OLD.project_id);
    wanted_storage_key := COALESCE(NEW.storage_key, OLD.storage_key);
  ELSE
    wanted_project := COALESCE(NEW.project_id, OLD.project_id);
    wanted_job := COALESCE(NEW.job_id, OLD.job_id);
    wanted_storage_key := COALESCE(NEW.storage_key, OLD.storage_key);
  END IF;

  IF EXISTS (
    SELECT 1 FROM import_uploads u
    WHERE u.project_id = wanted_project
      AND u.storage_key = wanted_storage_key
      AND u.content IS NOT NULL
  ) AND NOT EXISTS (
    SELECT 1
    FROM import_uploads u
    JOIN import_job_requests r
      ON r.storage_key = u.storage_key AND r.project_id = u.project_id
    JOIN jobs j ON j.id = r.job_id AND j.project_id = r.project_id
    JOIN artifact_source_owners upload_owner
      ON upload_owner.id = u.artifact_source_owner_id
      AND upload_owner.project_id = u.project_id
      AND upload_owner.read_state = 'active'
    JOIN artifact_source_owners request_owner
      ON request_owner.id = r.artifact_source_owner_id
      AND request_owner.project_id = r.project_id
      AND request_owner.read_state = 'active'
    JOIN artifact_source_owners job_owner
      ON job_owner.id = j.artifact_source_owner_id
      AND job_owner.project_id = j.project_id
      AND job_owner.read_state = 'active'
    WHERE u.project_id = wanted_project
      AND u.storage_key = wanted_storage_key
      AND r.job_id = wanted_job
      AND j.status IN ('queued', 'running')
      AND (
        SELECT count(*) FROM artifact_source_refs ref
        WHERE ref.owner_id = upload_owner.id
          AND ref.project_id = upload_owner.project_id
          AND ref.ref_kind = 'scope'
          AND ref.scope_kind = 'project'
      ) = 1
  ) AND NOT EXISTS (
    SELECT 1
    FROM import_uploads u
    JOIN import_queue_items queue_item
      ON queue_item.storage_key = u.storage_key
     AND queue_item.project_id = u.project_id
    JOIN import_queues queue
      ON queue.id = queue_item.queue_id
     AND queue.project_id = queue_item.project_id
    JOIN artifact_source_owners upload_owner
      ON upload_owner.id = u.artifact_source_owner_id
     AND upload_owner.project_id = u.project_id
     AND upload_owner.read_state = 'active'
    WHERE u.project_id = wanted_project
      AND u.storage_key = wanted_storage_key
      AND queue.status IN ('uploading', 'draft', 'running', 'failed')
  ) THEN
    RAISE EXCEPTION 'import content lacks its exact active owner/request/job chain'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$;


--
-- Name: ck_validate_owner_artifact_link(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_owner_artifact_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  present boolean := false;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM public.artifact_source_owners AS current_owner
    WHERE current_owner.id = NEW.id
      AND current_owner.project_id = NEW.project_id
      AND current_owner.artifact_kind = NEW.artifact_kind
      AND current_owner.artifact_id = NEW.artifact_id
      AND (current_owner.read_state = 'active' OR current_owner.fenced_at IS NULL)
  ) THEN
    RETURN NEW;
  END IF;

  IF NEW.artifact_kind = 'canon_book_enrichment_input' THEN
    SELECT EXISTS (SELECT 1 FROM public.canon_book_enrichment_inputs a WHERE a.job_id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'job' THEN
    SELECT EXISTS (SELECT 1 FROM public.jobs a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'import_upload' THEN
    SELECT EXISTS (SELECT 1 FROM public.import_uploads a WHERE a.artifact_id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'import_job_request' THEN
    SELECT EXISTS (SELECT 1 FROM public.import_job_requests a WHERE a.job_id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'chat_turn' THEN
    SELECT EXISTS (SELECT 1 FROM public.chat_turns a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id AND a.source_view IS NOT DISTINCT FROM NEW.source_view) INTO present;
  ELSIF NEW.artifact_kind = 'writer_proposal' THEN
    SELECT EXISTS (SELECT 1 FROM public.writer_proposals a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id AND a.source_view IS NOT DISTINCT FROM NEW.source_view) INTO present;
  ELSIF NEW.artifact_kind = 'export_artifact' THEN
    SELECT EXISTS (SELECT 1 FROM public.export_artifacts a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'runtime_session' THEN
    SELECT EXISTS (SELECT 1 FROM public.runtime_sessions a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'chapter_summary' THEN
    SELECT EXISTS (
      SELECT 1 FROM public.chapter_summaries a
      WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id
        AND a.artifact_source_owner_id = NEW.id
        AND a.source_view IS NOT DISTINCT FROM NEW.source_view
    ) INTO present;
  ELSIF NEW.artifact_kind = 'canon_author_row' THEN
    SELECT EXISTS (
      SELECT 1
      FROM public.canon_author_rows AS author_row
      WHERE author_row.id = NEW.artifact_id
        AND author_row.project_id = NEW.project_id
        AND author_row.artifact_source_owner_id = NEW.id
        AND author_row.source_view IS NOT DISTINCT FROM NEW.source_view
    ) INTO present;
    IF present AND EXISTS (
      SELECT 1
      FROM public.canon_author_rows AS author_row
      JOIN public.artifact_source_refs AS ref
        ON ref.owner_id = NEW.id AND ref.project_id = NEW.project_id
      WHERE author_row.id = NEW.artifact_id
        AND author_row.project_id = NEW.project_id
        AND ref.book_id IS DISTINCT FROM author_row.anchor_book_id
    ) THEN
      RAISE EXCEPTION 'canon author row refs must share its anchor book'
        USING ERRCODE = 'foreign_key_violation';
    END IF;
  ELSIF NEW.artifact_kind = 'canon_graph_contribution' THEN
    SELECT EXISTS (
      SELECT 1
      FROM public.canon_graph_contributions AS contribution
      WHERE contribution.id = NEW.artifact_id
        AND contribution.project_id = NEW.project_id
        AND contribution.source_view IS NOT DISTINCT FROM NEW.source_view
        AND contribution.artifact_source_owner_id = NEW.id
    ) INTO present;
  ELSIF NEW.artifact_kind = 'canon_correction_proposal' THEN
    SELECT EXISTS (SELECT 1 FROM public.canon_correction_proposals a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id AND a.source_view IS NOT DISTINCT FROM NEW.source_view) INTO present;
  ELSIF NEW.artifact_kind = 'agent_workflow_input' THEN
    SELECT EXISTS (SELECT 1 FROM public.agent_workflow_inputs a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id AND a.source_view IS NOT DISTINCT FROM NEW.source_view) INTO present;
  END IF;

  IF NOT present THEN
    RAISE EXCEPTION 'artifact owner has no exact same-project artifact row'
      USING ERRCODE = 'foreign_key_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_validate_owner_ref_set(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_owner_ref_set() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public'
    AS $$
DECLARE
  wanted_owner uuid;
  wanted_project uuid;
  owner_row public.artifact_source_owners%ROWTYPE;
  actual_count integer;
  chapter_count integer;
  ref_projections bytea[];
  expected_manifest_hash bytea;
  expected_chat_id uuid;
  expected_chat_epoch bigint;
  expected_chat_deleted_at timestamp with time zone;
  v1_scope_only_product_owner boolean;
BEGIN
  IF TG_TABLE_NAME = 'artifact_source_owners' THEN
    wanted_owner := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
    wanted_project := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  ELSE
    wanted_owner := CASE WHEN TG_OP = 'DELETE' THEN OLD.owner_id ELSE NEW.owner_id END;
    wanted_project := CASE WHEN TG_OP = 'DELETE' THEN OLD.project_id ELSE NEW.project_id END;
  END IF;

  IF TG_TABLE_NAME = 'artifact_source_refs'
     AND NOT public.ck_claim_artifact_source_ref_validation(
       'owner_ref_set', wanted_owner, wanted_project
     ) THEN
    RETURN NULL;
  END IF;

  SELECT * INTO owner_row
  FROM public.artifact_source_owners AS owner
  WHERE owner.id = wanted_owner AND owner.project_id = wanted_project;
  IF NOT FOUND THEN RETURN NULL; END IF;

  SELECT count(*), count(*) FILTER (WHERE ref_kind = 'chapter')
  INTO actual_count, chapter_count
  FROM public.artifact_source_refs AS ref
  WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id;

  v1_scope_only_product_owner := CASE
    WHEN owner_row.artifact_kind = 'chat_turn' THEN EXISTS (
      SELECT 1 FROM public.chat_turns AS turn_row
      WHERE turn_row.id = owner_row.artifact_id
        AND turn_row.project_id = owner_row.project_id
        AND turn_row.artifact_source_owner_id = owner_row.id
    )
    WHEN owner_row.artifact_kind = 'agent_workflow_input' THEN EXISTS (
      SELECT 1 FROM public.agent_workflow_inputs AS input
      WHERE input.id = owner_row.artifact_id
        AND input.project_id = owner_row.project_id
        AND input.artifact_source_owner_id = owner_row.id
        AND (
          input.job_kind = 'research_chat_turn'
          OR (
            input.job_kind = 'writer_agent'
            AND input.body_json ->> 'mode' = 'expand_outline'
          )
        )
    )
    WHEN owner_row.artifact_kind = 'writer_proposal' THEN EXISTS (
      SELECT 1
      FROM public.writer_proposals AS proposal
      JOIN public.agent_workflow_inputs AS input
        ON input.id = proposal.workflow_input_id
       AND input.project_id = proposal.project_id
       AND input.job_id = proposal.job_id
       AND input.job_kind = proposal.job_kind
       AND input.target_id = proposal.id
       AND input.source_view = proposal.source_view
      WHERE proposal.id = owner_row.artifact_id
        AND proposal.project_id = owner_row.project_id
        AND proposal.artifact_source_owner_id = owner_row.id
        AND proposal.mode = 'expand_outline'
        AND proposal.job_kind = 'writer_agent'
    )
    ELSE false
  END;

  IF owner_row.artifact_kind IN (
    'chat_turn', 'writer_proposal',
    'canon_correction_proposal', 'agent_workflow_input'
  ) THEN
    IF owner_row.source_view IS NULL
       OR owner_row.manifest_version NOT IN (1, 2)
       OR (
         owner_row.manifest_version = 1
         AND (
           owner_row.source_identity_state <> 'scope_only'
           OR NOT v1_scope_only_product_owner
           OR chapter_count <> 0
         )
       ) THEN
      RAISE EXCEPTION 'agent workflow product owner manifest is invalid'
        USING ERRCODE = 'check_violation';
    END IF;
    IF actual_count < 1 OR actual_count <> owner_row.ref_count THEN
      RAISE EXCEPTION 'agent workflow product owner ref_count is invalid'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1 FROM public.artifact_source_refs AS ref
      WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
        AND (
          ref.source_view IS DISTINCT FROM owner_row.source_view
          OR ref.source_independent
        )
    ) THEN
      RAISE EXCEPTION 'agent workflow product refs must share its exact source view'
        USING ERRCODE = 'check_violation';
    END IF;

    IF owner_row.artifact_kind = 'chat_turn' THEN
      SELECT chat.id, chat.lifecycle_epoch, chat.deleted_at
      INTO expected_chat_id, expected_chat_epoch, expected_chat_deleted_at
      FROM public.chat_turns AS turn_row
      JOIN public.chat_sessions AS chat
        ON chat.id = turn_row.chat_id AND chat.project_id = turn_row.project_id
      WHERE turn_row.id = owner_row.artifact_id
        AND turn_row.project_id = owner_row.project_id;
    ELSIF owner_row.artifact_kind = 'agent_workflow_input' THEN
      SELECT chat.id, chat.lifecycle_epoch, chat.deleted_at
      INTO expected_chat_id, expected_chat_epoch, expected_chat_deleted_at
      FROM public.agent_workflow_inputs AS input
      JOIN public.chat_turns AS turn_row
        ON input.job_kind = 'research_chat_turn'
       AND turn_row.id = input.target_id
       AND turn_row.project_id = input.project_id
       AND turn_row.job_id = input.job_id
      JOIN public.chat_sessions AS chat
        ON chat.id = turn_row.chat_id AND chat.project_id = turn_row.project_id
      WHERE input.id = owner_row.artifact_id
        AND input.project_id = owner_row.project_id;
    END IF;
    IF owner_row.read_state = 'chat_deleted'
       AND expected_chat_epoch > 1
       AND expected_chat_deleted_at = owner_row.fenced_at THEN
      expected_chat_epoch := expected_chat_epoch - 1;
    END IF;
    IF expected_chat_id IS NOT NULL AND EXISTS (
      SELECT 1 FROM public.artifact_source_refs AS ref
      WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
        AND (
          ref.chat_session_id IS DISTINCT FROM expected_chat_id
          OR ref.chat_lifecycle_epoch IS DISTINCT FROM expected_chat_epoch
        )
    ) THEN
      RAISE EXCEPTION 'chat workflow refs must carry the exact chat lifecycle'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF owner_row.artifact_kind = 'canon_author_row' THEN
    IF owner_row.manifest_version <> 2 THEN
      RAISE EXCEPTION 'canon author row requires manifest V2'
        USING ERRCODE = 'check_violation';
    END IF;
    IF actual_count NOT BETWEEN 1 AND 8 THEN
      RAISE EXCEPTION 'canon author row requires one to eight refs'
        USING ERRCODE = 'check_violation';
    END IF;
    IF chapter_count <> actual_count THEN
      RAISE EXCEPTION 'canon author row requires exact chapter refs'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1 FROM public.artifact_source_refs AS ref
      WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
        AND (
          ref.text_identity_state IS DISTINCT FROM 'text_derived_current'
          OR ref.source_independent
        )
    ) THEN
      RAISE EXCEPTION 'canon author row requires current exact refs'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1 FROM public.artifact_source_refs AS ref
      WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
        AND (ref.chat_session_id IS NOT NULL OR ref.chat_lifecycle_epoch IS NOT NULL)
    ) THEN
      RAISE EXCEPTION 'canon author row refs cannot carry chat identity'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1 FROM public.artifact_source_refs AS ref
      WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
        AND (
          ref.project_id IS DISTINCT FROM owner_row.project_id
          OR ref.source_view IS DISTINCT FROM owner_row.source_view
        )
    ) THEN
      RAISE EXCEPTION 'canon author row refs must share project and source view'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1
      FROM public.canon_author_rows AS author_row
      JOIN public.artifact_source_refs AS ref
        ON ref.owner_id = owner_row.id
       AND ref.project_id = owner_row.project_id
      WHERE author_row.artifact_source_owner_id = owner_row.id
        AND author_row.project_id = owner_row.project_id
        AND ref.book_id IS DISTINCT FROM author_row.anchor_book_id
    ) THEN
      RAISE EXCEPTION 'canon author row refs must share its anchor book'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF owner_row.artifact_kind = 'canon_graph_contribution' THEN
    IF owner_row.manifest_version <> 2 THEN
      RAISE EXCEPTION 'canon graph contribution requires manifest V2'
        USING ERRCODE = 'check_violation';
    END IF;
    IF actual_count < 1 OR actual_count <> owner_row.ref_count THEN
      RAISE EXCEPTION 'canon graph contribution ref_count is invalid'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1
      FROM public.artifact_source_refs AS ref
      WHERE ref.owner_id = owner_row.id
        AND ref.project_id = owner_row.project_id
        AND (
          ref.source_view IS DISTINCT FROM owner_row.source_view
          OR ref.source_independent
          OR ref.chat_session_id IS NOT NULL
          OR ref.chat_lifecycle_epoch IS NOT NULL
          OR (
            ref.ref_kind = 'chapter'
            AND ref.text_identity_state IS DISTINCT FROM 'text_derived_current'
          )
        )
    ) THEN
      RAISE EXCEPTION 'canon graph contribution has an invalid source ref'
        USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (
      SELECT 1
      FROM public.canon_graph_contributions AS contribution
      JOIN public.artifact_source_refs AS ref
        ON ref.owner_id = owner_row.id
       AND ref.project_id = owner_row.project_id
       AND ref.ref_kind = 'scope'
       AND ref.scope_kind = 'book'
       AND ref.book_id = contribution.book_id
       AND ref.source_view = contribution.source_view
      WHERE contribution.id = owner_row.artifact_id
        AND contribution.project_id = owner_row.project_id
        AND contribution.source_view IS NOT DISTINCT FROM owner_row.source_view
        AND contribution.artifact_source_owner_id = owner_row.id
    ) THEN
      RAISE EXCEPTION 'canon graph contribution has no target-book scope'
        USING ERRCODE = 'check_violation';
    END IF;
    IF chapter_count < 1 THEN
      RAISE EXCEPTION 'canon graph contribution requires an exact chapter ref'
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
      SELECT 1
      FROM public.artifact_source_refs AS ref
      JOIN public.canon_graph_contributions AS contribution
        ON contribution.id = owner_row.artifact_id
       AND contribution.project_id = owner_row.project_id
       AND contribution.artifact_source_owner_id = owner_row.id
      WHERE ref.owner_id = owner_row.id
        AND ref.project_id = owner_row.project_id
        AND ref.ref_kind = 'scope'
        AND (
          ref.scope_kind IS DISTINCT FROM 'book'
          OR ref.book_id IS DISTINCT FROM contribution.book_id
        )
    ) THEN
      RAISE EXCEPTION 'canon graph contribution has a foreign scope ref'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF actual_count < 1 OR actual_count <> owner_row.ref_count THEN
    RAISE EXCEPTION 'owner ref_count does not match its exact reference set'
      USING ERRCODE = 'check_violation';
  END IF;
  IF EXISTS (
    SELECT 1 FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
      AND ref.source_view IS DISTINCT FROM owner_row.source_view
  ) THEN
    RAISE EXCEPTION 'owner and source refs have different source-view arms'
      USING ERRCODE = 'check_violation';
  END IF;

  IF owner_row.manifest_version = 1 THEN
    IF owner_row.artifact_kind IN (
         'canon_author_row', 'canon_graph_contribution'
       )
       OR owner_row.source_identity_state <> 'scope_only'
       OR EXISTS (
         SELECT 1 FROM public.artifact_source_refs AS ref
         WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
           AND ref.text_identity_state = 'text_derived_current'
       ) THEN
      RAISE EXCEPTION 'invalid V1 owner/ref identity state'
        USING ERRCODE = 'check_violation';
    END IF;
    IF chapter_count <> 0 THEN
      RAISE EXCEPTION 'scope-only owner contains an exact chapter reference'
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT array_agg(public.ck_artifact_source_ref_projection(
      ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
      ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
      ref.source_view, ref.source_independent,
      ref.chat_session_id, ref.chat_lifecycle_epoch
    )) INTO ref_projections
    FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id;
    expected_manifest_hash := public.ck_artifact_manifest_digest(
      owner_row.artifact_kind, owner_row.project_id, owner_row.artifact_id,
      owner_row.source_view, ref_projections
    );
  ELSIF owner_row.manifest_version = 2 THEN
    IF owner_row.source_identity_state <> 'text_derived_current'
       OR EXISTS (
         SELECT 1 FROM public.artifact_source_refs AS ref
         WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id
           AND ref.ref_kind = 'chapter'
           AND ref.text_identity_state IS DISTINCT FROM 'text_derived_current'
       ) THEN
      RAISE EXCEPTION 'invalid V2 owner/ref identity state'
        USING ERRCODE = 'check_violation';
    END IF;
    IF chapter_count = 0 THEN
      RAISE EXCEPTION 'V2 owner has no exact source reference'
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT array_agg(public.ck_artifact_source_ref_projection_v2(
      ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
      ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
      ref.source_revision, ref.source_view, ref.locator_source_generation,
      ref.content_hash, ref.text_identity_state,
      ref.source_independent, ref.chat_session_id, ref.chat_lifecycle_epoch
    )) INTO ref_projections
    FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id;
    expected_manifest_hash := public.ck_artifact_manifest_digest_v2(
      owner_row.artifact_kind, owner_row.project_id, owner_row.artifact_id,
      owner_row.source_view, ref_projections
    );
  ELSE
    RAISE EXCEPTION 'unsupported artifact manifest version'
      USING ERRCODE = 'check_violation';
  END IF;

  IF owner_row.manifest_hash IS DISTINCT FROM expected_manifest_hash THEN
    RAISE EXCEPTION 'owner manifest hash does not match its exact reference set'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END;
$$;


--
-- Name: ck_validate_participant_owner_link(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_participant_owner_link() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  row_data jsonb;
  old_data jsonb;
  wanted_owner_id uuid;
  wanted_project_id uuid;
  wanted_artifact_id uuid;
  old_owner_id uuid;
  old_project_id uuid;
  old_artifact_id uuid;
  expected_kind text;
BEGIN
  IF TG_OP = 'DELETE' THEN row_data := pg_catalog.to_jsonb(OLD); ELSE row_data := pg_catalog.to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' THEN old_data := pg_catalog.to_jsonb(OLD); END IF;
  wanted_owner_id := NULLIF(row_data->>'artifact_source_owner_id', '')::uuid;
  wanted_project_id := NULLIF(row_data->>'project_id', '')::uuid;
  IF TG_OP = 'UPDATE' THEN
    old_owner_id := NULLIF(old_data->>'artifact_source_owner_id', '')::uuid;
    old_project_id := NULLIF(old_data->>'project_id', '')::uuid;
  END IF;

  IF TG_TABLE_NAME = 'canon_book_enrichment_inputs' THEN
    wanted_artifact_id := NULLIF(row_data->>'job_id', '')::uuid; old_artifact_id := NULLIF(old_data->>'job_id', '')::uuid; expected_kind := 'canon_book_enrichment_input';
  ELSIF TG_TABLE_NAME = 'jobs' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'job';
  ELSIF TG_TABLE_NAME = 'import_uploads' THEN
    wanted_artifact_id := NULLIF(row_data->>'artifact_id', '')::uuid; old_artifact_id := NULLIF(old_data->>'artifact_id', '')::uuid; expected_kind := 'import_upload';
  ELSIF TG_TABLE_NAME = 'import_job_requests' THEN
    wanted_artifact_id := NULLIF(row_data->>'job_id', '')::uuid; old_artifact_id := NULLIF(old_data->>'job_id', '')::uuid; expected_kind := 'import_job_request';
  ELSIF TG_TABLE_NAME = 'chat_turns' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'chat_turn';
  ELSIF TG_TABLE_NAME = 'writer_proposals' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'writer_proposal';
  ELSIF TG_TABLE_NAME = 'export_artifacts' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'export_artifact';
  ELSIF TG_TABLE_NAME = 'runtime_sessions' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'runtime_session';
  ELSIF TG_TABLE_NAME = 'chapter_summaries' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'chapter_summary';
  ELSIF TG_TABLE_NAME = 'canon_author_rows' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'canon_author_row';
  ELSIF TG_TABLE_NAME = 'canon_graph_contributions' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'canon_graph_contribution';
  ELSIF TG_TABLE_NAME = 'canon_correction_proposals' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'canon_correction_proposal';
  ELSIF TG_TABLE_NAME = 'agent_workflow_inputs' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'agent_workflow_input';
  ELSE
    RAISE EXCEPTION 'unregistered artifact participant trigger';
  END IF;

  IF TG_OP = 'DELETE' THEN
    IF wanted_owner_id IS NOT NULL AND EXISTS (
      SELECT 1 FROM public.artifact_source_owners AS owner
      WHERE owner.id = wanted_owner_id AND owner.project_id = wanted_project_id
        AND owner.artifact_kind = expected_kind AND owner.artifact_id = wanted_artifact_id
        AND (owner.read_state = 'active' OR owner.fenced_at IS NULL)
    ) THEN
      RAISE EXCEPTION 'artifact participant delete leaves its owner orphaned'
        USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN OLD;
  END IF;

  IF wanted_owner_id IS NULL THEN
    IF (TG_TABLE_NAME IN ('jobs', 'import_job_requests') AND row_data->>'governance_payload_state' = 'source_deleted')
       OR (TG_TABLE_NAME = 'import_uploads' AND row_data->>'content' IS NULL) THEN
      RETURN NEW;
    END IF;
    RAISE EXCEPTION 'artifact participant requires a source owner'
      USING ERRCODE = 'check_violation';
  END IF;

  IF wanted_artifact_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM public.artifact_source_owners AS owner
    WHERE owner.id = wanted_owner_id AND owner.project_id = wanted_project_id
      AND owner.artifact_kind = expected_kind AND owner.artifact_id = wanted_artifact_id
      AND (
        (
          TG_TABLE_NAME NOT IN (
            'chapter_summaries', 'canon_author_rows', 'canon_graph_contributions',
            'chat_turns',
            'writer_proposals', 'canon_correction_proposals', 'agent_workflow_inputs'
          )
        )
        OR (
          TG_TABLE_NAME IN (
            'chat_turns',
            'writer_proposals', 'canon_correction_proposals', 'agent_workflow_inputs'
          )
          AND owner.source_view IS NOT DISTINCT FROM NULLIF(row_data->>'source_view', '')
        )
        OR (
          TG_TABLE_NAME = 'chapter_summaries'
          AND owner.source_view IS NOT DISTINCT FROM NULLIF(row_data->>'source_view', '')
          AND EXISTS (
            SELECT 1 FROM public.artifact_source_refs AS book_ref
            WHERE book_ref.owner_id = owner.id AND book_ref.project_id = owner.project_id
              AND book_ref.ref_kind = 'scope' AND book_ref.scope_kind = 'book'
              AND book_ref.book_id = NULLIF(row_data->>'book_id', '')::uuid
              AND book_ref.source_view = NULLIF(row_data->>'source_view', '')
          )
          AND EXISTS (
            SELECT 1 FROM public.artifact_source_refs AS chapter_ref
            WHERE chapter_ref.owner_id = owner.id AND chapter_ref.project_id = owner.project_id
              AND chapter_ref.ref_kind = 'chapter'
              AND chapter_ref.book_id = NULLIF(row_data->>'book_id', '')::uuid
              AND chapter_ref.chapter_id = NULLIF(row_data->>'chapter_id', '')::uuid
              AND chapter_ref.source_revision = NULLIF(row_data->>'source_revision', '')::bigint
              AND chapter_ref.source_view = NULLIF(row_data->>'source_view', '')
          )
        )
        OR (
          TG_TABLE_NAME = 'canon_author_rows'
          AND owner.source_view IS NOT DISTINCT FROM NULLIF(row_data->>'source_view', '')
          AND EXISTS (
            SELECT 1 FROM public.artifact_source_refs AS ref
            WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
              AND ref.book_id = NULLIF(row_data->>'anchor_book_id', '')::uuid
              AND ref.source_view = NULLIF(row_data->>'source_view', '')
          )
          AND NOT EXISTS (
            SELECT 1 FROM public.artifact_source_refs AS ref
            WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
              AND ref.book_id IS DISTINCT FROM NULLIF(row_data->>'anchor_book_id', '')::uuid
          )
        )
        OR (
          TG_TABLE_NAME = 'canon_graph_contributions'
          AND owner.source_view IS NOT DISTINCT FROM NULLIF(row_data->>'source_view', '')
          AND EXISTS (
            SELECT 1 FROM public.artifact_source_refs AS ref
            WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
              AND ref.ref_kind = 'scope'
              AND ref.scope_kind = 'book'
              AND ref.book_id = NULLIF(row_data->>'book_id', '')::uuid
              AND ref.source_view = NULLIF(row_data->>'source_view', '')
          )
        )
      )
  ) THEN
    RAISE EXCEPTION 'artifact participant has a missing or mismatched owner'
      USING ERRCODE = 'foreign_key_violation';
  END IF;

  IF TG_OP = 'UPDATE' AND old_owner_id IS NOT NULL
     AND (old_owner_id IS DISTINCT FROM wanted_owner_id OR old_project_id IS DISTINCT FROM wanted_project_id OR old_artifact_id IS DISTINCT FROM wanted_artifact_id)
     AND EXISTS (
       SELECT 1 FROM public.artifact_source_owners AS owner
       WHERE owner.id = old_owner_id AND owner.project_id = old_project_id
         AND owner.artifact_kind = expected_kind AND owner.artifact_id = old_artifact_id
         AND (owner.read_state = 'active' OR owner.fenced_at IS NULL)
     ) THEN
    RAISE EXCEPTION 'artifact participant update leaves its previous owner orphaned'
      USING ERRCODE = 'foreign_key_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$;


--
-- Name: ck_validate_postgresql_step_registry(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_postgresql_step_registry() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  step_count integer;
  final_step_name text;
BEGIN
  IF NEW.target <> 'postgresql' THEN
    RETURN NEW;
  END IF;
  SELECT count(*), max(step_name) FILTER (
    WHERE step_order = (
      SELECT max(last_step.step_order)
      FROM public.deletion_target_step_progress last_step
      WHERE last_step.tombstone_id = NEW.tombstone_id
        AND last_step.project_id = NEW.project_id
        AND last_step.target = 'postgresql'
    )
  ) INTO step_count, final_step_name
  FROM public.deletion_target_step_progress step
  WHERE step.tombstone_id = NEW.tombstone_id
    AND step.project_id = NEW.project_id
    AND step.target = 'postgresql';
  IF step_count = 0
     OR EXISTS (
       SELECT 1
       FROM generate_series(1, step_count) expected(step_order)
       LEFT JOIN public.deletion_target_step_progress actual
         ON actual.tombstone_id = NEW.tombstone_id
        AND actual.project_id = NEW.project_id
        AND actual.target = 'postgresql'
        AND actual.step_order = expected.step_order
       WHERE actual.step_order IS NULL
     )
     OR final_step_name <> (lpad(step_count::text, 3, '0') || ':deletion_target_tasks:finalize')
     OR EXISTS (
       SELECT 1
       FROM public.deletion_target_step_progress step
       WHERE step.tombstone_id = NEW.tombstone_id
         AND step.project_id = NEW.project_id
         AND step.target = 'postgresql'
         AND step.step_name LIKE '%:finalize'
         AND step.step_order <> step_count
     ) THEN
    RAISE EXCEPTION 'postgresql target has an incomplete step registry'
      USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.state = 'succeeded' AND EXISTS (
    SELECT 1 FROM public.deletion_target_step_progress step
    WHERE step.tombstone_id = NEW.tombstone_id
      AND step.project_id = NEW.project_id
      AND step.target = 'postgresql'
      AND step.state <> 'succeeded'
  ) THEN
    RAISE EXCEPTION 'postgresql target completed before every step'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_validate_source_hard_purge_predecessor_change(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_source_hard_purge_predecessor_change() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public'
    AS $$
DECLARE
  affected_row jsonb;
  affected_project_id uuid;
  affected_tombstone_id uuid;
  affected_tombstone deletion_tombstones%ROWTYPE;
  hard_tombstone RECORD;
BEGIN
  affected_row := CASE
    WHEN TG_OP = 'DELETE' THEN to_jsonb(OLD)
    ELSE to_jsonb(NEW)
  END;
  IF TG_TABLE_NAME = 'deletion_hard_purge_predecessors' THEN
    affected_project_id := (affected_row ->> 'project_id')::uuid;
    affected_tombstone_id := (affected_row ->> 'hard_tombstone_id')::uuid;
    PERFORM pg_advisory_xact_lock(hashtextextended(affected_project_id::text, 77341001));
    IF EXISTS (
      SELECT 1 FROM deletion_tombstones AS candidate
      WHERE candidate.id = affected_tombstone_id
        AND candidate.project_id = affected_project_id
    ) AND NOT ck_source_hard_purge_predecessor_set_is_valid(
      affected_tombstone_id, affected_project_id
    ) THEN
      RAISE EXCEPTION 'source hard purge has an incomplete or mismatched predecessor set'
        USING ERRCODE = 'check_violation';
    END IF;
    RETURN COALESCE(NEW, OLD);
  END IF;

  affected_project_id := (affected_row ->> 'project_id')::uuid;
  IF TG_TABLE_NAME = 'deletion_tombstones' THEN
    affected_tombstone_id := (affected_row ->> 'id')::uuid;
  ELSE
    affected_tombstone_id := (affected_row ->> 'tombstone_id')::uuid;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(affected_project_id::text, 77341001));

  SELECT * INTO affected_tombstone
  FROM deletion_tombstones AS candidate
  WHERE candidate.id = affected_tombstone_id
    AND candidate.project_id = affected_project_id;
  IF NOT FOUND THEN
    RETURN COALESCE(NEW, OLD);
  END IF;

  IF affected_tombstone.purge_mode = 'source_hard_purge' THEN
    IF NOT ck_source_hard_purge_predecessor_set_is_valid(
      affected_tombstone.id, affected_tombstone.project_id
    ) THEN
      RAISE EXCEPTION 'source hard purge has an incomplete or mismatched predecessor set'
        USING ERRCODE = 'check_violation';
    END IF;
    RETURN COALESCE(NEW, OLD);
  END IF;

  IF affected_tombstone.purge_mode = 'derived_generation'
     AND affected_tombstone.status = 'purged' THEN
    FOR hard_tombstone IN
      SELECT hard.id, hard.project_id
      FROM deletion_tombstones AS hard
      WHERE hard.purge_mode = 'source_hard_purge'
        AND hard.project_id = affected_tombstone.project_id
        AND affected_tombstone.fenced_at <= hard.fenced_at
        AND ck_source_hard_purge_contains_derived(hard, affected_tombstone)
    LOOP
      IF NOT ck_source_hard_purge_predecessor_set_is_valid(
        hard_tombstone.id, hard_tombstone.project_id
      ) THEN
        RAISE EXCEPTION 'source hard purge has an incomplete or mismatched predecessor set'
          USING ERRCODE = 'check_violation';
      END IF;
    END LOOP;
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$;


--
-- Name: ck_validate_step_claim(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_step_claim() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  task deletion_target_tasks%ROWTYPE;
BEGIN
  IF NEW.state = 'pending' THEN
    RETURN NEW;
  END IF;
  SELECT * INTO task
  FROM deletion_target_tasks t
  WHERE t.tombstone_id = NEW.tombstone_id
    AND t.project_id = NEW.project_id
    AND t.target = NEW.target;
  IF task.state <> 'processing'
     OR task.claim_expires_at <= clock_timestamp()
     OR NEW.claim_incarnation <> task.claim_incarnation
     OR NEW.claimed_by_digest IS DISTINCT FROM task.claimed_by_digest THEN
    RAISE EXCEPTION 'postgresql step has a stale or expired task claim'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_validate_tombstone_source(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_validate_tombstone_source() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  found_source boolean;
BEGIN
  IF NEW.entity_kind = 'project' THEN
    SELECT EXISTS (
      SELECT 1 FROM projects p
      WHERE p.id = NEW.project_id
        AND (
          (NEW.purge_mode = 'derived_generation'
            AND p.deleted_at IS NOT NULL
            AND p.lifecycle_generation = NEW.project_generation + 1
            AND p.deleted_at = NEW.fenced_at)
          OR
          (NEW.purge_mode = 'source_hard_purge'
            AND p.lifecycle_generation = NEW.project_generation
            AND p.deleted_at IS NOT NULL
            AND p.hard_purge_started_at = NEW.fenced_at
            AND p.purge_deadline = NEW.purge_deadline)
        )
    ) INTO found_source;
  ELSIF NEW.entity_kind = 'book' THEN
    SELECT EXISTS (
      SELECT 1 FROM books b
      WHERE b.id = NEW.book_id
        AND b.project_id = NEW.project_id
        AND (
          (NEW.purge_mode = 'derived_generation'
            AND b.deleted_at IS NOT NULL
            AND b.lifecycle_generation = NEW.book_generation + 1
            AND b.deleted_at = NEW.fenced_at)
          OR
          (NEW.purge_mode = 'source_hard_purge'
            AND b.lifecycle_generation = NEW.book_generation
            AND b.deleted_at IS NOT NULL
            AND b.hard_purge_started_at = NEW.fenced_at
            AND b.purge_deadline = NEW.purge_deadline)
        )
    ) INTO found_source;
  ELSIF NEW.entity_kind = 'chapter' THEN
    SELECT EXISTS (
      SELECT 1 FROM chapters c
      WHERE c.id = NEW.chapter_id
        AND c.book_id = NEW.book_id
        AND c.project_id = NEW.project_id
        AND (
          (NEW.purge_mode = 'derived_generation'
            AND c.deleted_at IS NOT NULL
            AND c.lifecycle_generation = NEW.chapter_generation + 1
            AND c.deleted_at = NEW.fenced_at)
          OR
          (NEW.purge_mode = 'source_hard_purge'
            AND c.lifecycle_generation = NEW.chapter_generation
            AND c.deleted_at IS NOT NULL
            AND c.hard_purge_started_at = NEW.fenced_at
            AND c.purge_deadline = NEW.purge_deadline)
        )
    ) INTO found_source;
  ELSE
    SELECT EXISTS (
      SELECT 1 FROM chat_sessions c
      WHERE c.id = NEW.chat_session_id
        AND c.project_id = NEW.project_id
        AND c.deleted_at IS NOT NULL
        AND c.lifecycle_epoch = NEW.chat_lifecycle_epoch + 1
        AND c.deleted_at = NEW.fenced_at
        AND c.purge_after IS NOT DISTINCT FROM NEW.purge_deadline
    ) INTO found_source;
  END IF;

  IF NOT found_source THEN
    RAISE EXCEPTION 'tombstone source vector is missing or cross-project'
      USING ERRCODE = 'foreign_key_violation';
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: ck_writer_lease_identity_is_valid(bytea, text, bytea); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ck_writer_lease_identity_is_valid(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) RETURNS boolean
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
DECLARE
  required_capability constant text :=
    'artifact_sources_v1+lifecycle_v1+claim_incarnation_v1+owner_aware_purge_v1';
BEGIN
  IF wanted_capability_version <> required_capability
     OR pg_catalog.octet_length(wanted_instance_id_digest) <> 32
     OR wanted_instance_id_digest = pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex')
     OR pg_catalog.octet_length(wanted_build_digest) <> 32
     OR wanted_build_digest = pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex') THEN
    RETURN false;
  END IF;
  RETURN true;
END;
$$;


--
-- Name: governance_writer_fleet_fence_key(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.governance_writer_fleet_fence_key() RETURNS bigint
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$ SELECT 4848213110764141902::bigint $$;


--
-- Name: agent_workflow_inputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_workflow_inputs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    job_kind text NOT NULL,
    target_id uuid NOT NULL,
    source_view text NOT NULL,
    body_json jsonb NOT NULL,
    body_digest bytea NOT NULL,
    context_warning text,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_workflow_inputs_pkey PRIMARY KEY (id),
    CONSTRAINT agent_workflow_inputs_job_unique UNIQUE (job_id),
    CONSTRAINT agent_workflow_inputs_binding_unique UNIQUE (id, project_id, job_id, job_kind, target_id, source_view),
    CONSTRAINT agent_workflow_inputs_body_check CHECK ((jsonb_typeof(body_json) = 'object'::text) AND (octet_length((body_json)::text) <= 1048576)),
    CONSTRAINT agent_workflow_inputs_body_digest_check CHECK ((octet_length(body_digest) = 32) AND (body_digest <> decode(repeat('00'::text, 32), 'hex'::text))),
    CONSTRAINT agent_workflow_inputs_context_warning_check CHECK (((context_warning IS NULL) OR ((job_kind = 'writer_agent'::text) AND (context_warning = ANY (ARRAY['partial_context'::text, 'style_context_limited'::text]))) OR ((job_kind = ANY (ARRAY['research_chat_turn'::text, 'canon_correction'::text])) AND (context_warning = 'partial_context'::text)))),
    CONSTRAINT agent_workflow_inputs_job_kind_check CHECK ((job_kind = ANY (ARRAY['research_chat_turn'::text, 'writer_agent'::text, 'canon_correction'::text]))),
    CONSTRAINT agent_workflow_inputs_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT agent_workflow_inputs_target_id_check CHECK ((target_id <> '00000000-0000-0000-0000-000000000000'::uuid))
);


--
-- Name: writer_proposals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.writer_proposals (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    book_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    job_id uuid NOT NULL,
    job_kind text DEFAULT 'writer_agent'::text NOT NULL,
    workflow_input_id uuid,
    source_view text DEFAULT 'draft'::text NOT NULL,
    mode text NOT NULL,
    kind text NOT NULL,
    state text NOT NULL,
    target_json jsonb NOT NULL,
    finding_body text,
    diff_hunks_json jsonb,
    base_paragraphs_json jsonb,
    preview_paragraphs_json jsonb,
    base_revision bigint NOT NULL,
    post_apply_revision bigint,
    undo_revision bigint,
    context_warning text,
    output_digest bytea NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT writer_proposals_pkey PRIMARY KEY (id),
    CONSTRAINT writer_proposals_job_unique UNIQUE (job_id),
    CONSTRAINT writer_proposals_base_revision_check CHECK ((base_revision >= 1)),
    CONSTRAINT writer_proposals_content_check CHECK (CASE kind
      WHEN 'finding' THEN state = 'read_only'
        AND finding_body IS NOT NULL
        AND char_length(btrim(finding_body)) BETWEEN 1 AND 8000
        AND diff_hunks_json IS NULL
        AND base_paragraphs_json IS NULL
        AND preview_paragraphs_json IS NULL
      WHEN 'diff' THEN state IN ('pending', 'applied', 'undone')
        AND finding_body IS NULL
        AND diff_hunks_json IS NOT NULL
        AND jsonb_typeof(diff_hunks_json) = 'array'
        AND base_paragraphs_json IS NOT NULL
        AND jsonb_typeof(base_paragraphs_json) = 'array'
        AND preview_paragraphs_json IS NOT NULL
        AND jsonb_typeof(preview_paragraphs_json) = 'array'
      WHEN 'generated_draft' THEN state IN ('pending', 'applied', 'undone')
        AND finding_body IS NULL
        AND diff_hunks_json IS NULL
        AND base_paragraphs_json IS NOT NULL
        AND jsonb_typeof(base_paragraphs_json) = 'array'
        AND preview_paragraphs_json IS NOT NULL
        AND jsonb_typeof(preview_paragraphs_json) = 'array'
      ELSE false
    END),
    CONSTRAINT writer_proposals_context_warning_check CHECK (context_warning IS NULL OR context_warning IN ('partial_context', 'style_context_limited')),
    CONSTRAINT writer_proposals_job_kind_check CHECK ((job_kind = 'writer_agent'::text)),
    CONSTRAINT writer_proposals_json_bounds_check CHECK ((octet_length((target_json)::text) <= 65536) AND (COALESCE(octet_length((diff_hunks_json)::text), 0) <= 1048576) AND (COALESCE(octet_length((base_paragraphs_json)::text), 0) <= 2097152) AND (COALESCE(octet_length((preview_paragraphs_json)::text), 0) <= 2097152)),
    CONSTRAINT writer_proposals_kind_check CHECK ((kind = ANY (ARRAY['finding'::text, 'diff'::text, 'generated_draft'::text]))),
    CONSTRAINT writer_proposals_mode_check CHECK ((mode = ANY (ARRAY['review'::text, 'proofread'::text, 'rewrite'::text, 'style_match'::text, 'continuity'::text, 'expand_outline'::text]))),
    CONSTRAINT writer_proposals_output_digest_check CHECK ((octet_length(output_digest) = 32) AND (output_digest <> decode(repeat('00'::text, 32), 'hex'::text))),
    CONSTRAINT writer_proposals_revision_state_check CHECK ((((state = ANY (ARRAY['read_only'::text, 'pending'::text])) AND (post_apply_revision IS NULL) AND (undo_revision IS NULL)) OR ((state = 'applied'::text) AND (post_apply_revision IS NOT NULL) AND (post_apply_revision > base_revision) AND (undo_revision IS NULL)) OR ((state = 'undone'::text) AND (post_apply_revision IS NOT NULL) AND (post_apply_revision > base_revision) AND (undo_revision IS NOT NULL) AND (undo_revision > post_apply_revision)))),
    CONSTRAINT writer_proposals_source_view_check CHECK ((source_view = 'draft'::text)),
    CONSTRAINT writer_proposals_state_check CHECK ((state = ANY (ARRAY['read_only'::text, 'pending'::text, 'applied'::text, 'undone'::text]))),
    CONSTRAINT writer_proposals_target_check CHECK ((jsonb_typeof(target_json) = 'object'::text))
);


--
-- Name: canon_correction_proposals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_correction_proposals (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    anchor_book_id uuid NOT NULL,
    job_id uuid NOT NULL,
    job_kind text DEFAULT 'canon_correction'::text NOT NULL,
    workflow_input_id uuid,
    source_view text NOT NULL,
    origin_type text NOT NULL,
    origin_json jsonb NOT NULL,
    action text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    proposed_row_json jsonb,
    target_row_json jsonb,
    evidence_json jsonb NOT NULL,
    applied_result_json jsonb,
    context_warning text,
    output_digest bytea NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT canon_correction_proposals_pkey PRIMARY KEY (id),
    CONSTRAINT canon_correction_proposals_job_unique UNIQUE (job_id),
    CONSTRAINT canon_correction_proposals_action_check CHECK ((action = ANY (ARRAY['add'::text, 'update'::text, 'delete'::text]))),
    CONSTRAINT canon_correction_proposals_content_check CHECK ((((action = 'add'::text) AND (proposed_row_json IS NOT NULL) AND (jsonb_typeof(proposed_row_json) = 'object'::text) AND (target_row_json IS NULL)) OR ((action = 'update'::text) AND (proposed_row_json IS NOT NULL) AND (jsonb_typeof(proposed_row_json) = 'object'::text) AND (target_row_json IS NOT NULL) AND (jsonb_typeof(target_row_json) = 'object'::text)) OR ((action = 'delete'::text) AND (proposed_row_json IS NULL) AND (target_row_json IS NOT NULL) AND (jsonb_typeof(target_row_json) = 'object'::text))) AND (jsonb_typeof(evidence_json) = 'array'::text)),
    CONSTRAINT canon_correction_proposals_context_warning_check CHECK (((context_warning IS NULL) OR (context_warning = 'partial_context'::text))),
    CONSTRAINT canon_correction_proposals_job_kind_check CHECK ((job_kind = 'canon_correction'::text)),
    CONSTRAINT canon_correction_proposals_json_bounds_check CHECK ((octet_length((origin_json)::text) <= 65536) AND (COALESCE(octet_length((proposed_row_json)::text), 0) <= 1048576) AND (COALESCE(octet_length((target_row_json)::text), 0) <= 1048576) AND (octet_length((evidence_json)::text) <= 1048576) AND (COALESCE(octet_length((applied_result_json)::text), 0) <= 1048576)),
    CONSTRAINT canon_correction_proposals_origin_check CHECK ((origin_type = ANY (ARRAY['source'::text, 'writer'::text])) AND (jsonb_typeof(origin_json) = 'object'::text)),
    CONSTRAINT canon_correction_proposals_output_digest_check CHECK ((octet_length(output_digest) = 32) AND (output_digest <> decode(repeat('00'::text, 32), 'hex'::text))),
    CONSTRAINT canon_correction_proposals_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT canon_correction_proposals_state_check CHECK (((state = 'pending'::text) AND (applied_result_json IS NULL)) OR ((state = 'applied'::text) AND (applied_result_json IS NOT NULL) AND (jsonb_typeof(applied_result_json) = 'object'::text))),
    CONSTRAINT canon_correction_proposals_state_value_check CHECK ((state = ANY (ARRAY['pending'::text, 'applied'::text])))
);


--
-- Name: artifact_source_owners; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifact_source_owners (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    artifact_kind text NOT NULL,
    artifact_id uuid NOT NULL,
    source_view text,
    manifest_version integer NOT NULL,
    manifest_hash bytea NOT NULL,
    ref_count integer NOT NULL,
    read_state text NOT NULL,
    source_identity_state text NOT NULL,
    fenced_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT artifact_source_owners_artifact_kind_check CHECK ((artifact_kind = ANY (ARRAY['canon_book_enrichment_input'::text, 'job'::text, 'import_upload'::text, 'import_job_request'::text, 'chat_turn'::text, 'writer_proposal'::text, 'export_artifact'::text, 'runtime_session'::text, 'chapter_summary'::text, 'canon_author_row'::text, 'canon_graph_contribution'::text, 'canon_correction_proposal'::text, 'agent_workflow_input'::text]))),
    CONSTRAINT artifact_source_owners_contract_shape CHECK (((((manifest_version = 1) AND (source_identity_state = 'scope_only'::text)) OR ((manifest_version = 2) AND (source_identity_state = 'text_derived_current'::text))) AND (octet_length(manifest_hash) = 32) AND (manifest_hash <> decode(repeat('00'::text, 32), 'hex'::text)) AND (ref_count >= 1) AND (((read_state = 'active'::text) AND (fenced_at IS NULL)) OR ((read_state = ANY (ARRAY['source_deleted'::text, 'chat_deleted'::text])) AND (fenced_at IS NOT NULL))))),
    CONSTRAINT artifact_source_owners_read_state_check CHECK ((read_state = ANY (ARRAY['active'::text, 'source_deleted'::text, 'chat_deleted'::text]))),
    CONSTRAINT artifact_source_owners_ref_count_check CHECK ((ref_count >= 0)),
    CONSTRAINT artifact_source_owners_source_identity_state_check CHECK (((source_identity_state IS NULL) OR (source_identity_state = ANY (ARRAY['scope_only'::text, 'text_derived_current'::text])))),
    CONSTRAINT artifact_source_owners_source_view_check CHECK (((source_view IS NULL) OR (source_view = ANY (ARRAY['draft'::text, 'published'::text]))))
);


--
-- Name: artifact_source_refs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifact_source_refs (
    id uuid NOT NULL,
    owner_id uuid NOT NULL,
    project_id uuid NOT NULL,
    ref_kind text NOT NULL,
    scope_kind text,
    source_view text,
    project_generation bigint NOT NULL,
    book_id uuid,
    book_generation bigint,
    chapter_id uuid,
    chapter_generation bigint,
    source_revision bigint,
    locator_source_generation bytea,
    text_identity_state text,
    source_independent boolean DEFAULT false NOT NULL,
    chat_session_id uuid,
    chat_lifecycle_epoch bigint,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    content_hash bytea,
    CONSTRAINT artifact_source_refs_check CHECK ((((chat_session_id IS NULL) AND (chat_lifecycle_epoch IS NULL)) OR ((chat_session_id IS NOT NULL) AND (chat_lifecycle_epoch >= 1)))),
    CONSTRAINT artifact_source_refs_contract_shape CHECK (COALESCE(
      (ref_kind = 'chapter' AND scope_kind IS NULL AND source_view IS NOT NULL
        AND book_id IS NOT NULL AND book_generation >= 1
        AND chapter_id IS NOT NULL AND chapter_generation >= 1
        AND source_revision >= 1
        AND locator_source_generation IS NOT NULL
        AND octet_length(locator_source_generation) = 32
        AND locator_source_generation <> decode(repeat('00', 32), 'hex')
        AND text_identity_state = 'text_derived_current'
        AND content_hash IS NOT NULL AND octet_length(content_hash) = 32
        AND content_hash <> decode(repeat('00', 32), 'hex')
        AND source_independent = false)
      OR (ref_kind = 'scope' AND scope_kind = 'project'
        AND book_id IS NULL AND book_generation IS NULL
        AND chapter_id IS NULL AND chapter_generation IS NULL
        AND source_revision IS NULL
        AND locator_source_generation IS NULL AND text_identity_state IS NULL
        AND content_hash IS NULL
        AND ((source_independent = true AND source_view IS NULL)
          OR (source_independent = false AND source_view IS NOT NULL)))
      OR (ref_kind = 'scope' AND scope_kind = 'book' AND source_view IS NOT NULL
        AND source_independent = false AND book_id IS NOT NULL AND book_generation >= 1
        AND chapter_id IS NULL AND chapter_generation IS NULL
        AND source_revision IS NULL
        AND locator_source_generation IS NULL AND text_identity_state IS NULL
        AND content_hash IS NULL)
      OR (ref_kind = 'scope' AND scope_kind = 'chapter' AND source_view IS NOT NULL
        AND source_independent = false AND book_id IS NOT NULL AND book_generation >= 1
        AND chapter_id IS NOT NULL AND chapter_generation >= 1
        AND source_revision IS NULL
        AND locator_source_generation IS NULL AND text_identity_state IS NULL
        AND content_hash IS NULL),
      false
    )),
    CONSTRAINT artifact_source_refs_project_generation_check CHECK ((project_generation >= 1)),
    CONSTRAINT artifact_source_refs_ref_kind_check CHECK ((ref_kind = ANY (ARRAY['scope'::text, 'chapter'::text]))),
    CONSTRAINT artifact_source_refs_scope_kind_check CHECK (((scope_kind IS NULL) OR (scope_kind = ANY (ARRAY['project'::text, 'book'::text, 'chapter'::text])))),
    CONSTRAINT artifact_source_refs_source_view_check CHECK (((source_view IS NULL) OR (source_view = ANY (ARRAY['draft'::text, 'published'::text])))),
    CONSTRAINT artifact_source_refs_text_identity_state_check CHECK (((text_identity_state IS NULL) OR (text_identity_state = 'text_derived_current'::text)))
);
--
-- Name: audit_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_logs (
    id uuid NOT NULL,
    project_id uuid,
    actor_user_id uuid,
    operation_id text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    outcome text NOT NULL,
    data_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    audit_scope_kind text NOT NULL,
    project_generation bigint,
    book_id uuid,
    book_generation bigint,
    chapter_id uuid,
    chapter_generation bigint,
    chat_session_id uuid,
    chat_lifecycle_epoch bigint,
    audit_payload_state text NOT NULL,
    audit_payload_schema text NOT NULL,
    CONSTRAINT audit_logs_governance_scope_shape CHECK (
      audit_scope_kind = ANY (ARRAY['book'::text, 'scrubbed'::text])
      AND audit_payload_state = ANY (ARRAY['safe'::text, 'source_scrubbed'::text, 'project_scrubbed'::text])
      AND audit_payload_schema = ANY (ARRAY['export_downloaded_v1'::text, 'empty_v1'::text])
      AND (
        (audit_scope_kind = 'book'::text AND project_id IS NOT NULL AND project_generation >= 1 AND book_id IS NOT NULL AND book_generation >= 1 AND chapter_id IS NULL AND chapter_generation IS NULL AND chat_session_id IS NULL AND chat_lifecycle_epoch IS NULL)
        OR (audit_scope_kind = 'scrubbed'::text AND project_generation IS NULL AND book_id IS NULL AND book_generation IS NULL AND chapter_id IS NULL AND chapter_generation IS NULL AND chat_session_id IS NULL AND chat_lifecycle_epoch IS NULL AND resource_type = 'deleted'::text AND resource_id = '00000000-0000-0000-0000-000000000000'::text AND actor_user_id IS NULL AND data_json = '{}'::jsonb AND audit_payload_schema = 'empty_v1'::text)
      )
    ),
    CONSTRAINT audit_logs_payload_registry_check CHECK ((audit_payload_state <> 'safe'::text) OR public.ck_audit_payload_shape_valid(operation_id, resource_type, outcome, audit_payload_schema, data_json))
);


--
-- Name: book_source_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.book_source_members (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    project_lifecycle_generation bigint NOT NULL,
    book_id uuid NOT NULL,
    book_lifecycle_generation bigint NOT NULL,
    title text NOT NULL,
    CONSTRAINT book_source_members_book_lifecycle_generation_check CHECK ((book_lifecycle_generation >= 1)),
    CONSTRAINT book_source_members_project_lifecycle_generation_check CHECK ((project_lifecycle_generation >= 1)),
    CONSTRAINT book_source_members_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT book_source_members_title_check CHECK ((btrim(title) <> ''::text))
);


--
-- Name: books; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.books (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    title text NOT NULL,
    subtitle text,
    cover_color text,
    "order" integer NOT NULL,
    display_number text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    deleted_at timestamp with time zone,
    purge_after timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    lifecycle_generation bigint DEFAULT 1 NOT NULL,
    hard_purge_started_at timestamp with time zone,
    governance_retention_started_at timestamp with time zone,
    purge_deadline timestamp with time zone,
    CONSTRAINT books_contract_deleted_window CHECK (((deleted_at IS NULL) OR ((governance_retention_started_at IS NOT NULL) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval))))),
    CONSTRAINT books_cover_color_check CHECK (((cover_color IS NULL) OR (cover_color ~ '^#[0-9A-Fa-f]{6}$'::text))),
    CONSTRAINT books_governance_retention_shape CHECK ((((deleted_at IS NULL) AND (governance_retention_started_at IS NULL) AND (purge_after IS NULL) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at >= deleted_at) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval)) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at >= deleted_at) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval)) AND (hard_purge_started_at IS NOT NULL) AND (hard_purge_started_at >= purge_after)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at IS NULL) AND (purge_after IS NULL) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at IS NULL) AND (purge_after = deleted_at) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)))),
    CONSTRAINT books_lifecycle_generation_positive CHECK ((lifecycle_generation >= 1)),
    CONSTRAINT books_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'ready'::text, 'error'::text])))
);


--
-- Name: canon_author_rows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_author_rows (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    anchor_book_id uuid NOT NULL,
    row_kind text NOT NULL,
    payload_json jsonb NOT NULL,
    row_version bigint NOT NULL,
    effective_fingerprint bytea NOT NULL,
    search_document text NOT NULL,
    search_vector tsvector GENERATED ALWAYS AS (to_tsvector('russian'::regconfig, search_document)) STORED,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT canon_author_rows_check CHECK ((((jsonb_typeof(payload_json) = 'object'::text) AND ((payload_json ->> 'version'::text) = '1'::text) AND ((payload_json ->> 'kind'::text) = row_kind) AND (jsonb_typeof((payload_json -> 'value'::text)) = 'object'::text) AND (octet_length((payload_json)::text) <= 16384)) IS TRUE)),
    CONSTRAINT canon_author_rows_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT canon_author_rows_effective_fingerprint_check CHECK (((octet_length(effective_fingerprint) = 32) AND (effective_fingerprint <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT canon_author_rows_row_kind_check CHECK ((row_kind = ANY (ARRAY['entity'::text, 'alias'::text, 'fact'::text, 'relationship'::text, 'event'::text, 'event_link'::text, 'character_state'::text, 'dialogue_attribution'::text]))),
    CONSTRAINT canon_author_rows_row_version_check CHECK ((row_version >= 1)),
    CONSTRAINT canon_author_rows_search_document_check CHECK ((btrim(search_document) <> ''::text)),
    CONSTRAINT canon_author_rows_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: canon_book_enrichment_inputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_book_enrichment_inputs (
    job_id uuid NOT NULL,
    project_id uuid NOT NULL,
    artifact_source_owner_id uuid NOT NULL
);


--
-- Name: canon_book_enrichment_steps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_book_enrichment_steps (
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    step text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    runtime_started_at timestamp with time zone,
    completed_at timestamp with time zone,
    CONSTRAINT canon_book_enrichment_steps_completed_check CHECK (((completed_at IS NULL) OR (completed_at >= started_at))),
    CONSTRAINT canon_book_enrichment_steps_runtime_check CHECK ((((step = ANY (ARRAY['graph_review_completeness'::text, 'graph_review_source_evidence'::text, 'graph_review_saga_history'::text])) AND ((completed_at IS NULL) OR ((runtime_started_at IS NOT NULL) AND (completed_at >= runtime_started_at)))) OR ((step <> ALL (ARRAY['graph_review_completeness'::text, 'graph_review_source_evidence'::text, 'graph_review_saga_history'::text])) AND (runtime_started_at IS NULL)))),
    CONSTRAINT canon_book_enrichment_steps_step_check CHECK ((step = ANY (ARRAY['enrichment'::text, 'summary'::text, 'graph_draft'::text, 'graph_review_completeness'::text, 'graph_review_source_evidence'::text, 'graph_review_saga_history'::text, 'graph_activation'::text, 'ready'::text])))
);


--
-- Name: canon_graph_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_graph_attempts (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    job_id uuid NOT NULL,
    claim_incarnation bigint NOT NULL,
    runtime_session_id uuid NOT NULL,
    target_manifest bytea NOT NULL,
    previous_contribution_id uuid,
    previous_complete boolean NOT NULL,
    handle_bindings_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    draft_max_bytes integer NOT NULL,
    draft_max_lines integer NOT NULL,
    draft_max_rows integer NOT NULL,
    draft_revision bigint DEFAULT 0 NOT NULL,
    current_draft_digest bytea,
    frozen_revision bigint,
    frozen_digest bytea,
    self_reviewed_at timestamp with time zone,
    self_review_status_revision bigint,
    self_review_search_revision bigint,
    reviewed_revision bigint,
    completeness_child_thread_id text,
    completeness_completed_at timestamp with time zone,
    source_evidence_child_thread_id text,
    source_evidence_completed_at timestamp with time zone,
    saga_history_child_thread_id text,
    saga_history_completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT canon_graph_attempts_check1 CHECK ((((draft_revision = 0) AND (current_draft_digest IS NULL)) OR ((draft_revision >= 1) AND (current_draft_digest IS NOT NULL)))),
    CONSTRAINT canon_graph_attempts_check10 CHECK (((source_evidence_completed_at IS NULL) OR (source_evidence_completed_at >= created_at))),
    CONSTRAINT canon_graph_attempts_check11 CHECK (((saga_history_completed_at IS NULL) OR (saga_history_completed_at >= created_at))),
    CONSTRAINT canon_graph_attempts_check12 CHECK ((updated_at >= created_at)),
    CONSTRAINT canon_graph_attempts_check2 CHECK (((completeness_child_thread_id IS NULL) = (completeness_completed_at IS NULL))),
    CONSTRAINT canon_graph_attempts_check3 CHECK (((source_evidence_child_thread_id IS NULL) = (source_evidence_completed_at IS NULL))),
    CONSTRAINT canon_graph_attempts_check4 CHECK (((saga_history_child_thread_id IS NULL) = (saga_history_completed_at IS NULL))),
    CONSTRAINT canon_graph_attempts_check5 CHECK ((((completeness_child_thread_id IS NULL) AND (source_evidence_child_thread_id IS NULL) AND (saga_history_child_thread_id IS NULL) AND (reviewed_revision IS NULL)) OR ((reviewed_revision >= 1) AND ((completeness_child_thread_id IS NOT NULL) OR (source_evidence_child_thread_id IS NOT NULL) OR (saga_history_child_thread_id IS NOT NULL))))),
    CONSTRAINT canon_graph_attempts_check6 CHECK (((completeness_child_thread_id IS NULL) OR (source_evidence_child_thread_id IS NULL) OR (completeness_child_thread_id <> source_evidence_child_thread_id))),
    CONSTRAINT canon_graph_attempts_check7 CHECK (((completeness_child_thread_id IS NULL) OR (saga_history_child_thread_id IS NULL) OR (completeness_child_thread_id <> saga_history_child_thread_id))),
    CONSTRAINT canon_graph_attempts_check8 CHECK (((source_evidence_child_thread_id IS NULL) OR (saga_history_child_thread_id IS NULL) OR (source_evidence_child_thread_id <> saga_history_child_thread_id))),
    CONSTRAINT canon_graph_attempts_check9 CHECK (((completeness_completed_at IS NULL) OR (completeness_completed_at >= created_at))),
    CONSTRAINT canon_graph_attempts_claim_incarnation_check CHECK ((claim_incarnation >= 1)),
    CONSTRAINT canon_graph_attempts_completeness_child_thread_id_check CHECK (((completeness_child_thread_id IS NULL) OR ((btrim(completeness_child_thread_id) <> ''::text) AND (octet_length(completeness_child_thread_id) <= 512)))),
    CONSTRAINT canon_graph_attempts_current_draft_digest_check CHECK (((current_draft_digest IS NULL) OR ((octet_length(current_draft_digest) = 32) AND (current_draft_digest <> decode(repeat('00'::text, 32), 'hex'::text))))),
    CONSTRAINT canon_graph_attempts_draft_max_bytes_check CHECK (((draft_max_bytes >= 1) AND (draft_max_bytes <= 1000000))),
    CONSTRAINT canon_graph_attempts_draft_max_lines_check CHECK (((draft_max_lines >= 1) AND (draft_max_lines <= 25000))),
    CONSTRAINT canon_graph_attempts_draft_max_rows_check CHECK (((draft_max_rows >= 1) AND (draft_max_rows <= 10000))),
    CONSTRAINT canon_graph_attempts_draft_revision_check CHECK ((draft_revision >= 0)),
    CONSTRAINT canon_graph_attempts_frozen_check CHECK (((frozen_revision IS NULL) = (frozen_digest IS NULL)) AND ((frozen_revision IS NULL) = (self_reviewed_at IS NULL)) AND ((frozen_revision IS NULL) OR ((frozen_revision = draft_revision) AND (octet_length(frozen_digest) = 32)))),
    CONSTRAINT canon_graph_attempts_self_review_progress_check CHECK (((self_review_status_revision IS NULL) OR ((self_review_status_revision >= 1) AND (self_review_status_revision <= draft_revision))) AND ((self_review_search_revision IS NULL) OR ((self_review_search_revision >= 1) AND (self_review_search_revision <= draft_revision)))),
    CONSTRAINT canon_graph_attempts_handle_bindings_json_check CHECK (public.ck_canon_graph_handle_bindings_valid(handle_bindings_json)),
    CONSTRAINT canon_graph_attempts_previous_complete_check CHECK (previous_complete),
    CONSTRAINT canon_graph_attempts_saga_history_child_thread_id_check CHECK (((saga_history_child_thread_id IS NULL) OR ((btrim(saga_history_child_thread_id) <> ''::text) AND (octet_length(saga_history_child_thread_id) <= 512)))),
    CONSTRAINT canon_graph_attempts_source_evidence_child_thread_id_check CHECK (((source_evidence_child_thread_id IS NULL) OR ((btrim(source_evidence_child_thread_id) <> ''::text) AND (octet_length(source_evidence_child_thread_id) <= 512)))),
    CONSTRAINT canon_graph_attempts_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT canon_graph_attempts_target_manifest_check CHECK (((octet_length(target_manifest) = 32) AND (target_manifest <> decode(repeat('00'::text, 32), 'hex'::text))))
);


--
-- Name: canon_graph_book_slots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_graph_book_slots (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    active_contribution_id uuid,
    previous_contribution_id uuid,
    target_manifest bytea NOT NULL,
    job_id uuid,
    predecessor_bound_at timestamp with time zone,
    predecessor_expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT canon_graph_book_slots_check CHECK (((active_contribution_id IS NOT NULL) OR (previous_contribution_id IS NOT NULL))),
    CONSTRAINT canon_graph_book_slots_check1 CHECK (((job_id IS NULL) = (predecessor_bound_at IS NULL))),
    CONSTRAINT canon_graph_book_slots_check2 CHECK (((previous_contribution_id IS NOT NULL) OR ((job_id IS NULL) AND (predecessor_bound_at IS NULL) AND (predecessor_expires_at IS NULL)))),
    CONSTRAINT canon_graph_book_slots_check3 CHECK (((previous_contribution_id IS NULL) OR (job_id IS NOT NULL) OR (predecessor_expires_at IS NOT NULL))),
    CONSTRAINT canon_graph_book_slots_check4 CHECK (((predecessor_expires_at IS NULL) OR ((predecessor_expires_at > updated_at) AND (predecessor_expires_at <= (updated_at + '24:00:00'::interval))))),
    CONSTRAINT canon_graph_book_slots_check5 CHECK ((updated_at >= created_at)),
    CONSTRAINT canon_graph_book_slots_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT canon_graph_book_slots_target_manifest_check CHECK (((octet_length(target_manifest) = 32) AND (target_manifest <> decode(repeat('00'::text, 32), 'hex'::text))))
);


--
-- Name: canon_graph_contributions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_graph_contributions (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    id uuid NOT NULL,
    book_manifest bytea NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT canon_graph_contributions_book_manifest_check CHECK (((octet_length(book_manifest) = 32) AND (book_manifest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT canon_graph_contributions_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: canon_graph_overlays; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_graph_overlays (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    target_stable_row_id uuid NOT NULL,
    target_fingerprint bytea NOT NULL,
    kind text NOT NULL,
    replacement_author_stable_row_id uuid,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT canon_graph_overlays_check CHECK ((((kind = 'replacement'::text) AND (replacement_author_stable_row_id IS NOT NULL)) OR ((kind = 'suppression'::text) AND (replacement_author_stable_row_id IS NULL)))),
    CONSTRAINT canon_graph_overlays_kind_check CHECK ((kind = ANY (ARRAY['replacement'::text, 'suppression'::text]))),
    CONSTRAINT canon_graph_overlays_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT canon_graph_overlays_target_fingerprint_check CHECK (((octet_length(target_fingerprint) = 32) AND (target_fingerprint <> decode(repeat('00'::text, 32), 'hex'::text))))
);


--
-- Name: canon_graph_row_evidence; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_graph_row_evidence (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    contribution_id uuid NOT NULL,
    stable_row_id uuid NOT NULL,
    evidence_ordinal smallint NOT NULL,
    artifact_source_ref_id uuid NOT NULL,
    CONSTRAINT canon_graph_row_evidence_evidence_ordinal_check CHECK (((evidence_ordinal >= 0) AND (evidence_ordinal <= 7))),
    CONSTRAINT canon_graph_row_evidence_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: canon_graph_rows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canon_graph_rows (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    contribution_id uuid NOT NULL,
    stable_row_id uuid NOT NULL,
    row_version bigint NOT NULL,
    row_kind text NOT NULL,
    payload_json jsonb NOT NULL,
    effective_fingerprint bytea NOT NULL,
    search_document text NOT NULL,
    search_vector tsvector GENERATED ALWAYS AS (to_tsvector('russian'::regconfig, search_document)) STORED,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT canon_graph_rows_check CHECK ((((jsonb_typeof(payload_json) = 'object'::text) AND ((payload_json ->> 'version'::text) = '1'::text) AND ((payload_json ->> 'kind'::text) = row_kind) AND (jsonb_typeof((payload_json -> 'value'::text)) = 'object'::text) AND (octet_length((payload_json)::text) <= 16384)) IS TRUE)),
    CONSTRAINT canon_graph_rows_effective_fingerprint_check CHECK (((octet_length(effective_fingerprint) = 32) AND (effective_fingerprint <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT canon_graph_rows_row_kind_check CHECK ((row_kind = ANY (ARRAY['entity'::text, 'alias'::text, 'fact'::text, 'relationship'::text, 'event'::text, 'event_link'::text, 'character_state'::text, 'dialogue_attribution'::text]))),
    CONSTRAINT canon_graph_rows_row_version_check CHECK ((row_version >= 1)),
    CONSTRAINT canon_graph_rows_search_document_check CHECK ((btrim(search_document) <> ''::text)),
    CONSTRAINT canon_graph_rows_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: chapter_paragraphs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chapter_paragraphs (
    id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    revision bigint NOT NULL,
    variant text NOT NULL,
    kind text NOT NULL,
    "order" integer NOT NULL,
    markdown text NOT NULL,
    plain_text text NOT NULL,
    word_count integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chapter_paragraphs_kind_check CHECK ((kind = ANY (ARRAY['paragraph'::text, 'heading'::text, 'quote'::text, 'list_item'::text, 'scene_break'::text]))),
    CONSTRAINT chapter_paragraphs_variant_check CHECK ((variant = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: chapter_source_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chapter_source_members (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    project_lifecycle_generation bigint NOT NULL,
    book_id uuid NOT NULL,
    book_lifecycle_generation bigint NOT NULL,
    chapter_id uuid NOT NULL,
    chapter_lifecycle_generation bigint NOT NULL,
    source_revision bigint NOT NULL,
    title text NOT NULL,
    chapter_order integer NOT NULL,
    CONSTRAINT chapter_source_members_book_lifecycle_generation_check CHECK ((book_lifecycle_generation >= 1)),
    CONSTRAINT chapter_source_members_chapter_lifecycle_generation_check CHECK ((chapter_lifecycle_generation >= 1)),
    CONSTRAINT chapter_source_members_chapter_order_check CHECK ((chapter_order >= 0)),
    CONSTRAINT chapter_source_members_project_lifecycle_generation_check CHECK ((project_lifecycle_generation >= 1)),
    CONSTRAINT chapter_source_members_source_revision_check CHECK ((source_revision >= 1)),
    CONSTRAINT chapter_source_members_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT chapter_source_members_title_check CHECK ((btrim(title) <> ''::text))
);


--
-- Name: chapter_text_index_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chapter_text_index_state (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    chapter_id uuid NOT NULL,
    source_revision bigint NOT NULL,
	locator_source_generation bytea NOT NULL,
	chapter_content_hash bytea NOT NULL,
    indexed_at timestamp with time zone NOT NULL,
    CONSTRAINT chapter_text_index_state_chapter_content_hash_check CHECK ((octet_length(chapter_content_hash) = 32)),
    CONSTRAINT chapter_text_index_state_locator_source_generation_check CHECK ((octet_length(locator_source_generation) = 32)),
    CONSTRAINT chapter_text_index_state_source_revision_check CHECK ((source_revision >= 1)),
    CONSTRAINT chapter_text_index_state_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: chapters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chapters (
    id uuid NOT NULL,
    book_id uuid NOT NULL,
    title text NOT NULL,
    "order" integer NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    published_revision bigint,
    published_at timestamp with time zone,
    publish_error text,
    deleted_at timestamp with time zone,
    purge_after timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    lifecycle_generation bigint DEFAULT 1 NOT NULL,
    hard_purge_started_at timestamp with time zone,
    governance_retention_started_at timestamp with time zone,
    purge_deadline timestamp with time zone,
    project_id uuid NOT NULL,
    CONSTRAINT chapters_contract_deleted_window CHECK (((deleted_at IS NULL) OR ((governance_retention_started_at IS NOT NULL) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval))))),
    CONSTRAINT chapters_governance_retention_shape CHECK ((((deleted_at IS NULL) AND (governance_retention_started_at IS NULL) AND (purge_after IS NULL) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at >= deleted_at) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval)) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at >= deleted_at) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval)) AND (hard_purge_started_at IS NOT NULL) AND (hard_purge_started_at >= purge_after)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at IS NULL) AND (purge_after IS NULL) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at IS NULL) AND (purge_after = deleted_at) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)))),
    CONSTRAINT chapters_lifecycle_generation_positive CHECK ((lifecycle_generation >= 1)),
    CONSTRAINT chapters_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text, 'publishing'::text, 'publish_failed'::text, 'stale'::text, 'conflict'::text])))
);


--
-- Name: chat_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chat_sessions (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    scope text NOT NULL,
    book_id uuid,
    title text NOT NULL,
    thread_id text,
    deleted_at timestamp with time zone,
    purge_after timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    lifecycle_epoch bigint DEFAULT 1 NOT NULL,
    CONSTRAINT chat_sessions_deletion_window_check CHECK ((((deleted_at IS NULL) AND (purge_after IS NULL)) OR ((deleted_at IS NOT NULL) AND (purge_after = (deleted_at + '24:00:00'::interval))))),
    CONSTRAINT chat_sessions_lifecycle_epoch_positive CHECK ((lifecycle_epoch >= 1)),
    CONSTRAINT chat_sessions_scope_check CHECK ((((scope = 'project'::text) AND (book_id IS NULL)) OR ((scope = 'book'::text) AND (book_id IS NOT NULL)))),
    CONSTRAINT chat_sessions_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT chat_sessions_thread_id_check CHECK (((thread_id IS NULL) OR ((thread_id = btrim(thread_id)) AND (char_length(thread_id) >= 1) AND (char_length(thread_id) <= 255) AND (thread_id !~ '[[:cntrl:]]'::text)))),
    CONSTRAINT chat_sessions_title_check CHECK (((char_length(title) >= 1) AND (char_length(title) <= 180) AND (title ~ '[^[:space:]]'::text)))
);


--
-- Name: chat_turns; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chat_turns (
    id uuid NOT NULL,
    chat_id uuid NOT NULL,
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    job_id uuid NOT NULL,
    job_kind text DEFAULT 'research_chat_turn'::text NOT NULL,
    workflow_input_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    CONSTRAINT chat_turns_job_kind_check CHECK ((job_kind = 'research_chat_turn'::text)),
    CONSTRAINT chat_turns_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: deletion_completion_receipts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deletion_completion_receipts (
    tombstone_id uuid NOT NULL,
    project_id uuid NOT NULL,
    vector_digest bytea NOT NULL,
    target_receipts_digest bytea NOT NULL,
    completed_at timestamp with time zone NOT NULL,
    audit_expires_at timestamp with time zone,
    schema_version text NOT NULL,
    CONSTRAINT deletion_completion_receipts_check CHECK (((audit_expires_at IS NULL) OR (audit_expires_at >= (completed_at + '30 days'::interval)))),
    CONSTRAINT deletion_completion_receipts_completed_at_check CHECK ((completed_at = date_trunc('second'::text, completed_at))),
    CONSTRAINT deletion_completion_receipts_schema_version_check CHECK ((schema_version = 'CKDeletionReceiptV1'::text)),
    CONSTRAINT deletion_completion_receipts_target_receipts_digest_check CHECK (((octet_length(target_receipts_digest) = 32) AND (target_receipts_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT deletion_completion_receipts_vector_digest_check CHECK (((octet_length(vector_digest) = 32) AND (vector_digest <> decode(repeat('00'::text, 32), 'hex'::text))))
);


--
-- Name: deletion_hard_purge_predecessors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deletion_hard_purge_predecessors (
    hard_tombstone_id uuid NOT NULL,
    project_id uuid NOT NULL,
    predecessor_tombstone_id uuid CONSTRAINT deletion_hard_purge_predecess_predecessor_tombstone_id_not_null NOT NULL,
    predecessor_vector_digest bytea CONSTRAINT deletion_hard_purge_predeces_predecessor_vector_digest_not_null NOT NULL,
    predecessor_tombstone_digest bytea CONSTRAINT deletion_hard_purge_predece_predecessor_tombstone_dige_not_null NOT NULL,
    predecessor_target_receipts_digest bytea CONSTRAINT deletion_hard_purge_predece_predecessor_target_receipt_not_null NOT NULL,
    CONSTRAINT deletion_hard_purge_predeces_predecessor_target_receipts__check CHECK (((octet_length(predecessor_target_receipts_digest) = 32) AND (predecessor_target_receipts_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT deletion_hard_purge_predeces_predecessor_tombstone_digest_check CHECK (((octet_length(predecessor_tombstone_digest) = 32) AND (predecessor_tombstone_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT deletion_hard_purge_predecessor_predecessor_vector_digest_check CHECK (((octet_length(predecessor_vector_digest) = 32) AND (predecessor_vector_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT deletion_hard_purge_predecessors_check CHECK ((hard_tombstone_id <> predecessor_tombstone_id))
);


--
-- Name: deletion_target_step_progress; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deletion_target_step_progress (
    tombstone_id uuid NOT NULL,
    project_id uuid NOT NULL,
    target text NOT NULL,
    step_name text NOT NULL,
    step_order integer NOT NULL,
    cursor_kind text NOT NULL,
    state text NOT NULL,
    claim_incarnation bigint,
    claimed_by_digest bytea,
    cursor_uuid uuid,
    cursor_digest bytea,
    processed_count bigint DEFAULT 0 NOT NULL,
    started_at timestamp with time zone,
    updated_at timestamp with time zone,
    completed_at timestamp with time zone,
    CONSTRAINT deletion_target_step_progress_check CHECK (((step_name ~ '^[0-9]{3}:[a-z][a-z0-9_]*:(delete|scrub|detach|assert_absent|finalize)$'::text) AND ("left"(step_name, 3) = lpad((step_order)::text, 3, '0'::text)))),
    CONSTRAINT deletion_target_step_progress_check1 CHECK (((cursor_kind = ANY (ARRAY['none'::text, 'uuid'::text])) AND ((step_name !~~ '%:finalize'::text) OR (cursor_kind = 'none'::text)))),
    CONSTRAINT deletion_target_step_progress_check2 CHECK ((((state = 'pending'::text) AND (claim_incarnation IS NULL) AND (claimed_by_digest IS NULL) AND (cursor_uuid IS NULL) AND (cursor_digest IS NULL) AND (processed_count = 0) AND (started_at IS NULL) AND (updated_at IS NULL) AND (completed_at IS NULL)) OR ((state = 'running'::text) AND (claim_incarnation >= 1) AND (claimed_by_digest IS NOT NULL) AND (octet_length(claimed_by_digest) = 32) AND (claimed_by_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (cursor_digest IS NULL) AND ((cursor_kind = 'uuid'::text) OR (cursor_uuid IS NULL)) AND (started_at IS NOT NULL) AND (updated_at IS NOT NULL) AND (completed_at IS NULL)) OR ((state = 'succeeded'::text) AND (claim_incarnation >= 1) AND (claimed_by_digest IS NOT NULL) AND (octet_length(claimed_by_digest) = 32) AND (claimed_by_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (cursor_uuid IS NULL) AND (cursor_digest IS NOT NULL) AND (octet_length(cursor_digest) = 32) AND (cursor_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (started_at IS NOT NULL) AND (updated_at IS NOT NULL) AND (completed_at IS NOT NULL)))),
    CONSTRAINT deletion_target_step_progress_processed_count_check CHECK ((processed_count >= 0)),
    CONSTRAINT deletion_target_step_progress_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text]))),
    CONSTRAINT deletion_target_step_progress_step_order_check CHECK (((step_order >= 1) AND (step_order <= 999))),
    CONSTRAINT deletion_target_step_progress_target_check CHECK ((target = 'postgresql'::text))
);


--
-- Name: deletion_target_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deletion_target_tasks (
    tombstone_id uuid NOT NULL,
    project_id uuid NOT NULL,
    target text NOT NULL,
    state text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    claim_incarnation bigint DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    claimed_by_digest bytea,
    claim_expires_at timestamp with time zone,
    handler_version text,
    receipt_digest bytea,
    completed_at timestamp with time zone,
    last_reason_code text,
    CONSTRAINT deletion_target_tasks_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT deletion_target_tasks_check CHECK ((((state = ANY (ARRAY['pending'::text, 'failed'::text])) AND (claimed_by_digest IS NULL) AND (claim_expires_at IS NULL) AND (handler_version IS NULL) AND (receipt_digest IS NULL) AND (completed_at IS NULL)) OR ((state = 'processing'::text) AND (claim_incarnation >= 1) AND (claimed_by_digest IS NOT NULL) AND (octet_length(claimed_by_digest) = 32) AND (claimed_by_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (claim_expires_at IS NOT NULL) AND (handler_version IS NULL) AND (receipt_digest IS NULL) AND (completed_at IS NULL)) OR ((state = ANY (ARRAY['succeeded'::text, 'no_data'::text])) AND (claim_incarnation >= 1) AND (claimed_by_digest IS NOT NULL) AND (octet_length(claimed_by_digest) = 32) AND (claimed_by_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (claim_expires_at IS NOT NULL) AND (handler_version IS NOT NULL) AND (receipt_digest IS NOT NULL) AND (octet_length(receipt_digest) = 32) AND (receipt_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (completed_at IS NOT NULL) AND (completed_at = date_trunc('second'::text, completed_at))))),
    CONSTRAINT deletion_target_tasks_claim_incarnation_check CHECK ((claim_incarnation >= 0)),
    CONSTRAINT deletion_target_tasks_handler_version_check CHECK (((handler_version IS NULL) OR (handler_version = ANY (ARRAY['runtime_direct_sdk_v1'::text, 'filesystem_os_root_v1'::text, 'postgres_exact_inventory_v1'::text])))),
    CONSTRAINT deletion_target_tasks_last_reason_code_check CHECK (((last_reason_code IS NULL) OR (last_reason_code = ANY (ARRAY['target_unavailable'::text, 'stale_source'::text, 'expired_lease'::text, 'validation_failed'::text])))),
    CONSTRAINT deletion_target_tasks_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'processing'::text, 'succeeded'::text, 'no_data'::text, 'failed'::text]))),
    CONSTRAINT deletion_target_tasks_target_check CHECK ((target = ANY (ARRAY['runtime'::text, 'filesystem'::text, 'postgresql'::text]))),
    CONSTRAINT deletion_target_tasks_terminal_handler_check CHECK (((state <> ALL (ARRAY['succeeded'::text, 'no_data'::text])) OR ((target = 'runtime'::text) AND (handler_version = 'runtime_direct_sdk_v1'::text)) OR ((target = 'filesystem'::text) AND (state = 'succeeded'::text) AND (handler_version = 'filesystem_os_root_v1'::text)) OR ((target = 'postgresql'::text) AND (state = 'succeeded'::text) AND (handler_version = 'postgres_exact_inventory_v1'::text))))
);


--
-- Name: export_artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.export_artifacts (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    book_id uuid NOT NULL,
    project_generation bigint NOT NULL,
    book_generation bigint NOT NULL,
    chat_session_id uuid,
    chat_lifecycle_epoch bigint,
    format text NOT NULL,
    artifact_kind text NOT NULL,
    object_state text NOT NULL,
    create_operation_id uuid NOT NULL,
    pending_object_key text,
    storage_object_key text NOT NULL,
    expected_content_digest bytea NOT NULL,
    expected_size bigint NOT NULL,
    create_started_at timestamp with time zone NOT NULL,
    published_at timestamp with time zone,
    download_revoked_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    delete_operation_id uuid,
    delete_reason text,
    delete_started_at timestamp with time zone,
    deleted_at timestamp with time zone,
    deletion_receipt_digest bytea,
    delete_tombstone_id uuid,
    delete_project_id uuid,
    delete_vector_digest bytea,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT export_artifacts_artifact_kind_check CHECK ((artifact_kind = 'manuscript_export'::text)),
    CONSTRAINT export_artifacts_book_generation_check CHECK ((book_generation >= 1)),
    CONSTRAINT export_artifacts_check CHECK ((((chat_session_id IS NULL) AND (chat_lifecycle_epoch IS NULL)) OR ((chat_session_id IS NOT NULL) AND (chat_lifecycle_epoch >= 1)))),
    CONSTRAINT export_artifacts_check1 CHECK ((((object_state = 'creating'::text) AND (pending_object_key IS NOT NULL) AND (published_at IS NULL) AND (download_revoked_at IS NULL) AND (delete_operation_id IS NULL) AND (delete_reason IS NULL) AND (delete_started_at IS NULL) AND (deleted_at IS NULL) AND (deletion_receipt_digest IS NULL)) OR ((object_state = 'active'::text) AND (pending_object_key IS NULL) AND (published_at IS NOT NULL) AND (download_revoked_at IS NULL) AND (delete_operation_id IS NULL) AND (delete_reason IS NULL) AND (delete_started_at IS NULL) AND (deleted_at IS NULL) AND (deletion_receipt_digest IS NULL)) OR ((object_state = 'deleting'::text) AND (download_revoked_at IS NOT NULL) AND (delete_operation_id IS NOT NULL) AND (delete_reason IS NOT NULL) AND (delete_started_at IS NOT NULL) AND (deleted_at IS NULL) AND (deletion_receipt_digest IS NULL)) OR ((object_state = 'deleted'::text) AND (download_revoked_at IS NOT NULL) AND (delete_operation_id IS NOT NULL) AND (delete_reason IS NOT NULL) AND (delete_started_at IS NOT NULL) AND (deleted_at IS NOT NULL) AND (deletion_receipt_digest IS NOT NULL) AND (octet_length(deletion_receipt_digest) = 32)))),
    CONSTRAINT export_artifacts_check2 CHECK ((((delete_reason = 'privacy'::text) AND (delete_tombstone_id IS NOT NULL) AND (delete_project_id = project_id) AND (delete_vector_digest IS NOT NULL) AND (octet_length(delete_vector_digest) = 32)) OR ((delete_reason IS DISTINCT FROM 'privacy'::text) AND (delete_tombstone_id IS NULL) AND (delete_project_id IS NULL) AND (delete_vector_digest IS NULL)))),
    CONSTRAINT export_artifacts_delete_reason_check CHECK (((delete_reason IS NULL) OR (delete_reason = ANY (ARRAY['privacy'::text, 'expiry'::text, 'consumed'::text, 'aborted_create'::text])))),
    CONSTRAINT export_artifacts_expected_content_digest_check CHECK (((octet_length(expected_content_digest) = 32) AND (expected_content_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT export_artifacts_expected_size_check CHECK ((expected_size >= 0)),
    CONSTRAINT export_artifacts_format_check CHECK ((format = ANY (ARRAY['docx'::text, 'pdf'::text, 'epub'::text, 'fb2'::text, 'markdown'::text, 'json'::text]))),
    CONSTRAINT export_artifacts_object_state_check CHECK ((object_state = ANY (ARRAY['creating'::text, 'active'::text, 'deleting'::text, 'deleted'::text]))),
    CONSTRAINT export_artifacts_project_generation_check CHECK ((project_generation >= 1))
);


--
-- Name: governance_writer_leases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.governance_writer_leases (
    instance_id_digest bytea NOT NULL,
    capability_version text NOT NULL,
    build_digest bytea NOT NULL,
    lease_expires_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone NOT NULL,
    CONSTRAINT governance_writer_leases_build_digest_check CHECK (((octet_length(build_digest) = 32) AND (build_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT governance_writer_leases_capability_version_check CHECK ((capability_version = 'artifact_sources_v1+lifecycle_v1+claim_incarnation_v1+owner_aware_purge_v1'::text)),
    CONSTRAINT governance_writer_leases_check CHECK ((lease_expires_at > last_seen_at)),
    CONSTRAINT governance_writer_leases_instance_id_digest_check CHECK (((octet_length(instance_id_digest) = 32) AND (instance_id_digest <> decode(repeat('00'::text, 32), 'hex'::text))))
);


--
-- Name: idempotency_records; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.idempotency_records (
    id uuid NOT NULL,
    scope_hash text NOT NULL,
    operation_id text NOT NULL,
    idempotency_key_hash text NOT NULL,
    request_hash text NOT NULL,
    status_code integer NOT NULL,
    response_json jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: import_job_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.import_job_requests (
    job_id uuid NOT NULL,
    project_id uuid NOT NULL,
    actor_user_id uuid NOT NULL,
    storage_key text NOT NULL,
    idempotency_key text NOT NULL,
    request_method text NOT NULL,
    request_path text NOT NULL,
    request_fingerprint text NOT NULL,
    source_file_name text NOT NULL,
    extension text NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL,
    file_sha256 text NOT NULL,
    import_mode text NOT NULL,
    title text,
    can_cancel boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    artifact_source_owner_id uuid,
    status text DEFAULT 'pending'::text NOT NULL,
    governance_payload_state text DEFAULT 'active'::text NOT NULL,
    initial_publication text DEFAULT 'draft_only'::text NOT NULL,
    CONSTRAINT import_job_requests_contract_owner_shape CHECK ((((governance_payload_state = 'active'::text) AND (artifact_source_owner_id IS NOT NULL)) OR ((governance_payload_state = 'source_deleted'::text) AND (artifact_source_owner_id IS NULL) AND (status = 'expired'::text)))),
    CONSTRAINT import_job_requests_governance_payload_state_check CHECK ((governance_payload_state = ANY (ARRAY['active'::text, 'source_deleted'::text]))),
    CONSTRAINT import_job_requests_import_mode_check CHECK ((import_mode = 'new_book'::text)),
    CONSTRAINT import_job_requests_initial_publication_check CHECK ((initial_publication = ANY (ARRAY['draft_only'::text, 'publish_imported'::text]))),
    CONSTRAINT import_job_requests_size_bytes_check CHECK ((size_bytes >= 0)),
    CONSTRAINT import_job_requests_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'succeeded'::text, 'failed'::text, 'canceled'::text, 'expired'::text])))
);


--
-- Name: import_uploads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.import_uploads (
    storage_key text NOT NULL,
    project_id uuid NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    content bytea,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    artifact_id uuid DEFAULT gen_random_uuid() NOT NULL,
    artifact_source_owner_id uuid,
    CONSTRAINT import_uploads_contract_owner_shape CHECK (((content IS NULL) OR (artifact_source_owner_id IS NOT NULL))),
    CONSTRAINT import_uploads_size_bytes_check CHECK ((size_bytes >= 0))
);


--
-- Name: job_model_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.job_model_bindings (
    job_id uuid NOT NULL,
    project_id uuid NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    job_kind text NOT NULL,
    origin text NOT NULL,
    actor_user_id uuid,
    initiating_auth_session_id uuid,
    input_contract_version integer NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT job_model_bindings_check CHECK ((((origin = 'user'::text) AND (actor_user_id IS NOT NULL) AND (initiating_auth_session_id IS NOT NULL)) OR ((origin = 'service'::text) AND (actor_user_id IS NULL) AND (initiating_auth_session_id IS NULL)))),
    CONSTRAINT job_model_bindings_check1 CHECK ((((job_kind = 'canon_book_enrichment'::text) AND (origin = ANY (ARRAY['user'::text, 'service'::text]))) OR ((job_kind = ANY (ARRAY['research_chat_turn'::text, 'writer_agent'::text, 'canon_correction'::text])) AND (origin = 'user'::text)))),
    CONSTRAINT job_model_bindings_input_contract_version_check CHECK ((input_contract_version >= 1)),
    CONSTRAINT job_model_bindings_job_kind_check CHECK ((job_kind = ANY (ARRAY['canon_book_enrichment'::text, 'research_chat_turn'::text, 'writer_agent'::text, 'canon_correction'::text]))),
    CONSTRAINT job_model_bindings_origin_check CHECK ((origin = ANY (ARRAY['user'::text, 'service'::text])))
);


--
-- Name: jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.jobs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    progress_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    result_json jsonb DEFAULT '{}'::jsonb,
    error_json jsonb DEFAULT '{}'::jsonb,
    claimed_at timestamp with time zone,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    cancel_requested_at timestamp with time zone,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    claim_incarnation bigint DEFAULT 0 NOT NULL,
    artifact_source_owner_id uuid,
    governance_payload_state text NOT NULL,
    subject_json jsonb,
    creator_user_id uuid,
    can_cancel boolean NOT NULL,
    queue_class text NOT NULL,
    retry_count integer NOT NULL,
    max_attempts integer NOT NULL,
    next_attempt_at timestamp with time zone NOT NULL,
    CONSTRAINT jobs_claim_incarnation_nonnegative CHECK (((claim_incarnation IS NULL) OR (claim_incarnation >= 0))),
    CONSTRAINT jobs_completed_at_check CHECK (((completed_at IS NULL) OR ((started_at IS NULL) OR (completed_at >= started_at)))),
    CONSTRAINT jobs_contract_owner_shape CHECK ((((governance_payload_state = 'active'::text) AND (artifact_source_owner_id IS NOT NULL)) OR ((governance_payload_state = 'source_deleted'::text) AND (artifact_source_owner_id IS NULL) AND (status = 'expired'::text) AND (progress_json = '{}'::jsonb) AND (subject_json IS NULL) AND (creator_user_id IS NULL) AND (can_cancel = false) AND (result_json IS NULL) AND (error_json IS NULL)))),
    CONSTRAINT jobs_governance_payload_state_check CHECK (((((governance_payload_state = 'active'::text) AND (artifact_source_owner_id IS NOT NULL) AND (jsonb_typeof(subject_json) = 'object'::text) AND ((subject_json ->> 'type'::text) = kind) AND ((subject_json ->> 'projectId'::text) = (project_id)::text) AND ((creator_user_id IS NOT NULL) OR (kind = ANY (ARRAY['canon_text_index'::text, 'canon_book_enrichment'::text])))) OR ((governance_payload_state = 'source_deleted'::text) AND (artifact_source_owner_id IS NULL) AND (status = 'expired'::text) AND (subject_json IS NULL) AND (creator_user_id IS NULL) AND (can_cancel = false) AND (progress_json = '{}'::jsonb) AND (result_json IS NULL) AND (error_json IS NULL))) IS TRUE)),
    CONSTRAINT jobs_kind_check CHECK ((kind = ANY (ARRAY['import'::text, 'export'::text, 'canon_text_index'::text, 'canon_book_enrichment'::text, 'research_chat_turn'::text, 'writer_agent'::text, 'canon_correction'::text]))),
    CONSTRAINT jobs_max_attempts_check CHECK ((max_attempts = 3)),
    CONSTRAINT jobs_next_attempt_check CHECK ((next_attempt_at >= created_at)),
    CONSTRAINT jobs_queue_class_check CHECK ((queue_class = ANY (ARRAY['interactive'::text, 'user_demanded'::text, 'automatic'::text]))),
    CONSTRAINT jobs_queue_kind_check CHECK ((((queue_class = 'interactive'::text) AND (kind = ANY (ARRAY['research_chat_turn'::text, 'writer_agent'::text, 'canon_correction'::text]))) OR ((queue_class = 'automatic'::text) AND (kind = 'canon_book_enrichment'::text)) OR ((queue_class = 'user_demanded'::text) AND (kind = ANY (ARRAY['import'::text, 'export'::text, 'canon_text_index'::text, 'canon_book_enrichment'::text]))))),
    CONSTRAINT jobs_retry_count_check CHECK (((retry_count >= 0) AND (retry_count <= 2))),
    CONSTRAINT jobs_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'canceling'::text, 'succeeded'::text, 'failed'::text, 'canceled'::text, 'expired'::text])))
);


--
-- Name: job_agent_threads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.job_agent_threads (
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    role text NOT NULL,
    thread_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT job_agent_threads_pkey PRIMARY KEY (project_id, job_id, role),
    CONSTRAINT job_agent_threads_project_thread_key UNIQUE (project_id, thread_id),
    CONSTRAINT job_agent_threads_role_check CHECK ((role = ANY (ARRAY['canon_book_enrichment'::text, 'book_enrichment_review_completeness'::text, 'book_enrichment_review_source_citations'::text, 'book_enrichment_review_saga_history'::text, 'writer_agent'::text, 'canon_correction'::text]))),
    CONSTRAINT job_agent_threads_thread_id_check CHECK (((thread_id = btrim(thread_id)) AND (char_length(thread_id) >= 1) AND (char_length(thread_id) <= 255) AND (thread_id !~ '[[:cntrl:]]'::text)))
);


--
-- Name: project_invitations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_invitations (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    inviter_user_id uuid NOT NULL,
    email text NOT NULL,
    email_hash text NOT NULL,
    role text NOT NULL,
    status text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    accepted_at timestamp with time zone,
    canceled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT project_invitations_role_check CHECK ((role = ANY (ARRAY['viewer'::text, 'editor'::text, 'admin'::text]))),
    CONSTRAINT project_invitations_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'accepted'::text, 'canceled'::text, 'expired'::text])))
);


--
-- Name: project_memberships; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_memberships (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    user_id uuid NOT NULL,
    role text NOT NULL,
    removed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT project_memberships_role_check CHECK ((role = ANY (ARRAY['viewer'::text, 'editor'::text, 'admin'::text, 'owner'::text])))
);


--
-- Name: project_source_views; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_source_views (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    project_lifecycle_generation bigint NOT NULL,
    catalog_revision bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT project_source_views_catalog_revision_check CHECK ((catalog_revision >= 1)),
    CONSTRAINT project_source_views_project_lifecycle_generation_check CHECK ((project_lifecycle_generation >= 1)),
    CONSTRAINT project_source_views_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text])))
);


--
-- Name: projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projects (
    id uuid NOT NULL,
    owner_user_id uuid NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    deleted_at timestamp with time zone,
    purge_after timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    lifecycle_generation bigint DEFAULT 1 NOT NULL,
    hard_purge_started_at timestamp with time zone,
    governance_retention_started_at timestamp with time zone,
    purge_deadline timestamp with time zone,
    CONSTRAINT projects_contract_deleted_window CHECK (((deleted_at IS NULL) OR ((governance_retention_started_at IS NOT NULL) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval))))),
    CONSTRAINT projects_governance_retention_shape CHECK ((((deleted_at IS NULL) AND (governance_retention_started_at IS NULL) AND (purge_after IS NULL) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at >= deleted_at) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval)) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at >= deleted_at) AND (purge_after = (governance_retention_started_at + '7 days'::interval)) AND (purge_deadline = (purge_after + '24:00:00'::interval)) AND (hard_purge_started_at IS NOT NULL) AND (hard_purge_started_at >= purge_after)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at IS NULL) AND (purge_after IS NULL) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)) OR ((deleted_at IS NOT NULL) AND (governance_retention_started_at IS NULL) AND (purge_after = deleted_at) AND (purge_deadline IS NULL) AND (hard_purge_started_at IS NULL)))),
    CONSTRAINT projects_lifecycle_generation_positive CHECK ((lifecycle_generation >= 1))
);


--
-- Name: reader_annotations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reader_annotations (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    book_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    paragraph_id uuid,
    paragraph_revision bigint,
    paragraph_variant text,
    kind text NOT NULL,
    locator_json jsonb NOT NULL,
    body_json jsonb NOT NULL,
    status text DEFAULT 'saved'::text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    deleted_at timestamp with time zone,
    purge_after timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reader_annotations_check CHECK ((((paragraph_id IS NULL) AND (paragraph_revision IS NULL) AND (paragraph_variant IS NULL)) OR ((paragraph_id IS NOT NULL) AND (paragraph_revision IS NOT NULL) AND (paragraph_variant IS NOT NULL)))),
    CONSTRAINT reader_annotations_check1 CHECK (((kind <> 'highlight'::text) OR (paragraph_id IS NOT NULL))),
    CONSTRAINT reader_annotations_kind_check CHECK ((kind = ANY (ARRAY['highlight'::text, 'note'::text, 'bookmark'::text]))),
    CONSTRAINT reader_annotations_paragraph_variant_check CHECK (((paragraph_variant IS NULL) OR (paragraph_variant = ANY (ARRAY['draft'::text, 'published'::text])))),
    CONSTRAINT reader_annotations_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'saved'::text, 'deleted'::text])))
);


--
-- Name: runtime_session_deletion_associations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.runtime_session_deletion_associations (
    tombstone_id uuid NOT NULL,
    project_id uuid NOT NULL,
    runtime_session_id uuid CONSTRAINT runtime_session_deletion_associatio_runtime_session_id_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: runtime_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.runtime_sessions (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    job_claim_incarnation bigint NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    origin text NOT NULL,
    actor_user_id uuid,
    initiating_auth_session_id uuid,
    required_capabilities text[] NOT NULL,
    release_id text NOT NULL,
    model text NOT NULL,
    provider text NOT NULL,
    runtime_profile_id text NOT NULL,
    auth_profile_id text NOT NULL,
    role_id text NOT NULL,
    host_id text NOT NULL,
    boot_id uuid NOT NULL,
    runtime_root_id uuid NOT NULL,
    start_token uuid NOT NULL,
    status text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    destroyed_at timestamp with time zone,
    root_skill text,
    native_collaboration boolean,
    skill_digest bytea,
    tool_contract_digest bytea,
    budget_digest bytea,
    CONSTRAINT runtime_sessions_auth_profile_id_check CHECK (((btrim(auth_profile_id) <> ''::text) AND (length(auth_profile_id) <= 255) AND (auth_profile_id !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_boot_id_check CHECK ((boot_id <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT runtime_sessions_check CHECK ((((origin = 'user'::text) AND (actor_user_id IS NOT NULL) AND (initiating_auth_session_id IS NOT NULL)) OR ((origin = 'service'::text) AND (actor_user_id IS NULL) AND (initiating_auth_session_id IS NULL)))),
    CONSTRAINT runtime_sessions_check1 CHECK (((destroyed_at IS NULL) OR (status = 'destroyed'::text))),
    CONSTRAINT runtime_sessions_check2 CHECK (((destroyed_at IS NULL) OR (destroyed_at >= created_at))),
    CONSTRAINT runtime_sessions_host_id_check CHECK (((btrim(host_id) <> ''::text) AND (length(host_id) <= 255) AND (host_id !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_job_claim_incarnation_check CHECK ((job_claim_incarnation >= 1)),
    CONSTRAINT runtime_sessions_model_check CHECK (((btrim(model) <> ''::text) AND (length(model) <= 255) AND (model !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_origin_check CHECK ((origin = ANY (ARRAY['user'::text, 'service'::text]))),
    CONSTRAINT runtime_sessions_provider_check CHECK (((btrim(provider) <> ''::text) AND (length(provider) <= 255) AND (provider !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_release_id_check CHECK (((btrim(release_id) <> ''::text) AND (length(release_id) <= 512) AND (release_id !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_required_capabilities_check CHECK (((required_capabilities = ARRAY['read_governed_data'::text]) OR (required_capabilities = ARRAY['operate_agents'::text]) OR (required_capabilities = ARRAY['read_governed_data'::text, 'operate_agents'::text]))),
    CONSTRAINT runtime_sessions_role_binding_check CHECK ((((status = 'destroyed'::text) AND (root_skill IS NULL) AND (native_collaboration IS NULL) AND (skill_digest IS NULL) AND (tool_contract_digest IS NULL) AND (budget_digest IS NULL)) OR ((root_skill IS NOT NULL) AND (btrim(root_skill) <> ''::text) AND (root_skill = btrim(root_skill)) AND (length(root_skill) <= 255) AND (root_skill !~ '[[:cntrl:]]'::text) AND (native_collaboration IS NOT NULL) AND (skill_digest IS NOT NULL) AND (octet_length(skill_digest) = 32) AND (skill_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (tool_contract_digest IS NOT NULL) AND (octet_length(tool_contract_digest) = 32) AND (tool_contract_digest <> decode(repeat('00'::text, 32), 'hex'::text)) AND (budget_digest IS NOT NULL) AND (octet_length(budget_digest) = 32) AND (budget_digest <> decode(repeat('00'::text, 32), 'hex'::text))))),
    CONSTRAINT runtime_sessions_role_id_check CHECK (((btrim(role_id) <> ''::text) AND (length(role_id) <= 255) AND (role_id !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_runtime_profile_id_check CHECK (((btrim(runtime_profile_id) <> ''::text) AND (length(runtime_profile_id) <= 255) AND (runtime_profile_id !~ E'[\r\n]'::text))),
    CONSTRAINT runtime_sessions_runtime_root_id_check CHECK ((runtime_root_id <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT runtime_sessions_start_token_check CHECK ((start_token <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT runtime_sessions_status_check CHECK ((status = ANY (ARRAY['starting'::text, 'active'::text, 'revoked'::text, 'closed'::text, 'destroyed'::text])))
);


--
-- Name: sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sessions (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    session_hash text NOT NULL,
    csrf_secret_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    rotated_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid NOT NULL,
    email text NOT NULL,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    email_verified_at timestamp with time zone,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Data for Name: artifact_source_owners; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: artifact_source_refs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: audit_logs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: book_source_members; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: books; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_author_rows; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_book_enrichment_inputs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_graph_attempts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_graph_book_slots; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_graph_contributions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_graph_overlays; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_graph_row_evidence; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: canon_graph_rows; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chapter_paragraphs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chapter_source_members; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chapter_text_index_state; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chapters; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chat_sessions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chat_turns; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: deletion_completion_receipts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: deletion_hard_purge_predecessors; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: deletion_target_step_progress; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: deletion_target_tasks; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: deletion_tombstones; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: export_artifacts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: governance_writer_leases; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: idempotency_records; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: import_job_requests; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: import_uploads; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: job_model_bindings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: jobs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: project_invitations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: project_memberships; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: project_source_views; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: projects; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: reader_annotations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: runtime_session_deletion_associations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: runtime_sessions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: sessions; Type: TABLE DATA; Schema: public; Owner: -
--




--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Name: artifact_source_owners artifact_source_owners_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_owners
    ADD CONSTRAINT artifact_source_owners_pkey PRIMARY KEY (id, project_id);


--
-- Name: artifact_source_owners artifact_source_owners_project_id_artifact_kind_artifact_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_owners
    ADD CONSTRAINT artifact_source_owners_project_id_artifact_kind_artifact_id_key UNIQUE (project_id, artifact_kind, artifact_id);


--
-- Name: artifact_source_refs artifact_source_refs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_refs
    ADD CONSTRAINT artifact_source_refs_pkey PRIMARY KEY (id, project_id);


--
-- Name: audit_logs audit_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_logs
    ADD CONSTRAINT audit_logs_pkey PRIMARY KEY (id);


--
-- Name: book_source_members book_source_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.book_source_members
    ADD CONSTRAINT book_source_members_pkey PRIMARY KEY (project_id, source_view, book_id);


--
-- Name: book_source_members book_source_members_project_id_source_view_project_lifecycl_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.book_source_members
    ADD CONSTRAINT book_source_members_project_id_source_view_project_lifecycl_key UNIQUE (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation);


--
-- Name: books books_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.books
    ADD CONSTRAINT books_id_project_id_key UNIQUE (id, project_id);


--
-- Name: books books_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.books
    ADD CONSTRAINT books_pkey PRIMARY KEY (id);


--
-- Name: canon_author_rows canon_author_rows_overlay_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_author_rows
    ADD CONSTRAINT canon_author_rows_overlay_identity_key UNIQUE (project_id, source_view, anchor_book_id, id);


--
-- Name: canon_author_rows canon_author_rows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_author_rows
    ADD CONSTRAINT canon_author_rows_pkey PRIMARY KEY (id);


--
-- Name: canon_author_rows canon_author_rows_project_id_artifact_source_owner_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_author_rows
    ADD CONSTRAINT canon_author_rows_project_id_artifact_source_owner_id_key UNIQUE (project_id, artifact_source_owner_id);


--
-- Name: canon_book_enrichment_inputs canon_book_enrichment_inputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_book_enrichment_inputs
    ADD CONSTRAINT canon_book_enrichment_inputs_pkey PRIMARY KEY (job_id, project_id);


--
-- Name: canon_book_enrichment_steps canon_book_enrichment_steps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_book_enrichment_steps
    ADD CONSTRAINT canon_book_enrichment_steps_pkey PRIMARY KEY (project_id, job_id, step);


--
-- Name: canon_graph_attempts canon_graph_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_attempts
    ADD CONSTRAINT canon_graph_attempts_pkey PRIMARY KEY (project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id);


--
-- Name: canon_graph_attempts canon_graph_attempts_project_id_job_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_attempts
    ADD CONSTRAINT canon_graph_attempts_project_id_job_id_key UNIQUE (project_id, job_id);


--
-- Name: canon_graph_book_slots canon_graph_book_slots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_book_slots
    ADD CONSTRAINT canon_graph_book_slots_pkey PRIMARY KEY (project_id, source_view, book_id);


--
-- Name: canon_graph_contributions canon_graph_contributions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_contributions
    ADD CONSTRAINT canon_graph_contributions_pkey PRIMARY KEY (project_id, source_view, book_id, id);


--
-- Name: canon_graph_contributions canon_graph_contributions_project_id_artifact_source_owner__key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_contributions
    ADD CONSTRAINT canon_graph_contributions_project_id_artifact_source_owner__key UNIQUE (project_id, artifact_source_owner_id);


--
-- Name: canon_graph_overlays canon_graph_overlays_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_overlays
    ADD CONSTRAINT canon_graph_overlays_pkey PRIMARY KEY (project_id, source_view, book_id, target_stable_row_id, target_fingerprint);


--
-- Name: canon_graph_row_evidence canon_graph_row_evidence_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_row_evidence
    ADD CONSTRAINT canon_graph_row_evidence_pkey PRIMARY KEY (project_id, source_view, book_id, contribution_id, stable_row_id, evidence_ordinal);


--
-- Name: canon_graph_row_evidence canon_graph_row_evidence_project_id_source_view_book_id_con_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_row_evidence
    ADD CONSTRAINT canon_graph_row_evidence_project_id_source_view_book_id_con_key UNIQUE (project_id, source_view, book_id, contribution_id, stable_row_id, artifact_source_ref_id);


--
-- Name: canon_graph_rows canon_graph_rows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_rows
    ADD CONSTRAINT canon_graph_rows_pkey PRIMARY KEY (project_id, source_view, book_id, contribution_id, stable_row_id);


--
-- Name: chapter_paragraphs chapter_paragraphs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_paragraphs
    ADD CONSTRAINT chapter_paragraphs_pkey PRIMARY KEY (id, chapter_id, revision, variant);


--
-- Name: chapter_source_members chapter_source_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_source_members
    ADD CONSTRAINT chapter_source_members_pkey PRIMARY KEY (project_id, source_view, chapter_id);


--
-- Name: chapter_text_index_state chapter_text_index_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_text_index_state
    ADD CONSTRAINT chapter_text_index_state_pkey PRIMARY KEY (project_id, source_view, chapter_id);


--
-- Name: chapters chapters_id_book_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_id_book_id_key UNIQUE (id, book_id);


--
-- Name: chapters chapters_id_book_project_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_id_book_project_unique UNIQUE (id, book_id, project_id);


--
-- Name: chapters chapters_id_project_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_id_project_unique UNIQUE (id, project_id);


--
-- Name: chapters chapters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_pkey PRIMARY KEY (id);


--
-- Name: chat_sessions chat_sessions_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_id_project_id_key UNIQUE (id, project_id);


--
-- Name: chat_sessions chat_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_pkey PRIMARY KEY (id);


--
-- Name: chat_sessions chat_sessions_view_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_view_identity_key UNIQUE (id, project_id, source_view);


--
-- Name: chat_turns chat_turns_exact_link_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_exact_link_key UNIQUE (id, chat_id, project_id, source_view);


--
-- Name: chat_turns chat_turns_job_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_job_id_key UNIQUE (job_id);


--
-- Name: chat_turns chat_turns_id_chat_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_id_chat_id_key UNIQUE (id, chat_id);


--
-- Name: chat_turns chat_turns_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_id_project_id_key UNIQUE (id, project_id);


--
-- Name: chat_turns chat_turns_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_pkey PRIMARY KEY (id);


--
-- Name: deletion_completion_receipts deletion_completion_receipts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_completion_receipts
    ADD CONSTRAINT deletion_completion_receipts_pkey PRIMARY KEY (tombstone_id, project_id);


--
-- Name: deletion_completion_receipts deletion_completion_receipts_tombstone_id_project_id_vector_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_completion_receipts
    ADD CONSTRAINT deletion_completion_receipts_tombstone_id_project_id_vector_key UNIQUE (tombstone_id, project_id, vector_digest, target_receipts_digest);


--
-- Name: deletion_hard_purge_predecessors deletion_hard_purge_predecessors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_hard_purge_predecessors
    ADD CONSTRAINT deletion_hard_purge_predecessors_pkey PRIMARY KEY (hard_tombstone_id, project_id, predecessor_tombstone_id);


--
-- Name: deletion_target_step_progress deletion_target_step_progress_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_target_step_progress
    ADD CONSTRAINT deletion_target_step_progress_pkey PRIMARY KEY (tombstone_id, project_id, target, step_name);


--
-- Name: deletion_target_step_progress deletion_target_step_progress_tombstone_id_project_id_targe_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_target_step_progress
    ADD CONSTRAINT deletion_target_step_progress_tombstone_id_project_id_targe_key UNIQUE (tombstone_id, project_id, target, step_order);


--
-- Name: deletion_target_tasks deletion_target_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_target_tasks
    ADD CONSTRAINT deletion_target_tasks_pkey PRIMARY KEY (tombstone_id, project_id, target);


--
-- Name: deletion_tombstones deletion_tombstones_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_tombstones
    ADD CONSTRAINT deletion_tombstones_id_project_id_key UNIQUE (id, project_id);


--
-- Name: deletion_tombstones deletion_tombstones_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_tombstones
    ADD CONSTRAINT deletion_tombstones_pkey PRIMARY KEY (id);


--
-- Name: export_artifacts export_artifacts_job_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_job_id_project_id_key UNIQUE (job_id, project_id);


--
-- Name: export_artifacts export_artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_pkey PRIMARY KEY (id, project_id);


--
-- Name: governance_writer_leases governance_writer_leases_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.governance_writer_leases
    ADD CONSTRAINT governance_writer_leases_pkey PRIMARY KEY (instance_id_digest);


--
-- Name: idempotency_records idempotency_records_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_records
    ADD CONSTRAINT idempotency_records_pkey PRIMARY KEY (id);


--
-- Name: idempotency_records idempotency_records_scope_hash_operation_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_records
    ADD CONSTRAINT idempotency_records_scope_hash_operation_id_idempotency_key_key UNIQUE (scope_hash, operation_id, idempotency_key_hash);


--
-- Name: import_job_requests import_job_requests_actor_user_id_request_method_request_pa_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_actor_user_id_request_method_request_pa_key UNIQUE (actor_user_id, request_method, request_path, idempotency_key);


--
-- Name: import_job_requests import_job_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_pkey PRIMARY KEY (job_id);


--
-- Name: import_uploads import_uploads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_uploads
    ADD CONSTRAINT import_uploads_pkey PRIMARY KEY (storage_key);


--
-- Name: job_model_bindings job_model_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_model_bindings
    ADD CONSTRAINT job_model_bindings_pkey PRIMARY KEY (job_id, project_id);


--
-- Name: jobs jobs_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_id_project_id_key UNIQUE (id, project_id);


--
-- Name: jobs jobs_id_project_kind_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_id_project_kind_key UNIQUE (id, project_id, kind);


--
-- Name: jobs jobs_owner_tuple_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_owner_tuple_unique UNIQUE (id, project_id, artifact_source_owner_id, kind);


--
-- Name: jobs jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_pkey PRIMARY KEY (id);


--
-- Name: project_invitations project_invitations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_invitations
    ADD CONSTRAINT project_invitations_pkey PRIMARY KEY (id);


--
-- Name: project_memberships project_memberships_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_memberships
    ADD CONSTRAINT project_memberships_pkey PRIMARY KEY (id);


--
-- Name: project_memberships project_memberships_project_id_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_memberships
    ADD CONSTRAINT project_memberships_project_id_user_id_key UNIQUE (project_id, user_id);


--
-- Name: project_source_views project_source_views_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_source_views
    ADD CONSTRAINT project_source_views_pkey PRIMARY KEY (project_id, source_view);


--
-- Name: project_source_views project_source_views_project_id_source_view_project_lifecyc_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_source_views
    ADD CONSTRAINT project_source_views_project_id_source_view_project_lifecyc_key UNIQUE (project_id, source_view, project_lifecycle_generation);


--
-- Name: projects projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);


--
-- Name: reader_annotations reader_annotations_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_id_project_id_key UNIQUE (id, project_id);


--
-- Name: reader_annotations reader_annotations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_pkey PRIMARY KEY (id);


--
-- Name: runtime_session_deletion_associations runtime_session_deletion_associations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_session_deletion_associations
    ADD CONSTRAINT runtime_session_deletion_associations_pkey PRIMARY KEY (tombstone_id, project_id, runtime_session_id);


--
-- Name: runtime_sessions runtime_sessions_graph_attempt_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_graph_attempt_key UNIQUE (project_id, job_id, job_claim_incarnation, id);


--
-- Name: runtime_sessions runtime_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_pkey PRIMARY KEY (id, project_id);


--
-- Name: runtime_sessions runtime_sessions_project_id_job_id_job_claim_incarnation_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_project_id_job_id_job_claim_incarnation_key UNIQUE (project_id, job_id, job_claim_incarnation);


--
-- Name: runtime_sessions runtime_sessions_runtime_root_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_runtime_root_id_key UNIQUE (runtime_root_id);


--
-- Name: runtime_sessions runtime_sessions_start_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_start_token_key UNIQUE (start_token);


--
-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);


--
-- Name: sessions sessions_session_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_session_hash_key UNIQUE (session_hash);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: artifact_source_refs_book_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX artifact_source_refs_book_unique ON public.artifact_source_refs USING btree (owner_id, project_id, scope_kind, project_generation, book_id, book_generation, source_view, chat_session_id, chat_lifecycle_epoch) NULLS NOT DISTINCT WHERE ((ref_kind = 'scope'::text) AND (scope_kind = 'book'::text));


--
-- Name: artifact_source_refs_chapter_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX artifact_source_refs_chapter_unique ON public.artifact_source_refs USING btree (owner_id, project_id, scope_kind, project_generation, book_id, book_generation, chapter_id, chapter_generation, source_view, chat_session_id, chat_lifecycle_epoch) NULLS NOT DISTINCT WHERE ((ref_kind = 'scope'::text) AND (scope_kind = 'chapter'::text));


--
-- Name: artifact_source_refs_chapter_text_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX artifact_source_refs_chapter_text_unique ON public.artifact_source_refs USING btree (owner_id, project_id, ref_kind, project_generation, book_id, book_generation, chapter_id, chapter_generation, source_revision, source_view, chat_session_id, chat_lifecycle_epoch) NULLS NOT DISTINCT WHERE (ref_kind = 'chapter'::text);


-- Name: artifact_source_refs_owner_project_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX artifact_source_refs_owner_project_idx ON public.artifact_source_refs USING btree (owner_id, project_id);


--
-- Name: artifact_source_refs_project_independent_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX artifact_source_refs_project_independent_unique ON public.artifact_source_refs USING btree (owner_id, project_id, scope_kind, project_generation, chat_session_id, chat_lifecycle_epoch) NULLS NOT DISTINCT WHERE ((ref_kind = 'scope'::text) AND (scope_kind = 'project'::text) AND (source_independent = true));


--
-- Name: artifact_source_refs_project_view_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX artifact_source_refs_project_view_unique ON public.artifact_source_refs USING btree (owner_id, project_id, scope_kind, project_generation, source_view, chat_session_id, chat_lifecycle_epoch) NULLS NOT DISTINCT WHERE ((ref_kind = 'scope'::text) AND (scope_kind = 'project'::text) AND (source_independent = false));


--
-- Name: book_source_members_current_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX book_source_members_current_idx ON public.book_source_members USING btree (project_id, source_view, project_lifecycle_generation, book_id);


--
-- Name: books_active_project_generation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX books_active_project_generation_idx ON public.books USING btree (project_id, id, lifecycle_generation) WHERE (deleted_at IS NULL);


--
-- Name: books_active_project_order_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX books_active_project_order_idx ON public.books USING btree (project_id, "order", id) WHERE (deleted_at IS NULL);


--
-- Name: books_deleted_project_generation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX books_deleted_project_generation_idx ON public.books USING btree (project_id, id, lifecycle_generation, deleted_at) WHERE (deleted_at IS NOT NULL);


--
-- Name: canon_author_rows_scope_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_author_rows_scope_idx ON public.canon_author_rows USING btree (project_id, source_view, anchor_book_id, id);


--
-- Name: canon_author_rows_search_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_author_rows_search_idx ON public.canon_author_rows USING gin (search_vector);


--
-- Name: canon_graph_contributions_scope_manifest_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_graph_contributions_scope_manifest_idx ON public.canon_graph_contributions USING btree (project_id, source_view, book_id, book_manifest, id);


--
-- Name: canon_graph_rows_contribution_order_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_graph_rows_contribution_order_idx ON public.canon_graph_rows USING btree (project_id, source_view, book_id, contribution_id, stable_row_id);


--
-- Name: canon_graph_rows_exact_lookup_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_graph_rows_exact_lookup_idx ON public.canon_graph_rows USING btree (project_id, source_view, book_id, stable_row_id, effective_fingerprint, contribution_id);


--
-- Name: canon_graph_rows_search_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_graph_rows_search_idx ON public.canon_graph_rows USING gin (search_vector);


--
-- Name: chapter_paragraphs_chapter_revision_variant_order_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chapter_paragraphs_chapter_revision_variant_order_idx ON public.chapter_paragraphs USING btree (chapter_id, revision, variant, "order", id);


--
-- Name: chapter_source_members_order_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chapter_source_members_order_idx ON public.chapter_source_members USING btree (project_id, source_view, book_id, chapter_order, chapter_id);


--
-- Name: chapters_active_book_order_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chapters_active_book_order_idx ON public.chapters USING btree (book_id, "order", id) WHERE (deleted_at IS NULL);


--
-- Name: chapters_active_project_generation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chapters_active_project_generation_idx ON public.chapters USING btree (project_id, id, lifecycle_generation) WHERE (deleted_at IS NULL);


--
-- Name: chapters_deleted_project_generation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chapters_deleted_project_generation_idx ON public.chapters USING btree (project_id, id, lifecycle_generation, deleted_at) WHERE (deleted_at IS NOT NULL);


--
-- Name: chat_sessions_active_project_updated_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chat_sessions_active_project_updated_idx ON public.chat_sessions USING btree (project_id, source_view, updated_at DESC, id DESC) WHERE (deleted_at IS NULL);


--
-- Name: chat_sessions_deleted_project_epoch_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chat_sessions_deleted_project_epoch_idx ON public.chat_sessions USING btree (project_id, id, lifecycle_epoch, deleted_at) WHERE (deleted_at IS NOT NULL);


--
-- Name: chat_sessions_project_thread_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX chat_sessions_project_thread_idx ON public.chat_sessions USING btree (project_id, thread_id) WHERE (thread_id IS NOT NULL);


--
-- Name: chat_turns_chat_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chat_turns_chat_created_idx ON public.chat_turns USING btree (chat_id, created_at DESC, id DESC);


--
-- Name: writer_proposals_chapter_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX writer_proposals_chapter_created_idx ON public.writer_proposals USING btree (project_id, chapter_id, created_at DESC, id DESC);


--
-- Name: canon_correction_proposals_project_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX canon_correction_proposals_project_created_idx ON public.canon_correction_proposals USING btree (project_id, created_at DESC, id DESC);


--
-- Name: deletion_completion_receipts_gc_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_completion_receipts_gc_idx ON public.deletion_completion_receipts USING btree (audit_expires_at, tombstone_id, project_id) WHERE (audit_expires_at IS NOT NULL);


--
-- Name: deletion_hard_purge_predecessors_reverse_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_hard_purge_predecessors_reverse_idx ON public.deletion_hard_purge_predecessors USING btree (predecessor_tombstone_id, project_id, hard_tombstone_id);


--
-- Name: deletion_target_tasks_claim_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_target_tasks_claim_idx ON public.deletion_target_tasks USING btree (next_attempt_at, tombstone_id, project_id, target) WHERE (state = ANY (ARRAY['pending'::text, 'failed'::text]));


--
-- Name: deletion_target_tasks_lease_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_target_tasks_lease_idx ON public.deletion_target_tasks USING btree (claim_expires_at, tombstone_id, project_id, target) WHERE (state = 'processing'::text);


--
-- Name: deletion_tombstones_audit_gc_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_tombstones_audit_gc_idx ON public.deletion_tombstones USING btree (audit_expires_at, id, project_id) WHERE ((status = 'purged'::text) AND (audit_expires_at IS NOT NULL));


--
-- Name: deletion_tombstones_book_vector_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX deletion_tombstones_book_vector_unique ON public.deletion_tombstones USING btree (purge_mode, project_id, project_generation, book_id, book_generation) WHERE (entity_kind = 'book'::text);


--
-- Name: deletion_tombstones_chapter_vector_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX deletion_tombstones_chapter_vector_unique ON public.deletion_tombstones USING btree (purge_mode, project_id, project_generation, book_id, book_generation, chapter_id, chapter_generation) WHERE (entity_kind = 'chapter'::text);


--
-- Name: deletion_tombstones_chat_vector_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX deletion_tombstones_chat_vector_unique ON public.deletion_tombstones USING btree (purge_mode, project_id, chat_session_id, chat_lifecycle_epoch) WHERE (entity_kind = 'chat'::text);


--
-- Name: deletion_tombstones_claim_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_tombstones_claim_idx ON public.deletion_tombstones USING btree (origin, purge_deadline, created_at, id, project_id) WHERE (status = ANY (ARRAY['pending'::text, 'failed'::text]));


--
-- Name: deletion_tombstones_deadline_breach_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deletion_tombstones_deadline_breach_idx ON public.deletion_tombstones USING btree (purge_deadline, id, project_id) WHERE ((origin = 'governed_online'::text) AND (status <> 'purged'::text) AND (retention_deadline_breached_at IS NULL));


--
-- Name: deletion_tombstones_project_vector_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX deletion_tombstones_project_vector_unique ON public.deletion_tombstones USING btree (purge_mode, project_id, project_generation) WHERE (entity_kind = 'project'::text);


--
-- Name: export_artifacts_expiry_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX export_artifacts_expiry_idx ON public.export_artifacts USING btree (expires_at, project_id, id) WHERE (object_state = ANY (ARRAY['creating'::text, 'active'::text]));


--
-- Name: export_artifacts_tombstone_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX export_artifacts_tombstone_idx ON public.export_artifacts USING btree (delete_project_id, delete_tombstone_id, delete_vector_digest, id) WHERE (delete_reason = 'privacy'::text);


--
-- Name: governance_writer_leases_expiry_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX governance_writer_leases_expiry_idx ON public.governance_writer_leases USING btree (lease_expires_at, capability_version, instance_id_digest);


--
-- Name: import_job_requests_project_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX import_job_requests_project_created_idx ON public.import_job_requests USING btree (project_id, created_at DESC);


--
-- Name: import_uploads_project_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX import_uploads_project_created_idx ON public.import_uploads USING btree (project_id, created_at DESC);


--
-- Name: jobs_claimable_priority_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX jobs_claimable_priority_idx ON public.jobs USING btree (queue_class, next_attempt_at, created_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text, 'canceling'::text]));


--
-- Name: jobs_one_nonterminal_enrichment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX jobs_one_nonterminal_enrichment_idx ON public.jobs USING btree (project_id, ((subject_json ->> 'sourceView'::text)), ((subject_json ->> 'bookId'::text))) WHERE ((kind = 'canon_book_enrichment'::text) AND (status = ANY (ARRAY['queued'::text, 'running'::text, 'canceling'::text])));


--
-- Name: jobs_one_queued_text_successor_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX jobs_one_queued_text_successor_idx ON public.jobs USING btree (project_id, ((subject_json ->> 'sourceView'::text))) WHERE ((kind = 'canon_text_index'::text) AND (status = 'queued'::text) AND (claim_incarnation = 0) AND (cancel_requested_at IS NULL));


--
-- Name: project_invitations_active_pending_email_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX project_invitations_active_pending_email_unique ON public.project_invitations USING btree (project_id, email_hash) WHERE ((status = 'pending'::text) AND (accepted_at IS NULL) AND (canceled_at IS NULL));


--
-- Name: project_invitations_email_hash_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX project_invitations_email_hash_created_idx ON public.project_invitations USING btree (email_hash, created_at DESC);


--
-- Name: project_invitations_project_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX project_invitations_project_created_idx ON public.project_invitations USING btree (project_id, created_at DESC);


--
-- Name: projects_active_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projects_active_id_idx ON public.projects USING btree (id) WHERE (deleted_at IS NULL);


--
-- Name: projects_deleted_generation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projects_deleted_generation_idx ON public.projects USING btree (id, lifecycle_generation, deleted_at) WHERE (deleted_at IS NOT NULL);


--
-- Name: runtime_session_deletion_associations_session_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX runtime_session_deletion_associations_session_idx ON public.runtime_session_deletion_associations USING btree (runtime_session_id, project_id, tombstone_id);


--
-- Name: runtime_sessions_host_recovery_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX runtime_sessions_host_recovery_idx ON public.runtime_sessions USING btree (host_id, id) WHERE (destroyed_at IS NULL);


--
-- Name: runtime_sessions_one_non_destroyed_job_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX runtime_sessions_one_non_destroyed_job_idx ON public.runtime_sessions USING btree (project_id, job_id) WHERE (destroyed_at IS NULL);


--
-- Name: writer_proposals writer_proposals_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER writer_proposals_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.writer_proposals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: canon_correction_proposals canon_correction_proposals_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_correction_proposals_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.canon_correction_proposals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: agent_workflow_inputs agent_workflow_inputs_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER agent_workflow_inputs_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.agent_workflow_inputs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: artifact_source_owners artifact_source_owner_artifact_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER artifact_source_owner_artifact_link AFTER INSERT OR UPDATE ON public.artifact_source_owners DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_owner_artifact_link();


--
-- Name: artifact_source_owners artifact_source_owner_currentness; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER artifact_source_owner_currentness AFTER INSERT OR UPDATE ON public.artifact_source_owners DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_active_artifact_source_owner_currentness();


--
-- Name: artifact_source_owners artifact_source_owner_identity_immutable; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER artifact_source_owner_identity_immutable BEFORE UPDATE ON public.artifact_source_owners FOR EACH ROW EXECUTE FUNCTION public.ck_forbid_artifact_source_owner_identity_update();


--
-- Name: artifact_source_owners artifact_source_owner_ref_set; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER artifact_source_owner_ref_set AFTER INSERT OR UPDATE ON public.artifact_source_owners DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_owner_ref_set();


--
-- Name: artifact_source_refs artifact_source_ref_change_mark; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER artifact_source_ref_change_mark AFTER INSERT OR DELETE OR UPDATE ON public.artifact_source_refs FOR EACH ROW EXECUTE FUNCTION public.ck_mark_artifact_source_ref_validation();


--
-- Name: artifact_source_refs artifact_source_ref_currentness; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER artifact_source_ref_currentness AFTER INSERT OR DELETE OR UPDATE ON public.artifact_source_refs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_active_artifact_source_owner_currentness();


--
-- Name: artifact_source_refs artifact_source_ref_owner_set; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER artifact_source_ref_owner_set AFTER INSERT OR DELETE OR UPDATE ON public.artifact_source_refs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_owner_ref_set();


--
-- Name: artifact_source_refs artifact_source_refs_immutable; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER artifact_source_refs_immutable BEFORE UPDATE ON public.artifact_source_refs FOR EACH ROW EXECUTE FUNCTION public.ck_forbid_artifact_source_ref_update();


--
-- Name: audit_logs audit_logs_payload_registry_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER audit_logs_payload_registry_guard AFTER INSERT OR UPDATE ON public.audit_logs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_audit_payload_row();


--
-- Name: audit_logs audit_logs_scrub_terminal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER audit_logs_scrub_terminal BEFORE UPDATE ON public.audit_logs FOR EACH ROW EXECUTE FUNCTION public.ck_audit_scrub_is_terminal();


--
-- Name: books books_lifecycle_commit_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER books_lifecycle_commit_guard AFTER DELETE OR UPDATE ON public.books DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_source_lifecycle_commit_guard();


--
-- Name: books books_restore_hard_purge_fence; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER books_restore_hard_purge_fence BEFORE UPDATE OF deleted_at, hard_purge_started_at ON public.books FOR EACH ROW EXECUTE FUNCTION public.ck_reject_restore_after_hard_purge();


--
-- Name: canon_author_rows canon_author_rows_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_author_rows_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.canon_author_rows DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: canon_book_enrichment_inputs canon_book_enrichment_inputs_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_book_enrichment_inputs_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.canon_book_enrichment_inputs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: canon_graph_book_slots canon_graph_book_slots_contribution_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_book_slots_contribution_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_book_slots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_contribution_slot();


--
-- Name: canon_graph_book_slots canon_graph_book_slots_overlay_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_book_slots_overlay_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_book_slots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_overlay_link();


--
-- Name: canon_graph_contributions canon_graph_contributions_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_contributions_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_contributions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: canon_graph_contributions canon_graph_contributions_slot_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_contributions_slot_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_contributions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_contribution_slot();


--
-- Name: canon_graph_overlays canon_graph_overlays_target_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_overlays_target_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_overlays DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_overlay_link();


--
-- Name: canon_graph_row_evidence canon_graph_row_evidence_row_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_row_evidence_row_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_row_evidence DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_row_evidence();


--
-- Name: canon_graph_rows canon_graph_rows_evidence_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_rows_evidence_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_rows DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_row_evidence();


--
-- Name: canon_graph_rows canon_graph_rows_overlay_seal; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER canon_graph_rows_overlay_seal AFTER INSERT OR DELETE OR UPDATE ON public.canon_graph_rows DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_canon_graph_overlay_link();


--
-- Name: chapters chapters_lifecycle_commit_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER chapters_lifecycle_commit_guard AFTER DELETE OR UPDATE ON public.chapters DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_source_lifecycle_commit_guard();


--
-- Name: chapters chapters_restore_hard_purge_fence; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER chapters_restore_hard_purge_fence BEFORE UPDATE OF deleted_at, hard_purge_started_at ON public.chapters FOR EACH ROW EXECUTE FUNCTION public.ck_reject_restore_after_hard_purge();


--
-- Name: chat_sessions chat_sessions_lifecycle_commit_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER chat_sessions_lifecycle_commit_guard AFTER DELETE OR UPDATE ON public.chat_sessions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_chat_lifecycle_commit_guard();


--
-- Name: chat_turns chat_turns_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER chat_turns_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.chat_turns DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: deletion_completion_receipts deletion_completion_receipts_exact_predecessor_set; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_completion_receipts_exact_predecessor_set AFTER INSERT OR DELETE OR UPDATE ON public.deletion_completion_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_source_hard_purge_predecessor_change();


--
-- Name: deletion_completion_receipts deletion_completion_receipts_exact_retention; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_completion_receipts_exact_retention AFTER INSERT OR UPDATE ON public.deletion_completion_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_deletion_receipt_retention();


--
-- Name: deletion_hard_purge_predecessors deletion_hard_purge_predecessors_exact_set; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_hard_purge_predecessors_exact_set AFTER INSERT OR DELETE OR UPDATE ON public.deletion_hard_purge_predecessors DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_source_hard_purge_predecessor_change();


--
-- Name: deletion_target_step_progress deletion_postgresql_step_order; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_postgresql_step_order AFTER INSERT OR UPDATE OF state ON public.deletion_target_step_progress DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_postgresql_step_order();


--
-- Name: deletion_target_tasks deletion_postgresql_step_registry_complete; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_postgresql_step_registry_complete AFTER INSERT OR UPDATE ON public.deletion_target_tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_postgresql_step_registry();


--
-- Name: deletion_target_tasks deletion_postgresql_target_order; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_postgresql_target_order AFTER UPDATE OF state ON public.deletion_target_tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_postgresql_target_order();


--
-- Name: deletion_target_step_progress deletion_step_claim_matches_task; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_step_claim_matches_task AFTER INSERT OR UPDATE ON public.deletion_target_step_progress DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_step_claim();


--
-- Name: deletion_target_tasks deletion_target_claim_cas; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER deletion_target_claim_cas BEFORE UPDATE ON public.deletion_target_tasks FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_target_claim_cas();


--
-- Name: deletion_target_tasks deletion_target_tasks_runtime_receipt_mode_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER deletion_target_tasks_runtime_receipt_mode_guard BEFORE INSERT OR UPDATE OF state, handler_version ON public.deletion_target_tasks FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_runtime_receipt_mode();


--
-- Name: deletion_target_tasks deletion_task_target_set_complete; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_task_target_set_complete AFTER INSERT OR DELETE OR UPDATE ON public.deletion_target_tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_tombstone_target_set_complete();


--
-- Name: deletion_tombstones deletion_tombstone_target_set_complete; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_tombstone_target_set_complete AFTER INSERT OR UPDATE ON public.deletion_tombstones DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_tombstone_target_set_complete();


--
-- Name: deletion_tombstones deletion_tombstones_exact_predecessor_set; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_tombstones_exact_predecessor_set AFTER INSERT OR DELETE OR UPDATE ON public.deletion_tombstones DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_source_hard_purge_predecessor_change();


--
-- Name: deletion_tombstones deletion_tombstones_exact_retention; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_tombstones_exact_retention AFTER INSERT OR UPDATE ON public.deletion_tombstones DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_deletion_tombstone_retention();


--
-- Name: deletion_tombstones deletion_tombstones_immutable_identity; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER deletion_tombstones_immutable_identity BEFORE UPDATE ON public.deletion_tombstones FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_tombstone_immutability();


--
-- Name: deletion_tombstones deletion_tombstones_validate_live_source; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER deletion_tombstones_validate_live_source AFTER INSERT ON public.deletion_tombstones DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_tombstone_source();


--
-- Name: export_artifacts export_artifacts_closed_transition; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER export_artifacts_closed_transition BEFORE UPDATE ON public.export_artifacts FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_export_artifact_transition();


--
-- Name: export_artifacts export_artifacts_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER export_artifacts_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.export_artifacts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: import_job_requests import_job_requests_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER import_job_requests_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.import_job_requests DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: import_job_requests import_job_requests_staging_shape; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER import_job_requests_staging_shape AFTER INSERT OR UPDATE ON public.import_job_requests DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_import_staging_shape();


--
-- Name: import_uploads import_uploads_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER import_uploads_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.import_uploads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: import_uploads import_uploads_staging_shape; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER import_uploads_staging_shape AFTER INSERT OR UPDATE ON public.import_uploads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_import_staging_shape();


--
-- Name: jobs jobs_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER jobs_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: projects projects_lifecycle_commit_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER projects_lifecycle_commit_guard AFTER DELETE OR UPDATE ON public.projects DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_source_lifecycle_commit_guard();


--
-- Name: projects projects_restore_hard_purge_fence; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projects_restore_hard_purge_fence BEFORE UPDATE OF deleted_at, hard_purge_started_at ON public.projects FOR EACH ROW EXECUTE FUNCTION public.ck_reject_restore_after_hard_purge();


--
-- Name: runtime_sessions runtime_sessions_owner_link; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER runtime_sessions_owner_link AFTER INSERT OR DELETE OR UPDATE ON public.runtime_sessions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.ck_validate_participant_owner_link();


--
-- Name: agent_workflow_inputs agent_workflow_inputs_job_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_workflow_inputs
    ADD CONSTRAINT agent_workflow_inputs_job_fkey FOREIGN KEY (job_id, project_id, job_kind) REFERENCES public.jobs(id, project_id, kind) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: agent_workflow_inputs agent_workflow_inputs_owner_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_workflow_inputs
    ADD CONSTRAINT agent_workflow_inputs_owner_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: agent_workflow_inputs agent_workflow_inputs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_workflow_inputs
    ADD CONSTRAINT agent_workflow_inputs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: writer_proposals writer_proposals_chapter_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.writer_proposals
    ADD CONSTRAINT writer_proposals_chapter_fkey FOREIGN KEY (chapter_id, book_id, project_id) REFERENCES public.chapters(id, book_id, project_id);


--
-- Name: writer_proposals writer_proposals_job_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.writer_proposals
    ADD CONSTRAINT writer_proposals_job_fkey FOREIGN KEY (job_id, project_id, job_kind) REFERENCES public.jobs(id, project_id, kind) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: writer_proposals writer_proposals_owner_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.writer_proposals
    ADD CONSTRAINT writer_proposals_owner_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: writer_proposals writer_proposals_workflow_input_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.writer_proposals
    ADD CONSTRAINT writer_proposals_workflow_input_fkey FOREIGN KEY (workflow_input_id, project_id, job_id, job_kind, id, source_view) REFERENCES public.agent_workflow_inputs(id, project_id, job_id, job_kind, target_id, source_view) ON DELETE SET NULL (workflow_input_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_correction_proposals canon_correction_proposals_book_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_correction_proposals
    ADD CONSTRAINT canon_correction_proposals_book_fkey FOREIGN KEY (anchor_book_id, project_id) REFERENCES public.books(id, project_id);


ALTER TABLE ONLY public.canon_correction_proposals
    ADD CONSTRAINT canon_correction_proposals_job_fkey FOREIGN KEY (job_id, project_id, job_kind) REFERENCES public.jobs(id, project_id, kind) DEFERRABLE INITIALLY DEFERRED;


ALTER TABLE ONLY public.canon_correction_proposals
    ADD CONSTRAINT canon_correction_proposals_owner_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


ALTER TABLE ONLY public.canon_correction_proposals
    ADD CONSTRAINT canon_correction_proposals_workflow_input_fkey FOREIGN KEY (workflow_input_id, project_id, job_id, job_kind, id, source_view) REFERENCES public.agent_workflow_inputs(id, project_id, job_id, job_kind, target_id, source_view) ON DELETE SET NULL (workflow_input_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: artifact_source_owners artifact_source_owners_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_owners
    ADD CONSTRAINT artifact_source_owners_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: artifact_source_refs artifact_source_refs_book_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_refs
    ADD CONSTRAINT artifact_source_refs_book_id_project_id_fkey FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id);


--
-- Name: artifact_source_refs artifact_source_refs_chapter_id_book_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_refs
    ADD CONSTRAINT artifact_source_refs_chapter_id_book_id_project_id_fkey FOREIGN KEY (chapter_id, book_id, project_id) REFERENCES public.chapters(id, book_id, project_id);


--
-- Name: artifact_source_refs artifact_source_refs_chat_session_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_refs
    ADD CONSTRAINT artifact_source_refs_chat_session_id_project_id_fkey FOREIGN KEY (chat_session_id, project_id) REFERENCES public.chat_sessions(id, project_id);


--
-- Name: artifact_source_refs artifact_source_refs_owner_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_refs
    ADD CONSTRAINT artifact_source_refs_owner_id_project_id_fkey FOREIGN KEY (owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) ON DELETE CASCADE;


--
-- Name: artifact_source_refs artifact_source_refs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_source_refs
    ADD CONSTRAINT artifact_source_refs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: audit_logs audit_logs_actor_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_logs
    ADD CONSTRAINT audit_logs_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id);


--
-- Name: audit_logs audit_logs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_logs
    ADD CONSTRAINT audit_logs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: book_source_members book_source_members_book_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.book_source_members
    ADD CONSTRAINT book_source_members_book_id_project_id_fkey FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id) ON DELETE CASCADE;


--
-- Name: book_source_members book_source_members_project_id_source_view_project_lifecyc_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.book_source_members
    ADD CONSTRAINT book_source_members_project_id_source_view_project_lifecyc_fkey FOREIGN KEY (project_id, source_view, project_lifecycle_generation) REFERENCES public.project_source_views(project_id, source_view, project_lifecycle_generation) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: books books_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.books
    ADD CONSTRAINT books_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: canon_author_rows canon_author_rows_anchor_book_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_author_rows
    ADD CONSTRAINT canon_author_rows_anchor_book_id_project_id_fkey FOREIGN KEY (anchor_book_id, project_id) REFERENCES public.books(id, project_id) ON DELETE CASCADE;


--
-- Name: canon_author_rows canon_author_rows_artifact_source_owner_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_author_rows
    ADD CONSTRAINT canon_author_rows_artifact_source_owner_id_project_id_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_book_enrichment_inputs canon_book_enrichment_inputs_owner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_book_enrichment_inputs
    ADD CONSTRAINT canon_book_enrichment_inputs_owner_fk FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_book_enrichment_inputs canon_book_enrichment_inputs_job_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_book_enrichment_inputs
    ADD CONSTRAINT canon_book_enrichment_inputs_job_id_project_id_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_book_enrichment_steps canon_book_enrichment_steps_job_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_book_enrichment_steps
    ADD CONSTRAINT canon_book_enrichment_steps_job_id_project_id_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id) ON DELETE CASCADE;


--
-- Name: canon_graph_attempts canon_graph_attempts_job_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_attempts
    ADD CONSTRAINT canon_graph_attempts_job_id_project_id_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_attempts canon_graph_attempts_project_id_job_id_claim_incarnation_r_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_attempts
    ADD CONSTRAINT canon_graph_attempts_project_id_job_id_claim_incarnation_r_fkey FOREIGN KEY (project_id, job_id, claim_incarnation, runtime_session_id) REFERENCES public.runtime_sessions(project_id, job_id, job_claim_incarnation, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_attempts canon_graph_attempts_project_id_source_view_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_attempts
    ADD CONSTRAINT canon_graph_attempts_project_id_source_view_book_id_fkey FOREIGN KEY (project_id, source_view, book_id) REFERENCES public.book_source_members(project_id, source_view, book_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_attempts canon_graph_attempts_project_id_source_view_book_id_previo_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_attempts
    ADD CONSTRAINT canon_graph_attempts_project_id_source_view_book_id_previo_fkey FOREIGN KEY (project_id, source_view, book_id, previous_contribution_id) REFERENCES public.canon_graph_contributions(project_id, source_view, book_id, id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_book_slots canon_graph_book_slots_job_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_book_slots
    ADD CONSTRAINT canon_graph_book_slots_job_id_project_id_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_book_slots canon_graph_book_slots_project_id_source_view_book_id_acti_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_book_slots
    ADD CONSTRAINT canon_graph_book_slots_project_id_source_view_book_id_acti_fkey FOREIGN KEY (project_id, source_view, book_id, active_contribution_id) REFERENCES public.canon_graph_contributions(project_id, source_view, book_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_book_slots canon_graph_book_slots_project_id_source_view_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_book_slots
    ADD CONSTRAINT canon_graph_book_slots_project_id_source_view_book_id_fkey FOREIGN KEY (project_id, source_view, book_id) REFERENCES public.book_source_members(project_id, source_view, book_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_book_slots canon_graph_book_slots_project_id_source_view_book_id_prev_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_book_slots
    ADD CONSTRAINT canon_graph_book_slots_project_id_source_view_book_id_prev_fkey FOREIGN KEY (project_id, source_view, book_id, previous_contribution_id) REFERENCES public.canon_graph_contributions(project_id, source_view, book_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_contributions canon_graph_contributions_artifact_source_owner_id_project_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_contributions
    ADD CONSTRAINT canon_graph_contributions_artifact_source_owner_id_project_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_contributions canon_graph_contributions_project_id_source_view_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_contributions
    ADD CONSTRAINT canon_graph_contributions_project_id_source_view_book_id_fkey FOREIGN KEY (project_id, source_view, book_id) REFERENCES public.book_source_members(project_id, source_view, book_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_overlays canon_graph_overlays_project_id_source_view_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_overlays
    ADD CONSTRAINT canon_graph_overlays_project_id_source_view_book_id_fkey FOREIGN KEY (project_id, source_view, book_id) REFERENCES public.book_source_members(project_id, source_view, book_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_overlays canon_graph_overlays_project_id_source_view_book_id_replac_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_overlays
    ADD CONSTRAINT canon_graph_overlays_project_id_source_view_book_id_replac_fkey FOREIGN KEY (project_id, source_view, book_id, replacement_author_stable_row_id) REFERENCES public.canon_author_rows(project_id, source_view, anchor_book_id, id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_row_evidence canon_graph_row_evidence_artifact_source_ref_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_row_evidence
    ADD CONSTRAINT canon_graph_row_evidence_artifact_source_ref_id_project_id_fkey FOREIGN KEY (artifact_source_ref_id, project_id) REFERENCES public.artifact_source_refs(id, project_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_row_evidence canon_graph_row_evidence_project_id_source_view_book_id_co_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_row_evidence
    ADD CONSTRAINT canon_graph_row_evidence_project_id_source_view_book_id_co_fkey FOREIGN KEY (project_id, source_view, book_id, contribution_id, stable_row_id) REFERENCES public.canon_graph_rows(project_id, source_view, book_id, contribution_id, stable_row_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: canon_graph_rows canon_graph_rows_project_id_source_view_book_id_contributi_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canon_graph_rows
    ADD CONSTRAINT canon_graph_rows_project_id_source_view_book_id_contributi_fkey FOREIGN KEY (project_id, source_view, book_id, contribution_id) REFERENCES public.canon_graph_contributions(project_id, source_view, book_id, id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: chapter_paragraphs chapter_paragraphs_chapter_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_paragraphs
    ADD CONSTRAINT chapter_paragraphs_chapter_id_fkey FOREIGN KEY (chapter_id) REFERENCES public.chapters(id);


--
-- Name: chapter_source_members chapter_source_members_chapter_id_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_source_members
    ADD CONSTRAINT chapter_source_members_chapter_id_book_id_fkey FOREIGN KEY (chapter_id, book_id) REFERENCES public.chapters(id, book_id) ON DELETE CASCADE;


--
-- Name: chapter_source_members chapter_source_members_chapter_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_source_members
    ADD CONSTRAINT chapter_source_members_chapter_id_project_id_fkey FOREIGN KEY (chapter_id, project_id) REFERENCES public.chapters(id, project_id) ON DELETE CASCADE;


--
-- Name: chapter_source_members chapter_source_members_project_id_source_view_project_life_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_source_members
    ADD CONSTRAINT chapter_source_members_project_id_source_view_project_life_fkey FOREIGN KEY (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation) REFERENCES public.book_source_members(project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;


--
-- Name: chapter_text_index_state chapter_text_index_state_chapter_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapter_text_index_state
    ADD CONSTRAINT chapter_text_index_state_chapter_id_project_id_fkey FOREIGN KEY (chapter_id, project_id) REFERENCES public.chapters(id, project_id) ON DELETE CASCADE;


--
-- Name: chapters chapters_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_book_id_fkey FOREIGN KEY (book_id) REFERENCES public.books(id);


--
-- Name: chapters chapters_book_project_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_book_project_fk FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id);


--
-- Name: chat_sessions chat_sessions_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: chat_sessions chat_sessions_book_project_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_book_project_fkey FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id);


--
-- Name: chat_sessions chat_sessions_project_view_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_project_view_fkey FOREIGN KEY (project_id, source_view) REFERENCES public.project_source_views(project_id, source_view);


--
-- Name: chat_sessions chat_sessions_book_view_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_sessions
    ADD CONSTRAINT chat_sessions_book_view_fkey FOREIGN KEY (project_id, source_view, book_id) REFERENCES public.book_source_members(project_id, source_view, book_id);


--
-- Name: chat_turns chat_turns_chat_view_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_chat_view_fkey FOREIGN KEY (chat_id, project_id, source_view) REFERENCES public.chat_sessions(id, project_id, source_view);


--
-- Name: chat_turns chat_turns_job_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_job_fkey FOREIGN KEY (job_id, project_id, job_kind) REFERENCES public.jobs(id, project_id, kind) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: chat_turns chat_turns_owner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_owner_fk FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: chat_turns chat_turns_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: chat_turns chat_turns_workflow_input_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_turns
    ADD CONSTRAINT chat_turns_workflow_input_fkey FOREIGN KEY (workflow_input_id, project_id, job_id, job_kind, id, source_view) REFERENCES public.agent_workflow_inputs(id, project_id, job_id, job_kind, target_id, source_view) ON DELETE SET NULL (workflow_input_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: deletion_completion_receipts deletion_completion_receipts_tombstone_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_completion_receipts
    ADD CONSTRAINT deletion_completion_receipts_tombstone_id_project_id_fkey FOREIGN KEY (tombstone_id, project_id) REFERENCES public.deletion_tombstones(id, project_id) ON DELETE CASCADE;


--
-- Name: deletion_hard_purge_predecessors deletion_hard_purge_predeces_predecessor_tombstone_id_pro_fkey1; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_hard_purge_predecessors
    ADD CONSTRAINT deletion_hard_purge_predeces_predecessor_tombstone_id_pro_fkey1 FOREIGN KEY (predecessor_tombstone_id, project_id, predecessor_vector_digest, predecessor_target_receipts_digest) REFERENCES public.deletion_completion_receipts(tombstone_id, project_id, vector_digest, target_receipts_digest);


--
-- Name: deletion_hard_purge_predecessors deletion_hard_purge_predecess_hard_tombstone_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_hard_purge_predecessors
    ADD CONSTRAINT deletion_hard_purge_predecess_hard_tombstone_id_project_id_fkey FOREIGN KEY (hard_tombstone_id, project_id) REFERENCES public.deletion_tombstones(id, project_id) ON DELETE CASCADE;


--
-- Name: deletion_hard_purge_predecessors deletion_hard_purge_predecess_predecessor_tombstone_id_pro_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_hard_purge_predecessors
    ADD CONSTRAINT deletion_hard_purge_predecess_predecessor_tombstone_id_pro_fkey FOREIGN KEY (predecessor_tombstone_id, project_id) REFERENCES public.deletion_tombstones(id, project_id);


--
-- Name: deletion_target_step_progress deletion_target_step_progress_tombstone_id_project_id_targ_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_target_step_progress
    ADD CONSTRAINT deletion_target_step_progress_tombstone_id_project_id_targ_fkey FOREIGN KEY (tombstone_id, project_id, target) REFERENCES public.deletion_target_tasks(tombstone_id, project_id, target) ON DELETE CASCADE;


--
-- Name: deletion_target_tasks deletion_target_tasks_tombstone_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deletion_target_tasks
    ADD CONSTRAINT deletion_target_tasks_tombstone_id_project_id_fkey FOREIGN KEY (tombstone_id, project_id) REFERENCES public.deletion_tombstones(id, project_id) ON DELETE CASCADE;


--
-- Name: export_artifacts export_artifacts_artifact_source_owner_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_artifact_source_owner_id_project_id_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: export_artifacts export_artifacts_book_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_book_id_project_id_fkey FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id);


--
-- Name: export_artifacts export_artifacts_chat_session_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_chat_session_id_project_id_fkey FOREIGN KEY (chat_session_id, project_id) REFERENCES public.chat_sessions(id, project_id);


--
-- Name: export_artifacts export_artifacts_delete_tombstone_id_delete_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_delete_tombstone_id_delete_project_id_fkey FOREIGN KEY (delete_tombstone_id, delete_project_id) REFERENCES public.deletion_tombstones(id, project_id);


--
-- Name: export_artifacts export_artifacts_job_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_job_id_project_id_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id);


--
-- Name: export_artifacts export_artifacts_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.export_artifacts
    ADD CONSTRAINT export_artifacts_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: import_job_requests import_job_requests_actor_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id);


--
-- Name: import_job_requests import_job_requests_job_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_job_id_fkey FOREIGN KEY (job_id) REFERENCES public.jobs(id);


--
-- Name: import_job_requests import_job_requests_owner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_owner_fk FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: import_job_requests import_job_requests_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: import_job_requests import_job_requests_storage_key_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_job_requests
    ADD CONSTRAINT import_job_requests_storage_key_fkey FOREIGN KEY (storage_key) REFERENCES public.import_uploads(storage_key);


--
-- Name: import_uploads import_uploads_owner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_uploads
    ADD CONSTRAINT import_uploads_owner_fk FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: import_uploads import_uploads_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.import_uploads
    ADD CONSTRAINT import_uploads_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: job_model_bindings job_model_bindings_actor_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_model_bindings
    ADD CONSTRAINT job_model_bindings_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id);


--
-- Name: job_model_bindings job_model_bindings_initiating_auth_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_model_bindings
    ADD CONSTRAINT job_model_bindings_initiating_auth_session_id_fkey FOREIGN KEY (initiating_auth_session_id) REFERENCES public.sessions(id);


--
-- Name: job_model_bindings job_model_bindings_job_id_project_id_artifact_source_owner_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_model_bindings
    ADD CONSTRAINT job_model_bindings_job_id_project_id_artifact_source_owner_fkey FOREIGN KEY (job_id, project_id, artifact_source_owner_id, job_kind) REFERENCES public.jobs(id, project_id, artifact_source_owner_id, kind) ON DELETE CASCADE;


--
-- Name: job_agent_threads job_agent_threads_job_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_agent_threads
    ADD CONSTRAINT job_agent_threads_job_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id) ON DELETE CASCADE;


--
-- Name: jobs jobs_creator_user_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_creator_user_fk FOREIGN KEY (creator_user_id) REFERENCES public.users(id);


--
-- Name: jobs jobs_owner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_owner_fk FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: jobs jobs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: project_invitations project_invitations_inviter_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_invitations
    ADD CONSTRAINT project_invitations_inviter_user_id_fkey FOREIGN KEY (inviter_user_id) REFERENCES public.users(id);


--
-- Name: project_invitations project_invitations_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_invitations
    ADD CONSTRAINT project_invitations_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: project_memberships project_memberships_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_memberships
    ADD CONSTRAINT project_memberships_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: project_memberships project_memberships_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_memberships
    ADD CONSTRAINT project_memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: project_source_views project_source_views_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_source_views
    ADD CONSTRAINT project_source_views_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: projects projects_owner_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id);


--
-- Name: reader_annotations reader_annotations_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_book_id_fkey FOREIGN KEY (book_id) REFERENCES public.books(id);


--
-- Name: reader_annotations reader_annotations_book_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_book_id_project_id_fkey FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id);


--
-- Name: reader_annotations reader_annotations_chapter_id_book_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_chapter_id_book_id_fkey FOREIGN KEY (chapter_id, book_id) REFERENCES public.chapters(id, book_id);


--
-- Name: reader_annotations reader_annotations_chapter_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_chapter_id_fkey FOREIGN KEY (chapter_id) REFERENCES public.chapters(id);


--
-- Name: reader_annotations reader_annotations_paragraph_id_chapter_id_paragraph_revis_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_paragraph_id_chapter_id_paragraph_revis_fkey FOREIGN KEY (paragraph_id, chapter_id, paragraph_revision, paragraph_variant) REFERENCES public.chapter_paragraphs(id, chapter_id, revision, variant);


--
-- Name: reader_annotations reader_annotations_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reader_annotations
    ADD CONSTRAINT reader_annotations_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: runtime_session_deletion_associations runtime_session_deletion_asso_runtime_session_id_project_i_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_session_deletion_associations
    ADD CONSTRAINT runtime_session_deletion_asso_runtime_session_id_project_i_fkey FOREIGN KEY (runtime_session_id, project_id) REFERENCES public.runtime_sessions(id, project_id);


--
-- Name: runtime_session_deletion_associations runtime_session_deletion_associati_tombstone_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_session_deletion_associations
    ADD CONSTRAINT runtime_session_deletion_associati_tombstone_id_project_id_fkey FOREIGN KEY (tombstone_id, project_id) REFERENCES public.deletion_tombstones(id, project_id) ON DELETE CASCADE;


--
-- Name: runtime_sessions runtime_sessions_actor_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id);


--
-- Name: runtime_sessions runtime_sessions_artifact_source_owner_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_artifact_source_owner_id_project_id_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: runtime_sessions runtime_sessions_initiating_auth_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_initiating_auth_session_id_fkey FOREIGN KEY (initiating_auth_session_id) REFERENCES public.sessions(id);


--
-- Name: runtime_sessions runtime_sessions_job_id_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runtime_sessions
    ADD CONSTRAINT runtime_sessions_job_id_project_id_fkey FOREIGN KEY (job_id, project_id) REFERENCES public.jobs(id, project_id);


--
-- Name: sessions sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: SCHEMA canonkeeper_worker; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA canonkeeper_worker TO canonkeeper_worker_discovery;


--
-- Name: SCHEMA public; Type: ACL; Schema: -; Owner: -
--

REVOKE USAGE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO canonkeeper_governance_owner;
GRANT USAGE ON SCHEMA public TO canonkeeper_product_runtime;


--
-- Name: FUNCTION claim_next_deletion_audit_gc_for_worker(); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_deletion_audit_gc_for_worker() FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_deletion_audit_gc_for_worker() TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION claim_next_deletion_target_task_for_worker(worker_digest bytea, lease_seconds integer, runtime_target_enabled boolean); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_deletion_target_task_for_worker(worker_digest bytea, lease_seconds integer, runtime_target_enabled boolean) FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_deletion_target_task_for_worker(worker_digest bytea, lease_seconds integer, runtime_target_enabled boolean) TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION claim_next_export_intent_for_worker(); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_export_intent_for_worker() FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_export_intent_for_worker() TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION claim_next_import_expiry_for_worker(); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_import_expiry_for_worker() FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_import_expiry_for_worker() TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION claim_next_queued_job_for_worker(wanted_kinds text[]); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[]) FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[]) TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION claim_next_source_hard_purge_for_worker(); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_source_hard_purge_for_worker() FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_source_hard_purge_for_worker() TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION claim_next_tenant_retention_for_worker(); Type: ACL; Schema: canonkeeper_worker; Owner: -
--

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() TO canonkeeper_worker_discovery;


--
-- Name: FUNCTION ck_artifact_kind_tag(kind text); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_artifact_kind_tag(kind text) FROM PUBLIC;


--
-- Name: FUNCTION ck_artifact_manifest_digest_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_artifact_manifest_digest_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) FROM PUBLIC;


--
-- Name: FUNCTION ck_artifact_manifest_projection_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_artifact_manifest_projection_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) FROM PUBLIC;


--
-- Name: FUNCTION ck_artifact_source_owner_is_current(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_artifact_source_owner_is_current(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_artifact_source_owner_is_current(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_artifact_source_owner_is_lifecycle_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_artifact_source_owner_is_lifecycle_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_artifact_source_owner_is_lifecycle_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_artifact_source_ref_projection_v2(ref_kind text, scope_kind text, project_id uuid, project_generation bigint, book_id uuid, book_generation bigint, chapter_id uuid, chapter_generation bigint, source_revision bigint, source_view text, locator_source_generation bytea, content_hash bytea, text_identity_state text, source_independent boolean, chat_session_id uuid, chat_lifecycle_epoch bigint); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_artifact_source_ref_projection_v2(ref_kind text, scope_kind text, project_id uuid, project_generation bigint, book_id uuid, book_generation bigint, chapter_id uuid, chapter_generation bigint, source_revision bigint, source_view text, locator_source_generation bytea, content_hash bytea, text_identity_state text, source_independent boolean, chat_session_id uuid, chat_lifecycle_epoch bigint) FROM PUBLIC;


--
-- Name: FUNCTION ck_enforce_postgresql_target_order(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_enforce_postgresql_target_order() FROM PUBLIC;


--
-- Name: FUNCTION ck_enforce_runtime_receipt_mode(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_enforce_runtime_receipt_mode() FROM PUBLIC;


--
-- Name: FUNCTION ck_expire_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_expire_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_expire_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_heartbeat_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_heartbeat_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_heartbeat_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_jsonb_object_length(candidate jsonb); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_jsonb_object_length(candidate jsonb) FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_jsonb_object_length(candidate jsonb) TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_purge_expired_governance_writer_leases(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_purge_expired_governance_writer_leases() FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_purge_expired_governance_writer_leases() TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_register_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_register_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) FROM PUBLIC;
GRANT ALL ON FUNCTION public.ck_register_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) TO canonkeeper_product_runtime;


--
-- Name: TABLE deletion_tombstones; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.deletion_tombstones TO canonkeeper_governance_owner;


--
-- Name: FUNCTION ck_source_lifecycle_commit_guard(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_source_lifecycle_commit_guard() FROM PUBLIC;


--
-- Name: FUNCTION ck_claim_artifact_source_ref_validation(text, uuid, uuid); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_claim_artifact_source_ref_validation(text, uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_claim_artifact_source_ref_validation(text, uuid, uuid) TO canonkeeper_product_runtime;


--
-- Name: FUNCTION ck_mark_artifact_source_ref_validation(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_mark_artifact_source_ref_validation() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_active_artifact_source_owner_currentness(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_active_artifact_source_owner_currentness() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_canon_graph_contribution_slot(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_canon_graph_contribution_slot() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_canon_graph_overlay_link(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_canon_graph_overlay_link() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_canon_graph_row_evidence(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_canon_graph_row_evidence() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_owner_artifact_link(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_owner_artifact_link() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_owner_ref_set(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_owner_ref_set() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_participant_owner_link(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_participant_owner_link() FROM PUBLIC;


--
-- Name: FUNCTION ck_validate_postgresql_step_registry(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_validate_postgresql_step_registry() FROM PUBLIC;


--
-- Name: FUNCTION ck_writer_lease_identity_is_valid(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.ck_writer_lease_identity_is_valid(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) FROM PUBLIC;


--
-- Name: FUNCTION governance_writer_fleet_fence_key(); Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON FUNCTION public.governance_writer_fleet_fence_key() FROM PUBLIC;


--
-- Name: TABLE agent_workflow_inputs; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,DELETE ON TABLE public.agent_workflow_inputs TO canonkeeper_governance_owner;


GRANT SELECT,DELETE,UPDATE ON TABLE public.writer_proposals TO canonkeeper_governance_owner;


GRANT SELECT,DELETE,UPDATE ON TABLE public.canon_correction_proposals TO canonkeeper_governance_owner;


--
-- Name: TABLE artifact_source_owners; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.artifact_source_owners TO canonkeeper_governance_owner;


--
-- Name: TABLE artifact_source_refs; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.artifact_source_refs TO canonkeeper_governance_owner;


--
-- Name: TABLE audit_logs; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.audit_logs TO canonkeeper_governance_owner;


--
-- Name: TABLE book_source_members; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.book_source_members TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.book_source_members TO canonkeeper_governance_owner;


--
-- Name: TABLE books; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.books TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_author_rows; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_author_rows TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.canon_author_rows TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_book_enrichment_inputs; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_book_enrichment_inputs TO canonkeeper_product_runtime;


--
-- Name: TABLE canon_book_enrichment_steps; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_book_enrichment_steps TO canonkeeper_product_runtime;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_book_enrichment_steps TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_graph_attempts; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_graph_attempts TO canonkeeper_product_runtime;


--
-- Name: TABLE canon_graph_book_slots; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_graph_book_slots TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.canon_graph_book_slots TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_graph_contributions; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_graph_contributions TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.canon_graph_contributions TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_graph_overlays; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE ON TABLE public.canon_graph_overlays TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.canon_graph_overlays TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_graph_row_evidence; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_graph_row_evidence TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.canon_graph_row_evidence TO canonkeeper_governance_owner;


--
-- Name: TABLE canon_graph_rows; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.canon_graph_rows TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.canon_graph_rows TO canonkeeper_governance_owner;


--
-- Name: TABLE chapter_source_members; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.chapter_source_members TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_source_members TO canonkeeper_governance_owner;


--
-- Name: TABLE chapter_text_index_state; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.chapter_text_index_state TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_text_index_state TO canonkeeper_governance_owner;


--
-- Name: TABLE chapters; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.chapters TO canonkeeper_governance_owner;


--
-- Name: TABLE chat_sessions; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.chat_sessions TO canonkeeper_governance_owner;


--
-- Name: TABLE chat_turns; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.chat_turns TO canonkeeper_governance_owner;


--
-- Name: TABLE deletion_completion_receipts; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.deletion_completion_receipts TO canonkeeper_governance_owner;


--
-- Name: TABLE deletion_hard_purge_predecessors; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.deletion_hard_purge_predecessors TO canonkeeper_governance_owner;


--
-- Name: TABLE deletion_target_tasks; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.deletion_target_tasks TO canonkeeper_governance_owner;


--
-- Name: TABLE export_artifacts; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.export_artifacts TO canonkeeper_governance_owner;


--
-- Name: TABLE governance_writer_leases; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.governance_writer_leases TO canonkeeper_governance_owner;


--
-- Name: TABLE import_job_requests; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.import_job_requests TO canonkeeper_governance_owner;


--
-- Name: TABLE import_uploads; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.import_uploads TO canonkeeper_governance_owner;


--
-- Name: TABLE job_model_bindings; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.job_model_bindings TO canonkeeper_product_runtime;


--
-- Name: TABLE job_agent_threads; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE public.job_agent_threads TO canonkeeper_product_runtime;


--
-- Name: TABLE jobs; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.jobs TO canonkeeper_governance_owner;


--
-- Name: TABLE project_memberships; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.project_memberships TO canonkeeper_governance_owner;


--
-- Name: COLUMN project_memberships.role; Type: ACL; Schema: public; Owner: -
--

GRANT UPDATE(role) ON TABLE public.project_memberships TO canonkeeper_governance_owner;


--
-- Name: TABLE project_source_views; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.project_source_views TO canonkeeper_product_runtime;
GRANT SELECT,UPDATE ON TABLE public.project_source_views TO canonkeeper_governance_owner;


--
-- Name: TABLE projects; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.projects TO canonkeeper_governance_owner;


--
-- Name: TABLE reader_annotations; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,UPDATE ON TABLE public.reader_annotations TO canonkeeper_governance_owner;


--
-- Name: TABLE runtime_session_deletion_associations; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.runtime_session_deletion_associations TO canonkeeper_product_runtime;


--
-- Name: TABLE runtime_sessions; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.runtime_sessions TO canonkeeper_product_runtime;


--
-- Права product runtime на таблицы, которые использует текущая composition.
-- Worker schema, governance leases и неподключённые model/graph writers сюда не входят.
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE
    public.artifact_source_owners,
    public.artifact_source_refs,
    public.audit_logs,
    public.books,
    public.chapter_paragraphs,
    public.chapters,
    public.deletion_tombstones,
    public.export_artifacts,
    public.import_job_requests,
    public.import_uploads,
    public.jobs,
    public.project_invitations,
    public.project_memberships,
    public.projects,
    public.reader_annotations
TO canonkeeper_product_runtime;

GRANT SELECT,INSERT,UPDATE ON TABLE
    public.deletion_completion_receipts,
    public.deletion_target_step_progress,
    public.deletion_target_tasks,
    public.idempotency_records,
    public.sessions,
    public.users
TO canonkeeper_product_runtime;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE
    public.agent_workflow_inputs,
    public.writer_proposals,
    public.canon_correction_proposals,
    public.chat_sessions,
    public.chat_turns
TO canonkeeper_product_runtime;

GRANT SELECT,INSERT ON TABLE public.deletion_hard_purge_predecessors
TO canonkeeper_product_runtime;

--
-- Name: chapter_text_search_rows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chapter_text_search_rows (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    source_revision bigint NOT NULL,
    locator_source_generation bytea NOT NULL,
    title text NOT NULL,
    content text NOT NULL,
    chapter_content_hash bytea NOT NULL,
    indexed_at timestamp with time zone NOT NULL,
    search_vector tsvector GENERATED ALWAYS AS (to_tsvector('russian'::regconfig, content)) STORED,
    CONSTRAINT chapter_text_search_rows_pkey PRIMARY KEY (project_id, source_view, chapter_id),
    CONSTRAINT chapter_text_search_rows_chapter_content_hash_check CHECK ((octet_length(chapter_content_hash) = 32)),
    CONSTRAINT chapter_text_search_rows_locator_source_generation_check CHECK ((octet_length(locator_source_generation) = 32)),
    CONSTRAINT chapter_text_search_rows_source_revision_check CHECK ((source_revision >= 1)),
    CONSTRAINT chapter_text_search_rows_source_view_check CHECK ((source_view = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT chapter_text_search_rows_title_check CHECK ((btrim(title) <> ''::text)),
    CONSTRAINT chapter_text_search_rows_chapter_id_project_id_fkey FOREIGN KEY (chapter_id, project_id) REFERENCES public.chapters(id, project_id) ON DELETE CASCADE
);

CREATE INDEX chapter_text_search_rows_search_idx ON public.chapter_text_search_rows USING gin (search_vector);
CREATE INDEX chapter_text_search_rows_book_idx ON public.chapter_text_search_rows USING btree (project_id, source_view, book_id, chapter_id);

GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.chapter_text_search_rows TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_text_search_rows TO canonkeeper_governance_owner;


CREATE TABLE public.chapter_summaries (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    project_lifecycle_generation bigint NOT NULL,
    book_lifecycle_generation bigint NOT NULL,
    chapter_lifecycle_generation bigint NOT NULL,
    source_revision bigint NOT NULL,
    locator_source_generation bytea NOT NULL,
    chapter_content_hash bytea NOT NULL,
    summary_contract_version integer NOT NULL,
    body text NOT NULL,
    body_digest bytea NOT NULL,
    artifact_source_owner_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (project_id, source_view, chapter_id),
    UNIQUE (artifact_source_owner_id),
    CONSTRAINT chapter_summaries_source_view_check CHECK (source_view = ANY (ARRAY['draft'::text, 'published'::text])),
    CONSTRAINT chapter_summaries_generations_check CHECK (project_lifecycle_generation >= 1 AND book_lifecycle_generation >= 1 AND chapter_lifecycle_generation >= 1),
    CONSTRAINT chapter_summaries_source_revision_check CHECK (source_revision >= 1),
    CONSTRAINT chapter_summaries_source_generation_check CHECK (octet_length(locator_source_generation) = 32),
    CONSTRAINT chapter_summaries_content_hash_check CHECK (octet_length(chapter_content_hash) = 32),
    CONSTRAINT chapter_summaries_body_check CHECK (btrim(body) <> ''),
    CONSTRAINT chapter_summaries_body_digest_check CHECK (octet_length(body_digest) = 32),
    CONSTRAINT chapter_summaries_project_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_summaries_book_fkey FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_summaries_chapter_fkey FOREIGN KEY (chapter_id, project_id) REFERENCES public.chapters(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_summaries_owner_fkey FOREIGN KEY (artifact_source_owner_id, project_id) REFERENCES public.artifact_source_owners(id, project_id) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX chapter_summaries_catalog_idx ON public.chapter_summaries (project_id, source_view, book_id, chapter_id);

CREATE CONSTRAINT TRIGGER chapter_summaries_owner_link
AFTER INSERT OR DELETE OR UPDATE ON public.chapter_summaries
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION public.ck_validate_participant_owner_link();

GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.chapter_summaries TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_summaries TO canonkeeper_governance_owner;


CREATE TABLE public.canon_graph_draft_rows (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    job_id uuid NOT NULL,
    claim_incarnation bigint NOT NULL,
    runtime_session_id uuid NOT NULL,
    row_ref uuid NOT NULL,
    row_version bigint NOT NULL,
    row_kind text NOT NULL,
    row_json text NOT NULL,
    evidence_chapter_ids uuid[] NOT NULL,
    deleted boolean DEFAULT false NOT NULL,
    created_revision bigint NOT NULL,
    updated_revision bigint NOT NULL,
    PRIMARY KEY (project_id, job_id, row_ref),
    CONSTRAINT canon_graph_draft_rows_attempt_fkey FOREIGN KEY (project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id)
      REFERENCES public.canon_graph_attempts(project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id) ON DELETE CASCADE,
    CONSTRAINT canon_graph_draft_rows_source_view_check CHECK (source_view = ANY (ARRAY['draft'::text, 'published'::text])),
    CONSTRAINT canon_graph_draft_rows_version_check CHECK (row_version >= 1 AND created_revision >= 1 AND updated_revision >= created_revision),
    CONSTRAINT canon_graph_draft_rows_kind_check CHECK (row_kind = ANY (ARRAY['entity'::text, 'alias'::text, 'fact'::text, 'relationship'::text, 'event'::text, 'event_link'::text, 'character_state'::text, 'dialogue_attribution'::text])),
    CONSTRAINT canon_graph_draft_rows_json_check CHECK (jsonb_typeof(row_json::jsonb) = 'object' AND row_json::jsonb->>'version' = '1' AND row_json::jsonb->>'kind' = row_kind AND jsonb_typeof(row_json::jsonb->'value') = 'object' AND jsonb_typeof(row_json::jsonb->'evidenceChapterIds') = 'array' AND octet_length(row_json) <= 16384),
    CONSTRAINT canon_graph_draft_rows_evidence_check CHECK (cardinality(evidence_chapter_ids) BETWEEN 1 AND 8)
);

CREATE INDEX canon_graph_draft_rows_current_idx ON public.canon_graph_draft_rows (project_id, job_id, row_kind, row_ref) WHERE deleted = false;
GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.canon_graph_draft_rows TO canonkeeper_product_runtime;


CREATE TABLE public.canon_graph_draft_review_reads (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    book_id uuid NOT NULL,
    job_id uuid NOT NULL,
    claim_incarnation bigint NOT NULL,
    runtime_session_id uuid NOT NULL,
    revision bigint NOT NULL,
    row_ref uuid NOT NULL,
    PRIMARY KEY (project_id, job_id, revision, row_ref),
    CONSTRAINT canon_graph_draft_review_reads_attempt_fkey FOREIGN KEY (project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id)
      REFERENCES public.canon_graph_attempts(project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id) ON DELETE CASCADE,
    CONSTRAINT canon_graph_draft_review_reads_row_fkey FOREIGN KEY (project_id, job_id, row_ref)
      REFERENCES public.canon_graph_draft_rows(project_id, job_id, row_ref) ON DELETE CASCADE,
    CONSTRAINT canon_graph_draft_review_reads_revision_check CHECK (revision >= 1)
);

GRANT SELECT,INSERT,DELETE ON TABLE public.canon_graph_draft_review_reads TO canonkeeper_product_runtime;


--
-- PostgreSQL database dump complete
--

-- Владельцы объектов постоянной защищённой границы.
ALTER SCHEMA canonkeeper_worker OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_deletion_audit_gc_for_worker() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_deletion_target_task_for_worker(worker_digest bytea, lease_seconds integer, runtime_target_enabled boolean) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_export_intent_for_worker() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_import_expiry_for_worker() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[]) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_source_hard_purge_for_worker() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_artifact_kind_tag(kind text) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_artifact_manifest_digest_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_artifact_manifest_projection_v2(artifact_kind text, project_id uuid, artifact_id uuid, owner_source_view text, ref_projections bytea[]) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_artifact_source_owner_is_current(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_artifact_source_owner_is_lifecycle_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_artifact_source_ref_projection_v2(ref_kind text, scope_kind text, project_id uuid, project_generation bigint, book_id uuid, book_generation bigint, chapter_id uuid, chapter_generation bigint, source_revision bigint, source_view text, locator_source_generation bytea, content_hash bytea, text_identity_state text, source_independent boolean, chat_session_id uuid, chat_lifecycle_epoch bigint) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_claim_artifact_source_ref_validation(wanted_kind text, wanted_owner uuid, wanted_project uuid) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_enforce_runtime_receipt_mode() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_expire_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_heartbeat_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_jsonb_object_length(candidate jsonb) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_mark_artifact_source_ref_validation() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_purge_expired_governance_writer_leases() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_register_governance_writer_lease(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_active_artifact_source_owner_currentness() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_canon_graph_contribution_slot() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_canon_graph_overlay_link() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_canon_graph_row_evidence() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_owner_artifact_link() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_owner_ref_set() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_validate_participant_owner_link() OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_writer_lease_identity_is_valid(wanted_instance_id_digest bytea, wanted_capability_version text, wanted_build_digest bytea) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.governance_writer_fleet_fence_key() OWNER TO canonkeeper_governance_owner;


CREATE TABLE public.import_queues (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
    created_by_user_id uuid NOT NULL REFERENCES public.users(id),
    idempotency_key text NOT NULL DEFAULT gen_random_uuid()::text CHECK (btrim(idempotency_key) <> ''),
    request_fingerprint text NOT NULL CHECK (btrim(request_fingerprint) <> ''),
    status text NOT NULL CHECK (status IN ('uploading','draft','running','succeeded','failed','canceled','expired')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE (id, project_id)
);

CREATE UNIQUE INDEX import_queues_idempotency_key_idx
ON public.import_queues(project_id, created_by_user_id, idempotency_key);

CREATE UNIQUE INDEX import_queues_one_active_per_project
ON public.import_queues(project_id)
WHERE status IN ('uploading','draft','running','failed');

CREATE TABLE public.import_queue_items (
    id uuid PRIMARY KEY,
    queue_id uuid NOT NULL,
    project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
    client_item_id text NOT NULL CHECK (btrim(client_item_id) <> ''),
    position integer NOT NULL CHECK (position > 0),
    source_file_name text NOT NULL CHECK (btrim(source_file_name) <> ''),
    storage_key text REFERENCES public.import_uploads(storage_key) ON DELETE SET NULL,
    mime_type text,
    size_bytes bigint CHECK (size_bytes >= 0),
    sha256 text,
    detected_book_order integer CHECK (detected_book_order > 0),
    confirmed_book_order integer CHECK (confirmed_book_order > 0),
    preflight_status text NOT NULL CHECK (preflight_status IN ('uploading','ready','invalid')),
    preflight_error_json jsonb,
    import_job_id uuid,
    book_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (queue_id, client_item_id),
    UNIQUE (queue_id, position) DEFERRABLE INITIALLY DEFERRED,
    UNIQUE (queue_id, confirmed_book_order) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (queue_id, project_id) REFERENCES public.import_queues(id, project_id) ON DELETE CASCADE,
    FOREIGN KEY (import_job_id, project_id)
      REFERENCES public.jobs(id, project_id) ON DELETE SET NULL (import_job_id),
    FOREIGN KEY (book_id, project_id)
      REFERENCES public.books(id, project_id) ON DELETE SET NULL (book_id)
);

CREATE INDEX import_queue_items_project_position_idx
ON public.import_queue_items(project_id, queue_id, position);

CREATE CONSTRAINT TRIGGER import_queue_items_staging_shape
AFTER INSERT OR UPDATE ON public.import_queue_items
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION public.ck_validate_import_staging_shape();

GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.import_queues TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.import_queues TO canonkeeper_governance_owner;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.import_queue_items TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.import_queue_items TO canonkeeper_governance_owner;
