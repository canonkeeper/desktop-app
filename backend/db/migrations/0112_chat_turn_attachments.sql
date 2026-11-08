-- The chapter of the author attached to a question of the chat (specs/2026-11-07-chat-attach-chapter.md, CA6): one row a
-- turn, numbered in its chat, its paragraphs as parsed from the file or the pasted text — never the file. The Codex
-- thread keeps only its header in the input of the turn; the text lives here once, for the tool that reads it by
-- paragraphs, the panel that shows it, the proofreading the author starts from it and the copy of his data. It goes
-- with its turn: deleting the chat or the project deletes the turns, and the rows of their attachments with them.
CREATE TABLE public.chat_turn_attachments (
    turn_id uuid NOT NULL,
    chat_id uuid NOT NULL,
    project_id uuid NOT NULL,
    number integer NOT NULL,
    title text NOT NULL,
    file_name text NOT NULL,
    format text NOT NULL,
    paragraphs text[] NOT NULL,
    words integer NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chat_turn_attachments_pkey PRIMARY KEY (turn_id),
    CONSTRAINT chat_turn_attachments_number_key UNIQUE (chat_id, number),
    CONSTRAINT chat_turn_attachments_turn_fkey FOREIGN KEY (turn_id, chat_id)
        REFERENCES public.chat_turns(id, chat_id) ON DELETE CASCADE,
    CONSTRAINT chat_turn_attachments_project_id_fkey FOREIGN KEY (project_id)
        REFERENCES public.projects(id) ON DELETE CASCADE,
    CONSTRAINT chat_turn_attachments_number_check CHECK (number BETWEEN 1 AND 999),
    CONSTRAINT chat_turn_attachments_title_check CHECK (char_length(title) <= 200),
    CONSTRAINT chat_turn_attachments_file_name_check CHECK (char_length(file_name) <= 255),
    CONSTRAINT chat_turn_attachments_format_check CHECK (format IN ('docx', 'txt', 'md', 'text')),
    CONSTRAINT chat_turn_attachments_paragraphs_check CHECK (cardinality(paragraphs) >= 1),
    CONSTRAINT chat_turn_attachments_words_check CHECK (words >= 1)
);
CREATE INDEX chat_turn_attachments_project_idx ON public.chat_turn_attachments (project_id);

REVOKE ALL ON TABLE public.chat_turn_attachments FROM PUBLIC;
GRANT SELECT, INSERT ON TABLE public.chat_turn_attachments TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.chat_turn_attachments TO canonkeeper_governance_owner;
