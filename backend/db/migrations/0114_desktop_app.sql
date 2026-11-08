-- The data of the desktop build (specs/2026-11-07-desktop-app.md): the consent of the author to build the cards of a
-- saga with the model (DA8), the journal of what went to the AI (DA6), the link of a saga with an Obsidian vault (DA11),
-- the Jobs of the Obsidian export and of the saga file (DA11.2, DA12.2, DA12.3) and the Obsidian source of the
-- materials of the author (DA11.1.4). Every row of a saga belongs to its project: deleting a book, a chapter or a chat
-- keeps it, the project purge removes it in its own transaction (DA6.3, DA15.1).

-- DA8.1, DA8.4: whether the cards Job may call the model for a saga. No row or paused — only what costs nothing. The
-- spending limit is in money when the author set prices, in tokens otherwise, never both; since is the start of the
-- current build, from which the journal counts what was spent.
CREATE TABLE public.card_build_permissions (
    project_id uuid NOT NULL,
    state text NOT NULL,
    budget_usd_micros bigint,
    budget_tokens bigint,
    since timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT card_build_permissions_pkey PRIMARY KEY (project_id),
    CONSTRAINT card_build_permissions_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT card_build_permissions_state_check CHECK (state IN ('allowed', 'paused')),
    CONSTRAINT card_build_permissions_budget_usd_micros_check CHECK (budget_usd_micros IS NULL OR budget_usd_micros > 0),
    CONSTRAINT card_build_permissions_budget_tokens_check CHECK (budget_tokens IS NULL OR budget_tokens > 0),
    CONSTRAINT card_build_permissions_one_budget_check CHECK (budget_usd_micros IS NULL OR budget_tokens IS NULL)
);

REVOKE ALL ON TABLE public.card_build_permissions FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.card_build_permissions TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.card_build_permissions TO canonkeeper_governance_owner;

-- DA5.7, DA6.2: one request of the gateway to a provider, written once when it ends. Only the check of the connection
-- has no saga. The Job is not a foreign key: the journal outlives the Job, and the author clears it himself.
CREATE TABLE public.model_requests (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    at timestamp with time zone NOT NULL,
    project_id uuid,
    job_id uuid,
    purpose text NOT NULL,
    profile_name text NOT NULL,
    provider_host text NOT NULL,
    model text NOT NULL,
    input_tokens bigint,
    output_tokens bigint,
    outcome text NOT NULL,
    duration_ms integer NOT NULL,
    CONSTRAINT model_requests_pkey PRIMARY KEY (id),
    CONSTRAINT model_requests_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT model_requests_purpose_check CHECK (purpose IN (
        'chat_turn', 'chat_scan', 'chapter_card', 'card_pass', 'saga_names', 'probe', 'mcp'
    )),
    CONSTRAINT model_requests_project_check CHECK ((project_id IS NULL) = (purpose = 'probe')),
    CONSTRAINT model_requests_profile_name_check CHECK (char_length(profile_name) BETWEEN 1 AND 100),
    CONSTRAINT model_requests_provider_host_check CHECK (char_length(provider_host) BETWEEN 1 AND 255),
    CONSTRAINT model_requests_model_check CHECK (char_length(model) BETWEEN 1 AND 200),
    CONSTRAINT model_requests_input_tokens_check CHECK (input_tokens IS NULL OR input_tokens >= 0),
    CONSTRAINT model_requests_output_tokens_check CHECK (output_tokens IS NULL OR output_tokens >= 0),
    CONSTRAINT model_requests_outcome_check CHECK (outcome IN ('completed', 'incomplete', 'failed', 'canceled')),
    CONSTRAINT model_requests_duration_ms_check CHECK (duration_ms >= 0)
);

CREATE INDEX model_requests_project_at_idx ON public.model_requests (project_id, at);
CREATE INDEX model_requests_job_idx ON public.model_requests (job_id) WHERE job_id IS NOT NULL;

REVOKE ALL ON TABLE public.model_requests FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.model_requests TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.model_requests TO canonkeeper_governance_owner;

