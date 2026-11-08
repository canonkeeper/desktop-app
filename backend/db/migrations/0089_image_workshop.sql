-- The drawing workshop (specs/2026-11-02-image-workshop.md, IW5): a drawing is an object of the project — the
-- author's idea, the description for the artist whose pieces say where they come from (brief, IW2) and an
-- explicit current version among the rows of saga_images of its branch. The current version changes only by
-- the rules of IW5.2 (I3): the first single version of a drawing without one, a chosen variant, «Сделать
-- текущей», and on deleting it the latest ready version without a group. What the author wrote is his: the
-- export carries it, deleting a book or a chat keeps it, the project purge removes it.
CREATE TABLE public.saga_drawings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    branch integer,
    idea text DEFAULT '' NOT NULL,
    brief jsonb NOT NULL,
    brief_revision bigint DEFAULT 1 NOT NULL,
    current_image_id uuid,
    chat_id uuid,
    finished_at timestamp with time zone,
    finished_by uuid,
    created_by uuid NOT NULL,
    updated_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT saga_drawings_pkey PRIMARY KEY (id),
    CONSTRAINT saga_drawings_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_drawings_project_branch_key UNIQUE (project_id, branch),
    CONSTRAINT saga_drawings_current_fkey FOREIGN KEY (project_id, current_image_id)
        REFERENCES public.saga_images(project_id, id) ON DELETE SET NULL (current_image_id),
    CONSTRAINT saga_drawings_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id),
    CONSTRAINT saga_drawings_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id),
    CONSTRAINT saga_drawings_finished_by_fkey FOREIGN KEY (finished_by) REFERENCES public.users(id),
    CONSTRAINT saga_drawings_branch_check CHECK (branch IS NULL OR branch >= 1),
    CONSTRAINT saga_drawings_idea_check CHECK (char_length(idea) <= 4000),
    CONSTRAINT saga_drawings_brief_check CHECK (jsonb_typeof(brief) = 'object' AND octet_length(brief::text) <= 262144),
    CONSTRAINT saga_drawings_revision_check CHECK (brief_revision >= 1),
    CONSTRAINT saga_drawings_finished_check CHECK (finished_by IS NULL OR finished_at IS NOT NULL)
);

CREATE INDEX saga_drawings_project_updated_idx ON public.saga_drawings (project_id, updated_at DESC, id DESC);
CREATE INDEX saga_drawings_current_idx ON public.saga_drawings (project_id, current_image_id) WHERE current_image_id IS NOT NULL;
CREATE INDEX saga_drawings_chat_idx ON public.saga_drawings (chat_id) WHERE chat_id IS NOT NULL;
CREATE INDEX saga_drawings_created_by_idx ON public.saga_drawings (created_by);
CREATE INDEX saga_drawings_updated_by_idx ON public.saga_drawings (updated_by);
CREATE INDEX saga_drawings_finished_by_idx ON public.saga_drawings (finished_by) WHERE finished_by IS NOT NULL;

REVOKE ALL ON TABLE public.saga_drawings FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_drawings TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_drawings TO canonkeeper_governance_owner;

-- IW5.4: a drawing for every branch with a row that is not an upload (a branch of uploads alone is «Мои
-- картинки», R15.20). The current version is the latest ready row of the branch without a group and not an
-- upload. A drawing with a current version and no group waiting for the choice is finished; one without a
-- current version or with a group waiting stays in the workshop (IW10.4). The idea is empty: the author's words of
-- the old drawings are in the questions of the chat and are not copied. The description is one helper piece —
-- the chat's agent or the domain wrote it, no author's words or links in it are checked — taken from the
-- current version when it was drawn by a description, otherwise from the latest such row, otherwise from the
-- latest row: an enlargement or a cutout carries an instruction, not a description. The facts stay on the
-- versions; the helper carries them into the description with labels.
INSERT INTO public.saga_drawings (
    project_id, branch, idea, brief, current_image_id, finished_at, created_by, updated_by, created_at, updated_at
)
SELECT branch.project_id, branch.branch, '',
       jsonb_build_object(
         'title', COALESCE(described.title, ''),
         'kind', described.kind,
         'aspect', described.aspect,
         'chapter', COALESCE(described.chapter_key, ''),
         'subjects', to_jsonb(described.subject_names),
         'segments', jsonb_build_array(jsonb_build_object('text', described.prompt, 'origin', 'helper')),
         'facts', '[]'::jsonb, 'staging', '[]'::jsonb, 'references', 'null'::jsonb, 'images', '[]'::jsonb
       ),
       current_version.id,
       CASE WHEN current_version.id IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM public.saga_images AS waiting
         WHERE waiting.project_id = branch.project_id AND waiting.branch = branch.branch
           AND waiting.variant_group IS NOT NULL AND waiting.status IN ('drawing', 'ready')
       ) THEN clock_timestamp() END,
       COALESCE(first_version.created_by, project.owner_user_id),
       COALESCE(first_version.created_by, project.owner_user_id),
       first_version.created_at, last_version.created_at
