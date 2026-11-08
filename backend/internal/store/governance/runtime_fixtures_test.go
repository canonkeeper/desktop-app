package governancestore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
)

func insertProjectStoreUserForTest(t *testing.T, db *storedb.DB, userID, email string) {
	t.Helper()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, $2, 'Project Owner', 'test-password-hash', $3)
	`, userID, email, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertProjectStoreProjectForTest(t *testing.T, db *storedb.DB, projectID, ownerUserID, title string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, '', $4, $4)
	`, projectID, ownerUserID, title, now); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision)
		SELECT id, 'draft', lifecycle_generation, 1 FROM projects WHERE id = $1
	`, projectID); err != nil {
		t.Fatalf("insert project draft source marker: %v", err)
	}
}

func insertBookForExportStoreTest(t *testing.T, db *storedb.DB, bookID, projectID, title string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, subtitle, cover_color, "order", display_number, status, revision, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, NULL, 1, '1', 'draft', 1, $4, $4)
	`, bookID, projectID, title, now); err != nil {
		t.Fatalf("insert book: %v", err)
	}
}

func insertChapterForExportStoreTest(t *testing.T, db *storedb.DB, chapterID, bookID, title string, order int, status string, revision int64, publishedRevision *int64, deletedAt *time.Time, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapters (
			id, project_id, book_id, title, "order", status, revision,
			published_revision, published_at, deleted_at,
			governance_retention_started_at, purge_after, purge_deadline,
			created_at, updated_at
		)
		VALUES (
			$1, (SELECT project_id FROM books WHERE id = $2), $2, $3, $4, $5, $6,
			$7, $8, $9,
			$9, CASE WHEN $9::timestamptz IS NULL THEN NULL ELSE $9 + interval '7 days' END,
			CASE WHEN $9::timestamptz IS NULL THEN NULL ELSE $9 + interval '8 days' END,
			$10, $10
		)
	`, chapterID, bookID, title, order, status, revision, publishedRevision, now, deletedAt, now); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

func insertParagraphForExportStoreTest(t *testing.T, db *storedb.DB, paragraphID, chapterID string, revision int64, variant library.ParagraphVariant, order int, text string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (
			id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 'paragraph', $5, $6, $6, 1, $7, $7)
	`, paragraphID, chapterID, revision, string(variant), order, text, now); err != nil {
		t.Fatalf("insert paragraph: %v", err)
	}
}
