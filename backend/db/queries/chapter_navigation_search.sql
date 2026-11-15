-- name: SearchChapterNavigation :many
WITH readable AS (
  SELECT member.project_id, member.book_id, member.chapter_id,
    project.name AS project_title, book_member.title AS book_title,
    member.title AS chapter_title,
    CASE WHEN member.source_view = 'draft' THEN chapter.revision ELSE chapter.published_revision END::integer AS revision,
    row_number() OVER (PARTITION BY member.book_id ORDER BY member.chapter_order, member.chapter_id)::integer AS chapter_place
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
-- The number of a chapter is read from its title by the one rule of the domain, its place when the title writes none
-- (specs/2026-11-15-reader-chapter-number.md RN1.1): a search by a number gives all the candidates at once — the chapter
-- at that place and the titles that hold its digits, without a limit —, and the repository keeps those whose number it
-- is and cuts the page; a search by words gives its page.
SELECT project_id, book_id, chapter_id, project_title, book_title, chapter_title, revision, chapter_place
FROM readable
WHERE revision > 0
  AND CASE WHEN sqlc.arg(chapter_number)::text <> ''
    THEN readable.chapter_place::text = sqlc.arg(chapter_number)::text OR strpos(chapter_title, sqlc.arg(chapter_number)::text) > 0
    ELSE strpos(lower(chapter_title), lower(sqlc.arg(query)::text)) > 0 END
  AND (NOT sqlc.arg(has_cursor)::boolean OR (project_id, book_id, chapter_id) >
    (sqlc.narg(after_project_id)::uuid, sqlc.narg(after_book_id)::uuid, sqlc.narg(after_chapter_id)::uuid))
ORDER BY project_id, book_id, chapter_id
LIMIT sqlc.narg(result_limit)::integer;
