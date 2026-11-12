package librarystore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLibraryStoreFenceBookGovernedDeletionCommitsExactTenantFence(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000001101"
	projectA := "00000000-0000-4000-8000-000000001102"
	projectB := "00000000-0000-4000-8000-000000001103"
	bookA := "00000000-0000-4000-8000-000000001104"
	bookB := "00000000-0000-4000-8000-000000001105"
	membershipA := "00000000-0000-4000-8000-000000001106"
	membershipB := "00000000-0000-4000-8000-000000001107"
	tombstoneID := "00000000-0000-4000-8000-000000001108"
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "book-delete-editor@example.test")
	insertProjectStoreProjectForTest(t, db, projectA, actorID, "Book Project A", fencedAt.Add(-2*time.Hour))
	insertProjectStoreProjectForTest(t, db, projectB, actorID, "Book Project B", fencedAt.Add(-2*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipA, projectA, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
	insertProjectStoreMembershipForTest(t, db, membershipB, projectB, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
	baseStore := New(db)
	createBookForOrderTest(t, baseStore, projectA, bookA, "Book A", 1, "1", fencedAt.Add(-time.Hour))
	createBookForOrderTest(t, baseStore, projectB, bookB, "Book B", 1, "1", fencedAt.Add(-time.Hour))

	var projectBBefore, bookBBefore string
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(project)::text FROM projects project WHERE id = $1`, projectB).Scan(&projectBBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(book)::text FROM books book WHERE id = $1`, bookB).Scan(&bookBBefore); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	result, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID,
		ProjectID:   projectA,
		BookID:      bookA,
		TombstoneID: tombstoneID,
		DeletedAt:   fencedAt,
	})
	if err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	wantDeleted := governance.SourceGenerationVector{
		ProjectID: projectA, ProjectGeneration: 1, BookID: bookA, BookGeneration: 1,
	}
	wantCurrent := governance.SourceGenerationVector{
		ProjectID: projectA, ProjectGeneration: 1, BookID: bookA, BookGeneration: 2,
	}
	if result.Deleted != wantDeleted || result.Current != wantCurrent || result.TombstoneID != tombstoneID {
		t.Fatalf("FenceBookDeletion() result = %#v, want %#v -> %#v", result, wantDeleted, wantCurrent)
	}

	var projectGeneration, bookGeneration int64
	var deletedAt, purgeAfter, hardPurgeStarted pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `
		SELECT project.lifecycle_generation, book.lifecycle_generation,
		       book.deleted_at, book.purge_after, book.hard_purge_started_at
		FROM projects project
		JOIN books book ON book.project_id = project.id
		WHERE project.id = $1 AND book.id = $2
	`, projectA, bookA).Scan(&projectGeneration, &bookGeneration, &deletedAt, &purgeAfter, &hardPurgeStarted); err != nil {
		t.Fatal(err)
	}
	if projectGeneration != 1 || bookGeneration != 2 || !deletedAt.Valid || !deletedAt.Time.Equal(fencedAt) ||
		!purgeAfter.Valid || !purgeAfter.Time.Equal(fencedAt.Add(deletion.SourceRestoreWindow)) || hardPurgeStarted.Valid {
		t.Fatalf("book lifecycle = project %d book %d deleted %v purge %v hard %v", projectGeneration, bookGeneration, deletedAt, purgeAfter, hardPurgeStarted)
	}

	var entityKind, selectedBookID string
	var selectedProjectGeneration, selectedBookGeneration int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT entity_kind, book_id::text, project_generation, book_generation
		FROM deletion_tombstones WHERE id = $1 AND project_id = $2
	`, tombstoneID, projectA).Scan(&entityKind, &selectedBookID, &selectedProjectGeneration, &selectedBookGeneration); err != nil {
		t.Fatal(err)
	}
	if entityKind != "book" || selectedBookID != bookA || selectedProjectGeneration != 1 || selectedBookGeneration != 1 {
		t.Fatalf("book tombstone = kind %q book %q vector %d/%d", entityKind, selectedBookID, selectedProjectGeneration, selectedBookGeneration)
	}
	var targets []string
	if err := db.Pool.QueryRow(ctx, `SELECT array_agg(target ORDER BY target) FROM deletion_target_tasks WHERE tombstone_id = $1`, tombstoneID).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if strings.Join(targets, ",") != "filesystem,postgresql,runtime" {
		t.Fatalf("book targets = %v", targets)
	}

	var projectBAfter, bookBAfter string
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(project)::text FROM projects project WHERE id = $1`, projectB).Scan(&projectBAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(book)::text FROM books book WHERE id = $1`, bookB).Scan(&bookBAfter); err != nil {
		t.Fatal(err)
	}
	if projectBAfter != projectBBefore || bookBAfter != bookBBefore {
		t.Fatalf("unrelated tenant changed: project %t book %t", projectBAfter != projectBBefore, bookBAfter != bookBBefore)
	}
}

func TestLibraryStoreCreateBookInitializesCurrentSourceState(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000001111"
	projectID := "00000000-0000-4000-8000-000000001112"
	bookID := "00000000-0000-4000-8000-000000001113"
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "book-create-governance@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Book Create Project", now.Add(-time.Hour))
	store := New(db)
	if _, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID: bookID, ProjectID: projectID, Title: "Typed Book", Order: 1,
		DisplayNumber: "1", CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateBook() error = %v", err)
	}
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT book.lifecycle_generation
		FROM books AS book
		WHERE book.id = $1 AND book.project_id = $2
	`, bookID, projectID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != 1 {
		t.Fatalf("created book lifecycle generation = %d", generation)
	}
	var catalogRevision int64
	var catalogTitle string
	if err := db.Pool.QueryRow(ctx, `
		SELECT marker.catalog_revision, member.title
		FROM project_source_views AS marker
		JOIN book_source_members AS member
		  ON member.project_id = marker.project_id
		 AND member.source_view = marker.source_view
		 AND member.project_lifecycle_generation = marker.project_lifecycle_generation
		WHERE marker.project_id = $1 AND marker.source_view = 'draft' AND member.book_id = $2
	`, projectID, bookID).Scan(&catalogRevision, &catalogTitle); err != nil {
		t.Fatalf("read book draft source member: %v", err)
	}
	if catalogRevision != 2 || catalogTitle != "Typed Book" {
		t.Fatalf("book source catalog = revision %d title %q, want 2 and Typed Book", catalogRevision, catalogTitle)
	}
}

func TestLibraryStorePhase04CatalogTracksChapterDraftAndPublish(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	libraryStore := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := "00000000-0000-4000-8000-000000001121"
	projectID := "00000000-0000-4000-8000-000000001122"
	membershipID := "00000000-0000-4000-8000-000000001123"
	bookID := "00000000-0000-4000-8000-000000001124"
	chapterID := "00000000-0000-4000-8000-000000001125"
	paragraphID := "00000000-0000-4000-8000-000000001126"
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "phase04-catalog@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, userID, "Catalog project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)
	if _, err := libraryStore.CreateBook(ctx, library.CreateBookStoreInput{
		ID: bookID, ProjectID: projectID, Title: "Catalog book", Order: 1, DisplayNumber: "1", CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateBook() error = %v", err)
	}
	paragraph := library.Paragraph{ID: paragraphID, ChapterID: chapterID, Revision: 1, Variant: library.ParagraphVariantDraft,
		Kind: library.ParagraphKindParagraph, Order: 1, Markdown: "source", PlainText: "source", WordCount: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := libraryStore.CreateChapterWithDraft(ctx, library.CreateChapterWithDraftStoreInput{
		ID: chapterID, ProjectID: projectID, BookID: bookID, Title: "Draft title", Order: 1, CreatedAt: now, Paragraphs: []library.Paragraph{paragraph},
	}); err != nil {
		t.Fatalf("CreateChapterWithDraft() error = %v", err)
	}
	paragraph.Revision = 2
	paragraph.UpdatedAt = now.Add(time.Second)
	if _, err := libraryStore.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, ExpectedRevision: 1, Revision: 2,
		Title: "Renamed draft", Status: library.ChapterStatusDraft, UpdatedAt: paragraph.UpdatedAt, Paragraphs: []library.Paragraph{paragraph},
	}); err != nil {
		t.Fatalf("SaveChapterDraft() error = %v", err)
	}
	firstPublished, err := libraryStore.PublishChapter(ctx, library.PublishChapterStoreInput{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, Revision: 2, PublishedAt: now.Add(2 * time.Second),
		Paragraphs: []library.Paragraph{{ID: paragraphID, ChapterID: chapterID, Revision: 2, Variant: library.ParagraphVariantPublished, Markdown: "ignored"}},
	})
	if err != nil {
		t.Fatalf("PublishChapter() error = %v", err)
	}
	var draftRevision, publishedRevision, draftMarker, publishedMarker int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT source_revision FROM chapter_source_members WHERE project_id = $1 AND source_view = 'draft' AND chapter_id = $2),
		  (SELECT source_revision FROM chapter_source_members WHERE project_id = $1 AND source_view = 'published' AND chapter_id = $2),
		  (SELECT catalog_revision FROM project_source_views WHERE project_id = $1 AND source_view = 'draft'),
		  (SELECT catalog_revision FROM project_source_views WHERE project_id = $1 AND source_view = 'published')
	`, projectID, chapterID).Scan(&draftRevision, &publishedRevision, &draftMarker, &publishedMarker); err != nil {
		t.Fatalf("read catalog revisions: %v", err)
	}
	if draftRevision != 1 || publishedRevision != 2 || draftMarker != 4 || publishedMarker != 1 {
		t.Fatalf("catalog revisions = draft source %d published source %d draft marker %d published marker %d", draftRevision, publishedRevision, draftMarker, publishedMarker)
	}
	paragraphs, err := libraryStore.ListChapterParagraphs(ctx, library.ListChapterParagraphsStoreInput{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, Revision: 2, Variant: library.ParagraphVariantPublished,
	})
	if err != nil || len(paragraphs) != 1 || paragraphs[0].Markdown != "source" {
		t.Fatalf("published snapshot = %#v, error = %v", paragraphs, err)
	}
	publishedCatalog := bookCatalogForTest(t, db, projectID, bookID, governance.SourceViewPublished)
	if _, err := libraryStore.UpdateBook(ctx, library.UpdateBookStoreInput{
		ProjectID: projectID, BookID: bookID, Title: "Renamed catalog book", UpdatedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("UpdateBook() error = %v", err)
	}
	draftCatalog := bookCatalogForTest(t, db, projectID, bookID, governance.SourceViewDraft)
	catalogBeforeRepeat := bookCatalogForTest(t, db, projectID, bookID, governance.SourceViewPublished)
	if draftCatalog.title != "Renamed catalog book" || catalogBeforeRepeat.title != "Catalog book" || catalogBeforeRepeat != publishedCatalog {
		t.Fatalf("book catalog after draft rename = draft %#v published %#v, want published unchanged %#v", draftCatalog, catalogBeforeRepeat, publishedCatalog)
	}

	repeatedPublished, err := libraryStore.PublishChapter(ctx, library.PublishChapterStoreInput{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, Revision: 2, PublishedAt: now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("PublishChapter(repeat after metadata drift) error = %v", err)
	}
	repeatedCatalog := bookCatalogForTest(t, db, projectID, bookID, governance.SourceViewPublished)
	paragraphsAfterRepeat, err := libraryStore.ListChapterParagraphs(ctx, library.ListChapterParagraphsStoreInput{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, Revision: 2, Variant: library.ParagraphVariantPublished,
	})
	if err != nil {
		t.Fatalf("ListChapterParagraphs(published after repeat) error = %v", err)
	}
	if repeatedCatalog.title != "Renamed catalog book" || repeatedCatalog == publishedCatalog {
		t.Fatalf("published catalog after repeat = %#v, want renamed and changed from %#v", repeatedCatalog, publishedCatalog)
	}
	if repeatedPublished.Revision != firstPublished.Revision || repeatedPublished.PublishedRevision == nil || firstPublished.PublishedRevision == nil ||
		*repeatedPublished.PublishedRevision != *firstPublished.PublishedRevision || repeatedPublished.PublishedAt == nil || firstPublished.PublishedAt == nil ||
		!repeatedPublished.PublishedAt.Equal(*firstPublished.PublishedAt) || len(paragraphsAfterRepeat) != 1 || paragraphsAfterRepeat[0].ID != paragraphs[0].ID {
		t.Fatalf("repeat publish rewrote immutable snapshot: first=%#v repeated=%#v paragraphs=%#v", firstPublished, repeatedPublished, paragraphsAfterRepeat)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT catalog_revision FROM project_source_views WHERE project_id = $1 AND source_view = 'published'`, projectID).Scan(&publishedMarker); err != nil {
		t.Fatalf("read repeated published marker: %v", err)
	}
	if publishedMarker != 2 {
		t.Fatalf("published marker after metadata repair = %d, want 2", publishedMarker)
	}

	thirdPublished, err := libraryStore.PublishChapter(ctx, library.PublishChapterStoreInput{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, Revision: 2, PublishedAt: now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatalf("PublishChapter(third no-op) error = %v", err)
	}
	thirdCatalog := bookCatalogForTest(t, db, projectID, bookID, governance.SourceViewPublished)
	if err := db.Pool.QueryRow(ctx, `SELECT catalog_revision FROM project_source_views WHERE project_id = $1 AND source_view = 'published'`, projectID).Scan(&publishedMarker); err != nil {
		t.Fatalf("read no-op published marker: %v", err)
	}
	if publishedMarker != 2 || thirdCatalog != repeatedCatalog || thirdPublished.PublishedAt == nil || firstPublished.PublishedAt == nil || !thirdPublished.PublishedAt.Equal(*firstPublished.PublishedAt) {
		t.Fatalf("third publish was not a no-op: marker=%d catalog changed=%t first=%#v third=%#v", publishedMarker, thirdCatalog != repeatedCatalog, firstPublished, thirdPublished)
	}
}

