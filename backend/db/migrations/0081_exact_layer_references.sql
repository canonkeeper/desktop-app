-- Saga illustrations (specs/2026-10-29-saga-illustrations.md R25.2): a region edit whose author chose
-- the images himself takes exactly them; the choice of a new drawing lies in its reference_image_ids.
ALTER TABLE public.saga_image_layers ADD COLUMN exact_references boolean NOT NULL DEFAULT false;

REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
