-- The last update of a book from a file, kept 7 days so the author can return the book to the version before it
-- (specs/2026-11-12-ux10-book-revert.md BR1): one row a book, which a new update replaces, and a row for every chapter the
-- update met — what it did to it, the revision and lifecycle generation it left, and what the chapter was before it: its
-- revision, title and place, and for a chapter whose words stayed the italic and bold of its paragraphs. The words of the
-- chapters are not copied: the earlier revisions of their paragraphs keep them. The rows go after their term (retention),
-- with the book and the saga in their deletions, and after the return; the export of the author's data does not read them.
CREATE TABLE public.book_update_snapshots (
    book_id uuid NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid,
    updated_at timestamp with time zone NOT NULL,
    purge_after timestamp with time zone NOT NULL,
    CONSTRAINT book_update_snapshots_pkey PRIMARY KEY (book_id),
    CONSTRAINT book_update_snapshots_book_id_fkey FOREIGN KEY (book_id)
        REFERENCES public.books(id) ON DELETE CASCADE,
    CONSTRAINT book_update_snapshots_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT book_update_snapshots_job_id_fkey FOREIGN KEY (job_id)
        REFERENCES public.jobs(id) ON DELETE SET NULL,
    CONSTRAINT book_update_snapshots_term_check CHECK (purge_after > updated_at)
);

CREATE INDEX book_update_snapshots_project_idx ON public.book_update_snapshots USING btree (project_id);
CREATE INDEX book_update_snapshots_purge_after_idx ON public.book_update_snapshots USING btree (purge_after);
CREATE INDEX book_update_snapshots_job_idx ON public.book_update_snapshots USING btree (job_id) WHERE (job_id IS NOT NULL);

CREATE TABLE public.book_update_snapshot_chapters (
    book_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    change text NOT NULL,
    revision bigint NOT NULL,
    generation bigint NOT NULL,
    previous_revision bigint,
    previous_title text,
    previous_order integer,
    previous_markdown text[],
    CONSTRAINT book_update_snapshot_chapters_pkey PRIMARY KEY (book_id, chapter_id),
    CONSTRAINT book_update_snapshot_chapters_book_id_fkey FOREIGN KEY (book_id)
        REFERENCES public.book_update_snapshots(book_id) ON DELETE CASCADE,
    CONSTRAINT book_update_snapshot_chapters_chapter_id_fkey FOREIGN KEY (chapter_id)
        REFERENCES public.chapters(id) ON DELETE CASCADE,
    CONSTRAINT book_update_snapshot_chapters_change_check
        CHECK (change IN ('words', 'punctuation', 'placed', 'same', 'added', 'removed')),
    CONSTRAINT book_update_snapshot_chapters_revision_check CHECK (revision > 0 AND generation > 0),
    CONSTRAINT book_update_snapshot_chapters_previous_check CHECK (
        (change = 'added' AND previous_revision IS NULL AND previous_title IS NULL AND previous_order IS NULL)
        OR (change <> 'added' AND previous_revision > 0 AND previous_title IS NOT NULL AND previous_order IS NOT NULL)
    ),
    CONSTRAINT book_update_snapshot_chapters_markdown_check CHECK (previous_markdown IS NULL OR change = 'placed')
);

CREATE INDEX book_update_snapshot_chapters_chapter_idx ON public.book_update_snapshot_chapters USING btree (chapter_id);

REVOKE ALL ON TABLE public.book_update_snapshots FROM PUBLIC;
REVOKE ALL ON TABLE public.book_update_snapshot_chapters FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.book_update_snapshots TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.book_update_snapshot_chapters TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.book_update_snapshots TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.book_update_snapshot_chapters TO canonkeeper_governance_owner;