// bookCatalog — строки каталога версии текста, из которых строится манифест книги.
type bookCatalog struct {
	title    string
	chapters string
}

func bookCatalogForTest(t *testing.T, db *storedb.DB, projectID, bookID string, view governance.SourceView) bookCatalog {
	t.Helper()
	var catalog bookCatalog
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT book.title,
		       coalesce(string_agg(
		         chapter.chapter_id::text || ':' || chapter.title || ':' || chapter.source_revision::text || ':' || chapter.chapter_order::text,
		         ',' ORDER BY chapter.chapter_order, chapter.chapter_id
		       ), '')
		FROM book_source_members AS book
		LEFT JOIN chapter_source_members AS chapter
		  ON chapter.project_id = book.project_id AND chapter.source_view = book.source_view AND chapter.book_id = book.book_id
		WHERE book.project_id = $1 AND book.source_view = $2 AND book.book_id = $3
		GROUP BY book.title
	`, projectID, string(view), bookID).Scan(&catalog.title, &catalog.chapters); err != nil {
		t.Fatalf("read %s book catalog: %v", view, err)
	}
	return catalog
}

func TestLibraryStorePublishChapterSerializesPublishersBeforeChapterLock(t *testing.T) {
	fixture := newPhase04LibraryRollbackFixture(t)
	secondChapterID := projects.NewSecureIDGenerator().NewProjectID()
	createChapterForOrderTest(t, New(fixture.db), fixture.projectID, fixture.bookID, secondChapterID, "Second chapter", 2, fixture.now)

	firstLocked := make(chan struct{})
	releaseFirst := make(chan struct{}, 1)
	defer func() {
		select {
		case releaseFirst <- struct{}{}:
		default:
		}
	}()
	var lockCount atomic.Int32
	store := New(fixture.db, withDeletionFailpoint(func(point string) error {
		if point == "publish_chapter_after_source_lock" && lockCount.Add(1) == 1 {
			close(firstLocked)
			<-releaseFirst
		}
		return nil
	}))
	publish := func(chapterID string) error {
		_, err := store.PublishChapter(context.Background(), library.PublishChapterStoreInput{
			ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: chapterID,
			Revision: 1, PublishedAt: fixture.now.Add(time.Second),
		})
		return err
	}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- publish(fixture.chapterID) }()
	select {
	case <-firstLocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first publisher did not lock its chapter")
	}
	go func() { secondDone <- publish(secondChapterID) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := fixture.db.Pool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND pid <> pg_backend_pid()
				  AND wait_event_type = 'Lock'
				  AND (query LIKE '%LockCurrentSourceChaptersForBook%'
				       OR query LIKE '%SELECT lifecycle_generation%FROM books%FOR UPDATE%')
			)
		`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second publisher did not wait on the first publisher")
		}
		time.Sleep(10 * time.Millisecond)
	}
	releaseFirst <- struct{}{}

	for name, done := range map[string]<-chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s PublishChapter() error = %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s PublishChapter() did not complete", name)
		}
	}
}

func TestLibraryStoreCreateChapterWithDraftSerializesConcurrentAppends(t *testing.T) {
	fixture := newPhase04LibraryRollbackFixture(t)
	store := New(fixture.db)
	bookID := projects.NewSecureIDGenerator().NewProjectID()
	createBookForOrderTest(t, store, fixture.projectID, bookID, "Concurrent appends", 2, "2", fixture.now)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstConfig := fixture.db.Pool.Config().Copy()
	firstConfig.MaxConns = 1
	firstConfig.MinConns = 1
	firstPool, err := pgxpool.NewWithConfig(ctx, firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer firstPool.Close()
	secondConfig := fixture.db.Pool.Config().Copy()
	secondConfig.MaxConns = 1
	secondConfig.MinConns = 1
	secondPool, err := pgxpool.NewWithConfig(ctx, secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer secondPool.Close()
	secondConn, err := secondPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var secondPID int
	if err := secondConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID); err != nil {
		secondConn.Release()
		t.Fatal(err)
	}
	secondConn.Release()

	firstLocked := make(chan struct{})
	releaseFirst := make(chan struct{}, 1)
	firstStore := New(&storedb.DB{Pool: firstPool}, withDeletionFailpoint(func(point string) error {
		if point == "create_chapter_after_sibling_catalog_sync" {
			close(firstLocked)
			<-releaseFirst
		}
		return nil
	}))
	secondStore := New(&storedb.DB{Pool: secondPool})
	create := func(store *Store, chapterID, title string) error {
		_, err := store.CreateChapterWithDraft(ctx, library.CreateChapterWithDraftStoreInput{
			ID: chapterID, ProjectID: fixture.projectID, BookID: bookID, Title: title, Order: 1, CreatedAt: fixture.now,
		})
		return err
	}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- create(firstStore, projects.NewSecureIDGenerator().NewProjectID(), "First") }()
	select {
	case <-firstLocked:
	case <-ctx.Done():
		t.Fatal("first append did not reach the catalog failpoint")
	}
	go func() { secondDone <- create(secondStore, projects.NewSecureIDGenerator().NewProjectID(), "Second") }()

	observerTx, err := fixture.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observerTx.Rollback(context.Background()) }()
	waitForLibraryMutationLock(t, ctx, observerTx, secondPID, secondDone, "second append", "from books", "book")
	releaseFirst <- struct{}{}
	firstErr := <-firstDone
	secondErr := <-secondDone
	if firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent append errors = first %v second %v", firstErr, secondErr)
	}

	rows, err := fixture.db.Pool.Query(ctx, `
		SELECT chapter."order", member.chapter_order
		FROM chapters AS chapter
		JOIN chapter_source_members AS member
		  ON member.project_id = chapter.project_id
		 AND member.source_view = 'draft'
		 AND member.book_id = chapter.book_id
		 AND member.chapter_id = chapter.id
		WHERE chapter.project_id = $1 AND chapter.book_id = $2 AND chapter.deleted_at IS NULL
		ORDER BY chapter."order", chapter.id
	`, fixture.projectID, bookID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var chapterOrders, catalogOrders []int
	for rows.Next() {
		var chapterOrder, catalogOrder int
		if err := rows.Scan(&chapterOrder, &catalogOrder); err != nil {
			t.Fatal(err)
		}
		chapterOrders = append(chapterOrders, chapterOrder)
		catalogOrders = append(catalogOrders, catalogOrder)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(chapterOrders) != 2 || chapterOrders[0] != 1 || chapterOrders[1] != 2 || catalogOrders[0] != 1 || catalogOrders[1] != 2 {
		t.Fatalf("concurrent append orders = chapters %v catalog %v, want [1 2]", chapterOrders, catalogOrders)
	}
}

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
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Rollback project", fixture.now)
	insertProjectStoreMembershipForTest(
		t, db, ids.NewProjectMembershipID(), fixture.projectID, fixture.actorID,
		projects.RoleOwner, fixture.now, false,
	)
	store := New(db)
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

func assertPhase04LibraryCatalogRollback(
	t *testing.T,
	fixture phase04LibraryRollbackFixture,
	point string,
	mutate func(*Store) error,
) {
	t.Helper()
	before := phase04LibrarySourceStateForTest(t, fixture)
	injected := errors.New("library catalog failpoint")
	store := New(
		fixture.db,
		withDeletionFailpoint(func(got string) error {
			if got == point {
				return injected
			}
			return nil
		}),
	)
	if err := mutate(store); !errors.Is(err, injected) {
		t.Fatalf("%s error = %v, want injected", point, err)
	}
	if after := phase04LibrarySourceStateForTest(t, fixture); after != before {
		t.Fatalf("%s changed source/catalog state after rollback", point)
	}
}

func TestLibraryStorePhase04CatalogMutationsRollBackWithSource(t *testing.T) {
	t.Run("create book", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		bookID := projects.NewSecureIDGenerator().NewProjectID()
		assertPhase04LibraryCatalogRollback(t, fixture, "create_book_after_catalog_mutation", func(store *Store) error {
			_, err := store.CreateBook(context.Background(), library.CreateBookStoreInput{
				ID: bookID, ProjectID: fixture.projectID, Title: "New book", Order: 2, DisplayNumber: "2", CreatedAt: fixture.now,
			})
			return err
		})
	})

	t.Run("update book", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		assertPhase04LibraryCatalogRollback(t, fixture, "update_book_after_catalog_mutation", func(store *Store) error {
			_, err := store.UpdateBook(context.Background(), library.UpdateBookStoreInput{
				ProjectID: fixture.projectID, BookID: fixture.bookID, Title: "Changed title", UpdatedAt: fixture.now.Add(time.Second),
			})
			return err
		})
	})

	for _, point := range []string{"create_chapter_after_sibling_catalog_sync", "create_chapter_after_catalog_mutation"} {
		t.Run(point, func(t *testing.T) {
			fixture := newPhase04LibraryRollbackFixture(t)
			store := New(fixture.db)
			secondID := projects.NewSecureIDGenerator().NewProjectID()
			createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, secondID, "Second", 2, fixture.now)
			insertedID := projects.NewSecureIDGenerator().NewProjectID()
			assertPhase04LibraryCatalogRollback(t, fixture, point, func(store *Store) error {
				afterID := fixture.chapterID
				_, err := store.CreateChapterWithDraft(context.Background(), library.CreateChapterWithDraftStoreInput{
					ID: insertedID, ProjectID: fixture.projectID, BookID: fixture.bookID, Title: "Inserted", Order: 3, CreatedAt: fixture.now,
					AfterChapterID: library.OrderPlacement{Present: true, AfterID: &afterID},
				})
				return err
			})
		})
	}

	t.Run("save chapter", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		assertPhase04LibraryCatalogRollback(t, fixture, "save_chapter_after_catalog_mutation", func(store *Store) error {
			_, err := store.SaveChapterDraft(context.Background(), library.SaveChapterDraftStoreInput{
				ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
				ExpectedRevision: 1, Revision: 2, Title: "Changed chapter", Status: library.ChapterStatusDraft, UpdatedAt: fixture.now.Add(time.Second),
				Paragraphs: []library.Paragraph{{
					ID: fixture.paragraphID, ChapterID: fixture.chapterID, Revision: 2, Variant: library.ParagraphVariantDraft,
					Kind: library.ParagraphKindParagraph, Order: 1, Markdown: "after", PlainText: "after", WordCount: 1,
					CreatedAt: fixture.now, UpdatedAt: fixture.now.Add(time.Second),
				}},
			})
			return err
		})
	})

	t.Run("publish chapter", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		assertPhase04LibraryCatalogRollback(t, fixture, "publish_chapter_after_catalog_mutation", func(store *Store) error {
			_, err := store.PublishChapter(context.Background(), library.PublishChapterStoreInput{
				ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
				Revision: 1, PublishedAt: fixture.now.Add(time.Second),
			})
			return err
		})
	})

	t.Run("delete book", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		assertPhase04LibraryCatalogRollback(t, fixture, "book_after_source_update", func(store *Store) error {
			_, err := store.FenceBookDeletion(context.Background(), library.FenceBookDeletionInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID,
				TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.now.Add(time.Second),
			})
			return err
		})
	})

	t.Run("restore book", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		if _, err := New(fixture.db).FenceBookDeletion(
			context.Background(),
			library.FenceBookDeletionInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID,
				TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.now.Add(time.Second),
			},
		); err != nil {
			t.Fatalf("delete before restore rollback: %v", err)
		}
		input := restoreBookInputForTest(fixture.actorID, fixture.projectID, fixture.bookID)
		assertPhase04LibraryCatalogRollback(t, fixture, "book_restore_after_source_update", func(store *Store) error {
			_, err := store.RestoreBook(context.Background(), input)
			return err
		})
	})

	t.Run("delete chapter", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		assertPhase04LibraryCatalogRollback(t, fixture, "chapter_after_source_update", func(store *Store) error {
			_, err := store.FenceChapterDeletion(context.Background(), library.FenceChapterDeletionInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
				TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.now.Add(time.Second),
			})
			return err
		})
	})

	t.Run("restore chapter", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		if _, err := New(fixture.db).FenceChapterDeletion(
			context.Background(),
			library.FenceChapterDeletionInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
				TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.now.Add(time.Second),
			},
		); err != nil {
			t.Fatalf("delete chapter before restore rollback: %v", err)
		}
		input := library.RestoreChapterStoreInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookID, ChapterID: fixture.chapterID,
		}
		assertPhase04LibraryCatalogRollback(t, fixture, "chapter_restore_after_source_update", func(store *Store) error {
			_, err := store.RestoreChapter(context.Background(), input)
			return err
		})
	})
}

