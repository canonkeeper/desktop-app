-- Saga illustrations, the images an author attaches to an edit (specs/2026-10-29-saga-illustrations.md
-- R15.15, R15.16).

-- attached_images are the gallery numbers of the images attached to an edit by words or a region edit,
-- in order: the place in the list is what the author's text calls «фото N». They are numbers, not ids:
-- numbers are never given again, and an image deleted later keeps its place, so the anchors of the
-- others and of the text stay as they were.
ALTER TABLE public.saga_images
  ADD COLUMN attached_images integer[] NOT NULL DEFAULT '{}'
    CHECK (cardinality(attached_images) <= 4 AND 0 < ALL (attached_images));
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

-- reference_image_ids are the references the agent names for a region edit (saga.draw with region and
-- references), read like those of a drawing: a deleted one is left out.
ALTER TABLE public.saga_image_layers
  ADD COLUMN attached_images integer[] NOT NULL DEFAULT '{}'
    CHECK (cardinality(attached_images) <= 4 AND 0 < ALL (attached_images)),
  ADD COLUMN reference_image_ids uuid[] NOT NULL DEFAULT '{}'
    CHECK (cardinality(reference_image_ids) <= 4);
REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
