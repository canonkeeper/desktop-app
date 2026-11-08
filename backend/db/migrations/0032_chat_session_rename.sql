-- Existing rename may change a live title; identity and deletion guards remain intact.
CREATE OR REPLACE FUNCTION public.ck_chat_lifecycle_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF NOT EXISTS (
      SELECT 1
      FROM deletion_tombstones t
      JOIN deletion_target_tasks task
        ON task.tombstone_id = t.id
       AND task.project_id = t.project_id
       AND task.target = 'postgresql'
      JOIN deletion_target_step_progress step
        ON step.tombstone_id = task.tombstone_id
       AND step.project_id = task.project_id
       AND step.target = task.target
      WHERE t.project_id = OLD.project_id
        AND (
          (t.entity_kind = 'chat'
            AND t.entity_id = OLD.id
            AND t.chat_session_id = OLD.id
            AND t.chat_lifecycle_epoch = OLD.lifecycle_epoch - 1
            AND t.purge_deadline IS NOT DISTINCT FROM OLD.purge_after
            AND t.purge_mode = 'chat_epoch')
          OR
          (t.purge_mode = 'source_hard_purge'
            AND (
              (t.entity_kind = 'project'
                AND t.entity_id = OLD.project_id
                AND EXISTS (
                  SELECT 1 FROM projects selected
                  WHERE selected.id = t.project_id
                    AND selected.lifecycle_generation = t.project_generation
                    AND selected.deleted_at IS NOT NULL
                    AND selected.hard_purge_started_at = t.fenced_at
                ))
              OR
              (t.entity_kind = 'book'
                AND t.entity_id = OLD.book_id
                AND t.book_id = OLD.book_id
                AND EXISTS (
                  SELECT 1 FROM books selected
                  WHERE selected.id = t.book_id
                    AND selected.project_id = t.project_id
                    AND selected.lifecycle_generation = t.book_generation
                    AND selected.deleted_at IS NOT NULL
                    AND selected.hard_purge_started_at = t.fenced_at
                ))
            ))
        )
        AND t.status = 'processing'
        AND task.state = 'processing'
        AND task.claim_expires_at > clock_timestamp()
        AND step.state IN ('running', 'succeeded')
        AND step.claim_incarnation = task.claim_incarnation
        AND step.claimed_by_digest = task.claimed_by_digest
        AND step.step_name = lpad(step.step_order::text, 3, '0')
          || ':chat_sessions:delete'
        AND NOT EXISTS (
          SELECT 1 FROM deletion_target_tasks external
          WHERE external.tombstone_id = t.id
            AND external.project_id = t.project_id
            AND external.target IN ('runtime', 'filesystem')
            AND external.state NOT IN ('succeeded', 'no_data')
        )
    ) THEN
      RAISE EXCEPTION 'chat hard delete lacks an exact live PostgreSQL step claim'
        USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.project_id IS DISTINCT FROM OLD.project_id
     OR NEW.source_view IS DISTINCT FROM OLD.source_view
     OR NEW.scope IS DISTINCT FROM OLD.scope
     OR NEW.book_id IS DISTINCT FROM OLD.book_id
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'chat session identity, view and scope are immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.title IS DISTINCT FROM OLD.title
     AND (OLD.deleted_at IS NOT NULL OR NEW.deleted_at IS NOT NULL) THEN
    RAISE EXCEPTION 'deleted chat title is immutable' USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.deleted_at IS NOT NULL AND (
       NEW.deleted_at IS DISTINCT FROM OLD.deleted_at
       OR NEW.purge_after IS DISTINCT FROM OLD.purge_after
       OR NEW.lifecycle_epoch IS DISTINCT FROM OLD.lifecycle_epoch
     ) THEN
    RAISE EXCEPTION 'deleted chat session lifecycle cannot change in place'
      USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at
     AND (
       NEW.lifecycle_epoch IS DISTINCT FROM OLD.lifecycle_epoch
       OR NEW.purge_after IS DISTINCT FROM OLD.purge_after
     ) THEN
    RAISE EXCEPTION 'chat lifecycle fields change only during governed deletion'
      USING ERRCODE = 'check_violation';
  END IF;

  IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
    IF NEW.purge_after IS DISTINCT FROM NEW.deleted_at + interval '24 hours' THEN
      RAISE EXCEPTION 'governed chat delete has a noncanonical purge deadline'
        USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.lifecycle_epoch IS NULL
       OR NEW.lifecycle_epoch <> OLD.lifecycle_epoch + 1
       OR NOT EXISTS (
         SELECT 1 FROM deletion_tombstones t
         WHERE t.project_id = OLD.project_id
           AND t.entity_kind = 'chat'
           AND t.entity_id = OLD.id
           AND t.purge_mode = 'chat_epoch'
           AND t.origin = 'governed_online'
           AND t.chat_lifecycle_epoch = OLD.lifecycle_epoch
           AND t.purge_deadline IS NOT DISTINCT FROM NEW.purge_after
       ) THEN
      RAISE EXCEPTION 'chat delete lacks its exact incremented epoch tombstone'
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION public.ck_chat_lifecycle_commit_guard() FROM PUBLIC;
