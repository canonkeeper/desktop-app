-- The writer's workshop (specs/2026-11-02-writer-workshop.md): a chapter is written only from the author's own
-- draft, and what should happen is his idea, not a kind of draft (W2.1, W8). The columns of the plan stay until
-- the next release: the release before this one reads them after a rollback (W8.3).
ALTER TABLE public.chapter_drafts
  ADD COLUMN idea text NOT NULL DEFAULT '',
  ADD CONSTRAINT chapter_drafts_idea_check CHECK (char_length(idea) <= 8000);

-- W8.3: a draft of what should happen keeps it as its idea; the plan a model made from it moves into the
-- draft between the marks of what the author did not write, with his edits. An idea longer than its field
-- stays in the draft, so nothing he typed is lost. The revision moves, so a page with the old text cannot
-- overwrite the new one.
WITH converted AS (
  SELECT draft.id, draft.project_id,
    CASE WHEN char_length(draft.body) <= 8000 THEN draft.body ELSE '' END AS idea,
    CASE WHEN char_length(draft.body) > 8000 THEN draft.body ELSE '' END AS kept,
    draft.plan
  FROM public.chapter_drafts AS draft
  WHERE draft.kind = 'skeleton'
), placed AS (
  SELECT converted.*,
    -- What is left of the 120000 characters for the plan between its two marks.
    120000 - char_length(converted.kept) - CASE WHEN converted.kept <> '' THEN 2 ELSE 0 END - 2 AS room
  FROM converted
)
UPDATE public.chapter_drafts AS draft
SET idea = placed.idea,
    body = concat_ws(E'\n\n',
      NULLIF(placed.kept, ''),
      CASE WHEN placed.plan <> '' THEN '⟦' || left(placed.plan, placed.room) || '⟧' END),
    kind = CASE WHEN placed.plan <> '' THEN 'plan' ELSE 'sketch' END,
    revision = draft.revision + 1,
    updated_at = clock_timestamp()
FROM placed
WHERE draft.project_id = placed.project_id AND draft.id = placed.id;

ALTER TABLE public.chapter_drafts
  DROP CONSTRAINT chapter_drafts_kind_check,
  ADD CONSTRAINT chapter_drafts_kind_check CHECK (kind IN ('sketch', 'plan'));
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;

-- W8.1: the Jobs of a plan from what should happen go with their source owners: their subject names a text the
-- Job no longer takes. The Jobs of a chapter from a plan or a sketch stay, without the counts of the plan and
-- the rounds of the judge, who now reads a chapter once and rewrites nothing (W3.5).
CREATE TEMPORARY TABLE ck_removed_skeleton_job_owner (project_id uuid NOT NULL, id uuid NOT NULL) ON COMMIT DROP;
INSERT INTO ck_removed_skeleton_job_owner (project_id, id)
SELECT owner.project_id, owner.id
FROM public.artifact_source_owners AS owner
JOIN public.jobs AS job ON job.project_id = owner.project_id AND job.id = owner.artifact_id
WHERE owner.artifact_kind = 'job' AND job.kind = 'chapter_draft' AND job.subject_json ? 'skeleton';
DELETE FROM public.jobs WHERE kind = 'chapter_draft' AND subject_json ? 'skeleton';
DELETE FROM public.artifact_source_refs AS ref
USING ck_removed_skeleton_job_owner AS removed
WHERE ref.project_id = removed.project_id AND ref.owner_id = removed.id;
DELETE FROM public.artifact_source_owners AS owner
USING ck_removed_skeleton_job_owner AS removed
WHERE owner.project_id = removed.project_id AND owner.id = removed.id;

UPDATE public.jobs
SET result_json = result_json - 'plan' - 'beats' - 'examples' - 'threads' - 'review'
WHERE kind = 'chapter_draft' AND result_json IS NOT NULL;
REVOKE ALL ON TABLE public.jobs FROM PUBLIC;

-- W6, W7.1: the notes on a draft and its chapter, a table of their own, so that a note never moves the revision
-- of the draft the author types in. The writing Job writes its hints (meaning, repeat, machine), the helper its
-- brief, canon, questions and variants. A note belongs to its draft: deleting a book keeps it, deleting the
-- draft or the project removes it.
-- A note has a short number for the model, never repeated in its draft: the draft counts them.
ALTER TABLE public.chapter_drafts
  ADD CONSTRAINT chapter_drafts_id_project_key UNIQUE (id, project_id),
  ADD COLUMN last_note_number integer NOT NULL DEFAULT 0,
  ADD CONSTRAINT chapter_drafts_last_note_number_check CHECK (last_note_number >= 0);

CREATE TABLE public.chapter_draft_notes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    number integer NOT NULL,
    kind text NOT NULL,
    target text NOT NULL,
    origin text NOT NULL,
    section text,
    variant_group uuid,
    quote text,
    body text NOT NULL,
    replacement text,
    options text[],
    refs text[] NOT NULL DEFAULT '{}',
    answer text,
    status text NOT NULL,
    turn_id uuid,
    job_id uuid,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_draft_notes_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_draft_notes_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_notes_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_notes_number_key UNIQUE (draft_id, number),
    CONSTRAINT chapter_draft_notes_number_check CHECK (number >= 1),
    CONSTRAINT chapter_draft_notes_kind_check
        CHECK (kind IN ('brief', 'canon', 'question', 'variant', 'meaning', 'repeat', 'machine')),
    CONSTRAINT chapter_draft_notes_target_check CHECK (target IN ('draft', 'chapter', 'none')),
    CONSTRAINT chapter_draft_notes_origin_check CHECK (
      origin IN ('helper', 'job') AND (origin = 'job') = (kind IN ('meaning', 'repeat', 'machine'))
      AND (origin = 'job') = (job_id IS NOT NULL)
    ),
    CONSTRAINT chapter_draft_notes_section_check CHECK (section IS NULL OR char_length(section) BETWEEN 1 AND 200),
    CONSTRAINT chapter_draft_notes_quote_check CHECK (quote IS NULL OR char_length(quote) BETWEEN 1 AND 2000),
    CONSTRAINT chapter_draft_notes_body_check CHECK (char_length(body) BETWEEN 1 AND 2000),
    CONSTRAINT chapter_draft_notes_replacement_check
        CHECK (replacement IS NULL OR char_length(replacement) BETWEEN 1 AND 4000),
    CONSTRAINT chapter_draft_notes_options_check CHECK (options IS NULL OR cardinality(options) BETWEEN 2 AND 4),
    CONSTRAINT chapter_draft_notes_refs_check CHECK (cardinality(refs) <= 5),
    CONSTRAINT chapter_draft_notes_answer_check CHECK (answer IS NULL OR char_length(answer) BETWEEN 1 AND 2000),
    CONSTRAINT chapter_draft_notes_status_check CHECK (status IN ('open', 'answered', 'applied', 'dismissed'))
);

CREATE INDEX chapter_draft_notes_project_idx ON public.chapter_draft_notes (project_id);

REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_notes FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_draft_notes TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_draft_notes TO canonkeeper_governance_owner;

-- The memory of the author is the background of the helper (owner 03.11, Decision log): what the chat remembered in a
-- turn is shown there as «Запомнил: …» with «Отменить», so a record keeps the Job of the turn that wrote it, which every
-- line of the feed names. No foreign key: a deleted chat leaves the record, as a deleted book does.
ALTER TABLE public.author_memory ADD COLUMN job_id uuid;
CREATE INDEX author_memory_job_idx ON public.author_memory (project_id, job_id) WHERE job_id IS NOT NULL;
REVOKE ALL ON TABLE public.author_memory FROM PUBLIC;
