ALTER TABLE public.deletion_tombstones
    ADD COLUMN permission_loss_actor_user_id uuid;

ALTER TABLE public.deletion_tombstones
    ADD CONSTRAINT deletion_tombstones_permission_loss_shape CHECK (
        permission_loss_actor_user_id IS NULL
        OR (entity_kind = 'project' AND purge_mode = 'derived_generation')
    );

DROP INDEX public.deletion_tombstones_project_vector_unique;

CREATE UNIQUE INDEX deletion_tombstones_project_vector_unique
    ON public.deletion_tombstones (purge_mode, project_id, project_generation)
    WHERE entity_kind = 'project' AND permission_loss_actor_user_id IS NULL;

CREATE UNIQUE INDEX deletion_tombstones_permission_loss_event_unique
    ON public.deletion_tombstones (project_id, permission_loss_actor_user_id, fenced_at)
    WHERE permission_loss_actor_user_id IS NOT NULL;

CREATE FUNCTION public.ck_enforce_permission_loss_tombstone_immutability() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.permission_loss_actor_user_id IS DISTINCT FROM OLD.permission_loss_actor_user_id THEN
    RAISE EXCEPTION 'typed tombstone identity is immutable'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER deletion_tombstones_permission_loss_immutable
    BEFORE UPDATE ON public.deletion_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_permission_loss_tombstone_immutability();

CREATE FUNCTION public.ck_validate_permission_loss_tombstone_source() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM public.projects AS project
    JOIN public.project_memberships AS membership
      ON membership.project_id = project.id
     AND membership.user_id = NEW.permission_loss_actor_user_id
    WHERE project.id = NEW.project_id
      AND project.deleted_at IS NULL
      AND project.lifecycle_generation = NEW.project_generation
      AND membership.updated_at = NEW.fenced_at
      AND (
        membership.removed_at = NEW.fenced_at
        OR (membership.removed_at IS NULL AND membership.role = 'viewer')
      )
  ) THEN
    RAISE EXCEPTION 'permission-loss tombstone does not match the exact project membership transition'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER deletion_tombstones_validate_live_source ON public.deletion_tombstones;

CREATE CONSTRAINT TRIGGER deletion_tombstones_validate_live_source
    AFTER INSERT ON public.deletion_tombstones
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (NEW.permission_loss_actor_user_id IS NULL)
    EXECUTE FUNCTION public.ck_validate_tombstone_source();

CREATE CONSTRAINT TRIGGER deletion_tombstones_validate_permission_loss
    AFTER INSERT ON public.deletion_tombstones
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (NEW.permission_loss_actor_user_id IS NOT NULL)
    EXECUTE FUNCTION public.ck_validate_permission_loss_tombstone_source();

REVOKE ALL ON FUNCTION public.ck_enforce_permission_loss_tombstone_immutability() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_validate_permission_loss_tombstone_source() FROM PUBLIC;

ALTER TABLE public.deletion_target_tasks
    DROP CONSTRAINT deletion_target_tasks_terminal_handler_check;

ALTER TABLE public.deletion_target_tasks
    ADD CONSTRAINT deletion_target_tasks_terminal_handler_check CHECK (
        state NOT IN ('succeeded', 'no_data')
        OR (target = 'runtime' AND handler_version = 'runtime_direct_sdk_v1')
        OR (target = 'filesystem' AND handler_version = 'filesystem_os_root_v1')
        OR (target = 'postgresql' AND handler_version = 'postgres_exact_inventory_v1')
    );

CREATE FUNCTION public.ck_enforce_permission_loss_no_data_target() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.state = 'no_data'
     AND NEW.target IN ('filesystem', 'postgresql')
     AND NOT EXISTS (
       SELECT 1
       FROM public.deletion_tombstones AS tombstone
       WHERE tombstone.id = NEW.tombstone_id
         AND tombstone.project_id = NEW.project_id
         AND tombstone.permission_loss_actor_user_id IS NOT NULL
     ) THEN
    RAISE EXCEPTION 'external no_data target requires a permission-loss tombstone'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER deletion_target_tasks_permission_loss_no_data
    BEFORE INSERT OR UPDATE OF state, handler_version ON public.deletion_target_tasks
    FOR EACH ROW EXECUTE FUNCTION public.ck_enforce_permission_loss_no_data_target();

