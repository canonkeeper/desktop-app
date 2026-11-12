-- The wave of the preparation of a saga (specs/2026-11-09-first-session.md, FS1): one row a project. The finalisation of
-- the import of a new book opens it, and the cards Job that wrote the last card closes it once — the single fact «Сага
-- готова» that the letter (FS2) and the line of the site (FS4) are told from. An open wave has no ready_* values.
CREATE TABLE public.saga_ready_waves (
    project_id uuid NOT NULL,
    started_at timestamp with time zone NOT NULL,
    started_by_user_id uuid,
    ready_at timestamp with time zone,
    ready_books integer,
    ready_chapters integer,
    CONSTRAINT saga_ready_waves_pkey PRIMARY KEY (project_id),
    CONSTRAINT saga_ready_waves_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_ready_waves_started_by_user_id_fkey FOREIGN KEY (started_by_user_id)
        REFERENCES public.users(id) ON DELETE SET NULL,
    CONSTRAINT saga_ready_waves_ready_check CHECK (
        (ready_at IS NULL AND ready_books IS NULL AND ready_chapters IS NULL)
        OR (ready_at IS NOT NULL AND ready_books > 0 AND ready_chapters > 0)
    )
);

REVOKE ALL ON TABLE public.saga_ready_waves FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_ready_waves TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_ready_waves TO canonkeeper_governance_owner;

-- The author's choice to get the letter «Сага готова» (FS3): on until the author turns it off in «Кабинет».
ALTER TABLE public.users ADD COLUMN saga_ready_letters boolean DEFAULT true NOT NULL;
REVOKE ALL ON TABLE public.users FROM PUBLIC;
