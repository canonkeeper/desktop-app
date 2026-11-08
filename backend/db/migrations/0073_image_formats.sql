-- Saga illustrations: the seven shapes of a drawing and the cutout (specs/2026-10-29-saga-illustrations.md
-- R16.1, R16.5, R18.3).

-- The shape a drawing is drawn in: the frame the model is told in the text of every request.
ALTER TABLE public.saga_images DROP CONSTRAINT saga_images_aspect_check;
ALTER TABLE public.saga_images ADD CONSTRAINT saga_images_aspect_check
  CHECK (aspect IN ('square', 'portrait', 'landscape', 'tall', 'wide', 'panorama', 'banner'));

-- cutout marks a version whose background was cut away: it is drawn and kept transparent, as an item
-- is, while it stays what it shows.
ALTER TABLE public.saga_images ADD COLUMN cutout boolean NOT NULL DEFAULT false;
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
