-- Saga illustrations, the image limit of the provider (specs/2026-10-29-saga-illustrations.md
-- UC13): drawings go through the images routes of the model API, which tell a used-up image limit
-- apart from a content refusal.

-- R13.5: a drawing or a layer the provider declined because its image limit is used up ends
-- failed with draw_limited; retry_at is when the provider resets the limit, NULL when it did not
-- say. R13.6: charge is what the transaction that started the drawing or the layer spent of its
-- author — quota: a place in images_per_day, trial: a trial drawing as well; NULL: nothing (a free
-- style sketch, a drawing started before this migration) or given back already. A drawing or a
-- layer that ends draw_failed or draw_limited gives it back in the transaction that ends its Job.
ALTER TABLE public.saga_images
  ADD COLUMN retry_at timestamptz,
  ADD COLUMN charge text CHECK (charge IS NULL OR charge IN ('quota', 'trial')),
  DROP CONSTRAINT saga_images_error_code_check,
  DROP CONSTRAINT saga_images_error_check,
  ADD CONSTRAINT saga_images_error_code_check CHECK (
    error_code IS NULL OR error_code IN ('draw_refused', 'draw_failed', 'draw_limited')
  ),
  ADD CONSTRAINT saga_images_error_check CHECK (
    (status = 'refused' AND error_code = 'draw_refused')
    OR (status = 'failed' AND error_code IN ('draw_failed', 'draw_limited'))
    OR (status IN ('drawing', 'ready') AND error_code IS NULL AND error_message IS NULL)
  ),
  ADD CONSTRAINT saga_images_retry_check CHECK (retry_at IS NULL OR error_code = 'draw_limited');
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

ALTER TABLE public.saga_image_layers
  ADD COLUMN retry_at timestamptz,
  ADD COLUMN charge text CHECK (charge IS NULL OR charge IN ('quota', 'trial')),
  DROP CONSTRAINT saga_image_layers_error_code_check,
  DROP CONSTRAINT saga_image_layers_error_check,
  ADD CONSTRAINT saga_image_layers_error_code_check CHECK (
    error_code IS NULL OR error_code IN ('draw_refused', 'draw_failed', 'draw_limited', 'layer_artifact')
  ),
  ADD CONSTRAINT saga_image_layers_error_check CHECK (
    (status = 'refused' AND error_code = 'draw_refused')
    OR (status = 'failed' AND error_code IN ('draw_failed', 'draw_limited', 'layer_artifact'))
    OR (status IN ('drawing', 'ready') AND error_code IS NULL AND error_message IS NULL)
  ),
  ADD CONSTRAINT saga_image_layers_retry_check CHECK (retry_at IS NULL OR error_code = 'draw_limited');
REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
