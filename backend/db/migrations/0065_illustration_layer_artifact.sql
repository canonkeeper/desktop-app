-- Saga illustrations, the artifact guard of a region edit (specs/2026-10-29-saga-illustrations.md
-- R12.9): a layer whose answers the guard rejected twice — a band, a flat patch or a seam the model
-- would bring into the drawing — ends failed with layer_artifact.
ALTER TABLE public.saga_image_layers
  DROP CONSTRAINT saga_image_layers_error_code_check,
  DROP CONSTRAINT saga_image_layers_error_check,
  ADD CONSTRAINT saga_image_layers_error_code_check CHECK (
    error_code IS NULL OR error_code IN ('draw_refused', 'draw_failed', 'layer_artifact')
  ),
  ADD CONSTRAINT saga_image_layers_error_check CHECK (
    (status = 'refused' AND error_code = 'draw_refused')
    OR (status = 'failed' AND error_code IN ('draw_failed', 'layer_artifact'))
    OR (status IN ('drawing', 'ready') AND error_code IS NULL AND error_message IS NULL)
  );
REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
