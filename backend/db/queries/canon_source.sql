-- name: LockCurrentSourceProject :one
SELECT id, lifecycle_generation
FROM projects
WHERE id = sqlc.arg(project_id)
  AND lifecycle_generation = sqlc.arg(project_lifecycle_generation)
  AND deleted_at IS NULL
FOR SHARE;

-- name: LockCurrentSourceBook :one
SELECT id, lifecycle_generation
FROM books
WHERE project_id = sqlc.arg(project_id)
  AND id = sqlc.arg(book_id)
  AND lifecycle_generation = sqlc.arg(book_lifecycle_generation)
  AND deleted_at IS NULL
FOR SHARE;

-- name: LockCurrentSourceChapter :one
SELECT id, lifecycle_generation
FROM chapters
WHERE project_id = sqlc.arg(project_id)
  AND book_id = sqlc.arg(book_id)
  AND id = sqlc.arg(chapter_id)
  AND lifecycle_generation = sqlc.arg(chapter_lifecycle_generation)
  AND deleted_at IS NULL
FOR SHARE;

-- name: LockCurrentSourceChaptersForBook :many
SELECT id
FROM chapters
WHERE project_id = sqlc.arg(project_id)
  AND book_id = sqlc.arg(book_id)
  AND deleted_at IS NULL
ORDER BY id
FOR SHARE;

-- name: LockLiveSourceBooksForProject :many
SELECT id, lifecycle_generation, title
FROM books
WHERE project_id = sqlc.arg(project_id)
  AND deleted_at IS NULL
ORDER BY id
FOR SHARE;

-- name: LockLiveSourceChaptersForProject :many
SELECT
  id, book_id, lifecycle_generation, revision, published_revision,
  title, "order" AS chapter_order
FROM chapters
WHERE project_id = sqlc.arg(project_id)
  AND deleted_at IS NULL
ORDER BY id
FOR SHARE;

-- name: CreateDraftProjectSourceView :one
INSERT INTO project_source_views (
  project_id, source_view, project_lifecycle_generation, catalog_revision
)
VALUES (
  sqlc.arg(project_id), 'draft', sqlc.arg(project_lifecycle_generation), 1
)
RETURNING project_id, source_view, project_lifecycle_generation, catalog_revision;

-- name: CreateRestoredProjectSourceView :one
INSERT INTO project_source_views (
  project_id, source_view, project_lifecycle_generation, catalog_revision
)
VALUES (
  sqlc.arg(project_id), sqlc.arg(source_view),
  sqlc.arg(project_lifecycle_generation), sqlc.arg(catalog_revision)
)
RETURNING project_id, source_view, project_lifecycle_generation, catalog_revision;

-- name: LockProjectSourceView :one
SELECT project_id, source_view, project_lifecycle_generation, catalog_revision
FROM project_source_views
WHERE project_id = sqlc.arg(project_id)
  AND source_view = sqlc.arg(source_view)
  AND project_lifecycle_generation = sqlc.arg(project_lifecycle_generation)
FOR UPDATE;

-- name: BumpProjectSourceView :one
UPDATE project_source_views
SET catalog_revision = catalog_revision + 1
WHERE project_id = sqlc.arg(project_id)
  AND source_view = sqlc.arg(source_view)
  AND project_lifecycle_generation = sqlc.arg(project_lifecycle_generation)
RETURNING project_id, source_view, project_lifecycle_generation, catalog_revision;

-- name: CreateOrBumpPublishedProjectSourceView :one
INSERT INTO project_source_views (
  project_id, source_view, project_lifecycle_generation, catalog_revision
)
VALUES (
  sqlc.arg(project_id), 'published', sqlc.arg(project_lifecycle_generation), 1
)
ON CONFLICT (project_id, source_view) DO UPDATE
SET catalog_revision = project_source_views.catalog_revision + 1
WHERE project_source_views.project_lifecycle_generation = EXCLUDED.project_lifecycle_generation
RETURNING project_id, source_view, project_lifecycle_generation, catalog_revision;