func TestLibraryStoreFenceBookDeletionReauthorizesExactAncestry(t *testing.T) {
	tests := []struct {
		name       string
		role       projects.Role
		removed    bool
		crossScope bool
		wantErr    error
	}{
		{name: "viewer forbidden", role: projects.RoleViewer, wantErr: library.ErrForbidden},
		{name: "removed editor concealed", role: projects.RoleEditor, removed: true, wantErr: library.ErrNotFound},
		{name: "cross project book concealed", role: projects.RoleEditor, crossScope: true, wantErr: library.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			ids := projects.NewSecureIDGenerator()
			actorID := ids.NewProjectID()
			projectA := ids.NewProjectID()
			projectB := ids.NewProjectID()
			bookA := ids.NewProjectID()
			bookB := ids.NewProjectID()
			membershipID := ids.NewProjectMembershipID()
			tombstoneID := ids.NewDeletionTombstoneID()
			fencedAt := time.Now().UTC().Truncate(time.Microsecond)
			insertProjectStoreUserForTest(t, db, actorID, "book-reauthorize@example.test")
			insertProjectStoreProjectForTest(t, db, projectA, actorID, "Project A", fencedAt.Add(-time.Hour))
			insertProjectStoreProjectForTest(t, db, projectB, actorID, "Project B", fencedAt.Add(-time.Hour))
			insertProjectStoreMembershipForTest(t, db, membershipID, projectA, actorID, tt.role, fencedAt.Add(-time.Hour), tt.removed)
			baseStore := New(db)
			createBookForOrderTest(t, baseStore, projectA, bookA, "Book A", 1, "1", fencedAt.Add(-time.Hour))
			createBookForOrderTest(t, baseStore, projectB, bookB, "Book B", 1, "1", fencedAt.Add(-time.Hour))

			inputProjectID := projectA
			if tt.crossScope {
				inputProjectID = projectB
			}
			store := New(db)
			_, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
				ActorUserID: actorID, ProjectID: inputProjectID, BookID: bookA,
				TombstoneID: tombstoneID, DeletedAt: fencedAt,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("FenceBookDeletion() error = %v, want %v", err, tt.wantErr)
			}
			assertBookDeletionRolledBackForTest(t, db, projectA, bookA, tombstoneID)
		})
	}
}

func TestLibraryStoreFenceBookDeletionRollsBackEveryFailpointAndContributorFailure(t *testing.T) {
	failpoints := []string{
		"book_after_row_lock",
		"book_after_source_update",
		"book_after_tombstone_insert",
		"book_after_target_runtime",
		"book_after_target_filesystem",
		"book_after_target_postgresql",
	}
	for _, failpoint := range failpoints {
		t.Run(failpoint, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			ids := projects.NewSecureIDGenerator()
			actorID := ids.NewProjectID()
			projectID := ids.NewProjectID()
			bookID := ids.NewProjectID()
			membershipID := ids.NewProjectMembershipID()
			tombstoneID := ids.NewDeletionTombstoneID()
			fencedAt := time.Now().UTC().Truncate(time.Microsecond)
			insertProjectStoreUserForTest(t, db, actorID, "book-rollback@example.test")
			insertProjectStoreProjectForTest(t, db, projectID, actorID, "Rollback Project", fencedAt.Add(-time.Hour))
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-time.Hour), false)
			baseStore := New(db)
			createBookForOrderTest(t, baseStore, projectID, bookID, "Rollback Book", 1, "1", fencedAt.Add(-time.Hour))
			injected := errors.New("book deletion failpoint")
			store := New(
				db,
				withDeletionFailpoint(func(point string) error {
					if point == failpoint {
						return injected
					}
					return nil
				}),
			)
			_, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
				ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
				TombstoneID: tombstoneID, DeletedAt: fencedAt,
			})
			if !errors.Is(err, injected) {
				t.Fatalf("FenceBookDeletion() error = %v, want injected", err)
			}
			assertBookDeletionRolledBackForTest(t, db, projectID, bookID, tombstoneID)
		})
	}

}

