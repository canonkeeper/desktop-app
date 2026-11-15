-- The folder of the export of a saga into its vault of Obsidian (DA11.2) is part B of the bridge
-- (specs/2026-11-13-desk-obsidian.md OB1.4): a saga is linked with its vault before any export, so the folder of the export
-- has no value until part B gives it one, and its length is checked only when it has one.
ALTER TABLE public.obsidian_vaults
    ALTER COLUMN export_dir DROP NOT NULL,
    DROP CONSTRAINT obsidian_vaults_export_dir_check,
    ADD CONSTRAINT obsidian_vaults_export_dir_check CHECK (export_dir IS NULL OR char_length(export_dir) BETWEEN 1 AND 1024);
REVOKE ALL ON TABLE public.obsidian_vaults FROM PUBLIC;
