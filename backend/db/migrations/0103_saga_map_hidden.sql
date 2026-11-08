-- What the author took off the map of a project (specs/2026-11-04-saga-map.md, R11.3): a node, or the link of two nodes
-- when other_key is set. It is read together with the prepared rows of the map, which it never changes, so a decision shows
-- at once and outlives their rebuilds. The names are what the list «Убранное» prints.
CREATE TABLE public.saga_map_hidden (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    node_key text NOT NULL,
    other_key text DEFAULT ''::text NOT NULL,
    node_name text NOT NULL,
    other_name text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT saga_map_hidden_pkey PRIMARY KEY (id),
    CONSTRAINT saga_map_hidden_project_item_key UNIQUE (project_id, node_key, other_key),
    CONSTRAINT saga_map_hidden_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT saga_map_hidden_node_key_check CHECK (char_length(node_key) BETWEEN 1 AND 400),
    CONSTRAINT saga_map_hidden_other_key_check CHECK (char_length(other_key) <= 400 AND other_key <> node_key),
    CONSTRAINT saga_map_hidden_node_name_check CHECK (char_length(node_name) BETWEEN 1 AND 400),
    CONSTRAINT saga_map_hidden_other_name_check CHECK (char_length(other_name) <= 400)
);

REVOKE ALL ON TABLE public.saga_map_hidden FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_map_hidden TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_map_hidden TO canonkeeper_governance_owner;
