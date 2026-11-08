-- Saga illustrations (specs/2026-10-29-saga-illustrations.md, UC1–UC5): the gallery of a
-- project, the reference images of glossary entries, the saga style and the illustration Job.

-- UC3, R3.3: the saga style put into every drawing, and the counter of the short image numbers
-- (K): a number is never given twice, also after its image is deleted.
CREATE TABLE public.project_illustration_settings (
  project_id uuid PRIMARY KEY REFERENCES public.projects(id) ON DELETE CASCADE,
  style text CHECK (style IS NULL OR char_length(style) BETWEEN 1 AND 500),
  last_image_number integer NOT NULL DEFAULT 0 CHECK (last_image_number >= 0),
  updated_by uuid REFERENCES public.users(id),
  updated_at timestamptz NOT NULL
);

-- UC1, R1.4: one drawing of the gallery. The files are
-- <file_storage_dir>/illustrations/<project_id>/<id>.png and <id>.thumb.jpg. The chat and the turn
-- that drew it are kept as plain ids: deleting the chat or a book keeps the gallery.
CREATE TABLE public.saga_images (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
  number integer NOT NULL CHECK (number >= 1),
  status text NOT NULL CHECK (status IN ('drawing', 'ready', 'failed', 'refused')),
  kind text NOT NULL CHECK (kind IN ('character', 'place', 'scene', 'item', 'other')),
  aspect text NOT NULL CHECK (aspect IN ('square', 'landscape', 'portrait')),
  prompt text NOT NULL CHECK (char_length(btrim(prompt)) BETWEEN 1 AND 4000),
  revised_prompt text CHECK (revised_prompt IS NULL OR char_length(revised_prompt) <= 8000),
  style text CHECK (style IS NULL OR char_length(style) BETWEEN 1 AND 500),
  chapter_key text CHECK (chapter_key IS NULL OR char_length(chapter_key) BETWEEN 1 AND 32),
  chapter_order integer CHECK (chapter_order IS NULL OR chapter_order >= 1),
  subjects text[] NOT NULL DEFAULT '{}',
  subject_names text[] NOT NULL DEFAULT '{}',
  base_image_id uuid,
  reference_image_ids uuid[] NOT NULL DEFAULT '{}',
  chat_id uuid,
  turn_id uuid,
  job_id uuid NOT NULL,
  created_by uuid REFERENCES public.users(id),
  width integer CHECK (width IS NULL OR width > 0),
  height integer CHECK (height IS NULL OR height > 0),
  bytes bigint CHECK (bytes IS NULL OR bytes > 0),
  model text NOT NULL CHECK (model = btrim(model) AND char_length(model) BETWEEN 1 AND 120),
  error_code text CHECK (error_code IS NULL OR error_code IN ('draw_refused', 'draw_failed')),
  error_message text CHECK (error_message IS NULL OR char_length(error_message) <= 500),
  created_at timestamptz NOT NULL,
  ready_at timestamptz,
  CONSTRAINT saga_images_project_number_key UNIQUE (project_id, number),
  CONSTRAINT saga_images_project_id_key UNIQUE (project_id, id),
  CONSTRAINT saga_images_chapter_check CHECK ((chapter_key IS NULL) = (chapter_order IS NULL)),
  CONSTRAINT saga_images_subjects_check CHECK (
    cardinality(subjects) <= 5 AND cardinality(subjects) = cardinality(subject_names)
  ),
  CONSTRAINT saga_images_references_check CHECK (cardinality(reference_image_ids) <= 5),
  CONSTRAINT saga_images_ready_check CHECK (
    (status = 'ready') = (width IS NOT NULL AND height IS NOT NULL AND bytes IS NOT NULL AND ready_at IS NOT NULL)
  ),
  CONSTRAINT saga_images_error_check CHECK (
    (status = 'refused' AND error_code = 'draw_refused')
    OR (status = 'failed' AND error_code = 'draw_failed')
    OR (status IN ('drawing', 'ready') AND error_code IS NULL AND error_message IS NULL)
  ),
  CONSTRAINT saga_images_base_image_fkey FOREIGN KEY (project_id, base_image_id)
    REFERENCES public.saga_images(project_id, id) ON DELETE SET NULL (base_image_id)
);
CREATE INDEX saga_images_project_created_idx ON public.saga_images USING btree (project_id, number DESC);
CREATE INDEX saga_images_subjects_idx ON public.saga_images USING gin (subjects);

-- UC2, R2.1: a reference image of a glossary entry, valid from a chapter on (its reading-order
-- number at the time; 1 — from the start). Deleting the image removes its references.
CREATE TABLE public.saga_image_references (
  project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
  subject_key text NOT NULL CHECK (char_length(subject_key) BETWEEN 1 AND 200),
  subject_name text NOT NULL CHECK (char_length(btrim(subject_name)) BETWEEN 1 AND 200),
  from_chapter integer NOT NULL CHECK (from_chapter >= 1),
  from_chapter_key text CHECK (from_chapter_key IS NULL OR char_length(from_chapter_key) BETWEEN 1 AND 32),
  image_id uuid NOT NULL,
  created_by uuid REFERENCES public.users(id),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (project_id, subject_key, from_chapter),
  CONSTRAINT saga_image_references_image_fkey FOREIGN KEY (project_id, image_id)
    REFERENCES public.saga_images(project_id, id) ON DELETE CASCADE
);
CREATE INDEX saga_image_references_image_idx ON public.saga_image_references USING btree (project_id, image_id);

REVOKE ALL ON TABLE public.project_illustration_settings FROM PUBLIC;
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
REVOKE ALL ON TABLE public.saga_image_references FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.project_illustration_settings TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_images TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_image_references TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.project_illustration_settings TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.saga_images TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.saga_image_references TO canonkeeper_governance_owner;

-- R1.3: a drawing is an interactive Job the chat turn waits for.
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text,
      'illustration'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = ANY (ARRAY['research_chat_turn'::text, 'illustration'::text]))
      OR (queue_class = 'automatic'::text AND kind = 'chapter_cards'::text)
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'canon_text_index'::text
      ]))
    );
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;

-- UC5, R5.1, R5.2: drawings per Moscow day and their model spend.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN images_per_day integer CHECK (images_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN images integer NOT NULL DEFAULT 0 CHECK (images >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;
ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check CHECK (source IN ('chat', 'scan', 'cards', 'images'));
REVOKE ALL ON public.model_usage_days FROM PUBLIC;

-- The dispatcher claims illustration Jobs too; everything else is as in 0056.
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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards', 'illustration')
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
