-- The links of the records of the materials of a saga with the notes of its vault of Obsidian
-- (specs/2026-11-15-desk-obsidian-notes.md ON1.9): one record is one note, one path of a note one record. The path is the
-- note's from the root of the vault; revision is the revision of the record the load left — a record of another revision
-- was changed in the app since —, fingerprint the fingerprint of the record the note gave then. The rows go with the link
-- of the saga with its vault — its removal takes them, a new path of the vault keeps them —, with the saga, and with their
-- record: a record the author deletes leaves its note new to the next preview.
CREATE TABLE public.obsidian_notes (
    project_id uuid NOT NULL,
    note_path text NOT NULL,
    record_id uuid NOT NULL,
    revision bigint NOT NULL,
    fingerprint text NOT NULL,
    CONSTRAINT obsidian_notes_pkey PRIMARY KEY (project_id, note_path),
    CONSTRAINT obsidian_notes_record_key UNIQUE (record_id),
    CONSTRAINT obsidian_notes_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.obsidian_vaults(project_id) ON DELETE CASCADE,
    CONSTRAINT obsidian_notes_record_id_fkey FOREIGN KEY (record_id)
        REFERENCES public.author_materials(id) ON DELETE CASCADE,
    CONSTRAINT obsidian_notes_path_check CHECK (char_length(note_path) BETWEEN 1 AND 4096 AND note_path IS NFC NORMALIZED),
    CONSTRAINT obsidian_notes_revision_check CHECK (revision >= 1),
    CONSTRAINT obsidian_notes_fingerprint_check CHECK (fingerprint ~ '^[0-9a-f]{64}$')
);

REVOKE ALL ON TABLE public.obsidian_notes FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.obsidian_notes TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.obsidian_notes TO canonkeeper_governance_owner;
