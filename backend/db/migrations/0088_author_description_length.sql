-- Saga illustrations (specs/2026-10-29-saga-illustrations.md R21.14, R26.5): the author describes a drawing in
-- up to 4000 characters wherever he does it, as long as the description itself (the owner 02.11: 300 were too
-- few). A variant's staging and a regeneration's note hold as much; the rows already kept fit.
ALTER TABLE public.saga_images
  DROP CONSTRAINT saga_images_variant_prompt_check,
  DROP CONSTRAINT saga_images_regenerate_note_check,
  ADD CONSTRAINT saga_images_variant_prompt_check
    CHECK (variant_prompt IS NULL OR char_length(variant_prompt) <= 4000),
  ADD CONSTRAINT saga_images_regenerate_note_check
    CHECK (regenerate_note IS NULL OR (regenerate_note = btrim(regenerate_note) AND char_length(regenerate_note) BETWEEN 1 AND 4000));

REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
