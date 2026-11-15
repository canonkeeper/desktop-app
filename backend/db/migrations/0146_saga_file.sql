-- The saga file (specs/2026-11-13-desk-sagafile.md): a saga moves with its ready memory into a new saga.
--
-- SF4.1: a record of the author's memory that came from a saga file keeps where it came from — a chat, the helper of a
-- chapter or of a drawing — when its Job is not in this saga. A record written on the page or by a Job of this saga
-- keeps none.
ALTER TABLE public.author_memory
    ADD COLUMN carried_origin text,
    ADD CONSTRAINT author_memory_carried_origin_check CHECK (carried_origin IN ('chat', 'draft', 'drawing'));

-- SF3.8: the chats a saga file brought are records of the saga to read: the title, the dates, the turns of the author
-- who saved the file (question, answer, outcome, the title of the attached chapter) and how many turns of other members
-- it left out. No thread of the model stands behind them, so nobody continues them and no model reads them. They go
-- with the saga.
CREATE TABLE public.saga_transcripts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    title text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    last_turn_at timestamp with time zone NOT NULL,
    others_turns integer NOT NULL,
    turns jsonb NOT NULL,
    CONSTRAINT saga_transcripts_pkey PRIMARY KEY (id),
    CONSTRAINT saga_transcripts_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_transcripts_title_check CHECK (char_length(title) BETWEEN 1 AND 180),
    CONSTRAINT saga_transcripts_others_turns_check CHECK (others_turns >= 0),
    CONSTRAINT saga_transcripts_turns_check CHECK (jsonb_typeof(turns) = 'array'),
    CONSTRAINT saga_transcripts_dates_check CHECK (last_turn_at >= created_at)
);

CREATE INDEX saga_transcripts_project_idx ON public.saga_transcripts USING btree (project_id, last_turn_at DESC, id DESC);

REVOKE ALL ON TABLE public.saga_transcripts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_transcripts TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_transcripts TO canonkeeper_governance_owner;

-- SF5.1: the saga file is written and opened at once, without a Job; the kinds 0114 kept for it go. Nothing else of the
-- kinds and classes changes.
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text,
      'illustration'::text, 'saga_layer'::text, 'chapter_draft'::text, 'author_materials_import'::text,
      'obsidian_export'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = ANY (ARRAY[
        'research_chat_turn'::text, 'illustration'::text, 'chapter_draft'::text
      ]))
      OR (queue_class = 'automatic'::text AND kind = ANY (ARRAY[
        'chapter_cards'::text, 'saga_layer'::text, 'obsidian_export'::text
      ]))
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'canon_text_index'::text, 'author_materials_import'::text, 'obsidian_export'::text
      ]))
    );
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;

-- The claim of a worker as in 0136, without the kinds of the saga file.
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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards', 'illustration', 'saga_layer', 'chapter_draft', 'author_materials_import', 'obsidian_export')
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
