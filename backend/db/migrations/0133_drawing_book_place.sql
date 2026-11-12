-- The place of a drawing in the book (specs/2026-11-11-ux6-drawing-flow.md DF4.2): «Поставить в книгу…» stands the main
-- variant after a paragraph of its chapter the author chose, «Не показывать в книге» takes it out of the book and keeps its
-- chapter. NULL — where it stood before: after the last paragraph of its sources in the chapter, or at its end; 0 — not in
-- the book; N — after paragraph N of the chapter. A choice of another main variant carries the place the author gave over.
ALTER TABLE public.saga_images
    ADD COLUMN book_after integer,
    ADD CONSTRAINT saga_images_book_after_check CHECK (book_after IS NULL OR book_after >= 0);

REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
