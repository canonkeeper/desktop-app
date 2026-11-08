-- The earlier writing goes (specs/2026-11-06-writing-agent.md, WA10.4, WA13, slice S8): «Дописать по замыслу», the
-- block of the helper and its rewriter. The data it left moves to the writing agent here; its columns stay unread until
-- the next release drops them (S10, rule P5.2), so they are only emptied.

-- No run of the earlier writing may cross the release: nothing reads its work any more. The release waits for every Job
-- to end before it stops the backend; one queued after that look stops the release here, and the previous one runs on.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.jobs
        WHERE kind = 'chapter_draft' AND subject_json->>'mode' = 'write'
          AND status NOT IN ('succeeded', 'failed', 'canceled', 'expired')
    ) THEN
        RAISE EXCEPTION 'a chapter_draft Job of the earlier writing has not ended: release again once it ends';
    END IF;
END
$$;

-- One working text: body. It is as long as the chapter text was.
ALTER TABLE public.chapter_drafts
    DROP CONSTRAINT chapter_drafts_body_check,
    ADD CONSTRAINT chapter_drafts_body_check CHECK (char_length(body) <= 400000);
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;

-- Where a chapter was written, the author's text before it stays as the version «switch» («ваш текст до помощника»):
-- its words are counted by the rule of «Ваших слов» (runs of letters, combining marks and digits; the author's are
-- those outside ⟦…⟧, testdata/words.json of the domain).
INSERT INTO public.chapter_draft_versions (project_id, draft_id, number, reason, text, words, author_words)
SELECT draft.project_id, draft.id, draft.last_version_number + 1, 'switch', draft.body,
       (SELECT count(*) FROM regexp_matches(replace(replace(draft.body, '⟦', ''), '⟧', ''),
            '[[:alnum:]À-ÖØ-öø-ɏЀ-ԯ̀-ͯ]+', 'g'))::integer,
       (SELECT count(*) FROM regexp_matches(regexp_replace(draft.body, '⟦[^⟧]*(⟧|$)', ' ', 'g'),
            '[[:alnum:]À-ÖØ-öø-ɏЀ-ԯ̀-ͯ]+', 'g'))::integer
FROM public.chapter_drafts AS draft
WHERE draft.chapter_text <> '' AND btrim(draft.body) <> '';

-- The chapter becomes the working text; the revision moves, so a page still holding the old text cannot write it over.
-- What the earlier writing kept for its next run and for its line of the outcome is emptied.
UPDATE public.chapter_drafts
SET last_version_number = last_version_number + CASE WHEN btrim(body) <> '' THEN 1 ELSE 0 END,
    body = chapter_text, chapter_text = '', revision = revision + 1
WHERE chapter_text <> '';
UPDATE public.chapter_drafts
SET pending_block = '', run_after_job = NULL, run_after_rework = false, last_run = '', last_run_words = 0, last_run_offset = 0
WHERE pending_block <> '' OR run_after_job IS NOT NULL OR run_after_rework OR last_run <> '' OR last_run_words <> 0 OR last_run_offset <> 0;

-- The notes on the chapter text stand in the working text now. The notes of the earlier writing (meaning, repeat,
-- machine) stay the author's data until he closes them (WA10.5).
UPDATE public.chapter_draft_notes SET target = 'draft' WHERE target = 'chapter';

-- The runs of the earlier writing stay in the history of the project, sealed as a deleted draft seals its runs (the
-- query EraseChapterDraftJobText, jobs.ChapterDraftSubject.Erased): who and where, nothing of the author's text; of the
-- result, the kind of work and what it read. A run of a deleted draft sealed before this release takes the same form.
UPDATE public.jobs
SET subject_json = jsonb_strip_nulls(jsonb_build_object(
        'type', subject_json->'type', 'projectId', subject_json->'projectId', 'draftId', subject_json->'draftId',
        'mode', subject_json->'mode', 'bookId', subject_json->'bookId', 'afterChapterId', subject_json->'afterChapterId',
        'erased', true
    )),
    result_json = CASE WHEN result_json IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
        'type', result_json->'type', 'mode', result_json->'mode', 'total', result_json->'total', 'done', result_json->'done',
        'chapters', result_json->'chapters'
    )) END
WHERE kind = 'chapter_draft' AND (subject_json->>'mode' = 'write' OR subject_json ? 'sketch');