-- name: LockBookSourceMember :one
SELECT book_id
FROM book_source_members
WHERE project_id = sqlc.arg(project_id)
  AND source_view = sqlc.arg(source_view)
  AND project_lifecycle_generation = sqlc.arg(project_lifecycle_generation)
  AND book_id = sqlc.arg(book_id)
  AND book_lifecycle_generation = sqlc.arg(book_lifecycle_generation)
FOR UPDATE;

-- name: UpsertBookSourceMember :exec
INSERT INTO book_source_members (
  project_id, source_view, project_lifecycle_generation,
  book_id, book_lifecycle_generation, title
)
VALUES (
  sqlc.arg(project_id), sqlc.arg(source_view), sqlc.arg(project_lifecycle_generation),
  sqlc.arg(book_id), sqlc.arg(book_lifecycle_generation), sqlc.arg(title)
)
ON CONFLICT (project_id, source_view, book_id) DO UPDATE
SET project_lifecycle_generation = EXCLUDED.project_lifecycle_generation,
    book_lifecycle_generation = EXCLUDED.book_lifecycle_generation,
    title = EXCLUDED.title;

-- name: UpsertChapterSourceMember :exec
INSERT INTO chapter_source_members (
  project_id, source_view, project_lifecycle_generation,
  book_id, book_lifecycle_generation,
  chapter_id, chapter_lifecycle_generation, source_revision,
  title, chapter_order
)
VALUES (
  sqlc.arg(project_id), sqlc.arg(source_view), sqlc.arg(project_lifecycle_generation),
  sqlc.arg(book_id), sqlc.arg(book_lifecycle_generation),
  sqlc.arg(chapter_id), sqlc.arg(chapter_lifecycle_generation), sqlc.arg(source_revision),
  sqlc.arg(title), sqlc.arg(chapter_order)
)
ON CONFLICT (project_id, source_view, chapter_id) DO UPDATE
SET project_lifecycle_generation = EXCLUDED.project_lifecycle_generation,
    book_id = EXCLUDED.book_id,
    book_lifecycle_generation = EXCLUDED.book_lifecycle_generation,
    chapter_lifecycle_generation = EXCLUDED.chapter_lifecycle_generation,
    source_revision = EXCLUDED.source_revision,
    title = EXCLUDED.title,
    chapter_order = EXCLUDED.chapter_order;

-- name: NormalizePublishedChapterOrder :exec
WITH ordered AS (
  SELECT
    published.chapter_id,
    row_number() OVER (
      ORDER BY draft.chapter_order, published.chapter_id
    )::integer - 1 AS normalized_order
  FROM chapter_source_members AS published
  JOIN chapter_source_members AS draft
    ON draft.project_id = published.project_id
   AND draft.source_view = 'draft'
   AND draft.project_lifecycle_generation = published.project_lifecycle_generation
   AND draft.book_id = published.book_id
   AND draft.book_lifecycle_generation = published.book_lifecycle_generation
   AND draft.chapter_id = published.chapter_id
   AND draft.chapter_lifecycle_generation = published.chapter_lifecycle_generation
  JOIN chapters AS chapter
    ON chapter.id = published.chapter_id
   AND chapter.project_id = published.project_id
   AND chapter.book_id = published.book_id
   AND chapter.lifecycle_generation = published.chapter_lifecycle_generation
   AND chapter.deleted_at IS NULL
  WHERE published.project_id = sqlc.arg(project_id)
    AND published.source_view = 'published'
    AND published.project_lifecycle_generation = sqlc.arg(project_lifecycle_generation)
    AND published.book_id = sqlc.arg(book_id)
    AND published.book_lifecycle_generation = sqlc.arg(book_lifecycle_generation)
)
UPDATE chapter_source_members AS member
SET chapter_order = ordered.normalized_order
FROM ordered
WHERE member.project_id = sqlc.arg(project_id)
  AND member.source_view = 'published'
  AND member.chapter_id = ordered.chapter_id;

-- name: ListCurrentBookSourceMembers :many
SELECT
  member.project_id, member.source_view, member.project_lifecycle_generation,
  member.book_id, member.book_lifecycle_generation, member.title
