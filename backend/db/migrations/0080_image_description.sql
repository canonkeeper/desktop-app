-- Saga illustrations (specs/2026-10-29-saga-illustrations.md R23.2): the author describes a drawing in
-- his own words, longer than the caption the agent gives it.
ALTER TABLE public.saga_images DROP CONSTRAINT saga_images_title_check;
ALTER TABLE public.saga_images ADD CONSTRAINT saga_images_title_check
  CHECK (title IS NULL OR (title = btrim(title) AND char_length(title) BETWEEN 1 AND 200));

REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
