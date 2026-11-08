-- name: ListCanonicalTextSelectionRows :many
WITH exact_marker AS (
  SELECT
    marker.project_id,
    marker.source_view,
    marker.project_lifecycle_generation
  FROM project_source_views AS marker
  JOIN projects AS project
    ON project.id = marker.project_id
   AND project.lifecycle_generation = marker.project_lifecycle_generation
   AND project.deleted_at IS NULL
  WHERE marker.project_id = sqlc.arg(project_id)
    AND marker.source_view = sqlc.arg(source_view)
), current_book_members AS (
  SELECT member.*
  FROM book_source_members AS member
  JOIN books AS current_book
    ON current_book.id = member.book_id
   AND current_book.project_id = member.project_id
   AND current_book.deleted_at IS NULL
  WHERE member.project_id = sqlc.arg(project_id)
    AND member.source_view = sqlc.arg(source_view)
    AND (
      member.source_view <> 'published'
      OR EXISTS (
        SELECT 1
        FROM chapter_source_members AS chapter
        JOIN chapters AS current_chapter
          ON current_chapter.id = chapter.chapter_id
         AND current_chapter.project_id = chapter.project_id
         AND current_chapter.book_id = chapter.book_id
         AND current_chapter.deleted_at IS NULL
        WHERE chapter.project_id = member.project_id
          AND chapter.source_view = member.source_view
          AND chapter.project_lifecycle_generation = member.project_lifecycle_generation
          AND chapter.book_id = member.book_id
          AND chapter.book_lifecycle_generation = member.book_lifecycle_generation
      )
    )
), current_chapter_members AS (
  SELECT member.*
  FROM chapter_source_members AS member
  JOIN chapters AS current_chapter
    ON current_chapter.id = member.chapter_id
   AND current_chapter.project_id = member.project_id
   AND current_chapter.book_id = member.book_id
   AND current_chapter.deleted_at IS NULL
  WHERE member.project_id = sqlc.arg(project_id)
    AND member.source_view = sqlc.arg(source_view)
)
SELECT
  marker.project_id,
  marker.source_view,
  marker.project_lifecycle_generation,
  member.book_id,
  member.book_lifecycle_generation,
  member.title AS book_title,
  live_book.id AS live_book_id,
  chapter.chapter_id,
  chapter.chapter_lifecycle_generation,
  chapter.source_revision,
  chapter.title AS chapter_title,
  chapter.chapter_order,
  live_chapter.id AS live_chapter_id,
  state.source_revision AS state_source_revision,
  state.locator_source_generation AS state_source_generation,
  state.chapter_content_hash AS state_chapter_content_hash
FROM exact_marker AS marker
LEFT JOIN current_book_members AS member
  ON member.project_id = marker.project_id
 AND member.source_view = marker.source_view
 AND member.project_lifecycle_generation = marker.project_lifecycle_generation
LEFT JOIN books AS live_book
  ON live_book.id = member.book_id
 AND live_book.project_id = member.project_id
 AND live_book.lifecycle_generation = member.book_lifecycle_generation
 AND live_book.deleted_at IS NULL
LEFT JOIN current_chapter_members AS chapter
  ON chapter.project_id = member.project_id
 AND chapter.source_view = member.source_view
 AND chapter.project_lifecycle_generation = member.project_lifecycle_generation
 AND chapter.book_id = member.book_id
 AND chapter.book_lifecycle_generation = member.book_lifecycle_generation
LEFT JOIN chapters AS live_chapter
  ON live_chapter.id = chapter.chapter_id
 AND live_chapter.project_id = chapter.project_id
 AND live_chapter.book_id = chapter.book_id
 AND live_chapter.lifecycle_generation = chapter.chapter_lifecycle_generation
 AND live_chapter.deleted_at IS NULL
LEFT JOIN chapter_text_index_state AS state
  ON state.project_id = chapter.project_id
 AND state.source_view = chapter.source_view
 AND state.chapter_id = chapter.chapter_id
ORDER BY member.book_id, chapter.chapter_order, chapter.chapter_id;

