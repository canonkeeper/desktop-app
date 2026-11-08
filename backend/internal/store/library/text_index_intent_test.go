package librarystore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
)

type textIndexIntentForTest struct {
	Status   string
	View     governance.SourceView
	Revision int64
}

func TestLibraryTextIndexIntentTracksWriteModesAndRollback(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	fixture := createTextIndexChapterFixtureForTest(t, db, 4100, false)
	store := New(db)

	if intents := textIndexIntentsForTest(t, db, fixture.projectID); len(intents) != 1 {
		t.Fatalf("intents after chapter create = %#v, want one draft intent", intents)
	}
	markQueuedTextIntentsSucceededForTest(t, db, fixture.projectID)

	titleOnly := fixture.paragraph
	titleOnly.Revision = 2
	titleOnly.UpdatedAt = fixture.now.Add(time.Minute)
	if _, err := store.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
		ExpectedRevision: 1, Revision: 2, Title: "Новое название",
		Status: library.ChapterStatusDraft, UpdatedAt: titleOnly.UpdatedAt,
		Paragraphs: []library.Paragraph{titleOnly},
	}); err != nil {
		t.Fatalf("SaveChapterDraft(title only) error = %v", err)
	}
	if intents := textIndexIntentsForTest(t, db, fixture.projectID); len(intents) != 1 || intents[0].Status != "succeeded" {
		t.Fatalf("intents after title-only save = %#v, want no new intent", intents)
	}

	changed := titleOnly
	changed.Revision = 3
	changed.Markdown = "изменённый текст"
	changed.PlainText = "изменённый текст"
	changed.WordCount = 2
	changed.UpdatedAt = fixture.now.Add(2 * time.Minute)
	if _, err := store.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
		ExpectedRevision: 2, Revision: 3, Title: "Новое название",
		Status: library.ChapterStatusDraft, UpdatedAt: changed.UpdatedAt,
		Paragraphs: []library.Paragraph{changed},
	}); err != nil {
		t.Fatalf("SaveChapterDraft(text change) error = %v", err)
	}
	draftRevisionBeforeRollback := queuedTextIndexRevisionForTest(t, db, fixture.projectID, governance.SourceViewDraft)

	rollbackErr := errors.New("rollback intent")
	rollbackStore := New(db, withDeletionFailpoint(func(point string) error {
		if point == "save_chapter_after_catalog_mutation" {
			return rollbackErr
		}
		return nil
	}))
	changed.Revision = 4
	changed.Markdown = "не должен сохраниться"
	changed.PlainText = "не должен сохраниться"
	changed.UpdatedAt = fixture.now.Add(3 * time.Minute)
	_, err := rollbackStore.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
		ExpectedRevision: 3, Revision: 4, Title: "Новое название",
		Status: library.ChapterStatusDraft, UpdatedAt: changed.UpdatedAt,
		Paragraphs: []library.Paragraph{changed},
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("SaveChapterDraft(rollback) error = %v, want rollback error", err)
	}
	if got := queuedTextIndexRevisionForTest(t, db, fixture.projectID, governance.SourceViewDraft); got != draftRevisionBeforeRollback {
		t.Fatalf("draft intent revision after rollback = %d, want %d", got, draftRevisionBeforeRollback)
	}
	chapter, err := store.GetChapter(ctx, fixture.projectID, fixture.bookID, fixture.chapterID)
	if err != nil || chapter.Chapter.Revision != 3 {
		t.Fatalf("chapter after rollback = %#v, error = %v", chapter.Chapter, err)
	}

	if _, err := store.PublishChapter(ctx, library.PublishChapterStoreInput{
		ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
		Revision: 3, PublishedAt: fixture.now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("PublishChapter() error = %v", err)
	}
	assertQueuedTextIntentsMatchCatalogForTest(t, db, fixture.projectID, []governance.SourceView{
		governance.SourceViewDraft,
		governance.SourceViewPublished,
	})
}

