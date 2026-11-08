ALTER TABLE public.jobs
    ADD COLUMN codex_thread_id text,
    ADD COLUMN codex_thread_started_at timestamptz,
    ADD COLUMN codex_turn_id text;

ALTER TABLE public.jobs
    ADD CONSTRAINT jobs_codex_thread_id_check CHECK (
        codex_thread_id IS NULL OR (
            kind IN ('canon_book_enrichment', 'writer_agent', 'canon_correction') AND
            codex_thread_id = btrim(codex_thread_id) AND
            char_length(codex_thread_id) BETWEEN 1 AND 255 AND
            codex_thread_id !~ '[[:cntrl:]]'
        )
    ) NOT VALID,
    ADD CONSTRAINT jobs_codex_thread_started_at_check CHECK (
        (codex_thread_id IS NULL) = (codex_thread_started_at IS NULL)
    ) NOT VALID,
    ADD CONSTRAINT jobs_codex_turn_id_check CHECK (
        codex_turn_id IS NULL OR (
            codex_thread_id IS NOT NULL AND
            codex_turn_id = btrim(codex_turn_id) AND
            char_length(codex_turn_id) BETWEEN 1 AND 255 AND
            codex_turn_id !~ '[[:cntrl:]]'
        )
    ) NOT VALID;

DO $ck_native_root_preflight$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM public.jobs AS job
    JOIN public.job_agent_threads AS binding
      ON binding.project_id = job.project_id
     AND binding.job_id = job.id
     AND binding.thread_id IS NOT NULL
     AND (
       (job.kind = 'canon_book_enrichment' AND binding.role IN ('canon_book_enrichment', 'book_enrichment_main'))
       OR (job.kind = 'writer_agent' AND binding.role = 'writer_agent')
       OR (job.kind = 'canon_correction' AND binding.role = 'canon_correction')
     )
    GROUP BY job.project_id, job.id
    HAVING count(DISTINCT binding.thread_id) > 1
  ) THEN
    RAISE EXCEPTION 'conflicting native Codex root bindings'
      USING ERRCODE = '23505';
  END IF;
END;
$ck_native_root_preflight$;

ALTER TABLE public.jobs
    VALIDATE CONSTRAINT jobs_codex_thread_id_check,
    VALIDATE CONSTRAINT jobs_codex_thread_started_at_check,
    VALIDATE CONSTRAINT jobs_codex_turn_id_check;

CREATE UNIQUE INDEX jobs_codex_thread_id_idx
    ON public.jobs (codex_thread_id)
    WHERE codex_thread_id IS NOT NULL;

ALTER TABLE public.jobs
    ADD CONSTRAINT jobs_codex_root_scope_key
    UNIQUE (id, project_id, codex_thread_id);

WITH root_bindings AS (
  SELECT
    job.project_id,
    job.id AS job_id,
    min(binding.thread_id) AS thread_id,
    min(binding.created_at) AS started_at
  FROM public.jobs AS job
  JOIN public.job_agent_threads AS binding
    ON binding.project_id = job.project_id
   AND binding.job_id = job.id
   AND binding.thread_id IS NOT NULL
   AND (
     (job.kind = 'canon_book_enrichment' AND binding.role IN ('canon_book_enrichment', 'book_enrichment_main'))
     OR (job.kind = 'writer_agent' AND binding.role = 'writer_agent')
     OR (job.kind = 'canon_correction' AND binding.role = 'canon_correction')
   )
  GROUP BY job.project_id, job.id
)
UPDATE public.jobs AS job
SET codex_thread_id = root.thread_id,
    codex_thread_started_at = root.started_at
FROM root_bindings AS root
WHERE job.project_id = root.project_id
  AND job.id = root.job_id;

ALTER TABLE public.canon_graph_draft_rows
    DROP CONSTRAINT canon_graph_draft_rows_attempt_fkey;
ALTER TABLE public.canon_graph_draft_review_reads
    DROP CONSTRAINT canon_graph_draft_review_reads_attempt_fkey;

ALTER TABLE public.canon_graph_attempts
    DROP CONSTRAINT canon_graph_attempts_pkey,
    DROP CONSTRAINT canon_graph_attempts_project_id_job_id_key,
    ALTER COLUMN claim_incarnation DROP NOT NULL,
    ALTER COLUMN runtime_session_id DROP NOT NULL,
    ADD CONSTRAINT canon_graph_attempts_pkey PRIMARY KEY (project_id, job_id),
    ADD CONSTRAINT canon_graph_attempts_legacy_identity_key UNIQUE (
        project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id
    );

ALTER TABLE public.canon_graph_draft_rows
    ALTER COLUMN claim_incarnation DROP NOT NULL,
    ALTER COLUMN runtime_session_id DROP NOT NULL,
    ADD CONSTRAINT canon_graph_draft_rows_attempt_fkey FOREIGN KEY (
        project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id
    ) REFERENCES public.canon_graph_attempts (
        project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id
    ) ON DELETE CASCADE NOT VALID,
    ADD CONSTRAINT canon_graph_draft_rows_job_attempt_fkey FOREIGN KEY (project_id, job_id)
    REFERENCES public.canon_graph_attempts (project_id, job_id) ON DELETE CASCADE NOT VALID;

