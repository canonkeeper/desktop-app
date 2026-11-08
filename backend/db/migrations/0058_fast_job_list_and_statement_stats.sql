-- The shelf and the indexing status read only the Jobs they show
-- (specs/2026-10-28-fast-job-list-and-postgres.md).

-- UC4: the currentness of an owner reads each table a reference names by its key, in a
-- correlated EXISTS of the branch of that reference, instead of one LEFT JOIN of all ten tables.
-- Every join of 0048 matched at most one row per reference (primary keys) and the columns a
-- branch compares are never NULL (artifact_source_refs_contract_shape), so the result is the
-- same for any data. Planning one call drops from 20-37 ms to 2-3 ms on production.
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
                  ref.chapter_id IS NULL
                  AND EXISTS (
                    SELECT 1 FROM public.books AS book
                    WHERE book.id = ref.book_id AND book.project_id = ref.project_id
                      AND book.deleted_at IS NULL AND book.lifecycle_generation = ref.book_generation
                  )
                WHEN ref.ref_kind = 'scope' AND ref.scope_kind = 'chapter' THEN
                  EXISTS (
                    SELECT 1 FROM public.books AS book
                    WHERE book.id = ref.book_id AND book.project_id = ref.project_id
                      AND book.deleted_at IS NULL AND book.lifecycle_generation = ref.book_generation
                  )
                  AND EXISTS (
                    SELECT 1 FROM public.chapters AS chapter
                    WHERE chapter.id = ref.chapter_id AND chapter.book_id = ref.book_id
                      AND chapter.project_id = ref.project_id
                      AND chapter.deleted_at IS NULL AND chapter.lifecycle_generation = ref.chapter_generation
                  )
                WHEN ref.ref_kind = 'chapter' AND ref.text_identity_state = 'text_derived_current' THEN
                  owner.manifest_version = 2
                  AND EXISTS (
                    SELECT 1 FROM public.project_source_views AS source_project
                    WHERE source_project.project_id = ref.project_id
                      AND source_project.source_view = ref.source_view
                      AND source_project.project_lifecycle_generation = ref.project_generation
                  )
                  AND EXISTS (
                    SELECT 1 FROM public.book_source_members AS source_book
                    WHERE source_book.project_id = ref.project_id
                      AND source_book.source_view = ref.source_view
                      AND source_book.project_lifecycle_generation = ref.project_generation
                      AND source_book.book_id = ref.book_id
                      AND source_book.book_lifecycle_generation = ref.book_generation
                  )
                  AND EXISTS (
                    SELECT 1 FROM public.chapter_source_members AS source_chapter
                    WHERE source_chapter.project_id = ref.project_id
                      AND source_chapter.source_view = ref.source_view
                      AND source_chapter.project_lifecycle_generation = ref.project_generation
                      AND source_chapter.book_id = ref.book_id
                      AND source_chapter.book_lifecycle_generation = ref.book_generation
                      AND source_chapter.chapter_id = ref.chapter_id
                      AND source_chapter.chapter_lifecycle_generation = ref.chapter_generation
                      AND source_chapter.source_revision = ref.source_revision
                  )
                  AND EXISTS (
                    SELECT 1 FROM public.chapter_text_search_rows AS chapter_text
                    WHERE chapter_text.project_id = ref.project_id
                      AND chapter_text.source_view = ref.source_view
                      AND chapter_text.book_id = ref.book_id
                      AND chapter_text.chapter_id = ref.chapter_id
                      AND chapter_text.source_revision = ref.source_revision
                      AND chapter_text.locator_source_generation = ref.locator_source_generation
                      AND chapter_text.chapter_content_hash = ref.content_hash
                  )
                ELSE false
              END
            )
            AND (
              ref.chat_session_id IS NULL
              OR EXISTS (
                SELECT 1 FROM public.chat_sessions AS chat
                WHERE chat.id = ref.chat_session_id AND chat.project_id = ref.project_id
                  AND chat.deleted_at IS NULL AND chat.lifecycle_epoch = ref.chat_lifecycle_epoch
              )
            )
          )
      )
  )
$$;

-- CREATE OR REPLACE keeps the owner and grants of the function; PUBLIC stays without access.
REVOKE ALL ON FUNCTION public.ck_artifact_source_owner_is_current(uuid, uuid, text, uuid) FROM PUBLIC;

-- UC2, UC3: a page of Jobs of one kind, and the latest Job of a status group, in time order.
CREATE INDEX jobs_project_kind_recent_idx ON public.jobs USING btree (project_id, kind, created_at DESC, id DESC);

-- UC1: the Jobs in work of a project, which the shelf follows.
CREATE INDEX jobs_project_active_recent_idx ON public.jobs USING btree (project_id, created_at DESC, id DESC)
  WHERE status IN ('queued', 'running', 'canceling');

-- UC6: statement statistics for the operator (shared_preload_libraries in
-- compose.production.yaml). The schema and its objects are the administrator's alone: the
-- extension opens its views and functions for every role, and the worker discovery identity
-- must not read any relation. Query texts are normalized, so no user value is kept.
CREATE SCHEMA canonkeeper_stats;
REVOKE ALL ON SCHEMA canonkeeper_stats FROM PUBLIC;
CREATE EXTENSION pg_stat_statements WITH SCHEMA canonkeeper_stats;
REVOKE ALL ON ALL TABLES IN SCHEMA canonkeeper_stats FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA canonkeeper_stats FROM PUBLIC;
