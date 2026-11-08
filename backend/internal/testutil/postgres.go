package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dbmigrations "canonkeeper/backend/db/migrations"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func DatabaseURL(t *testing.T) string {
	t.Helper()

	dsn := os.Getenv("CANONKEEPER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}

	return dsn
}

// migrationInventory reads and validates the embedded inventory once per test
// process; the inventory is compiled in and cannot change while tests run.
var migrationInventory = sync.OnceValues(dbmigrations.Read)

func MigrationSQL(t *testing.T, version int64) []byte {
	t.Helper()
	inventory, err := migrationInventory()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range inventory {
		if migration.Version == version {
			return append([]byte(nil), migration.SQL...)
		}
	}
	t.Fatalf("migration version %d is unavailable", version)
	return nil
}

// Every fixture database keeps the product schemas under these names, so a
// copy of a migrated template needs no renaming.
const (
	fixtureSchema       = "ck_fixture"
	fixtureWorkerSchema = fixtureSchema + "_worker"
)

type isolatedFixture struct {
	databaseURL    string
	schema         string
	workerSchema   string
	appliedVersion int64
}

var isolatedFixtures sync.Map

func IsolatedPostgresSchema(t *testing.T, db *storedb.DB) string {
	t.Helper()
	value, ok := isolatedFixtures.Load(db)
	if !ok {
		t.Fatal("database is not an isolated migration fixture")
	}
	return value.(*isolatedFixture).schema
}

// OpenIsolatedPostgresTestDB opens a fresh database holding the shared embedded
// migration inventory through the requested product version, without a product ledger.
func OpenIsolatedPostgresTestDB(t *testing.T, schemaPrefix string, through ...int64) *storedb.DB {
	return openIsolatedPostgresTestDB(t, schemaPrefix, nil, false, through...)
}

func OpenIsolatedPostgresTestDBWithTracer(t *testing.T, schemaPrefix string, tracer pgx.QueryTracer, through ...int64) *storedb.DB {
	return openIsolatedPostgresTestDB(t, schemaPrefix, tracer, false, through...)
}

// OpenIsolatedPostgresMigrationDB applies every embedded migration, including
// the ledger, for migration-owner privilege tests.
func OpenIsolatedPostgresMigrationDB(t *testing.T, schemaPrefix string) *storedb.DB {
	return openIsolatedPostgresTestDB(t, schemaPrefix, nil, true)
}

// Each fixture is its own database copied from a template that holds exactly
// the requested migrations. A copy costs a file copy instead of replaying the
// whole migration inventory, and no fixture sees another one's objects.
func openIsolatedPostgresTestDB(t *testing.T, schemaPrefix string, tracer pgx.QueryTracer, includeLedger bool, through ...int64) *storedb.DB {
	t.Helper()
	ctx := context.Background()
	databaseURL := DatabaseURL(t)
	inventory, err := migrationInventory()
	if err != nil {
		t.Fatal(err)
	}
	terminalVersion := latestProductVersion(inventory)
	if len(through) > 1 {
		t.Fatal("OpenIsolatedPostgresTestDB accepts at most one terminal migration version")
	}
	if len(through) == 1 {
		terminalVersion = through[0]
	}
	if terminalVersion < 0 || terminalVersion > latestProductVersion(inventory) {
		t.Fatalf("isolated fixture terminal migration version %d is unavailable", terminalVersion)
	}
	template, err := migratedTemplate(ctx, databaseURL, inventory, terminalVersion, includeLedger)
	if err != nil {
		t.Fatal(err)
	}

	admin, err := storedb.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	database := fixtureDatabaseName(schemaPrefix)
	quotedDatabase := pgx.Identifier{database}.Sanitize()
	if _, err := admin.Pool.Exec(ctx, "CREATE DATABASE "+quotedDatabase+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	dropDatabase := func() {
		_, _ = admin.Pool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quotedDatabase+" WITH (FORCE)")
		admin.Close()
	}
	quotedSchema := pgx.Identifier{fixtureSchema}.Sanitize()
	config, err := fixtureConfig(databaseURL, database, quotedSchema)
	if err != nil {
		dropDatabase()
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		dropDatabase()
		t.Fatal(err)
	}
	db := &storedb.DB{Pool: pool}
	isolatedFixtures.Store(db, &isolatedFixture{databaseURL: databaseURL, schema: fixtureSchema, workerSchema: fixtureWorkerSchema, appliedVersion: terminalVersion})
	t.Cleanup(func() {
		isolatedFixtures.Delete(db)
		db.Close()
		dropDatabase()
	})
	return db
}

// ApplyNextMigration applies only the next embedded product migration to an
// isolated fixture. It rejects a replay or a skipped version.
func ApplyNextMigration(t *testing.T, db *storedb.DB, through int64) error {
	t.Helper()
	value, ok := isolatedFixtures.Load(db)
	if !ok {
		return fmt.Errorf("database is not an isolated migration fixture")
	}
	fixture := value.(*isolatedFixture)
	if fixture.appliedVersion != through {
		return fmt.Errorf("fixture is at migration %d, cannot apply next from %d", fixture.appliedVersion, through)
	}
	inventory, err := migrationInventory()
	if err != nil {
		return err
	}
	var next *dbmigrations.Migration
	for index := range inventory {
		if inventory[index].Version == through+1 {
			next = &inventory[index]
			break
		}
	}
	if next == nil || next.Version == 0 {
		return fmt.Errorf("next migration after %d is unavailable", through)
	}
	err = withMigrationLock(context.Background(), fixture.databaseURL, func(*pgx.Conn) error {
		_, err := db.Pool.Exec(context.Background(), expandMigration(string(next.SQL), fixture.schema, fixture.workerSchema))
		return err
	})
	if err != nil {
		t.Fatalf("apply migration %s: %v", next.Name, err)
	}
	fixture.appliedVersion = next.Version
	return nil
}

// withMigrationLock serializes every migration a test applies across test
// processes: the baseline creates cluster-wide roles, and two sessions creating
// the same role at once fail on pg_authid.
func withMigrationLock(ctx context.Context, databaseURL string, run func(admin *pgx.Conn) error) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('canonkeeper test migrations'))"); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext('canonkeeper test migrations'))")
	}()
	return run(conn)
}

