-- Allow the product runtime to validate source hard-purge predecessor sets.

REVOKE ALL ON FUNCTION
    public.ck_source_hard_purge_predecessor_set_is_valid(uuid, uuid),
    public.ck_deletion_selected_vector_digest(text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_deletion_selected_vector_projection(text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_deletion_predecessor_set_digest(bytea, uuid[], bytea[], bytea[]),
    public.ck_deletion_predecessor_set_projection(bytea, uuid[], bytea[], bytea[]),
    public.ck_deletion_tombstone_digest(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone, uuid),
    public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone, uuid),
    public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone),
    public.ck_unix_micros(timestamp with time zone)
FROM PUBLIC;

GRANT EXECUTE ON FUNCTION
    public.ck_source_hard_purge_predecessor_set_is_valid(uuid, uuid),
    public.ck_deletion_selected_vector_digest(text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_deletion_selected_vector_projection(text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_deletion_predecessor_set_digest(bytea, uuid[], bytea[], bytea[]),
    public.ck_deletion_predecessor_set_projection(bytea, uuid[], bytea[], bytea[]),
    public.ck_deletion_tombstone_digest(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone, uuid),
    public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone, uuid),
    public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone),
    public.ck_unix_micros(timestamp with time zone)
TO canonkeeper_product_runtime;
