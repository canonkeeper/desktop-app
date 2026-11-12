-- name: ListChapterDrafts :many
-- The list reads no text of a draft: a project may hold many long ones. The words of its working text are counted here
-- by the rule of the head of the chapter (specs/2026-11-12-ux12-writing.md WR2.1; sagadraft.WordsOf, the shared table
-- testdata/words.json): runs of letters, marks and digits — «по-русски» is two words, a dash none, a mark of the helper
-- parts a word, a mark of the author's italic and bold is no sign (specs/2026-11-08-italic-bold-in-text.md TF1.4). The
-- classes are read by the Unicode collation of PostgreSQL, whatever the locale of the database; the marks and the
-- numbers it does not count as letters are named by their blocks.
-- A row names the step of its draft as the strip over the chapter does (specs/2026-11-11-ux5-writing-path.md WP1.2): the
-- idea, the latest plan and why it was made, the last Job and how far it came, the open findings and corrections.
SELECT draft.id, draft.title, draft.kind,
       regexp_count(translate(draft.body, '⟨⟩⟪⟫', '') COLLATE "pg_c_utf8",
                    '[[:alpha:][:digit:]\u0300-\u036f\u0483-\u0489\u1ab0-\u1aff\u1dc0-\u1dff\u20d0-\u20f0\ua66f-\ua672\ua67c-\ua67d\ufe00-\ufe0f\ufe20-\ufe2f\u00b2\u00b3\u00b9\u00bc-\u00be\u2070-\u2079\u2080-\u2089\u2150-\u2189\u2460-\u249b\u24ea-\u24ff\u2776-\u2793\uff10-\uff19]+')::integer AS words,
       draft.added_chapter_id, (draft.edits_chapter_id IS NOT NULL)::boolean AS edits, draft.revision, draft.created_at, draft.updated_at,
       draft.idea, draft.plan_number, draft.plan_approved,
       COALESCE((SELECT plan.idea_hash FROM chapter_draft_plans AS plan
                 WHERE plan.draft_id = draft.id AND plan.project_id = draft.project_id AND plan.number = draft.plan_number), '')::text AS plan_idea_hash,
       COALESCE((SELECT plan.reason FROM chapter_draft_plans AS plan
                 WHERE plan.draft_id = draft.id AND plan.project_id = draft.project_id AND plan.number = draft.plan_number), '')::text AS plan_reason,
       job.id AS job_id, job.status AS job_status, COALESCE(job.subject_json->>'mode', '')::text AS job_mode,
       COALESCE(job.result_json->>'phase', '')::text AS job_phase, COALESCE((job.result_json->>'total')::integer, 0)::integer AS job_total,
       COALESCE((job.result_json->>'done')::integer, 0)::integer AS job_done,
       COALESCE(job.error_json->>'code', '')::text AS job_error_code,
       (SELECT count(*) FROM chapter_draft_notes AS note
        WHERE note.project_id = draft.project_id AND note.draft_id = draft.id AND note.status = 'open'
          AND note.kind = ANY(sqlc.arg(finding_kinds)::text[]))::integer AS findings,
       (SELECT count(*) FROM chapter_draft_notes AS note
        WHERE note.project_id = draft.project_id AND note.draft_id = draft.id AND note.status = 'open' AND note.kind = 'proofread')::integer AS corrections,
       original.attachment_chat_id, original.attachment_number,
       -- The beginning of the text names a draft without a title or idea (specs/2026-11-11-ux9-writing-a.md WA1.1); an edit
       -- of a chapter of the book reads its whole text, to say whether the book holds it (WA1.3), with the chapter as the
       -- book has it now: its title, its revision and its paragraphs with their plain text.
       (CASE WHEN draft.edits_chapter_id IS NOT NULL THEN draft.body ELSE left(draft.body, 1200) END)::text AS body_head,
       draft.edits_revision, edited.id AS edited_chapter_id, edited.title AS edited_chapter_title, edited.published_revision AS edited_revision,
       COALESCE((SELECT jsonb_agg(jsonb_build_array(paragraph.markdown, paragraph.plain_text) ORDER BY paragraph."order")
                 FROM chapter_paragraphs AS paragraph
                 WHERE paragraph.chapter_id = edited.id AND paragraph.revision = edited.published_revision AND paragraph.variant = 'published'), '[]')::jsonb AS edited_paragraphs,
       (SELECT count(*) FROM chapter_drafts AS counted
        LEFT JOIN chapter_draft_originals AS counted_original
          ON counted_original.draft_id = counted.id AND counted_original.project_id = counted.project_id
        WHERE counted.project_id = draft.project_id
          AND (NOT sqlc.arg(has_chat)::boolean OR counted_original.attachment_chat_id = sqlc.arg(chat_id)::uuid)) AS total_count
