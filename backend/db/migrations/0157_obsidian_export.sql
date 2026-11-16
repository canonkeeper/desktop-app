-- The export to Obsidian (specs/2026-11-15-desk-obsidian-export.md OE3.1) is an operation of the bridge that writes the folder
-- of the export at once, without a Job and without an export by itself: the kind obsidian_export 0114 kept for it goes from
-- the checks of the Jobs and from the kinds a worker claims, and the columns of the link no code reads — auto_export and
-- last_import_at — go; export_dir and last_export_at are used. No code ever queued a Job of the kind; the Jobs of the other
-- kinds stay as they are, in any status.
ALTER TABLE public.obsidian_vaults
    DROP COLUMN auto_export,
    DROP COLUMN last_import_at;

ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text,
      'illustration'::text, 'saga_layer'::text, 'chapter_draft'::text, 'author_materials_import'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = ANY (ARRAY[
        'research_chat_turn'::text, 'illustration'::text, 'chapter_draft'::text
      ]))
      OR (queue_class = 'automatic'::text AND kind = ANY (ARRAY[
        'chapter_cards'::text, 'saga_layer'::text
      ]))
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'canon_text_index'::text, 'author_materials_import'::text
      ]))
    ),
    DROP CONSTRAINT jobs_governance_payload_state_check,
    ADD CONSTRAINT jobs_governance_payload_state_check CHECK ((
      (
        governance_payload_state = 'active'::text
        AND artifact_source_owner_id IS NOT NULL
        AND jsonb_typeof(subject_json) = 'object'::text
        AND (subject_json ->> 'type'::text) = kind
        AND (subject_json ->> 'projectId'::text) = (project_id)::text
        AND (
          creator_user_id IS NOT NULL
          OR kind = ANY (ARRAY['canon_text_index'::text, 'chapter_cards'::text, 'saga_layer'::text])
        )
      )
      OR (
        governance_payload_state = 'source_deleted'::text
        AND artifact_source_owner_id IS NULL
        AND status = 'expired'::text
        AND subject_json IS NULL
        AND creator_user_id IS NULL
        AND can_cancel = false
        AND progress_json = '{}'::jsonb
        AND result_json IS NULL
        AND error_json IS NULL
      )
    ) IS TRUE);
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;

-- The claim of a worker as in 0146, without the kind of the export.
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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards', 'illustration', 'saga_layer', 'chapter_draft', 'author_materials_import')
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
