-- Saga search rows (specs/2026-10-28-saga-search-in-postgres.md).

-- name: ChapterSearchTextExists :one
SELECT EXISTS (
  SELECT 1 FROM chapter_search_texts AS text
  WHERE text.project_id = sqlc.arg(project_id)
    AND text.chapter_id = sqlc.arg(chapter_id)
    AND text.content_hash = sqlc.arg(content_hash)::bytea
)::boolean AS present;

-- name: InsertChapterSearchText :many
-- A text already stored keeps its rows: the same content hash means the same words.
INSERT INTO chapter_search_texts (project_id, chapter_id, content_hash, paragraph_orders, paragraph_tokens, text_runes)
SELECT chapter.project_id, chapter.id, sqlc.arg(content_hash)::bytea, sqlc.arg(paragraph_orders)::integer[],
  sqlc.arg(paragraph_tokens)::integer[], sqlc.arg(text_runes)::integer
FROM chapters AS chapter
WHERE chapter.project_id = sqlc.arg(project_id) AND chapter.id = sqlc.arg(chapter_id)
ON CONFLICT (project_id, chapter_id, content_hash) DO NOTHING
RETURNING id;

-- name: InsertChapterSearchWords :copyfrom
INSERT INTO chapter_search_words (text_id, project_id, word, stem, postings)
VALUES (sqlc.arg(text_id), sqlc.arg(project_id), sqlc.arg(word), sqlc.arg(stem), sqlc.arg(postings));

-- name: DeleteStaleChapterSearchTexts :execrows
-- Search rows of a chapter text that no text index state of the chapter names any more.
DELETE FROM chapter_search_texts AS text
WHERE text.project_id = sqlc.arg(project_id)
  AND text.chapter_id = sqlc.arg(chapter_id)
  AND NOT EXISTS (
    SELECT 1 FROM chapter_text_index_state AS state
    WHERE state.project_id = text.project_id
      AND state.chapter_id = text.chapter_id
      AND state.chapter_content_hash = text.content_hash
  );

-- name: ListChapterSearchTexts :many
SELECT text.id, text.chapter_id, text.content_hash, text.paragraph_orders, text.paragraph_tokens, text.text_runes
FROM chapter_search_texts AS text
WHERE text.project_id = sqlc.arg(project_id)
  AND text.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[]);

-- name: ListSagaSearchVocabulary :many
-- The looked-up words of the texts and every word of the texts starting with a prefix.
SELECT DISTINCT word.word::text AS word, word.stem::text AS stem
FROM chapter_search_words AS word
WHERE word.project_id = sqlc.arg(project_id)
  AND word.word = ANY(sqlc.arg(words)::text[])
  AND word.text_id = ANY(sqlc.arg(text_ids)::uuid[])
UNION
SELECT DISTINCT prefixed.word::text AS word, prefixed.stem::text AS stem
FROM unnest(sqlc.arg(prefixes)::text[]) AS prefix(value)
JOIN LATERAL (
  SELECT word.word, word.stem
  FROM chapter_search_words AS word
  WHERE word.project_id = sqlc.arg(project_id)
    AND word.word >= prefix.value COLLATE "C"
    AND word.word < (prefix.value || chr(1114111)) COLLATE "C"
    AND word.text_id = ANY(sqlc.arg(text_ids)::uuid[])
) AS prefixed ON true;

-- name: ListSagaSearchStemWords :many
SELECT word.text_id, word.word::text AS word, word.stem::text AS stem, word.postings
FROM chapter_search_words AS word
WHERE word.project_id = sqlc.arg(project_id)
  AND word.stem = ANY(sqlc.arg(stems)::text[])
  AND word.text_id = ANY(sqlc.arg(text_ids)::uuid[]);

