ALTER TABLE public.canon_graph_draft_review_reads
    ADD COLUMN IF NOT EXISTS row_version bigint;

ALTER TABLE public.canon_graph_draft_review_reads
    ADD CONSTRAINT canon_graph_draft_review_reads_row_version_check
    CHECK (row_version IS NULL OR row_version >= 1);

REVOKE ALL ON TABLE public.canon_graph_draft_review_reads FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.canon_graph_draft_review_reads TO canonkeeper_product_runtime;
