package storedb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// Image workshop IW5.4: migration 0089 makes a drawing of every branch with a row that is not an upload — a
// branch with edits, a regeneration and an enlargement is one drawing, a branch of an upload alone is none,
// a drawing made from the author's upload is a drawing of its own branch; the current version is the latest
// ready row without a group; a branch whose group waits without a ready row has no current version and is not
// finished, nor is a drawing whose group still waits for the choice. The description is one helper piece of
// the latest row drawn by a description, the idea is empty.
func TestMigration0089MakesADrawingOfEveryBranch(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_image_workshop_migration", 88)
	const (
		owner   = "00000000-0000-4000-8000-000000008901"
		project = "00000000-0000-4000-8000-000000008902"
		group   = "00000000-0000-4000-8000-000000008903"
		waiting = "00000000-0000-4000-8000-000000008904"
	)
	for _, statement := range []string{
		`INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ('` + owner + `', 'workshop@example.test', 'W', 'hash', '2026-11-02Z')`,
		`INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at) VALUES ('` + project + `', '` + owner + `', 'W', '', '2026-11-02Z', '2026-11-02Z')`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	id := func(number int) string { return fmt.Sprintf("00000000-0000-4000-8000-0000000089%02d", 10+number) }
	type row struct {
		number, branch, base int
		status, kind, prompt string
		group                string
		enlarged             bool
	}
	insert := func(image row) {
		t.Helper()
		var base, variant any
		if image.base > 0 {
			base = id(image.base)
		}
		if image.group != "" {
			variant = image.group
		}
		var prompt, model, job, title any = image.prompt, "m", id(image.number), "Кай у костра"
		if image.kind == "upload" {
			prompt, model, job, title = nil, nil, nil, "Эскиз автора"
		}
		ready := image.status == "ready"
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO saga_images (id, project_id, number, branch, status, kind, aspect, prompt, base_image_id, job_id, created_by, model,
			  created_at, title, chapter_key, chapter_order, subjects, subject_names, variant_group, enlarged,
			  width, height, bytes, ready_at, error_code)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, 'square', $7, $8::uuid, $9::uuid, $10::uuid, $11,
			  '2026-11-02Z', $12, '481', 3, '{кай}', '{Кай}', $13::uuid, $14,
			  CASE WHEN $15 THEN 1 END, CASE WHEN $15 THEN 1 END, CASE WHEN $15 THEN 1 END, CASE WHEN $15 THEN '2026-11-02Z'::timestamptz END,
			  CASE WHEN $5 = 'failed' THEN 'draw_failed' END)
		`, id(image.number), project, image.number, image.branch, image.status, image.kind, prompt, base, job, owner, model,
			title, variant, image.enlarged, ready); err != nil {
			t.Fatalf("row %d: %v", image.number, err)
		}
	}
	for _, image := range []row{
		{number: 1, branch: 1, status: "ready", kind: "scene", prompt: "Кай у костра ночью"},
		{number: 2, branch: 1, base: 1, status: "ready", kind: "scene", prompt: "шрам на щеке"},
		{number: 3, branch: 1, status: "ready", kind: "scene", prompt: "Кай у костра ночью, крупно"},
		{number: 4, branch: 1, base: 3, status: "ready", kind: "scene", prompt: "Увеличь", enlarged: true},
		{number: 5, branch: 5, status: "ready", kind: "upload"},
		{number: 6, branch: 6, base: 5, status: "ready", kind: "scene", prompt: "Город по эскизу автора"},
		{number: 7, branch: 7, status: "drawing", kind: "character", prompt: "Дракон", group: group},
		{number: 8, branch: 7, status: "failed", kind: "character", prompt: "Дракон", group: group},
		{number: 9, branch: 9, status: "ready", kind: "place", prompt: "Замок"},
		{number: 10, branch: 9, base: 9, status: "ready", kind: "place", prompt: "Замок ночью", group: waiting},
	} {
		insert(image)
	}
	if err := testutil.ApplyNextMigration(t, db, 88); err != nil {
		t.Fatal(err)
	}
	type drawing struct {
		current  int
		finished bool
		idea     string
		brief    string
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT drawing.branch, COALESCE(current.number, 0), drawing.finished_at IS NOT NULL, drawing.idea, drawing.brief::text
		FROM saga_drawings AS drawing
		LEFT JOIN saga_images AS current ON current.project_id = drawing.project_id AND current.id = drawing.current_image_id
		WHERE drawing.project_id = $1::uuid ORDER BY drawing.branch
	`, project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]drawing{}
	for rows.Next() {
		var branch int
		var found drawing
		if err := rows.Scan(&branch, &found.current, &found.finished, &found.idea, &found.brief); err != nil {
			t.Fatal(err)
		}
		got[branch] = found
	}
	if rows.Err() != nil || len(got) != 4 {
		t.Fatalf("drawings = %+v, %v; want those of branches 1, 6, 7, 9", got, rows.Err())
	}
	for branch, want := range map[int]drawing{1: {current: 4, finished: true}, 6: {current: 6, finished: true}, 7: {}, 9: {current: 9}} {
		if found := got[branch]; found.current != want.current || found.finished != want.finished || found.idea != "" {
			t.Fatalf("drawing of branch %d = %+v; want current %d, finished %v and no idea", branch, found, want.current, want.finished)
		}
	}
	var brief struct {
		Title    string   `json:"title"`
		Kind     string   `json:"kind"`
		Aspect   string   `json:"aspect"`
		Chapter  string   `json:"chapter"`
		Subjects []string `json:"subjects"`
		Segments []struct {
			Text   string `json:"text"`
			Origin string `json:"origin"`
		} `json:"segments"`
		Facts      []string `json:"facts"`
		References *[]int   `json:"references"`
	}
	if err := json.Unmarshal([]byte(got[1].brief), &brief); err != nil {
		t.Fatal(err)
	}
	if brief.Title != "Кай у костра" || brief.Kind != "scene" || brief.Aspect != "square" || brief.Chapter != "481" ||
		len(brief.Subjects) != 1 || brief.Subjects[0] != "Кай" || len(brief.Segments) != 1 || brief.Segments[0].Origin != "helper" ||
		brief.Segments[0].Text != "Кай у костра ночью, крупно" || len(brief.Facts) != 0 || brief.References != nil {
		t.Fatalf("brief of branch 1 = %s", got[1].brief)
	}
	for _, statement := range []string{
		`INSERT INTO saga_drawings (project_id, branch, brief, created_by, updated_by) VALUES ('` + project + `', 1, '{}', '` + owner + `', '` + owner + `')`,
		`UPDATE saga_drawings SET brief_revision = 0`,
		`UPDATE saga_drawings SET idea = repeat('а', 4001)`,
		`UPDATE saga_drawings SET brief = '[]'`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err == nil {
			t.Fatalf("the table took %q", statement)
		}
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM saga_images WHERE id = $1::uuid`, id(4)); err != nil {
		t.Fatal(err)
	}
	var current *string
	if err := db.Pool.QueryRow(ctx, `SELECT current_image_id::text FROM saga_drawings WHERE branch = 1`).Scan(&current); err != nil || current != nil {
		t.Fatalf("deleting the current version leaves %v, %v", current, err)
	}
}
