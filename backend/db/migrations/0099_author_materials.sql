-- The materials of the author (specs/2026-11-04-saga-map.md, UC9): skills, places, characters and the like he worked
-- out apart from his saga. A record is written by him and belongs to the project: deleting a book keeps it, the project
-- purge removes it. One name is one record of a project.
CREATE TABLE public.author_materials (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    name text NOT NULL,
    name_key text NOT NULL,
    kind text NOT NULL,
    description text DEFAULT '' NOT NULL,
    properties jsonb DEFAULT '[]'::jsonb NOT NULL,
    section text DEFAULT '' NOT NULL,
    source_name text DEFAULT '' NOT NULL,
    source_kind text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_by uuid NOT NULL,
    updated_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT author_materials_pkey PRIMARY KEY (id),
    CONSTRAINT author_materials_project_name_key UNIQUE (project_id, name_key),
    CONSTRAINT author_materials_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT author_materials_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id),
    CONSTRAINT author_materials_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id),
    CONSTRAINT author_materials_kind_check CHECK (kind IN ('skill', 'place', 'character', 'item', 'faction', 'other')),
    CONSTRAINT author_materials_name_check CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    CONSTRAINT author_materials_name_key_check CHECK (char_length(name_key) BETWEEN 1 AND 400),
    CONSTRAINT author_materials_description_check CHECK (char_length(description) <= 8000),
    CONSTRAINT author_materials_properties_check CHECK (jsonb_typeof(properties) = 'array' AND jsonb_array_length(properties) <= 50),
    CONSTRAINT author_materials_section_check CHECK (char_length(section) <= 200),
    CONSTRAINT author_materials_source_name_check CHECK (char_length(source_name) <= 255),
    CONSTRAINT author_materials_source_kind_check CHECK (source_kind IN ('xlsx', 'docx', 'text')),
    CONSTRAINT author_materials_revision_check CHECK (revision >= 1)
);

CREATE INDEX author_materials_created_by_idx ON public.author_materials (created_by);
CREATE INDEX author_materials_updated_by_idx ON public.author_materials (updated_by);

REVOKE ALL ON TABLE public.author_materials FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.author_materials TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.author_materials TO canonkeeper_governance_owner;

-- An import the author has not answered yet (R9.2): what his file holds and the candidates of its Job. The text of the
-- file lives here and not in the Job, so answering it or starting another one removes the text. One per project.
CREATE TABLE public.author_material_imports (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    source_name text DEFAULT '' NOT NULL,
    source_kind text NOT NULL,
    parsed jsonb NOT NULL,
    candidates jsonb,
    created_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT author_material_imports_pkey PRIMARY KEY (id),
    CONSTRAINT author_material_imports_project_key UNIQUE (project_id),
    CONSTRAINT author_material_imports_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT author_material_imports_job_fkey FOREIGN KEY (job_id, project_id)
        REFERENCES public.jobs(id, project_id) ON DELETE CASCADE,
    CONSTRAINT author_material_imports_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id),
    CONSTRAINT author_material_imports_source_name_check CHECK (char_length(source_name) <= 255),
    CONSTRAINT author_material_imports_source_kind_check CHECK (source_kind IN ('xlsx', 'docx', 'text')),
    CONSTRAINT author_material_imports_candidates_check CHECK (candidates IS NULL OR jsonb_typeof(candidates) = 'array')
);

CREATE INDEX author_material_imports_job_idx ON public.author_material_imports (job_id, project_id);
CREATE INDEX author_material_imports_created_by_idx ON public.author_material_imports (created_by);

REVOKE ALL ON TABLE public.author_material_imports FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.author_material_imports TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.author_material_imports TO canonkeeper_governance_owner;

-- The Job of an import: direct model calls that map the sections of a file. A person asked for it, nobody stands by it.
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
      OR (queue_class = 'automatic'::text AND kind = ANY (ARRAY['chapter_cards'::text, 'saga_layer'::text]))
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'canon_text_index'::text, 'author_materials_import'::text
      ]))
    );
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;

ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check
    CHECK (source IN ('chat', 'scan', 'cards', 'images', 'layer', 'draft', 'drawing', 'draft_helper', 'voice', 'author_materials'));
REVOKE ALL ON public.model_usage_days FROM PUBLIC;

-- The dispatcher claims these Jobs too; everything else is as in 0085.
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