-- name: ListSagaSearchRareStemWords :many
-- The word rows of the stems whose postings in these texts fit in max_bytes. octet_length reads the
-- stored size of a value and not the value, so the rows of a frequent stem are not read at all.
SELECT word.text_id, word.word::text AS word, word.stem::text AS stem, word.postings
FROM chapter_search_words AS word
WHERE word.project_id = sqlc.arg(project_id)
  AND word.text_id = ANY(sqlc.arg(text_ids)::uuid[])
  AND word.stem = ANY(ARRAY(
    SELECT sized.stem FROM chapter_search_words AS sized
    WHERE sized.project_id = sqlc.arg(project_id)
      AND sized.stem = ANY(sqlc.arg(stems)::text[])
      AND sized.text_id = ANY(sqlc.arg(text_ids)::uuid[])
    GROUP BY sized.stem
    HAVING sum(octet_length(sized.postings)) <= sqlc.arg(max_bytes)::bigint
  ));

-- name: ListSagaSearchWordsContaining :many
SELECT word.text_id, word.word::text AS word, word.stem::text AS stem, word.postings
FROM chapter_search_words AS word
WHERE word.project_id = sqlc.arg(project_id)
  AND word.text_id = ANY(sqlc.arg(text_ids)::uuid[])
  AND word.word = ANY(ARRAY(
    SELECT DISTINCT candidate.word FROM chapter_search_words AS candidate
    WHERE candidate.project_id = sqlc.arg(project_id)
      AND candidate.word LIKE '%' || sqlc.arg(part)::text || '%'
  ));

-- name: ListSagaParagraphsAt :many
SELECT paragraph.chapter_id, paragraph."order", paragraph.plain_text
FROM generate_subscripts(sqlc.arg(chapter_ids)::uuid[], 1) AS wanted(at)
JOIN chapter_paragraphs AS paragraph
  ON paragraph.chapter_id = (sqlc.arg(chapter_ids)::uuid[])[wanted.at]
 AND paragraph.revision = (sqlc.arg(revisions)::bigint[])[wanted.at]
 AND paragraph."order" = (sqlc.arg(orders)::integer[])[wanted.at]
 AND paragraph.variant = sqlc.arg(source_view)
JOIN chapters AS chapter
  ON chapter.id = paragraph.chapter_id
 AND chapter.project_id = sqlc.arg(project_id)
 AND chapter.deleted_at IS NULL;

-- Operator commands of the saga search rows (UC4): coverage, snapshot and load.

-- name: ListSagaSearchCoverage :many
-- Every current chapter text of a live chapter and whether its search rows are stored.
SELECT state.project_id, state.source_view, count(*)::bigint AS texts,
  count(*) FILTER (WHERE EXISTS (
    SELECT 1 FROM chapter_search_texts AS text
    WHERE text.project_id = state.project_id AND text.chapter_id = state.chapter_id
      AND text.content_hash = state.chapter_content_hash
  ))::bigint AS covered
FROM chapter_text_index_state AS state
JOIN projects AS project ON project.id = state.project_id AND project.deleted_at IS NULL
JOIN chapters AS chapter ON chapter.id = state.chapter_id AND chapter.project_id = state.project_id AND chapter.deleted_at IS NULL
GROUP BY state.project_id, state.source_view
ORDER BY state.project_id, state.source_view;

-- name: ListSagaSearchMissingTexts :many
-- The current chapter texts without search rows, one per text (its first view names where its
-- paragraphs are read).
SELECT DISTINCT ON (state.project_id, state.chapter_id, state.chapter_content_hash)
  state.project_id, state.chapter_id, state.chapter_content_hash, state.source_view, state.source_revision
FROM chapter_text_index_state AS state
JOIN projects AS project ON project.id = state.project_id AND project.deleted_at IS NULL
JOIN chapters AS chapter ON chapter.id = state.chapter_id AND chapter.project_id = state.project_id AND chapter.deleted_at IS NULL
WHERE (sqlc.narg(project_id)::uuid IS NULL OR state.project_id = sqlc.narg(project_id)::uuid)
  AND NOT EXISTS (
    SELECT 1 FROM chapter_search_texts AS text
    WHERE text.project_id = state.project_id AND text.chapter_id = state.chapter_id
      AND text.content_hash = state.chapter_content_hash
  )
