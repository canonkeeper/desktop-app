-- Store private versioned book-cover metadata; bytes remain under the shared artifact root.

CREATE TABLE public.book_cover_artifacts (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL,
    book_id uuid NOT NULL,
    book_lifecycle_generation bigint NOT NULL,
    version bigint NOT NULL,
    storage_key text UNIQUE NOT NULL,
    mime_type text NOT NULL,
    byte_size bigint NOT NULL,
    width integer NOT NULL,
    height integer NOT NULL,
    content_sha256 bytea NOT NULL,
    state text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    activated_at timestamp with time zone,
    superseded_at timestamp with time zone,
    removed_at timestamp with time zone,
    object_deleted_at timestamp with time zone,
    CONSTRAINT book_cover_artifacts_book_fkey
        FOREIGN KEY (book_id, project_id) REFERENCES public.books(id, project_id),
    CONSTRAINT book_cover_artifacts_generation_check CHECK (book_lifecycle_generation >= 1),
    CONSTRAINT book_cover_artifacts_version_check CHECK (version >= 1),
    CONSTRAINT book_cover_artifacts_storage_key_check CHECK (
        storage_key ~ '^book-covers/[^/\\]+$'
    ),
    CONSTRAINT book_cover_artifacts_mime_type_check CHECK (
        mime_type IN ('image/jpeg', 'image/png', 'image/webp')
    ),
    CONSTRAINT book_cover_artifacts_byte_size_check CHECK (byte_size BETWEEN 1 AND 5242880),
    CONSTRAINT book_cover_artifacts_dimensions_check CHECK (width >= 600 AND height >= 900),
    CONSTRAINT book_cover_artifacts_digest_check CHECK (
        octet_length(content_sha256) = 32
        AND content_sha256 <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT book_cover_artifacts_state_check CHECK (
        state IN ('staging', 'active', 'superseded', 'removed')
    ),
    CONSTRAINT book_cover_artifacts_timestamps_check CHECK (
        (state = 'staging' AND activated_at IS NULL AND superseded_at IS NULL AND removed_at IS NULL AND object_deleted_at IS NULL)
        OR (state = 'active' AND activated_at IS NOT NULL AND superseded_at IS NULL AND removed_at IS NULL AND object_deleted_at IS NULL)
        OR (state = 'superseded' AND activated_at IS NOT NULL AND superseded_at IS NOT NULL AND removed_at IS NULL)
        OR (state = 'removed' AND activated_at IS NOT NULL AND superseded_at IS NULL AND removed_at IS NOT NULL)
    ),
    CONSTRAINT book_cover_artifacts_time_order_check CHECK (
        created_at <= COALESCE(activated_at, created_at)
        AND activated_at <= COALESCE(superseded_at, activated_at)
        AND activated_at <= COALESCE(removed_at, activated_at)
        AND object_deleted_at >= COALESCE(superseded_at, removed_at, object_deleted_at)
    ),
    CONSTRAINT book_cover_artifacts_book_generation_version_key
        UNIQUE (book_id, book_lifecycle_generation, version)
);

CREATE UNIQUE INDEX book_cover_artifacts_active_book_generation_idx
    ON public.book_cover_artifacts (book_id, book_lifecycle_generation)
    WHERE state = 'active';

CREATE INDEX book_cover_artifacts_project_book_generation_idx
    ON public.book_cover_artifacts (project_id, book_id, book_lifecycle_generation, version);

CREATE FUNCTION public.ck_restore_book_cover_generation() RETURNS trigger
    LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE public.book_cover_artifacts
    SET book_lifecycle_generation = NEW.lifecycle_generation
    WHERE project_id = NEW.project_id
      AND book_id = NEW.id
      AND book_lifecycle_generation = NEW.lifecycle_generation - 1;
    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION public.ck_restore_book_cover_generation() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.ck_restore_book_cover_generation() TO canonkeeper_product_runtime;

CREATE TRIGGER books_restore_cover_generation
    AFTER UPDATE OF deleted_at ON public.books
    FOR EACH ROW
    WHEN (OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL)
    EXECUTE FUNCTION public.ck_restore_book_cover_generation();

REVOKE ALL ON TABLE public.book_cover_artifacts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.book_cover_artifacts TO canonkeeper_product_runtime;
GRANT SELECT, UPDATE, DELETE ON TABLE public.book_cover_artifacts TO canonkeeper_governance_owner;
