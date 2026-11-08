-- Successful Chat reads retain bounded display facts on their existing issued refs.
ALTER TABLE public.agent_tool_refs
    ADD COLUMN reference_snapshot jsonb,
    ADD CONSTRAINT agent_tool_refs_reference_snapshot_check CHECK (
        reference_snapshot IS NULL OR (
            jsonb_typeof(reference_snapshot) = 'object' AND (
                (target_kind = 'chapter'
                 AND reference_snapshot ? 'source'
                 AND jsonb_typeof(reference_snapshot->'source') = 'object'
                 AND reference_snapshot - 'source' = '{}'::jsonb)
                OR
                (target_kind IN ('canon_author', 'canon_graph')
                 AND reference_snapshot ? 'canon'
                 AND jsonb_typeof(reference_snapshot->'canon') = 'object'
                 AND reference_snapshot - 'canon' = '{}'::jsonb)
            )
        )
    );

REVOKE UPDATE (reference_snapshot) ON TABLE public.agent_tool_refs FROM PUBLIC;
GRANT UPDATE (reference_snapshot) ON TABLE public.agent_tool_refs TO canonkeeper_product_runtime;
