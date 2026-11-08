ALTER TABLE public.chat_turns
    ADD COLUMN provider_turn_id text;

CREATE UNIQUE INDEX chat_turns_provider_turn_idx
    ON public.chat_turns (project_id, chat_id, provider_turn_id)
    WHERE provider_turn_id IS NOT NULL;

ALTER TABLE public.chat_turns
    ADD CONSTRAINT chat_turns_provider_turn_id_check CHECK (
        provider_turn_id IS NULL OR (
            provider_turn_id = btrim(provider_turn_id) AND
            char_length(provider_turn_id) BETWEEN 1 AND 255 AND
            provider_turn_id !~ '[[:cntrl:]]'
        )
    );

REVOKE ALL ON TABLE public.chat_turns FROM PUBLIC;
GRANT SELECT, UPDATE ON TABLE public.chat_turns TO canonkeeper_product_runtime;
