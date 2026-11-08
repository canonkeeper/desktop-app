-- Saga illustrations, the caption of a drawing (specs/2026-10-29-saga-illustrations.md R8.17).

-- title is the caption the author sees in the gallery, the lightbox, the chat and the reader —
-- «Герой у костра ночью», not the description the image model reads. The agent gives it with
-- saga.draw; a new version without its own takes the caption of its base, a style sketch is
-- «Эскиз: акварель». NULL — a drawing made before captions: the web shows the start of its
-- description.
ALTER TABLE public.saga_images
  ADD COLUMN title text CHECK (title IS NULL OR (title = btrim(title) AND char_length(title) BETWEEN 1 AND 80));
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
