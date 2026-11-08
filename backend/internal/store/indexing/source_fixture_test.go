package indexingstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

type indexingStoreFixtureForTest struct {
	actorID      string
	projectID    string
	membershipID string
	bookID       string
	chapterID    string
	paragraphID  string
	sessionProof auth.SessionWriteProof
}

func createIndexingStoreFixtureForTest(t *testing.T, db *storedb.DB, base int) indexingStoreFixtureForTest {
	t.Helper()
	fixture := indexingStoreFixtureForTest{
		actorID:      indexingStoreUUIDForTest(base + 1),
		projectID:    indexingStoreUUIDForTest(base + 2),
		membershipID: indexingStoreUUIDForTest(base + 3),
		bookID:       indexingStoreUUIDForTest(base + 4),
		chapterID:    indexingStoreUUIDForTest(base + 5),
		paragraphID:  indexingStoreUUIDForTest(base + 6),
	}
	now := indexingStoreTestNow()
	fixture.sessionProof = insertProjectStoreUserWithSessionForTest(t, db, fixture.actorID, fmt.Sprintf("indexing-%d@example.com", base))
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Indexing Project", now)
	insertProjectStoreMembershipForTest(t, db, fixture.membershipID, fixture.projectID, fixture.actorID, projects.RoleEditor, now, false)
	insertIndexingStoreBookForTest(t, db, fixture.bookID, fixture.projectID, now)
	insertIndexingStoreChapterForTest(t, db, fixture.projectID, fixture.chapterID, fixture.bookID, now)
	insertIndexingStoreParagraphForTest(t, db, fixture.paragraphID, fixture.chapterID, now)
	seedIndexingCatalogForTest(t, db, fixture)
	return fixture
}

func seedIndexingCatalogForTest(t *testing.T, db *storedb.DB, fixture indexingStoreFixtureForTest) {
	t.Helper()
	err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO book_source_members (
			  project_id, source_view, project_lifecycle_generation,
			  book_id, book_lifecycle_generation, title
			) VALUES ($1, 'draft', 1, $2, 1, 'Indexing Book')
		`, fixture.projectID, fixture.bookID); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `
			INSERT INTO chapter_source_members (
			  project_id, source_view, project_lifecycle_generation,
			  book_id, book_lifecycle_generation,
			  chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order
			) VALUES ($1, 'draft', 1, $2, 1, $3, 1, 1, 'Indexing Chapter', 1)
		`, fixture.projectID, fixture.bookID, fixture.chapterID)
		return err
	})
	if err != nil {
		t.Fatalf("seed indexing catalog: %v", err)
	}
}

func insertIndexingStoreBookForTest(t *testing.T, db *storedb.DB, bookID, projectID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
		VALUES ($1, $2, 'Indexing Book', 1, '1', $3, $3)
	`, bookID, projectID, now); err != nil {
		t.Fatalf("insert book: %v", err)
	}
}

func insertIndexingStoreChapterForTest(t *testing.T, db *storedb.DB, projectID, chapterID, bookID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapters (id, project_id, book_id, title, "order", revision, created_at, updated_at)
		VALUES ($1, $2, $3, 'Indexing Chapter', 1, 1, $4, $4)
	`, chapterID, projectID, bookID, now); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

func insertIndexingStoreParagraphForTest(t *testing.T, db *storedb.DB, paragraphID, chapterID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at)
		VALUES ($1, $2, 1, 'draft', 'paragraph', 1, 'dragon', 'dragon', 1, $3, $3)
	`, paragraphID, chapterID, now); err != nil {
		t.Fatalf("insert paragraph: %v", err)
	}
}

func indexingStoreUUIDForTest(value int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
}

func indexingStoreTestNow() time.Time {
	return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
}
