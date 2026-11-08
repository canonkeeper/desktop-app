ALTER TABLE public.personal_notes
 ADD COLUMN title text NOT NULL DEFAULT '',
 ADD CONSTRAINT personal_notes_title_length CHECK (char_length(title) <= 200);
REVOKE ALL ON public.personal_notes FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.personal_notes TO canonkeeper_product_runtime;