FROM book_source_members AS member
JOIN project_source_views AS marker
  ON marker.project_id = member.project_id
 AND marker.source_view = member.source_view
 AND marker.project_lifecycle_generation = member.project_lifecycle_generation
JOIN projects AS project
  ON project.id = marker.project_id
 AND project.lifecycle_generation = marker.project_lifecycle_generation
 AND project.deleted_at IS NULL
JOIN books AS book
  ON book.id = member.book_id
 AND book.project_id = member.project_id
 AND book.lifecycle_generation = member.book_lifecycle_generation
 AND book.deleted_at IS NULL
WHERE member.project_id = sqlc.arg(project_id)
  AND member.source_view = sqlc.arg(source_view)
  AND (
    member.source_view <> 'published'
    OR EXISTS (
      SELECT 1
      FROM chapter_source_members AS chapter_member
      JOIN chapters AS chapter
        ON chapter.id = chapter_member.chapter_id
       AND chapter.project_id = chapter_member.project_id
       AND chapter.book_id = chapter_member.book_id
       AND chapter.lifecycle_generation = chapter_member.chapter_lifecycle_generation
       AND chapter.deleted_at IS NULL
      WHERE chapter_member.project_id = member.project_id
        AND chapter_member.source_view = member.source_view
        AND chapter_member.project_lifecycle_generation = member.project_lifecycle_generation
        AND chapter_member.book_id = member.book_id
        AND chapter_member.book_lifecycle_generation = member.book_lifecycle_generation
    )
  )
ORDER BY book."order", member.book_id;

-- name: ListCurrentChapterSourceMembers :many
SELECT
  member.project_id, member.source_view, member.project_lifecycle_generation,
  member.book_id, member.book_lifecycle_generation,
  member.chapter_id, member.chapter_lifecycle_generation,
  member.source_revision, member.title, member.chapter_order
FROM chapter_source_members AS member
JOIN project_source_views AS marker
  ON marker.project_id = member.project_id
 AND marker.source_view = member.source_view
 AND marker.project_lifecycle_generation = member.project_lifecycle_generation
JOIN projects AS project
  ON project.id = marker.project_id
 AND project.lifecycle_generation = marker.project_lifecycle_generation
 AND project.deleted_at IS NULL
JOIN book_source_members AS book_member
  ON book_member.project_id = member.project_id
 AND book_member.source_view = member.source_view
 AND book_member.project_lifecycle_generation = member.project_lifecycle_generation
 AND book_member.book_id = member.book_id
 AND book_member.book_lifecycle_generation = member.book_lifecycle_generation
JOIN books AS book
  ON book.id = member.book_id
 AND book.project_id = member.project_id
 AND book.lifecycle_generation = member.book_lifecycle_generation
 AND book.deleted_at IS NULL
JOIN chapters AS chapter
  ON chapter.id = member.chapter_id
 AND chapter.project_id = member.project_id
 AND chapter.book_id = member.book_id
 AND chapter.lifecycle_generation = member.chapter_lifecycle_generation
 AND chapter.deleted_at IS NULL
WHERE member.project_id = sqlc.arg(project_id)
  AND member.source_view = sqlc.arg(source_view)
ORDER BY book."order", member.chapter_order, member.chapter_id;

-- name: ListChapterSourceSnapshot :many
SELECT
  paragraph.id, paragraph.chapter_id, paragraph.revision, paragraph.variant,
  paragraph.kind, paragraph."order", paragraph.markdown, paragraph.plain_text,
  paragraph.word_count, paragraph.created_at, paragraph.updated_at
FROM chapter_source_members AS member
JOIN project_source_views AS marker
  ON marker.project_id = member.project_id
 AND marker.source_view = member.source_view
 AND marker.project_lifecycle_generation = member.project_lifecycle_generation
JOIN projects AS project
  ON project.id = marker.project_id
 AND project.lifecycle_generation = marker.project_lifecycle_generation
 AND project.deleted_at IS NULL
JOIN books AS book
  ON book.id = member.book_id
 AND book.project_id = member.project_id
 AND book.lifecycle_generation = member.book_lifecycle_generation
 AND book.deleted_at IS NULL