FROM chapter_drafts AS draft
LEFT JOIN jobs AS job ON job.id = draft.job_id AND job.project_id = draft.project_id
LEFT JOIN chapters AS edited ON edited.id = draft.edits_chapter_id AND edited.project_id = draft.project_id AND edited.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM books AS edited_book WHERE edited_book.id = edited.book_id AND edited_book.deleted_at IS NULL)
-- The chapter attached in the chat a proofreading began from (specs/2026-11-11-ux5-proofread-mode.md PM3.3): the card of
-- the chat reads the drafts of its chat by it.
LEFT JOIN chapter_draft_originals AS original ON original.draft_id = draft.id AND original.project_id = draft.project_id
WHERE draft.project_id = sqlc.arg(project_id)
  AND (NOT sqlc.arg(has_chat)::boolean OR original.attachment_chat_id = sqlc.arg(chat_id)::uuid)
  AND (NOT sqlc.arg(has_cursor)::boolean
    OR (draft.created_at, draft.id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY draft.created_at DESC, draft.id DESC
LIMIT sqlc.arg(limit_count);

-- name: CreateChapterDraft :one
INSERT INTO chapter_drafts (project_id, title, book_id, after_chapter_id, idea, kind, body, created_by, updated_by)
VALUES (sqlc.arg(project_id), sqlc.arg(title), sqlc.narg(book_id), sqlc.narg(after_chapter_id), sqlc.arg(idea), sqlc.arg(kind), sqlc.arg(body), sqlc.arg(actor), sqlc.arg(actor))
RETURNING id;

-- name: GetChapterDraft :one
-- What the draft points at is shown only while it lives: a deleted book hides the place and leaves the draft.
SELECT draft.id, draft.project_id, draft.title, draft.idea, draft.kind, draft.body,
       draft.revision, draft.created_at, draft.updated_at, draft.chat_id,
       draft.book_id AS stored_book_id, book.id AS book_id, book.title AS book_title,
       after_chapter.id AS after_chapter_id, after_chapter.title AS after_chapter_title,
       draft.after_chapter_id AS stored_after_chapter_id,
       added_chapter.id AS added_chapter_id, added_chapter.title AS added_chapter_title,
       draft.added_chapter_id AS stored_added_chapter_id,
       job.id AS job_id, job.status AS job_status, COALESCE(job.subject_json->>'mode', '')::text AS job_mode,
       COALESCE(job.result_json->>'phase', '')::text AS job_phase, COALESCE((job.result_json->>'total')::integer, 0)::integer AS job_total,
       COALESCE((job.result_json->>'done')::integer, 0)::integer AS job_done,
       COALESCE(job.error_json->>'code', '')::text AS job_error_code,
       -- The scene a writing failed on and the lines of the author it lost, by their numbers (specs/2026-11-11-ux9-writing-a.md WA2.4).
       COALESCE(job.result_json->'missing', 'null')::jsonb AS job_missing,
       -- The scene the writing writes now, as its answer comes (specs/2026-11-12-ux10-scene-stream.md ST1.4): only while the
       -- Job runs — a Job stopping, waiting for its retry or ended shows none.
       (CASE WHEN job.status = 'running' THEN COALESCE(job.result_json->'writing', 'null') ELSE 'null' END)::jsonb AS job_writing,
       author.display_name AS author_name,
       draft.edits_chapter_id AS stored_edits_chapter_id, draft.edits_revision, draft.edits_previous_revision,
       edited.id AS edited_chapter_id, edited.title AS edited_chapter_title, edited.book_id AS edited_book_id,
       edited.published_revision AS edited_revision,
       COALESCE((SELECT sum(paragraph.word_count) FROM chapter_paragraphs AS paragraph
                 WHERE paragraph.chapter_id = edited.id AND paragraph.revision = edited.published_revision AND paragraph.variant = 'published'), 0)::integer AS edited_words,
       -- The paragraphs of the edited chapter as the book has them now, to say whether it holds the draft (WA4.1).
       COALESCE((SELECT jsonb_agg(jsonb_build_array(paragraph.markdown, paragraph.plain_text) ORDER BY paragraph."order")
                 FROM chapter_paragraphs AS paragraph
                 WHERE paragraph.chapter_id = edited.id AND paragraph.revision = edited.published_revision AND paragraph.variant = 'published'), '[]')::jsonb AS edited_paragraphs,
       draft.case_choice, draft.plan_number, draft.plan_approved, draft.pack_choices, draft.last_version_number,
       COALESCE((SELECT plan.idea_hash FROM chapter_draft_plans AS plan
                 WHERE plan.draft_id = draft.id AND plan.project_id = draft.project_id AND plan.number = draft.plan_number), '')::text AS plan_idea_hash,
       COALESCE((SELECT plan.reason FROM chapter_draft_plans AS plan
                 WHERE plan.draft_id = draft.id AND plan.project_id = draft.project_id AND plan.number = draft.plan_number), '')::text AS plan_reason
FROM chapter_drafts AS draft
JOIN users AS author ON author.id = draft.created_by
LEFT JOIN chapters AS edited ON edited.id = draft.edits_chapter_id AND edited.project_id = draft.project_id AND edited.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM books AS edited_book WHERE edited_book.id = edited.book_id AND edited_book.deleted_at IS NULL)
LEFT JOIN books AS book ON book.id = draft.book_id AND book.project_id = draft.project_id AND book.deleted_at IS NULL
LEFT JOIN chapters AS after_chapter ON after_chapter.id = draft.after_chapter_id AND after_chapter.project_id = draft.project_id AND after_chapter.deleted_at IS NULL
  AND after_chapter.book_id = book.id
LEFT JOIN chapters AS added_chapter ON added_chapter.id = draft.added_chapter_id AND added_chapter.project_id = draft.project_id AND added_chapter.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM books AS added_book WHERE added_book.id = added_chapter.book_id AND added_book.deleted_at IS NULL)
LEFT JOIN jobs AS job ON job.id = draft.job_id AND job.project_id = draft.project_id
WHERE draft.project_id = sqlc.arg(project_id) AND draft.id = sqlc.arg(id);

