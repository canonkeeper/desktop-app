-- Saga illustrations, the branch of a drawing (specs/2026-10-29-saga-illustrations.md UC14, R14.1).

-- branch is the number of the first drawing of the branch a drawing belongs to: its own number for
-- a drawing without a base; an edit by words, a new version of the agent and «Ещё вариант» take the
-- branch of the drawing they are made from. It is kept, not derived from base_image_id: a variant of
-- a first drawing has no base, and deleting a version nulls the base of its edits, which would split
-- the branch. Deleting drawings never changes it.
ALTER TABLE public.saga_images ADD COLUMN branch integer;

-- A first drawing joins the branch of the earliest first drawing with the same description, kind
-- and shape: before UC14 a variant copied the description and kept no link to its original. Every
-- other drawing takes the branch of its base.
WITH RECURSIVE chain AS (
  SELECT image.project_id, image.id, (
           SELECT min(earlier.number) FROM public.saga_images AS earlier
           WHERE earlier.project_id = image.project_id AND earlier.base_image_id IS NULL
             AND earlier.prompt = image.prompt AND earlier.kind = image.kind AND earlier.aspect = image.aspect
         ) AS branch
  FROM public.saga_images AS image
  WHERE image.base_image_id IS NULL
  UNION ALL
  SELECT edit.project_id, edit.id, chain.branch
  FROM public.saga_images AS edit
  JOIN chain ON chain.project_id = edit.project_id AND chain.id = edit.base_image_id
)
UPDATE public.saga_images AS image SET branch = chain.branch
FROM chain WHERE chain.project_id = image.project_id AND chain.id = image.id;

ALTER TABLE public.saga_images
  ALTER COLUMN branch SET NOT NULL,
  ADD CONSTRAINT saga_images_branch_check CHECK (branch BETWEEN 1 AND number);
CREATE INDEX saga_images_project_branch_idx ON public.saga_images USING btree (project_id, branch, number DESC);
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
