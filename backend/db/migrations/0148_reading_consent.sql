-- The reading of the books of a saga by the author's own AI (specs/2026-11-13-desk-consent.md).
--
-- CN3.1: the consent of a saga names its source (own; the source «Аккаунт CanonKeeper» adds its own) and the host of the
-- provider the text goes to; a pause has its reason; the stop sum has its unit — the ISO 4217 code of the currency of the
-- prices, its amount in millionths of it, or tokens, in and out — instead of dollars, and the step «ещё на X» is the sum
-- the author named last. The consent keeps its own count of what was spent since its start — the journal may be cleared
-- and a row of it may fail — and the time of its last continuation, from which the pace of the reading is counted. Rows
-- written before this migration were written by tests alone: without a source and a host they do not act, and the reading
-- waits for a new consent; a paused one is paused by the author.
ALTER TABLE public.card_build_permissions
    DROP CONSTRAINT card_build_permissions_one_budget_check,
    DROP CONSTRAINT card_build_permissions_budget_usd_micros_check,
    DROP CONSTRAINT card_build_permissions_budget_tokens_check,
    DROP COLUMN budget_usd_micros,
    DROP COLUMN budget_tokens,
    ADD COLUMN source text,
    ADD COLUMN provider_host text,
    ADD COLUMN pause text,
    ADD COLUMN stop_unit text NOT NULL DEFAULT 'tokens',
    ADD COLUMN stop_amount bigint,
    ADD COLUMN step_amount bigint,
    ADD COLUMN spent_input_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN spent_cached_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN spent_output_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN spent_micros bigint NOT NULL DEFAULT 0,
    ADD COLUMN spent_estimated boolean NOT NULL DEFAULT false,
    ADD COLUMN resumed_at timestamp with time zone;
UPDATE public.card_build_permissions SET pause = 'author' WHERE state = 'paused';
UPDATE public.card_build_permissions SET resumed_at = since;
ALTER TABLE public.card_build_permissions
    ALTER COLUMN stop_unit DROP DEFAULT,
    ALTER COLUMN resumed_at SET NOT NULL,
    ADD CONSTRAINT card_build_permissions_source_check CHECK (source IN ('own')),
    ADD CONSTRAINT card_build_permissions_source_host_check CHECK ((source IS NULL) = (provider_host IS NULL)),
    ADD CONSTRAINT card_build_permissions_provider_host_check CHECK (provider_host IS NULL OR char_length(provider_host) BETWEEN 1 AND 255),
    ADD CONSTRAINT card_build_permissions_pause_check CHECK (
      (state = 'allowed' AND pause IS NULL)
      OR (state = 'paused' AND pause IN ('author', 'sum', 'funds', 'key', 'model', 'connection', 'prices'))
    ),
    ADD CONSTRAINT card_build_permissions_stop_unit_check CHECK (stop_unit IN ('tokens', 'RUB', 'USD', 'CNY', 'EUR')),
    ADD CONSTRAINT card_build_permissions_stop_amount_check CHECK (stop_amount IS NULL OR stop_amount > 0),
    ADD CONSTRAINT card_build_permissions_step_amount_check CHECK (step_amount IS NULL OR step_amount > 0),
    ADD CONSTRAINT card_build_permissions_spent_check CHECK (
      spent_input_tokens >= 0 AND spent_cached_tokens >= 0 AND spent_output_tokens >= 0 AND spent_micros >= 0
    );

-- CN3.3: a row of the journal has the cost of its request in the currency of the prices of its minute, null without them,
-- and says when its tokens or its cost are an estimate — the provider sent no usage.
ALTER TABLE public.model_requests
    ADD COLUMN cost_micros bigint,
    ADD COLUMN currency text,
    ADD COLUMN estimated boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT model_requests_cost_check CHECK ((cost_micros IS NULL) = (currency IS NULL) AND (cost_micros IS NULL OR cost_micros >= 0)),
    ADD CONSTRAINT model_requests_currency_check CHECK (currency IN ('RUB', 'USD', 'CNY', 'EUR'));
REVOKE ALL ON TABLE public.model_requests FROM PUBLIC;

-- CN3.6: a chapter whose card did not come after the paid answers of the model — an answer cut by the limit or one that
-- does not read — for its text and the version of the instructions; the reading does not take it again until its text
-- changes or the author asks to repeat it.
CREATE TABLE public.reading_unread_chapters (
    project_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    content_hash text NOT NULL,
    instructions_version integer NOT NULL,
    reason text NOT NULL,
    marked_at timestamp with time zone NOT NULL,
    CONSTRAINT reading_unread_chapters_pkey PRIMARY KEY (project_id, chapter_id, content_hash, instructions_version),
    CONSTRAINT reading_unread_chapters_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT reading_unread_chapters_reason_check CHECK (reason IN ('cut_answer', 'unreadable')),
    CONSTRAINT reading_unread_chapters_content_hash_check CHECK (char_length(content_hash) BETWEEN 1 AND 200),
    CONSTRAINT reading_unread_chapters_instructions_version_check CHECK (instructions_version > 0)
);
REVOKE ALL ON TABLE public.reading_unread_chapters FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.reading_unread_chapters TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.reading_unread_chapters TO canonkeeper_governance_owner;

-- CN2.2: the sample of three chapters is a cards Job of its own, its chapters in its subject. The cards Job that waits for a
-- text version is one, without the sample — neither takes the other; one sample of a saga is alive at a time. Jobs of any
-- status before this migration have no sample.
DROP INDEX public.jobs_one_queued_chapter_cards_idx;
CREATE UNIQUE INDEX jobs_one_queued_chapter_cards_idx ON public.jobs USING btree (
    project_id, ((subject_json ->> 'sourceView'::text))
) WHERE kind = 'chapter_cards'::text AND status = 'queued'::text
    AND claim_incarnation = 0 AND cancel_requested_at IS NULL AND NOT (subject_json ? 'sample'::text);
CREATE UNIQUE INDEX jobs_one_live_cards_sample_idx ON public.jobs USING btree (project_id)
    WHERE kind = 'chapter_cards'::text AND (subject_json ? 'sample'::text)
    AND status = ANY (ARRAY['queued'::text, 'running'::text, 'canceling'::text]);
