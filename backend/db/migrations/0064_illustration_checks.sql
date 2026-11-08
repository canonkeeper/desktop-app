-- Saga illustrations, the self-check of a drawing (specs/2026-10-29-saga-illustrations.md UC12).

-- R12.1: facts are up to twelve short checkable facts of the text the agent derived for a drawing
-- or a region edit; sides are those of the character. R12.2: check_report is what a vision model
-- saw of each fact on the render of the drawing's file and the defects it noticed — status
-- pending while the illustration Job checks the render, done, or unavailable when the critic did
-- not answer; revision is the render_revision it judged. NULL — never checked (drawn before).
ALTER TABLE public.saga_images
  ADD COLUMN facts text[] NOT NULL DEFAULT '{}' CHECK (cardinality(facts) <= 12),
  ADD COLUMN check_report jsonb CHECK (
    check_report IS NULL
    OR (jsonb_typeof(check_report) = 'object' AND check_report->>'status' IN ('pending', 'done', 'unavailable'))
  );
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

-- R12.2: the check of the render right after a layer was laid in.
ALTER TABLE public.saga_image_layers
  ADD COLUMN facts text[] NOT NULL DEFAULT '{}' CHECK (cardinality(facts) <= 12),
  ADD COLUMN check_report jsonb CHECK (
    check_report IS NULL
    OR (jsonb_typeof(check_report) = 'object' AND check_report->>'status' IN ('pending', 'done', 'unavailable'))
  );
REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
