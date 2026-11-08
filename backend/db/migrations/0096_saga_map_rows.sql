-- The prepared saga map (specs/2026-11-04-saga-map.md, UC10): the graph, the card of every entry for the whole saga and its parts
-- along the scale, built by the last step of the cards Job and read by key, so no request reads the cards of a saga. Derived data:
-- any source deletion removes it in its fence, and the next cards Job builds it again. The bodies are gzip of JSON, so PostgreSQL
-- keeps them as they are.
CREATE TABLE public.saga_map_graphs (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    revision text NOT NULL,
    document bytea NOT NULL,
    chapters bytea NOT NULL,
    built_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT saga_map_graphs_pkey PRIMARY KEY (project_id, source_view),
    CONSTRAINT saga_map_graphs_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_map_graphs_source_view_check CHECK (source_view IN ('draft', 'published')),
    CONSTRAINT saga_map_graphs_revision_check CHECK (char_length(revision) BETWEEN 1 AND 128)
);
ALTER TABLE public.saga_map_graphs
    ALTER COLUMN document SET STORAGE EXTERNAL,
    ALTER COLUMN chapters SET STORAGE EXTERNAL;

CREATE TABLE public.saga_map_nodes (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    node_key text NOT NULL,
    card bytea NOT NULL,
    CONSTRAINT saga_map_nodes_pkey PRIMARY KEY (project_id, source_view, node_key),
    CONSTRAINT saga_map_nodes_graph_fkey FOREIGN KEY (project_id, source_view)
        REFERENCES public.saga_map_graphs(project_id, source_view) ON DELETE CASCADE
);
ALTER TABLE public.saga_map_nodes
    ALTER COLUMN card SET STORAGE EXTERNAL;

-- What the card of a node needs for the chapters from first_chapter up to the next part.
CREATE TABLE public.saga_map_node_parts (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    node_key text NOT NULL,
    first_chapter integer NOT NULL,
    body bytea NOT NULL,
    CONSTRAINT saga_map_node_parts_pkey PRIMARY KEY (project_id, source_view, node_key, first_chapter),
    CONSTRAINT saga_map_node_parts_node_fkey FOREIGN KEY (project_id, source_view, node_key)
        REFERENCES public.saga_map_nodes(project_id, source_view, node_key) ON DELETE CASCADE
);
ALTER TABLE public.saga_map_node_parts
    ALTER COLUMN body SET STORAGE EXTERNAL;

CREATE TABLE public.saga_map_names (
    project_id uuid NOT NULL,
    source_view text NOT NULL,
    name_key text NOT NULL,
    node_key text NOT NULL,
    CONSTRAINT saga_map_names_pkey PRIMARY KEY (project_id, source_view, name_key),
    CONSTRAINT saga_map_names_node_fkey FOREIGN KEY (project_id, source_view, node_key)
        REFERENCES public.saga_map_nodes(project_id, source_view, node_key) ON DELETE CASCADE
);
CREATE INDEX saga_map_names_node_idx ON public.saga_map_names (project_id, source_view, node_key);

REVOKE ALL ON TABLE public.saga_map_graphs FROM PUBLIC;
REVOKE ALL ON TABLE public.saga_map_nodes FROM PUBLIC;
REVOKE ALL ON TABLE public.saga_map_node_parts FROM PUBLIC;
REVOKE ALL ON TABLE public.saga_map_names FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_map_graphs TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_map_nodes TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_map_node_parts TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_map_names TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_map_graphs TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.saga_map_nodes TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.saga_map_node_parts TO canonkeeper_governance_owner;
GRANT SELECT ON TABLE public.saga_map_names TO canonkeeper_governance_owner;
