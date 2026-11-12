-- name: ListSagaMemoryBooks :many
SELECT book.id, book."order", book.key_number, book.title
FROM books AS book
WHERE book.project_id = sqlc.arg(project_id)
  AND book.deleted_at IS NULL;

-- name: ListSagaMemoryParagraphs :many
SELECT paragraph.chapter_id, paragraph."order", paragraph.plain_text
FROM chapter_paragraphs AS paragraph
JOIN chapters AS chapter
  ON chapter.id = paragraph.chapter_id
 AND chapter.project_id = sqlc.arg(project_id)
 AND chapter.deleted_at IS NULL
WHERE paragraph.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[])
  AND paragraph.variant = sqlc.arg(source_view)
  AND (paragraph.chapter_id::text || ':' || paragraph.revision::text) = ANY(sqlc.arg(chapter_revisions)::text[])
ORDER BY paragraph.chapter_id, paragraph."order";

-- name: LexizeSagaMemoryWords :many
SELECT word::text AS word,
  coalesce(array_to_string(ts_lexize('russian_stem', word), ' '), word)::text AS stem
FROM unnest(sqlc.arg(words)::text[]) AS word;

-- name: ListChapterCards :many
-- The parts a reader does not need are left out of the card JSON.
SELECT card.chapter_id, card.content_hash, (card.card_json - sqlc.arg(omitted)::text[])::jsonb AS card_json
FROM chapter_cards AS card
WHERE card.project_id = sqlc.arg(project_id)
  AND card.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[]);

-- name: GetChapterCardsStamp :one
-- The stamp of the cards of chapter texts, of the name registry and of the author's materials (specs/2026-11-04-saga-map.md,
-- R1.5, R9.5): a new, replaced or removed card, a new registry or a changed record changes it.
SELECT count(card.chapter_id)::bigint AS cards,
  coalesce(max(card.created_at), 'epoch'::timestamptz)::timestamptz AS written,
  coalesce((SELECT registry.digest FROM saga_names AS registry WHERE registry.project_id = sqlc.arg(project_id)), '')::text AS names_digest,
  (SELECT count(*)::text || ':' || coalesce(max(record.updated_at)::text, '') FROM author_materials AS record WHERE record.project_id = sqlc.arg(project_id))::text AS materials
FROM chapter_cards AS card
WHERE card.project_id = sqlc.arg(project_id)
  AND card.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[])
  AND (card.chapter_id::text || ':' || encode(card.content_hash, 'hex')) = ANY(sqlc.arg(chapter_texts)::text[]);

-- name: ListChapterCardKeys :many
-- A text with a card of any instructions counts: a newer version of the instructions never rebuilds
-- a saga by itself, the operator chooses which sagas to rebuild (specs/2026-11-01-index-foundation.md,
-- R3.1). Readers parse the cards of every version, each a superset of the one before.
SELECT card.chapter_id, card.content_hash
FROM chapter_cards AS card
WHERE card.project_id = sqlc.arg(project_id)
  AND card.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[]);

-- name: RecordChapterCardsProgress :execrows
UPDATE jobs AS job
SET progress_json = jsonb_build_object('value', sqlc.arg(progress)::double precision),
    updated_at = clock_timestamp()
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.kind = 'chapter_cards'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL;

-- name: InsertChapterCard :execrows
-- A card of newer instructions replaces the older card of the same text, also one carried over a change of
-- signs: the model read this text (specs/2026-10-27-update-book-from-file.md, R3.2).
INSERT INTO chapter_cards (project_id, chapter_id, content_hash, card_json, model, version)
SELECT chapter.project_id, chapter.id, sqlc.arg(content_hash)::bytea, sqlc.arg(card_json)::jsonb,
  sqlc.arg(model)::text, sqlc.arg(version)::integer
FROM chapters AS chapter
WHERE chapter.project_id = sqlc.arg(project_id)
  AND chapter.id = sqlc.arg(chapter_id)
ON CONFLICT (project_id, chapter_id, content_hash) DO UPDATE
SET card_json = EXCLUDED.card_json, model = EXCLUDED.model, version = EXCLUDED.version,
    built_from = NULL, created_at = clock_timestamp()
WHERE chapter_cards.version < EXCLUDED.version;

