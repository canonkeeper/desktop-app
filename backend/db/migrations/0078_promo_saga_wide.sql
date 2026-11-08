-- Promotion permissions (specs/2026-10-30-promo-permissions.md R1.4, R1.5, R6.3): a permission covers the
-- saga, not a list made up front. The drawings are every drawing of the gallery while it is granted, so the
-- record keeps no numbers; fragments holds what the administrator took by the permission, nothing while it
-- waits. Requests of the edition that named exact materials cannot be read as covering the saga: production
-- held none on 31.10 (checked again right before this release), the rest are test records.
DELETE FROM public.promo_permissions WHERE terms_version = '2026-10-30';

ALTER TABLE public.promo_permissions
  DROP CONSTRAINT promo_permissions_materials_check,
  DROP CONSTRAINT promo_permissions_fragments_check,
  DROP COLUMN illustration_numbers,
  ADD CONSTRAINT promo_permissions_fragments_check CHECK (
    jsonb_typeof(fragments) = 'array'
    AND (jsonb_array_length(fragments) = 0 OR status IN ('granted', 'revoked'))
  );
