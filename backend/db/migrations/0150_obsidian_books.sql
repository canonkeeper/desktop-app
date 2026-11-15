-- The links of the books of a saga with the folders of its vault of Obsidian (specs/2026-11-14-desk-obsidian-books.md
-- OK0.2): a row for a volume the bridge put into the queue of the volumes — its fingerprint is the SHA-256 of the glued text
-- it uploaded, its choice the paths left out under its folder and the folders of materials inside it — or for a book of the
-- saga linked without a load. What holds a folder is a rule of reading (OK0.3), not a constraint: the book of a volume comes
-- later than its row, the trash and the return of a book are the core's, and no row refers to a table of the core, so the
-- core deletes, restores and updates its books without the bridge. The rows go with the link of the saga with its vault —
-- its removal takes them, a new path of the vault keeps them — and with the saga.
CREATE TABLE public.obsidian_books (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    folder text NOT NULL,
    item_id uuid,
    book_id uuid,
    fingerprint text,
    excluded text[] DEFAULT '{}'::text[] NOT NULL,
    nested text[] DEFAULT '{}'::text[] NOT NULL,
    linked_at timestamp with time zone NOT NULL,
    CONSTRAINT obsidian_books_pkey PRIMARY KEY (id),
    CONSTRAINT obsidian_books_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.obsidian_vaults(project_id) ON DELETE CASCADE,
    CONSTRAINT obsidian_books_source_check CHECK ((item_id IS NULL) <> (book_id IS NULL)),
    CONSTRAINT obsidian_books_fingerprint_check CHECK (
      (item_id IS NULL) = (fingerprint IS NULL) AND (fingerprint IS NULL OR fingerprint ~ '^[0-9a-f]{64}$')
    ),
    CONSTRAINT obsidian_books_folder_check CHECK (char_length(folder) BETWEEN 1 AND 4096 AND folder IS NFC NORMALIZED),
    CONSTRAINT obsidian_books_choice_check CHECK (cardinality(excluded) <= 2000 AND cardinality(nested) <= 200)
);

CREATE INDEX obsidian_books_project_folder_idx ON public.obsidian_books USING btree (project_id, folder);
CREATE INDEX obsidian_books_book_idx ON public.obsidian_books USING btree (project_id, book_id) WHERE (book_id IS NOT NULL);
CREATE UNIQUE INDEX obsidian_books_item_idx ON public.obsidian_books USING btree (project_id, item_id) WHERE (item_id IS NOT NULL);

REVOKE ALL ON TABLE public.obsidian_books FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.obsidian_books TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.obsidian_books TO canonkeeper_governance_owner;