-- name: DeleteStaleChapterCards :execrows
DELETE FROM chapter_cards AS card
WHERE card.project_id = sqlc.arg(project_id)
  AND NOT EXISTS (
    SELECT 1
    FROM chapter_text_index_state AS state
    WHERE state.project_id = card.project_id
      AND state.chapter_id = card.chapter_id
      AND state.chapter_content_hash = card.content_hash
  );

-- name: ListChapterCardsCoverage :many
-- The operator's chapter-cards status (specs/2026-10-29-chapter-cards-quality.md, UC1): every
-- current chapter text of a live chapter, once per text, and the card it has; model is empty
-- for a text without a card. Like the versions the cards Job builds (IndexedVersions), only
-- projects an active owner or member can open. The owner names the saga in the admin service status
-- (specs/2026-11-08-admin-redesign.md AR6.2).
SELECT text.project_id, text.owner_user_id,
  count(*)::bigint AS texts,
  count(card.chapter_id) FILTER (WHERE card.version = sqlc.arg(version)::integer)::bigint AS current_cards,
  count(card.chapter_id) FILTER (WHERE card.version <> sqlc.arg(version)::integer)::bigint AS other_cards,
  coalesce(string_agg(DISTINCT card.model || ' v' || card.version::text, ', '), '')::text AS models
FROM (
  SELECT DISTINCT state.project_id, project.owner_user_id, state.chapter_id, state.chapter_content_hash
  FROM chapter_text_index_state AS state
  JOIN projects AS project ON project.id = state.project_id AND project.deleted_at IS NULL
  JOIN chapters AS chapter ON chapter.id = state.chapter_id AND chapter.project_id = state.project_id AND chapter.deleted_at IS NULL
  WHERE EXISTS (
      SELECT 1 FROM users AS owner
      WHERE owner.id = project.owner_user_id AND owner.disabled_at IS NULL
    ) OR EXISTS (
      SELECT 1
      FROM project_memberships AS member
      JOIN users AS person ON person.id = member.user_id AND person.disabled_at IS NULL
      WHERE member.project_id = project.id AND member.removed_at IS NULL
    )
) AS text
LEFT JOIN chapter_cards AS card
  ON card.project_id = text.project_id AND card.chapter_id = text.chapter_id AND card.content_hash = text.chapter_content_hash
GROUP BY text.project_id, text.owner_user_id
ORDER BY text.project_id;

-- name: ListCurrentChapterCards :many
-- The cards of current chapter texts of live projects (of one project when given), for the
-- operator's chapter-cards export.
SELECT card.project_id, card.chapter_id, card.content_hash, card.version, card.model, card.card_json
FROM chapter_cards AS card
JOIN projects AS project ON project.id = card.project_id AND project.deleted_at IS NULL
WHERE (sqlc.narg(project_id)::uuid IS NULL OR card.project_id = sqlc.narg(project_id)::uuid)
  AND EXISTS (
    SELECT 1 FROM chapter_text_index_state AS state
    JOIN chapters AS chapter ON chapter.id = state.chapter_id AND chapter.project_id = state.project_id AND chapter.deleted_at IS NULL
    WHERE state.project_id = card.project_id AND state.chapter_id = card.chapter_id
      AND state.chapter_content_hash = card.content_hash
  )
ORDER BY card.project_id, card.chapter_id;

-- name: LockChapterCardsJobs :many
-- The operator's load holds the project's cards Jobs until it commits: the claim skips locked Job
-- rows, so a queued Job is not claimed meanwhile; a running one refuses the load.
SELECT job.status
FROM jobs AS job
WHERE job.project_id = sqlc.arg(project_id)
  AND job.kind = 'chapter_cards'
  AND job.status IN ('queued', 'running', 'canceling')
FOR UPDATE;

-- name: ReplaceChapterCard :one
-- The operator's load writes a card built elsewhere over the card of the same text, whatever
-- model wrote it; inserted is false when a card was replaced.
INSERT INTO chapter_cards (project_id, chapter_id, content_hash, card_json, model, version)
VALUES (sqlc.arg(project_id), sqlc.arg(chapter_id), sqlc.arg(content_hash)::bytea, sqlc.arg(card_json)::jsonb,
  sqlc.arg(model)::text, sqlc.arg(version)::integer)
ON CONFLICT (project_id, chapter_id, content_hash) DO UPDATE
SET card_json = EXCLUDED.card_json, model = EXCLUDED.model, version = EXCLUDED.version,
    built_from = NULL, created_at = clock_timestamp()
