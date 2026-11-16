-- The page «CanonKeeper на компьютере» of the site (specs/2026-11-16-desk-app-page.md AP1.2).

-- Whether the administrator shows the page and its entries. One row at most; it appears on the first change. No row is a
-- page not shown: the page shows only when this switch is on and the mirror has an open version (AP1.1).
CREATE TABLE public.platform_desktop_app (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  shown boolean NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by uuid NOT NULL REFERENCES public.users(id)
);
REVOKE ALL ON TABLE public.platform_desktop_app FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON TABLE public.platform_desktop_app TO canonkeeper_product_runtime;
