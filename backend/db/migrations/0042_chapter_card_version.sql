-- A chapter card records the version of the instructions that wrote it (specs/2026-10-09-saga-memory.md,
-- UC2). The cards Job rebuilds a card of older instructions; until then the old card keeps serving
-- the chat. Existing cards were written by the first instructions.
ALTER TABLE public.chapter_cards
    ADD COLUMN version integer NOT NULL DEFAULT 1,
    ADD CONSTRAINT chapter_cards_version_check CHECK (version >= 1);

ALTER TABLE public.chapter_cards ALTER COLUMN version DROP DEFAULT;

REVOKE ALL ON TABLE public.chapter_cards FROM PUBLIC;
