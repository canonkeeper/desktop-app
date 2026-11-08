CREATE TABLE public.agent_timeline_bindings (
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    provider_thread_id text NOT NULL,
    provider_turn_id text NOT NULL,
    source_role text NOT NULL,
    turn_index integer NOT NULL,
    provider_turn_started_at timestamptz,
    high_water_sequence bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, provider_thread_id, provider_turn_id),
    CONSTRAINT agent_timeline_bindings_indexes_check CHECK (turn_index >= 0 AND high_water_sequence >= 0),
    CONSTRAINT agent_timeline_bindings_role_check CHECK (source_role IN ('main', 'completeness', 'source_evidence', 'saga_history')),
    CONSTRAINT agent_timeline_bindings_job_fkey FOREIGN KEY (job_id)
      REFERENCES public.jobs (id) ON DELETE CASCADE
);

CREATE TABLE public.agent_timeline_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    event_id text NOT NULL,
    provider_thread_id text NOT NULL,
    provider_turn_id text NOT NULL,
    turn_index integer NOT NULL,
    provider_item_id text,
    item_index integer,
    logical_section integer NOT NULL,
    event_kind text NOT NULL,
    provider_turn_started_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (project_id, job_id, event_id),
    CONSTRAINT agent_timeline_events_indexes_check CHECK (
      turn_index >= 0 AND logical_section >= 0 AND
      (item_index IS NULL OR item_index >= 0) AND
      (provider_item_id IS NULL OR item_index IS NOT NULL)
    ),
    CONSTRAINT agent_timeline_events_kind_check CHECK (
      event_kind IN ('run_started', 'user', 'reasoning', 'assistant', 'tool_request',
                     'tool_result', 'plan', 'turn_outcome', 'reviewer', 'graph', 'job_outcome')
    ),
    CONSTRAINT agent_timeline_events_binding_fkey FOREIGN KEY (project_id, provider_thread_id, provider_turn_id)
      REFERENCES public.agent_timeline_bindings (project_id, provider_thread_id, provider_turn_id) ON DELETE CASCADE
);

CREATE INDEX agent_timeline_events_job_sequence_idx
    ON public.agent_timeline_events (project_id, job_id, sequence);

REVOKE ALL ON TABLE public.agent_timeline_bindings FROM PUBLIC;
REVOKE ALL ON TABLE public.agent_timeline_events FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON TABLE public.agent_timeline_bindings TO canonkeeper_product_runtime;
GRANT SELECT, INSERT ON TABLE public.agent_timeline_events TO canonkeeper_product_runtime;
