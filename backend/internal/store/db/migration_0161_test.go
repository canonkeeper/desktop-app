package storedb_test

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-18-free-tier.md FT1.2, FT1.1, FT2.1: migration 0161 counts every book that ever came into the sagas of an
// account that is not deleted — live and in the trash, in live and deleted sagas, not in the example saga — and from then
// on every insert of a book counts itself; «Бесплатно» loads a book once and has one trial proofreading.
func TestMigration0161CountsTheBooksOfLiveAccountsAndEveryNewBook(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_book_loads", 160)
	const live = "00000000-0000-4000-8000-000000161000"
	if _, err := db.Pool.Exec(ctx, `
BEGIN;
SET LOCAL session_replication_role = replica;
INSERT INTO users (id, email, display_name, password_hash, email_verified_at, created_at, updated_at, disabled_at, deleted_at) VALUES
  ('00000000-0000-4000-8000-000000161000', 'loads-live@example.test', 'Автор', 'hash', '2026-11-01Z', '2026-11-01Z', '2026-11-01Z', NULL, NULL),
  ('00000000-0000-4000-8000-000000161900', 'loads-gone@example.test', 'Ушедший', 'hash', '2026-11-01Z', '2026-11-01Z', '2026-11-01Z', '2026-11-10Z', '2026-11-10Z');
INSERT INTO projects (id, owner_user_id, name, demo, created_at, updated_at, deleted_at,
                      governance_retention_started_at, purge_after, purge_deadline) VALUES
  ('00000000-0000-4000-8000-000000161001', '00000000-0000-4000-8000-000000161000', 'Северный предел', false, '2026-10-01Z', '2026-10-01Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000161002', '00000000-0000-4000-8000-000000161000', 'Удалённая', false, '2026-10-01Z', '2026-10-01Z',
   '2026-11-12Z', '2026-11-12Z', '2026-11-19Z', '2026-11-20Z'),
  ('00000000-0000-4000-8000-000000161003', '00000000-0000-4000-8000-000000161000', 'Пример', true, '2026-10-01Z', '2026-10-01Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000161009', '00000000-0000-4000-8000-000000161900', 'В корзине ушедшего', false, '2026-10-01Z', '2026-10-01Z',
   '2026-11-09Z', '2026-11-09Z', '2026-11-16Z', '2026-11-17Z');
INSERT INTO books (id, project_id, title, "order", display_number, key_number, created_at, updated_at, deleted_at,
                   governance_retention_started_at, purge_after, purge_deadline) VALUES
  ('00000000-0000-4000-8000-000000161101', '00000000-0000-4000-8000-000000161001', 'Живая', 1, '1', 1, '2026-10-02Z', '2026-10-02Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000161102', '00000000-0000-4000-8000-000000161001', 'В корзине', 2, '2', 2, '2026-10-03Z', '2026-10-03Z',
   '2026-11-10Z', '2026-11-10Z', '2026-11-17Z', '2026-11-18Z'),
  ('00000000-0000-4000-8000-000000161201', '00000000-0000-4000-8000-000000161002', 'Книга удалённой саги', 1, '1', 1, '2026-10-04Z', '2026-10-04Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000161301', '00000000-0000-4000-8000-000000161003', 'Книга примера', 1, '1', 1, '2026-10-05Z', '2026-10-05Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000161901', '00000000-0000-4000-8000-000000161009', 'Книга ушедшего', 1, '1', 1, '2026-10-06Z', '2026-10-06Z', NULL, NULL, NULL, NULL);
COMMIT;
`); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ApplyNextMigration(t, db, 160); err != nil {
		t.Fatal(err)
	}
	loads := func() map[string]time.Time {
		t.Helper()
		rows, err := db.Pool.Query(ctx, `SELECT book_id::text, owner_user_id::text, loaded_at FROM book_loads`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		found := map[string]time.Time{}
		for rows.Next() {
			var book, owner string
			var at time.Time
			if err := rows.Scan(&book, &owner, &at); err != nil {
				t.Fatal(err)
			}
			if owner != live {
				t.Fatalf("book %s counted for %s, want only the live account", book, owner)
			}
			found[book] = at.UTC()
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return found
	}
	counted := loads()
	want := map[string]time.Time{
		"00000000-0000-4000-8000-000000161101": time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		"00000000-0000-4000-8000-000000161102": time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		"00000000-0000-4000-8000-000000161201": time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
	if len(counted) != len(want) {
		t.Fatalf("books counted = %v, want %v (none of the example saga, none of a deleted account)", counted, want)
	}
	for book, at := range want {
		if !counted[book].Equal(at) {
			t.Fatalf("book %s counted at %v, want %v", book, counted[book], at)
		}
	}

	// From now on a book counts itself when it is inserted, with the moment it was created; a book of the example saga does
	// not, and a restore in the replica mode does not count twice.
	if _, err := db.Pool.Exec(ctx, `
INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000161103', '00000000-0000-4000-8000-000000161001', 'Новая', 3, '3', '2026-11-18T10:00:00Z', '2026-11-18T10:00:00Z'),
  ('00000000-0000-4000-8000-000000161302', '00000000-0000-4000-8000-000000161003', 'Вторая книга примера', 2, '2', '2026-11-18Z', '2026-11-18Z')
`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
BEGIN;
SET LOCAL session_replication_role = replica;
INSERT INTO books (id, project_id, title, "order", display_number, key_number, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000161104', '00000000-0000-4000-8000-000000161001', 'Из копии', 4, '4', 4, '2026-11-18Z', '2026-11-18Z');
COMMIT;
`); err != nil {
		t.Fatal(err)
	}
	counted = loads()
	if at, ok := counted["00000000-0000-4000-8000-000000161103"]; !ok || !at.Equal(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)) || len(counted) != 4 {
		t.Fatalf("books counted after the inserts = %v, want the new book of the saga at its creation and nothing else", counted)
	}

	// The tiers: «Бесплатно» loads a book once and has one trial proofreading, the others reload and have none.
	rows, err := db.Pool.Query(ctx, `SELECT code, book_reloads, trial_proofreads FROM subscription_tiers ORDER BY rank`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tiers := map[string][2]int{}
	for rows.Next() {
		var code string
		var reloads, proofreads int
		if err := rows.Scan(&code, &reloads, &proofreads); err != nil {
			t.Fatal(err)
		}
		tiers[code] = [2]int{reloads, proofreads}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantTiers := map[string][2]int{"free": {0, 1}, "artist": {1, 0}, "writer": {1, 0}, "pro": {1, 0}}
	for code, values := range wantTiers {
		if tiers[code] != values {
			t.Fatalf("tier %s book_reloads, trial_proofreads = %v, want %v", code, tiers[code], values)
		}
	}
	// A charge of a proofreading is a kind of its own; another kind is refused.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO subscription_chapter_charges (id, user_id, kind, draft_id, job_id, charged_at)
		VALUES (gen_random_uuid(), $1, 'proofread', gen_random_uuid(), gen_random_uuid(), now())
	`, live); err != nil {
		t.Fatalf("a charge of a proofreading: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO subscription_chapter_charges (id, user_id, kind, draft_id, job_id, charged_at)
		VALUES (gen_random_uuid(), $1, 'check', gen_random_uuid(), gen_random_uuid(), now())
	`, live); err == nil {
		t.Fatal("a charge of an unknown kind was taken")
	}
}
