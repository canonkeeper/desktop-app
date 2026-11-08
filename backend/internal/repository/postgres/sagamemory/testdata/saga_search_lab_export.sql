-- Export of the saga search lab (search_lab_*_test.go): the draft text version of the given
-- projects, their chapter cards and nothing else. Read-only; run against TEST with
--   PGOPTIONS='-c default_transaction_read_only=on' psql "$DSN" -X -q \
--     -v projects="'<uuid>','<uuid>'" -f saga_search_lab_export.sql
-- from a private directory outside the repository (the files hold the saga texts) that has a
-- data/ subdirectory; CANONKEEPER_SAGA_SEARCH_LAB then names data/. search_calls.json and
-- tool_calls.json come from the saga.* function calls in the research-chat rollouts of TEST
-- (arguments only), mapped to projects through chat_sessions.thread_id.
\set sel 'WITH sel AS (SELECT m.project_id, m.book_id, m.chapter_id, m.source_revision, m.title, m.chapter_order, encode(s.chapter_content_hash, ''hex'') AS content_hash FROM chapter_source_members m JOIN chapters c ON c.id = m.chapter_id AND c.project_id = m.project_id AND c.deleted_at IS NULL JOIN books b ON b.id = m.book_id AND b.project_id = m.project_id AND b.deleted_at IS NULL JOIN chapter_text_index_state s ON s.project_id = m.project_id AND s.source_view = m.source_view AND s.chapter_id = m.chapter_id AND s.source_revision = m.source_revision WHERE m.source_view = ''draft'' AND m.project_id IN (' :projects '))'
\o data/books.csv
COPY (:sel SELECT b.project_id, b.id, b."order", b.title FROM books b WHERE b.deleted_at IS NULL AND b.project_id IN (SELECT DISTINCT project_id FROM sel) ORDER BY 1, 3) TO STDOUT WITH (FORMAT csv);
\o data/chapters.csv
COPY (:sel SELECT project_id, book_id, chapter_id, source_revision, title, chapter_order, content_hash FROM sel ORDER BY 1, 2, 6) TO STDOUT WITH (FORMAT csv);
\o data/paragraphs.csv
COPY (:sel SELECT sel.chapter_id, p."order", p.plain_text FROM sel JOIN chapter_paragraphs p ON p.chapter_id = sel.chapter_id AND p.revision = sel.source_revision AND p.variant = 'draft' ORDER BY 1, 2) TO STDOUT WITH (FORMAT csv);
\o data/cards.csv
COPY (:sel SELECT k.project_id, k.chapter_id, encode(k.content_hash, 'hex'), k.card_json::text, k.model, k.version FROM chapter_cards k JOIN sel ON sel.chapter_id = k.chapter_id AND sel.content_hash = encode(k.content_hash, 'hex')) TO STDOUT WITH (FORMAT csv);
\o
