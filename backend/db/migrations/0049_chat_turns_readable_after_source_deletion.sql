-- A chat turn stays readable after the text it read is deleted (specs/2026-10-16-book-deletion-keeps-chats.md,
-- R1.4): the lifecycle check of a chat-bound ref names the project and the chat, not the book
-- and the chapter. 0048 made the same change in the currentness check; the chat page also reads
-- a turn through this one (GetReadableChatTurn, the timeline binding, the Job resolver).

CREATE OR REPLACE FUNCTION public.ck_artifact_source_owner_is_lifecycle_readable(wanted_owner_id uuid, wanted_project_id uuid, wanted_artifact_kind text, wanted_artifact_id uuid) RETURNS boolean
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
      AND owner.fenced_at IS NULL
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
        WHERE ref.owner_id = owner.id
          AND ref.project_id = owner.project_id
          AND NOT (
            ref.project_generation = project.lifecycle_generation
            AND (
              ref.chat_session_id IS NOT NULL
              OR (
                (
                  ref.book_id IS NULL
                  OR (
                    book.id IS NOT NULL AND book.deleted_at IS NULL
                    AND ref.book_generation = book.lifecycle_generation
                  )
                )
                AND (
                  ref.chapter_id IS NULL
                  OR (
                    chapter.id IS NOT NULL AND chapter.deleted_at IS NULL
                    AND ref.chapter_generation = chapter.lifecycle_generation
                  )
                )
              )
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

-- CREATE OR REPLACE keeps the grants of the function; PUBLIC stays without access.
REVOKE ALL ON FUNCTION public.ck_artifact_source_owner_is_lifecycle_readable(uuid, uuid, text, uuid) FROM PUBLIC;
