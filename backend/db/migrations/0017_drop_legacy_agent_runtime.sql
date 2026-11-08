REVOKE ALL ON TABLE public.job_agent_threads FROM PUBLIC;

DO $ck_legacy_agent_runtime_preflight$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM public.job_agent_threads AS binding
    JOIN public.jobs AS job
      ON job.project_id = binding.project_id
     AND job.id = binding.job_id
    WHERE binding.thread_id IS NOT NULL
      AND (
        (job.kind = 'canon_book_enrichment' AND binding.role IN ('canon_book_enrichment', 'book_enrichment_main'))
        OR (job.kind = 'writer_agent' AND binding.role = 'writer_agent')
        OR (job.kind = 'canon_correction' AND binding.role = 'canon_correction')
      )
      AND (
        job.codex_thread_id IS DISTINCT FROM binding.thread_id
        OR job.codex_thread_started_at IS NULL
      )
  ) THEN
    RAISE EXCEPTION 'legacy native root binding was not migrated'
      USING ERRCODE = 'check_violation';
  END IF;

END;
$ck_legacy_agent_runtime_preflight$;

ALTER TABLE public.canon_graph_draft_review_reads
    DROP CONSTRAINT canon_graph_draft_review_reads_attempt_fkey,
    DROP COLUMN claim_incarnation,
    DROP COLUMN runtime_session_id;

ALTER TABLE public.canon_graph_draft_rows
    DROP CONSTRAINT canon_graph_draft_rows_attempt_fkey,
    DROP COLUMN claim_incarnation,
    DROP COLUMN runtime_session_id;

ALTER TABLE public.canon_graph_attempts
    DROP CONSTRAINT canon_graph_attempts_project_id_job_id_claim_incarnation_r_fkey,
    DROP CONSTRAINT canon_graph_attempts_legacy_identity_key,
    DROP COLUMN claim_incarnation,
    DROP COLUMN runtime_session_id;

DROP TABLE public.agent_timeline_events;
DROP TABLE public.agent_timeline_bindings;
DROP TABLE public.canon_book_enrichment_steps;
DROP TABLE public.agent_session_replays;
DROP TABLE public.agent_session_refs;
DROP TABLE public.runtime_initial_turn_requests;
DROP TABLE public.runtime_session_deletion_associations;
DROP TABLE public.job_agent_threads;
DROP TABLE public.runtime_sessions;

ALTER TABLE public.artifact_source_owners
    DROP CONSTRAINT artifact_source_owners_artifact_kind_check;

DELETE FROM public.artifact_source_owners
WHERE artifact_kind = 'runtime_session';

SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE public.artifact_source_owners
    ADD CONSTRAINT artifact_source_owners_artifact_kind_check CHECK (
      artifact_kind = ANY (ARRAY[
        'canon_book_enrichment_input'::text,
        'job'::text,
        'import_upload'::text,
        'import_job_request'::text,
        'chat_turn'::text,
        'writer_proposal'::text,
        'export_artifact'::text,
        'chapter_summary'::text,
        'canon_author_row'::text,
        'canon_graph_contribution'::text,
        'canon_correction_proposal'::text,
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
    WHEN 'writer_proposal' THEN 8
    WHEN 'export_artifact' THEN 10
    WHEN 'chapter_summary' THEN 12
    WHEN 'canon_author_row' THEN 13
    WHEN 'canon_graph_contribution' THEN 14
    WHEN 'canon_correction_proposal' THEN 15
    WHEN 'agent_workflow_input' THEN 16
    ELSE 0 END
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
  ELSIF NEW.artifact_kind = 'writer_proposal' THEN
    SELECT EXISTS (SELECT 1 FROM public.writer_proposals a WHERE a.id = NEW.artifact_id AND a.project_id = NEW.project_id AND a.artifact_source_owner_id = NEW.id AND a.source_view IS NOT DISTINCT FROM NEW.source_view) INTO present;
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
  ELSIF TG_TABLE_NAME = 'writer_proposals' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'writer_proposal';
  ELSIF TG_TABLE_NAME = 'export_artifacts' THEN
    wanted_artifact_id := NULLIF(row_data->>'id', '')::uuid; old_artifact_id := NULLIF(old_data->>'id', '')::uuid; expected_kind := 'export_artifact';
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
        TG_TABLE_NAME NOT IN (
          'chapter_summaries', 'canon_author_rows', 'canon_graph_contributions',
          'chat_turns', 'writer_proposals', 'canon_correction_proposals', 'agent_workflow_inputs'
        )
        OR (
          TG_TABLE_NAME IN ('chat_turns', 'writer_proposals', 'canon_correction_proposals', 'agent_workflow_inputs')
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
