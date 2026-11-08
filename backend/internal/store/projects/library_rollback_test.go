package projectstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"
)

type phase04LibraryRollbackFixture struct {
	db                         *storedb.DB
	actorID, projectID, bookID string
	chapterID, paragraphID     string
	now                        time.Time
}

func newPhase04LibraryRollbackFixture(t *testing.T) phase04LibraryRollbackFixture {
	t.Helper()
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	fixture := phase04LibraryRollbackFixture{
		db: db, actorID: ids.NewProjectID(), projectID: ids.NewProjectID(), bookID: ids.NewProjectID(),
		chapterID: ids.NewProjectID(), paragraphID: ids.NewProjectID(), now: time.Now().UTC().Truncate(time.Microsecond),
	}
	cleanupProjectsStoreRows(t, db, fixture.actorID, fixture.projectID)
	insertProjectStoreUserForTest(t, db, fixture.actorID, fixture.actorID+"@example.test")
	if _, err := New(db).CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
		ProjectID: fixture.projectID, OwnerUserID: fixture.actorID, OwnerMembershipID: ids.NewProjectMembershipID(),
		Title: "Rollback project", CreatedAt: fixture.now,
	}); err != nil {
		t.Fatalf("create rollback project: %v", err)
	}
	store := librarystore.New(db)
	if _, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID: fixture.bookID, ProjectID: fixture.projectID, Title: "Rollback book", Order: 1, DisplayNumber: "1", CreatedAt: fixture.now,
	}); err != nil {
		t.Fatalf("create rollback book: %v", err)
	}
	if _, err := store.CreateChapterWithDraft(ctx, library.CreateChapterWithDraftStoreInput{
		ID: fixture.chapterID, ProjectID: fixture.projectID, BookID: fixture.bookID, Title: "Rollback chapter", Order: 1, CreatedAt: fixture.now,
		Paragraphs: []library.Paragraph{{
			ID: fixture.paragraphID, ChapterID: fixture.chapterID, Revision: 1, Variant: library.ParagraphVariantDraft,
			Kind: library.ParagraphKindParagraph, Order: 1, Markdown: "before", PlainText: "before", WordCount: 1,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		}},
	}); err != nil {
		t.Fatalf("create rollback chapter: %v", err)
	}
	return fixture
}

func phase04LibrarySourceStateForTest(t *testing.T, fixture phase04LibraryRollbackFixture) string {
	t.Helper()
	var state string
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT jsonb_build_object(
		  'project', (SELECT to_jsonb(project) FROM projects AS project WHERE id = $1),
		  'books', COALESCE((SELECT jsonb_agg(to_jsonb(book) ORDER BY id) FROM books AS book WHERE project_id = $1), '[]'::jsonb),
		  'chapters', COALESCE((SELECT jsonb_agg(to_jsonb(chapter) ORDER BY id) FROM chapters AS chapter WHERE project_id = $1), '[]'::jsonb),
		  'paragraphs', COALESCE((SELECT jsonb_agg(to_jsonb(paragraph) ORDER BY paragraph.id, paragraph.revision, paragraph.variant)
		    FROM chapter_paragraphs AS paragraph JOIN chapters AS chapter ON chapter.id = paragraph.chapter_id WHERE chapter.project_id = $1), '[]'::jsonb),
		  'markers', COALESCE((SELECT jsonb_agg(to_jsonb(marker) ORDER BY source_view) FROM project_source_views AS marker WHERE project_id = $1), '[]'::jsonb),
		  'books_catalog', COALESCE((SELECT jsonb_agg(to_jsonb(member) ORDER BY source_view, book_id) FROM book_source_members AS member WHERE project_id = $1), '[]'::jsonb),
		  'chapters_catalog', COALESCE((SELECT jsonb_agg(to_jsonb(member) ORDER BY source_view, book_id, chapter_id) FROM chapter_source_members AS member WHERE project_id = $1), '[]'::jsonb)
		)::text
	`, fixture.projectID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