func TestLibraryStoreListDeletedAndRestoreBookKeepsCurrentGeneration(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000001201"
	projectID := "00000000-0000-4000-8000-000000001202"
	bookID := "00000000-0000-4000-8000-000000001203"
	membershipID := "00000000-0000-4000-8000-000000001204"
	tombstoneID := "00000000-0000-4000-8000-000000001205"
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "book-restore@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Book Project", fencedAt.Add(-2*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
	baseStore := New(db)
	createBookForOrderTest(t, baseStore, projectID, bookID, "Restore Book", 1, "1", fencedAt.Add(-time.Hour))
	store := New(db)
	if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
		TombstoneID: tombstoneID, DeletedAt: fencedAt,
	}); err != nil {
		t.Fatalf("delete before restore: %v", err)
	}

	deleted, err := store.ListBooks(ctx, library.ListBooksStoreInput{
		ActorUserID: actorID, ProjectID: projectID, State: library.SourceListDeleted, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListBooks(deleted) error = %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != bookID || deleted[0].DeletedAt == nil ||
		!deleted[0].DeletedAt.Equal(fencedAt) || deleted[0].RestoreUntil == nil ||
		!deleted[0].RestoreUntil.Equal(fencedAt.Add(deletion.SourceRestoreWindow)) {
		t.Fatalf("deleted books = %#v", deleted)
	}

	restored, err := store.RestoreBook(ctx, library.RestoreBookStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
	})
	if err != nil {
		t.Fatalf("RestoreBook() error = %v", err)
	}
	if restored.ID != bookID || restored.DeletedAt != nil || restored.RestoreUntil != nil {
		t.Fatalf("RestoreBook() = %#v", restored)
	}
	var projectGeneration, bookGeneration int64
	var deletedAt, retentionStarted, purgeAfter, purgeDeadline pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `
		SELECT project.lifecycle_generation, book.lifecycle_generation,
		       book.deleted_at, book.governance_retention_started_at,
		       book.purge_after, book.purge_deadline
		FROM projects AS project
		JOIN books AS book ON book.project_id = project.id
		WHERE project.id = $1 AND book.id = $2
	`, projectID, bookID).Scan(
		&projectGeneration, &bookGeneration, &deletedAt, &retentionStarted, &purgeAfter, &purgeDeadline,
	); err != nil {
		t.Fatal(err)
	}
	if projectGeneration != 1 || bookGeneration != 2 || deletedAt.Valid || retentionStarted.Valid || purgeAfter.Valid || purgeDeadline.Valid {
		t.Fatalf("restored book lifecycle = project %d book %d deleted=%v retention=%v purge=%v deadline=%v", projectGeneration, bookGeneration, deletedAt, retentionStarted, purgeAfter, purgeDeadline)
	}
	assertQueuedTextIntentsMatchCatalogForTest(t, db, projectID, []governance.SourceView{governance.SourceViewDraft})
	var tombstoneCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE id = $1`, tombstoneID).Scan(&tombstoneCount); err != nil || tombstoneCount != 1 {
		t.Fatalf("book tombstone count = %d, err = %v", tombstoneCount, err)
	}

	_, err = store.RestoreBook(ctx, library.RestoreBookStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
	})
	if !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("second RestoreBook() error = %v, want ErrNotFound", err)
	}
}

func TestLibraryStoreRestoreBookRollsBackAfterSourceUpdate(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	actorID := ids.NewProjectID()
	projectID := ids.NewProjectID()
	bookID := ids.NewProjectID()
	membershipID := ids.NewProjectMembershipID()
	tombstoneID := ids.NewDeletionTombstoneID()
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "book-restore-rollback@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Rollback Project", fencedAt.Add(-2*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
	baseStore := New(db)
	createBookForOrderTest(t, baseStore, projectID, bookID, "Restore Rollback Book", 1, "1", fencedAt.Add(-time.Hour))
	deleteStore := New(db)
	if _, err := deleteStore.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
		TombstoneID: tombstoneID, DeletedAt: fencedAt,
	}); err != nil {
		t.Fatalf("delete before restore rollback: %v", err)
	}
	input := restoreBookInputForTest(actorID, projectID, bookID)
	injected := errors.New("restore book failpoint")
	store := New(db, withDeletionFailpoint(func(point string) error {
		if point == "book_restore_after_source_update" {
			return injected
		}
		return nil
	}))
	if _, err := store.RestoreBook(ctx, input); !errors.Is(err, injected) {
		t.Fatalf("RestoreBook() error = %v, want injected", err)
	}
	assertBookRestoreRolledBackForTest(t, db, input)
}

func TestLibraryStoreRestoreBookEnforcesMembershipAndStrictPurgeBoundary(t *testing.T) {
	t.Run("viewer is forbidden and removed member concealed", func(t *testing.T) {
		ctx := context.Background()
		db := openCurrentStoreTestDB(t)
		ids := projects.NewSecureIDGenerator()
		actorID := ids.NewProjectID()
		projectID := ids.NewProjectID()
		bookID := ids.NewProjectID()
		membershipID := ids.NewProjectMembershipID()
		tombstoneID := ids.NewDeletionTombstoneID()
		fencedAt := time.Now().UTC().Truncate(time.Microsecond)
		insertProjectStoreUserForTest(t, db, actorID, "book-restore-auth@example.test")
		insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Auth Project", fencedAt.Add(-2*time.Hour))
		insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
		baseStore := New(db)
		createBookForOrderTest(t, baseStore, projectID, bookID, "Restore Auth Book", 1, "1", fencedAt.Add(-time.Hour))
		store := New(db)
		if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
			ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
			TombstoneID: tombstoneID, DeletedAt: fencedAt,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'viewer' WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		viewerInput := restoreBookInputForTest(actorID, projectID, bookID)
		if _, err := store.RestoreBook(ctx, viewerInput); !errors.Is(err, library.ErrForbidden) {
			t.Fatalf("RestoreBook(viewer) error = %v, want ErrForbidden", err)
		}
		assertBookRestoreRolledBackForTest(t, db, viewerInput)
		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		removedInput := restoreBookInputForTest(actorID, projectID, bookID)
		if _, err := store.RestoreBook(ctx, removedInput); !errors.Is(err, library.ErrNotFound) {
			t.Fatalf("RestoreBook(removed) error = %v, want ErrNotFound", err)
		}
		assertBookRestoreRolledBackForTest(t, db, removedInput)
	})

	t.Run("purge boundary is exclusive", func(t *testing.T) {
		ctx := context.Background()
		db := openCurrentStoreTestDB(t)
		ids := projects.NewSecureIDGenerator()
		actorID := ids.NewProjectID()
		projectID := ids.NewProjectID()
		bookID := ids.NewProjectID()
		membershipID := ids.NewProjectMembershipID()
		tombstoneID := ids.NewDeletionTombstoneID()
		fencedAt := time.Now().UTC().Truncate(time.Microsecond)
		insertProjectStoreUserForTest(t, db, actorID, "book-restore-boundary@example.test")
		insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Boundary Project", fencedAt.Add(-2*time.Hour))
		insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
		baseStore := New(db)
		createBookForOrderTest(t, baseStore, projectID, bookID, "Restore Boundary Book", 1, "1", fencedAt.Add(-time.Hour))
		store := New(db)
		if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
			ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
			TombstoneID: tombstoneID, DeletedAt: fencedAt,
		}); err != nil {
			t.Fatal(err)
		}
		conn, err := db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			conn.Release()
			t.Fatal(err)
		}
		_, updateErr := conn.Exec(ctx, `
			WITH boundary AS (SELECT clock_timestamp() AS now_at)
			UPDATE books
			SET deleted_at = boundary.now_at - interval '7 days',
			    governance_retention_started_at = boundary.now_at - interval '7 days',
			    purge_after = boundary.now_at,
			    purge_deadline = boundary.now_at + interval '24 hours'
			FROM boundary WHERE id = $1 AND project_id = $2
		`, bookID, projectID)
		_, resetErr := conn.Exec(ctx, `SET session_replication_role = origin`)
		conn.Release()
		if updateErr != nil || resetErr != nil {
			t.Fatalf("set book purge boundary: update=%v reset=%v", updateErr, resetErr)
		}
		input := restoreBookInputForTest(actorID, projectID, bookID)
		if _, err := store.RestoreBook(ctx, input); !errors.Is(err, library.ErrConflict) {
			t.Fatalf("RestoreBook(boundary) error = %v, want ErrConflict", err)
		}
		assertBookRestoreRolledBackForTest(t, db, input)
	})

	t.Run("parent lifecycle is disclosed only after identity and role", func(t *testing.T) {
		ctx := context.Background()
		db := openCurrentStoreTestDB(t)
		ids := projects.NewSecureIDGenerator()
		actorID := ids.NewProjectID()
		projectID := ids.NewProjectID()
		bookID := ids.NewProjectID()
		membershipID := ids.NewProjectMembershipID()
		fencedAt := time.Now().UTC().Truncate(time.Microsecond)
		insertProjectStoreUserForTest(t, db, actorID, "book-restore-parent@example.test")
		insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Parent Project", fencedAt.Add(-2*time.Hour))
		insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-2*time.Hour), false)
		baseStore := New(db)
		createBookForOrderTest(t, baseStore, projectID, bookID, "Restore Parent Book", 1, "1", fencedAt.Add(-time.Hour))
		store := New(db)
		if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
			ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
			TombstoneID: ids.NewDeletionTombstoneID(), DeletedAt: fencedAt,
		}); err != nil {
			t.Fatal(err)
		}
		conn, err := db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			conn.Release()
			t.Fatal(err)
		}
		_, updateErr := conn.Exec(ctx, `
			UPDATE projects
			SET deleted_at = $2::timestamptz, governance_retention_started_at = $2::timestamptz,
			    purge_after = $2::timestamptz + interval '7 days', purge_deadline = $2::timestamptz + interval '8 days'
			WHERE id = $1
		`, projectID, fencedAt)
		_, resetErr := conn.Exec(ctx, `SET session_replication_role = origin`)
		conn.Release()
		if updateErr != nil || resetErr != nil {
			t.Fatalf("delete parent project: update=%v reset=%v", updateErr, resetErr)
		}

		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'viewer' WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		access, err := store.ResolveBookAccessForActor(ctx, actorID, bookID, projectaccess.VisibilityDeletedSource)
		if err != nil || access.ProjectRole != projects.RoleViewer {
			t.Fatalf("ResolveBookAccessForActor(viewer) = %#v, %v", access, err)
		}
		lifecycle, err := store.LoadDeletedBookLifecycle(ctx, projectID, bookID)
		if err != nil || lifecycle != projectaccess.DeletedSourceParentDeleted {
			t.Fatalf("LoadDeletedBookLifecycle() = %q, %v", lifecycle, err)
		}
		viewerInput := restoreBookInputForTest(actorID, projectID, bookID)
		if _, err := store.RestoreBook(ctx, viewerInput); !errors.Is(err, library.ErrForbidden) {
			t.Fatalf("RestoreBook(parent deleted viewer) error = %v, want ErrForbidden", err)
		}
		assertBookRestoreRolledBackForTest(t, db, viewerInput)

		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'editor' WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		editorInput := restoreBookInputForTest(actorID, projectID, bookID)
		if _, err := store.RestoreBook(ctx, editorInput); !errors.Is(err, library.ErrConflict) {
			t.Fatalf("RestoreBook(parent deleted editor) error = %v, want ErrConflict", err)
		}
		assertBookRestoreRolledBackForTest(t, db, editorInput)

		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ResolveBookAccessForActor(ctx, actorID, bookID, projectaccess.VisibilityDeletedSource); !errors.Is(err, library.ErrNotFound) {
			t.Fatalf("ResolveBookAccessForActor(removed) error = %v, want ErrNotFound", err)
		}
	})
}

func TestLibraryStoreFenceChapterGovernedDeletionCommitsExactAncestryFence(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000001301"
	projectA := "00000000-0000-4000-8000-000000001302"
	projectB := "00000000-0000-4000-8000-000000001303"
	bookA := "00000000-0000-4000-8000-000000001304"
	bookB := "00000000-0000-4000-8000-000000001305"
	chapterA := "00000000-0000-4000-8000-000000001306"
	chapterB := "00000000-0000-4000-8000-000000001307"
	membershipA := "00000000-0000-4000-8000-000000001308"
	membershipB := "00000000-0000-4000-8000-000000001309"
	tombstoneID := "00000000-0000-4000-8000-000000001310"
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "chapter-delete-editor@example.test")
	insertProjectStoreProjectForTest(t, db, projectA, actorID, "Chapter Project A", fencedAt.Add(-3*time.Hour))
	insertProjectStoreProjectForTest(t, db, projectB, actorID, "Chapter Project B", fencedAt.Add(-3*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipA, projectA, actorID, projects.RoleEditor, fencedAt.Add(-3*time.Hour), false)
	insertProjectStoreMembershipForTest(t, db, membershipB, projectB, actorID, projects.RoleEditor, fencedAt.Add(-3*time.Hour), false)
	baseStore := New(db)
	createBookForOrderTest(t, baseStore, projectA, bookA, "Book A", 1, "1", fencedAt.Add(-2*time.Hour))
	createBookForOrderTest(t, baseStore, projectB, bookB, "Book B", 1, "1", fencedAt.Add(-2*time.Hour))
	createChapterForOrderTest(t, baseStore, projectA, bookA, chapterA, "Chapter A", 1, fencedAt.Add(-time.Hour))
	createChapterForOrderTest(t, baseStore, projectB, bookB, chapterB, "Chapter B", 1, fencedAt.Add(-time.Hour))

	var projectBBefore, bookBBefore, chapterBBefore string
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(project)::text FROM projects project WHERE id = $1`, projectB).Scan(&projectBBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(book)::text FROM books book WHERE id = $1`, bookB).Scan(&bookBBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(chapter)::text FROM chapters chapter WHERE id = $1`, chapterB).Scan(&chapterBBefore); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	result, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: actorID, ProjectID: projectA, BookID: bookA, ChapterID: chapterA,
		TombstoneID: tombstoneID, DeletedAt: fencedAt,
	})
	if err != nil {
		t.Fatalf("FenceChapterDeletion() error = %v", err)
	}
	wantDeleted := governance.SourceGenerationVector{
		ProjectID: projectA, ProjectGeneration: 1,
		BookID: bookA, BookGeneration: 1,
		ChapterID: chapterA, ChapterGeneration: 1,
	}
	wantCurrent := wantDeleted
	wantCurrent.ChapterGeneration = 2
	if result.Deleted != wantDeleted || result.Current != wantCurrent || result.TombstoneID != tombstoneID {
		t.Fatalf("FenceChapterDeletion result = %#v, want %#v -> %#v", result, wantDeleted, wantCurrent)
	}
	var projectGeneration, bookGeneration, chapterGeneration int64
	var deletedAt, purgeAfter, hardPurgeStarted pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `
		SELECT project.lifecycle_generation, book.lifecycle_generation,
		       chapter.lifecycle_generation, chapter.deleted_at,
		       chapter.purge_after, chapter.hard_purge_started_at
		FROM projects AS project
		JOIN books AS book ON book.project_id = project.id
		JOIN chapters AS chapter ON chapter.project_id = project.id AND chapter.book_id = book.id
		WHERE project.id = $1 AND book.id = $2 AND chapter.id = $3
	`, projectA, bookA, chapterA).Scan(
		&projectGeneration, &bookGeneration, &chapterGeneration,
		&deletedAt, &purgeAfter, &hardPurgeStarted,
	); err != nil {
		t.Fatal(err)
	}
	if projectGeneration != 1 || bookGeneration != 1 || chapterGeneration != 2 ||
		!deletedAt.Valid || !deletedAt.Time.Equal(fencedAt) ||
		!purgeAfter.Valid || !purgeAfter.Time.Equal(fencedAt.Add(deletion.SourceRestoreWindow)) || hardPurgeStarted.Valid {
		t.Fatalf("chapter lifecycle = project %d book %d chapter %d deleted=%v purge=%v hard=%v", projectGeneration, bookGeneration, chapterGeneration, deletedAt, purgeAfter, hardPurgeStarted)
	}

	var projectBAfter, bookBAfter, chapterBAfter string
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(project)::text FROM projects project WHERE id = $1`, projectB).Scan(&projectBAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(book)::text FROM books book WHERE id = $1`, bookB).Scan(&bookBAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(chapter)::text FROM chapters chapter WHERE id = $1`, chapterB).Scan(&chapterBAfter); err != nil {
		t.Fatal(err)
	}
	if projectBAfter != projectBBefore || bookBAfter != bookBBefore || chapterBAfter != chapterBBefore {
		t.Fatal("unrelated chapter ancestry changed")
	}
}

func TestLibraryStoreCreateChapterInitializesCurrentSourceState(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000001321"
	projectID := "00000000-0000-4000-8000-000000001322"
	bookID := "00000000-0000-4000-8000-000000001323"
	chapterID := "00000000-0000-4000-8000-000000001324"
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "chapter-create-governance@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Chapter Create Project", now.Add(-2*time.Hour))
	store := New(db)
	createBookForOrderTest(t, store, projectID, bookID, "Chapter Create Book", 1, "1", now.Add(-time.Hour))
	if _, err := store.CreateChapterWithDraft(ctx, library.CreateChapterWithDraftStoreInput{
		ID: chapterID, ProjectID: projectID, BookID: bookID, Title: "Typed Chapter", Order: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateChapterWithDraft() error = %v", err)
	}
	var storedProjectID string
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT chapter.project_id::text, chapter.lifecycle_generation
		FROM chapters AS chapter
		WHERE chapter.id = $1 AND chapter.book_id = $2
	`, chapterID, bookID).Scan(&storedProjectID, &generation); err != nil {
		t.Fatal(err)
	}
	if storedProjectID != projectID || generation != 1 {
		t.Fatalf("created chapter source state = project %q generation %d", storedProjectID, generation)
	}
}

