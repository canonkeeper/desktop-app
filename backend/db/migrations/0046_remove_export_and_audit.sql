-- Book export and the audit log leave the product (specs/2026-10-09-saga-memory.md, R6.3). The
-- audit log recorded only export downloads, so both go together: export Jobs, their artifacts and
-- source manifests are deleted, the export artifact and audit log tables are dropped with their
-- functions, and the Job and artifact kinds narrow to the flows that remain. The governance
-- triggers are redefined first without the branches that name the dropped table: a trigger
-- expression that names a missing table fails to plan. Export files and book covers share one
-- storage root; export files left there are no longer referenced.

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

CREATE TEMPORARY TABLE ck_removed_export_owner (project_id uuid NOT NULL, id uuid NOT NULL) ON COMMIT DROP;

-- Owners are selected by kind, not through live rows: owners fenced by a source deletion keep
-- no artifact row, and jobs tombstoned by a source deletion keep no owner reference.
INSERT INTO ck_removed_export_owner (project_id, id)
SELECT project_id, id FROM public.artifact_source_owners
WHERE artifact_kind = 'export_artifact'
UNION
SELECT owner.project_id, owner.id
FROM public.artifact_source_owners AS owner
JOIN public.jobs AS job ON job.project_id = owner.project_id AND job.id = owner.artifact_id
WHERE owner.artifact_kind = 'job' AND job.kind = 'export';

REVOKE ALL ON TABLE public.export_artifacts FROM PUBLIC;
REVOKE ALL ON TABLE public.audit_logs FROM PUBLIC;
DROP TABLE public.export_artifacts;
DROP TABLE public.audit_logs;

DELETE FROM public.jobs WHERE kind = 'export';
DELETE FROM public.artifact_source_refs AS ref
USING ck_removed_export_owner AS removed
WHERE ref.project_id = removed.project_id AND ref.owner_id = removed.id;
DELETE FROM public.artifact_source_owners AS owner
USING ck_removed_export_owner AS removed
WHERE owner.project_id = removed.project_id AND owner.id = removed.id;

-- PostgreSQL deletion registries persisted before 0046 name the export artifact delete and the
-- audit scrub steps. An unfinished target drops those steps; its later steps take consecutive
-- numbers and restart from pending, since they are idempotent deletes (see 0043). Finished
-- targets keep their recorded history.
CREATE TEMPORARY TABLE ck_export_audit_registry_target (
  tombstone_id uuid NOT NULL, project_id uuid NOT NULL, first_removed integer NOT NULL
) ON COMMIT DROP;

INSERT INTO ck_export_audit_registry_target (tombstone_id, project_id, first_removed)
SELECT step.tombstone_id, step.project_id, min(step.step_order)
FROM public.deletion_target_step_progress AS step
JOIN public.deletion_target_tasks AS task
  ON task.tombstone_id = step.tombstone_id
 AND task.project_id = step.project_id
 AND task.target = step.target
WHERE step.target = 'postgresql'
  AND task.state <> 'succeeded'
  AND split_part(step.step_name, ':', 2) IN ('export_artifacts', 'audit_logs')
GROUP BY step.tombstone_id, step.project_id;

DELETE FROM public.deletion_target_step_progress AS step
USING ck_export_audit_registry_target AS target
WHERE step.tombstone_id = target.tombstone_id
  AND step.project_id = target.project_id
  AND step.target = 'postgresql'
  AND split_part(step.step_name, ':', 2) IN ('export_artifacts', 'audit_logs');

-- Two passes keep the unique step order free of transient collisions.
UPDATE public.deletion_target_step_progress AS step
   SET step_order = 500 + numbered.position,
       step_name = lpad((500 + numbered.position)::text, 3, '0') || substr(step.step_name, 4),
       state = 'pending', claim_incarnation = NULL, claimed_by_digest = NULL,
       cursor_uuid = NULL, cursor_digest = NULL, processed_count = 0,
       started_at = NULL, updated_at = NULL, completed_at = NULL
  FROM ck_export_audit_registry_target AS target,
       (
         SELECT remaining.tombstone_id, remaining.project_id, remaining.step_name,
                row_number() OVER (
                  PARTITION BY remaining.tombstone_id, remaining.project_id ORDER BY remaining.step_order
                )::integer AS position
         FROM public.deletion_target_step_progress AS remaining
         WHERE remaining.target = 'postgresql'
       ) AS numbered
 WHERE step.tombstone_id = target.tombstone_id
   AND step.project_id = target.project_id
   AND step.target = 'postgresql'
   AND step.step_order > target.first_removed
   AND numbered.tombstone_id = step.tombstone_id
   AND numbered.project_id = step.project_id
   AND numbered.step_name = step.step_name;

