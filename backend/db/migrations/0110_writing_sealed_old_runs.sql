-- The runs of the writing agent made before the earlier writing went (specs/2026-11-06-writing-agent.md, WA13, slice
-- S8) keep keys the decoder of this release no longer knows: the start of a run put the title of the draft into its
-- subject. One such row makes every list of the project's Jobs a conflict. 0109 sealed only the runs of the earlier
-- writing (mode write or the key sketch); here every chapter_draft Job with a key of its subject or its result outside
-- the set of this release is sealed by the same rule as a run of a deleted draft (the query EraseChapterDraftJobText,
-- jobs.ChapterDraftSubject.Erased, jobs.ChapterDraftResult.Sealed): who and where, nothing of the author's text; of the
-- result, the kind of work, how far it came, what it read, the pack and the versions it kept. A row within the set is
-- left as it is: after 0109 production has no other.
DO $$
DECLARE
    -- The keys of jobs.ChapterDraftSubject and jobs.ChapterDraftResult in this release.
    subject_keys CONSTANT text[] := ARRAY['type', 'projectId', 'draftId', 'mode', 'bookId', 'afterChapterId', 'revision',
        'planNumber', 'scenes', 'from', 'tail', 'place', 'instruction', 'seams', 'erased'];
    result_keys CONSTANT text[] := ARRAY['type', 'mode', 'phase', 'total', 'chapters', 'done', 'sceneRuns', 'pack',
        'beforeVersion', 'writtenVersion'];
    earlier uuid[];
BEGIN
    SELECT coalesce(array_agg(id), ARRAY[]::uuid[]) INTO earlier
    FROM public.jobs
    WHERE kind = 'chapter_draft' AND (
        CASE WHEN jsonb_typeof(subject_json) = 'object' THEN subject_json - subject_keys <> '{}'::jsonb ELSE false END
        OR CASE WHEN jsonb_typeof(result_json) = 'object' THEN result_json - result_keys <> '{}'::jsonb ELSE false END
    );

    -- A run that has not ended is not sealed under its worker: it stops the release, and the previous one runs on.
    IF EXISTS (
        SELECT 1 FROM public.jobs
        WHERE id = ANY (earlier) AND status NOT IN ('succeeded', 'failed', 'canceled', 'expired')
    ) THEN
        RAISE EXCEPTION 'a chapter_draft Job of the earlier shape has not ended: release again once it ends';
    END IF;

    UPDATE public.jobs
    SET subject_json = jsonb_strip_nulls(jsonb_build_object(
            'type', subject_json->'type', 'projectId', subject_json->'projectId', 'draftId', subject_json->'draftId',
            'mode', subject_json->'mode', 'bookId', subject_json->'bookId', 'afterChapterId', subject_json->'afterChapterId',
            'erased', true
        )),
        result_json = CASE WHEN result_json IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
            'type', result_json->'type', 'mode', result_json->'mode', 'total', result_json->'total', 'done', result_json->'done',
            'chapters', result_json->'chapters', 'pack', result_json->'pack', 'beforeVersion', result_json->'beforeVersion',
            'writtenVersion', result_json->'writtenVersion'
        )) END
    WHERE kind = 'chapter_draft' AND id = ANY (earlier);
END
$$;

REVOKE ALL ON TABLE public.jobs FROM PUBLIC;