func TestLibraryTextIndexIntentDeleteAndRestoreExactViews(t *testing.T) {
	for _, test := range []struct {
		name       string
		base       int
		keepActive bool
	}{
		{name: "active work gets successors and restore refreshes both views", base: 4200, keepActive: true},
		{name: "no active work gets no successor", base: 4300, keepActive: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			fixture := createTextIndexChapterFixtureForTest(t, db, test.base, true)
			store := New(db)
			if test.keepActive {
				markQueuedTextIntentsRunningForTest(t, db, fixture.projectID, fixture.now.Add(time.Minute))
			} else {
				markQueuedTextIntentsSucceededForTest(t, db, fixture.projectID)
			}

			if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID,
				ChapterID: fixture.chapterID, TombstoneID: testUUID(test.base + 10),
				DeletedAt: fixture.now.Add(2 * time.Minute),
			}); err != nil {
				t.Fatalf("FenceChapterDeletion() error = %v", err)
			}

			if !test.keepActive {
				if intents := textIndexIntentsForTest(t, db, fixture.projectID); len(intents) != 2 {
					t.Fatalf("intents after inactive delete = %#v, want only two terminal predecessors", intents)
				}
				return
			}
			assertTextIndexSuccessorsForTest(t, db, fixture.projectID)

			if _, err := store.RestoreChapter(ctx, library.RestoreChapterStoreInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID,
				ChapterID: fixture.chapterID,
			}); err != nil {
				t.Fatalf("RestoreChapter() error = %v", err)
			}
			assertQueuedTextIntentsMatchCatalogForTest(t, db, fixture.projectID, []governance.SourceView{
				governance.SourceViewDraft,
				governance.SourceViewPublished,
			})
		})
	}
}

type textIndexChapterFixtureForTest struct {
	actorID, projectID, bookID, chapterID string
	paragraph                             library.Paragraph
	now                                   time.Time
}

func createTextIndexChapterFixtureForTest(t *testing.T, db *storedb.DB, base int, publish bool) textIndexChapterFixtureForTest {
	t.Helper()
	ctx := context.Background()
	fixture := textIndexChapterFixtureForTest{
		actorID: testUUID(base), projectID: testUUID(base + 1), bookID: testUUID(base + 2),
		chapterID: testUUID(base + 3), now: time.Now().UTC().Truncate(time.Microsecond),
	}
	insertProjectStoreUserForTest(t, db, fixture.actorID, testUUID(base)+"@example.test")
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Text intent project", fixture.now)
	insertProjectStoreMembershipForTest(t, db, testUUID(base+4), fixture.projectID, fixture.actorID, projects.RoleOwner, fixture.now, false)
	store := New(db)
	if _, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID: fixture.bookID, ProjectID: fixture.projectID, Title: "Книга", Order: 1,
		DisplayNumber: "1", CreatedAt: fixture.now,
	}); err != nil {
		t.Fatalf("CreateBook() error = %v", err)
	}
	if intents := textIndexIntentsForTest(t, db, fixture.projectID); len(intents) != 0 {
		t.Fatalf("intents after book create = %#v, want none", intents)
	}
	if _, err := store.UpdateBook(ctx, library.UpdateBookStoreInput{
		ProjectID: fixture.projectID, BookID: fixture.bookID, Title: "Переименованная книга",
		UpdatedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpdateBook(title) error = %v", err)
	}
	if intents := textIndexIntentsForTest(t, db, fixture.projectID); len(intents) != 0 {
		t.Fatalf("intents after book title update = %#v, want none", intents)
	}
	fixture.paragraph = library.Paragraph{
		ID: testUUID(base + 5), ChapterID: fixture.chapterID, Revision: 1,
		Variant: library.ParagraphVariantDraft, Kind: library.ParagraphKindParagraph, Order: 1,
		Markdown: "исходный текст", PlainText: "исходный текст", WordCount: 2,
		CreatedAt: fixture.now, UpdatedAt: fixture.now,
	}
	if _, err := store.CreateChapterWithDraft(ctx, library.CreateChapterWithDraftStoreInput{
		ID: fixture.chapterID, ProjectID: fixture.projectID, BookID: fixture.bookID,
		Title: "Глава", Order: 1, CreatedAt: fixture.now,
		Paragraphs: []library.Paragraph{fixture.paragraph},
	}); err != nil {
		t.Fatalf("CreateChapterWithDraft() error = %v", err)
	}
	if publish {
		if _, err := store.PublishChapter(ctx, library.PublishChapterStoreInput{
			ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
			Revision: 1, PublishedAt: fixture.now.Add(time.Minute),
		}); err != nil {
			t.Fatalf("PublishChapter() error = %v", err)
		}
	}
	return fixture
}