ORDER BY state.project_id, state.chapter_id, state.chapter_content_hash, state.source_view;

-- name: ListSagaSearchSnapshotParagraphs :many
SELECT paragraph.id, paragraph."order", paragraph.plain_text
FROM chapter_paragraphs AS paragraph
WHERE paragraph.chapter_id = sqlc.arg(chapter_id)
  AND paragraph.revision = sqlc.arg(revision)
  AND paragraph.variant = sqlc.arg(source_view)
ORDER BY paragraph."order", paragraph.id;

-- name: LockSagaSearchProject :one
SELECT project.id FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
FOR SHARE;

-- name: CountSagaSearchCurrentTexts :one
-- How many of the given chapter texts are current: a live chapter has a text index state with
-- that content hash.
SELECT count(*)::bigint
FROM unnest(sqlc.arg(chapter_ids)::uuid[]) WITH ORDINALITY AS given_chapter(id, at)
JOIN unnest(sqlc.arg(content_hashes)::bytea[]) WITH ORDINALITY AS given_hash(value, at) ON given_hash.at = given_chapter.at
WHERE EXISTS (
  SELECT 1 FROM chapter_text_index_state AS state
  JOIN chapters AS chapter ON chapter.id = state.chapter_id AND chapter.project_id = state.project_id AND chapter.deleted_at IS NULL
  WHERE state.project_id = sqlc.arg(project_id)
    AND state.chapter_id = given_chapter.id
    AND state.chapter_content_hash = given_hash.value
);

-- name: CountSagaSearchWrongStems :one
-- Word rows whose stem is not the russian_stem lexeme of this database.
SELECT count(*)::bigint
FROM unnest(sqlc.arg(words)::text[]) WITH ORDINALITY AS word(value, at)
JOIN unnest(sqlc.arg(stems)::text[]) WITH ORDINALITY AS stem(value, at) ON stem.at = word.at
WHERE stem.value IS DISTINCT FROM coalesce(array_to_string(ts_lexize('russian_stem', word.value), ' '), word.value);

-- name: ListSagaSearchTextIDs :many
SELECT text.id FROM chapter_search_texts AS text ORDER BY text.id;

-- name: CountStoredSagaSearchWrongStems :one
-- Word rows of the given texts whose stem is not the russian_stem lexeme of this database.
SELECT count(*)::bigint
FROM chapter_search_words AS word
WHERE word.text_id = ANY(sqlc.arg(text_ids)::uuid[])
  AND word.stem <> coalesce(array_to_string(ts_lexize('russian_stem', word.word), ' '), word.word);

-- name: CountSagaSearchActiveTextJobs :one
-- Imports and text indexes of the project that are queued or running: they write chapter texts.
SELECT count(*)::bigint
FROM jobs AS job
WHERE job.project_id = sqlc.arg(project_id)
  AND job.kind IN ('import', 'canon_text_index')
  AND job.status IN ('queued', 'running', 'canceling');

-- name: DeleteStaleProjectSearchTexts :execrows
-- Search rows of the project's chapter texts that no text index state names any more.
DELETE FROM chapter_search_texts AS text
WHERE text.project_id = sqlc.arg(project_id)
  AND NOT EXISTS (
    SELECT 1 FROM chapter_text_index_state AS state
    WHERE state.project_id = text.project_id
      AND state.chapter_id = text.chapter_id
      AND state.chapter_content_hash = text.content_hash
  );

-- name: LockSagaSearchChapter :exec
-- The writers of the search rows of one chapter go one at a time (text index transactions of
-- every version), and a load of the project waits for them all (LockSagaSearchProject).
SELECT pg_advisory_xact_lock_shared(hashtextextended('saga-search-project:' || sqlc.arg(project_id)::uuid::text, 0)),
  pg_advisory_xact_lock(hashtextextended('saga-search-chapter:' || sqlc.arg(chapter_id)::uuid::text, 0));

-- name: LockSagaSearchProjectRows :exec
SELECT pg_advisory_xact_lock(hashtextextended('saga-search-project:' || sqlc.arg(project_id)::uuid::text, 0));
