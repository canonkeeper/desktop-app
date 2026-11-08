ALTER TABLE public.job_agent_threads
    ALTER COLUMN thread_id DROP NOT NULL;

ALTER TABLE public.job_agent_threads
    ADD COLUMN IF NOT EXISTS launch_intent text,
    ADD COLUMN IF NOT EXISTS issuer_id uuid,
    ADD COLUMN IF NOT EXISTS issuer_incarnation bigint,
    ADD COLUMN IF NOT EXISTS fence_state text NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS launch_receipt jsonb,
    ADD COLUMN IF NOT EXISTS reconciled_at timestamptz;

CREATE UNIQUE INDEX IF NOT EXISTS job_agent_threads_launch_intent_idx
    ON public.job_agent_threads (launch_intent)
    WHERE launch_intent IS NOT NULL;

ALTER TABLE public.job_agent_threads
    DROP CONSTRAINT IF EXISTS job_agent_threads_fence_state_check;
ALTER TABLE public.job_agent_threads
    ADD CONSTRAINT job_agent_threads_fence_state_check
    CHECK (fence_state IN ('pending', 'bound', 'reconciled', 'fenced'));

ALTER TABLE public.job_agent_threads
    DROP CONSTRAINT IF EXISTS job_agent_threads_role_check;
ALTER TABLE public.job_agent_threads
    ADD CONSTRAINT job_agent_threads_role_check
    CHECK (role IN ('canon_book_enrichment', 'book_enrichment_main',
                    'book_enrichment_review_completeness',
                    'book_enrichment_review_source_citations',
                    'book_enrichment_review_saga_history',
                    'writer_agent', 'canon_correction'));

CREATE TABLE public.runtime_initial_turn_requests (
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    role text NOT NULL,
    claim_incarnation bigint NOT NULL,
    runtime_session_id uuid NOT NULL,
    launch_intent text NOT NULL,
    assigned_input_digest bytea NOT NULL,
    client_user_message_id text NOT NULL,
    issuer_id uuid NOT NULL,
    issuer_incarnation bigint NOT NULL,
    provider_turn_id text,
    reconciliation_receipt jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, job_id, role),
    CONSTRAINT runtime_initial_turn_requests_digest_check CHECK (octet_length(assigned_input_digest) = 32),
    CONSTRAINT runtime_initial_turn_requests_incarnations_check CHECK (claim_incarnation >= 1 AND issuer_incarnation >= 1)
);

CREATE UNIQUE INDEX runtime_initial_turn_requests_client_id_idx
    ON public.runtime_initial_turn_requests (project_id, client_user_message_id);

CREATE TABLE public.agent_session_refs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    runtime_session_id uuid NOT NULL,
    role text NOT NULL,
    ref_kind text NOT NULL,
    token text NOT NULL,
    target jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (id),
    UNIQUE (runtime_session_id, ref_kind, token),
    CONSTRAINT agent_session_refs_kind_check CHECK (ref_kind IN ('source', 'canon_row', 'draft_row', 'writer_assignment'))
);

CREATE TABLE public.agent_session_replays (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    runtime_session_id uuid NOT NULL,
    namespace text NOT NULL,
    tool text NOT NULL,
    operation text NOT NULL,
    request_digest bytea NOT NULL,
    result_json jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (id),
    UNIQUE (runtime_session_id, namespace, tool, operation),
    CONSTRAINT agent_session_replays_digest_check CHECK (octet_length(request_digest) = 32)
);

ALTER TABLE public.runtime_initial_turn_requests
    ADD CONSTRAINT runtime_initial_turn_requests_runtime_session_fkey
    FOREIGN KEY (runtime_session_id, project_id)
    REFERENCES public.runtime_sessions (id, project_id)
    ON DELETE CASCADE;

ALTER TABLE public.agent_session_refs
    ADD CONSTRAINT agent_session_refs_runtime_session_fkey
    FOREIGN KEY (runtime_session_id, project_id)
    REFERENCES public.runtime_sessions (id, project_id)
    ON DELETE CASCADE;

ALTER TABLE public.agent_session_replays
    ADD CONSTRAINT agent_session_replays_runtime_session_fkey
    FOREIGN KEY (runtime_session_id, project_id)
    REFERENCES public.runtime_sessions (id, project_id)
    ON DELETE CASCADE;

REVOKE ALL ON TABLE public.runtime_initial_turn_requests FROM PUBLIC;
REVOKE ALL ON TABLE public.agent_session_refs FROM PUBLIC;
REVOKE ALL ON TABLE public.agent_session_replays FROM PUBLIC;

GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.runtime_initial_turn_requests
    TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, DELETE ON TABLE public.agent_session_refs
    TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, DELETE ON TABLE public.agent_session_replays
    TO canonkeeper_product_runtime;
