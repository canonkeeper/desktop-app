-- Deleting a book or a chapter no longer touches chats (specs/2026-10-16-book-deletion-keeps-chats.md):
-- a chat turn is the author's conversation, not text derived from the saga. Its refs stay as a
-- record of what the turn read, so they may outlive the book and its chapters, and a chat-bound
-- owner is current while its project and chat are; only a new chat ref must name current text.

-- The hard purge of a book deletes its rows while the chat turns that read it keep their refs.
ALTER TABLE public.artifact_source_refs
  DROP CONSTRAINT artifact_source_refs_book_id_project_id_fkey,
  DROP CONSTRAINT artifact_source_refs_chapter_id_book_id_project_id_fkey;

CREATE OR REPLACE FUNCTION public.ck_artifact_source_owner_is_current(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
  SELECT EXISTS (
    SELECT 1
    FROM public.artifact_source_owners AS owner
    JOIN public.projects AS project
      ON project.id = owner.project_id AND project.deleted_at IS NULL
    WHERE owner.id = wanted_owner_id
      AND owner.project_id = wanted_project_id
      AND owner.artifact_kind = wanted_artifact_kind
      AND owner.artifact_id = wanted_artifact_id
      AND owner.read_state = 'active'
      AND owner.ref_count >= 1
      AND owner.ref_count = (
        SELECT count(*)::integer
        FROM public.artifact_source_refs AS counted_ref
        WHERE counted_ref.owner_id = owner.id
          AND counted_ref.project_id = owner.project_id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM public.artifact_source_refs AS ref
        LEFT JOIN public.books AS book
          ON book.id = ref.book_id AND book.project_id = ref.project_id
        LEFT JOIN public.chapters AS chapter
          ON chapter.id = ref.chapter_id AND chapter.book_id = ref.book_id
         AND chapter.project_id = ref.project_id
        LEFT JOIN public.chat_sessions AS chat
          ON chat.id = ref.chat_session_id AND chat.project_id = ref.project_id
        LEFT JOIN public.project_source_views AS source_project
          ON source_project.project_id = ref.project_id
         AND source_project.source_view = ref.source_view
         AND source_project.project_lifecycle_generation = ref.project_generation
        LEFT JOIN public.book_source_members AS source_book
          ON source_book.project_id = ref.project_id
         AND source_book.source_view = ref.source_view
         AND source_book.project_lifecycle_generation = ref.project_generation
         AND source_book.book_id = ref.book_id
         AND source_book.book_lifecycle_generation = ref.book_generation
        LEFT JOIN public.chapter_source_members AS source_chapter
          ON source_chapter.project_id = ref.project_id
         AND source_chapter.source_view = ref.source_view
         AND source_chapter.project_lifecycle_generation = ref.project_generation
         AND source_chapter.book_id = ref.book_id
         AND source_chapter.book_lifecycle_generation = ref.book_generation
         AND source_chapter.chapter_id = ref.chapter_id
         AND source_chapter.chapter_lifecycle_generation = ref.chapter_generation
         AND source_chapter.source_revision = ref.source_revision
        LEFT JOIN public.chapter_text_search_rows AS chapter_text
          ON chapter_text.project_id = ref.project_id
         AND chapter_text.source_view = ref.source_view
         AND chapter_text.book_id = ref.book_id
         AND chapter_text.chapter_id = ref.chapter_id
         AND chapter_text.source_revision = ref.source_revision
         AND chapter_text.locator_source_generation = ref.locator_source_generation
         AND chapter_text.chapter_content_hash = ref.content_hash
        WHERE ref.owner_id = owner.id
          AND ref.project_id = owner.project_id
          AND NOT (
            ref.project_generation = project.lifecycle_generation
            AND (
              ref.chat_session_id IS NOT NULL
              OR CASE
                WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'project' THEN
                  ref.book_id IS NULL AND ref.chapter_id IS NULL
                WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'book' THEN
                  book.id IS NOT NULL AND book.deleted_at IS NULL
                  AND ref.book_generation = book.lifecycle_generation
                  AND ref.chapter_id IS NULL
                WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'chapter' THEN
                  book.id IS NOT NULL AND book.deleted_at IS NULL
                  AND ref.book_generation = book.lifecycle_generation
                  AND chapter.id IS NOT NULL AND chapter.deleted_at IS NULL
                  AND ref.chapter_generation = chapter.lifecycle_generation
                WHEN ref.ref_kind = 'chapter' AND ref.text_identity_state = 'text_derived_current' THEN
                  owner.manifest_version = 2
                  AND source_project.project_id IS NOT NULL
                  AND source_book.book_id IS NOT NULL
                  AND source_chapter.chapter_id IS NOT NULL
                  AND chapter_text.chapter_id IS NOT NULL
                ELSE false
              END
            )
            AND (
              ref.chat_session_id IS NULL
              OR (
                chat.id IS NOT NULL AND chat.deleted_at IS NULL
                AND ref.chat_lifecycle_epoch = chat.lifecycle_epoch
              )
            )
          )
      )
  )
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
  -- A chat records only reads of current text; afterwards the record may outlive the text.
  IF TG_TABLE_NAME = 'artifact_source_refs' AND TG_OP <> 'DELETE' THEN
    IF NEW.chat_session_id IS NOT NULL
       AND NEW.ref_kind = 'chapter' AND NEW.text_identity_state = 'text_derived_current'
       AND NOT EXISTS (
         SELECT 1 FROM public.chapter_text_search_rows AS document
         WHERE document.project_id = NEW.project_id
           AND document.source_view = NEW.source_view
           AND document.book_id = NEW.book_id
           AND document.chapter_id = NEW.chapter_id
           AND document.source_revision = NEW.source_revision
           AND document.locator_source_generation = NEW.locator_source_generation
           AND document.chapter_content_hash = NEW.content_hash
       ) THEN
      RAISE EXCEPTION 'chat artifact source reference must name current text'
        USING ERRCODE = 'check_violation';
    END IF;
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
      AND ref.chat_session_id IS NULL
  ) AS target ON target.book_id = book.id
  WHERE book.project_id = owner_row.project_id ORDER BY book.id FOR SHARE OF book;
  PERFORM chapter.id
  FROM public.chapters AS chapter
  JOIN (
    SELECT DISTINCT ref.chapter_id FROM public.artifact_source_refs AS ref
    WHERE ref.owner_id = owner_row.id AND ref.project_id = owner_row.project_id AND ref.chapter_id IS NOT NULL
      AND ref.chat_session_id IS NULL
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
    AND ref.text_identity_state = 'text_derived_current'
    AND ref.chat_session_id IS NULL;
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
      AND ref.chat_session_id IS NULL
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

-- CREATE OR REPLACE keeps the grants of both functions; PUBLIC stays without access.
REVOKE ALL ON FUNCTION public.ck_artifact_source_owner_is_current(uuid, uuid, text, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.ck_validate_active_artifact_source_owner_currentness() FROM PUBLIC;
