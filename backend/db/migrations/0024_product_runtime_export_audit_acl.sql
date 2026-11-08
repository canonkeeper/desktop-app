REVOKE ALL ON FUNCTION public.ck_audit_payload_shape_valid(text, text, text, text, jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_audit_payload_shape_valid(text, text, text, text, jsonb) TO canonkeeper_product_runtime;

REVOKE ALL ON FUNCTION public.ck_jsonb_object_key_count(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_jsonb_object_key_count(jsonb) TO canonkeeper_product_runtime;
