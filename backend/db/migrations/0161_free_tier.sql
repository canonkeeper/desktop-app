-- «Бесплатно»: a book is loaded once, «Письмо» is a trial (specs/2026-11-18-free-tier.md FT1, FT2).

-- FT1.1: book_reloads — 0 or 1, not of a day: 0 is a book loaded once — no new version of it from a file, and the
-- books of an account are every book that ever came into its sagas. A tier sets it («Бесплатно» 0, the paid ones 1);
-- the writer's own value and the platform's are a way to change it for one writer or for all, NULL — none.
ALTER TABLE public.subscription_tiers
  ADD COLUMN book_reloads integer NOT NULL DEFAULT 1 CHECK (book_reloads IN (0, 1));
UPDATE public.subscription_tiers SET book_reloads = 0, updated_at = now(), updated_by = NULL WHERE code = 'free';
ALTER TABLE public.subscription_tiers ALTER COLUMN book_reloads DROP DEFAULT;

ALTER TABLE public.writer_quota_overrides
  ADD COLUMN book_reloads integer CHECK (book_reloads IN (0, 1));
REVOKE ALL ON public.writer_quota_overrides FROM PUBLIC;
ALTER TABLE public.platform_quota_defaults
  ADD COLUMN book_reloads integer CHECK (book_reloads IN (0, 1));
REVOKE ALL ON public.platform_quota_defaults FROM PUBLIC;

-- FT1.2: the books that ever came into the sagas of an account, one row a book, never updated. The rows have no foreign
-- key: a deleted book or saga keeps its row, so a deletion gives no place back; the deletion of the account takes them.
-- Only inserts of distinct keys: no row waits for another, so the lock order of 0059 stays as it is.
CREATE TABLE public.book_loads (
  book_id uuid PRIMARY KEY,
  owner_user_id uuid NOT NULL,
  loaded_at timestamptz NOT NULL
);
CREATE INDEX book_loads_owner_idx ON public.book_loads (owner_user_id);
REVOKE ALL ON TABLE public.book_loads FROM PUBLIC;
GRANT SELECT, DELETE ON TABLE public.book_loads TO canonkeeper_product_runtime;
GRANT SELECT, INSERT ON TABLE public.book_loads TO canonkeeper_governance_owner;

-- Every insert of a book writes the row, whatever path inserts it (a new book, an import, the file of a saga), but in the
-- example saga: its book is not the author's. A plain trigger, not ENABLE ALWAYS: a restore of a copy brings the rows of
-- the copy and does not count its books twice. A blocked owner is counted too.
CREATE FUNCTION public.ck_count_book_load() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER
SET search_path TO 'pg_catalog', 'public'
AS $$
BEGIN
  INSERT INTO public.book_loads (book_id, owner_user_id, loaded_at)
  SELECT NEW.id, project.owner_user_id, NEW.created_at
  FROM public.projects AS project
  WHERE project.id = NEW.project_id AND NOT project.demo
  ON CONFLICT (book_id) DO NOTHING;
  RETURN NULL;
END;
$$;
ALTER FUNCTION public.ck_count_book_load() OWNER TO canonkeeper_governance_owner;
REVOKE ALL ON FUNCTION public.ck_count_book_load() FROM PUBLIC;

CREATE TRIGGER books_count_load
AFTER INSERT ON public.books
FOR EACH ROW EXECUTE FUNCTION public.ck_count_book_load();

-- The books there are now: live and in the trash, in live and deleted sagas, not in the example saga, of accounts that
-- are not deleted. Books a purge already took are gone and are not counted — accepted.
INSERT INTO public.book_loads (book_id, owner_user_id, loaded_at)
SELECT book.id, project.owner_user_id, book.created_at
FROM public.books AS book
JOIN public.projects AS project ON project.id = book.project_id
JOIN public.users AS account ON account.id = project.owner_user_id
WHERE NOT project.demo AND account.deleted_at IS NULL;

-- FT2.1: the trial proofreadings of a tier for the life of an account («Бесплатно» 1, the others 0). A proofreading is
-- charged as a new chapter is: a row of subscription_chapter_charges of its kind, counted while its Job lives or once its
-- corrections reached the author (scene_at).
ALTER TABLE public.subscription_tiers
  ADD COLUMN trial_proofreads integer NOT NULL DEFAULT 0 CHECK (trial_proofreads >= 0);
UPDATE public.subscription_tiers SET trial_proofreads = 1, updated_at = now(), updated_by = NULL WHERE code = 'free';
REVOKE ALL ON TABLE public.subscription_tiers FROM PUBLIC;

ALTER TABLE public.subscription_chapter_charges
  DROP CONSTRAINT subscription_chapter_charges_kind_check,
  ADD CONSTRAINT subscription_chapter_charges_kind_check CHECK (kind IN ('window', 'trial', 'proofread'));
REVOKE ALL ON TABLE public.subscription_chapter_charges FROM PUBLIC;
