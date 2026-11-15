-- The helper «Аккаунт CanonKeeper» of the desktop app: the chat and the reading of the books on the models of the site by
-- the tier (specs/2026-11-13-desk-account-ai.md).
--
-- AI1.4, AI2.2, AI2.3, AI2.9: the work of the app a site counts — the source a turn or an upload of materials took at its
-- start, kept with its Job and serving it to its end whatever the settings become; the number of the permit of the Job —
-- the ref is derived from the Job and the number, and the id of a permit lives only in the memory of the process —; and
-- whether the unit of the question of a turn came back, written once the permit is closed; the mark of the link the work
-- started under — a SHA-256 of the salt of the data directory and the pass, never the pass — since the site knows the refs
-- of one device only, and a work of another link is never asked by its ref. A Job of any status before this migration has
-- no row: its work is counted by nobody but the computer, as before.
CREATE TABLE public.app_work (
    job_id uuid NOT NULL,
    kind text NOT NULL,
    source text NOT NULL,
    link_mark text,
    permit_number integer NOT NULL DEFAULT 0,
    returned text,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT app_work_pkey PRIMARY KEY (job_id),
    CONSTRAINT app_work_job_id_fkey FOREIGN KEY (job_id) REFERENCES public.jobs(id) ON DELETE CASCADE,
    CONSTRAINT app_work_kind_check CHECK (kind IN ('chat_turn', 'materials', 'reading')),
    CONSTRAINT app_work_source_check CHECK (source IN ('own', 'account')),
    CONSTRAINT app_work_link_mark_check CHECK (
      (source = 'own' AND link_mark IS NULL) OR (source = 'account' AND link_mark ~ '^[0-9a-f]{64}$')
    ),
    CONSTRAINT app_work_permit_number_check CHECK (permit_number >= 0),
    CONSTRAINT app_work_returned_check CHECK (returned IS NULL OR returned IN ('yes', 'no', 'pending'))
);
REVOKE ALL ON TABLE public.app_work FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.app_work TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.app_work TO canonkeeper_governance_owner;

-- AI1.7, AI4.2: a row of the journal of a request of the profile account names the code of the refusal of the site — never
-- its words, a body or a header.
ALTER TABLE public.model_requests
    ADD COLUMN refusal_code text,
    ADD CONSTRAINT model_requests_refusal_code_check CHECK (
      refusal_code IS NULL OR (char_length(refusal_code) BETWEEN 3 AND 100 AND refusal_code ~ '^[a-z_]+(\.[a-z_]+)+$')
    );

-- AI3.4, AI3.5: the consent of the reading of a saga given to the helper of the account — the source account, the host of
-- the site, no money and no stop sum — and its pauses: the characters of the tier, the fence of the work of the reading, an
-- app the site does not take, the reading of the site unavailable; no connection and the author's pause are the same as
-- those of «Свой ИИ».
ALTER TABLE public.card_build_permissions
    DROP CONSTRAINT card_build_permissions_source_check,
    DROP CONSTRAINT card_build_permissions_pause_check,
    ADD CONSTRAINT card_build_permissions_source_check CHECK (source IN ('own', 'account')),
    ADD CONSTRAINT card_build_permissions_pause_check CHECK (
      (state = 'allowed' AND pause IS NULL)
      OR (state = 'paused' AND pause IN (
        'author', 'sum', 'funds', 'key', 'model', 'connection', 'prices', 'characters', 'reading_fence', 'unfit', 'unavailable'
      ))
    );
