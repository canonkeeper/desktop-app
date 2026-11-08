package storedb_test

import (
	"context"
	"fmt"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// Illustrations R14.1: migration 0069 gives every drawing the number of the first drawing of its
// branch — versions edited from a drawing follow their base up to the first one, a variant of a first
// drawing (the same description, kind and shape, no base) joins its branch, a drawing whose base was
// deleted starts its own; afterwards the branch is required and is never after the drawing.
func TestMigration0069GroupsDrawingsIntoBranches(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_illustration_branches", 68)
	const (
		owner   = "00000000-0000-4000-8000-000000006901"
		project = "00000000-0000-4000-8000-000000006902"
	)
	for _, statement := range []string{
		`INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ('` + owner + `', 'branches@example.test', 'B', 'hash', '2026-10-30Z')`,
		`INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at) VALUES ('` + project + `', '` + owner + `', 'B', '', '2026-10-30Z', '2026-10-30Z')`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	id := func(number int) string { return fmt.Sprintf("00000000-0000-4000-8000-0000000069%02d", 10+number) }
	insert := func(number, base int, prompt, kind string) {
		t.Helper()
		var baseID any
		if base > 0 {
			baseID = id(base)
		}
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO saga_images (id, project_id, number, status, kind, aspect, prompt, base_image_id, job_id, created_by, model, created_at)
			VALUES ($1::uuid, $2::uuid, $3, 'drawing', $4, 'square', $5, $6::uuid, gen_random_uuid(), $7::uuid, 'm', '2026-10-30Z')
		`, id(number), project, number, kind, prompt, baseID, owner); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, 0, "Кай у костра ночью", "scene")
	insert(2, 1, "волосы до плеч", "scene")
	insert(3, 2, "шрам на щеке", "scene")
	insert(4, 0, "Город на рассвете", "place")
	insert(5, 0, "Кай у костра ночью", "scene") // «Перегенерировать» of 1: its base was 1's (none)
	insert(6, 5, "в плаще", "scene")
	insert(7, 0, "Кай у костра ночью", "character") // another kind: its own branch
	insert(8, 0, "волосы до плеч", "scene")         // an edit whose base was deleted: its own branch
	if err := testutil.ApplyNextMigration(t, db, 68); err != nil {
		t.Fatal(err)
	}
	want := map[int]int{1: 1, 2: 1, 3: 1, 4: 4, 5: 1, 6: 1, 7: 7, 8: 8}
	rows, err := db.Pool.Query(ctx, `SELECT number, branch FROM saga_images WHERE project_id = $1::uuid ORDER BY number`, project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]int{}
	for rows.Next() {
		var number, branch int
		if err := rows.Scan(&number, &branch); err != nil {
			t.Fatal(err)
		}
		got[number] = branch
	}
	if rows.Err() != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("branches = %v, %v; want %v", got, rows.Err(), want)
	}
	for _, statement := range []string{
		`UPDATE saga_images SET branch = NULL WHERE number = 1`,
		`UPDATE saga_images SET branch = 9 WHERE number = 8`,
		`UPDATE saga_images SET branch = 0 WHERE number = 1`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err == nil {
			t.Fatalf("the table took %q", statement)
		}
	}
}