UPDATE public.deletion_target_step_progress AS step
   SET step_order = step.step_order - 500,
       step_name = lpad((step.step_order - 500)::text, 3, '0') || substr(step.step_name, 4)
  FROM ck_export_audit_registry_target AS target
 WHERE step.tombstone_id = target.tombstone_id
   AND step.project_id = target.project_id
   AND step.target = 'postgresql'
   AND step.step_order > 500;

SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_kind_check,
    ADD CONSTRAINT jobs_kind_check CHECK (kind = ANY (ARRAY[
      'import'::text, 'canon_text_index'::text, 'research_chat_turn'::text, 'chapter_cards'::text
    ])),
    DROP CONSTRAINT jobs_queue_kind_check,
    ADD CONSTRAINT jobs_queue_kind_check CHECK (
      (queue_class = 'interactive'::text AND kind = 'research_chat_turn'::text)
      OR (queue_class = 'automatic'::text AND kind = 'chapter_cards'::text)
      OR (queue_class = 'user_demanded'::text AND kind = ANY (ARRAY[
        'import'::text, 'canon_text_index'::text
      ]))
    );

ALTER TABLE public.artifact_source_owners
    DROP CONSTRAINT artifact_source_owners_artifact_kind_check,
    ADD CONSTRAINT artifact_source_owners_artifact_kind_check CHECK (
      artifact_kind = ANY (ARRAY[
        'job'::text,
        'import_upload'::text,
        'import_job_request'::text,
        'chat_turn'::text,
        'agent_workflow_input'::text
      ])
    );

-- Artifact kind tags keep their numbers (R6.5); tag 10 (export artifact) is retired.
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
    WHEN 'agent_workflow_input' THEN 16
    ELSE 0 END
$$;

DROP FUNCTION public.ck_enforce_export_artifact_transition();
DROP FUNCTION public.ck_validate_audit_payload_row();
DROP FUNCTION public.ck_audit_scrub_is_terminal();
DROP FUNCTION public.ck_audit_payload_shape_valid(text, text, text, text, jsonb);
DROP FUNCTION public.ck_jsonb_object_key_count(jsonb);
DROP FUNCTION canonkeeper_worker.claim_next_export_intent_for_worker();

REVOKE ALL ON TABLE public.jobs FROM PUBLIC;
REVOKE ALL ON TABLE public.artifact_source_owners FROM PUBLIC;
REVOKE ALL ON TABLE public.deletion_target_step_progress FROM PUBLIC;

-- Tenant retention keeps only the purge of soft-deleted reader annotations.
DROP FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker();

CREATE FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker()
RETURNS TABLE(project_id uuid, arm text, reader_annotation_id uuid)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path TO 'pg_catalog'
AS $$
BEGIN
  RETURN QUERY
  SELECT annotation.project_id, 'soft_deleted_reader_annotation'::text, annotation.id
  FROM public.reader_annotations AS annotation
  WHERE annotation.deleted_at IS NOT NULL
    AND annotation.purge_after <= pg_catalog.clock_timestamp()
  ORDER BY annotation.purge_after, annotation.id, annotation.project_id
  LIMIT 1
  FOR UPDATE OF annotation SKIP LOCKED;
END;
$$;

ALTER FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() OWNER TO canonkeeper_governance_owner;
REVOKE ALL ON FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION canonkeeper_worker.claim_next_tenant_retention_for_worker() TO canonkeeper_worker_discovery;

-- The dispatcher claims import, text index, chat turn and cards Jobs; the locked row re-checks
-- that it is still claimable (see 0040).
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
       WHERE wanted.value NOT IN ('import', 'canon_text_index', 'research_chat_turn', 'chapter_cards')
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
