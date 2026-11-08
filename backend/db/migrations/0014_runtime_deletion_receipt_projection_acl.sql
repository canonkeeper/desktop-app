-- Allow the product runtime to validate deletion receipt-set digests.

REVOKE ALL ON FUNCTION public.ck_deletion_target_receipt_set_projection(text, bytea, bytea, bytea[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_deletion_target_receipt_set_projection(text, bytea, bytea, bytea[]) TO canonkeeper_product_runtime;
