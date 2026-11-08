-- «Переработать и мой текст» (specs/2026-11-02-writer-workshop.md W3.10): the author's choice for «Написать главу», kept
-- on his account. Off, his lines stand in the chapter as he wrote them and only what the helper wrote is rewritten.
ALTER TABLE public.users ADD COLUMN rework_own_text boolean NOT NULL DEFAULT false;
REVOKE ALL ON TABLE public.users FROM PUBLIC;
