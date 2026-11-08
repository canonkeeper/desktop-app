-- The section «Письмо» (specs/2026-11-01-writer-assistant-index.md, UC7): a chapter is written in a draft of
-- the project and not in a chat. What the author wrote is his: the export carries it, deleting a book or a
-- chapter keeps the draft and only clears its link at the hard purge, the project purge removes it.
CREATE TABLE public.chapter_drafts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    title text NOT NULL,
    book_id uuid,
    after_chapter_id uuid,
    kind text NOT NULL,
    body text NOT NULL,
    plan text NOT NULL DEFAULT '',
    plan_edited boolean NOT NULL DEFAULT false,
    chapter_text text NOT NULL DEFAULT '',
    chapter_edited boolean NOT NULL DEFAULT false,
    job_id uuid,
    added_chapter_id uuid,
    revision bigint DEFAULT 1 NOT NULL,
    created_by uuid NOT NULL,
    updated_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_drafts_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_drafts_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_drafts_book_fkey FOREIGN KEY (book_id, project_id)
        REFERENCES public.books(id, project_id) ON DELETE SET NULL (book_id),
    CONSTRAINT chapter_drafts_after_chapter_fkey FOREIGN KEY (after_chapter_id, project_id)
        REFERENCES public.chapters(id, project_id) ON DELETE SET NULL (after_chapter_id),
    CONSTRAINT chapter_drafts_added_chapter_fkey FOREIGN KEY (added_chapter_id, project_id)
        REFERENCES public.chapters(id, project_id) ON DELETE SET NULL (added_chapter_id),
    CONSTRAINT chapter_drafts_job_fkey FOREIGN KEY (job_id, project_id)
        REFERENCES public.jobs(id, project_id) ON DELETE SET NULL (job_id),
    CONSTRAINT chapter_drafts_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id),
    CONSTRAINT chapter_drafts_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id),
    CONSTRAINT chapter_drafts_kind_check CHECK (kind IN ('skeleton', 'sketch', 'plan')),
    CONSTRAINT chapter_drafts_title_check CHECK (title = btrim(title) AND char_length(title) <= 200),
    CONSTRAINT chapter_drafts_body_check CHECK (char_length(body) <= 120000),
    CONSTRAINT chapter_drafts_plan_check CHECK (char_length(plan) <= 120000),
    CONSTRAINT chapter_drafts_chapter_check CHECK (char_length(chapter_text) <= 400000),
    CONSTRAINT chapter_drafts_revision_check CHECK (revision >= 1)
);

CREATE INDEX chapter_drafts_project_created_idx ON public.chapter_drafts (project_id, created_at DESC, id DESC);
CREATE INDEX chapter_drafts_book_idx ON public.chapter_drafts (book_id, project_id) WHERE book_id IS NOT NULL;
CREATE INDEX chapter_drafts_after_chapter_idx ON public.chapter_drafts (after_chapter_id, project_id) WHERE after_chapter_id IS NOT NULL;
CREATE INDEX chapter_drafts_added_chapter_idx ON public.chapter_drafts (added_chapter_id, project_id) WHERE added_chapter_id IS NOT NULL;
CREATE INDEX chapter_drafts_job_idx ON public.chapter_drafts (job_id, project_id) WHERE job_id IS NOT NULL;
CREATE INDEX chapter_drafts_created_by_idx ON public.chapter_drafts (created_by);
CREATE INDEX chapter_drafts_updated_by_idx ON public.chapter_drafts (updated_by);

REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_drafts TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_drafts TO canonkeeper_governance_owner;

-- R7.6: the runs of a plan or a chapter per Moscow day, the writer's own limit and the platform default.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN chapter_drafts_per_day integer CHECK (chapter_drafts_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN chapter_drafts_per_day integer CHECK (chapter_drafts_per_day >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN chapter_drafts integer NOT NULL DEFAULT 0 CHECK (chapter_drafts >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;

-- R7.3: the Job belongs to a draft. No release ever had one of a chat; those of the stand go with their
-- owners: their subject names a chat and a mode that no longer exist.
CREATE TEMPORARY TABLE ck_removed_chat_draft_owner (project_id uuid NOT NULL, id uuid NOT NULL) ON COMMIT DROP;
INSERT INTO ck_removed_chat_draft_owner (project_id, id)
SELECT owner.project_id, owner.id
FROM public.artifact_source_owners AS owner
JOIN public.jobs AS job ON job.project_id = owner.project_id AND job.id = owner.artifact_id
WHERE owner.artifact_kind = 'job' AND job.kind = 'chapter_draft';
DELETE FROM public.jobs WHERE kind = 'chapter_draft';
DELETE FROM public.artifact_source_refs AS ref
USING ck_removed_chat_draft_owner AS removed
WHERE ref.project_id = removed.project_id AND ref.owner_id = removed.id;
DELETE FROM public.artifact_source_owners AS owner
USING ck_removed_chat_draft_owner AS removed
WHERE owner.project_id = removed.project_id AND owner.id = removed.id;
