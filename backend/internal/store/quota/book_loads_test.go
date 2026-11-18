package quotastore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-18-free-tier.md FT1.3, FT1.4, FT1.5: when a book of the owner loads once, the books of his account are
// every book that ever came into his sagas — a deleted one too —, compared first with his books before a new one and
// again after it is written; a book without text and a new version of a book are refused; a return from the trash is
// never refused by that count. When a book loads again the fence is as before.
func TestABookLoadedOnceCountsEveryBookThatCameIntoTheSagas(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_book_loads")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "book-loads@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	seed.book(quotaProjectA, false, 100)
	seed.book(quotaProjectA, true, 100)
	seed.exec(`INSERT INTO writer_quota_overrides (user_id, book_reloads, project_books, account_books, updated_at)
		VALUES ($1, 0, 5, 2, $2)`, quotaWriterA, seed.now)
	ctx := context.Background()
	fence := Fence{}
	inTx := func(check func(tx pgx.Tx) error) error { return db.WithTx(ctx, check) }
	refusedBy := func(name string, err error, key quota.Key, limit int64) {
		t.Helper()
		if exceeded, ok := quota.AsExceeded(err); !ok || exceeded.Key != key || exceeded.Limit != limit {
			t.Fatalf("%s = %v; want %s with the limit %d", name, err, key, limit)
		}
	}

	account, err := New(db, fence).AccountQuota(ctx, quotaWriterA, seed.now)
	if err != nil {
		t.Fatal(err)
	}
	if account.BooksLoaded != 2 || account.Sources.Books != 1 || account.Limits.BookReloads != 0 {
		t.Fatalf("the account loaded %d books with %d live, book_reloads %d; want 2, 1, 0", account.BooksLoaded,
			account.Sources.Books, account.Limits.BookReloads)
	}

	// One live book of two allowed, but the deleted one took the second place for good.
	refusedBy("a new book after a deleted one", inTx(func(tx pgx.Tx) error {
		return fence.CheckNewBooksInTx(ctx, tx, quotaProjectA, 1, true)
	}), quota.KeyBookReloads, 2)
	// A book without text can never get one: refused at once, whatever the count.
	seed.exec(`UPDATE writer_quota_overrides SET account_books = 5 WHERE user_id = $1`, quotaWriterA)
	refusedBy("an empty book", inTx(func(tx pgx.Tx) error {
		return fence.CheckNewBooksInTx(ctx, tx, quotaProjectA, 1, false)
	}), quota.KeyBookReloads, 5)
	if err := inTx(func(tx pgx.Tx) error { return fence.CheckNewBooksInTx(ctx, tx, quotaProjectA, 1, true) }); err != nil {
		t.Fatalf("a new book within the count: %v", err)
	}
	refusedBy("a new version of a book", inTx(func(tx pgx.Tx) error {
		return fence.CheckBookUpdateInTx(ctx, tx, quotaProjectA)
	}), quota.KeyBookReloads, 0)

	// A deleted saga keeps the places of its books too.
	seed.project(quotaProjectB, quotaWriterA)
	seed.book(quotaProjectB, false, 100)
	// The deletion of a saga is a fence of its own store; here the row of the fixture takes its marks without it.
	seed.exec(`BEGIN; SET LOCAL session_replication_role = replica;
		UPDATE projects SET deleted_at = now(), governance_retention_started_at = now(), purge_after = now() + interval '7 days',
		purge_deadline = now() + interval '8 days' WHERE id = '` + quotaProjectB + `'; COMMIT;`)
	seed.exec(`UPDATE writer_quota_overrides SET account_books = 3 WHERE user_id = $1`, quotaWriterA)
	refusedBy("a new book after a deleted saga", inTx(func(tx pgx.Tx) error {
		return fence.CheckNewBooksInTx(ctx, tx, quotaProjectA, 1, true)
	}), quota.KeyBookReloads, 3)

	// After the write: the count of the account comes first, before the books of the saga; a return from the trash does
	// not grow it and is not refused by it.
	seed.exec(`UPDATE writer_quota_overrides SET project_books = 1, account_books = 2 WHERE user_id = $1`, quotaWriterA)
	seed.book(quotaProjectA, false, 100)
	refusedBy("the write of a third book", inTx(func(tx pgx.Tx) error {
		return fence.CheckAdmittedSourcesInTx(ctx, tx, quotaProjectA, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters|quota.MeasureLoads)
	}), quota.KeyBookReloads, 2)
	seed.exec(`UPDATE writer_quota_overrides SET project_books = 2 WHERE user_id = $1`, quotaWriterA)
	if err := inTx(func(tx pgx.Tx) error {
		return fence.CheckAdmittedSourcesInTx(ctx, tx, quotaProjectA, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters)
	}); err != nil {
		t.Fatalf("a return from the trash over the count of the account: %v", err)
	}

	// A book that loads again: the live books alone, as before.
	seed.exec(`UPDATE writer_quota_overrides SET book_reloads = NULL, project_books = 5, account_books = 3 WHERE user_id = $1`, quotaWriterA)
	for name, check := range map[string]func(tx pgx.Tx) error{
		"a new book":    func(tx pgx.Tx) error { return fence.CheckNewBooksInTx(ctx, tx, quotaProjectA, 1, true) },
		"an empty book": func(tx pgx.Tx) error { return fence.CheckNewBooksInTx(ctx, tx, quotaProjectA, 1, false) },
		"a new version": func(tx pgx.Tx) error { return fence.CheckBookUpdateInTx(ctx, tx, quotaProjectA) },
		"a written book": func(tx pgx.Tx) error {
			return fence.CheckAdmittedSourcesInTx(ctx, tx, quotaProjectA, quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters|quota.MeasureLoads)
		},
	} {
		if err := inTx(check); err != nil {
			t.Fatalf("%s when a book loads again: %v", name, err)
		}
	}
}
