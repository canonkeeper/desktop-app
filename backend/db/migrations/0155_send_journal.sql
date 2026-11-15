-- The journal of what went out (specs/2026-11-14-desk-journal.md JR1.8): the local gateway of the desktop app writes the
-- record of every sending before its body goes. A record names its number — the number of the row of its request too —,
-- its time, the saga of its Job, the Job, the thread of Codex, the number of the check of a probe, its purpose, the profile
-- and the host it goes to in fact, the bytes of its body and what of the saga it gives that went with none before it —
-- places by their ids, never a word of the text. model_disclosures of 0114 never had a writer and has no row anywhere: its
-- columns become the record's. The journal is written once and goes when the author clears it or the saga is purged.
DROP INDEX public.model_disclosures_project_at_idx;
DROP INDEX public.model_disclosures_job_idx;
ALTER TABLE public.model_disclosures
    DROP CONSTRAINT model_disclosures_purpose_check,
    DROP CONSTRAINT model_disclosures_job_check,
    DROP CONSTRAINT model_disclosures_client_check,
    DROP CONSTRAINT model_disclosures_what_check,
    DROP CONSTRAINT model_disclosures_chapter_check,
    DROP CONSTRAINT model_disclosures_chapter_key_check,
    DROP CONSTRAINT model_disclosures_attachment_number_check,
    DROP CONSTRAINT model_disclosures_paragraphs_check,
    DROP CONSTRAINT model_disclosures_items_check,
    DROP COLUMN client,
    DROP COLUMN chapter_id,
    DROP COLUMN chapter_key,
    DROP COLUMN attachment_number,
    DROP COLUMN paragraphs,
    DROP COLUMN what,
    DROP COLUMN items,
    ALTER COLUMN id DROP DEFAULT,
    ALTER COLUMN project_id DROP NOT NULL,
    ADD COLUMN thread_id text,
    ADD COLUMN check_id uuid,
    ADD COLUMN profile_name text NOT NULL,
    ADD COLUMN provider_host text NOT NULL,
    ADD COLUMN request_bytes bigint NOT NULL,
    ADD COLUMN named jsonb NOT NULL,
    ADD COLUMN first_time boolean NOT NULL,
    ADD CONSTRAINT model_disclosures_purpose_check CHECK (purpose IN (
        'chat', 'probe', 'card', 'card_pass', 'names', 'layer_book', 'layer_reduce', 'layer_verify', 'layer_gap', 'scan',
        'materials'
    )),
    -- A probe has a number of its check and no saga or Job; nothing else has a check.
    ADD CONSTRAINT model_disclosures_check_check CHECK (
        (purpose = 'probe') = (check_id IS NOT NULL) AND (purpose <> 'probe' OR (project_id IS NULL AND job_id IS NULL))
    ),
    ADD CONSTRAINT model_disclosures_thread_id_check CHECK (thread_id IS NULL OR char_length(thread_id) BETWEEN 1 AND 200),
    ADD CONSTRAINT model_disclosures_profile_name_check CHECK (char_length(profile_name) BETWEEN 1 AND 100),
    ADD CONSTRAINT model_disclosures_provider_host_check CHECK (char_length(provider_host) BETWEEN 1 AND 255),
    ADD CONSTRAINT model_disclosures_request_bytes_check CHECK (request_bytes >= 0),
    ADD CONSTRAINT model_disclosures_named_check CHECK (jsonb_typeof(named) = 'object');

-- The rows of the page go by days from the newest: by time, by the purposes of a kind, by Job and by check; the saga goes
-- by its own.
CREATE INDEX model_disclosures_at_idx ON public.model_disclosures (at);
CREATE INDEX model_disclosures_purpose_at_idx ON public.model_disclosures (purpose, at);
CREATE INDEX model_disclosures_job_at_idx ON public.model_disclosures (job_id, at) WHERE job_id IS NOT NULL;
CREATE INDEX model_disclosures_check_at_idx ON public.model_disclosures (check_id, at) WHERE check_id IS NOT NULL;
CREATE INDEX model_disclosures_project_idx ON public.model_disclosures (project_id) WHERE project_id IS NOT NULL;

REVOKE ALL ON TABLE public.model_disclosures FROM PUBLIC;

-- What a tool of a turn gave Codex and its thread has not sent yet (JR1.4): the record of the next request of the thread
-- takes it in its transaction, in whatever turn and after a restart; a correction Codex refused takes its row back. The
-- clearing of the journal keeps it.
CREATE TABLE public.model_disclosures_pending (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    at timestamp with time zone NOT NULL,
    thread_id text NOT NULL,
    project_id uuid NOT NULL,
    named jsonb NOT NULL,
    CONSTRAINT model_disclosures_pending_pkey PRIMARY KEY (id),
    CONSTRAINT model_disclosures_pending_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT model_disclosures_pending_thread_id_check CHECK (char_length(thread_id) BETWEEN 1 AND 200),
    CONSTRAINT model_disclosures_pending_named_check CHECK (jsonb_typeof(named) = 'object')
);

