-- Writer and canon correction leave the product (specs/2026-10-09-saga-memory.md, UC6).
-- Their Jobs, private workflow inputs, proposals and source manifests are deleted, the proposal
-- tables are dropped and the Job, binding, input and artifact kinds narrow to the flows that
-- remain. The governance triggers are redefined first without the branches that name the
-- dropped tables: a trigger expression that names a missing table fails to plan. Native Codex
-- threads of the removed flows stay in their former CODEX_HOME directories, which the operator
-- removes together with those homes.

CREATE OR REPLACE FUNCTION public.ck_validate_owner_ref_set() RETURNS trigger
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

CREATE OR REPLACE FUNCTION public.ck_validate_owner_artifact_link() RETURNS trigger
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
  ELSIF NEW.artifact_kind = 'export_artifact' THEN
    SELECT EXISTS (SELECT 1 FROM public.export_artifacts a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id) INTO present;
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

CREATE OR REPLACE FUNCTION public.ck_validate_participant_owner_link() RETURNS trigger
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
  ELSIF TG_TABLE_NAME = 'export_artifacts' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'export_artifact';
  ELSIF TG_TABLE_NAME = 'chapter_summaries' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'chapter_summary';
  ELSIF TG_TABLE_NAME = 'canon_author_rows' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'canon_author_row';
  ELSIF TG_TABLE_NAME = 'canon_graph_contributions' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'canon_graph_contribution';
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
        TG_TABLE_NAME NOT IN (
          'chapter_summaries', 'canon_author_rows', 'canon_graph_contributions',
          'chat_turns', 'agent_workflow_inputs'
        )
        OR (
          TG_TABLE_NAME IN ('chat_turns', 'agent_workflow_inputs')
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
              AND ref.ref_kind = 'scope' AND ref.scope_kind = 'book'
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

CREATE TEMPORARY TABLE ck_removed_agent_owner (project_id uuid NOT NULL, id uuid NOT NULL) ON COMMIT DROP;

-- Owners are selected by kind, not through live rows: owners fenced by a source deletion keep
-- no artifact row, and jobs tombstoned by a source deletion keep no owner reference.
INSERT INTO ck_removed_agent_owner (project_id, id)
SELECT project_id, id FROM public.artifact_source_owners
WHERE artifact_kind IN ('writer_proposal', 'canon_correction_proposal')
UNION
SELECT project_id, artifact_source_owner_id FROM public.agent_workflow_inputs
WHERE job_kind IN ('writer_agent', 'canon_correction')
UNION
SELECT owner.project_id, owner.id
FROM public.artifact_source_owners AS owner
JOIN public.jobs AS job ON job.project_id = owner.project_id AND job.id = owner.artifact_id
WHERE owner.artifact_kind = 'job' AND job.kind IN ('writer_agent', 'canon_correction');

DROP TABLE public.writer_proposals;
DROP TABLE public.canon_correction_proposals;

DELETE FROM public.agent_workflow_inputs WHERE job_kind IN ('writer_agent', 'canon_correction');
DELETE FROM public.job_model_bindings WHERE job_kind IN ('writer_agent', 'canon_correction');
DELETE FROM public.jobs WHERE kind IN ('writer_agent', 'canon_correction');
DELETE FROM public.artifact_source_refs AS ref
USING ck_removed_agent_owner AS removed
WHERE ref.project_id = removed.project_id AND ref.owner_id = removed.id;
DELETE FROM public.artifact_source_owners AS owner
USING ck_removed_agent_owner AS removed
WHERE owner.project_id = removed.project_id AND owner.id = removed.id;

SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'export'::text, 'canon_text_index'::text, 'canon_book_enrichment'::text,
      'research_chat_turn'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = 'research_chat_turn'::text)
      OR (queue_class = 'automatic'::text AND kind = 'canon_book_enrichment'::text)
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'export'::text, 'canon_text_index'::text, 'canon_book_enrichment'::text
      ]))
    ),
    DROP CONSTRAINT jobs_codex_thread_id_check,
    ADD CONSTRAINT jobs_codex_thread_id_check CHECK (
        codex_thread_id IS NULL OR (
            kind = 'canon_book_enrichment' AND
            codex_thread_id = btrim(codex_thread_id) AND
            char_length(codex_thread_id) BETWEEN 1 AND 255 AND
            codex_thread_id !~ '[[:cntrl:]]'
        )
    );

ALTER TABLE public.job_model_bindings
    DROP CONSTRAINT job_model_bindings_job_kind_check,
    ADD CONSTRAINT job_model_bindings_job_kind_check CHECK (job_kind = ANY (ARRAY[
      'canon_book_enrichment'::text, 'research_chat_turn'::text
    ])),
    DROP CONSTRAINT job_model_bindings_check1,
    ADD CONSTRAINT job_model_bindings_check1 CHECK (
      (job_kind = 'canon_book_enrichment'::text AND origin = ANY (ARRAY['user'::text, 'service'::text]))
      OR (job_kind = 'research_chat_turn'::text AND origin = 'user'::text)
    );

ALTER TABLE public.agent_workflow_inputs
    DROP CONSTRAINT agent_workflow_inputs_job_kind_check,
    ADD CONSTRAINT agent_workflow_inputs_job_kind_check CHECK (job_kind = 'research_chat_turn'::text),
    DROP CONSTRAINT agent_workflow_inputs_context_warning_check,
    ADD CONSTRAINT agent_workflow_inputs_context_warning_check CHECK (
      context_warning IS NULL OR context_warning = 'partial_context'::text
    );

ALTER TABLE public.artifact_source_owners
    DROP CONSTRAINT artifact_source_owners_artifact_kind_check,
    ADD CONSTRAINT artifact_source_owners_artifact_kind_check CHECK (
      artifact_kind = ANY (ARRAY[
        'canon_book_enrichment_input'::text,
        'job'::text,
        'import_upload'::text,
        'import_job_request'::text,
        'chat_turn'::text,
        'export_artifact'::text,
        'chapter_summary'::text,
        'canon_author_row'::text,
        'canon_graph_contribution'::text,
        'agent_workflow_input'::text
      ])
    );

CREATE OR REPLACE FUNCTION public.ck_artifact_kind_tag(kind text) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    AS $$
  SELECT CASE kind
    WHEN 'canon_book_enrichment_input' THEN 2
    WHEN 'job' THEN 3
    WHEN 'import_upload' THEN 4
    WHEN 'import_job_request' THEN 5
    WHEN 'chat_turn' THEN 6
    WHEN 'export_artifact' THEN 10
    WHEN 'chapter_summary' THEN 12
    WHEN 'canon_author_row' THEN 13
    WHEN 'canon_graph_contribution' THEN 14
    WHEN 'agent_workflow_input' THEN 16
    ELSE 0 END
$$;

REVOKE ALL ON TABLE public.jobs FROM PUBLIC;
REVOKE ALL ON TABLE public.job_model_bindings FROM PUBLIC;
REVOKE ALL ON TABLE public.agent_workflow_inputs FROM PUBLIC;
REVOKE ALL ON TABLE public.artifact_source_owners FROM PUBLIC;
