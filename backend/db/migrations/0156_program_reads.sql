-- The readings of the programs of the author through the bridge (specs/2026-11-15-desk-mcp.md MC4.2, MC4.4): the door of the
-- desktop app writes a record of every reading in the journal of what went out before its answer goes — the second and last
-- writer of the journal, beside the gateway. A reading names the program by the name it gave itself, the page of the answer
-- and the number of the pages; it has no Job, no thread, no helper and no host. The records of the gateway stay as they are.
ALTER TABLE public.model_disclosures
    DROP CONSTRAINT model_disclosures_purpose_check,
    DROP CONSTRAINT model_disclosures_profile_name_check,
    DROP CONSTRAINT model_disclosures_provider_host_check,
    ALTER COLUMN profile_name DROP NOT NULL,
    ALTER COLUMN provider_host DROP NOT NULL,
    ADD COLUMN client text,
    ADD COLUMN page integer,
    ADD COLUMN pages integer,
    ADD CONSTRAINT model_disclosures_purpose_check CHECK (purpose IN (
        'chat', 'probe', 'card', 'card_pass', 'names', 'layer_book', 'layer_reduce', 'layer_verify', 'layer_gap', 'scan',
        'materials', 'mcp'
    )),
    -- A sending goes to a helper of a profile and a host; a reading of a program to no helper.
    ADD CONSTRAINT model_disclosures_profile_name_check CHECK (
        (purpose = 'mcp') = (profile_name IS NULL) AND (profile_name IS NULL OR char_length(profile_name) BETWEEN 1 AND 100)
    ),
    ADD CONSTRAINT model_disclosures_provider_host_check CHECK (
        (purpose = 'mcp') = (provider_host IS NULL) AND (provider_host IS NULL OR char_length(provider_host) BETWEEN 1 AND 255)
    ),
    -- A reading names its program; nothing else has one.
    ADD CONSTRAINT model_disclosures_client_check CHECK (
        (purpose = 'mcp') = (client IS NOT NULL) AND (client IS NULL OR char_length(client) BETWEEN 1 AND 200)
    ),
    -- A reading names its page of the answer and how many there were; nothing else has a page.
    ADD CONSTRAINT model_disclosures_page_check CHECK (
        (purpose = 'mcp') = (page IS NOT NULL) AND (page IS NULL) = (pages IS NULL) AND (page IS NULL OR page BETWEEN 1 AND pages)
    ),
    -- A reading has no Job and no thread, and is never the first time to a helper.
    ADD CONSTRAINT model_disclosures_mcp_check CHECK (purpose <> 'mcp' OR (job_id IS NULL AND thread_id IS NULL AND NOT first_time));

REVOKE ALL ON TABLE public.model_disclosures FROM PUBLIC;
