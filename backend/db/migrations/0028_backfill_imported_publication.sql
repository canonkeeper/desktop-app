-- Publish legacy FB2/EPUB imports that predate imported-book publication.

REVOKE ALL ON TABLE
    public.chapter_paragraphs,
    public.chapters,
    public.project_source_views,
    public.book_source_members,
    public.chapter_source_members
FROM PUBLIC;

WITH latest_import AS (
  SELECT DISTINCT ON (job.project_id, job.result_json->>'bookId')
    job.project_id,
    job.result_json->>'bookId' AS book_id,
    job.result_json->>'sourceFileName' AS source_file_name,
    job.completed_at
  FROM public.jobs AS job
  WHERE job.kind = 'import'
    AND job.status = 'succeeded'
    AND job.completed_at IS NOT NULL
    AND jsonb_typeof(job.result_json) = 'object'
    AND job.result_json->>'type' = 'import'
    AND COALESCE(job.result_json->>'bookId', '') <> ''
  ORDER BY job.project_id, job.result_json->>'bookId', job.completed_at DESC, job.id DESC
),
eligible_books AS MATERIALIZED (
SELECT
  project.id AS project_id,
  project.lifecycle_generation AS project_generation,
  book.id AS book_id,
  book.lifecycle_generation AS book_generation,
  imported.completed_at AS published_at
FROM latest_import AS imported
JOIN public.books AS book
  ON book.project_id = imported.project_id
 AND book.id::text = imported.book_id
JOIN public.projects AS project ON project.id = book.project_id
WHERE lower(COALESCE(imported.source_file_name, '')) ~ '\.(fb2|epub)$'
  AND project.deleted_at IS NULL
  AND book.deleted_at IS NULL
  AND book.status = 'ready'
  AND EXISTS (
    SELECT 1
    FROM public.chapters AS chapter
    WHERE chapter.project_id = book.project_id
      AND chapter.book_id = book.id
  )
  AND NOT EXISTS (
    SELECT 1
    FROM public.chapters AS chapter
    WHERE chapter.project_id = book.project_id
      AND chapter.book_id = book.id
      AND (
        chapter.deleted_at IS NOT NULL
        OR chapter.status <> 'draft'
        OR chapter.published_revision IS NOT NULL
        OR chapter.published_at IS NOT NULL
        OR chapter.publish_error IS NOT NULL
      )
  )
  AND EXISTS (
    SELECT 1
    FROM public.book_source_members AS member
    WHERE member.project_id = book.project_id
      AND member.source_view = 'draft'
      AND member.project_lifecycle_generation = project.lifecycle_generation
      AND member.book_id = book.id
      AND member.book_lifecycle_generation = book.lifecycle_generation
      AND member.title = book.title
  )
  AND NOT EXISTS (
    SELECT 1
    FROM public.chapters AS chapter
    WHERE chapter.project_id = book.project_id
      AND chapter.book_id = book.id
      AND NOT EXISTS (
        SELECT 1
        FROM public.chapter_source_members AS member
        WHERE member.project_id = chapter.project_id
          AND member.source_view = 'draft'
          AND member.project_lifecycle_generation = project.lifecycle_generation
          AND member.book_id = chapter.book_id
          AND member.book_lifecycle_generation = book.lifecycle_generation
          AND member.chapter_id = chapter.id
          AND member.chapter_lifecycle_generation = chapter.lifecycle_generation
          AND member.source_revision = chapter.revision
          AND member.title = chapter.title
          AND member.chapter_order = chapter."order"
      )
  )
  AND NOT EXISTS (
    SELECT 1
    FROM public.project_source_views AS published_project
    WHERE published_project.project_id = project.id
      AND published_project.source_view = 'published'
      AND published_project.project_lifecycle_generation <> project.lifecycle_generation
  )
  AND NOT EXISTS (
    SELECT 1
    FROM public.book_source_members AS published_book
    WHERE published_book.project_id = book.project_id
      AND published_book.source_view = 'published'
      AND published_book.book_id = book.id
  )
  AND NOT EXISTS (
    SELECT 1
    FROM public.chapter_source_members AS published_chapter
    WHERE published_chapter.project_id = book.project_id
      AND published_chapter.source_view = 'published'
      AND published_chapter.book_id = book.id
  )
  AND NOT EXISTS (
    SELECT 1
    FROM public.chapters AS chapter
    JOIN public.chapter_paragraphs AS paragraph ON paragraph.chapter_id = chapter.id
    WHERE chapter.project_id = book.project_id
      AND chapter.book_id = book.id
      AND paragraph.variant = 'published'
  )
),
eligible_chapters AS MATERIALIZED (
SELECT
  eligible.project_id,
  eligible.project_generation,
  eligible.book_id,
  eligible.book_generation,
  eligible.published_at,
  chapter.id AS chapter_id,
  chapter.lifecycle_generation AS chapter_generation,
  chapter.revision,
  book_member.title AS book_title,
  chapter_member.title AS chapter_title,
  chapter_member.chapter_order
FROM eligible_books AS eligible
JOIN public.chapters AS chapter
  ON chapter.project_id = eligible.project_id
 AND chapter.book_id = eligible.book_id
JOIN public.book_source_members AS book_member
  ON book_member.project_id = eligible.project_id
 AND book_member.source_view = 'draft'
 AND book_member.project_lifecycle_generation = eligible.project_generation
 AND book_member.book_id = eligible.book_id
 AND book_member.book_lifecycle_generation = eligible.book_generation
JOIN public.chapter_source_members AS chapter_member
  ON chapter_member.project_id = eligible.project_id
 AND chapter_member.source_view = 'draft'
 AND chapter_member.project_lifecycle_generation = eligible.project_generation
 AND chapter_member.book_id = eligible.book_id
 AND chapter_member.book_lifecycle_generation = eligible.book_generation
 AND chapter_member.chapter_id = chapter.id
 AND chapter_member.chapter_lifecycle_generation = chapter.lifecycle_generation
 AND chapter_member.source_revision = chapter.revision
),
inserted_paragraphs AS (
INSERT INTO public.chapter_paragraphs (
  id, chapter_id, revision, variant, kind, "order",
  markdown, plain_text, word_count, created_at, updated_at
)
SELECT
  draft.id, draft.chapter_id, draft.revision, 'published', draft.kind, draft."order",
  draft.markdown, draft.plain_text, draft.word_count, draft.created_at, draft.updated_at
FROM eligible_chapters AS eligible
JOIN public.chapter_paragraphs AS draft
  ON draft.chapter_id = eligible.chapter_id
 AND draft.revision = eligible.revision
 AND draft.variant = 'draft'
ON CONFLICT (id, chapter_id, revision, variant) DO NOTHING
RETURNING id
),
updated_chapters AS (
UPDATE public.chapters AS chapter
SET status = 'published',
    published_revision = eligible.revision,
    published_at = eligible.published_at,
    publish_error = NULL,
    updated_at = eligible.published_at
FROM eligible_chapters AS eligible
WHERE chapter.project_id = eligible.project_id
  AND chapter.book_id = eligible.book_id
  AND chapter.id = eligible.chapter_id
  AND chapter.lifecycle_generation = eligible.chapter_generation
  AND chapter.revision = eligible.revision
  AND chapter.status = 'draft'
  AND chapter.published_revision IS NULL
  AND chapter.published_at IS NULL
  AND chapter.publish_error IS NULL
  AND chapter.deleted_at IS NULL
RETURNING chapter.id
),
bumped_catalog AS (
INSERT INTO public.project_source_views (
  project_id, source_view, project_lifecycle_generation, catalog_revision
)
SELECT DISTINCT project_id, 'published', project_generation, 1
FROM eligible_books
ON CONFLICT (project_id, source_view) DO UPDATE
SET catalog_revision = public.project_source_views.catalog_revision + 1
WHERE public.project_source_views.project_lifecycle_generation = EXCLUDED.project_lifecycle_generation
RETURNING project_id
),
inserted_book_members AS (
INSERT INTO public.book_source_members (
  project_id, source_view, project_lifecycle_generation,
  book_id, book_lifecycle_generation, title
)
SELECT DISTINCT
  project_id, 'published', project_generation,
  book_id, book_generation, book_title
FROM eligible_chapters
ON CONFLICT (project_id, source_view, book_id) DO NOTHING
RETURNING book_id
),
inserted_chapter_members AS (
INSERT INTO public.chapter_source_members (
  project_id, source_view, project_lifecycle_generation,
  book_id, book_lifecycle_generation,
  chapter_id, chapter_lifecycle_generation,
  source_revision, title, chapter_order
)
SELECT
  project_id, 'published', project_generation,
  book_id, book_generation,
  chapter_id, chapter_generation,
  revision, chapter_title, chapter_order
FROM eligible_chapters
ON CONFLICT (project_id, source_view, chapter_id) DO NOTHING
RETURNING chapter_id
)
SELECT
  (SELECT count(*) FROM inserted_paragraphs) AS paragraph_count,
  (SELECT count(*) FROM updated_chapters) AS chapter_count,
  (SELECT count(*) FROM bumped_catalog) AS project_count,
  (SELECT count(*) FROM inserted_book_members) AS book_member_count,
  (SELECT count(*) FROM inserted_chapter_members) AS chapter_member_count;
