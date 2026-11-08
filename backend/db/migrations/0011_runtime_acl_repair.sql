-- Repair the exact product-runtime ACLs required by the current schema.

GRANT UPDATE ON TABLE public.canon_graph_draft_review_reads TO canonkeeper_product_runtime;

REVOKE ALL ON FUNCTION public.ck_canon_graph_handle_bindings_valid(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_canon_graph_handle_bindings_valid(jsonb) TO canonkeeper_product_runtime;

REVOKE ALL ON SEQUENCE public.agent_timeline_events_sequence_seq FROM PUBLIC;
GRANT USAGE, SELECT ON SEQUENCE public.agent_timeline_events_sequence_seq TO canonkeeper_product_runtime;
