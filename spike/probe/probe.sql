-- SPDX-License-Identifier: AGPL-3.0-only
-- A synthetic schema with the PostgreSQL features the product schema relies on: schemas and
-- group roles, uuid keys, jsonb, a stored russian tsvector with GIN, expression and "C"
-- collation indexes, plpgsql triggers, a SECURITY DEFINER claim with FOR UPDATE SKIP LOCKED,
-- advisory locks and the pg_stat_statements extension. The text is public domain.
CREATE SCHEMA probe;
CREATE ROLE probe_group NOLOGIN;
GRANT USAGE ON SCHEMA probe TO probe_group;

CREATE TABLE probe.chapters (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  key text COLLATE "C" NOT NULL UNIQUE,
  title text NOT NULL,
  body text NOT NULL,
  meta jsonb NOT NULL DEFAULT '{}'::jsonb,
  search tsvector GENERATED ALWAYS AS (to_tsvector('russian', title || ' ' || body)) STORED
);
CREATE INDEX chapters_search ON probe.chapters USING gin (search);
CREATE INDEX chapters_title_lower ON probe.chapters (lower(title));
CREATE INDEX chapters_key_prefix ON probe.chapters (key text_pattern_ops);

CREATE TABLE probe.audit (
  id bigserial PRIMARY KEY,
  chapter uuid NOT NULL,
  at timestamptz NOT NULL DEFAULT now(),
  what text NOT NULL
);

CREATE FUNCTION probe.audit_chapter() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO probe.audit (chapter, what) VALUES (NEW.id, TG_OP);
  RETURN NEW;
END
$$;
CREATE TRIGGER chapters_audit AFTER INSERT OR UPDATE ON probe.chapters
  FOR EACH ROW EXECUTE FUNCTION probe.audit_chapter();

CREATE FUNCTION probe.claim_next() RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, probe AS $$
DECLARE
  picked uuid;
BEGIN
  SELECT id INTO picked FROM probe.chapters ORDER BY key FOR UPDATE SKIP LOCKED LIMIT 1;
  RETURN picked;
END
$$;
REVOKE ALL ON FUNCTION probe.claim_next() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION probe.claim_next() TO probe_group;

CREATE SCHEMA probe_stats;
CREATE EXTENSION pg_stat_statements WITH SCHEMA probe_stats;

INSERT INTO probe.chapters (key, title, body, meta) VALUES
  ('ch-001', 'Глава первая',
   '«Eh bien, mon prince. Gênes et Lucques ne sont plus que des apanages». — Ну что, князь? Кутузова ждали к вечеру…',
   '{"tags": ["война", "мир"]}'),
  ('ch-002', 'Глава вторая',
   'Ёлка и ель, ёж и еж: регистр и порядок не зависят от системы.',
   '{"tags": ["ёж"]}');

SELECT pg_advisory_lock(42);
SELECT pg_advisory_unlock(42);