CREATE FUNCTION public.ck_deletion_target_receipt_projection(
    target text,
    tombstone_digest bytea,
    selected_vector_digest bytea,
    handler_version text,
    disposition text,
    completed_at timestamptz,
    permission_loss_actor_user_id uuid
) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  target_tag integer;
  disposition_tag integer;
  handler_allowed boolean;
BEGIN
  target_tag := CASE target
    WHEN 'runtime' THEN 1 WHEN 'filesystem' THEN 2
    WHEN 'postgresql' THEN 4 ELSE 0 END;
  disposition_tag := CASE disposition
    WHEN 'purged' THEN 1 WHEN 'no_data' THEN 2 ELSE 0 END;
  handler_allowed :=
    (target = 'runtime'
      AND handler_version = 'runtime_direct_sdk_v1'
      AND disposition IN ('purged', 'no_data'))
    OR (target = 'filesystem'
      AND handler_version = 'filesystem_os_root_v1'
      AND (disposition = 'purged'
        OR (permission_loss_actor_user_id IS NOT NULL AND disposition = 'no_data')))
    OR (target = 'postgresql'
      AND handler_version = 'postgres_exact_inventory_v1'
      AND (disposition = 'purged'
        OR (permission_loss_actor_user_id IS NOT NULL AND disposition = 'no_data')));
  IF target_tag = 0 OR disposition_tag = 0
     OR handler_allowed IS DISTINCT FROM true OR completed_at IS NULL
     OR NOT public.ck_is_nonzero_digest(tombstone_digest)
     OR NOT public.ck_is_nonzero_digest(selected_vector_digest) THEN
    RAISE EXCEPTION 'invalid deletion target receipt projection input';
  END IF;
  RETURN public.ck_tag_byte(target_tag) || tombstone_digest || selected_vector_digest
    || int8send(octet_length(convert_to(handler_version, 'UTF8')))
    || convert_to(handler_version, 'UTF8')
    || public.ck_tag_byte(disposition_tag) || int8send(public.ck_unix_seconds(completed_at));
END;
$$;

CREATE FUNCTION public.ck_deletion_target_receipt_digest(
    target text,
    tombstone_digest bytea,
    selected_vector_digest bytea,
    handler_version text,
    disposition text,
    completed_at timestamptz,
    permission_loss_actor_user_id uuid
) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionTargetReceiptV1', 'UTF8') || decode('00', 'hex')
    || public.ck_deletion_target_receipt_projection(
      target, tombstone_digest, selected_vector_digest, handler_version,
      disposition, completed_at, permission_loss_actor_user_id
    )
  )
$$;

