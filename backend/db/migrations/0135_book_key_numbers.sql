-- The number of a book in the keys of its chapters (specs/2026-11-11-ux7-reorder.md RK1): the key of a chapter that has
-- no number of its own in the saga is b<number of the book>c<order of the chapter>, and the links of the answers of the
-- chat (in the Codex thread, which is never rewritten), the places of the drawings and the links of the threads keep it.
-- Until now the number was books."order" — the place of the book on the shelf — so a move of a volume would lead them
-- to other chapters. From now on a book gets its number once, when it is created, and keeps it: a move, a deletion and
-- a return of books change only "order".

-- RK1.1: the greatest number a saga ever gave; it outlives the purge of a deleted book, so a number is never given twice.
ALTER TABLE public.projects
  ADD COLUMN book_key_numbers integer NOT NULL DEFAULT 0 CHECK (book_key_numbers >= 0);

ALTER TABLE public.books ADD COLUMN key_number integer;

-- RK1.2: every key the server serves today stays. The keys are read of the living books with their "order" as the
-- number, so a living book keeps its "order" (of two living books on one place — corrupt, the keys of one hid the other —
-- the first created). A deleted book keeps its "order" while no living book holds it (of two deleted ones, the one
-- deleted last: its links are the latest). The others get numbers after every place of the saga, by their creation.
WITH ranked AS (
  SELECT book.id,
         row_number() OVER (
           PARTITION BY book.project_id, book."order"
           ORDER BY book.deleted_at IS NOT NULL,
                    CASE WHEN book.deleted_at IS NULL THEN book.created_at END,
                    book.deleted_at DESC,
                    book.id
         ) AS rank
  FROM public.books AS book
)
UPDATE public.books AS book
SET key_number = book."order"
FROM ranked
WHERE ranked.id = book.id AND ranked.rank = 1;

WITH top AS (
  SELECT project_id, max("order") AS place FROM public.books GROUP BY project_id
), rest AS (
  SELECT book.id, top.place + row_number() OVER (PARTITION BY book.project_id ORDER BY book.created_at, book.id) AS number
  FROM public.books AS book
  JOIN top ON top.project_id = book.project_id
  WHERE book.key_number IS NULL
)
UPDATE public.books AS book
SET key_number = rest.number
FROM rest
WHERE rest.id = book.id;

UPDATE public.projects AS project
SET book_key_numbers = issued.number
FROM (SELECT project_id, max(key_number) AS number FROM public.books GROUP BY project_id) AS issued
WHERE issued.project_id = project.id;

-- The lifecycle guard of a book is a deferred trigger: its checks of the rows written above run now, so the table can be
-- altered in the same transaction.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE public.books
  ALTER COLUMN key_number SET NOT NULL,
  ADD CONSTRAINT books_key_number_positive CHECK (key_number >= 1);

-- A number is the book's alone in its saga, also of the books in «Недавно удалённые».
CREATE UNIQUE INDEX books_project_key_number_key ON public.books USING btree (project_id, key_number);

-- RK1.1: the number is issued here, by every insert of a book (the shelf, the import, the example saga): the next after
-- the greatest the saga gave. A book that brings its number — a copy restored from a backup — keeps it, and the saga
-- issues after it. The two ways of creating a book lock the project first, so the counter is taken in that order. It
-- fires also in the replica mode of a seed or a restore, where the other triggers sleep.
CREATE FUNCTION public.ck_issue_book_key_number() RETURNS trigger
LANGUAGE plpgsql
SET search_path TO 'pg_catalog', 'public'
AS $$
BEGIN
  IF NEW.key_number IS NULL THEN
    UPDATE public.projects
    SET book_key_numbers = book_key_numbers + 1
    WHERE id = NEW.project_id
    RETURNING book_key_numbers INTO NEW.key_number;
  ELSE
    UPDATE public.projects
    SET book_key_numbers = greatest(book_key_numbers, NEW.key_number)
    WHERE id = NEW.project_id;
  END IF;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.ck_issue_book_key_number() FROM PUBLIC;

CREATE TRIGGER books_issue_key_number
BEFORE INSERT ON public.books
FOR EACH ROW EXECUTE FUNCTION public.ck_issue_book_key_number();
ALTER TABLE public.books ENABLE ALWAYS TRIGGER books_issue_key_number;

-- The number never changes once given.
CREATE FUNCTION public.ck_keep_book_key_number() RETURNS trigger
LANGUAGE plpgsql
SET search_path TO 'pg_catalog', 'public'
AS $$
BEGIN
  IF NEW.key_number IS DISTINCT FROM OLD.key_number THEN
    RAISE EXCEPTION 'the number of a book in the keys of its chapters never changes'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.ck_keep_book_key_number() FROM PUBLIC;

CREATE TRIGGER books_keep_key_number
BEFORE UPDATE OF key_number ON public.books
FOR EACH ROW EXECUTE FUNCTION public.ck_keep_book_key_number();