func TestPhase015SourceLifecycleAcceptance(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	const (
		actorID      = "00000000-0000-4000-8000-000000001361"
		projectID    = "00000000-0000-4000-8000-000000001362"
		bookID       = "00000000-0000-4000-8000-000000001363"
		chapterID    = "00000000-0000-4000-8000-000000001364"
		membershipID = "00000000-0000-4000-8000-000000001365"
		tombstoneID  = "00000000-0000-4000-8000-000000001366"
	)
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "phase-015-lifecycle@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Phase 01.5 Project", fencedAt.Add(-3*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-3*time.Hour), false)
	baseStore := New(db)
	createBookForOrderTest(t, baseStore, projectID, bookID, "Phase 01.5 Book", 1, "1", fencedAt.Add(-2*time.Hour))
	createChapterForOrderTest(t, baseStore, projectID, bookID, chapterID, "Phase 01.5 Chapter", 1, fencedAt.Add(-time.Hour))

	var projectGeneration, bookGeneration, chapterGeneration int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT project.lifecycle_generation, book.lifecycle_generation, chapter.lifecycle_generation
		FROM projects AS project
		JOIN books AS book ON book.project_id = project.id
		JOIN chapters AS chapter ON chapter.project_id = project.id AND chapter.book_id = book.id
		WHERE project.id = $1 AND book.id = $2 AND chapter.id = $3
	`, projectID, bookID, chapterID).Scan(&projectGeneration, &bookGeneration, &chapterGeneration); err != nil {
		t.Fatal(err)
	}
	if projectGeneration != 1 || bookGeneration != 1 || chapterGeneration != 1 {
		t.Fatalf("initial lifecycle generations = %d/%d/%d", projectGeneration, bookGeneration, chapterGeneration)
	}

	store := New(db)
	if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID, ChapterID: chapterID,
		TombstoneID: tombstoneID, DeletedAt: fencedAt,
	}); err != nil {
		t.Fatalf("FenceChapterDeletion() error = %v", err)
	}

	var targets []string
	if err := db.Pool.QueryRow(ctx, `
		SELECT array_agg(target ORDER BY CASE target
			WHEN 'runtime' THEN 1
			WHEN 'filesystem' THEN 2
			WHEN 'postgresql' THEN 3
			ELSE 4
		END)
		FROM deletion_target_tasks
		WHERE tombstone_id = $1 AND project_id = $2
	`, tombstoneID, projectID).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if strings.Join(targets, ",") != "runtime,filesystem,postgresql" {
		t.Fatalf("chapter deletion targets = %v", targets)
	}

	deleted, err := store.ListChapters(ctx, library.ListChaptersStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
		State: library.SourceListDeleted, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListChapters(deleted) error = %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != chapterID || deleted[0].DeletedAt == nil {
		t.Fatalf("deleted chapters = %#v", deleted)
	}

	if _, err := store.RestoreChapter(ctx, library.RestoreChapterStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID, ChapterID: chapterID,
	}); err != nil {
		t.Fatalf("RestoreChapter() error = %v", err)
	}

	var liveGeneration, tombstoneGeneration int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT chapter.lifecycle_generation, tombstone.chapter_generation
		FROM chapters AS chapter
		JOIN deletion_tombstones AS tombstone
		  ON tombstone.project_id = chapter.project_id AND tombstone.id = $1
		WHERE chapter.project_id = $2 AND chapter.book_id = $3 AND chapter.id = $4
	`, tombstoneID, projectID, bookID, chapterID).Scan(&liveGeneration, &tombstoneGeneration); err != nil {
		t.Fatal(err)
	}
	if liveGeneration != 2 || tombstoneGeneration != 1 {
		t.Fatalf("restored/live chapter generation = %d, tombstone generation = %d", liveGeneration, tombstoneGeneration)
	}
}

func TestLibraryStoreListDeletedAndRestoreChapterKeepsCurrentVector(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000001341"
	projectID := "00000000-0000-4000-8000-000000001342"
	bookID := "00000000-0000-4000-8000-000000001343"
	chapterID := "00000000-0000-4000-8000-000000001344"
	membershipID := "00000000-0000-4000-8000-000000001345"
	tombstoneID := "00000000-0000-4000-8000-000000001346"
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "chapter-restore@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Chapter Restore Project", fencedAt.Add(-3*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, fencedAt.Add(-3*time.Hour), false)
	baseStore := New(db)
	createBookForOrderTest(t, baseStore, projectID, bookID, "Chapter Restore Book", 1, "1", fencedAt.Add(-2*time.Hour))
	createChapterForOrderTest(t, baseStore, projectID, bookID, chapterID, "Chapter Restore", 1, fencedAt.Add(-time.Hour))
	store := New(db)
	if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID, ChapterID: chapterID,
		TombstoneID: tombstoneID, DeletedAt: fencedAt,
	}); err != nil {
		t.Fatalf("delete before chapter restore: %v", err)
	}
	deleted, err := store.ListChapters(ctx, library.ListChaptersStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID, State: library.SourceListDeleted, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListChapters(deleted) error = %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != chapterID || deleted[0].DeletedAt == nil ||
		!deleted[0].DeletedAt.Equal(fencedAt) || deleted[0].RestoreUntil == nil ||
		!deleted[0].RestoreUntil.Equal(fencedAt.Add(deletion.SourceRestoreWindow)) {
		t.Fatalf("deleted chapters = %#v", deleted)
	}
	restored, err := store.RestoreChapter(ctx, library.RestoreChapterStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID, ChapterID: chapterID,
	})
	if err != nil {
		t.Fatalf("RestoreChapter() error = %v", err)
	}
	if restored.ID != chapterID || restored.DeletedAt != nil || restored.RestoreUntil != nil {
		t.Fatalf("RestoreChapter() = %#v", restored)
	}
	var projectGeneration, bookGeneration, chapterGeneration int64
	var deletedAt, purgeAfter pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `
		SELECT project.lifecycle_generation, book.lifecycle_generation,
		       chapter.lifecycle_generation, chapter.deleted_at, chapter.purge_after
		FROM projects AS project
		JOIN books AS book ON book.project_id = project.id
		JOIN chapters AS chapter ON chapter.project_id = project.id AND chapter.book_id = book.id
		WHERE project.id = $1 AND book.id = $2 AND chapter.id = $3
	`, projectID, bookID, chapterID).Scan(
		&projectGeneration, &bookGeneration, &chapterGeneration, &deletedAt, &purgeAfter,
	); err != nil {
		t.Fatal(err)
	}
	if projectGeneration != 1 || bookGeneration != 1 || chapterGeneration != 2 || deletedAt.Valid || purgeAfter.Valid {
		t.Fatalf("restored chapter lifecycle = %d/%d/%d deleted=%v purge=%v", projectGeneration, bookGeneration, chapterGeneration, deletedAt, purgeAfter)
	}
	assertQueuedTextIntentsMatchCatalogForTest(t, db, projectID, []governance.SourceView{governance.SourceViewDraft})
}

func TestLibraryStoreChapterDeleteAndRestoreRollbackMatrices(t *testing.T) {
	ctx := context.Background()
	fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
	deletePoints := []string{
		"chapter_after_row_lock",
		"chapter_after_source_update",
		"chapter_after_tombstone_insert",
		"chapter_after_target_runtime",
		"chapter_after_target_filesystem",
		"chapter_after_target_postgresql",
	}
	for _, point := range deletePoints {
		t.Run(point, func(t *testing.T) {
			tombstoneID := projects.NewSecureIDGenerator().NewDeletionTombstoneID()
			injected := errors.New("chapter delete failpoint")
			store := New(
				fixture.db,
				withDeletionFailpoint(func(got string) error {
					if got == point {
						return injected
					}
					return nil
				}),
			)
			_, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
				ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
				BookID: fixture.bookID, ChapterID: fixture.chapterID,
				TombstoneID: tombstoneID, DeletedAt: fixture.fencedAt,
			})
			if !errors.Is(err, injected) {
				t.Fatalf("FenceChapterDeletion() error = %v, want injected", err)
			}
			assertChapterDeletionRolledBackForTest(t, fixture.db, fixture, tombstoneID)
		})
	}

	tombstoneID := projects.NewSecureIDGenerator().NewDeletionTombstoneID()
	deleteStore := New(fixture.db)
	if _, err := deleteStore.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
		BookID: fixture.bookID, ChapterID: fixture.chapterID,
		TombstoneID: tombstoneID, DeletedAt: fixture.fencedAt,
	}); err != nil {
		t.Fatalf("delete before restore matrix: %v", err)
	}
	t.Run("chapter_restore_after_source_update", func(t *testing.T) {
		input := restoreChapterInputForTest(fixture)
		injected := errors.New("chapter restore failpoint")
		store := New(fixture.db, withDeletionFailpoint(func(got string) error {
			if got == "chapter_restore_after_source_update" {
				return injected
			}
			return nil
		}))
		if _, err := store.RestoreChapter(ctx, input); !errors.Is(err, injected) {
			t.Fatalf("RestoreChapter() error = %v, want injected", err)
		}
		assertChapterRestoreRolledBackForTest(t, fixture.db, input)
	})
}

func TestLibraryStoreChapterDeletionAndRestoreReauthorizeAndExpire(t *testing.T) {
	t.Run("viewer and removed membership", func(t *testing.T) {
		ctx := context.Background()
		fixture := newGovernedChapterFixtureForTest(t, projects.RoleViewer, false)
		store := New(fixture.db)
		tombstoneID := projects.NewSecureIDGenerator().NewDeletionTombstoneID()
		_, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
			BookID: fixture.bookID, ChapterID: fixture.chapterID,
			TombstoneID: tombstoneID, DeletedAt: fixture.fencedAt,
		})
		if !errors.Is(err, library.ErrForbidden) {
			t.Fatalf("FenceChapterDeletion(viewer) error = %v, want ErrForbidden", err)
		}
		assertChapterDeletionRolledBackForTest(t, fixture.db, fixture, tombstoneID)
		if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, fixture.membershipID); err != nil {
			t.Fatal(err)
		}
		_, err = store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
			BookID: fixture.bookID, ChapterID: fixture.chapterID,
			TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.fencedAt,
		})
		if !errors.Is(err, library.ErrNotFound) {
			t.Fatalf("FenceChapterDeletion(removed) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("restore boundary is exclusive", func(t *testing.T) {
		ctx := context.Background()
		fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
		store := New(fixture.db)
		if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
			BookID: fixture.bookID, ChapterID: fixture.chapterID,
			TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.fencedAt,
		}); err != nil {
			t.Fatal(err)
		}
		conn, err := fixture.db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			conn.Release()
			t.Fatal(err)
		}
		_, updateErr := conn.Exec(ctx, `
			WITH boundary AS (SELECT clock_timestamp() AS now_at)
			UPDATE chapters
			SET deleted_at = boundary.now_at - interval '7 days',
			    governance_retention_started_at = boundary.now_at - interval '7 days',
			    purge_after = boundary.now_at,
			    purge_deadline = boundary.now_at + interval '24 hours'
			FROM boundary
			WHERE id = $1 AND book_id = $2 AND project_id = $3
		`, fixture.chapterID, fixture.bookID, fixture.projectID)
		_, resetErr := conn.Exec(ctx, `SET session_replication_role = origin`)
		conn.Release()
		if updateErr != nil || resetErr != nil {
			t.Fatalf("set chapter purge boundary: update=%v reset=%v", updateErr, resetErr)
		}
		input := restoreChapterInputForTest(fixture)
		if _, err := store.RestoreChapter(ctx, input); !errors.Is(err, library.ErrConflict) {
			t.Fatalf("RestoreChapter(boundary) error = %v, want ErrConflict", err)
		}
		assertChapterRestoreRolledBackForTest(t, fixture.db, input)
	})

	t.Run("deleted parent book conflicts only after role proof", func(t *testing.T) {
		ctx := context.Background()
		fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
		store := New(fixture.db)
		if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
			BookID: fixture.bookID, ChapterID: fixture.chapterID,
			TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: fixture.fencedAt,
		}); err != nil {
			t.Fatal(err)
		}
		conn, err := fixture.db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			conn.Release()
			t.Fatal(err)
		}
		_, updateErr := conn.Exec(ctx, `
			UPDATE books
			SET deleted_at = $2::timestamptz, governance_retention_started_at = $2::timestamptz,
			    purge_after = $2::timestamptz + interval '7 days', purge_deadline = $2::timestamptz + interval '8 days'
			WHERE id = $1
		`, fixture.bookID, fixture.fencedAt)
		_, resetErr := conn.Exec(ctx, `SET session_replication_role = origin`)
		conn.Release()
		if updateErr != nil || resetErr != nil {
			t.Fatalf("delete parent book: update=%v reset=%v", updateErr, resetErr)
		}
		if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'viewer' WHERE id = $1`, fixture.membershipID); err != nil {
			t.Fatal(err)
		}
		access, err := store.ResolveChapterAccessForActor(ctx, fixture.actorID, fixture.chapterID, projectaccess.VisibilityDeletedSource)
		if err != nil || access.ProjectRole != projects.RoleViewer {
			t.Fatalf("ResolveChapterAccessForActor(viewer) = %#v, %v", access, err)
		}
		lifecycle, err := store.LoadDeletedChapterLifecycle(ctx, fixture.projectID, fixture.bookID, fixture.chapterID)
		if err != nil || lifecycle != projectaccess.DeletedSourceParentDeleted {
			t.Fatalf("LoadDeletedChapterLifecycle() = %q, %v", lifecycle, err)
		}
		viewerInput := restoreChapterInputForTest(fixture)
		if _, err := store.RestoreChapter(ctx, viewerInput); !errors.Is(err, library.ErrForbidden) {
			t.Fatalf("RestoreChapter(parent deleted viewer) error = %v, want ErrForbidden", err)
		}
		assertChapterRestoreRolledBackForTest(t, fixture.db, viewerInput)
		if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'editor' WHERE id = $1`, fixture.membershipID); err != nil {
			t.Fatal(err)
		}
		editorInput := restoreChapterInputForTest(fixture)
		if _, err := store.RestoreChapter(ctx, editorInput); !errors.Is(err, library.ErrConflict) {
			t.Fatalf("RestoreChapter(parent deleted editor) error = %v, want ErrConflict", err)
		}
		assertChapterRestoreRolledBackForTest(t, fixture.db, editorInput)
	})
}