FROM (
  SELECT project_id, branch FROM public.saga_images WHERE kind <> 'upload' GROUP BY project_id, branch
) AS branch
JOIN public.projects AS project ON project.id = branch.project_id
JOIN LATERAL (
  SELECT image.created_by, image.created_at FROM public.saga_images AS image
  WHERE image.project_id = branch.project_id AND image.branch = branch.branch AND image.kind <> 'upload'
  ORDER BY image.number LIMIT 1
) AS first_version ON true
JOIN LATERAL (
  SELECT image.created_at FROM public.saga_images AS image
  WHERE image.project_id = branch.project_id AND image.branch = branch.branch
  ORDER BY image.number DESC LIMIT 1
) AS last_version ON true
LEFT JOIN LATERAL (
  SELECT image.id FROM public.saga_images AS image
  WHERE image.project_id = branch.project_id AND image.branch = branch.branch AND image.kind <> 'upload'
    AND image.status = 'ready' AND image.variant_group IS NULL
  ORDER BY image.number DESC LIMIT 1
) AS current_version ON true
JOIN LATERAL (
  SELECT image.title, image.kind, image.aspect, image.chapter_key, image.subject_names, image.prompt
  FROM public.saga_images AS image
  WHERE image.project_id = branch.project_id AND image.branch = branch.branch AND image.kind <> 'upload'
  ORDER BY (image.id IS NOT DISTINCT FROM current_version.id AND NOT image.enlarged AND NOT image.cutout) DESC,
           (NOT image.enlarged AND NOT image.cutout) DESC, image.variant_group IS NULL DESC, image.number DESC
  LIMIT 1
) AS described ON true;

-- IW4.2: the conversation of the helper of a drawing is a chat session of its own kind with the turns and
-- the lifecycle of every turn; a session of the kind drawing is not a chat of the list.
ALTER TABLE public.chat_sessions
  ADD COLUMN kind text DEFAULT 'research' NOT NULL,
  ADD CONSTRAINT chat_sessions_kind_check CHECK (kind IN ('research', 'drawing'));
REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;

-- IW3.5: the candidates of one edit are layers of one group, hidden until the author chooses one; each
-- has its reading. One charge for the group, handed on as for the variants of a drawing (R21.4).
ALTER TABLE public.saga_image_layers
  ADD COLUMN variant_group uuid,
  ADD COLUMN variant_prompt text,
  ADD CONSTRAINT saga_image_layers_variant_check CHECK (variant_group IS NULL OR NOT visible),
  ADD CONSTRAINT saga_image_layers_variant_prompt_check
    CHECK (variant_prompt IS NULL OR char_length(btrim(variant_prompt)) BETWEEN 1 AND 4000);
REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
CREATE INDEX saga_image_layers_variant_group ON public.saga_image_layers (project_id, variant_group, position)
  WHERE variant_group IS NOT NULL;

-- IW8.2: the turns of the helper per Moscow day, the writer's own limit and the platform default.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN drawing_turns_per_day integer CHECK (drawing_turns_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN drawing_turns_per_day integer CHECK (drawing_turns_per_day >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN drawing_turns integer NOT NULL DEFAULT 0 CHECK (drawing_turns >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;

-- IW8.2: the spend of the helper's model is a source of its own.
ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check CHECK (source IN ('chat', 'scan', 'cards', 'images', 'layer', 'draft', 'drawing'));
REVOKE ALL ON public.model_usage_days FROM PUBLIC;
