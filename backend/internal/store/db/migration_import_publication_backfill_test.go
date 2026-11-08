package storedb_test

import (
	"context"
	"strings"
	"testing"

	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

const publicationBackfillProjectID = "10000000-0000-4000-8000-000000000001"

type publicationProtectedState struct {
	jobs, indexState, indexRows, draftParagraphs, lifecycle, books string
	canonGraph, canonAuthor, summaries                             int
}

func TestImportedPublicationBackfillIsExactAndSideEffectFree(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_import_publication_backfill", 27)
	seedImportedPublicationBackfill(t, db)
	before := readPublicationProtectedState(t, db)

	if err := testutil.ApplyNextMigration(t, db, 27); err != nil {
		t.Fatal(err)
	}
	assertImportedPublicationBackfill(t, db)
	if after := readPublicationProtectedState(t, db); after != before {
		t.Fatalf("protected state changed:\nbefore=%+v\nafter=%+v", before, after)
	}

	// The numbered runner never replays a migration, but the SQL remains data-idempotent.
	schema := pgx.Identifier{testutil.IsolatedPostgresSchema(t, db)}.Sanitize()
	replay := strings.ReplaceAll(string(testutil.MigrationSQL(t, 28)), "public.", schema+".")
	if _, err := db.Pool.Exec(context.Background(), replay); err != nil {
		t.Fatalf("replay migration 0028: %v", err)
	}
	assertImportedPublicationBackfill(t, db)
	if afterReplay := readPublicationProtectedState(t, db); afterReplay != before {
		t.Fatalf("idempotent replay changed protected state:\nbefore=%+v\nafter=%+v", before, afterReplay)
	}
}

func seedImportedPublicationBackfill(t *testing.T, db *storedb.DB) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(), `
BEGIN;
SET LOCAL session_replication_role = replica;

INSERT INTO projects (id, owner_user_id, name, created_at, updated_at)
VALUES ('10000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000002', 'Publication backfill', '2026-08-01Z', '2026-08-01Z');

INSERT INTO books (id, project_id, title, "order", display_number, status, created_at, updated_at)
SELECT ('20000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
       '10000000-0000-4000-8000-000000000001', 'Eligible ' || value, value, value::text, 'ready', '2026-08-01Z', '2026-08-01Z'
FROM generate_series(1, 12) AS value;
INSERT INTO books (id, project_id, title, "order", display_number, status, created_at, updated_at) VALUES
  ('20000000-0000-4000-8000-000000000101', '10000000-0000-4000-8000-000000000001', 'DOCX', 13, '13', 'ready', '2026-08-01Z', '2026-08-01Z'),
  ('20000000-0000-4000-8000-000000000102', '10000000-0000-4000-8000-000000000001', 'Manual', 14, '14', 'ready', '2026-08-01Z', '2026-08-01Z'),
  ('20000000-0000-4000-8000-000000000103', '10000000-0000-4000-8000-000000000001', 'Mixed', 15, '15', 'ready', '2026-08-01Z', '2026-08-01Z'),
  ('20000000-0000-4000-8000-000000000104', '10000000-0000-4000-8000-000000000001', 'Published', 16, '16', 'ready', '2026-08-01Z', '2026-08-01Z');

INSERT INTO chapters (id, project_id, book_id, title, "order", status, revision, created_at, updated_at)
SELECT ('30000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
       '10000000-0000-4000-8000-000000000001',
       ('20000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
       'Chapter ' || value, 1, 'draft', 1, '2026-08-01Z', '2026-08-01Z'
FROM generate_series(1, 12) AS value;
INSERT INTO chapters (id, project_id, book_id, title, "order", status, revision, published_revision, published_at, created_at, updated_at) VALUES
  ('30000000-0000-4000-8000-000000000101', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000101', 'DOCX chapter', 1, 'draft', 1, NULL, NULL, '2026-08-01Z', '2026-08-01Z'),
  ('30000000-0000-4000-8000-000000000102', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000102', 'Manual chapter', 1, 'draft', 1, NULL, NULL, '2026-08-01Z', '2026-08-01Z'),
  ('30000000-0000-4000-8000-000000000103', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000103', 'Mixed published', 1, 'published', 1, 1, '2026-08-02Z', '2026-08-01Z', '2026-08-02Z'),
  ('30000000-0000-4000-8000-000000000104', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000103', 'Mixed draft', 2, 'draft', 1, NULL, NULL, '2026-08-01Z', '2026-08-01Z'),
  ('30000000-0000-4000-8000-000000000105', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000104', 'Already published', 1, 'published', 1, 1, '2026-08-02Z', '2026-08-01Z', '2026-08-02Z');

INSERT INTO chapter_paragraphs (id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at)
SELECT ('40000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
       ('30000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
       1, 'draft', 'paragraph', 0, 'draft ' || value, 'draft ' || value, 2, '2026-08-01Z', '2026-08-01Z'
FROM generate_series(1, 12) AS value;
INSERT INTO chapter_paragraphs (id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at) VALUES
  ('40000000-0000-4000-8000-000000000101', '30000000-0000-4000-8000-000000000101', 1, 'draft', 'paragraph', 0, 'docx', 'docx', 1, '2026-08-01Z', '2026-08-01Z'),
  ('40000000-0000-4000-8000-000000000102', '30000000-0000-4000-8000-000000000102', 1, 'draft', 'paragraph', 0, 'manual', 'manual', 1, '2026-08-01Z', '2026-08-01Z'),
  ('40000000-0000-4000-8000-000000000103', '30000000-0000-4000-8000-000000000103', 1, 'draft', 'paragraph', 0, 'mixed one', 'mixed one', 2, '2026-08-01Z', '2026-08-01Z'),
  ('40000000-0000-4000-8000-000000000103', '30000000-0000-4000-8000-000000000103', 1, 'published', 'paragraph', 0, 'mixed one', 'mixed one', 2, '2026-08-01Z', '2026-08-01Z'),
  ('40000000-0000-4000-8000-000000000104', '30000000-0000-4000-8000-000000000104', 1, 'draft', 'paragraph', 0, 'mixed two', 'mixed two', 2, '2026-08-01Z', '2026-08-01Z'),
  ('40000000-0000-4000-8000-000000000105', '30000000-0000-4000-8000-000000000105', 1, 'draft', 'paragraph', 0, 'published', 'published', 1, '2026-08-01Z', '2026-08-01Z'),
  ('40000000-0000-4000-8000-000000000105', '30000000-0000-4000-8000-000000000105', 1, 'published', 'paragraph', 0, 'published', 'published', 1, '2026-08-01Z', '2026-08-01Z');

INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision, created_at) VALUES
  ('10000000-0000-4000-8000-000000000001', 'draft', 1, 7, '2026-08-01Z'),
  ('10000000-0000-4000-8000-000000000001', 'published', 1, 3, '2026-08-02Z');
INSERT INTO book_source_members (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation, title)
SELECT project_id, 'draft', 1, id, 1, title FROM books WHERE project_id='10000000-0000-4000-8000-000000000001';
INSERT INTO chapter_source_members (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation, chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order)
SELECT project_id, 'draft', 1, book_id, 1, id, 1, revision, title, "order"
FROM chapters WHERE project_id='10000000-0000-4000-8000-000000000001';
INSERT INTO book_source_members VALUES
  ('10000000-0000-4000-8000-000000000001', 'published', 1, '20000000-0000-4000-8000-000000000103', 1, 'Mixed'),
  ('10000000-0000-4000-8000-000000000001', 'published', 1, '20000000-0000-4000-8000-000000000104', 1, 'Published');
INSERT INTO chapter_source_members VALUES
  ('10000000-0000-4000-8000-000000000001', 'published', 1, '20000000-0000-4000-8000-000000000103', 1, '30000000-0000-4000-8000-000000000103', 1, 1, 'Mixed published', 1),
  ('10000000-0000-4000-8000-000000000001', 'published', 1, '20000000-0000-4000-8000-000000000104', 1, '30000000-0000-4000-8000-000000000105', 1, 1, 'Already published', 1);

WITH lineage(book_id, job_id, owner_id, file_name, completed_at) AS (
  SELECT ('20000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
         ('50000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
         ('60000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
         CASE WHEN value=12 THEN 'volume-12.EPUB' ELSE 'volume-' || value || '.fb2' END,
         '2026-08-03Z'::timestamptz + value * interval '1 minute'
  FROM generate_series(1, 12) AS value
  UNION ALL VALUES
    ('20000000-0000-4000-8000-000000000101'::uuid, '50000000-0000-4000-8000-000000000101'::uuid, '60000000-0000-4000-8000-000000000101'::uuid, 'draft.docx', '2026-08-03Z'::timestamptz),
    ('20000000-0000-4000-8000-000000000103'::uuid, '50000000-0000-4000-8000-000000000103'::uuid, '60000000-0000-4000-8000-000000000103'::uuid, 'mixed.fb2', '2026-08-03Z'::timestamptz),
    ('20000000-0000-4000-8000-000000000104'::uuid, '50000000-0000-4000-8000-000000000104'::uuid, '60000000-0000-4000-8000-000000000104'::uuid, 'published.fb2', '2026-08-03Z'::timestamptz)
)
INSERT INTO jobs (id, project_id, kind, status, progress_json, result_json, error_json, started_at, completed_at, created_at, updated_at, claim_incarnation, artifact_source_owner_id, governance_payload_state, subject_json, creator_user_id, can_cancel, queue_class, retry_count, max_attempts, next_attempt_at)
SELECT job_id, '10000000-0000-4000-8000-000000000001', 'import', 'succeeded', '{}'::jsonb,
       jsonb_build_object('type','import','bookId',book_id::text,'chapterCount',1,'paragraphCount',1,'importMode','new_book','sourceFileName',file_name),
       NULL, completed_at - interval '1 minute', completed_at, completed_at - interval '2 minutes', completed_at,
       1, owner_id, 'active', jsonb_build_object('type','import','id',job_id::text,'projectId','10000000-0000-4000-8000-000000000001'),
       '10000000-0000-4000-8000-000000000002', false, 'user_demanded', 0, 3, completed_at - interval '2 minutes'
FROM lineage;

INSERT INTO chapter_text_index_state (project_id, source_view, chapter_id, source_revision, locator_source_generation, chapter_content_hash, indexed_at)
VALUES ('10000000-0000-4000-8000-000000000001', 'draft', '30000000-0000-4000-8000-000000000102', 1, decode(repeat('11',32),'hex'), decode(repeat('22',32),'hex'), '2026-08-04Z');
INSERT INTO chapter_text_search_rows (project_id, source_view, book_id, chapter_id, source_revision, locator_source_generation, title, content, chapter_content_hash, indexed_at)
VALUES ('10000000-0000-4000-8000-000000000001', 'draft', '20000000-0000-4000-8000-000000000102', '30000000-0000-4000-8000-000000000102', 1, decode(repeat('11',32),'hex'), 'Manual chapter', 'manual', decode(repeat('22',32),'hex'), '2026-08-04Z');

COMMIT;`)
	if err != nil {
		t.Fatalf("seed publication backfill: %v", err)
	}
}