func latestProductVersion(inventory []dbmigrations.Migration) int64 {
	return inventory[len(inventory)-1].Version
}

type migrationStep struct {
	name string
	sql  string
}

// fixtureRecipe lists the exact statements that build a fixture: the product
// schema, then every selected migration rewritten into the fixture schemas.
func fixtureRecipe(inventory []dbmigrations.Migration, terminalVersion int64, includeLedger bool) []migrationStep {
	steps := []migrationStep{{name: "fixture schema", sql: "CREATE SCHEMA " + pgx.Identifier{fixtureSchema}.Sanitize()}}
	for _, migration := range inventory {
		if migration.Version == 0 && !includeLedger {
			continue
		}
		if migration.Version > terminalVersion {
			break
		}
		steps = append(steps, migrationStep{
			name: migration.Name,
			sql:  expandMigration(string(migration.SQL), fixtureSchema, fixtureWorkerSchema),
		})
	}
	return steps
}

type templateKey struct {
	terminalVersion int64
	includeLedger   bool
}

var (
	templatesMu sync.Mutex
	templates   = map[templateKey]string{}
)

// migratedTemplate returns a template database built by the recipe. The name is
// the digest of the recipe, so a changed migration gets a new template and a
// template is never reused for different SQL. Under the migration lock
// concurrent test processes build each template once.
func migratedTemplate(ctx context.Context, databaseURL string, inventory []dbmigrations.Migration, terminalVersion int64, includeLedger bool) (string, error) {
	key := templateKey{terminalVersion: terminalVersion, includeLedger: includeLedger}
	templatesMu.Lock()
	defer templatesMu.Unlock()
	if name, ok := templates[key]; ok {
		return name, nil
	}
	recipe := fixtureRecipe(inventory, terminalVersion, includeLedger)
	digest := sha256.New()
	for _, step := range recipe {
		fmt.Fprintf(digest, "%d:%s\n%d:%s\n", len(step.name), step.name, len(step.sql), step.sql)
	}
	name := "ck_template_" + hex.EncodeToString(digest.Sum(nil))[:24]
	err := withMigrationLock(ctx, databaseURL, func(admin *pgx.Conn) error {
		var ready bool
		err := admin.QueryRow(ctx, "SELECT datistemplate FROM pg_catalog.pg_database WHERE datname = $1", name).Scan(&ready)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if ready {
			return nil
		}
		return buildTemplate(ctx, admin, databaseURL, name, recipe)
	})
	if err != nil {
		return "", err
	}
	templates[key] = name
	return name, nil
}