-- name: LockChapterDraft :one
SELECT draft.revision, draft.kind, draft.title, draft.idea, draft.body,
       draft.book_id, draft.after_chapter_id, draft.added_chapter_id, draft.job_id, draft.chat_id,
       draft.edits_chapter_id, draft.edits_revision, draft.edits_previous_revision, draft.plan_number
FROM chapter_drafts AS draft
WHERE draft.project_id = sqlc.arg(project_id) AND draft.id = sqlc.arg(id)
FOR UPDATE;

-- name: ChapterDraftPlaceOfChat :one
-- The place of the draft whose helper's session a chat is: the helper reads the saga only before it
-- (specs/2026-11-06-writing-agent.md, WA3.3).
SELECT book_id, after_chapter_id FROM chapter_drafts WHERE project_id = sqlc.arg(project_id) AND chat_id = sqlc.arg(chat_id);

-- name: ChapterDraftJobActive :one
-- A Job that no longer exists is not active.
SELECT EXISTS (
  SELECT 1 FROM jobs
  WHERE id = sqlc.arg(job_id) AND project_id = sqlc.arg(project_id) AND status IN ('queued', 'running', 'canceling')
)::boolean;

-- name: UpdateChapterDraft :exec
-- The draft keeps one working text, body (specs/2026-11-06-writing-agent.md, WA10.4).
UPDATE chapter_drafts
SET title = sqlc.arg(title), book_id = sqlc.narg(book_id), after_chapter_id = sqlc.narg(after_chapter_id), idea = sqlc.arg(idea), kind = sqlc.arg(kind),
    body = sqlc.arg(body), revision = revision + 1, updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: MarkChapterDraftAdded :exec