RETURNING (xmax = 0) AS inserted;

-- name: ListChapterCardFacts :many
-- The facts of the cards whose subject contains one of the patterns (lower case, ё as е, without
-- stress marks, a no-break space as a space and runs of spaces as one, as glossary keys are),
-- read inside PostgreSQL so a call gets only the facts of one glossary entry
-- (specs/2026-10-29-chapter-cards-quality.md, UC4); the caller keeps the exact names. What a v4 card
-- says a character knows comes as a fact whose attribute is the kind of knowledge, after the facts
-- of its card (specs/2026-11-01-index-foundation.md, R4.1).
SELECT card.chapter_id, card.content_hash, fact.ordinal::integer AS ordinal, fact.value::jsonb AS fact
FROM chapter_cards AS card
CROSS JOIN LATERAL (
  SELECT stated.value, stated.ordinal
  FROM jsonb_array_elements(
    CASE WHEN jsonb_typeof(card.card_json->'facts') = 'array' THEN card.card_json->'facts' ELSE '[]'::jsonb END
  ) WITH ORDINALITY AS stated(value, ordinal)
  UNION ALL
  SELECT jsonb_build_object(
    'subject', known.value->>'who',
    'attribute', coalesce(nullif(known.value->>'kind', ''), 'знание'),
    'value', known.value->>'what',
    'p', known.value->'p'
  ), 1000000 + known.ordinal
  FROM jsonb_array_elements(
    CASE WHEN jsonb_typeof(card.card_json->'knows') = 'array' THEN card.card_json->'knows' ELSE '[]'::jsonb END
  ) WITH ORDINALITY AS known(value, ordinal)
) AS fact(value, ordinal)
WHERE card.project_id = sqlc.arg(project_id)
  AND card.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[])
  AND regexp_replace(
    translate(lower(fact.value->>'subject'), 'ё' || U&'\00A0' || U&'\0301' || U&'\0300', 'е '), '\s+', ' ', 'g'
  ) LIKE ANY(sqlc.arg(patterns)::text[])
ORDER BY card.chapter_id, fact.ordinal;

-- name: GetSagaNames :one
SELECT registry.names_json FROM saga_names AS registry WHERE registry.project_id = sqlc.arg(project_id);

-- name: GetSagaNamesDigest :one
SELECT registry.digest FROM saga_names AS registry WHERE registry.project_id = sqlc.arg(project_id);

-- name: UpsertSagaNames :exec
INSERT INTO saga_names (project_id, digest, names_json, model)
VALUES (sqlc.arg(project_id), sqlc.arg(digest)::text, sqlc.arg(names_json)::jsonb, sqlc.arg(model)::text)
ON CONFLICT (project_id) DO UPDATE
SET digest = EXCLUDED.digest, names_json = EXCLUDED.names_json, model = EXCLUDED.model,
    updated_at = clock_timestamp();

-- name: ListChapterCardStamps :many
-- What the cards of the chapters were built from, the thread layer counts new cards by it: a card carried over
-- a change of signs names the text the model read (specs/2026-10-27-update-book-from-file.md, R3.3).
SELECT card.chapter_id, card.content_hash, card.version, coalesce(card.built_from, card.content_hash)::bytea AS built_from
FROM chapter_cards AS card
WHERE card.project_id = sqlc.arg(project_id)
  AND card.chapter_id = ANY(sqlc.arg(chapter_ids)::uuid[]);

-- name: GetSagaThreads :one
-- The finished build without the books, enough for the registry.
SELECT layer.chapters_json, layer.threads_json
FROM saga_threads AS layer
WHERE layer.project_id = sqlc.arg(project_id) AND layer.digest IS NOT NULL;

-- name: GetSagaThreadsWithBooks :one
SELECT layer.chapters_json, layer.threads_json, layer.books_json
FROM saga_threads AS layer
WHERE layer.project_id = sqlc.arg(project_id) AND layer.digest IS NOT NULL;

-- name: GetSagaThreadChapters :one
SELECT layer.chapters_json
FROM saga_threads AS layer
WHERE layer.project_id = sqlc.arg(project_id) AND layer.digest IS NOT NULL;

-- name: GetSagaThreadBuild :one
-- Everything a build goes on from: the finished build and the checkpoint.
SELECT layer.digest, layer.chapters_json, layer.books_json, layer.threads_json,
  layer.checkpoint_books_json, layer.checkpoint_json