func buildTemplate(ctx context.Context, admin *pgx.Conn, databaseURL, name string, recipe []migrationStep) error {
	quoted := pgx.Identifier{name}.Sanitize()
	// A build that died halfway left a database that never became a template.
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted+" TEMPLATE template0"); err != nil {
		return err
	}
	config, err := fixtureConfig(databaseURL, name, pgx.Identifier{fixtureSchema}.Sanitize())
	if err != nil {
		return err
	}
	conn, err := pgx.ConnectConfig(ctx, config.ConnConfig)
	if err != nil {
		return err
	}
	for _, step := range recipe {
		if _, err := conn.Exec(ctx, step.sql); err != nil {
			_ = conn.Close(context.Background())
			return fmt.Errorf("apply migration %s: %w", step.name, err)
		}
	}
	if err := conn.Close(ctx); err != nil {
		return err
	}
	_, err = admin.Exec(ctx, "ALTER DATABASE "+quoted+" WITH IS_TEMPLATE true ALLOW_CONNECTIONS false")
	return err
}

func fixtureConfig(databaseURL, database, quotedSchema string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.ConnConfig.Database = database
	config.ConnConfig.RuntimeParams["search_path"] = quotedSchema + ",pg_catalog"
	return config, nil
}

var fixtureSequence struct {
	sync.Mutex
	last int64
}

func fixtureDatabaseName(prefix string) string {
	fixtureSequence.Lock()
	defer fixtureSequence.Unlock()
	now := time.Now().UnixNano()
	if now <= fixtureSequence.last {
		now = fixtureSequence.last + 1
	}
	fixtureSequence.last = now
	return prefix + "_" + strconv.FormatInt(now, 36) + "_" + strconv.FormatInt(int64(os.Getpid()), 36)
}

func expandMigration(raw, schema, workerSchema string) string {
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	quotedWorkerSchema := pgx.Identifier{workerSchema}.Sanitize()
	migration := strings.ReplaceAll(raw, "public.", quotedSchema+".")
	migration = strings.ReplaceAll(migration, "SET search_path TO 'pg_catalog', 'public'", "SET search_path TO 'pg_catalog', "+quotedSchema)
	migration = strings.ReplaceAll(migration, "SET search_path = public, pg_catalog", "SET search_path = "+quotedSchema+", pg_catalog")
	migration = strings.ReplaceAll(migration, "SET search_path = pg_catalog, public", "SET search_path = pg_catalog, "+quotedSchema)
	migration = strings.ReplaceAll(migration, "SCHEMA public", "SCHEMA "+quotedSchema)
	migration = strings.ReplaceAll(migration, "IN SCHEMA public", "IN SCHEMA "+quotedSchema)
	migration = strings.ReplaceAll(migration, "canonkeeper_worker.", quotedWorkerSchema+".")
	migration = strings.ReplaceAll(migration, "CREATE SCHEMA canonkeeper_worker;", "CREATE SCHEMA "+quotedWorkerSchema+";")
	migration = strings.ReplaceAll(migration, "ON SCHEMA canonkeeper_worker ", "ON SCHEMA "+quotedWorkerSchema+" ")
	migration = strings.ReplaceAll(migration, "IN SCHEMA canonkeeper_worker", "IN SCHEMA "+quotedWorkerSchema)
	migration = strings.ReplaceAll(migration, "ALTER SCHEMA canonkeeper_worker ", "ALTER SCHEMA "+quotedWorkerSchema+" ")
	return migration
}