JOIN chapters AS chapter
  ON chapter.id = member.chapter_id
 AND chapter.project_id = member.project_id
 AND chapter.book_id = member.book_id
 AND chapter.lifecycle_generation = member.chapter_lifecycle_generation
 AND chapter.deleted_at IS NULL
JOIN chapter_paragraphs AS paragraph
  ON paragraph.chapter_id = member.chapter_id
 AND paragraph.revision = member.source_revision
 AND paragraph.variant = member.source_view
WHERE member.project_id = sqlc.arg(project_id)
  AND member.source_view = sqlc.arg(source_view)
  AND member.chapter_id = sqlc.arg(chapter_id)
ORDER BY paragraph."order", paragraph.id;

-- name: LoadCurrentBookSourceSnapshotRows :many
WITH current_book AS (
  SELECT
    member.project_id, member.source_view,
    member.project_lifecycle_generation, marker.catalog_revision,
    member.book_id, member.book_lifecycle_generation,
    book."order" AS project_book_order, member.title AS book_title
  FROM book_source_members AS member
  JOIN project_source_views AS marker
    ON marker.project_id = member.project_id
   AND marker.source_view = member.source_view
   AND marker.project_lifecycle_generation = member.project_lifecycle_generation
  JOIN projects AS project
    ON project.id = marker.project_id
   AND project.lifecycle_generation = marker.project_lifecycle_generation
   AND project.deleted_at IS NULL
  JOIN books AS book
    ON book.id = member.book_id
   AND book.project_id = member.project_id
   AND book.lifecycle_generation = member.book_lifecycle_generation
   AND book.deleted_at IS NULL
  WHERE member.project_id = sqlc.arg(project_id)
    AND member.source_view = sqlc.arg(source_view)
    AND member.book_id = sqlc.arg(book_id)
    AND NOT EXISTS (
      SELECT 1
      FROM chapter_source_members AS stale_member
      JOIN chapters AS live_chapter
        ON live_chapter.id = stale_member.chapter_id
       AND live_chapter.project_id = stale_member.project_id
       AND live_chapter.book_id = stale_member.book_id
       AND live_chapter.deleted_at IS NULL
      WHERE stale_member.project_id = member.project_id
        AND stale_member.source_view = member.source_view
        AND stale_member.project_lifecycle_generation = member.project_lifecycle_generation
        AND stale_member.book_id = member.book_id
        AND stale_member.book_lifecycle_generation = member.book_lifecycle_generation
        AND live_chapter.lifecycle_generation <> stale_member.chapter_lifecycle_generation
    )
    AND (
      member.source_view <> 'published'
      OR EXISTS (
        SELECT 1
        FROM chapter_source_members AS published_member
        JOIN chapters AS published_chapter
          ON published_chapter.id = published_member.chapter_id
         AND published_chapter.project_id = published_member.project_id
         AND published_chapter.book_id = published_member.book_id
         AND published_chapter.lifecycle_generation = published_member.chapter_lifecycle_generation
         AND published_chapter.deleted_at IS NULL
        WHERE published_member.project_id = member.project_id
          AND published_member.source_view = member.source_view
          AND published_member.project_lifecycle_generation = member.project_lifecycle_generation
          AND published_member.book_id = member.book_id
          AND published_member.book_lifecycle_generation = member.book_lifecycle_generation
      )
    )
), current_chapters AS (
  SELECT
    member.chapter_id, member.chapter_lifecycle_generation,
    member.source_revision, member.title AS chapter_title,
    member.chapter_order
  FROM chapter_source_members AS member
  JOIN current_book AS book
    ON book.project_id = member.project_id
   AND book.source_view = member.source_view
   AND book.project_lifecycle_generation = member.project_lifecycle_generation
   AND book.book_id = member.book_id
   AND book.book_lifecycle_generation = member.book_lifecycle_generation
  JOIN chapters AS chapter
    ON chapter.id = member.chapter_id
   AND chapter.project_id = book.project_id
   AND chapter.book_id = book.book_id
   AND chapter.lifecycle_generation = member.chapter_lifecycle_generation
   AND chapter.deleted_at IS NULL
)
SELECT
  book.project_id, book.source_view,
  book.project_lifecycle_generation, book.catalog_revision,
  book.book_id, book.book_lifecycle_generation,
  book.project_book_order, book.book_title,
  chapter.chapter_id, chapter.chapter_lifecycle_generation,
  chapter.source_revision, chapter.chapter_title, chapter.chapter_order,
  paragraph.id AS paragraph_id, paragraph."order" AS paragraph_order,
  paragraph.plain_text AS paragraph_content
