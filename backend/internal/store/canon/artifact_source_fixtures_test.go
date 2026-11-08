package canonstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

const (
	artifactConstraintUserID       = "00000000-0000-4000-8000-000000000a01"
	artifactConstraintProjectID    = "00000000-0000-4000-8000-000000000a02"
	artifactConstraintJobID        = "00000000-0000-4000-8000-000000000a03"
	artifactConstraintOwnerID      = "00000000-0000-4000-8000-000000000a04"
	artifactConstraintRefID        = "00000000-0000-4000-8000-000000000a05"
	artifactConstraintOtherID      = "00000000-0000-4000-8000-000000000a06"
	artifactConstraintThirdID      = "00000000-0000-4000-8000-000000000a07"
	artifactConstraintChatID       = "00000000-0000-4000-8000-000000000a08"
	artifactConstraintBookID       = "00000000-0000-4000-8000-000000000a09"
	artifactConstraintChapterID    = "00000000-0000-4000-8000-000000000a0a"
	artifactConstraintParaID       = "00000000-0000-4000-8000-000000000a0b"
	artifactConstraintChunkID      = "00000000-0000-4000-8000-000000000a0c"
	artifactConstraintBadChunk     = "00000000-0000-4000-8000-000000000a0d"
	artifactConstraintChunkOwnerID = "00000000-0000-4000-8000-000000000a0e"
	artifactConstraintChunkRefID   = "00000000-0000-4000-8000-000000000a0f"
	canonicalStoreOwnerID          = artifactConstraintChunkOwnerID
)

func openArtifactConstraintTestDB(t *testing.T) *Store {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	insertProjectStoreUserForTest(t, db, artifactConstraintUserID, "artifact-constraints@example.com")
	insertProjectStoreProjectForTest(t, db, artifactConstraintProjectID, artifactConstraintUserID, "Artifact Constraints", now)
	return db
}

func seedArtifactConstraintExactSource(t *testing.T, db *Store, _ bool) governance.ChapterSourceRefInput {
	t.Helper()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	insertBookForExportStoreTest(t, db, artifactConstraintBookID, artifactConstraintProjectID, "Exact Source Book", now)
	insertChapterForExportStoreTest(t, db, artifactConstraintChapterID, artifactConstraintBookID, "Exact Source Chapter", 0, "draft", 1, nil, nil, now)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (
			id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count
		) VALUES ($1, $2, 1, 'draft', 'paragraph', 0, 'exact canonical content', 'exact canonical content', 3)
	`, artifactConstraintParaID, artifactConstraintChapterID); err != nil {
		t.Fatalf("insert exact source paragraph: %v", err)
	}
	vector := governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: 1,
		BookID: artifactConstraintBookID, BookGeneration: 1,
		ChapterID: artifactConstraintChapterID, ChapterGeneration: 1,
	}
	locatorDigest, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	chapterHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: artifactConstraintParaID, Order: 0, Content: "exact canonical content",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO project_source_views (
			project_id, source_view, project_lifecycle_generation, catalog_revision
		) VALUES ($1, 'draft', 1, 1) ON CONFLICT DO NOTHING`, []any{artifactConstraintProjectID}},
		{`INSERT INTO book_source_members (
			project_id, source_view, project_lifecycle_generation,
			book_id, book_lifecycle_generation, title
		) VALUES ($1, 'draft', 1, $2, 1, 'Exact Source Book') ON CONFLICT DO NOTHING`, []any{artifactConstraintProjectID, artifactConstraintBookID}},
		{`INSERT INTO chapter_source_members (
			project_id, source_view, project_lifecycle_generation,
			book_id, book_lifecycle_generation, chapter_id,
			chapter_lifecycle_generation, source_revision, title, chapter_order
		) VALUES ($1, 'draft', 1, $2, 1, $3, 1, 1, 'Exact Source Chapter', 0) ON CONFLICT DO NOTHING`, []any{
			artifactConstraintProjectID, artifactConstraintBookID, artifactConstraintChapterID,
		}},
		{`INSERT INTO chapter_text_search_rows (
			project_id, source_view, book_id, chapter_id, source_revision,
			locator_source_generation, title, content, chapter_content_hash, indexed_at
		) VALUES ($1, 'draft', $2, $3, 1, $4, 'Exact Source Chapter', 'exact canonical content', $5, $6)
		ON CONFLICT DO NOTHING`, []any{
			artifactConstraintProjectID, artifactConstraintBookID, artifactConstraintChapterID,
			locatorDigest[:], chapterHash[:], now,
		}},
		{`INSERT INTO chapter_text_index_state (
			project_id, source_view, chapter_id, source_revision,
			locator_source_generation, chapter_content_hash, indexed_at
		) VALUES ($1, 'draft', $2, 1, $3, $4, $5)
		ON CONFLICT DO NOTHING`, []any{
			artifactConstraintProjectID, artifactConstraintChapterID,
			locatorDigest[:], chapterHash[:], now,
		}},
	} {
		if _, err := db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatalf("insert exact source catalog: %v", err)
		}
	}
	return governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 1, SourceView: view,
		SourceGenerationDigest: locatorDigest, ContentHash: chapterHash,
		Chat: governance.AbsentChatArm(),
	}
}

func seedCanonicalSourceStoreFixture(t *testing.T, db *Store) governance.CanonicalChapterSourceLocatorV1 {
	t.Helper()
	input := seedArtifactConstraintExactSource(t, db, false)
	return governance.CanonicalChapterSourceLocatorV1{
		ProjectID: input.Source.ProjectID, BookID: input.Source.BookID, ChapterID: input.Source.ChapterID, SourceRef: input.Source.ChapterID,
		SourceView: governance.SourceViewDraft, SourceRevision: input.SourceRevision,
		SourceGeneration: input.SourceGenerationDigest, ContentHash: input.ContentHash,
	}
}

func insertBookForExportStoreTest(t *testing.T, db *Store, bookID, projectID, title string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, subtitle, cover_color, "order", display_number, status, revision, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, NULL, 1, '1', 'draft', 1, $4, $4)
	`, bookID, projectID, title, now); err != nil {
		t.Fatalf("insert book: %v", err)
	}
}

func insertChapterForExportStoreTest(t *testing.T, db *Store, chapterID, bookID, title string, order int, status string, revision int64, publishedRevision *int64, deletedAt *time.Time, now time.Time) {
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
