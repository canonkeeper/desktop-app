-- The memory of the author (specs/2026-11-01-writer-assistant-index.md, UC4): what he said or approved
-- about his saga and the text does not hold: his plans, corrections, style rules and what he rejected.
-- It is author-written data of the project: deleting a book or a chapter keeps the records and only
-- drops the link (a soft-deleted chapter is just not shown, its hard purge clears the column), the
-- project purge removes them with the project row.
CREATE TABLE public.author_memory (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    kind text NOT NULL,
    body text NOT NULL,
    chapter_id uuid,
    name text,
    revision bigint DEFAULT 1 NOT NULL,
    created_by uuid NOT NULL,
    updated_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT author_memory_pkey PRIMARY KEY (id),
    CONSTRAINT author_memory_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT author_memory_chapter_fkey FOREIGN KEY (chapter_id, project_id)
        REFERENCES public.chapters(id, project_id) ON DELETE SET NULL (chapter_id),
    CONSTRAINT author_memory_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id),
    CONSTRAINT author_memory_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id),
    CONSTRAINT author_memory_kind_check CHECK (kind IN ('plan', 'correction', 'style_rule', 'rejected')),
    CONSTRAINT author_memory_body_check CHECK (body = btrim(body) AND char_length(body) BETWEEN 1 AND 8000),
    CONSTRAINT author_memory_name_check CHECK (name IS NULL OR (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200)),
    CONSTRAINT author_memory_revision_check CHECK (revision >= 1)
);

CREATE INDEX author_memory_project_created_idx ON public.author_memory (project_id, created_at DESC, id DESC);
CREATE INDEX author_memory_chapter_idx ON public.author_memory (chapter_id, project_id) WHERE chapter_id IS NOT NULL;
CREATE INDEX author_memory_created_by_idx ON public.author_memory (created_by);
CREATE INDEX author_memory_updated_by_idx ON public.author_memory (updated_by);

REVOKE ALL ON TABLE public.author_memory FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.author_memory TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.author_memory TO canonkeeper_governance_owner;
