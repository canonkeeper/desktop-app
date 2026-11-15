-- The names and the threads of a saga that did not come together for the author's own AI or the helper of the account
-- (specs/2026-11-15-desk-reading-rest.md).
--
-- RR1.4, RR1.5: a mark of a failure acts only for the helper it happened with — the source, and of «Свой ИИ» also the host
-- of the provider, the model that reads the books and the limit of an answer, as a SHA-256 of them. A chapter marked before
-- this migration names no helper: its mark does not act, and the chapter gets one attempt.
ALTER TABLE public.reading_unread_chapters
    ADD COLUMN helper text,
    ADD CONSTRAINT reading_unread_chapters_helper_check CHECK (helper IS NULL OR helper ~ '^[0-9a-f]{64}$');

-- RR1.2, RR1.3, RR1.6: the register of names whose set — the digest of the list of the names of the cards — did not come
-- together, and the build of the threads over the stamps of its chapters — those its checkpoint keeps — with the version of
-- the instructions of the layer, by the text version whose Job marked it, with why and for which helper. A new failure of
-- the same saga, kind and text version replaces the mark; the saga takes its marks with it.
CREATE TABLE public.reading_unread_rest (
    project_id uuid NOT NULL,
    kind text NOT NULL,
    source_view text NOT NULL,
    names_digest text,
    layer_stamps jsonb,
    layer_version integer,
    reason text NOT NULL,
    helper text NOT NULL,
    marked_at timestamp with time zone NOT NULL,
    CONSTRAINT reading_unread_rest_pkey PRIMARY KEY (project_id, kind, source_view),
    CONSTRAINT reading_unread_rest_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT reading_unread_rest_kind_check CHECK (
      (kind = 'names' AND names_digest IS NOT NULL AND layer_stamps IS NULL AND layer_version IS NULL)
      OR (kind = 'threads' AND names_digest IS NULL AND jsonb_typeof(layer_stamps) = 'array' AND layer_version > 0)
    ),
    CONSTRAINT reading_unread_rest_source_view_check CHECK (source_view IN ('published', 'draft')),
    CONSTRAINT reading_unread_rest_names_digest_check CHECK (names_digest IS NULL OR char_length(names_digest) BETWEEN 1 AND 200),
    CONSTRAINT reading_unread_rest_reason_check CHECK (reason IN ('cut_answer', 'unreadable', 'refused', 'no_answer')),
    CONSTRAINT reading_unread_rest_helper_check CHECK (helper ~ '^[0-9a-f]{64}$')
);
REVOKE ALL ON TABLE public.reading_unread_rest FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.reading_unread_rest TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.reading_unread_rest TO canonkeeper_governance_owner;