func readPublicationProtectedState(t *testing.T, db *storedb.DB) publicationProtectedState {
	t.Helper()
	var state publicationProtectedState
	err := db.Pool.QueryRow(context.Background(), `
SELECT
  COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id)::text FROM jobs AS row WHERE project_id=$1), '[]'),
  COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.chapter_id)::text FROM chapter_text_index_state AS row WHERE project_id=$1), '[]'),
  COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.chapter_id)::text FROM chapter_text_search_rows AS row WHERE project_id=$1), '[]'),
  COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.chapter_id, row.id)::text FROM chapter_paragraphs AS row WHERE variant='draft'), '[]'),
  COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'generation',lifecycle_generation,'revision',revision) ORDER BY id)::text FROM chapters WHERE project_id=$1), '[]'),
  COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'status',status,'generation',lifecycle_generation,'revision',revision) ORDER BY id)::text FROM books WHERE project_id=$1), '[]'),
  (SELECT count(*) FROM canon_graph_rows WHERE project_id=$1),
  (SELECT count(*) FROM canon_author_rows WHERE project_id=$1),
  (SELECT count(*) FROM chapter_summaries WHERE project_id=$1)
`, publicationBackfillProjectID).Scan(
		&state.jobs, &state.indexState, &state.indexRows, &state.draftParagraphs, &state.lifecycle, &state.books,
		&state.canonGraph, &state.canonAuthor, &state.summaries,
	)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertImportedPublicationBackfill(t *testing.T, db *storedb.DB) {
	t.Helper()
	var chapters, paragraphPairs, bookMembers, chapterMembers, catalogRevision int
	if err := db.Pool.QueryRow(context.Background(), `
SELECT
  (SELECT count(*) FROM chapters AS chapter JOIN books AS book ON book.id=chapter.book_id AND book.project_id=chapter.project_id
   JOIN jobs AS job ON job.project_id=book.project_id AND job.kind='import' AND job.status='succeeded' AND job.result_json->>'bookId'=book.id::text
   WHERE book.project_id=$1 AND book."order" BETWEEN 1 AND 12 AND chapter.status='published'
     AND chapter.published_revision=chapter.revision AND chapter.published_at=job.completed_at),
  (SELECT count(*) FROM books AS book JOIN chapters AS chapter ON chapter.book_id=book.id AND chapter.project_id=book.project_id
   JOIN chapter_paragraphs AS draft ON draft.chapter_id=chapter.id AND draft.revision=chapter.revision AND draft.variant='draft'
   JOIN chapter_paragraphs AS published ON published.id=draft.id AND published.chapter_id=draft.chapter_id AND published.revision=draft.revision AND published.variant='published'
   WHERE book.project_id=$1 AND book."order" BETWEEN 1 AND 12
     AND (published.kind,published."order",published.markdown,published.plain_text,published.word_count,published.created_at,published.updated_at)
       IS NOT DISTINCT FROM (draft.kind,draft."order",draft.markdown,draft.plain_text,draft.word_count,draft.created_at,draft.updated_at)),
  (SELECT count(*) FROM book_source_members AS member JOIN books AS book ON book.id=member.book_id AND book.project_id=member.project_id
   WHERE member.project_id=$1 AND member.source_view='published' AND book."order" BETWEEN 1 AND 12),
  (SELECT count(*) FROM chapter_source_members AS member JOIN books AS book ON book.id=member.book_id AND book.project_id=member.project_id
   WHERE member.project_id=$1 AND member.source_view='published' AND book."order" BETWEEN 1 AND 12),
  (SELECT catalog_revision FROM project_source_views WHERE project_id=$1 AND source_view='published')
`, publicationBackfillProjectID).Scan(&chapters, &paragraphPairs, &bookMembers, &chapterMembers, &catalogRevision); err != nil {
		t.Fatal(err)
	}
	if chapters != 12 || paragraphPairs != 12 || bookMembers != 12 || chapterMembers != 12 || catalogRevision != 4 {
		t.Fatalf("eligible state chapters/pairs/books/members/catalog = %d/%d/%d/%d/%d", chapters, paragraphPairs, bookMembers, chapterMembers, catalogRevision)
	}

	assertPublicationChapterState(t, db, "30000000-0000-4000-8000-000000000101", "draft", 0)
	assertPublicationChapterState(t, db, "30000000-0000-4000-8000-000000000102", "draft", 0)
	assertPublicationChapterState(t, db, "30000000-0000-4000-8000-000000000103", "published", 1)
	assertPublicationChapterState(t, db, "30000000-0000-4000-8000-000000000104", "draft", 0)
	assertPublicationChapterState(t, db, "30000000-0000-4000-8000-000000000105", "published", 1)
}

func assertPublicationChapterState(t *testing.T, db *storedb.DB, chapterID, wantStatus string, wantPublished int) {
	t.Helper()
	var status string
	var publishedRows int
	if err := db.Pool.QueryRow(context.Background(), `
SELECT status, (SELECT count(*) FROM chapter_paragraphs WHERE chapter_id=chapter.id AND variant='published')
FROM chapters AS chapter WHERE id=$1
`, chapterID).Scan(&status, &publishedRows); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || publishedRows != wantPublished {
		t.Fatalf("chapter %s status/published = %s/%d, want %s/%d", chapterID, status, publishedRows, wantStatus, wantPublished)
	}
}