ALTER TABLE public.canon_graph_draft_review_reads
    ALTER COLUMN claim_incarnation DROP NOT NULL,
    ALTER COLUMN runtime_session_id DROP NOT NULL,
    ADD CONSTRAINT canon_graph_draft_review_reads_attempt_fkey FOREIGN KEY (
        project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id
    ) REFERENCES public.canon_graph_attempts (
        project_id, source_view, book_id, job_id, claim_incarnation, runtime_session_id
    ) ON DELETE CASCADE NOT VALID,
    ADD CONSTRAINT canon_graph_draft_review_reads_job_attempt_fkey FOREIGN KEY (project_id, job_id)
    REFERENCES public.canon_graph_attempts (project_id, job_id) ON DELETE CASCADE NOT VALID;

ALTER TABLE public.canon_graph_draft_rows
    VALIDATE CONSTRAINT canon_graph_draft_rows_attempt_fkey,
    VALIDATE CONSTRAINT canon_graph_draft_rows_job_attempt_fkey;
ALTER TABLE public.canon_graph_draft_review_reads
    VALIDATE CONSTRAINT canon_graph_draft_review_reads_attempt_fkey,
    VALIDATE CONSTRAINT canon_graph_draft_review_reads_job_attempt_fkey;

CREATE TABLE public.agent_tool_calls (
    job_id uuid NOT NULL,
    project_id uuid NOT NULL,
    root_thread_id text NOT NULL,
    caller_thread_id text NOT NULL,
    turn_id text NOT NULL,
    call_id text NOT NULL,
    tool_name text NOT NULL,
    request_digest bytea NOT NULL,
    result_payload bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, root_thread_id, caller_thread_id, turn_id, call_id),
    CONSTRAINT agent_tool_calls_job_root_fkey FOREIGN KEY (job_id, project_id, root_thread_id)
      REFERENCES public.jobs (id, project_id, codex_thread_id) ON DELETE CASCADE,
    CONSTRAINT agent_tool_calls_identity_check CHECK (
      root_thread_id = btrim(root_thread_id) AND caller_thread_id = btrim(caller_thread_id) AND
      turn_id = btrim(turn_id) AND call_id = btrim(call_id) AND
      char_length(root_thread_id) BETWEEN 1 AND 255 AND
      char_length(caller_thread_id) BETWEEN 1 AND 255 AND
      char_length(turn_id) BETWEEN 1 AND 255 AND
      char_length(call_id) BETWEEN 1 AND 255 AND
      root_thread_id !~ '[[:cntrl:]]' AND caller_thread_id !~ '[[:cntrl:]]' AND
      turn_id !~ '[[:cntrl:]]' AND call_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT agent_tool_calls_tool_name_check CHECK (
      tool_name = btrim(tool_name) AND char_length(tool_name) BETWEEN 1 AND 255 AND
      tool_name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT agent_tool_calls_request_digest_check CHECK (
      octet_length(request_digest) = 32 AND request_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT agent_tool_calls_result_payload_check CHECK (octet_length(result_payload) >= 1)
);

CREATE TABLE public.agent_tool_refs (
    job_id uuid NOT NULL,
    project_id uuid NOT NULL,
    ref text NOT NULL,
    target_kind text NOT NULL,
    target_id uuid NOT NULL,
    revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, job_id, ref),
    CONSTRAINT agent_tool_refs_job_fkey FOREIGN KEY (job_id, project_id)
      REFERENCES public.jobs (id, project_id) ON DELETE CASCADE,
    CONSTRAINT agent_tool_refs_ref_check CHECK (
      ref = btrim(ref) AND char_length(ref) BETWEEN 1 AND 32 AND ref !~ '[[:cntrl:]]'
    ),
    CONSTRAINT agent_tool_refs_target_kind_check CHECK (
      target_kind = btrim(target_kind) AND char_length(target_kind) BETWEEN 1 AND 64 AND target_kind !~ '[[:cntrl:]]'
    ),
    CONSTRAINT agent_tool_refs_revision_check CHECK (revision >= 1)
);

REVOKE ALL ON TABLE public.agent_tool_calls FROM PUBLIC;
REVOKE ALL ON TABLE public.agent_tool_refs FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.agent_tool_calls TO canonkeeper_product_runtime;
GRANT SELECT, INSERT, DELETE ON TABLE public.agent_tool_refs TO canonkeeper_product_runtime;
GRANT UPDATE (result_payload) ON TABLE public.agent_tool_calls TO canonkeeper_product_runtime;
GRANT UPDATE (ref) ON TABLE public.agent_tool_refs TO canonkeeper_product_runtime;
