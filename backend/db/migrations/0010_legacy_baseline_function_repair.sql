-- Repair the exact pre-ledger TEST baseline function drift observed at the 15dc snapshot.

-- These replacements preserve rows and function signatures; numbered migrations apply them before adoption completes.

CREATE OR REPLACE FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[]) RETURNS TABLE(project_id uuid, work_id uuid, kind text, state text, claim_incarnation bigint, exhausted_status text)
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

CREATE OR REPLACE FUNCTION public.ck_artifact_source_ref_projection(ref_kind text, scope_kind text, project_id uuid, project_generation bigint, book_id uuid DEFAULT NULL::uuid, book_generation bigint DEFAULT NULL::bigint, chapter_id uuid DEFAULT NULL::uuid, chapter_generation bigint DEFAULT NULL::bigint, source_view text DEFAULT NULL::text, source_independent boolean DEFAULT false, chat_session_id uuid DEFAULT NULL::uuid, chat_lifecycle_epoch bigint DEFAULT NULL::bigint) RETURNS bytea
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

CREATE OR REPLACE FUNCTION public.ck_validate_active_artifact_source_owner_currentness() RETURNS trigger
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

REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[]) FROM PUBLIC;
GRANT ALL ON FUNCTION canonkeeper_worker.claim_next_queued_job_for_worker(wanted_kinds text[])
  TO canonkeeper_worker_discovery;

REVOKE ALL ON FUNCTION public.ck_artifact_source_ref_projection(
  ref_kind text, scope_kind text, project_id uuid, project_generation bigint,
  book_id uuid, book_generation bigint, chapter_id uuid, chapter_generation bigint,
  source_view text, source_independent boolean, chat_session_id uuid, chat_lifecycle_epoch bigint
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_artifact_source_ref_projection(
  ref_kind text, scope_kind text, project_id uuid, project_generation bigint,
  book_id uuid, book_generation bigint, chapter_id uuid, chapter_generation bigint,
  source_view text, source_independent boolean, chat_session_id uuid, chat_lifecycle_epoch bigint
) TO canonkeeper_product_runtime, canonkeeper_governance_owner, CURRENT_USER;

REVOKE ALL ON FUNCTION public.ck_validate_active_artifact_source_owner_currentness() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_validate_participant_owner_link() FROM PUBLIC;
