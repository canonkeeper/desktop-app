-- The name registry of a saga (specs/2026-11-01-index-foundation.md, UC5): who is who under which
-- names, built by one model call over the names of all chapter cards. One row per project; it is
-- derived data: any source deletion removes it in its fence, and the next cards Job builds it again.
CREATE TABLE public.saga_names (
    project_id uuid NOT NULL,
    digest text NOT NULL,
    names_json jsonb NOT NULL,
    model text NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT saga_names_pkey PRIMARY KEY (project_id),
    CONSTRAINT saga_names_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_names_digest_check CHECK (char_length(digest) = 64),
    CONSTRAINT saga_names_names_json_check CHECK (jsonb_typeof(names_json) = 'object'),
    CONSTRAINT saga_names_model_check CHECK (model = btrim(model) AND char_length(model) BETWEEN 1 AND 200)
);

REVOKE ALL ON TABLE public.saga_names FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_names TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_names TO canonkeeper_governance_owner;
