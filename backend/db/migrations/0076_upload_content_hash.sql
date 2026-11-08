-- Saga illustrations (specs/2026-10-29-saga-illustrations.md R15.21): the same file uploaded again in a
-- saga is the image it already has. content_hash is the SHA-256 of the file as sent; uploads made
-- before this migration have none.
ALTER TABLE public.saga_images
  ADD COLUMN content_hash text CHECK (content_hash IS NULL OR content_hash ~ '^[0-9a-f]{64}$');
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

CREATE UNIQUE INDEX saga_images_upload_content ON public.saga_images (project_id, content_hash)
  WHERE content_hash IS NOT NULL;