UPDATE chapter_drafts
SET added_chapter_id = sqlc.arg(chapter_id), revision = revision + 1, updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: DeleteChapterDraft :exec
DELETE FROM chapter_drafts WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: EraseChapterDraftJobText :exec
-- A deleted draft leaves nothing of the author's text in the rows of its runs (specs/2026-11-01-writer-assistant-index.md
-- R7.3): the subject is sealed (jobs.ChapterDraftSubject.Erased), the migration of
-- specs/2026-11-06-writing-agent.md S8 seals the runs of the earlier writing the same way. What stays is named one by one,
-- so a text field added later stays out: who and where of the subject; of the result, what jobs.ChapterDraftResult.Sealed
-- keeps — the kind of work, how far it came, what it read, the pack, the versions it kept and what its check counted and
-- measured (numbers, specs/2026-11-11-writing-check-signal.md). A run that goes on is not
-- touched: a draft under it is not deleted. A draft id is compared as a uuid: the subject keeps it as the request wrote it.
UPDATE jobs
SET subject_json = jsonb_strip_nulls(jsonb_build_object(
      'type', subject_json->'type', 'projectId', subject_json->'projectId', 'draftId', subject_json->'draftId', 'mode', subject_json->'mode',
      'bookId', subject_json->'bookId', 'afterChapterId', subject_json->'afterChapterId', 'erased', true
    )),
    result_json = CASE WHEN result_json IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
      'type', result_json->'type', 'mode', result_json->'mode', 'total', result_json->'total', 'done', result_json->'done',
      'chapters', result_json->'chapters', 'pack', result_json->'pack', 'beforeVersion', result_json->'beforeVersion',
      'writtenVersion', result_json->'writtenVersion', 'check', result_json->'check', 'missing', result_json->'missing'
    )) END,
    updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND kind = 'chapter_draft' AND status NOT IN ('queued', 'running', 'canceling')
  AND (subject_json->>'draftId')::uuid = sqlc.arg(draft_id)::uuid;

-- name: ListChapterDraftNotes :many
-- A note keeps the chat turn of the helper that wrote it and reads as its native turn — the providerTurnId of the
-- feed, which shows the note under that turn's answer (specs/2026-11-02-writer-workshop.md W4.5).
SELECT id, number, kind, target, origin, section, quote, body, replacement, options, refs, answer, status, variant_group, anchor_before, anchor_after, blocks, known, effects, recommend, fallback, level, category, book_quote, scene, pack_id,
  COALESCE((SELECT turn.provider_turn_id FROM chat_turns AS turn WHERE turn.id = chapter_draft_notes.turn_id AND turn.project_id = chapter_draft_notes.project_id), '')::text AS turn_id,
  created_at, updated_at
FROM chapter_draft_notes
WHERE chapter_draft_notes.project_id = sqlc.arg(project_id) AND chapter_draft_notes.draft_id = sqlc.arg(draft_id)
ORDER BY number;

-- name: ChapterDraftTypicalWords :many
-- The words of the last chapters of the published text before the place of a draft (W2.4): after the chapter it
-- follows, before the book it starts; without a live place, the last chapters of the saga.
WITH place AS (
  SELECT book."order" AS book_order, after_chapter."order" AS chapter_order
  FROM books AS book
  LEFT JOIN chapters AS after_chapter
    ON after_chapter.id = sqlc.narg(after_chapter_id) AND after_chapter.book_id = book.id AND after_chapter.deleted_at IS NULL
  WHERE book.id = sqlc.narg(book_id) AND book.project_id = sqlc.arg(project_id) AND book.deleted_at IS NULL
), last_chapters AS (
  SELECT chapter.id, chapter.published_revision
  FROM chapters AS chapter
  JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id AND book.deleted_at IS NULL
  LEFT JOIN place ON true
  WHERE chapter.project_id = sqlc.arg(project_id) AND chapter.deleted_at IS NULL AND chapter.published_revision IS NOT NULL
    AND (place.book_order IS NULL OR book."order" < place.book_order
      OR (book."order" = place.book_order AND chapter."order" <= place.chapter_order))
  ORDER BY book."order" DESC, chapter."order" DESC
  LIMIT sqlc.arg(chapters)
)
SELECT (
  SELECT coalesce(sum(paragraph.word_count), 0)
  FROM chapter_paragraphs AS paragraph
  WHERE paragraph.chapter_id = last_chapters.id AND paragraph.revision = last_chapters.published_revision
    AND paragraph.variant = 'published'
)::integer AS words
FROM last_chapters;

-- name: ChapterDraftOfEditedChapter :one
-- The one draft that edits a chapter of the book (specs/2026-11-05-voice-full-access.md A8.7).
SELECT id FROM chapter_drafts WHERE project_id = sqlc.arg(project_id) AND edits_chapter_id = sqlc.arg(chapter_id);

