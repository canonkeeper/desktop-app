CREATE TABLE public.schema_migrations (
    version bigint PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);

REVOKE ALL ON TABLE public.schema_migrations FROM PUBLIC;