func TestLibraryStoreSaveChapterDraftRejectsStaleExpectedRevision(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1800)

	first, err := store.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID:        fixture.projectID,
		BookID:           fixture.bookID,
		ChapterID:        fixture.chapterID,
		ExpectedRevision: 1,
		Title:            "Fresh",
		Revision:         2,
		Status:           library.ChapterStatusDraft,
		UpdatedAt:        indexingStoreTestNow(),
		Paragraphs: []library.Paragraph{
			libraryStoreParagraphForTest(fixture.paragraphID, fixture.chapterID, 2, "fresh"),
		},
	})
	if err != nil {
		t.Fatalf("first SaveChapterDraft() error = %v", err)
	}
	if first.Revision != 2 {
		t.Fatalf("first saved revision = %d, want 2", first.Revision)
	}

	_, err = store.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID:        fixture.projectID,
		BookID:           fixture.bookID,
		ChapterID:        fixture.chapterID,
		ExpectedRevision: 1,
		Title:            "Stale",
		Revision:         2,
		Status:           library.ChapterStatusDraft,
		UpdatedAt:        indexingStoreTestNow().AddDate(0, 0, 1),
		Paragraphs: []library.Paragraph{
			libraryStoreParagraphForTest(fixture.paragraphID, fixture.chapterID, 2, "stale"),
		},
	})
	if !errors.Is(err, library.ErrConflict) {
		t.Fatalf("stale SaveChapterDraft() error = %v, want ErrConflict", err)
	}

	paragraphs, err := store.ListChapterParagraphs(ctx, library.ListChapterParagraphsStoreInput{
		ProjectID: fixture.projectID,
		BookID:    fixture.bookID,
		ChapterID: fixture.chapterID,
		Revision:  2,
		Variant:   library.ParagraphVariantDraft,
	})
	if err != nil {
		t.Fatalf("ListChapterParagraphs() error = %v", err)
	}
	if len(paragraphs) != 1 || paragraphs[0].Markdown != "fresh" {
		t.Fatalf("stale SaveChapterDraft overwrote paragraphs: %#v", paragraphs)
	}
}

func TestLibraryStoreCurrentScopeRejectsSubstitutionAndPostDeleteWriter(t *testing.T) {
	ctx := context.Background()
	fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
	otherProjectID := projects.NewSecureIDGenerator().NewProjectID()
	insertProjectStoreProjectForTest(
		t, fixture.db, otherProjectID, fixture.actorID, "Other Project", fixture.fencedAt.Add(-time.Hour),
	)
	store := New(fixture.db)

	if _, err := store.GetBook(ctx, otherProjectID, fixture.bookID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetBook(cross-project) error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetChapter(ctx, otherProjectID, fixture.bookID, fixture.chapterID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetChapter(cross-project) error = %v, want ErrNotFound", err)
	}

	if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.actorID,
		ProjectID:   fixture.projectID,
		BookID:      fixture.bookID,
		ChapterID:   fixture.chapterID,
		TombstoneID: projects.NewSecureIDGenerator().NewProjectID(),
		DeletedAt:   fixture.fencedAt,
	}); err != nil {
		t.Fatalf("FenceChapterDeletion() error = %v", err)
	}

	paragraphID := projects.NewSecureIDGenerator().NewProjectID()
	_, err := store.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID:        fixture.projectID,
		BookID:           fixture.bookID,
		ChapterID:        fixture.chapterID,
		ExpectedRevision: 1,
		Title:            "Stale writer",
		Revision:         2,
		Status:           library.ChapterStatusDraft,
		UpdatedAt:        fixture.fencedAt.Add(time.Minute),
		Paragraphs: []library.Paragraph{
			libraryStoreParagraphForTest(paragraphID, fixture.chapterID, 2, "must not persist"),
		},
	})
	if !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("SaveChapterDraft(after delete) error = %v, want ErrNotFound", err)
	}
	var paragraphCount int
	if err := fixture.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM chapter_paragraphs WHERE id = $1
	`, paragraphID).Scan(&paragraphCount); err != nil {
		t.Fatal(err)
	}
	if paragraphCount != 0 {
		t.Fatalf("post-delete writer persisted %d paragraph rows", paragraphCount)
	}
}

func TestLibraryStorePublishChapterAllowsDraftAndPublishedStableParagraphSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1810)
	now := indexingStoreTestNow()

	if _, err := store.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID:        fixture.projectID,
		BookID:           fixture.bookID,
		ChapterID:        fixture.chapterID,
		ExpectedRevision: 1,
		Title:            "Ready",
		Revision:         2,
		Status:           library.ChapterStatusDraft,
		UpdatedAt:        now,
		Paragraphs: []library.Paragraph{
			libraryStoreParagraphForTest(fixture.paragraphID, fixture.chapterID, 2, "draft"),
		},
	}); err != nil {
		t.Fatalf("SaveChapterDraft() error = %v", err)
	}

	published := libraryStoreParagraphForTest(fixture.paragraphID, fixture.chapterID, 2, "draft")
	published.Variant = library.ParagraphVariantPublished
	if _, err := store.PublishChapter(ctx, library.PublishChapterStoreInput{
		ProjectID:   fixture.projectID,
		BookID:      fixture.bookID,
		ChapterID:   fixture.chapterID,
		Revision:    2,
		PublishedAt: now,
		Paragraphs:  []library.Paragraph{published},
	}); err != nil {
		t.Fatalf("PublishChapter() error = %v", err)
	}

	draftParagraphs, err := store.ListChapterParagraphs(ctx, library.ListChapterParagraphsStoreInput{
		ProjectID: fixture.projectID,
		BookID:    fixture.bookID,
		ChapterID: fixture.chapterID,
		Revision:  2,
		Variant:   library.ParagraphVariantDraft,
	})
	if err != nil {
		t.Fatalf("ListChapterParagraphs(draft) error = %v", err)
	}
	publishedParagraphs, err := store.ListChapterParagraphs(ctx, library.ListChapterParagraphsStoreInput{
		ProjectID: fixture.projectID,
		BookID:    fixture.bookID,
		ChapterID: fixture.chapterID,
		Revision:  2,
		Variant:   library.ParagraphVariantPublished,
	})
	if err != nil {
		t.Fatalf("ListChapterParagraphs(published) error = %v", err)
	}
	if len(draftParagraphs) != 1 || len(publishedParagraphs) != 1 {
		t.Fatalf("snapshot counts draft/published = %d/%d, want 1/1", len(draftParagraphs), len(publishedParagraphs))
	}
	if draftParagraphs[0].ID != fixture.paragraphID || publishedParagraphs[0].ID != fixture.paragraphID {
		t.Fatalf("snapshot paragraph IDs = %q/%q, want stable %q", draftParagraphs[0].ID, publishedParagraphs[0].ID, fixture.paragraphID)
	}
}

func TestLibraryStoreCreateAndUpdateBookHonorAfterBookID(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1820)
	now := indexingStoreTestNow()

	secondID := indexingStoreUUIDForTest(1827)
	thirdID := indexingStoreUUIDForTest(1828)
	insertedID := indexingStoreUUIDForTest(1829)
	createBookForOrderTest(t, store, fixture.projectID, secondID, "Second", 2, "2", now)
	createBookForOrderTest(t, store, fixture.projectID, thirdID, "Third", 3, "3", now)

	afterFirst := fixture.bookID
	inserted, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID:            insertedID,
		ProjectID:     fixture.projectID,
		Title:         "Inserted",
		Order:         4,
		DisplayNumber: "4",
		CreatedAt:     now,
		AfterBookID: library.OrderPlacement{
			Present: true,
			AfterID: &afterFirst,
		},
	})
	if err != nil {
		t.Fatalf("CreateBook(afterBookID) error = %v", err)
	}
	if inserted.Order != 2 {
		t.Fatalf("inserted book order = %d, want 2", inserted.Order)
	}
	assertLibraryStoreBookOrder(t, store, fixture.projectID, []string{fixture.bookID, insertedID, secondID, thirdID})

	moved, err := store.UpdateBook(ctx, library.UpdateBookStoreInput{
		ProjectID:   fixture.projectID,
		BookID:      thirdID,
		Title:       "Third",
		UpdatedAt:   now,
		AfterBookID: library.OrderPlacement{Present: true},
	})
	if err != nil {
		t.Fatalf("UpdateBook(afterBookID=null) error = %v", err)
	}
	if moved.Order != 1 {
		t.Fatalf("moved book order = %d, want 1", moved.Order)
	}
	assertLibraryStoreBookOrder(t, store, fixture.projectID, []string{thirdID, fixture.bookID, insertedID, secondID})
}

func TestLibraryStoreBookOrderMutationsLockProjectBeforeBooks(t *testing.T) {
	t.Run("reorder", func(t *testing.T) {
		db := openCurrentStoreTestDB(t)
		store := New(db)
		fixture := createIndexingStoreFixtureForTest(t, db, 1830)
		now := indexingStoreTestNow()
		secondID := indexingStoreUUIDForTest(1837)
		thirdID := indexingStoreUUIDForTest(1838)
		createBookForOrderTest(t, store, fixture.projectID, secondID, "Second", 2, "2", now)
		createBookForOrderTest(t, store, fixture.projectID, thirdID, "Third", 3, "3", now)

		runBookOrderMutationBehindTerminalLocks(t, db, fixture.projectID, func(ctx context.Context, mutationStore *Store) error {
			_, err := mutationStore.UpdateBook(ctx, library.UpdateBookStoreInput{
				ProjectID: fixture.projectID,
				BookID:    thirdID,
				Title:     "Third",
				UpdatedAt: now.Add(time.Second),
				AfterBookID: library.OrderPlacement{
					Present: true,
				},
			})
			return err
		})
		assertLibraryStoreBookOrder(t, store, fixture.projectID, []string{thirdID, fixture.bookID, secondID})
	})

	t.Run("placement-create", func(t *testing.T) {
		db := openCurrentStoreTestDB(t)
		store := New(db)
		fixture := createIndexingStoreFixtureForTest(t, db, 1850)
		now := indexingStoreTestNow()
		secondID := indexingStoreUUIDForTest(1857)
		insertedID := indexingStoreUUIDForTest(1858)
		createBookForOrderTest(t, store, fixture.projectID, secondID, "Second", 2, "2", now)

		afterFirst := fixture.bookID
		runBookOrderMutationBehindTerminalLocks(t, db, fixture.projectID, func(ctx context.Context, mutationStore *Store) error {
			_, err := mutationStore.CreateBook(ctx, library.CreateBookStoreInput{
				ID:            insertedID,
				ProjectID:     fixture.projectID,
				Title:         "Inserted",
				Order:         3,
				DisplayNumber: "3",
				CreatedAt:     now.Add(time.Second),
				AfterBookID: library.OrderPlacement{
					Present: true,
					AfterID: &afterFirst,
				},
			})
			return err
		})
		assertLibraryStoreBookOrder(t, store, fixture.projectID, []string{fixture.bookID, insertedID, secondID})
	})
}

func TestLibraryStoreCreateChapterHonorsAfterChapterID(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1830)
	now := indexingStoreTestNow()

	secondID := indexingStoreUUIDForTest(1837)
	insertedID := indexingStoreUUIDForTest(1838)
	createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, secondID, "Second", 2, now)

	afterFirst := fixture.chapterID
	inserted, err := store.CreateChapterWithDraft(ctx, library.CreateChapterWithDraftStoreInput{
		ID:        insertedID,
		ProjectID: fixture.projectID,
		BookID:    fixture.bookID,
		Title:     "Inserted",
		Order:     3,
		CreatedAt: now,
		AfterChapterID: library.OrderPlacement{
			Present: true,
			AfterID: &afterFirst,
		},
	})
	if err != nil {
		t.Fatalf("CreateChapterWithDraft(afterChapterID) error = %v", err)
	}
	if inserted.Order != 2 {
		t.Fatalf("inserted chapter order = %d, want 2", inserted.Order)
	}
	assertLibraryStoreChapterOrder(t, store, fixture.projectID, fixture.bookID, []string{fixture.chapterID, insertedID, secondID})
}

func TestLibraryStoreListChaptersReturnsOrderedBookChapters(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1840)
	now := indexingStoreTestNow()
	secondID := indexingStoreUUIDForTest(1847)
	createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, secondID, "Second", 2, now)

	chapters, err := store.ListChapters(ctx, library.ListChaptersStoreInput{
		ProjectID: fixture.projectID,
		BookID:    fixture.bookID,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("ListChapters() error = %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapter count = %d, want 2: %#v", len(chapters), chapters)
	}
	if chapters[0].ID != fixture.chapterID || chapters[0].Order != 1 || chapters[1].ID != secondID || chapters[1].Order != 2 {
		t.Fatalf("chapter order = %#v, want fixture chapter then second", chapters)
	}

	limited, err := store.ListChapters(ctx, library.ListChaptersStoreInput{
		ProjectID: fixture.projectID,
		BookID:    fixture.bookID,
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("ListChapters(limit=1) error = %v", err)
	}
	if len(limited) != 1 || limited[0].ID != fixture.chapterID {
		t.Fatalf("limited chapters = %#v, want first chapter only", limited)
	}
}

func TestLibraryStoreGetChapterReturnsNavigationAcrossEntireBook(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1850)
	now := indexingStoreTestNow()

	chapterIDs := make([]string, 105)
	chapterIDs[0] = fixture.chapterID
	rows := make([][]any, 0, len(chapterIDs)-1)
	for order := 2; order <= len(chapterIDs); order++ {
		chapterID := indexingStoreUUIDForTest(3000 + order)
		chapterIDs[order-1] = chapterID
		rows = append(rows, []any{chapterID, fixture.bookID, fixture.projectID, "Chapter", order, 1, now, now})
	}
	if _, err := db.Pool.CopyFrom(ctx, pgx.Identifier{"chapters"}, []string{
		"id", "book_id", "project_id", "title", "order", "lifecycle_generation", "created_at", "updated_at",
	}, pgx.CopyFromRows(rows)); err != nil {
		t.Fatalf("seed chapters for navigation: %v", err)
	}

	for _, tc := range []struct {
		name             string
		index            int
		previousExpected bool
		nextExpected     bool
	}{
		{name: "first", index: 0, nextExpected: true},
		{name: "middle", index: 52, previousExpected: true, nextExpected: true},
		{name: "last", index: len(chapterIDs) - 1, previousExpected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := store.GetChapter(ctx, fixture.projectID, fixture.bookID, chapterIDs[tc.index])
			if err != nil {
				t.Fatalf("GetChapter() error = %v", err)
			}
			if record.Navigation.Position != tc.index+1 || record.Navigation.Total != len(chapterIDs) {
				t.Fatalf("navigation position = %d/%d, want %d/%d", record.Navigation.Position, record.Navigation.Total, tc.index+1, len(chapterIDs))
			}
			if (record.Navigation.Previous != nil) != tc.previousExpected || (record.Navigation.Next != nil) != tc.nextExpected {
				t.Fatalf("navigation neighbors = previous %#v next %#v", record.Navigation.Previous, record.Navigation.Next)
			}
			if tc.previousExpected && record.Navigation.Previous.ID != chapterIDs[tc.index-1] {
				t.Fatalf("previous chapter = %q, want %q", record.Navigation.Previous.ID, chapterIDs[tc.index-1])
			}
			if tc.nextExpected && record.Navigation.Next.ID != chapterIDs[tc.index+1] {
				t.Fatalf("next chapter = %q, want %q", record.Navigation.Next.ID, chapterIDs[tc.index+1])
			}
		})
	}
}

func TestLibraryStoreBookSummariesIncludeCounts(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1860)
	now := indexingStoreTestNow()
	secondChapterID := indexingStoreUUIDForTest(1867)
	secondParagraphID := indexingStoreUUIDForTest(1868)
	createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, secondChapterID, "Second", 2, now)
	insertIndexingStoreParagraphForTest(t, db, secondParagraphID, secondChapterID, now)
	got, err := store.GetBook(ctx, fixture.projectID, fixture.bookID)
	if err != nil {
		t.Fatalf("GetBook() error = %v", err)
	}
	if got.ChapterCount != 2 || got.WordCount != 2 {
		t.Fatalf("GetBook() counts = chapterCount %d wordCount %d, want 2/2", got.ChapterCount, got.WordCount)
	}
	books, err := store.ListBooks(ctx, library.ListBooksStoreInput{
		ProjectID: fixture.projectID,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("ListBooks() error = %v", err)
	}
	if len(books) != 1 || books[0].ID != fixture.bookID || books[0].ChapterCount != 2 || books[0].WordCount != 2 {
		t.Fatalf("ListBooks() summary = %#v, want fixture book with counts", books)
	}
}

// The shelf knows how each book begins (specs/2026-11-11-ux9-shelf-chat.md SC1.3): the first paragraphs of its first live
// chapter, by which a file of the same text is asked about as this book, whatever its name.
func TestLibraryStoreListBooksTellsHowABookBegins(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1870)
	now := indexingStoreTestNow()
	if _, err := db.Pool.Exec(ctx, `
		UPDATE chapter_paragraphs SET markdown = 'Туман лежал на *Белом* порту.', plain_text = 'Туман лежал на Белом порту.' WHERE id = $1
	`, fixture.paragraphID); err != nil {
		t.Fatalf("first paragraph: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chapter_paragraphs (id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at)
		VALUES ($1, $2, 1, 'draft', 'paragraph', 2, 'Мара ждала.', 'Мара ждала.', 2, $3, $3),
		       ($4, $2, 1, 'draft', 'paragraph', 3, ' ', ' ', 0, $3, $3)
	`, indexingStoreUUIDForTest(1877), fixture.chapterID, now, indexingStoreUUIDForTest(1878)); err != nil {
		t.Fatalf("second paragraph: %v", err)
	}
	createChapterForOrderTest(t, store, fixture.projectID, fixture.bookID, indexingStoreUUIDForTest(1879), "Second", 2, now)
	books, err := store.ListBooks(ctx, library.ListBooksStoreInput{ProjectID: fixture.projectID, Limit: 20})
	if err != nil {
		t.Fatalf("ListBooks() error = %v", err)
	}
	if len(books) != 1 || books[0].Opening != "Туман лежал на Белом порту. Мара ждала." {
		t.Fatalf("ListBooks() opening = %#v, want the first paragraphs of the first chapter", books)
	}
}