-- name: SearchCanonicalLexical :many
WITH manuscript_query AS (
  SELECT websearch_to_tsquery('russian'::regconfig, sqlc.arg(query)) AS tsq
),
chapter_results AS (
  SELECT
    document.chapter_id AS id,
    document.project_id,
    document.book_id,
    document.chapter_id,
    document.source_revision,
    document.source_view,
    document.locator_source_generation,
    document.content,
    document.chapter_content_hash AS content_hash,
    source_book.title AS book_title,
    source_chapter.title AS chapter_title,
    (
      SELECT count(*)::integer + 1
      FROM chapter_source_members AS prior
      WHERE prior.project_id = source_chapter.project_id
        AND prior.source_view = source_chapter.source_view
        AND prior.project_lifecycle_generation = source_chapter.project_lifecycle_generation
        AND prior.book_id = source_chapter.book_id
        AND prior.book_lifecycle_generation = source_chapter.book_lifecycle_generation
        AND (prior.chapter_order, prior.chapter_id) < (source_chapter.chapter_order, source_chapter.chapter_id)
    ) AS chapter_position,
    COALESCE(matched.id::text, '')::text AS source_paragraph_id,
    COALESCE(matched.position, 0)::integer AS source_paragraph_position,
    COALESCE(matched.plain_text, '') AS source_paragraph_content,
    ts_rank(document.search_vector, manuscript_query.tsq, 0)::float8 AS rank
  FROM chapter_text_search_rows AS document
  JOIN project_source_views AS marker
    ON marker.project_id = document.project_id
   AND marker.source_view = document.source_view
  JOIN projects AS live_project
    ON live_project.id = marker.project_id
   AND live_project.lifecycle_generation = marker.project_lifecycle_generation
   AND live_project.deleted_at IS NULL
  JOIN book_source_members AS source_book
    ON source_book.project_id = marker.project_id
   AND source_book.source_view = marker.source_view
   AND source_book.project_lifecycle_generation = marker.project_lifecycle_generation
   AND source_book.book_id = document.book_id
  JOIN books AS live_book
    ON live_book.id = source_book.book_id
   AND live_book.project_id = source_book.project_id
   AND live_book.lifecycle_generation = source_book.book_lifecycle_generation
   AND live_book.deleted_at IS NULL
  JOIN chapter_source_members AS source_chapter
    ON source_chapter.project_id = source_book.project_id
   AND source_chapter.source_view = source_book.source_view
   AND source_chapter.project_lifecycle_generation = source_book.project_lifecycle_generation
   AND source_chapter.book_id = source_book.book_id
   AND source_chapter.book_lifecycle_generation = source_book.book_lifecycle_generation
   AND source_chapter.chapter_id = document.chapter_id
   AND source_chapter.source_revision = document.source_revision
  JOIN chapters AS live_chapter
    ON live_chapter.id = source_chapter.chapter_id
   AND live_chapter.project_id = source_chapter.project_id
   AND live_chapter.book_id = source_chapter.book_id
   AND live_chapter.lifecycle_generation = source_chapter.chapter_lifecycle_generation
   AND live_chapter.deleted_at IS NULL
  JOIN chapter_text_index_state AS state
    ON state.project_id = source_chapter.project_id
   AND state.source_view = source_chapter.source_view
   AND state.chapter_id = source_chapter.chapter_id
   AND state.source_revision = source_chapter.source_revision
   AND state.locator_source_generation = document.locator_source_generation
   AND state.chapter_content_hash = document.chapter_content_hash
  CROSS JOIN manuscript_query
  LEFT JOIN LATERAL (
    SELECT paragraph.id, paragraph.plain_text,
      (
        SELECT count(*)::integer + 1 FROM chapter_paragraphs AS prior
        WHERE prior.chapter_id = paragraph.chapter_id
          AND prior.revision = paragraph.revision AND prior.variant = paragraph.variant
          AND (prior."order", prior.id) < (paragraph."order", paragraph.id)
      ) AS position
    FROM chapter_paragraphs AS paragraph
    WHERE paragraph.chapter_id = document.chapter_id
      AND paragraph.revision = document.source_revision
      AND paragraph.variant = document.source_view
      AND to_tsvector('russian'::regconfig, paragraph.plain_text) @@ manuscript_query.tsq
    ORDER BY paragraph."order", paragraph.id
    LIMIT 1
  ) AS matched ON true
  WHERE document.project_id = sqlc.arg(project_id)
    AND document.source_view = sqlc.arg(source_view)
    AND document.book_id = ANY(sqlc.arg(current_book_ids)::uuid[])
    AND document.locator_source_generation = ck_source_generation_digest(
      live_project.id, live_project.lifecycle_generation,
      live_book.id, live_book.lifecycle_generation,
      live_chapter.id, live_chapter.lifecycle_generation
    )
    AND document.search_vector @@ manuscript_query.tsq
    AND (sqlc.narg(book_id)::uuid IS NULL OR document.book_id = sqlc.narg(book_id)::uuid)
    AND (sqlc.narg(chapter_id)::uuid IS NULL OR document.chapter_id = sqlc.narg(chapter_id)::uuid)
    AND (sqlc.narg(updated_since)::timestamptz IS NULL OR state.indexed_at >= sqlc.narg(updated_since)::timestamptz)
)
SELECT
  id,
  project_id,
  book_id,
  chapter_id,
  source_revision,
  source_view,
  locator_source_generation,
  content,
  content_hash,
  book_title,
  chapter_title,
  chapter_position,
  source_paragraph_id,
  source_paragraph_position,
  source_paragraph_content,
  rank
