package storedb

import (
	"context"
	"os"
	"testing"

	dbmigrations "canonkeeper/backend/db/migrations"

	"github.com/jackc/pgx/v5"
)

// A release applies its migrations in one transaction. A migration that switches the
// transaction to immediate constraint checks must not change how the next one runs: on the
// production copy 0038 left checks immediate and 0041 failed deleting a Job before its owner.
func TestMigrationBatchStartsEveryMigrationWithDeferredChecks(t *testing.T) {
	dsn := os.Getenv("CANONKEEPER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch := []dbmigrations.Migration{
		{Name: "first", SQL: []byte(`
CREATE TEMP TABLE batch_owner (id integer PRIMARY KEY) ON COMMIT DROP;
CREATE TEMP TABLE batch_participant (
  owner_id integer REFERENCES batch_owner DEFERRABLE INITIALLY DEFERRED
) ON COMMIT DROP;
SET CONSTRAINTS ALL IMMEDIATE;`)},
		{Name: "second", SQL: []byte(`
INSERT INTO batch_participant VALUES (1);
INSERT INTO batch_owner VALUES (1);`)},
	}
	for _, migration := range batch {
		if err := applyMigration(ctx, tx, migration); err != nil {
			t.Fatalf("%v: the migration inherited immediate checks from the one before", err)
		}
	}
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		t.Fatalf("deferred checks at the end of the batch: %v", err)
	}
}
