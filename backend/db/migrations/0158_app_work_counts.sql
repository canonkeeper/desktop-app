-- The work of the linked apps apart in the reports of the owner (specs/2026-11-16-desk-admin-apps.md AA3).

-- AA3.1: the work of the apps a site counts, by the Moscow day of its unit: the turns of their chats with their corrections,
-- the readings of books and the uploads of materials. The fence of the permits moves them in the transactions and the days
-- in which it counts and gives back the units of the tier; a reading counts once, when it opens. They are columns of the
-- day row so the deletion of an account moves them to its pseudonym with the other counters (ck_anonymize_writer_usage);
-- the days before this migration stay at zero.
ALTER TABLE public.writer_usage_days
  ADD COLUMN app_turns bigint NOT NULL DEFAULT 0 CHECK (app_turns >= 0),
  ADD COLUMN app_readings bigint NOT NULL DEFAULT 0 CHECK (app_readings >= 0),
  ADD COLUMN app_material_imports bigint NOT NULL DEFAULT 0 CHECK (app_material_imports >= 0);
REVOKE ALL ON public.writer_usage_days FROM PUBLIC;

-- The first signs of a pass are shown nowhere: the admin panel and the operator name an app by its computer, system and
-- dates (AA1.1, AA2.1), so the column no code reads goes.
ALTER TABLE public.app_devices DROP COLUMN pass_prefix;
REVOKE ALL ON TABLE public.app_devices FROM PUBLIC;

-- AA3.2: the owner's days tell the work of the apps apart. The model spend of the site is the sources that are not of the
-- apps; the spend of the sources app_* and the work of the apps of the writers' days are columns of their own. The other
-- columns are as in 0059: questions and uploads are the units of the tier of the site and of the apps alike, and the
-- funnel counts the work of the apps too.
DROP FUNCTION public.ck_owner_days(date, date);
CREATE FUNCTION public.ck_owner_days(first_day date, last_day date)
RETURNS TABLE (
  day date,
  registered bigint, verified bigint, first_import bigint, first_question bigint, active bigint,
  questions bigint, answered bigint, failed bigint, interrupted bigint,
  imports bigint, imports_succeeded bigint, imports_failed bigint, imports_interrupted bigint,
  model_calls bigint, input_tokens bigint, cached_input_tokens bigint, output_tokens bigint,
  app_turns bigint, app_readings bigint, app_material_imports bigint,
  app_model_calls bigint, app_input_tokens bigint, app_cached_input_tokens bigint, app_output_tokens bigint
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
      coalesce(sum(writer_days.imports_interrupted), 0)::bigint AS imports_interrupted,
      coalesce(sum(writer_days.app_turns), 0)::bigint AS app_turns,
      coalesce(sum(writer_days.app_readings), 0)::bigint AS app_readings,
      coalesce(sum(writer_days.app_material_imports), 0)::bigint AS app_material_imports
    FROM calendar
    LEFT JOIN writer_days ON writer_days.day = calendar.day
    GROUP BY calendar.day
  ), spend AS (
    SELECT spent.day,
           coalesce(sum(spent.calls) FILTER (WHERE NOT spent.of_apps), 0)::bigint AS calls,
           coalesce(sum(spent.input_tokens) FILTER (WHERE NOT spent.of_apps), 0)::bigint AS input_tokens,
           coalesce(sum(spent.cached_input_tokens) FILTER (WHERE NOT spent.of_apps), 0)::bigint AS cached_input_tokens,
           coalesce(sum(spent.output_tokens) FILTER (WHERE NOT spent.of_apps), 0)::bigint AS output_tokens,
           coalesce(sum(spent.calls) FILTER (WHERE spent.of_apps), 0)::bigint AS app_calls,
           coalesce(sum(spent.input_tokens) FILTER (WHERE spent.of_apps), 0)::bigint AS app_input_tokens,
           coalesce(sum(spent.cached_input_tokens) FILTER (WHERE spent.of_apps), 0)::bigint AS app_cached_input_tokens,
           coalesce(sum(spent.output_tokens) FILTER (WHERE spent.of_apps), 0)::bigint AS app_output_tokens
    FROM (
      SELECT usage.*, starts_with(usage.source, 'app_') AS of_apps
      FROM public.model_usage_days AS usage
      WHERE usage.day BETWEEN first_day AND last_day
    ) AS spent
    GROUP BY spent.day
  ), days AS (
    SELECT per_day.*, coalesce(spend.calls, 0) AS model_calls, coalesce(spend.input_tokens, 0) AS input_tokens,
           coalesce(spend.cached_input_tokens, 0) AS cached_input_tokens,
           coalesce(spend.output_tokens, 0) AS output_tokens,
           coalesce(spend.app_calls, 0) AS app_model_calls, coalesce(spend.app_input_tokens, 0) AS app_input_tokens,
           coalesce(spend.app_cached_input_tokens, 0) AS app_cached_input_tokens,
           coalesce(spend.app_output_tokens, 0) AS app_output_tokens
    FROM per_day
    LEFT JOIN spend ON spend.day = per_day.day
  )
  SELECT days.day, days.registered, days.verified, days.first_import, days.first_question, days.active,
         days.questions, days.answered, days.failed, days.interrupted,
         days.imports, days.imports_succeeded, days.imports_failed, days.imports_interrupted,
         days.model_calls, days.input_tokens, days.cached_input_tokens, days.output_tokens,
         days.app_turns, days.app_readings, days.app_material_imports,
         days.app_model_calls, days.app_input_tokens, days.app_cached_input_tokens, days.app_output_tokens
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
         sum(days.cached_input_tokens)::bigint, sum(days.output_tokens)::bigint,
         sum(days.app_turns)::bigint, sum(days.app_readings)::bigint, sum(days.app_material_imports)::bigint,
         sum(days.app_model_calls)::bigint, sum(days.app_input_tokens)::bigint,
         sum(days.app_cached_input_tokens)::bigint, sum(days.app_output_tokens)::bigint
  FROM days
  ORDER BY 1 NULLS LAST
$$;
REVOKE ALL ON FUNCTION public.ck_owner_days(date, date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_owner_days(date, date) TO canonkeeper_product_runtime;
