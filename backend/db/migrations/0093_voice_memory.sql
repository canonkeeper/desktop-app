-- The memory of the voice co-author about a chapter draft (specs/2026-11-03-voice-assistant.md V1.10, V1.11): what a
-- conversation leaves for the next one — its summary and the plan of scenes. No audio and no transcript: the summary
-- is a few lines the agent writes when the conversation ends. Both go with their draft and their project.
CREATE TABLE public.chapter_draft_talks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    summary text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_draft_talks_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_draft_talks_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_talks_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_talks_summary_check CHECK (char_length(summary) BETWEEN 1 AND 2000)
);
CREATE INDEX chapter_draft_talks_draft_idx ON public.chapter_draft_talks (draft_id, created_at DESC);
CREATE INDEX chapter_draft_talks_project_idx ON public.chapter_draft_talks (project_id);

-- The plan of a conversation: scene number, title, whether material is written and the material the plan wrote at
-- the end of «Ваш текст».
CREATE TABLE public.chapter_draft_scenes (
    project_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    number integer NOT NULL,
    title text NOT NULL,
    filled boolean NOT NULL,
    material text[] NOT NULL DEFAULT '{}',
    CONSTRAINT chapter_draft_scenes_pkey PRIMARY KEY (draft_id, number),
    CONSTRAINT chapter_draft_scenes_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_scenes_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_scenes_number_check CHECK (number BETWEEN 1 AND 20),
    CONSTRAINT chapter_draft_scenes_title_check CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT chapter_draft_scenes_material_check CHECK (cardinality(material) <= 200)
);
CREATE INDEX chapter_draft_scenes_project_idx ON public.chapter_draft_scenes (project_id);

REVOKE ALL ON TABLE public.chapter_draft_talks FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_scenes FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.chapter_draft_talks TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, DELETE ON TABLE public.chapter_draft_scenes TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_draft_talks TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.chapter_draft_scenes TO canonkeeper_governance_owner;
