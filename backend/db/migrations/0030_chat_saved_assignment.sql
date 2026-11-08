-- Keep one optional pre-Job Research Chat assignment on the existing session authority.

ALTER TABLE public.chat_sessions
    ADD COLUMN saved_assignment text,
    ADD CONSTRAINT chat_sessions_saved_assignment_check CHECK (
        saved_assignment IS NULL
        OR (
            saved_assignment = btrim(saved_assignment)
            AND char_length(saved_assignment) BETWEEN 1 AND 8000
        )
    );

REVOKE ALL ON TABLE public.chat_sessions FROM PUBLIC;