FROM saga_threads AS layer
WHERE layer.project_id = sqlc.arg(project_id);

-- name: RecordSagaLayerProgress :execrows
UPDATE jobs AS job
SET progress_json = jsonb_build_object('value', sqlc.arg(progress)::double precision),
    updated_at = clock_timestamp()
WHERE job.project_id = sqlc.arg(project_id)
  AND job.id = sqlc.arg(job_id)
  AND job.kind = 'saga_layer'
  AND job.status = 'running'
  AND job.claim_incarnation = sqlc.arg(claim_incarnation)
  AND job.cancel_requested_at IS NULL;

-- name: SaveSagaThreadsCheckpoint :exec
-- A checkpoint leaves the finished build alone; without new books it leaves the books of the checkpoint.
INSERT INTO saga_threads (project_id, model, checkpoint_books_json, checkpoint_json)
VALUES (
  sqlc.arg(project_id), sqlc.arg(model)::text,
  COALESCE(sqlc.narg(checkpoint_books_json)::jsonb, '[]'::jsonb), sqlc.arg(checkpoint_json)::jsonb
)
ON CONFLICT (project_id) DO UPDATE
SET checkpoint_books_json = COALESCE(sqlc.narg(checkpoint_books_json)::jsonb, saga_threads.checkpoint_books_json),
    checkpoint_json = EXCLUDED.checkpoint_json,
    updated_at = clock_timestamp();

-- name: FinishSagaThreads :exec
-- The finished build replaces the old one in one step and ends the checkpoint.
INSERT INTO saga_threads (project_id, digest, chapters_json, books_json, threads_json, model)
VALUES (
  sqlc.arg(project_id), sqlc.arg(digest)::text, sqlc.arg(chapters_json)::jsonb, sqlc.arg(books_json)::jsonb,
  sqlc.arg(threads_json)::jsonb, sqlc.arg(model)::text
)
ON CONFLICT (project_id) DO UPDATE
SET digest = EXCLUDED.digest, chapters_json = EXCLUDED.chapters_json, books_json = EXCLUDED.books_json,
    threads_json = EXCLUDED.threads_json, model = EXCLUDED.model,
    checkpoint_books_json = '[]'::jsonb, checkpoint_json = NULL,
    updated_at = clock_timestamp();

-- name: GetSagaMapGraph :one
-- The prepared graph; a browser holding the revision needs no document.
SELECT graph.revision,
       CASE WHEN graph.revision = ANY(sqlc.arg(known)::text[]) THEN NULL ELSE graph.document END AS document
FROM saga_map_graphs AS graph
WHERE graph.project_id = sqlc.arg(project_id) AND graph.source_view = sqlc.arg(source_view);

-- name: GetSagaMapGraphRevision :one
SELECT graph.revision FROM saga_map_graphs AS graph
WHERE graph.project_id = sqlc.arg(project_id) AND graph.source_view = sqlc.arg(source_view);

-- name: GetSagaMapCard :one
-- The prepared card of a name for the whole saga.
SELECT node.card
FROM saga_map_names AS name
JOIN saga_map_nodes AS node
  ON node.project_id = name.project_id AND node.source_view = name.source_view AND node.node_key = name.node_key
WHERE name.project_id = sqlc.arg(project_id) AND name.source_view = sqlc.arg(source_view) AND name.name_key = sqlc.arg(name_key);

-- name: GetSagaMapChapters :one
SELECT graph.chapters FROM saga_map_graphs AS graph
WHERE graph.project_id = sqlc.arg(project_id) AND graph.source_view = sqlc.arg(source_view);

-- name: GetSagaMapNodePart :one
-- The part of the node of a name that holds a chapter.
SELECT part.body
FROM saga_map_names AS name
JOIN LATERAL (
  SELECT stored.body FROM saga_map_node_parts AS stored
  WHERE stored.project_id = name.project_id AND stored.source_view = name.source_view AND stored.node_key = name.node_key
    AND stored.first_chapter <= sqlc.arg(chapter)
  ORDER BY stored.first_chapter DESC
  LIMIT 1
) AS part ON true
WHERE name.project_id = sqlc.arg(project_id) AND name.source_view = sqlc.arg(source_view) AND name.name_key = sqlc.arg(name_key);

