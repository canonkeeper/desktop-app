-- Trigger execution needs no direct EXECUTE grant; cover CRUD uses the product role.
REVOKE EXECUTE ON FUNCTION public.ck_restore_book_cover_generation() FROM canonkeeper_product_runtime;
REVOKE ALL ON FUNCTION public.ck_restore_book_cover_generation() FROM PUBLIC;
REVOKE SELECT, UPDATE, DELETE ON TABLE public.book_cover_artifacts FROM canonkeeper_governance_owner;