FROM chapter_results
WHERE NOT sqlc.arg(has_cursor)::boolean
   OR rank < sqlc.arg(cursor_rank)::float8
   OR (
     rank = sqlc.arg(cursor_rank)::float8
     AND (book_id, chapter_id, id) >
       (sqlc.arg(cursor_book_id)::uuid, sqlc.arg(cursor_chapter_id)::uuid, sqlc.arg(cursor_result_id)::uuid)
   )
ORDER BY rank DESC, book_id, chapter_id, id
LIMIT sqlc.arg(limit_count);

-- name: ListCanonicalExactChapterDocuments :many
SELECT
  source_chapter.chapter_order,
  document.chapter_id,
  document.content
FROM chapter_text_search_rows AS document
JOIN project_source_views AS marker
  ON marker.project_id=document.project_id AND marker.source_view=document.source_view
JOIN projects AS project
  ON project.id=marker.project_id
 AND project.lifecycle_generation=marker.project_lifecycle_generation
 AND project.deleted_at IS NULL
JOIN books AS book
  ON book.project_id=project.id AND book.id=document.book_id AND book.deleted_at IS NULL
JOIN chapter_source_members AS source_chapter
  ON source_chapter.project_id=document.project_id
 AND source_chapter.source_view=document.source_view
 AND source_chapter.project_lifecycle_generation=project.lifecycle_generation
 AND source_chapter.book_id=document.book_id
 AND source_chapter.book_lifecycle_generation=book.lifecycle_generation
 AND source_chapter.chapter_id=document.chapter_id
 AND source_chapter.source_revision=document.source_revision
JOIN chapters AS chapter
  ON chapter.project_id=document.project_id
 AND chapter.book_id=document.book_id
 AND chapter.id=document.chapter_id
 AND chapter.lifecycle_generation=source_chapter.chapter_lifecycle_generation
 AND chapter.deleted_at IS NULL
JOIN chapter_text_index_state AS state
  ON state.project_id=document.project_id
 AND state.source_view=document.source_view
 AND state.chapter_id=document.chapter_id
 AND state.source_revision=document.source_revision
 AND state.locator_source_generation=document.locator_source_generation
 AND state.chapter_content_hash=document.chapter_content_hash
WHERE document.project_id=sqlc.arg(project_id)
  AND document.source_view=sqlc.arg(source_view)
  AND document.book_id=sqlc.arg(book_id)
  AND document.locator_source_generation=ck_source_generation_digest(
    project.id, project.lifecycle_generation,
    book.id, book.lifecycle_generation,
    chapter.id, chapter.lifecycle_generation
  )
  AND document.search_vector @@ plainto_tsquery('russian'::regconfig, sqlc.arg(query))
  AND (sqlc.narg(chapter_id)::uuid IS NULL OR document.chapter_id=sqlc.narg(chapter_id)::uuid)
  AND (
    NOT sqlc.arg(has_cursor)::boolean OR
    (source_chapter.chapter_order, document.chapter_id) >
      (sqlc.arg(cursor_chapter_order)::integer, sqlc.arg(cursor_chapter_id)::uuid)
  )
ORDER BY source_chapter.chapter_order, document.chapter_id
LIMIT sqlc.arg(candidate_limit);
