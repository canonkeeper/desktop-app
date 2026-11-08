-- Saga illustrations, the author's own images (specs/2026-10-29-saga-illustrations.md UC15).

-- R15.8: an image the author uploads is an item of the gallery of the kind upload: it has its
-- number K, caption, author and file like a drawing, but no description, model or Job, and it is
-- ready from the start. Exactly an upload has no prompt, model and job_id. An upload is a branch of
-- its own (UC15 R15.1): its branch is its own number, and a drawing made from it starts another.
ALTER TABLE public.saga_images
  ALTER COLUMN prompt DROP NOT NULL,
  ALTER COLUMN model DROP NOT NULL,
  ALTER COLUMN job_id DROP NOT NULL,
  DROP CONSTRAINT saga_images_kind_check,
  ADD CONSTRAINT saga_images_kind_check CHECK (kind IN ('character', 'place', 'scene', 'item', 'other', 'sheet', 'upload')),
  ADD CONSTRAINT saga_images_upload_check CHECK (
    (kind = 'upload') = (prompt IS NULL)
    AND (kind = 'upload') = (model IS NULL)
    AND (kind = 'upload') = (job_id IS NULL)
    AND (kind <> 'upload' OR (status = 'ready' AND title IS NOT NULL AND NOT sketch AND branch = number))
  );
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

-- R15.4: the author's confirmation of the rights to the images he uploads and of the consent of
-- the people on them, once per account and edition of the legal documents (version). Deleting the
-- account deletes it.
CREATE TABLE public.image_upload_consents (
  user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
  version text NOT NULL CHECK (version = btrim(version) AND char_length(version) BETWEEN 1 AND 32),
  accepted_at timestamptz NOT NULL
);
REVOKE ALL ON TABLE public.image_upload_consents FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.image_upload_consents TO canonkeeper_product_runtime;

-- R15.7: uploads per Moscow day, the writer's own limit and the platform default.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN image_uploads_per_day integer CHECK (image_uploads_per_day >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN image_uploads_per_day integer CHECK (image_uploads_per_day >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;
ALTER TABLE public.writer_usage_days
  ADD COLUMN image_uploads integer NOT NULL DEFAULT 0 CHECK (image_uploads >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;
