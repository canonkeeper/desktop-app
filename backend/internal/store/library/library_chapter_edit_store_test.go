package librarystore

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-08-subscription-tiers.md TR2.5: a write checks only what it grows. An owner whose books are over his
// limit — a lowered limit, or the plan that ended — edits a chapter and adds a chapter to his book, but a new book and
// a restored one are refused with quota.account_books.
func TestOwnerOverTheBookLimitEditsAndAddsChaptersButGetsNoNewBook(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	first, err := fixture.importText(library.ImportModeNewBook, "", 300)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.importText(library.ImportModeNewBook, "", 300); err != nil {
		t.Fatal(err)
	}
	deleted, err := fixture.importText(library.ImportModeNewBook, "", 300)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: deleted.BookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	fixture.limit(fixture.ownerID, quota.KeyAccountBooks, 1)

	var chapterID string
	var revision int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT id::text, revision FROM chapters WHERE book_id = $1 AND deleted_at IS NULL ORDER BY "order" LIMIT 1
	`, first.BookID).Scan(&chapterID, &revision); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		_, err := fixture.store.ReplaceChapterTextInTx(fixture.ctx, tx, library.ReplaceChapterTextInput{
			ProjectID: fixture.projectID, BookID: first.BookID, ChapterID: chapterID, ExpectedRevision: revision,
			Title: "Chapter", Paragraphs: []string{"Правка главы автора, у которого книг больше предела."},
		})
		return err
	}); err != nil {
		t.Fatalf("an edit of a chapter over the book limit: %v", err)
	}
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		_, err := fixture.store.AddChapterInTx(fixture.ctx, tx, library.AddChapterInput{
			ProjectID: fixture.projectID, BookID: first.BookID, AfterChapterID: chapterID, Title: "Новая глава",
			Paragraphs: []string{"Глава в своей книге."}, At: time.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("a new chapter in his own book over the book limit: %v", err)
	}

	_, err = fixture.store.CreateBook(fixture.ctx, library.CreateBookStoreInput{
		ID: "00000000-0000-4000-8000-000000000a7f", ProjectID: fixture.projectID, Title: "Ещё книга",
		Order: 3, DisplayNumber: "3", CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	wantQuotaKey(t, err, quota.KeyAccountBooks)
	_, err = fixture.store.RestoreBook(fixture.ctx, library.RestoreBookStoreInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: deleted.BookID,
	})
	wantQuotaKey(t, err, quota.KeyAccountBooks)
	if live := fixture.count(`SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL`, fixture.projectID); live != 2 {
		t.Fatalf("live books = %d, want the 2 he had", live)
	}
}
