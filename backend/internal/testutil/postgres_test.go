package testutil_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

func TestDatabaseFixturesSupportVersionBoundedThroughAndApplyNext(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_bounded", 0)
	schema := testutil.IsolatedPostgresSchema(t, db)
	var users *string
	if err := db.Pool.QueryRow(context.Background(),
		"SELECT to_regclass($1)::text", schema+".users",
	).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != nil {
		t.Fatalf("version-0 fixture unexpectedly applied the baseline in %s", schema)
	}

	if err := testutil.ApplyNextMigration(t, db, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(context.Background(),
		"SELECT to_regclass($1)::text", schema+".users",
	).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users == nil {
		t.Fatalf("exact next migration did not apply the baseline in %s", schema)
	}
	if err := testutil.ApplyNextMigration(t, db, 0); err == nil {
		t.Fatal("replayed migration was accepted")
	}
	if err := testutil.ApplyNextMigration(t, db, 2); err == nil {
		t.Fatal("skipped migration was accepted")
	}
}