// A new book stands after the live books of the shelf (specs/2026-11-11-ux7-upload.md UP2.3).
func TestLibraryStoreNextBookOrderStandsAfterTheBooks(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := indexingStoreTestNow()

	for caseIndex, tt := range []struct {
		name      string
		orders    []int
		wantOrder int
		wantErr   error
	}{
		{name: "empty", wantOrder: 1},
		{name: "contiguous", orders: []int{1, 2}, wantOrder: 3},
		{name: "missing first", orders: []int{2, 3}, wantOrder: 4},
		{name: "missing middle", orders: []int{1, 3}, wantOrder: 4},
		{name: "duplicate", orders: []int{1, 2, 2}, wantErr: library.ErrImportSequenceConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := 3000 + caseIndex*20
			actorID := indexingStoreUUIDForTest(base + 1)
			projectID := indexingStoreUUIDForTest(base + 2)
			insertProjectStoreUserForTest(t, db, actorID, fmt.Sprintf("book-order-%d@example.test", base))
			insertProjectStoreProjectForTest(t, db, projectID, actorID, "Book Order", now)
			for index, order := range tt.orders {
				if _, err := db.Pool.Exec(ctx, `
					INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
					VALUES ($1, $2, $3, $4, $5, $6, $6)
				`, indexingStoreUUIDForTest(base+3+index), projectID, fmt.Sprintf("Book %d", index+1), order, fmt.Sprint(order), now); err != nil {
					t.Fatalf("insert book order %d: %v", order, err)
				}
			}

			got, err := New(db).NextBookOrder(ctx, projectID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NextBookOrder error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got != tt.wantOrder {
				t.Fatalf("NextBookOrder = %d, want %d", got, tt.wantOrder)
			}
		})
	}
}