REVOKE ALL ON FUNCTION public.ck_enforce_permission_loss_no_data_target() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_deletion_target_receipt_projection(text, bytea, bytea, text, text, timestamptz, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_deletion_target_receipt_digest(text, bytea, bytea, text, text, timestamptz, uuid) FROM PUBLIC;
ALTER FUNCTION public.ck_deletion_target_receipt_projection(text, bytea, bytea, text, text, timestamptz, uuid) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_deletion_target_receipt_digest(text, bytea, bytea, text, text, timestamptz, uuid) OWNER TO canonkeeper_governance_owner;

CREATE FUNCTION public.ck_deletion_tombstone_projection(
    tombstone_id uuid,
    origin text,
    purge_mode text,
    selected_entity_kind text,
    selected_project_id uuid,
    selected_project_generation bigint,
    selected_book_id uuid,
    selected_book_generation bigint,
    selected_chapter_id uuid,
    selected_chapter_generation bigint,
    selected_chat_session_id uuid,
    selected_chat_lifecycle_epoch bigint,
    fenced_at timestamptz,
    purge_deadline timestamptz,
    permission_loss_actor_user_id uuid
) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
    AS $$
DECLARE
  result bytea;
BEGIN
  result := public.ck_deletion_tombstone_projection(
    tombstone_id, origin, purge_mode, selected_entity_kind,
    selected_project_id, selected_project_generation,
    selected_book_id, selected_book_generation,
    selected_chapter_id, selected_chapter_generation,
    selected_chat_session_id, selected_chat_lifecycle_epoch,
    fenced_at, purge_deadline
  );
  IF permission_loss_actor_user_id IS NULL THEN
    RETURN result;
  END IF;
  IF purge_mode <> 'derived_generation' OR selected_entity_kind <> 'project' THEN
    RAISE EXCEPTION 'invalid permission-loss tombstone scope';
  END IF;
  RETURN result || public.ck_tag_byte(1) || uuid_send(permission_loss_actor_user_id);
END;
$$;

CREATE FUNCTION public.ck_deletion_tombstone_digest(
    tombstone_id uuid,
    origin text,
    purge_mode text,
    selected_entity_kind text,
    selected_project_id uuid,
    selected_project_generation bigint,
    selected_book_id uuid,
    selected_book_generation bigint,
    selected_chapter_id uuid,
    selected_chapter_generation bigint,
    selected_chat_session_id uuid,
    selected_chat_lifecycle_epoch bigint,
    fenced_at timestamptz,
    purge_deadline timestamptz,
    permission_loss_actor_user_id uuid
) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
  SELECT sha256(
    convert_to('CKDeletionTombstoneV1', 'UTF8') || decode('00', 'hex')
    || public.ck_deletion_tombstone_projection(
      tombstone_id, origin, purge_mode, selected_entity_kind,
      selected_project_id, selected_project_generation,
      selected_book_id, selected_book_generation,
      selected_chapter_id, selected_chapter_generation,
      selected_chat_session_id, selected_chat_lifecycle_epoch,
      fenced_at, purge_deadline, permission_loss_actor_user_id
    )
  )
$$;

CREATE OR REPLACE FUNCTION public.ck_source_hard_purge_predecessor_set_is_valid(
    requested_hard_tombstone_id uuid,
    requested_project_id uuid
) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $$
DECLARE
  hard_tombstone deletion_tombstones%ROWTYPE;
  hard_selected_vector_digest bytea;
  expected_count bigint;
  actual_count bigint;
  predecessor_ids uuid[];
  predecessor_tombstone_digests bytea[];
  predecessor_target_receipt_digests bytea[];
  computed_set_digest bytea;
BEGIN
  SELECT * INTO hard_tombstone
  FROM deletion_tombstones AS candidate
  WHERE candidate.id = requested_hard_tombstone_id
    AND candidate.project_id = requested_project_id;

  IF NOT FOUND
     OR hard_tombstone.purge_mode <> 'source_hard_purge'
     OR hard_tombstone.origin <> 'governed_online'
     OR hard_tombstone.entity_kind = 'chat' THEN
    RETURN false;
  END IF;

  hard_selected_vector_digest := public.ck_deletion_selected_vector_digest(
    hard_tombstone.entity_kind,
    hard_tombstone.project_id,
    hard_tombstone.project_generation,
    hard_tombstone.book_id,
    hard_tombstone.book_generation,
    hard_tombstone.chapter_id,
    hard_tombstone.chapter_generation,
    hard_tombstone.chat_session_id,
    hard_tombstone.chat_lifecycle_epoch
  );

  SELECT count(*) INTO expected_count
  FROM deletion_tombstones AS derived
  WHERE derived.status = 'purged'
    AND derived.fenced_at <= hard_tombstone.fenced_at
    AND public.ck_source_hard_purge_contains_derived(hard_tombstone, derived);

  SELECT count(*) INTO actual_count
  FROM deletion_hard_purge_predecessors AS predecessor
  WHERE predecessor.hard_tombstone_id = hard_tombstone.id
    AND predecessor.project_id = hard_tombstone.project_id;

  IF expected_count = 0 OR actual_count <> expected_count THEN
    RETURN false;
  END IF;

  IF EXISTS (
    SELECT 1
    FROM deletion_hard_purge_predecessors AS predecessor
    LEFT JOIN deletion_tombstones AS derived
      ON derived.id = predecessor.predecessor_tombstone_id
     AND derived.project_id = predecessor.project_id
    LEFT JOIN deletion_completion_receipts AS receipt
      ON receipt.tombstone_id = predecessor.predecessor_tombstone_id
     AND receipt.project_id = predecessor.project_id
    WHERE predecessor.hard_tombstone_id = hard_tombstone.id
      AND predecessor.project_id = hard_tombstone.project_id
      AND (
        derived.id IS NULL
        OR derived.purge_mode <> 'derived_generation'
        OR derived.status <> 'purged'
        OR derived.fenced_at > hard_tombstone.fenced_at
        OR NOT public.ck_source_hard_purge_contains_derived(hard_tombstone, derived)
        OR receipt.tombstone_id IS NULL
        OR receipt.vector_digest IS DISTINCT FROM public.ck_deletion_selected_vector_digest(
          derived.entity_kind,
          derived.project_id,
          derived.project_generation,
          derived.book_id,
          derived.book_generation,
          derived.chapter_id,
          derived.chapter_generation,
          derived.chat_session_id,
          derived.chat_lifecycle_epoch
        )
        OR predecessor.predecessor_vector_digest IS DISTINCT FROM receipt.vector_digest
        OR predecessor.predecessor_tombstone_digest IS DISTINCT FROM public.ck_deletion_tombstone_digest(
          derived.id,
          derived.origin,
          derived.purge_mode,
          derived.entity_kind,
          derived.project_id,
          derived.project_generation,
          derived.book_id,
          derived.book_generation,
          derived.chapter_id,
          derived.chapter_generation,
          derived.chat_session_id,
          derived.chat_lifecycle_epoch,
          derived.fenced_at,
          derived.purge_deadline,
          derived.permission_loss_actor_user_id
        )
        OR predecessor.predecessor_target_receipts_digest
          IS DISTINCT FROM receipt.target_receipts_digest
      )
  ) THEN
    RETURN false;
  END IF;

  SELECT
    array_agg(predecessor.predecessor_tombstone_id
      ORDER BY uuid_send(predecessor.predecessor_tombstone_id)),
    array_agg(predecessor.predecessor_tombstone_digest
      ORDER BY uuid_send(predecessor.predecessor_tombstone_id)),
    array_agg(predecessor.predecessor_target_receipts_digest
      ORDER BY uuid_send(predecessor.predecessor_tombstone_id))
  INTO predecessor_ids, predecessor_tombstone_digests,
       predecessor_target_receipt_digests
  FROM deletion_hard_purge_predecessors AS predecessor
  WHERE predecessor.hard_tombstone_id = hard_tombstone.id
    AND predecessor.project_id = hard_tombstone.project_id;

  computed_set_digest := public.ck_deletion_predecessor_set_digest(
    hard_selected_vector_digest,
    predecessor_ids,
    predecessor_tombstone_digests,
    predecessor_target_receipt_digests
  );
  RETURN computed_set_digest = hard_tombstone.predecessor_set_digest;
END;
$$;

REVOKE ALL ON FUNCTION public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamptz, timestamptz, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_deletion_tombstone_digest(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamptz, timestamptz, uuid) FROM PUBLIC;
ALTER FUNCTION public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamptz, timestamptz, uuid) OWNER TO canonkeeper_governance_owner;
ALTER FUNCTION public.ck_deletion_tombstone_digest(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamptz, timestamptz, uuid) OWNER TO canonkeeper_governance_owner;