-- name: ChapterDraftThatAddedChapter :one
-- The draft whose chapter of the book it is: «Править главу» opens it, not a second one
-- (specs/2026-11-11-ux5-writing-path.md WP4.3). The newest, should two have added the same chapter.
SELECT id, body FROM chapter_drafts
WHERE project_id = sqlc.arg(project_id) AND added_chapter_id = sqlc.arg(chapter_id)
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: ReopenAddedChapterDraft :exec
-- The draft that added a chapter becomes the edit of it: it leaves «В книге», keeps its text, versions and helper, and
-- stands on the revision of the book its text matches.
UPDATE chapter_drafts
SET added_chapter_id = NULL, edits_chapter_id = sqlc.arg(chapter_id), edits_revision = sqlc.arg(edits_revision), edits_previous_revision = NULL,
    title = sqlc.arg(title), revision = revision + 1, updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: CreateChapterEditDraft :one
-- The text of the chapter is the working text of the draft: the chapter is written, only its edit is to come.
INSERT INTO chapter_drafts (project_id, title, book_id, after_chapter_id, idea, kind, body, edits_chapter_id, edits_revision, created_by, updated_by)
VALUES (sqlc.arg(project_id), sqlc.arg(title), sqlc.arg(book_id), sqlc.narg(after_chapter_id), '', 'sketch', sqlc.arg(body),
        sqlc.arg(chapter_id), sqlc.arg(edits_revision), sqlc.arg(actor), sqlc.arg(actor))
RETURNING id;

-- name: ReopenChapterEditDraft :exec
-- «Править главу» on a draft whose chapter changed in the book since: the draft takes the text of the book again.
UPDATE chapter_drafts
SET title = sqlc.arg(title), body = sqlc.arg(body), edits_revision = sqlc.arg(edits_revision), edits_previous_revision = NULL,
    revision = revision + 1, updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: MarkChapterDraftReplaced :exec
-- The chapter of the book took the text of the draft: the draft stands on its new revision and remembers the one before.
UPDATE chapter_drafts
SET title = sqlc.arg(title), edits_revision = sqlc.arg(edits_revision), edits_previous_revision = sqlc.narg(edits_previous_revision),
    body = sqlc.arg(body), revision = revision + 1, updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);


-- The plan of the writing agent (specs/2026-11-06-writing-agent.md, WA4, WA10): versions are immutable and numbered
-- per draft;

-- name: SetChapterDraftDecisions :exec
-- The case and the choices about the pack the author said (WA4.4); the revision stays: his text did not change.
UPDATE chapter_drafts SET case_choice = sqlc.narg(case_choice), pack_choices = sqlc.arg(pack_choices)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: CreateChapterDraftVersion :one
-- A version takes the next number of its draft.
WITH numbered AS (
  UPDATE chapter_drafts SET last_version_number = last_version_number + 1
  WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(draft_id)
  RETURNING last_version_number
)
INSERT INTO chapter_draft_versions (project_id, draft_id, number, reason, text, idea, words, author_words, plan_number, pack_id, support, job_id, place)
SELECT sqlc.arg(project_id), sqlc.arg(draft_id), numbered.last_version_number, sqlc.arg(reason), sqlc.arg(text), sqlc.narg(idea),
       sqlc.arg(words), sqlc.arg(author_words), sqlc.narg(plan_number), sqlc.narg(pack_id), sqlc.narg(support), sqlc.narg(job_id), sqlc.narg(place)
FROM numbered
RETURNING number;

-- name: ListChapterDraftVersions :many
-- A version is named by what it holds (specs/2026-11-11-ux9-writing-b.md WB3.1): the place its action changed and the
-- scenes a writing wrote.
SELECT number, reason, words, author_words, plan_number, created_at, place,
  COALESCE(jsonb_array_length(support), 0)::integer AS scenes
FROM chapter_draft_versions
WHERE project_id = sqlc.arg(project_id) AND draft_id = sqlc.arg(draft_id)
ORDER BY number DESC;

-- name: GetChapterDraftVersion :one
SELECT number, reason, text, idea, words, author_words, plan_number, pack_id, support, job_id, created_at, place
FROM chapter_draft_versions
WHERE project_id = sqlc.arg(project_id) AND draft_id = sqlc.arg(draft_id) AND number = sqlc.arg(number);

-- name: ChapterDraftOriginalSummary :one
-- What the page shows of the chapter the author loaded as a file (specs/2026-11-07-writing-proofread.md, PR7.5): its
-- name, its kind, its words, when it came, and whether it keeps the look of its Word file (PR9.3); the parsed chapter
-- counts the revisions of the file with corrections (specs/2026-11-11-ux5-proofread-mode.md PM1.4).
SELECT file_name, format, words, created_at, (document ? 'layout')::boolean AS look, document
FROM chapter_draft_originals
WHERE project_id = sqlc.arg(project_id) AND draft_id = sqlc.arg(draft_id);