func TestLibraryStoreNextBookOrderDoesNotWaitOnProjectMutationLock(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := indexingStoreTestNow()
	actorID := indexingStoreUUIDForTest(3291)
	projectID := indexingStoreUUIDForTest(3292)
	insertProjectStoreUserForTest(t, db, actorID, "book-order-read-lock@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Book Order Read", now)
	createBookForOrderTest(t, New(db), projectID, indexingStoreUUIDForTest(3293), "Book 1", 1, "1", now)

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var lockedProjectID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM projects WHERE id = $1 FOR UPDATE`, projectID).Scan(&lockedProjectID); err != nil {
		t.Fatal(err)
	}

	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	got, err := New(db).NextBookOrder(readCtx, projectID)
	if err != nil {
		t.Fatalf("NextBookOrder() waited on the project row lock: %v", err)
	}
	if got != 2 {
		t.Fatalf("NextBookOrder() = %d, want 2", got)
	}
}

// The place a deleted book keeps stays for its return: a new book stands after the books (UP2.3).
func TestLibraryStoreDeleteMiddleBookKeepsItsPlaceForItsReturn(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	now := indexingStoreTestNow()
	actorID := indexingStoreUUIDForTest(3221)
	projectID := indexingStoreUUIDForTest(3222)
	membershipID := indexingStoreUUIDForTest(3223)
	insertProjectStoreUserForTest(t, db, actorID, "delete-middle-order@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Delete Middle", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
	for order := 1; order <= 3; order++ {
		createBookForOrderTest(t, store, projectID, indexingStoreUUIDForTest(3223+order), fmt.Sprintf("Book %d", order), order, fmt.Sprint(order), now)
	}
	if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: indexingStoreUUIDForTest(3225),
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	if got, err := store.NextBookOrder(ctx, projectID); err != nil || got != 4 {
		t.Fatalf("NextBookOrder() = %d, %v, want 4, nil", got, err)
	}
}

// specs/2026-11-11-ux7-reorder.md RK1.1: the keys of the chapters do not depend on the places, so a book comes back to
// its place on the shelf and the book that took it meanwhile steps one place on.
func TestLibraryStoreRestoreBookTakesItsPlaceBack(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	// A deletion of now: the book is still in «Недавно удалённые».
	now := time.Now().UTC().Truncate(time.Microsecond)
	actorID := indexingStoreUUIDForTest(3301)
	projectID := indexingStoreUUIDForTest(3302)
	membershipID := indexingStoreUUIDForTest(3303)
	deletedBookID := indexingStoreUUIDForTest(3304)
	replacementBookID := indexingStoreUUIDForTest(3305)
	insertProjectStoreUserForTest(t, db, actorID, "restore-reused-order@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore reused order", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
	createBookForOrderTest(t, store, projectID, deletedBookID, "Deleted", 1, "1", now)
	if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
		BookID:      deletedBookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
		DeletedAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	createBookForOrderTest(t, store, projectID, replacementBookID, "Replacement", 1, "1", now.Add(2*time.Minute))

	restored, err := store.RestoreBook(ctx, restoreBookInputForTest(actorID, projectID, deletedBookID))
	if err != nil || restored.Order != 1 {
		t.Fatalf("RestoreBook() = order %d, %v; want its place 1", restored.Order, err)
	}
	if got := countJobsStoreRowsForTest(t, db, `
		SELECT count(*) FROM books WHERE project_id = $1 AND id = $2 AND "order" = 2 AND deleted_at IS NULL
	`, projectID, replacementBookID); got != 1 {
		t.Fatalf("the book that took the place stands at 2: %d", got)
	}
}

func libraryStoreParagraphForTest(paragraphID, chapterID string, revision int64, markdown string) library.Paragraph {
	return library.Paragraph{
		ID:        paragraphID,
		ChapterID: chapterID,
		Revision:  revision,
		Variant:   library.ParagraphVariantDraft,
		Kind:      library.ParagraphKindParagraph,
		Order:     1,
		Markdown:  markdown,
		PlainText: markdown,
		WordCount: 1,
		CreatedAt: indexingStoreTestNow(),
		UpdatedAt: indexingStoreTestNow(),
	}
}

func createBookForOrderTest(t *testing.T, store *Store, projectID, bookID, title string, order int, displayNumber string, now time.Time) {
	t.Helper()
	if _, err := store.CreateBook(context.Background(), library.CreateBookStoreInput{
		ID:            bookID,
		ProjectID:     projectID,
		Title:         title,
		Order:         order,
		DisplayNumber: displayNumber,
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("CreateBook(%s) error = %v", title, err)
	}
}

func runBookOrderMutationBehindTerminalLocks(t *testing.T, db *storedb.DB, projectID string, mutate func(context.Context, *Store) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mutationConfig := db.Pool.Config().Copy()
	mutationConfig.MaxConns = 1
	mutationConfig.MinConns = 1
	mutationPool, err := pgxpool.NewWithConfig(ctx, mutationConfig)
	if err != nil {
		t.Fatal(err)
	}
	mutationDB := &storedb.DB{Pool: mutationPool}
	defer mutationDB.Close()
	mutationConn, err := mutationPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mutationPID int
	err = mutationConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&mutationPID)
	mutationConn.Release()
	if err != nil {
		t.Fatal(err)
	}

	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		t.Fatal(err)
	}
	terminalTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = terminalTx.Rollback(context.Background()) }()
	projectGeneration, err := lockCanonTextTerminalProjectForLibraryTest(ctx, terminalTx, projectUUID)
	if err != nil {
		t.Fatal(err)
	}
	var catalogRevision int64
	if err := terminalTx.QueryRow(ctx, `
		SELECT catalog_revision
		FROM project_source_views
		WHERE project_id = $1 AND source_view = 'draft'
	`, projectUUID).Scan(&catalogRevision); err != nil {
		t.Fatal(err)
	}
	if err := lockCanonTextTerminalCatalogForLibraryTest(ctx, terminalTx, projectUUID, projectGeneration, jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: projectID, SourceView: governance.SourceViewDraft, CatalogRevision: uint64(catalogRevision),
	}); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() { result <- mutate(ctx, New(mutationDB)) }()
	waitForBookOrderMutationProjectLock(t, ctx, db, mutationDB, terminalTx, mutationPID, result)
	if err := terminalTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var mutationErr error
	select {
	case mutationErr = <-result:
	case <-ctx.Done():
		t.Fatal("book order mutation did not finish after terminal locks were released")
	}
	var postgresErr *pgconn.PgError
	if errors.As(mutationErr, &postgresErr) && postgresErr.Code == "40P01" {
		t.Fatalf("book order mutation deadlocked: %v", mutationErr)
	}
	if mutationErr != nil {
		t.Fatalf("book order mutation error = %v", mutationErr)
	}
}

func waitForBookOrderMutationProjectLock(t *testing.T, ctx context.Context, terminalDB, mutationDB *storedb.DB, terminalTx pgx.Tx, mutationPID int, result <-chan error) {
	t.Helper()
	waitForLibraryMutationLock(t, ctx, terminalTx, mutationPID, result, "book order mutation", "from projects", "project")
	if got := terminalDB.Pool.Stat().AcquiredConns() + mutationDB.Pool.Stat().AcquiredConns(); got != 2 {
		t.Fatalf("acquired database connections = %d, want 2", got)
	}
}

func waitForLibraryMutationLock(t *testing.T, ctx context.Context, observer pgx.Tx, mutationPID int, result <-chan error, operation, queryFragment, lockTarget string) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			t.Fatalf("%s completed before the expected lock wait: %v", operation, err)
		default:
		}

		if _, err := observer.Exec(waitCtx, `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatal(err)
		}
		var query string
		err := observer.QueryRow(waitCtx, `
			SELECT activity.query
			FROM pg_stat_activity AS activity
			WHERE activity.pid = $1
			  AND activity.state = 'active'
			  AND activity.wait_event_type = 'Lock'
		`, mutationPID).Scan(&query)
		if err == nil {
			query = strings.Join(strings.Fields(strings.ToLower(query)), " ")
			if !strings.Contains(query, queryFragment) || !strings.Contains(query, "for update") {
				t.Fatalf("%s waited on %q before acquiring the %s FOR UPDATE lock", operation, query, lockTarget)
			}
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			t.Fatalf("%s completed before the expected lock wait: %v", operation, err)
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatalf("%s did not contend for the %s FOR UPDATE lock", operation, lockTarget)
		}
	}
}

type governedChapterFixtureForTest struct {
	db           *storedb.DB
	actorID      string
	projectID    string
	bookID       string
	chapterID    string
	membershipID string
	fencedAt     time.Time
}

func newGovernedChapterFixtureForTest(t *testing.T, role projects.Role, removed bool) governedChapterFixtureForTest {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	fixture := governedChapterFixtureForTest{
		db:           db,
		actorID:      ids.NewProjectID(),
		projectID:    ids.NewProjectID(),
		bookID:       ids.NewProjectID(),
		chapterID:    ids.NewProjectID(),
		membershipID: ids.NewProjectMembershipID(),
		fencedAt:     time.Now().UTC().Truncate(time.Microsecond),
	}
	insertProjectStoreUserForTest(t, db, fixture.actorID, fixture.actorID+"@chapter-fixture.example.test")
	insertProjectStoreProjectForTest(
		t, db, fixture.projectID, fixture.actorID, "Governed Chapter Project", fixture.fencedAt.Add(-3*time.Hour),
	)
	insertProjectStoreMembershipForTest(
		t, db, fixture.membershipID, fixture.projectID, fixture.actorID, role, fixture.fencedAt.Add(-3*time.Hour), removed,
	)
	baseStore := New(db)
	createBookForOrderTest(
		t, baseStore, fixture.projectID, fixture.bookID, "Governed Chapter Book", 1, "1", fixture.fencedAt.Add(-2*time.Hour),
	)
	createChapterForOrderTest(
		t, baseStore, fixture.projectID, fixture.bookID, fixture.chapterID, "Governed Chapter", 1, fixture.fencedAt.Add(-time.Hour),
	)
	return fixture
}

func assertChapterDeletionRolledBackForTest(
	t *testing.T,
	db *storedb.DB,
	fixture governedChapterFixtureForTest,
	tombstoneID string,
) {
	t.Helper()
	ctx := context.Background()
	var deletedAt pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, lifecycle_generation
		FROM chapters
		WHERE id = $1 AND book_id = $2 AND project_id = $3
	`, fixture.chapterID, fixture.bookID, fixture.projectID).Scan(&deletedAt, &generation); err != nil {
		t.Fatal(err)
	}
	if deletedAt.Valid || generation != 1 {
		t.Fatalf("chapter mutation survived rollback: deleted=%v generation=%d", deletedAt, generation)
	}
	var tombstoneCount, targetCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE id = $1`, tombstoneID).Scan(&tombstoneCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_target_tasks WHERE tombstone_id = $1`, tombstoneID).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 0 || targetCount != 0 {
		t.Fatalf("chapter deletion outbox survived rollback: tombstones=%d targets=%d", tombstoneCount, targetCount)
	}
}

func restoreChapterInputForTest(fixture governedChapterFixtureForTest) library.RestoreChapterStoreInput {
	return library.RestoreChapterStoreInput{
		ActorUserID: fixture.actorID,
		ProjectID:   fixture.projectID,
		BookID:      fixture.bookID,
		ChapterID:   fixture.chapterID,
	}
}

func assertChapterRestoreRolledBackForTest(t *testing.T, db *storedb.DB, input library.RestoreChapterStoreInput) {
	t.Helper()
	ctx := context.Background()
	var deletedAt pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, lifecycle_generation
		FROM chapters
		WHERE id = $1 AND book_id = $2 AND project_id = $3
	`, input.ChapterID, input.BookID, input.ProjectID).Scan(&deletedAt, &generation); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid || generation != 2 {
		t.Fatalf("chapter restore survived rollback: deleted=%v generation=%d", deletedAt, generation)
	}
}

func assertBookDeletionRolledBackForTest(t *testing.T, db *storedb.DB, projectID, bookID, tombstoneID string) {
	t.Helper()
	ctx := context.Background()
	var deletedAt pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, lifecycle_generation
		FROM books WHERE id = $1 AND project_id = $2
	`, bookID, projectID).Scan(&deletedAt, &generation); err != nil {
		t.Fatal(err)
	}
	if deletedAt.Valid || generation != 1 {
		t.Fatalf("book mutation survived rollback: deleted=%v generation=%d", deletedAt, generation)
	}
	var tombstoneCount, targetCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE id = $1`, tombstoneID).Scan(&tombstoneCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_target_tasks WHERE tombstone_id = $1`, tombstoneID).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 0 || targetCount != 0 {
		t.Fatalf("book deletion outbox survived rollback: tombstones=%d targets=%d", tombstoneCount, targetCount)
	}
}

func restoreBookInputForTest(actorID, projectID, bookID string) library.RestoreBookStoreInput {
	return library.RestoreBookStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
		BookID:      bookID,
	}
}

func assertBookRestoreRolledBackForTest(t *testing.T, db *storedb.DB, input library.RestoreBookStoreInput) {
	t.Helper()
	ctx := context.Background()
	var deletedAt pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, lifecycle_generation
		FROM books WHERE id = $1 AND project_id = $2
	`, input.BookID, input.ProjectID).Scan(&deletedAt, &generation); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid || generation != 2 {
		t.Fatalf("book restore survived rollback: deleted=%v generation=%d", deletedAt, generation)
	}
}

func createChapterForOrderTest(t *testing.T, store *Store, projectID, bookID, chapterID, title string, order int, now time.Time) {
	t.Helper()
	if _, err := store.CreateChapterWithDraft(context.Background(), library.CreateChapterWithDraftStoreInput{
		ID:        chapterID,
		ProjectID: projectID,
		BookID:    bookID,
		Title:     title,
		Order:     order,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateChapterWithDraft(%s) error = %v", title, err)
	}
}

func assertLibraryStoreBookOrder(t *testing.T, store *Store, projectID string, wantIDs []string) {
	t.Helper()
	books, err := store.ListBooks(context.Background(), library.ListBooksStoreInput{
		ProjectID: projectID,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("ListBooks() error = %v", err)
	}
	if len(books) != len(wantIDs) {
		t.Fatalf("book count = %d, want %d: %#v", len(books), len(wantIDs), books)
	}
	for i, wantID := range wantIDs {
		if books[i].ID != wantID {
			t.Fatalf("book order[%d] = %s, want %s", i, books[i].ID, wantID)
		}
		if books[i].Order != i+1 {
			t.Fatalf("book %s order = %d, want %d", books[i].ID, books[i].Order, i+1)
		}
	}
}

func assertLibraryStoreChapterOrder(t *testing.T, store *Store, projectID, bookID string, wantIDs []string) {
	t.Helper()
	for i, wantID := range wantIDs {
		record, err := store.GetChapter(context.Background(), projectID, bookID, wantID)
		if err != nil {
			t.Fatalf("GetChapter(%s) error = %v", wantID, err)
		}
		if record.Chapter.Order != i+1 {
			t.Fatalf("chapter %s order = %d, want %d", wantID, record.Chapter.Order, i+1)
		}
	}
}
