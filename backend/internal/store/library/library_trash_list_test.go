package librarystore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/library"
	trashrepo "canonkeeper/backend/internal/repository/postgres/trash"
)

// specs/2026-11-11-ux7-trash.md TB1.1: «Недавно удалённые» list in one read what an author may return in every saga of his:
// a deleted book with its saga and number, deleted chapters of two books with their book — not of the selected one
// alone. An editor sees them too; a stranger sees nothing. The sagas are TestRecentlyDeletedListsTheSagasOfTheAuthor.
func TestRecentlyDeletedListsEverySagaOfTheAuthor(t *testing.T) {
	ctx := context.Background()
	fixture := newDeletionWorkFixture(t, "711", 3)
	const editorID, strangerID = "00000000-0000-4000-8000-000000071001", "00000000-0000-4000-8000-000000071002"
	for _, statement := range []string{
		`INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ('` + editorID + `','trash-editor@example.test','E','hash',now())`,
		`INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ('` + strangerID + `','trash-stranger@example.test','S','hash',now())`,
		`INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES (gen_random_uuid(),'` + fixture.projectID + `','` + editorID + `','editor',now(),now())`,
	} {
		if _, err := fixture.db.Pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for index, bookID := range fixture.bookIDs[:2] {
		if _, err := fixture.store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: bookID, ChapterID: fixture.chapterOf(t, bookID),
			TombstoneID: testUUID(71_100 + index), DeletedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("FenceChapterDeletion() = %v", err)
		}
	}
	if _, err := fixture.store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookIDs[2],
		TombstoneID: testUUID(71_200), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() = %v", err)
	}
	trash := trashrepo.New(fixture.db)
	for _, person := range []string{fixture.actorID, editorID} {
		sources, err := trash.ListDeletedSources(ctx, person, 100)
		if err != nil {
			t.Fatal(err)
		}
		if sources.Sagas.Total != 0 || sources.Books.Total != 1 || sources.Chapters.Total != 2 || len(sources.Chapters.Items) != 2 {
			t.Fatalf("the list of %s = %+v", person, sources)
		}
		book := sources.Books.Items[0]
		if book.ID != fixture.bookIDs[2] || book.Title != "Том 3" || book.SagaTitle == "" ||
			!book.RestoreUntil.After(book.DeletedAt) {
			t.Fatalf("the deleted book = %+v", book)
		}
		chapterBooks := map[string]bool{}
		for _, chapter := range sources.Chapters.Items {
			// The book of a deleted chapter is named by its place among the living books (specs/2026-11-11-ux7-reorder.md RK1.6).
			chapterBooks[chapter.BookID] = chapter.BookTitle != "" && chapter.Title == "Глава" && chapter.Order == 1 &&
				chapter.BookDisplayNumber == map[string]string{fixture.bookIDs[0]: "1", fixture.bookIDs[1]: "2"}[chapter.BookID]
		}
		if !chapterBooks[fixture.bookIDs[0]] || !chapterBooks[fixture.bookIDs[1]] {
			t.Fatalf("the deleted chapters = %+v", sources.Chapters.Items)
		}
	}
	if sources, err := trash.ListDeletedSources(ctx, strangerID, 100); err != nil || sources.Books.Total+sources.Chapters.Total+sources.Sagas.Total != 0 {
		t.Fatalf("the list of a stranger = %+v, %v", sources, err)
	}
	if limited, err := trash.ListDeletedSources(ctx, fixture.actorID, 1); err != nil || len(limited.Chapters.Items) != 1 || limited.Chapters.Total != 2 {
		t.Fatalf("a list cut to one row a group = %+v, %v", limited, err)
	}

}