-- name: DeleteSagaMapGraph :exec
DELETE FROM saga_map_graphs
WHERE project_id = sqlc.arg(project_id) AND source_view = sqlc.arg(source_view);

-- name: InsertSagaMapGraph :exec
INSERT INTO saga_map_graphs (project_id, source_view, revision, document, chapters)
VALUES (sqlc.arg(project_id), sqlc.arg(source_view), sqlc.arg(revision), sqlc.arg(document), sqlc.arg(chapters));

-- name: ChapterCardsClaimHeld :one
-- Whether a cards Job still runs under its claim.
SELECT EXISTS (
  SELECT 1 FROM jobs AS job
  WHERE job.project_id = sqlc.arg(project_id) AND job.id = sqlc.arg(job_id) AND job.kind = 'chapter_cards'
    AND job.status = 'running' AND job.claim_incarnation = sqlc.arg(claim_incarnation) AND job.cancel_requested_at IS NULL
);

-- name: ChapterCardsJobActive :one
-- Whether a cards Job of the version is waiting, at work or between its portions.
SELECT EXISTS (
  SELECT 1 FROM jobs AS job
  WHERE job.project_id = sqlc.arg(project_id) AND job.kind = 'chapter_cards' AND job.status IN ('queued', 'running')
    AND job.cancel_requested_at IS NULL AND job.subject_json->>'sourceView' = sqlc.arg(source_view)::text
);

-- name: SearchSagaMapNodes :many
-- The entries whose names, roles or note hold the query, most mentioned first, as the glossary finds them.
SELECT node.node_key, node.name, node.type
FROM saga_map_nodes AS node
WHERE node.project_id = sqlc.arg(project_id) AND node.source_view = sqlc.arg(source_view)
  AND strpos(node.search_text, sqlc.arg(query)::text) > 0
ORDER BY node.chapter_count DESC, node.name COLLATE "C"
LIMIT sqlc.arg(row_limit);

-- name: ListSagaMapNamesOfNodes :many
-- What the prepared map calls the given names, for telling which of them the book knows.
SELECT name.name_key, node.name, node.type
FROM saga_map_names AS name
JOIN saga_map_nodes AS node
  ON node.project_id = name.project_id AND node.source_view = name.source_view AND node.node_key = name.node_key
WHERE name.project_id = sqlc.arg(project_id) AND name.source_view = sqlc.arg(source_view) AND name.name_key = ANY(sqlc.arg(name_keys)::text[]);

-- name: ListSagaMapHidden :many
SELECT id, node_key, other_key, node_name, other_name, created_at
FROM saga_map_hidden
WHERE project_id = sqlc.arg(project_id)
ORDER BY created_at DESC, id;

-- name: HideSagaMapItem :one
INSERT INTO saga_map_hidden (project_id, node_key, other_key, node_name, other_name)
VALUES (sqlc.arg(project_id), sqlc.arg(node_key), sqlc.arg(other_key), sqlc.arg(node_name), sqlc.arg(other_name))
ON CONFLICT (project_id, node_key, other_key) DO UPDATE SET node_name = EXCLUDED.node_name
RETURNING id, node_key, other_key, node_name, other_name, created_at;

-- name: RestoreSagaMapItem :execrows
DELETE FROM saga_map_hidden
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: CardModelAllowed :one
-- Whether the cards Job may call the model for a saga (specs/2026-11-07-desktop-app.md, DA8.1): only a consent that is not paused.
SELECT EXISTS (
  SELECT 1 FROM card_build_permissions
  WHERE project_id = sqlc.arg(project_id) AND state = 'allowed'
)::boolean AS allowed;

-- name: UpsertCardBuildPermission :execrows
INSERT INTO card_build_permissions (project_id, state, budget_usd_micros, budget_tokens, since, updated_at)
SELECT project.id, sqlc.arg(state), sqlc.narg(budget_usd_micros), sqlc.narg(budget_tokens), sqlc.arg(since), sqlc.arg(updated_at)
FROM projects AS project
WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL
ON CONFLICT (project_id) DO UPDATE SET
  state = EXCLUDED.state,
  budget_usd_micros = EXCLUDED.budget_usd_micros,
  budget_tokens = EXCLUDED.budget_tokens,
  since = EXCLUDED.since,
  updated_at = EXCLUDED.updated_at;
