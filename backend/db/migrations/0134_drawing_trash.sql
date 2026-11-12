-- A deleted drawing lies in «Недавно удалённые» for 7 days, as a book does (specs/2026-11-11-ux7-trash.md TB3.1): its
-- row, its versions with their numbers, layers and files and the looks it is stay, and no read shows them; «Вернуть»
-- clears the mark and puts it back in its place. After purge_after the sweep of the drawings of the site removes the
-- versions, their layers and files and the row by the deletion it always had. The conversation of its helper goes at
-- once, by the deletion of a chat.
ALTER TABLE public.saga_drawings
  ADD COLUMN deleted_at timestamp with time zone,
  ADD COLUMN purge_after timestamp with time zone,
  ADD CONSTRAINT saga_drawings_deletion_window_check CHECK (
    (deleted_at IS NULL AND purge_after IS NULL)
    OR (deleted_at IS NOT NULL AND purge_after = deleted_at + interval '168 hours')
  );

-- Every read of a version passes «its drawing is not deleted» by the branch; the sweep reads the drawings by their term.
CREATE INDEX saga_drawings_deleted_branch_idx ON public.saga_drawings (project_id, branch) WHERE deleted_at IS NOT NULL;
CREATE INDEX saga_drawings_purge_idx ON public.saga_drawings (purge_after) WHERE deleted_at IS NOT NULL;

REVOKE ALL ON TABLE public.saga_drawings FROM PUBLIC;
