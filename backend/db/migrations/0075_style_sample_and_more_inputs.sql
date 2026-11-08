-- Saga illustrations, the style sample of a saga and more images at the input of a drawing
-- (specs/2026-10-29-saga-illustrations.md R19.1, R20.2).

-- style_image_id is the ready drawing every new drawing of the saga takes its manner from. Deleting
-- the drawing in any way clears it: no reference is left dangling.
ALTER TABLE public.project_illustration_settings
  ADD COLUMN style_image_id uuid,
  ADD CONSTRAINT project_illustration_settings_style_image_fkey FOREIGN KEY (project_id, style_image_id)
    REFERENCES public.saga_images(project_id, id) ON DELETE SET NULL (style_image_id);
REVOKE ALL ON TABLE public.project_illustration_settings FROM PUBLIC;

-- Ten images at the input of one call: a drawing keeps up to ten references, an edit up to eight
-- attached images, a layer up to nine references beside its crop.
ALTER TABLE public.saga_images
  DROP CONSTRAINT saga_images_references_check,
  ADD CONSTRAINT saga_images_references_check CHECK (cardinality(reference_image_ids) <= 10),
  DROP CONSTRAINT saga_images_attached_images_check,
  ADD CONSTRAINT saga_images_attached_images_check CHECK (cardinality(attached_images) <= 8 AND 0 < ALL (attached_images));
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

ALTER TABLE public.saga_image_layers
  DROP CONSTRAINT saga_image_layers_attached_images_check,
  ADD CONSTRAINT saga_image_layers_attached_images_check CHECK (cardinality(attached_images) <= 8 AND 0 < ALL (attached_images)),
  DROP CONSTRAINT saga_image_layers_reference_image_ids_check,
  ADD CONSTRAINT saga_image_layers_reference_image_ids_check CHECK (cardinality(reference_image_ids) <= 9);
REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