func textIndexIntentsForTest(t *testing.T, db *storedb.DB, projectID string) []textIndexIntentForTest {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
		SELECT status, subject_json->>'sourceView', (subject_json->>'catalogRevision')::bigint
		FROM jobs
		WHERE project_id = $1 AND kind = 'canon_text_index'
		ORDER BY created_at, id
	`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var intents []textIndexIntentForTest
	for rows.Next() {
		var intent textIndexIntentForTest
		if err := rows.Scan(&intent.Status, &intent.View, &intent.Revision); err != nil {
			t.Fatal(err)
		}
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return intents
}

func queuedTextIndexRevisionForTest(t *testing.T, db *storedb.DB, projectID string, view governance.SourceView) int64 {
	t.Helper()
	var revision int64
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT (subject_json->>'catalogRevision')::bigint
		FROM jobs
		WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued'
		  AND subject_json->>'sourceView' = $2
	`, projectID, view).Scan(&revision); err != nil {
		t.Fatalf("read queued %s intent: %v", view, err)
	}
	return revision
}

func markQueuedTextIntentsSucceededForTest(t *testing.T, db *storedb.DB, projectID string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status = 'succeeded', can_cancel = false, progress_json = '{"value":1}'::jsonb,
		    result_json = '{}'::jsonb, updated_at = clock_timestamp()
		WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued'
	`, projectID); err != nil {
		t.Fatal(err)
	}
}

func markQueuedTextIntentsRunningForTest(t *testing.T, db *storedb.DB, projectID string, claimedAt time.Time) {
	t.Helper()
	tag, err := db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status = 'running', claim_incarnation = 1, claimed_at = $2, updated_at = $2
		WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued'
	`, projectID, claimedAt)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 2 {
		t.Fatalf("running text intents = %d, want 2", tag.RowsAffected())
	}
}

func assertQueuedTextIntentsMatchCatalogForTest(t *testing.T, db *storedb.DB, projectID string, views []governance.SourceView) {
	t.Helper()
	for _, view := range views {
		var catalogRevision int64
		if err := db.Pool.QueryRow(context.Background(), `
			SELECT catalog_revision
			FROM project_source_views
			WHERE project_id = $1 AND source_view = $2
		`, projectID, view).Scan(&catalogRevision); err != nil {
			t.Fatalf("read %s catalog revision: %v", view, err)
		}
		if intentRevision := queuedTextIndexRevisionForTest(t, db, projectID, view); intentRevision != catalogRevision {
			t.Fatalf("%s intent revision = %d, want catalog revision %d", view, intentRevision, catalogRevision)
		}
	}
}

func assertTextIndexSuccessorsForTest(t *testing.T, db *storedb.DB, projectID string) {
	t.Helper()
	intents := textIndexIntentsForTest(t, db, projectID)
	if len(intents) != 4 {
		t.Fatalf("intents after active delete = %#v, want two canceling predecessors and two queued successors", intents)
	}
	counts := map[string]int{}
	for _, intent := range intents {
		counts[intent.Status+":"+string(intent.View)]++
	}
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if counts["canceling:"+string(view)] != 1 || counts["queued:"+string(view)] != 1 {
			t.Fatalf("intent statuses by view = %#v", counts)
		}
	}
	assertQueuedTextIntentsMatchCatalogForTest(t, db, projectID, []governance.SourceView{
		governance.SourceViewDraft,
		governance.SourceViewPublished,
	})
}

func testUUID(number int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", number)
}
