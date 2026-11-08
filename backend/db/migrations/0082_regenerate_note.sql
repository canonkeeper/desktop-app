-- Saga illustrations (specs/2026-10-29-saga-illustrations.md R26.5): what the author asked a
-- regeneration to take into account, apart from the description the next one draws by again.
ALTER TABLE public.saga_images ADD COLUMN regenerate_note text
  CHECK (regenerate_note IS NULL OR (regenerate_note = btrim(regenerate_note) AND char_length(regenerate_note) BETWEEN 1 AND 1000));

REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
