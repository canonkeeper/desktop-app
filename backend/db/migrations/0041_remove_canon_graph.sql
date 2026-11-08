-- Chapter cards replace the canon graph (specs/2026-10-09-saga-memory.md, UC5). The book
-- enrichment agent, its three reviewers, the graph, chapter summaries and author canon rows
-- leave the product together with their Jobs, private inputs, tool call records, model-facing
-- refs and source manifests; the Job, binding and artifact kinds narrow to the flows that
-- remain. The governance triggers are redefined first without the branches that name the
-- dropped tables. Native Codex threads of the indexing flow stay in its former CODEX_HOME,
-- which the operator removes together with that home.

CREATE OR REPLACE FUNCTION public.ck_validate_owner_ref_set()
 RETURNS trigger
 LANGUAGE plpgsql
 SECURITY DEFINER
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
        AND input.job_kind = 'research_chat_turn'
    )
    ELSE false
  END;

  IF owner_row.artifact_kind IN ('chat_turn', 'agent_workflow_input') THEN
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
    IF owner_row.source_identity_state <> 'scope_only'
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

CREATE OR REPLACE FUNCTION public.ck_validate_owner_artifact_link()
 RETURNS trigger
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

  IF NEW.artifact_kind = 'job' THEN
    SELECT EXISTS (SELECT 1 FROM public.jobs a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'import_upload' THEN
    SELECT EXISTS (SELECT 1 FROM public.import_uploads a WHERE a.artifact_id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'import_job_request' THEN
    SELECT EXISTS (SELECT 1 FROM public.import_job_requests a WHERE a.job_id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
  ELSIF NEW.artifact_kind = 'chat_turn' THEN
    SELECT EXISTS (SELECT 1 FROM public.chat_turns a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id AND a.source_view IS NOT DISTINCT FROM NEW.source_view) INTO present;
  ELSIF NEW.artifact_kind = 'export_artifact' THEN
    SELECT EXISTS (SELECT 1 FROM public.export_artifacts a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
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

CREATE OR REPLACE FUNCTION public.ck_validate_participant_owner_link()
 RETURNS trigger
 LANGUAGE plpgsql
 SECURITY DEFINER
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

  IF TG_TABLE_NAME = 'jobs' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'job';
  ELSIF TG_TABLE_NAME = 'import_uploads' THEN
    wanted_artifact_id := NULLIF(row_data->>'artifact_id', '')::uuid; old_artifact_id := NULLIF(old_data->>'artifact_id', '')::uuid; expected_kind := 'import_upload';
  ELSIF TG_TABLE_NAME = 'import_job_requests' THEN
    wanted_artifact_id := NULLIF(row_data->>'job_id', '')::uuid; old_artifact_id := NULLIF(old_data->>'job_id', '')::uuid; expected_kind := 'import_job_request';
  ELSIF TG_TABLE_NAME = 'chat_turns' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'chat_turn';
  ELSIF TG_TABLE_NAME = 'export_artifacts' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'export_artifact';
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
        TG_TABLE_NAME NOT IN ('chat_turns', 'agent_workflow_inputs')
        OR (
          TG_TABLE_NAME IN ('chat_turns', 'agent_workflow_inputs')
          AND owner.source_view IS NOT DISTINCT FROM NULLIF(row_data->>'source_view', '')
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

CREATE TEMPORARY TABLE ck_removed_enrichment_owner (project_id uuid NOT NULL, id uuid NOT NULL) ON COMMIT DROP;

-- Owners are selected by kind, not through live rows: owners fenced by a source deletion keep
-- no artifact row, and jobs tombstoned by a source deletion keep no owner reference.
INSERT INTO ck_removed_enrichment_owner (project_id, id)
SELECT project_id, id FROM public.artifact_source_owners
WHERE artifact_kind IN ('canon_book_enrichment_input', 'chapter_summary', 'canon_author_row', 'canon_graph_contribution')
UNION
SELECT owner.project_id, owner.id
FROM public.artifact_source_owners AS owner
JOIN public.jobs AS job ON job.project_id = owner.project_id AND job.id = owner.artifact_id
WHERE owner.artifact_kind = 'job' AND job.kind = 'canon_book_enrichment';

DROP TABLE public.canon_graph_draft_review_reads, public.canon_graph_draft_rows,
  public.canon_graph_row_evidence, public.canon_graph_rows, public.canon_graph_book_slots,
  public.canon_graph_attempts, public.canon_graph_overlays, public.canon_graph_contributions,
  public.canon_author_rows, public.chapter_summaries, public.canon_book_enrichment_inputs,
  public.agent_tool_calls, public.agent_tool_refs;

DELETE FROM public.job_model_bindings WHERE job_kind = 'canon_book_enrichment';
DELETE FROM public.jobs WHERE kind = 'canon_book_enrichment';
DELETE FROM public.artifact_source_refs AS ref
USING ck_removed_enrichment_owner AS removed
WHERE ref.project_id = removed.project_id AND ref.owner_id = removed.id;
DELETE FROM public.artifact_source_owners AS owner
USING ck_removed_enrichment_owner AS removed
WHERE owner.project_id = removed.project_id AND owner.id = removed.id;

-- The PostgreSQL deletion registry loses its enrichment input step. An unfinished target drops
-- that step and its later steps move one place up and restart from pending: they are
-- idempotent deletes and scrubs. Finished targets keep their recorded history.
CREATE TEMPORARY TABLE ck_removed_deletion_step (
  tombstone_id uuid NOT NULL, project_id uuid NOT NULL, step_order integer NOT NULL
) ON COMMIT DROP;

INSERT INTO ck_removed_deletion_step (tombstone_id, project_id, step_order)
SELECT step.tombstone_id, step.project_id, step.step_order
FROM public.deletion_target_step_progress AS step
JOIN public.deletion_target_tasks AS task
  ON task.tombstone_id = step.tombstone_id
 AND task.project_id = step.project_id
 AND task.target = step.target
WHERE step.target = 'postgresql'
  AND step.step_name LIKE '%:canon_book_enrichment_inputs:delete'
  AND task.state <> 'succeeded';

DELETE FROM public.deletion_target_step_progress AS step
USING ck_removed_deletion_step AS removed
WHERE step.tombstone_id = removed.tombstone_id
  AND step.project_id = removed.project_id
  AND step.target = 'postgresql'
  AND step.step_order = removed.step_order;

-- Two passes keep the unique step order free of transient collisions.
UPDATE public.deletion_target_step_progress AS step
   SET step_order = step.step_order + 500,
       step_name = lpad((step.step_order + 500)::text, 3, '0') || substr(step.step_name, 4),
       state = 'pending', claim_incarnation = NULL, claimed_by_digest = NULL,
       cursor_uuid = NULL, cursor_digest = NULL, processed_count = 0,
       started_at = NULL, updated_at = NULL, completed_at = NULL
  FROM ck_removed_deletion_step AS removed
 WHERE step.tombstone_id = removed.tombstone_id
   AND step.project_id = removed.project_id
   AND step.target = 'postgresql'
   AND step.step_order > removed.step_order;

UPDATE public.deletion_target_step_progress AS step
   SET step_order = step.step_order - 501,
       step_name = lpad((step.step_order - 501)::text, 3, '0') || substr(step.step_name, 4)
  FROM ck_removed_deletion_step AS removed
 WHERE step.tombstone_id = removed.tombstone_id
   AND step.project_id = removed.project_id
   AND step.target = 'postgresql'
   AND step.step_order > 500;

-- The text index of a version no longer distinguishes a text-only run: every successful run
-- queues the cards of its version.
UPDATE public.jobs
   SET subject_json = subject_json - 'textOnly'
 WHERE kind = 'canon_text_index'
   AND jsonb_typeof(subject_json) = 'object'
   AND subject_json ? 'textOnly';

SET CONSTRAINTS ALL IMMEDIATE;

DROP INDEX public.jobs_one_nonterminal_enrichment_idx;

-- Only enrichment Jobs carried a native Codex thread; chat threads live on chat sessions.
ALTER TABLE public.jobs
    DROP COLUMN codex_turn_id,
    DROP COLUMN codex_thread_started_at,
    DROP COLUMN codex_thread_id,
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'export'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = 'research_chat_turn'::text)
      OR (queue_class = 'automatic'::text AND kind = 'chapter_cards'::text)
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'export'::text, 'canon_text_index'::text
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
        AND (creator_user_id IS NOT NULL OR kind = ANY (ARRAY['canon_text_index'::text, 'chapter_cards'::text]))
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

ALTER TABLE public.job_model_bindings
    DROP CONSTRAINT job_model_bindings_job_kind_check,
    ADD CONSTRAINT job_model_bindings_job_kind_check CHECK (job_kind = 'research_chat_turn'::text),
    DROP CONSTRAINT job_model_bindings_check1,
    DROP CONSTRAINT job_model_bindings_check,
    ADD CONSTRAINT job_model_bindings_check CHECK (actor_user_id IS NOT NULL AND initiating_auth_session_id IS NOT NULL),
    DROP CONSTRAINT job_model_bindings_origin_check,
    ADD CONSTRAINT job_model_bindings_origin_check CHECK (origin = 'user'::text);

ALTER TABLE public.artifact_source_owners
    DROP CONSTRAINT artifact_source_owners_artifact_kind_check,
    ADD CONSTRAINT artifact_source_owners_artifact_kind_check CHECK (
      artifact_kind = ANY (ARRAY[
        'job'::text,
        'import_upload'::text,
        'import_job_request'::text,
        'chat_turn'::text,
        'export_artifact'::text,
        'agent_workflow_input'::text
      ])
    );

-- Artifact kind tags keep their numbers (R6.5).
CREATE OR REPLACE FUNCTION public.ck_artifact_kind_tag(kind text)
 RETURNS integer
 LANGUAGE sql
 IMMUTABLE PARALLEL SAFE STRICT
AS $$
  SELECT CASE kind
    WHEN 'job' THEN 3
    WHEN 'import_upload' THEN 4
    WHEN 'import_job_request' THEN 5
    WHEN 'chat_turn' THEN 6
    WHEN 'export_artifact' THEN 10
    WHEN 'agent_workflow_input' THEN 16
    ELSE 0 END
$$;

DROP FUNCTION public.ck_canon_book_enrichment_job_claim_candidate(text[]);
DROP FUNCTION public.ck_current_indexing_job(uuid, text, uuid, text, uuid, uuid);
DROP FUNCTION public.ck_delete_native_indexing_review_coverage(text);
DROP FUNCTION public.ck_canon_graph_draft_valid(text, boolean);
DROP FUNCTION public.ck_canon_graph_handle_bindings_valid(jsonb);
DROP FUNCTION public.ck_validate_canon_graph_contribution_slot();
DROP FUNCTION public.ck_validate_canon_graph_overlay_link();
DROP FUNCTION public.ck_validate_canon_graph_row_evidence();

REVOKE ALL ON TABLE public.jobs FROM PUBLIC;
REVOKE ALL ON TABLE public.job_model_bindings FROM PUBLIC;
REVOKE ALL ON TABLE public.artifact_source_owners FROM PUBLIC;

-- The dispatcher claims import, export, text index, chat turn and cards Jobs; the locked row
-- re-checks that it is still claimable (see 0040).
CREATE OR REPLACE FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[])
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
       WHERE wanted.value NOT IN ('import', 'export', 'canon_text_index', 'research_chat_turn', 'chapter_cards')
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
          AND job.updated_at < claim_time - interval '15 minutes'
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

  RETURN QUERY SELECT claimed_project_id, claimed_work_id, claimed_kind,
    claimed_state, claimed_incarnation, claimed_exhausted_status;
END;
$$;

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(text[]) TO canonkeeper_worker_discovery;

CREATE OR REPLACE FUNCTION public.ck_simple_job_claim_candidate(wanted_kinds text[])
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
  WHERE job.kind = ANY(wanted_kinds)
    AND job.kind <> 'import'
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
$$;

REVOKE ALL ON FUNCTION public.ck_simple_job_claim_candidate(text[]) FROM PUBLIC;

