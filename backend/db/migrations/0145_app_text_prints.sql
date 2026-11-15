-- The models of the site for a linked app (specs/2026-11-13-desk-proxy.md PX3): what the helper of an app read through
-- them, as the SHA-256 of the input of the card of a chapter and its length in characters — never the text. A print new in
-- 30 days adds its characters to the characters of the tier of the account in that window; a print the account knows adds
-- nothing. The rows go with the account.
CREATE TABLE public.app_text_prints (
    user_id uuid NOT NULL,
    print bytea NOT NULL,
    chars integer NOT NULL,
    first_at timestamp with time zone NOT NULL,
    last_at timestamp with time zone NOT NULL,
    CONSTRAINT app_text_prints_pkey PRIMARY KEY (user_id, print),
    CONSTRAINT app_text_prints_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT app_text_prints_print_check CHECK (octet_length(print) = 32),
    CONSTRAINT app_text_prints_chars_check CHECK (chars >= 0),
    CONSTRAINT app_text_prints_times_check CHECK (last_at >= first_at)
);

-- The characters of the window of an account.
CREATE INDEX app_text_prints_user_first_idx ON public.app_text_prints USING btree (user_id, first_at);

REVOKE ALL ON TABLE public.app_text_prints FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.app_text_prints TO canonkeeper_product_runtime;

-- PX3.2, PX3.3: the two fences of the work of apps on the models of the site are limits of the fence of the site — the
-- writer's own value, the platform's, the code's: the tokens of a question of a day (360 000 by the code) and the tokens of
-- the reading of books by a character of the tier (2 by the code). No tier sets them.
ALTER TABLE public.writer_quota_overrides
  ADD COLUMN app_tokens_per_question integer CHECK (app_tokens_per_question >= 0),
  ADD COLUMN app_reading_tokens_per_character integer CHECK (app_reading_tokens_per_character >= 0);
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN app_tokens_per_question integer CHECK (app_tokens_per_question >= 0),
  ADD COLUMN app_reading_tokens_per_character integer CHECK (app_reading_tokens_per_character >= 0);
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;

-- PX3.4: the spend of the work of apps is the spend of the site, by sources of their own and under no project.
ALTER TABLE public.model_usage_days
  DROP CONSTRAINT model_usage_days_source_check,
  ADD CONSTRAINT model_usage_days_source_check
    CHECK (source IN ('chat', 'scan', 'cards', 'images', 'layer', 'draft', 'drawing', 'draft_helper', 'voice', 'author_materials',
      'app_chat', 'app_scan', 'app_cards', 'app_names', 'app_layer', 'app_materials'));

-- The fences sum the spend of apps of an account by its days.
CREATE INDEX model_usage_days_app_idx ON public.model_usage_days USING btree (user_id, day)
  WHERE (source IN ('app_chat', 'app_scan', 'app_cards', 'app_names', 'app_layer', 'app_materials'));
