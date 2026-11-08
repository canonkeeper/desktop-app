-- Saga illustrations (specs/2026-10-29-saga-illustrations.md R21.3, R21.5): the variants of a drawing are
-- drawings of the gallery that wait for the author's choice. variant_group joins the rows of one
-- start; it is NULL on a plain drawing and on the one chosen. unchosen_variants is kept on the one
-- chosen: the numbers of the others, gone with the choice, so a later choice of another is a conflict and
-- the chat says «Вариант не выбран» instead of «Рисунок удалён»; NULL — no group was closed here.
-- variant_prompt is what sets this variant apart: its staging or reading, kept apart from prompt, which stays
-- the author's words or the agent's description (R21.14); it stays on the one chosen.
ALTER TABLE public.saga_images
  ADD COLUMN variant_group uuid,
  ADD COLUMN unchosen_variants integer[],
  ADD COLUMN variant_prompt text CHECK (variant_prompt IS NULL OR char_length(variant_prompt) <= 300),
  ADD CONSTRAINT saga_images_variant_state_check CHECK (variant_group IS NULL OR unchosen_variants IS NULL);
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

CREATE INDEX saga_images_variant_group ON public.saga_images (project_id, variant_group, number)
  WHERE variant_group IS NOT NULL;