FROM current_book AS book
LEFT JOIN current_chapters AS chapter ON true
LEFT JOIN chapter_paragraphs AS paragraph
  ON paragraph.chapter_id = chapter.chapter_id
 AND paragraph.revision = chapter.source_revision
 AND paragraph.variant = book.source_view
ORDER BY chapter.chapter_order, chapter.chapter_id, paragraph."order", paragraph.id;

-- name: ResolveCanonicalChapterSource :one
SELECT
  document.content,
  book_member.book_id,
  book_member.title AS book_title,
  chapter_member.title AS chapter_title,
  (
    SELECT count(*)::integer + 1
    FROM chapter_source_members AS prior
    WHERE prior.project_id = chapter_member.project_id
      AND prior.source_view = chapter_member.source_view
      AND prior.project_lifecycle_generation = chapter_member.project_lifecycle_generation
      AND prior.book_id = chapter_member.book_id
      AND prior.book_lifecycle_generation = chapter_member.book_lifecycle_generation
      AND (prior.chapter_order, prior.chapter_id) < (chapter_member.chapter_order, chapter_member.chapter_id)
  ) AS chapter_position,
  document.source_revision,
  document.locator_source_generation,
  document.chapter_content_hash,
  project.lifecycle_generation AS project_lifecycle_generation,
  book.lifecycle_generation AS book_lifecycle_generation,
  chapter.lifecycle_generation AS chapter_lifecycle_generation
FROM project_source_views AS marker
JOIN projects AS project
  ON project.id = marker.project_id
 AND project.lifecycle_generation = marker.project_lifecycle_generation
 AND project.deleted_at IS NULL
JOIN book_source_members AS book_member
  ON book_member.project_id = marker.project_id
 AND book_member.source_view = marker.source_view
 AND book_member.project_lifecycle_generation = marker.project_lifecycle_generation
JOIN books AS book
  ON book.id = book_member.book_id
 AND book.project_id = book_member.project_id
 AND book.lifecycle_generation = book_member.book_lifecycle_generation
 AND book.deleted_at IS NULL
JOIN chapter_source_members AS chapter_member
  ON chapter_member.project_id = book_member.project_id
 AND chapter_member.source_view = book_member.source_view
 AND chapter_member.project_lifecycle_generation = book_member.project_lifecycle_generation
 AND chapter_member.book_id = book_member.book_id
 AND chapter_member.book_lifecycle_generation = book_member.book_lifecycle_generation
 AND chapter_member.chapter_id = sqlc.arg(chapter_id)
JOIN chapters AS chapter
  ON chapter.id = chapter_member.chapter_id
 AND chapter.project_id = chapter_member.project_id
 AND chapter.book_id = chapter_member.book_id
 AND chapter.lifecycle_generation = chapter_member.chapter_lifecycle_generation
 AND chapter.deleted_at IS NULL
JOIN chapter_text_search_rows AS document
  ON document.project_id = chapter_member.project_id
 AND document.source_view = chapter_member.source_view
 AND document.book_id = chapter_member.book_id
 AND document.chapter_id = chapter_member.chapter_id
 AND document.source_revision = chapter_member.source_revision
 AND document.locator_source_generation = ck_source_generation_digest(
   project.id, project.lifecycle_generation,
   book.id, book.lifecycle_generation,
   chapter.id, chapter.lifecycle_generation
 )
JOIN chapter_text_index_state AS state
  ON state.project_id = document.project_id
 AND state.source_view = document.source_view
 AND state.chapter_id = document.chapter_id
 AND state.source_revision = document.source_revision
 AND state.locator_source_generation = document.locator_source_generation
 AND state.chapter_content_hash = document.chapter_content_hash
