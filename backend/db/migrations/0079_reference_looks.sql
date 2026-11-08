-- An entry may hold several references from one chapter: other angles of a look or, under a caption,
-- other looks (specs/2026-10-29-saga-illustrations.md R22.1, R22.2). Nothing replaces any more: a row
-- is one drawing as a reference of one entry from one chapter.
ALTER TABLE public.saga_image_references
  ADD COLUMN look text NOT NULL DEFAULT ''
  CONSTRAINT saga_image_references_look_check CHECK (char_length(look) <= 60);

ALTER TABLE public.saga_image_references DROP CONSTRAINT saga_image_references_pkey;
ALTER TABLE public.saga_image_references ADD PRIMARY KEY (project_id, subject_key, from_chapter, image_id);

REVOKE ALL ON TABLE public.saga_image_references FROM PUBLIC;
