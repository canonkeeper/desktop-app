-- The writing agent (specs/2026-11-06-writing-agent.md, WA10): what the author decides before prose is data of the
-- draft. A plan is a version: «как я понял» and the scene cards, immutable, numbered per draft; the approval names a
-- number. The pack on the moment of the chapter is kept once per fingerprint; a version of the text keeps the whole
-- working text with the plan and the pack it was written by. Everything goes with its draft and its project.
ALTER TABLE public.chapter_drafts
    ADD COLUMN case_choice text,
    ADD COLUMN plan_number integer,
    ADD COLUMN plan_approved integer,
    ADD COLUMN pack_choices jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN last_version_number integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT chapter_drafts_case_choice_check
        CHECK (case_choice IS NULL OR case_choice IN ('scratch', 'continue', 'nearly_done')),
    ADD CONSTRAINT chapter_drafts_plan_version_check CHECK (
        (plan_number IS NULL OR plan_number >= 1)
        AND (plan_approved IS NULL OR plan_number IS NOT NULL AND plan_approved BETWEEN 1 AND plan_number)
    ),
    ADD CONSTRAINT chapter_drafts_pack_choices_check
        CHECK (jsonb_typeof(pack_choices) = 'array' AND jsonb_array_length(pack_choices) <= 100),
    ADD CONSTRAINT chapter_drafts_last_version_number_check CHECK (last_version_number >= 0);

CREATE TABLE public.chapter_draft_packs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    hash text NOT NULL,
    pack jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_draft_packs_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_draft_packs_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_packs_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_packs_hash_key UNIQUE (draft_id, hash),
    CONSTRAINT chapter_draft_packs_id_draft_key UNIQUE (id, draft_id),
    CONSTRAINT chapter_draft_packs_hash_check CHECK (hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chapter_draft_packs_pack_check CHECK (jsonb_typeof(pack) = 'object')
);
CREATE INDEX chapter_draft_packs_project_idx ON public.chapter_draft_packs (project_id);

CREATE TABLE public.chapter_draft_plans (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    number integer NOT NULL,
    origin text NOT NULL,
    reason text NOT NULL,
    words text,
    chapter_case text NOT NULL,
    basis_revision bigint NOT NULL,
    idea_hash text NOT NULL,
    pack_id uuid,
    understanding jsonb NOT NULL,
    scenes jsonb NOT NULL,
    marks jsonb NOT NULL DEFAULT '[]'::jsonb,
    turn_id uuid,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_draft_plans_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_draft_plans_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_plans_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_plans_pack_fkey FOREIGN KEY (pack_id, draft_id)
        REFERENCES public.chapter_draft_packs(id, draft_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_plans_number_key UNIQUE (draft_id, number),
    CONSTRAINT chapter_draft_plans_number_check CHECK (number >= 1),
    CONSTRAINT chapter_draft_plans_origin_check CHECK (origin IN ('helper', 'author')),
    CONSTRAINT chapter_draft_plans_reason_check
        CHECK (reason IN ('first', 'answers', 'author_words', 'from_here', 'new_idea', 'reverse', 'author_edit')),
    CONSTRAINT chapter_draft_plans_words_check CHECK (words IS NULL OR char_length(words) BETWEEN 1 AND 8000),
    CONSTRAINT chapter_draft_plans_case_check CHECK (chapter_case IN ('scratch', 'continue', 'nearly_done')),
    CONSTRAINT chapter_draft_plans_idea_hash_check CHECK (idea_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chapter_draft_plans_json_check CHECK (
        jsonb_typeof(understanding) = 'object' AND jsonb_typeof(scenes) = 'array' AND jsonb_typeof(marks) = 'array'
    )
);
CREATE INDEX chapter_draft_plans_project_idx ON public.chapter_draft_plans (project_id);

CREATE TABLE public.chapter_draft_versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    number integer NOT NULL,
    reason text NOT NULL,
    text text NOT NULL,
    words integer NOT NULL,
    author_words integer NOT NULL,
    plan_number integer,
    pack_id uuid,
    support jsonb,
    job_id uuid,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_draft_versions_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_draft_versions_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_versions_draft_fkey FOREIGN KEY (draft_id, project_id)
        REFERENCES public.chapter_drafts(id, project_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_versions_pack_fkey FOREIGN KEY (pack_id, draft_id)
        REFERENCES public.chapter_draft_packs(id, draft_id) ON DELETE CASCADE,
    CONSTRAINT chapter_draft_versions_number_key UNIQUE (draft_id, number),
    CONSTRAINT chapter_draft_versions_number_check CHECK (number >= 1),
    CONSTRAINT chapter_draft_versions_reason_check CHECK (reason IN (
        'written', 'before_write', 'before_apply', 'before_take', 'before_restore', 'before_delete', 'switch'
    )),
    CONSTRAINT chapter_draft_versions_text_check CHECK (char_length(text) <= 400000),
    CONSTRAINT chapter_draft_versions_words_check CHECK (words >= 0 AND author_words BETWEEN 0 AND words),
    CONSTRAINT chapter_draft_versions_plan_check CHECK (plan_number IS NULL OR plan_number >= 1),
    CONSTRAINT chapter_draft_versions_support_check CHECK (support IS NULL OR jsonb_typeof(support) = 'array')
);
CREATE INDEX chapter_draft_versions_project_idx ON public.chapter_draft_versions (project_id);

-- A question of the helper is a card (WA3.5): what it blocks, what is known with quotes, 2–3 options with what each
-- means for the scene, a recommendation and the option taken when the author skips it.
ALTER TABLE public.chapter_draft_notes
    ADD COLUMN blocks text,
    ADD COLUMN known text,
    ADD COLUMN effects text[],
    ADD COLUMN recommend text,
    ADD COLUMN fallback text,
    ADD CONSTRAINT chapter_draft_notes_card_check CHECK (
        (blocks IS NULL OR char_length(blocks) BETWEEN 1 AND 200)
        AND (known IS NULL OR char_length(known) BETWEEN 1 AND 2000)
        AND (effects IS NULL OR options IS NOT NULL AND cardinality(effects) = cardinality(options))
        AND (recommend IS NULL OR recommend IN ('А', 'Б', 'В', 'Г'))
        AND (fallback IS NULL OR fallback IN ('А', 'Б', 'В', 'Г'))
    );

REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_notes FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_packs FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_plans FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_versions FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_draft_packs TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_draft_plans TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_draft_versions TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_draft_packs TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.chapter_draft_plans TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.chapter_draft_versions TO canonkeeper_governance_owner;