WHERE marker.project_id = sqlc.arg(project_id)
  AND marker.source_view = sqlc.arg(source_view)
  AND (sqlc.narg(book_id)::uuid IS NULL OR book_member.book_id = sqlc.narg(book_id));

-- name: ListCanonicalChapterSourceRefs :many
SELECT
  book_member.book_id,
  chapter_member.chapter_id,
  document.source_revision,
  document.locator_source_generation,
  document.chapter_content_hash,
  project.lifecycle_generation AS project_lifecycle_generation,
  book.lifecycle_generation AS book_lifecycle_generation,
  chapter.lifecycle_generation AS chapter_lifecycle_generation
FROM project_source_views AS marker
JOIN projects AS project
  ON project.id = marker.project_id
 AND project.lifecycle_generation = marker.project_lifecycle_generation
 AND project.deleted_at IS NULL
JOIN book_source_members AS book_member
  ON book_member.project_id = marker.project_id
 AND book_member.source_view = marker.source_view
 AND book_member.project_lifecycle_generation = marker.project_lifecycle_generation
JOIN books AS book
  ON book.id = book_member.book_id
 AND book.project_id = book_member.project_id
 AND book.lifecycle_generation = book_member.book_lifecycle_generation
 AND book.deleted_at IS NULL
JOIN chapter_source_members AS chapter_member
  ON chapter_member.project_id = book_member.project_id
 AND chapter_member.source_view = book_member.source_view
 AND chapter_member.project_lifecycle_generation = book_member.project_lifecycle_generation
 AND chapter_member.book_id = book_member.book_id
 AND chapter_member.book_lifecycle_generation = book_member.book_lifecycle_generation
 AND chapter_member.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[])
JOIN chapters AS chapter
  ON chapter.id = chapter_member.chapter_id
 AND chapter.project_id = chapter_member.project_id
 AND chapter.book_id = chapter_member.book_id
 AND chapter.lifecycle_generation = chapter_member.chapter_lifecycle_generation
 AND chapter.deleted_at IS NULL
JOIN chapter_text_search_rows AS document
  ON document.project_id = chapter_member.project_id
 AND document.source_view = chapter_member.source_view
 AND document.book_id = chapter_member.book_id
 AND document.chapter_id = chapter_member.chapter_id
 AND document.source_revision = chapter_member.source_revision
 AND document.locator_source_generation = ck_source_generation_digest(
   project.id, project.lifecycle_generation,
   book.id, book.lifecycle_generation,
   chapter.id, chapter.lifecycle_generation
 )
JOIN chapter_text_index_state AS state
  ON state.project_id = document.project_id
 AND state.source_view = document.source_view
 AND state.chapter_id = document.chapter_id
 AND state.source_revision = document.source_revision
 AND state.locator_source_generation = document.locator_source_generation
 AND state.chapter_content_hash = document.chapter_content_hash
WHERE marker.project_id = sqlc.arg(project_id)
  AND marker.source_view = sqlc.arg(source_view);

-- name: ListRetainedProjectSourceViews :many
SELECT project_id, source_view, project_lifecycle_generation, catalog_revision
FROM project_source_views
WHERE project_id = sqlc.arg(project_id)
ORDER BY source_view;

-- name: ListRetainedBookSourceMembers :many
SELECT
  project_id, source_view, project_lifecycle_generation,
  book_id, book_lifecycle_generation, title
FROM book_source_members
WHERE project_id = sqlc.arg(project_id)
ORDER BY source_view, book_id;

-- name: ListRetainedChapterSourceMembers :many
SELECT
  project_id, source_view, project_lifecycle_generation,
  book_id, book_lifecycle_generation,
  chapter_id, chapter_lifecycle_generation,
  source_revision, title, chapter_order
FROM chapter_source_members
WHERE project_id = sqlc.arg(project_id)
ORDER BY source_view, book_id, chapter_order, chapter_id;

-- name: DeleteRetainedProjectSourceViews :exec
DELETE FROM project_source_views
WHERE project_id = sqlc.arg(project_id);