-- DA6.1: what of the saga became the input of a model, written where the manifests of the Job and the turn are written,
-- before the tool returns. The text itself is not here: a chapter is named by its id and its key at the time it went,
-- and the row outlives the chapter, so the chapter is not a foreign key. Only what the MCP tools hand to the author's
-- own client has no Job, and only it names that client.
CREATE TABLE public.model_disclosures (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    at timestamp with time zone NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid,
    purpose text NOT NULL,
    client text,
    chapter_id uuid,
    chapter_key text,
    attachment_number integer,
    paragraphs int4range[],
    what text NOT NULL,
    items integer,
    CONSTRAINT model_disclosures_pkey PRIMARY KEY (id),
    CONSTRAINT model_disclosures_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT model_disclosures_purpose_check CHECK (purpose IN (
        'chat_turn', 'chat_scan', 'chapter_card', 'card_pass', 'saga_names', 'probe', 'mcp'
    )),
    CONSTRAINT model_disclosures_job_check CHECK (job_id IS NOT NULL OR purpose = 'mcp'),
    CONSTRAINT model_disclosures_client_check CHECK (
        (client IS NOT NULL) = (purpose = 'mcp') AND (client IS NULL OR char_length(client) BETWEEN 1 AND 200)
    ),
    CONSTRAINT model_disclosures_what_check CHECK (what IN ('chapter', 'cards', 'attachment', 'names', 'materials', 'memory')),
    CONSTRAINT model_disclosures_chapter_check CHECK (
        (chapter_id IS NOT NULL) = (what IN ('chapter', 'cards')) AND (chapter_key IS NULL) = (chapter_id IS NULL)
    ),
    CONSTRAINT model_disclosures_chapter_key_check CHECK (chapter_key IS NULL OR char_length(chapter_key) BETWEEN 1 AND 32),
    CONSTRAINT model_disclosures_attachment_number_check CHECK (
        (attachment_number IS NOT NULL) = (what = 'attachment') AND (attachment_number IS NULL OR attachment_number > 0)
    ),
    -- No ranges is the whole chapter or attachment.
    CONSTRAINT model_disclosures_paragraphs_check CHECK (
        paragraphs IS NULL OR (
            what IN ('chapter', 'attachment')
            AND cardinality(paragraphs) BETWEEN 1 AND 10000
            AND NOT ('empty'::int4range = ANY (paragraphs))
        )
    ),
    CONSTRAINT model_disclosures_items_check CHECK (
        (items IS NOT NULL) = (what IN ('materials', 'memory')) AND (items IS NULL OR items >= 0)
    )
);

CREATE INDEX model_disclosures_project_at_idx ON public.model_disclosures (project_id, at);
CREATE INDEX model_disclosures_job_idx ON public.model_disclosures (job_id) WHERE job_id IS NOT NULL;

REVOKE ALL ON TABLE public.model_disclosures FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.model_disclosures TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.model_disclosures TO canonkeeper_governance_owner;

-- DA11.0: the Obsidian vault of a saga on this computer — the folder the author chose and the folder of the export
-- inside it. The files of the vault are never touched by the purge of the saga.
CREATE TABLE public.obsidian_vaults (
    project_id uuid NOT NULL,
    vault_path text NOT NULL,
    export_dir text NOT NULL,
    auto_export boolean DEFAULT false NOT NULL,
    last_import_at timestamp with time zone,
    last_export_at timestamp with time zone,
    CONSTRAINT obsidian_vaults_pkey PRIMARY KEY (project_id),
    CONSTRAINT obsidian_vaults_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT obsidian_vaults_vault_path_check CHECK (char_length(vault_path) BETWEEN 1 AND 4096),
    CONSTRAINT obsidian_vaults_export_dir_check CHECK (char_length(export_dir) BETWEEN 1 AND 1024)
);

REVOKE ALL ON TABLE public.obsidian_vaults FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.obsidian_vaults TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.obsidian_vaults TO canonkeeper_governance_owner;

-- DA11.1.4: a note of the vault is a source of the materials of the author too.
ALTER TABLE public.author_materials
    DROP CONSTRAINT author_materials_source_kind_check,
    ADD CONSTRAINT author_materials_source_kind_check CHECK (source_kind IN ('xlsx', 'docx', 'text', 'obsidian'));
REVOKE ALL ON TABLE public.author_materials FROM PUBLIC;
ALTER TABLE public.author_material_imports
    DROP CONSTRAINT author_material_imports_source_kind_check,
    ADD CONSTRAINT author_material_imports_source_kind_check CHECK (source_kind IN ('xlsx', 'docx', 'text', 'obsidian'));
REVOKE ALL ON TABLE public.author_material_imports FROM PUBLIC;

-- DA11.2, DA12.2, DA12.3: the export to Obsidian — asked for by the author or started by the end of a map build when the
-- saga exports by itself — and the saga file out and in, asked for by the author. The automatic export is queued by the
-- cards Job, which has no creator, as the other automatic Jobs; the author's own Jobs keep theirs.
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text,
      'illustration'::text, 'saga_layer'::text, 'chapter_draft'::text, 'author_materials_import'::text,
      'obsidian_export'::text, 'saga_archive_export'::text, 'saga_archive_import'::text
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
        'import'::text, 'canon_text_index'::text, 'author_materials_import'::text,
        'obsidian_export'::text, 'saga_archive_export'::text, 'saga_archive_import'::text
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
          OR (kind = 'obsidian_export'::text AND queue_class = 'automatic'::text)
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

-- The dispatcher claims these Jobs too; everything else is as in 0099.
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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards', 'illustration', 'saga_layer', 'chapter_draft', 'author_materials_import', 'obsidian_export', 'saga_archive_export', 'saga_archive_import')
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