CREATE INDEX model_disclosures_pending_thread_idx ON public.model_disclosures_pending (thread_id, at);
CREATE INDEX model_disclosures_pending_project_idx ON public.model_disclosures_pending (project_id);

REVOKE ALL ON TABLE public.model_disclosures_pending FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.model_disclosures_pending TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.model_disclosures_pending TO canonkeeper_governance_owner;

-- The helpers a thread of Codex went to (JR1.5): the first sending of a thread to one it never went to, while it went to
-- another, carries «весь прежний разговор — впервые этому получателю». The clearing of the journal keeps it.
CREATE TABLE public.model_thread_recipients (
    thread_id text NOT NULL,
    profile_name text NOT NULL,
    provider_host text NOT NULL,
    project_id uuid NOT NULL,
    at timestamp with time zone NOT NULL,
    CONSTRAINT model_thread_recipients_pkey PRIMARY KEY (thread_id, profile_name, provider_host),
    CONSTRAINT model_thread_recipients_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT model_thread_recipients_thread_id_check CHECK (char_length(thread_id) BETWEEN 1 AND 200),
    CONSTRAINT model_thread_recipients_profile_name_check CHECK (char_length(profile_name) BETWEEN 1 AND 100),
    CONSTRAINT model_thread_recipients_provider_host_check CHECK (char_length(provider_host) BETWEEN 1 AND 255)
);

CREATE INDEX model_thread_recipients_project_idx ON public.model_thread_recipients (project_id);

REVOKE ALL ON TABLE public.model_thread_recipients FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.model_thread_recipients TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.model_thread_recipients TO canonkeeper_governance_owner;

-- The requests of the sample of three chapters (JR2.6): the step that writes the count of the consent and the row of the
-- request in one transaction writes them here, and getReadingSample and the estimate «по пробе» read them — the clearing of
-- the journal does not touch them. The requests of the samples the journal kept so far come over.
CREATE TABLE public.reading_sample_requests (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    job_id uuid NOT NULL,
    at timestamp with time zone NOT NULL,
    duration_ms integer NOT NULL,
    provider_host text NOT NULL,
    model text NOT NULL,
    input_tokens bigint,
    cached_input_tokens bigint,
    output_tokens bigint,
    cost_micros bigint,
    currency text,
    estimated boolean NOT NULL,
    CONSTRAINT reading_sample_requests_pkey PRIMARY KEY (id),
    CONSTRAINT reading_sample_requests_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT reading_sample_requests_duration_ms_check CHECK (duration_ms >= 0),
    CONSTRAINT reading_sample_requests_provider_host_check CHECK (char_length(provider_host) BETWEEN 1 AND 255),
    CONSTRAINT reading_sample_requests_model_check CHECK (char_length(model) BETWEEN 1 AND 200),
    CONSTRAINT reading_sample_requests_tokens_check CHECK (
        (input_tokens IS NULL OR input_tokens >= 0) AND (cached_input_tokens IS NULL OR cached_input_tokens >= 0)
        AND (output_tokens IS NULL OR output_tokens >= 0)
    ),
    CONSTRAINT reading_sample_requests_cost_check CHECK (
        (cost_micros IS NULL) = (currency IS NULL) AND (cost_micros IS NULL OR cost_micros >= 0)
        AND (currency IS NULL OR currency IN ('RUB', 'USD', 'CNY', 'EUR'))
    )
);

CREATE INDEX reading_sample_requests_job_idx ON public.reading_sample_requests (job_id, at);
CREATE INDEX reading_sample_requests_project_idx ON public.reading_sample_requests (project_id);

INSERT INTO public.reading_sample_requests (
    id, project_id, job_id, at, duration_ms, provider_host, model, input_tokens, cached_input_tokens, output_tokens,
    cost_micros, currency, estimated
)
SELECT request.id, job.project_id, job.id, request.at, request.duration_ms, request.provider_host, request.model,
    request.input_tokens, request.cached_input_tokens, request.output_tokens, request.cost_micros, request.currency,
    request.estimated
FROM public.model_requests AS request
JOIN public.jobs AS job ON job.id = request.job_id
WHERE job.kind = 'chapter_cards' AND job.subject_json ? 'sample' AND request.purpose IN ('card', 'card_pass');

REVOKE ALL ON TABLE public.reading_sample_requests FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.reading_sample_requests TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.reading_sample_requests TO canonkeeper_governance_owner;

-- A row of a request carries the number of the record of its sending as its own (its id, given by the gateway), the
-- number of the check of a probe, and how a failed request failed — the helper refused it before its answer, no answer came
-- for the connection, or the answer broke off once it began (JR2.4). A row written before this has neither.
ALTER TABLE public.model_requests
    ADD COLUMN check_id uuid,
    ADD COLUMN failure text,
    ADD CONSTRAINT model_requests_check_id_check CHECK (purpose = 'probe' OR check_id IS NULL),
    ADD CONSTRAINT model_requests_failure_check CHECK (failure IS NULL OR failure IN ('refused', 'connection', 'torn', 'unsent'));
REVOKE ALL ON TABLE public.model_requests FROM PUBLIC;
