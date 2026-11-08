-- The blind comparison of texts on the public page /compare/:slug (specs/2026-11-05-reading-test.md).
-- A test has no link to projects, books or accounts: its texts are copied into its row when it is
-- loaded. Of a voter the votes keep only a hash: no address, cookie, name or mail. Both tables go only
-- by the operator's `reading-test delete`.
CREATE TABLE public.reading_tests (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    pairs jsonb NOT NULL,
    closed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reading_tests_pkey PRIMARY KEY (id),
    CONSTRAINT reading_tests_slug_key UNIQUE (slug),
    CONSTRAINT reading_tests_slug_check CHECK (slug ~ '^[A-Za-z0-9_-]{16,64}$'),
    CONSTRAINT reading_tests_pairs_check CHECK (jsonb_typeof(pairs) = 'array')
);

CREATE TABLE public.reading_test_votes (
    test_id uuid NOT NULL,
    pair_number integer NOT NULL,
    voter_hash text NOT NULL,
    chosen text NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reading_test_votes_pkey PRIMARY KEY (test_id, pair_number, voter_hash),
    CONSTRAINT reading_test_votes_test_id_fkey FOREIGN KEY (test_id)
        REFERENCES public.reading_tests(id) ON DELETE CASCADE,
    CONSTRAINT reading_test_votes_voter_hash_check CHECK (voter_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT reading_test_votes_chosen_check CHECK (char_length(chosen) BETWEEN 1 AND 32),
    CONSTRAINT reading_test_votes_note_check CHECK (char_length(note) <= 500)
);

REVOKE ALL ON TABLE public.reading_tests FROM PUBLIC;
REVOKE ALL ON TABLE public.reading_test_votes FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.reading_tests TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.reading_test_votes TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.reading_tests TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.reading_test_votes TO canonkeeper_governance_owner;
