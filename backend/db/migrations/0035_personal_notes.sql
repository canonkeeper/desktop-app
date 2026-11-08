-- Notes are account-owned copies; deleting an origin never deletes the note.
CREATE TABLE public.personal_notes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_user_id uuid NOT NULL REFERENCES public.users(id),
 body text NOT NULL CHECK (length(btrim(body)) > 0 AND char_length(body) <= 100000),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 source_json jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (source_json IS NULL OR jsonb_typeof(source_json) = 'object')
);
CREATE INDEX personal_notes_owner_updated_idx ON public.personal_notes(owner_user_id, updated_at DESC, id DESC);
REVOKE ALL ON public.personal_notes FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.personal_notes TO canonkeeper_product_runtime;
