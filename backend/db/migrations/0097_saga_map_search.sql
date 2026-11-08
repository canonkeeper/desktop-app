-- The search of the saga map reads prepared rows (specs/2026-11-04-saga-map.md, R10.6). A row of an earlier build has an empty text
-- and matches nothing; the next rules of the map make every saga build its rows again at start.
ALTER TABLE public.saga_map_nodes
    ADD COLUMN name text NOT NULL DEFAULT '',
    ADD COLUMN type text NOT NULL DEFAULT '',
    ADD COLUMN chapter_count integer NOT NULL DEFAULT 0,
    ADD COLUMN search_text text NOT NULL DEFAULT '';
ALTER TABLE public.saga_map_nodes
    ALTER COLUMN name DROP DEFAULT,
    ALTER COLUMN type DROP DEFAULT,
    ALTER COLUMN chapter_count DROP DEFAULT,
    ALTER COLUMN search_text DROP DEFAULT;
REVOKE ALL ON TABLE public.saga_map_nodes FROM PUBLIC;
