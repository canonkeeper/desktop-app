package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-11-ux7-reorder.md RK1.2: migration 0135 gives every book its number in the keys of its chapters so
// that each key the server serves today stays the same — the number of a living book is the place it holds
// (`books."order"`, the column the keys were built of, not its rank among the living), a deleted book keeps its place
// while no book holds it, and the rest get numbers no book had. The example saga of the site keeps its one book's keys.
func TestMigration0135KeepsEveryChapterKeyTheServerServes(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_book_key_numbers", 134)
	// Three sagas: the second book of «Средняя» is deleted; in «Дырка» a new book took the place of a deleted one, and two
	// deleted books share a place; «Пример» is the example saga of the site.
	if _, err := db.Pool.Exec(ctx, `
BEGIN;
SET LOCAL session_replication_role = replica;
INSERT INTO projects (id, owner_user_id, name, demo, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000131001', '00000000-0000-4000-8000-000000131000', 'Средняя', false, '2026-10-01Z', '2026-10-01Z'),
  ('00000000-0000-4000-8000-000000131002', '00000000-0000-4000-8000-000000131000', 'Дырка', false, '2026-10-01Z', '2026-10-01Z'),
  ('00000000-0000-4000-8000-000000131003', '00000000-0000-4000-8000-000000131000', 'Пример', true, '2026-10-01Z', '2026-10-01Z');
INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at, deleted_at,
                   governance_retention_started_at, purge_after, purge_deadline) VALUES
  ('00000000-0000-4000-8000-000000131101', '00000000-0000-4000-8000-000000131001', 'Первая', 1, '1', '2026-10-01Z', '2026-10-01Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000131102', '00000000-0000-4000-8000-000000131001', 'Вторая', 2, '2', '2026-10-02Z', '2026-10-02Z',
   '2026-11-10Z', '2026-11-10Z', '2026-11-17Z', '2026-11-18Z'),
  ('00000000-0000-4000-8000-000000131103', '00000000-0000-4000-8000-000000131001', 'Третья', 3, '3', '2026-10-03Z', '2026-10-03Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000131201', '00000000-0000-4000-8000-000000131002', 'Начало', 1, '1', '2026-10-01Z', '2026-10-01Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000131202', '00000000-0000-4000-8000-000000131002', 'Ушедшая', 2, '2', '2026-10-02Z', '2026-10-02Z',
   '2026-11-01Z', '2026-11-01Z', '2026-11-08Z', '2026-11-09Z'),
  ('00000000-0000-4000-8000-000000131203', '00000000-0000-4000-8000-000000131002', 'На её месте', 2, '2', '2026-11-02Z', '2026-11-02Z', NULL, NULL, NULL, NULL),
  ('00000000-0000-4000-8000-000000131204', '00000000-0000-4000-8000-000000131002', 'Третья раньше', 3, '3', '2026-10-03Z', '2026-10-03Z',
   '2026-11-03Z', '2026-11-03Z', '2026-11-10Z', '2026-11-11Z'),
  ('00000000-0000-4000-8000-000000131205', '00000000-0000-4000-8000-000000131002', 'Третья позже', 3, '3', '2026-11-04Z', '2026-11-04Z',
   '2026-11-05Z', '2026-11-05Z', '2026-11-12Z', '2026-11-13Z'),
  ('00000000-0000-4000-8000-000000131301', '00000000-0000-4000-8000-000000131003', 'Игрок', 1, '1', '2026-10-01Z', '2026-10-01Z', NULL, NULL, NULL, NULL);
INSERT INTO chapters (id, project_id, book_id, title, "order", created_at, updated_at)
SELECT ('00000000-0000-4000-8000-' || lpad((131000000 + row_number() OVER ())::text, 12, '0'))::uuid,
       book.project_id, book.id, 'Глава. ' || book.title || ' ' || chapter, chapter, '2026-10-01Z', '2026-10-01Z'
FROM books AS book CROSS JOIN generate_series(1, 2) AS chapter;
COMMIT;
`); err != nil {
		t.Fatal(err)
	}
	// The keys the server serves today: the books the saga reads are the living ones, their number is their "order".
	served := func(number string) map[string]string {
		t.Helper()
		rows, err := db.Pool.Query(ctx, `
			SELECT chapter.id::text, 'b' || book.`+number+` || 'c' || chapter."order"
			FROM chapters AS chapter JOIN books AS book ON book.id = chapter.book_id
			WHERE book.deleted_at IS NULL
		`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		keys := map[string]string{}
		for rows.Next() {
			var id, key string
			if err := rows.Scan(&id, &key); err != nil {
				t.Fatal(err)
			}
			keys[id] = key
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	before := served(`"order"`)
	if len(before) != 10 {
		t.Fatalf("chapters of the living books before = %d, want 10", len(before))
	}
	if err := testutil.ApplyNextMigration(t, db, 134); err != nil {
		t.Fatal(err)
	}
	after := served("key_number")
	for id, key := range before {
		if after[id] != key {
			t.Fatalf("chapter %s served as %s before the migration and as %s after it", id, key, after[id])
		}
	}
	numbers := map[string]int{}
	rows, err := db.Pool.Query(ctx, `SELECT id::text, key_number FROM books`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var number int
		if err := rows.Scan(&id, &number); err != nil {
			t.Fatal(err)
		}
		numbers[id] = number
	}
	rows.Close()
	// A deleted book keeps its place while no book holds it; of two deleted books on one place the one deleted last keeps
	// it — its links are the latest; the others get numbers after every place of the saga, by their creation.
	for id, want := range map[string]int{
		"00000000-0000-4000-8000-000000131102": 2,
		"00000000-0000-4000-8000-000000131205": 3,
		"00000000-0000-4000-8000-000000131202": 4,
		"00000000-0000-4000-8000-000000131204": 5,
		"00000000-0000-4000-8000-000000131301": 1,
	} {
		if numbers[id] != want {
			t.Fatalf("book %s got number %d, want %d", id, numbers[id], want)
		}
	}
	var issued map[string]int = map[string]int{}
	projects, err := db.Pool.Query(ctx, `SELECT id::text, book_key_numbers FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	for projects.Next() {
		var id string
		var count int
		if err := projects.Scan(&id, &count); err != nil {
			t.Fatal(err)
		}
		issued[id] = count
	}
	projects.Close()
	if issued["00000000-0000-4000-8000-000000131001"] != 3 || issued["00000000-0000-4000-8000-000000131002"] != 5 ||
		issued["00000000-0000-4000-8000-000000131003"] != 1 {
		t.Fatalf("numbers issued by the sagas = %v, want 3, 5 and 1", issued)
	}

	// RK1.1: a new book gets the number after the greatest the saga ever gave, also once the book that had it is gone for
	// good, and also in the replica mode of a seed, where the other triggers sleep.
	if _, err := db.Pool.Exec(ctx, `
BEGIN;
SET LOCAL session_replication_role = replica;
DELETE FROM books WHERE id = '00000000-0000-4000-8000-000000131204';
INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
VALUES ('00000000-0000-4000-8000-000000131206', '00000000-0000-4000-8000-000000131002', 'Новая', 3, '3', '2026-11-11Z', '2026-11-11Z');
COMMIT;
`); err != nil {
		t.Fatal(err)
	}
	var fresh, total int
	if err := db.Pool.QueryRow(ctx, `
		SELECT book.key_number, project.book_key_numbers
		FROM books AS book JOIN projects AS project ON project.id = book.project_id
		WHERE book.id = '00000000-0000-4000-8000-000000131206'
	`).Scan(&fresh, &total); err != nil || fresh != 6 || total != 6 {
		t.Fatalf("a new book after a purged one got %d, the saga issued %d (%v); want 6 and 6", fresh, total, err)
	}
	// A copy restored from a backup brings the numbers of its books; they are kept, and the saga issues after them.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO books (id, project_id, title, "order", display_number, key_number, created_at, updated_at)
		VALUES ('00000000-0000-4000-8000-000000131207', '00000000-0000-4000-8000-000000131002', 'Из копии', 4, '4', 9, '2026-11-11Z', '2026-11-11Z')
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT book.key_number, project.book_key_numbers
		FROM books AS book JOIN projects AS project ON project.id = book.project_id
		WHERE book.id = '00000000-0000-4000-8000-000000131207'
	`).Scan(&fresh, &total); err != nil || fresh != 9 || total != 9 {
		t.Fatalf("a book with its own number got %d, the saga issued %d (%v); want 9 and 9", fresh, total, err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO books (id, project_id, title, "order", display_number, key_number, created_at, updated_at)
		VALUES ('00000000-0000-4000-8000-000000131208', '00000000-0000-4000-8000-000000131002', 'Второй раз', 5, '5', 9, '2026-11-11Z', '2026-11-11Z')
	`); err == nil {
		t.Fatal("a number of the saga was given twice")
	}
}
