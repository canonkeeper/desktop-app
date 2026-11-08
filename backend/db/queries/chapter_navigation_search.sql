-- name: SearchChapterNavigation :many
WITH readable AS (
  SELECT member.project_id, member.book_id, member.chapter_id,
    project.name AS project_title, book_member.title AS book_title,
    member.title AS chapter_title,
    CASE WHEN member.source_view = 'draft' THEN chapter.revision ELSE chapter.published_revision END::integer AS revision,
    COALESCE(substring(member.title FROM '^Глава ([0-9]+)\.($| )'),
      row_number() OVER (PARTITION BY member.book_id ORDER BY member.chapter_order, member.chapter_id)::text)::text AS chapter_number
  FROM chapter_source_members AS member
  JOIN project_source_views AS marker ON marker.project_id = member.project_id
    AND marker.source_view = member.source_view
    AND marker.project_lifecycle_generation = member.project_lifecycle_generation
  JOIN projects AS project ON project.id = member.project_id
    AND project.lifecycle_generation = member.project_lifecycle_generation AND project.deleted_at IS NULL
  JOIN project_memberships AS membership ON membership.project_id = project.id
    AND membership.user_id = sqlc.arg(actor_user_id) AND membership.removed_at IS NULL
  JOIN book_source_members AS book_member ON book_member.project_id = member.project_id
    AND book_member.source_view = member.source_view
    AND book_member.project_lifecycle_generation = member.project_lifecycle_generation
    AND book_member.book_id = member.book_id AND book_member.book_lifecycle_generation = member.book_lifecycle_generation
  JOIN books AS book ON book.id = member.book_id AND book.project_id = member.project_id
    AND book.lifecycle_generation = member.book_lifecycle_generation AND book.deleted_at IS NULL
  JOIN chapters AS chapter ON chapter.id = member.chapter_id AND chapter.project_id = member.project_id
    AND chapter.book_id = member.book_id AND chapter.lifecycle_generation = member.chapter_lifecycle_generation
    AND chapter.deleted_at IS NULL
  WHERE member.source_view = sqlc.arg(source_view)::text
    AND (member.source_view = 'published' OR membership.role IN ('owner', 'admin', 'editor'))
    AND (member.source_view = 'draft' OR chapter.published_revision IS NOT NULL)
)
SELECT project_id, book_id, chapter_id, project_title, book_title, chapter_title, revision, chapter_number
FROM readable
WHERE revision > 0
  AND CASE WHEN sqlc.arg(chapter_number)::text <> ''
    THEN COALESCE(NULLIF(ltrim(readable.chapter_number, '0'), ''), '0') = sqlc.arg(chapter_number)::text
    ELSE strpos(lower(chapter_title), lower(sqlc.arg(query)::text)) > 0 END
  AND (NOT sqlc.arg(has_cursor)::boolean OR (project_id, book_id, chapter_id) >
    (sqlc.narg(after_project_id)::uuid, sqlc.narg(after_book_id)::uuid, sqlc.narg(after_chapter_id)::uuid))
ORDER BY project_id, book_id, chapter_id
LIMIT sqlc.arg(result_limit);
