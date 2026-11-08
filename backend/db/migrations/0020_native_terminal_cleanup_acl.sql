-- Keep transient indexing coverage cleanup narrow without table-wide DELETE rights.

GRANT SELECT,DELETE ON TABLE public.idempotency_records
TO canonkeeper_governance_owner;

CREATE FUNCTION public.ck_delete_native_indexing_review_coverage(wanted_scope_hash text)
RETURNS bigint
LANGUAGE sql SECURITY DEFINER
SET search_path TO 'pg_catalog'
AS $$
  WITH deleted AS (
    DELETE FROM public.idempotency_records
    WHERE scope_hash = wanted_scope_hash
      AND operation_id = 'native.indexing.review_coverage.v1'
    RETURNING 1
  )
  SELECT pg_catalog.count(*) FROM deleted
$$;

ALTER FUNCTION public.ck_delete_native_indexing_review_coverage(text)
OWNER TO canonkeeper_governance_owner;
REVOKE ALL ON FUNCTION public.ck_delete_native_indexing_review_coverage(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_delete_native_indexing_review_coverage(text)
TO canonkeeper_product_runtime;
