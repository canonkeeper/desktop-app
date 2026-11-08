package librarystore

import (
	"strings"
	"testing"
)

func TestLibraryQueriesUseActiveAncestorChains(t *testing.T) {
	sql := normalizeSQL(readStoreSQLFile(t, "db", "queries", "library.sql"))
	for _, want := range []string{
		"FROM projects AS project WHERE project.id = sqlc.arg(project_id) AND project.deleted_at IS NULL RETURNING",
		"FROM books b JOIN projects p ON p.id = b.project_id LEFT JOIN LATERAL",
		"WHERE b.project_id = sqlc.arg(project_id) AND b.id = sqlc.arg(book_id) AND b.deleted_at IS NULL AND p.deleted_at IS NULL;",
		"UPDATE books b SET title = sqlc.arg(title), subtitle = sqlc.arg(subtitle), cover_color = sqlc.arg(cover_color), revision = b.revision + 1, updated_at = sqlc.arg(updated_at) FROM projects p WHERE b.project_id = sqlc.arg(project_id) AND b.id = sqlc.arg(book_id) AND b.project_id = p.id AND p.lifecycle_generation = sqlc.arg(project_generation) AND b.lifecycle_generation = sqlc.arg(book_generation) AND b.deleted_at IS NULL AND p.deleted_at IS NULL RETURNING",
		"FROM books AS book JOIN projects AS project ON project.id = book.project_id WHERE book.project_id = sqlc.arg(project_id) AND book.id = sqlc.arg(book_id) AND book.deleted_at IS NULL AND project.deleted_at IS NULL RETURNING",
		"FROM chapters c JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id JOIN projects p ON p.id = c.project_id WHERE c.project_id = sqlc.arg(project_id) AND c.book_id = sqlc.arg(book_id) AND c.deleted_at IS NULL AND b.deleted_at IS NULL AND p.deleted_at IS NULL",
		"FROM chapters c JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id JOIN projects p ON p.id = c.project_id WHERE c.project_id = sqlc.arg(project_id) AND c.book_id = sqlc.arg(book_id) AND c.id = sqlc.arg(chapter_id) AND c.deleted_at IS NULL AND b.deleted_at IS NULL AND p.deleted_at IS NULL",
		"FROM navigation AS nav JOIN chapters c ON c.id = nav.id WHERE c.id = sqlc.arg(chapter_id)",
		"FROM chapter_paragraphs p JOIN chapters c ON c.id = p.chapter_id JOIN books b ON b.id = c.book_id AND b.project_id = c.project_id JOIN projects pr ON pr.id = c.project_id WHERE c.project_id = sqlc.arg(project_id) AND c.book_id = sqlc.arg(book_id) AND p.chapter_id = sqlc.arg(chapter_id) AND p.revision = sqlc.arg(revision) AND p.variant = sqlc.arg(variant) AND c.deleted_at IS NULL AND b.deleted_at IS NULL AND pr.deleted_at IS NULL",
	} {
		requireSQLContains(t, sql, normalizeSQL(want))
	}
	for _, forbidden := range []string{"-- name: SoftDeleteBook :", "-- name: GetChapterParagraphByID :"} {
		if strings.Contains(sql, normalizeSQL(forbidden)) {
			t.Fatalf("library query source retains obsolete child-ID-only query %q", forbidden)
		}
	}
}

func TestLibraryQueriesExposeManuscriptContractFields(t *testing.T) {
	sql := normalizeSQL(readStoreSQLFile(t, "db", "queries", "library.sql"))
	for _, want := range []string{
		"subtitle,",
		"cover_color,",
		"sqlc.arg(subtitle)",
		"sqlc.arg(cover_color)",
		"RETURNING id, project_id, title, subtitle, cover_color, \"order\", display_number, status, revision, deleted_at, purge_after, created_at, updated_at, lifecycle_generation, hard_purge_started_at, governance_retention_started_at, purge_deadline",
		"-- name: GetBookSummaryByID :one SELECT b.id, b.project_id, b.title, b.subtitle, b.cover_color, b.\"order\", b.display_number, b.status, b.revision, b.deleted_at, b.purge_after, b.created_at, b.updated_at, COALESCE(cover.version, 0)::bigint AS cover_version",
		"RETURNING id, book_id, title, \"order\", status, revision, published_revision, published_at, publish_error, deleted_at, purge_after, created_at, updated_at, lifecycle_generation, hard_purge_started_at, governance_retention_started_at, purge_deadline, project_id",
		"kind,",
		"sqlc.arg(kind)",
		"p.id, p.chapter_id, p.revision, p.variant, p.kind, p.\"order\", p.markdown, p.plain_text, p.word_count, p.created_at, p.updated_at",
	} {
		requireSQLContains(t, sql, normalizeSQL(want))
	}
}

func TestLibraryQueriesBoundImportUploadContentLifecycle(t *testing.T) {
	sql := normalizeSQL(readStoreSQLFile(t, "db", "queries", "library.sql"))
	for _, want := range []string{
		"expires_at",
		"sqlc.arg(expires_at)",
		"-- name: LoadImportBodyForClaim :one",
		"job.claim_incarnation = sqlc.arg(claim_incarnation)",
		"clock_timestamp() < upload.expires_at",
		"ck_artifact_source_owner_is_readable( job.artifact_source_owner_id, job.project_id, 'job', job.id )",
		"job.artifact_source_owner_id IS NOT NULL AND request.artifact_source_owner_id IS NOT NULL AND upload.artifact_source_owner_id IS NOT NULL",
		"-- name: IsImportBodyExpiredForClaim :one",
		"clock_timestamp() >= upload.expires_at",
	} {
		requireSQLContains(t, sql, normalizeSQL(want))
	}
	if strings.Contains(sql, normalizeSQL("job.artifact_source_owner_id IS NULL AND request.artifact_source_owner_id IS NULL AND upload.artifact_source_owner_id IS NULL")) {
		t.Fatal("library query source accepts an ownerless import claim")
	}
	if strings.Contains(sql, normalizeSQL("-- name: PurgeExpiredImportUploadContent")) {
		t.Fatal("library query source retains the removed ambient import body purge")
	}
	if strings.Contains(sql, normalizeSQL("-- name: GetImportFile")) {
		t.Fatal("library query source retains the removed project/key-only import body reader")
	}
}
