package indexingstore

import (
	"context"
	"path/filepath"
	"testing"

	"canonkeeper/backend/internal/governance"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-15-desk-obsidian-export.md OE3.1: migration 0157 goes over a base of the version before it with the link of a
// saga with its vault and Jobs in different statuses — the Jobs stay as they are —; the columns auto_export and last_import_at
// go, export_dir and last_export_at stay, and the kind obsidian_export is in no check of the Jobs and in no kind a worker
// claims.
func TestMigration0157TakesTheExportOutOfTheJobs(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store_0157", nil, 156)
	fixture := newStoreFixtureOn(t, db, 8400, governance.SourceViewPublished)
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewDraft); err != nil || !queued {
		t.Fatalf("the cards Job before 0157 = %v, %v", queued, err)
	}
	vault := filepath.Join(t.TempDir(), "Северный предел")
	if err := obsidianrepo.New(db).Link(ctx, fixture.projectID, vault); err != nil {
		t.Fatal(err)
	}
	jobsOf := func() map[string]int {
		t.Helper()
		rows, err := db.Pool.Query(ctx, `SELECT kind || ':' || status, count(*) FROM jobs WHERE project_id = $1 GROUP BY 1`, fixture.projectID)
		if err != nil {
			t.Fatal(err)
		}
		counts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct {
			key   string
			count int
		}, error) {
			var item struct {
				key   string
				count int
			}
			err := row.Scan(&item.key, &item.count)
			return item, err
		})
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]int{}
		for _, item := range counts {
			result[item.key] = item.count
		}
		return result
	}
	before := jobsOf()
	if len(before) < 2 {
		t.Fatalf("the Jobs before 0157 are of one status: %v", before)
	}
	if err := testutil.ApplyNextMigration(t, db, 156); err != nil {
		t.Fatal(err)
	}
	after := jobsOf()
	for key, count := range before {
		if after[key] != count {
			t.Fatalf("the Jobs after 0157 = %v, before %v", after, before)
		}
	}
	schema := testutil.IsolatedPostgresSchema(t, db)
	var columns []string
	rows, err := db.Pool.Query(ctx, `
		SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'obsidian_vaults' ORDER BY column_name
	`, schema)
	if err != nil {
		t.Fatal(err)
	}
	if columns, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		t.Fatal(err)
	}
	want := []string{"export_dir", "last_export_at", "project_id", "vault_path"}
	if len(columns) != len(want) {
		t.Fatalf("the columns of obsidian_vaults after 0157 = %q", columns)
	}
	for index := range want {
		if columns[index] != want[index] {
			t.Fatalf("the columns of obsidian_vaults after 0157 = %q", columns)
		}
	}
	var path string
	if err := db.Pool.QueryRow(ctx, `SELECT vault_path FROM obsidian_vaults WHERE project_id = $1`, fixture.projectID).Scan(&path); err != nil || path != vault {
		t.Fatalf("the link after 0157 = %q, %v", path, err)
	}
	var named int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pg_constraint WHERE conrelid = $1::regclass AND pg_get_constraintdef(oid) LIKE '%obsidian_export%')
		     + (SELECT count(*) FROM pg_proc WHERE proname = 'claim_next_queued_job_for_worker' AND pronamespace = $2::regnamespace
		          AND prosrc LIKE '%obsidian_export%')
	`, schema+".jobs", schema+"_worker").Scan(&named); err != nil {
		t.Fatal(err)
	}
	if named != 0 {
		t.Fatalf("the kind obsidian_export is named %d times after 0157", named)
	}
}
