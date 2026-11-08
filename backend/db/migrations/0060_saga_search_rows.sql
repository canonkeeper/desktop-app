-- Saga search rows (specs/2026-10-28-saga-search-in-postgres.md, UC1): the paragraph index of the
-- chat tools is kept in PostgreSQL instead of the backend heap. One chapter text has one row of
-- paragraph orders and token counts and one row per distinct normalized word with its
-- russian_stem lexeme and postings. Like chapter cards, the rows are keyed by the content hash
-- of the chapter text, so the draft and published versions of the same text share them, and
-- they are derived data that disappear with their chapter or project.
CREATE TABLE public.chapter_search_texts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    content_hash bytea NOT NULL,
    paragraph_orders integer[] NOT NULL,
    paragraph_tokens integer[] NOT NULL,
    text_runes integer NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_search_texts_pkey PRIMARY KEY (id),
    CONSTRAINT chapter_search_texts_text_key UNIQUE (project_id, chapter_id, content_hash),
    CONSTRAINT chapter_search_texts_project_key UNIQUE (id, project_id),
    CONSTRAINT chapter_search_texts_content_hash_check CHECK (octet_length(content_hash) = 32),
    CONSTRAINT chapter_search_texts_paragraphs_check CHECK (
        cardinality(paragraph_orders) = cardinality(paragraph_tokens)
        AND array_position(paragraph_orders, NULL) IS NULL
        AND array_position(paragraph_tokens, NULL) IS NULL
    ),
    CONSTRAINT chapter_search_texts_text_runes_check CHECK (text_runes >= 0),
    CONSTRAINT chapter_search_texts_chapter_id_project_id_fkey FOREIGN KEY (chapter_id, project_id)
        REFERENCES public.chapters(id, project_id) ON DELETE CASCADE
);

-- Every word row repeats the project so that every lookup of a search call stays inside the
-- project's part of the word and stem indexes. Words and stems compare bytewise.
CREATE TABLE public.chapter_search_words (
    text_id uuid NOT NULL,
    project_id uuid NOT NULL,
    word text COLLATE "C" NOT NULL,
    stem text COLLATE "C" NOT NULL,
    postings bytea NOT NULL,
    CONSTRAINT chapter_search_words_word_check CHECK (word <> ''),
    CONSTRAINT chapter_search_words_text_id_project_id_fkey FOREIGN KEY (text_id, project_id)
        REFERENCES public.chapter_search_texts(id, project_id) ON DELETE CASCADE
);

CREATE INDEX chapter_search_words_text_idx ON public.chapter_search_words USING btree (text_id);
CREATE INDEX chapter_search_words_word_idx ON public.chapter_search_words USING btree (project_id, word);
CREATE INDEX chapter_search_words_stem_idx ON public.chapter_search_words USING btree (project_id, stem);

REVOKE ALL ON TABLE public.chapter_search_texts FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_search_words FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_search_texts TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.chapter_search_words TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chapter_search_texts TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.chapter_search_words TO canonkeeper_governance_owner;
