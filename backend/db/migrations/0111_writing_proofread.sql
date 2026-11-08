-- The proofreading of a chapter of the author (specs/2026-11-07-writing-proofread.md, PR7): the chapter he loaded as a
-- file or a text is kept parsed — its title and its paragraphs with their italic and bold — one row a draft, never the
-- file itself; the file with Word revisions is written against it. It goes with its draft and its project. A correction
-- of the proofreading is a note of its own kind, of the Job only; two reasons of a version name the text before a file
-- replaced it and the text a proofreading read.
CREATE TABLE public.chapter_draft_originals (
    draft_id uuid NOT NULL,
    project_id uuid NOT NULL,
    file_name text NOT NULL,
    format text NOT NULL,
    document jsonb NOT NULL,
    words integer NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_draft_originals_pkey PRIMARY KEY (draft_id),
    CONSTRAINT chapter_draft_originals_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_originals_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_originals_file_name_check CHECK (char_length(file_name) BETWEEN 1 AND 255),
    CONSTRAINT chapter_draft_originals_format_check CHECK (format IN ('docx', 'txt', 'md', 'text')),
    CONSTRAINT chapter_draft_originals_document_check
        CHECK (jsonb_typeof(document) = 'object' AND octet_length(document::text) <= 16777216),
    CONSTRAINT chapter_draft_originals_words_check CHECK (words >= 0)
);
CREATE INDEX chapter_draft_originals_project_idx ON public.chapter_draft_originals (project_id);

ALTER TABLE public.chapter_draft_notes
    DROP CONSTRAINT chapter_draft_notes_kind_check,
    DROP CONSTRAINT chapter_draft_notes_origin_check,
    ADD CONSTRAINT chapter_draft_notes_kind_check CHECK (kind IN (
        'brief', 'canon', 'question', 'variant', 'replaced', 'meaning', 'repeat', 'machine',
        'new', 'style', 'craft', 'seam', 'plan_follow', 'proofread'
    )),
    ADD CONSTRAINT chapter_draft_notes_origin_check CHECK (
        origin IN ('helper', 'job')
        AND (origin = 'job') = (job_id IS NOT NULL)
        AND (origin = 'job' OR kind IN ('brief', 'canon', 'question', 'variant', 'replaced'))
        AND (origin = 'helper' OR kind IN (
            'meaning', 'repeat', 'machine', 'new', 'question', 'variant', 'canon', 'style', 'craft', 'seam', 'plan_follow',
            'proofread'
        ))
    );

ALTER TABLE public.chapter_draft_versions
    DROP CONSTRAINT chapter_draft_versions_reason_check,
    ADD CONSTRAINT chapter_draft_versions_reason_check CHECK (reason IN (
        'written', 'before_write', 'before_apply', 'before_take', 'before_restore', 'before_delete', 'switch',
        'before_upload', 'before_proofread'
    ));

REVOKE ALL ON TABLE public.chapter_draft_originals FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_notes FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_versions FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_draft_originals TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_draft_originals TO canonkeeper_governance_owner;
