-- Saga illustrations, «Увеличить ×2» (specs/2026-10-29-saga-illustrations.md R17.1, R17.12).

-- enlarged marks a version redrawn tile by tile from its base at twice its size: the web names it
-- and shows its progress, its Job draws tiles instead of a description, and its variant enlarges the
-- same base again. It cannot be told from the description (an edit may say the same words) nor from
-- the sizes (the base may be deleted).
-- partial_frame of an enlarged version counts its tiles: 0 — no preview yet, 1 — the preview is kept,
-- one more for each of at most 10 tiles done.
ALTER TABLE public.saga_images
  ADD COLUMN enlarged boolean NOT NULL DEFAULT false,
  DROP CONSTRAINT saga_images_partial_frame_check,
  ADD CONSTRAINT saga_images_partial_frame_check CHECK (partial_frame BETWEEN 0 AND 11);
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
