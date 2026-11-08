-- Owner tools and counters that survive deletion (specs/2026-10-28-owner-tools-and-counters.md).

-- UC2: the counters of a deleted account move to a pseudonym that is no account, so the user
-- row is no longer their parent. A users row is never deleted, the cascade did nothing.
ALTER TABLE public.writer_usage_days DROP CONSTRAINT writer_usage_days_user_id_fkey;
ALTER TABLE public.model_usage_days DROP CONSTRAINT model_usage_days_user_id_fkey;

-- UC1: outcomes of the questions and uploads accepted that Moscow day.
ALTER TABLE public.writer_usage_days
  ADD COLUMN turns_answered integer NOT NULL DEFAULT 0 CHECK (turns_answered >= 0),
  ADD COLUMN turns_failed integer NOT NULL DEFAULT 0 CHECK (turns_failed >= 0),
  ADD COLUMN turns_interrupted integer NOT NULL DEFAULT 0 CHECK (turns_interrupted >= 0),
  ADD COLUMN imports_succeeded integer NOT NULL DEFAULT 0 CHECK (imports_succeeded >= 0),
  ADD COLUMN imports_failed integer NOT NULL DEFAULT 0 CHECK (imports_failed >= 0),
  ADD COLUMN imports_interrupted integer NOT NULL DEFAULT 0 CHECK (imports_interrupted >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;

-- UC1, R1.1–R1.4: a question or an upload that reaches its terminal status counts once for its
-- author in the day it was asked: succeeded — answered, failed — failed, canceled or expired —
-- interrupted. An author blocked meanwhile does not count (R1.2). Every path to a terminal
-- status updates the jobs row, including the claim function of the worker.
-- Lock order: the upsert below locks the author's writer_usage_days row late in the transaction
-- that ends the Job (import finalize already holds the owner's advisory quota lock
-- writer_quota:<owner>). Every transaction takes that quota lock before any writer_usage_days
-- row — uploads check the owner's books before they count the actor's upload — so no
-- transaction waits for the quota lock while it holds a day row.
CREATE FUNCTION public.ck_count_job_outcome() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER
SET search_path TO 'pg_catalog', 'public'
AS $$
DECLARE
  writer uuid := COALESCE(OLD.creator_user_id, NEW.creator_user_id);
  question boolean := OLD.kind = 'research_chat_turn';
  answered boolean := NEW.status = 'succeeded';
  failed boolean := NEW.status = 'failed';
  interrupted boolean := NEW.status IN ('canceled', 'expired');
BEGIN
  IF writer IS NULL OR NOT EXISTS (
    SELECT 1 FROM public.users AS account WHERE account.id = writer AND account.disabled_at IS NULL
  ) THEN
    RETURN NULL;
  END IF;
  INSERT INTO public.writer_usage_days AS usage (
    user_id, day, turns_answered, turns_failed, turns_interrupted,
    imports_succeeded, imports_failed, imports_interrupted
  ) VALUES (
    writer, (OLD.created_at AT TIME ZONE 'Europe/Moscow')::date,
    (question AND answered)::integer, (question AND failed)::integer, (question AND interrupted)::integer,
    (NOT question AND answered)::integer, (NOT question AND failed)::integer, (NOT question AND interrupted)::integer
  )
  ON CONFLICT (user_id, day) DO UPDATE SET
    turns_answered = usage.turns_answered + EXCLUDED.turns_answered,
    turns_failed = usage.turns_failed + EXCLUDED.turns_failed,
    turns_interrupted = usage.turns_interrupted + EXCLUDED.turns_interrupted,
    imports_succeeded = usage.imports_succeeded + EXCLUDED.imports_succeeded,
    imports_failed = usage.imports_failed + EXCLUDED.imports_failed,
    imports_interrupted = usage.imports_interrupted + EXCLUDED.imports_interrupted;
  RETURN NULL;
END;
$$;
ALTER FUNCTION public.ck_count_job_outcome() OWNER TO canonkeeper_governance_owner;
REVOKE ALL ON FUNCTION public.ck_count_job_outcome() FROM PUBLIC;
GRANT SELECT (id, disabled_at) ON public.users TO canonkeeper_governance_owner;
GRANT SELECT, INSERT, UPDATE ON public.writer_usage_days TO canonkeeper_governance_owner;

CREATE TRIGGER jobs_count_outcome
AFTER UPDATE OF status ON public.jobs
FOR EACH ROW
WHEN (
  OLD.status IN ('queued', 'running', 'canceling')
  AND NEW.status IN ('succeeded', 'failed', 'canceled', 'expired')
  AND OLD.kind IN ('research_chat_turn', 'import')
)
EXECUTE FUNCTION public.ck_count_job_outcome();

-- UC2, R2.1: the counters of a deleted account get one new random id that is written nowhere
-- else; its model spend also loses the project (an id that would lead back to the project's
-- tombstone or to its turns in other projects). Returns the rows it moved.
CREATE FUNCTION public.ck_anonymize_writer_usage(account uuid) RETURNS bigint
LANGUAGE plpgsql
SET search_path TO 'pg_catalog', 'public'
AS $$
DECLARE
  pseudonym uuid := gen_random_uuid();
  days bigint;
  spends bigint;
BEGIN
  UPDATE public.writer_usage_days SET user_id = pseudonym WHERE user_id = account;
  GET DIAGNOSTICS days = ROW_COUNT;
  WITH spent AS (
    DELETE FROM public.model_usage_days WHERE user_id = account RETURNING *
  )
  INSERT INTO public.model_usage_days (
    day, user_id, project_id, source, model,
    calls, input_tokens, cached_input_tokens, output_tokens, reasoning_output_tokens
  )
  SELECT spent.day, pseudonym, '00000000-0000-0000-0000-000000000000'::uuid, spent.source, spent.model,
         sum(spent.calls), sum(spent.input_tokens), sum(spent.cached_input_tokens),
         sum(spent.output_tokens), sum(spent.reasoning_output_tokens)
  FROM spent
  GROUP BY spent.day, spent.source, spent.model;
  GET DIAGNOSTICS spends = ROW_COUNT;
  RETURN days + spends;
END;
$$;
REVOKE ALL ON FUNCTION public.ck_anonymize_writer_usage(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_anonymize_writer_usage(uuid) TO canonkeeper_product_runtime;

-- UC3: the owner's days for `stats` and the monitor's morning mail. Addresses in the reserved
-- .test domain (production e2e) are not writers; pseudonyms of deleted accounts are. Model
-- spend is all of it. The row with day NULL is the period: sums, and distinct active writers.
CREATE FUNCTION public.ck_owner_days(first_day date, last_day date)
RETURNS TABLE (
  day date,
  registered bigint, verified bigint, first_import bigint, first_question bigint, active bigint,
  questions bigint, answered bigint, failed bigint, interrupted bigint,
  imports bigint, imports_succeeded bigint, imports_failed bigint, imports_interrupted bigint,
  model_calls bigint, input_tokens bigint, cached_input_tokens bigint, output_tokens bigint
)
LANGUAGE sql STABLE
SET search_path TO 'pg_catalog', 'public'
AS $$
  WITH writer_days AS (
    SELECT usage.*
    FROM public.writer_usage_days AS usage
    LEFT JOIN public.users AS account ON account.id = usage.user_id
    WHERE account.id IS NULL OR account.email NOT LIKE '%.test'
  ), firsts AS (
    SELECT min(writer_days.day) FILTER (WHERE writer_days.imports > 0) AS first_import,
           min(writer_days.day) FILTER (WHERE writer_days.chat_turns > 0) AS first_question
    FROM writer_days
    GROUP BY writer_days.user_id
  ), accounts AS (
    SELECT (account.created_at AT TIME ZONE 'Europe/Moscow')::date AS registered_day,
           (account.email_verified_at AT TIME ZONE 'Europe/Moscow')::date AS verified_day
    FROM public.users AS account
    WHERE account.email NOT LIKE '%.test'
  ), calendar AS (
    SELECT series.day::date AS day
    FROM generate_series(first_day, last_day, interval '1 day') AS series(day)
  ), per_day AS (
    SELECT calendar.day,
      (SELECT count(*) FROM accounts WHERE accounts.registered_day = calendar.day) AS registered,
      (SELECT count(*) FROM accounts WHERE accounts.verified_day = calendar.day) AS verified,
      (SELECT count(*) FROM firsts WHERE firsts.first_import = calendar.day) AS first_import,
      (SELECT count(*) FROM firsts WHERE firsts.first_question = calendar.day) AS first_question,
      count(writer_days.user_id) FILTER (WHERE writer_days.chat_turns > 0 OR writer_days.imports > 0) AS active,
      coalesce(sum(writer_days.chat_turns), 0)::bigint AS questions,
      coalesce(sum(writer_days.turns_answered), 0)::bigint AS answered,
      coalesce(sum(writer_days.turns_failed), 0)::bigint AS failed,
      coalesce(sum(writer_days.turns_interrupted), 0)::bigint AS interrupted,
      coalesce(sum(writer_days.imports), 0)::bigint AS imports,
      coalesce(sum(writer_days.imports_succeeded), 0)::bigint AS imports_succeeded,
      coalesce(sum(writer_days.imports_failed), 0)::bigint AS imports_failed,
      coalesce(sum(writer_days.imports_interrupted), 0)::bigint AS imports_interrupted
    FROM calendar
    LEFT JOIN writer_days ON writer_days.day = calendar.day
    GROUP BY calendar.day
  ), spend AS (
    SELECT spent.day, sum(spent.calls)::bigint AS calls, sum(spent.input_tokens)::bigint AS input_tokens,
           sum(spent.cached_input_tokens)::bigint AS cached_input_tokens,
           sum(spent.output_tokens)::bigint AS output_tokens
    FROM public.model_usage_days AS spent
    WHERE spent.day BETWEEN first_day AND last_day
    GROUP BY spent.day
  ), days AS (
    SELECT per_day.*, coalesce(spend.calls, 0) AS model_calls, coalesce(spend.input_tokens, 0) AS input_tokens,
           coalesce(spend.cached_input_tokens, 0) AS cached_input_tokens,
           coalesce(spend.output_tokens, 0) AS output_tokens
    FROM per_day
    LEFT JOIN spend ON spend.day = per_day.day
  )
  SELECT days.day, days.registered, days.verified, days.first_import, days.first_question, days.active,
         days.questions, days.answered, days.failed, days.interrupted,
         days.imports, days.imports_succeeded, days.imports_failed, days.imports_interrupted,
         days.model_calls, days.input_tokens, days.cached_input_tokens, days.output_tokens
  FROM days
  UNION ALL
  SELECT NULL::date, sum(days.registered)::bigint, sum(days.verified)::bigint,
         sum(days.first_import)::bigint, sum(days.first_question)::bigint,
         (SELECT count(DISTINCT writer_days.user_id) FROM writer_days
          WHERE writer_days.day BETWEEN first_day AND last_day
            AND (writer_days.chat_turns > 0 OR writer_days.imports > 0)),
         sum(days.questions)::bigint, sum(days.answered)::bigint, sum(days.failed)::bigint,
         sum(days.interrupted)::bigint, sum(days.imports)::bigint, sum(days.imports_succeeded)::bigint,
         sum(days.imports_failed)::bigint, sum(days.imports_interrupted)::bigint,
         sum(days.model_calls)::bigint, sum(days.input_tokens)::bigint,
         sum(days.cached_input_tokens)::bigint, sum(days.output_tokens)::bigint
  FROM days
  ORDER BY 1 NULLS LAST
$$;
REVOKE ALL ON FUNCTION public.ck_owner_days(date, date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_owner_days(date, date) TO canonkeeper_product_runtime;

-- UC1, R1.5: the history of live Jobs. Questions and uploads accepted before 0056 were not
-- counted; after it they were, so a day keeps the larger number. Outcomes of finished Jobs are
-- new columns. Jobs of deleted chats and projects are gone and stay uncounted; blocked authors
-- are skipped as the trigger skips them (R1.2).
INSERT INTO public.writer_usage_days AS usage (
  user_id, day, chat_turns, imports, turns_answered, turns_failed, turns_interrupted,
  imports_succeeded, imports_failed, imports_interrupted
)
SELECT job.creator_user_id, (job.created_at AT TIME ZONE 'Europe/Moscow')::date,
       count(*) FILTER (WHERE job.kind = 'research_chat_turn'),
       count(*) FILTER (WHERE job.kind = 'import'),
       count(*) FILTER (WHERE job.kind = 'research_chat_turn' AND job.status = 'succeeded'),
       count(*) FILTER (WHERE job.kind = 'research_chat_turn' AND job.status = 'failed'),
       count(*) FILTER (WHERE job.kind = 'research_chat_turn' AND job.status IN ('canceled', 'expired')),
       count(*) FILTER (WHERE job.kind = 'import' AND job.status = 'succeeded'),
       count(*) FILTER (WHERE job.kind = 'import' AND job.status = 'failed'),
       count(*) FILTER (WHERE job.kind = 'import' AND job.status IN ('canceled', 'expired'))
FROM public.jobs AS job
JOIN public.users AS author ON author.id = job.creator_user_id AND author.disabled_at IS NULL
WHERE job.kind IN ('research_chat_turn', 'import')
GROUP BY 1, 2
ON CONFLICT (user_id, day) DO UPDATE SET
  chat_turns = GREATEST(usage.chat_turns, EXCLUDED.chat_turns),
  imports = GREATEST(usage.imports, EXCLUDED.imports),
  turns_answered = EXCLUDED.turns_answered,
  turns_failed = EXCLUDED.turns_failed,
  turns_interrupted = EXCLUDED.turns_interrupted,
  imports_succeeded = EXCLUDED.imports_succeeded,
  imports_failed = EXCLUDED.imports_failed,
  imports_interrupted = EXCLUDED.imports_interrupted;

-- UC2, R2.3: accounts deleted before 0059 lose their counters' id too.
SELECT public.ck_anonymize_writer_usage(account.id)
FROM public.users AS account
WHERE account.deleted_at IS NOT NULL;
