-- The materials of the author written by hand and read from text files (specs/2026-11-10-ux8-memory-records.md MR2.1, MR2.4):
-- a record of the page has the source «manual», a table of a .csv the source «csv». Only the lists of the checks grow.
ALTER TABLE public.author_materials
    DROP CONSTRAINT author_materials_source_kind_check,
    ADD CONSTRAINT author_materials_source_kind_check CHECK (source_kind IN ('xlsx', 'docx', 'text', 'obsidian', 'csv', 'manual'));
ALTER TABLE public.author_material_imports
    DROP CONSTRAINT author_material_imports_source_kind_check,
    ADD CONSTRAINT author_material_imports_source_kind_check CHECK (source_kind IN ('xlsx', 'docx', 'text', 'obsidian', 'csv'));
REVOKE ALL ON TABLE public.author_materials FROM PUBLIC;
REVOKE ALL ON TABLE public.author_material_imports FROM PUBLIC;
