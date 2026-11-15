-- The journal of the local gateway of «Свой ИИ» (specs/2026-11-13-desk-gateway.md GW1.7): a request of Codex is the chat, a
-- call of the core is the step of the core it gives the words of (backend/internal/domain/coresteps), the probe of the
-- protocol of an address has no saga. The saga of a request is the saga of its Job; a request whose Job nobody named — Codex
-- outside a turn — keeps none. The tokens of the cache of the provider and the size of the request are kept beside the
-- tokens; the text of a request never is. Nobody wrote the table before: the purposes of 0114 go without rows.
ALTER TABLE public.model_requests
    DROP CONSTRAINT model_requests_purpose_check,
    ADD CONSTRAINT model_requests_purpose_check CHECK (purpose IN (
        'chat', 'probe', 'card', 'card_pass', 'names', 'layer_book', 'layer_reduce', 'layer_verify', 'layer_gap', 'scan',
        'materials'
    )),
    DROP CONSTRAINT model_requests_project_check,
    ADD CONSTRAINT model_requests_project_check CHECK (purpose <> 'probe' OR (project_id IS NULL AND job_id IS NULL)),
    ADD COLUMN cached_input_tokens bigint,
    ADD COLUMN request_bytes bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT model_requests_cached_input_tokens_check CHECK (cached_input_tokens IS NULL OR cached_input_tokens >= 0),
    ADD CONSTRAINT model_requests_request_bytes_check CHECK (request_bytes >= 0);
ALTER TABLE public.model_requests ALTER COLUMN request_bytes DROP DEFAULT;
REVOKE ALL ON TABLE public.model_requests FROM PUBLIC;
