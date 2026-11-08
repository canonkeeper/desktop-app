package storedb

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbmigrations "canonkeeper/backend/db/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestApplyMigrationsCreatesFreshSchemaAndLedger(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{empty: true},
	}

	if err := applyMigrationsWithTx(context.Background(), tx, inventory); err != nil {
		t.Fatal(err)
	}

	if len(tx.execs) != len(inventory)*3 {
		t.Fatalf("exec count = %d, want %d: %+v", len(tx.execs), len(inventory)*3, tx.execs)
	}
	if tx.quiescenceQueries != 0 {
		t.Fatalf("fresh schema quiescence queries = %d, want 0", tx.quiescenceQueries)
	}
	for index, migration := range inventory {
		assertDeferredChecks(t, tx.execs[index*3], migration)
		if tx.execs[index*3+1].sql != string(migration.SQL) {
			t.Fatalf("migration %s SQL was not applied in order", migration.Name)
		}
		assertLedgerInsert(t, tx.execs[index*3+2], migration)
	}
}

func TestApplyMigrationsRejectsManagedDatabaseWithActiveJobs(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{managed: []ledgerEntry{{
			version: inventory[0].Version, checksum: inventory[0].Checksum,
		}}},
		activeJobs: 1,
	}

	err = applyMigrationsWithTx(context.Background(), tx, inventory)
	if !errors.Is(err, ErrMigrationNotQuiescent) {
		t.Fatalf("error = %v, want ErrMigrationNotQuiescent", err)
	}
	if len(tx.execs) != 0 {
		t.Fatalf("execs after non-quiescent migration = %+v", tx.execs)
	}
	if tx.quiescenceQueries != 1 {
		t.Fatalf("quiescence queries = %d, want 1", tx.quiescenceQueries)
	}
}

func TestApplyMigrationsAdoptsExactLegacyBaselineWithoutRerun(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{
			legacy: true,
		},
	}

	if err := applyMigrationsWithTx(context.Background(), tx, inventory); err != nil {
		t.Fatal(err)
	}

	for _, exec := range tx.execs {
		if exec.sql == string(inventory[1].SQL) {
			t.Fatal("legacy adoption reran 0001 baseline")
		}
	}
	if len(tx.execs) < 4 {
		t.Fatalf("exec count = %d, want ledger DDL and two inserts: %+v", len(tx.execs), tx.execs)
	}
	assertLedgerInsert(t, tx.execs[2], inventory[0])
	assertLedgerInsert(t, tx.execs[3], inventory[1])
	if len(inventory) < 3 {
		t.Fatal("legacy adoption requires a forward ACL migration")
	}
	if len(tx.execs) < 8 || tx.execs[6].sql != string(inventory[2].SQL) {
		t.Fatalf("legacy adoption did not apply migration 0002 in the same transaction: %+v", tx.execs)
	}
	assertLedgerInsert(t, tx.execs[7], inventory[2])
}

func TestApplyMigrationsUsesExactLegacyBaselineChecksum(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		marker    string
		wantError bool
	}{
		{name: "exact marker", marker: inventory[1].Checksum},
		{name: "changed marker", marker: inventory[1].Checksum + "0", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &recordingMigrationTx{
				classification: migrationClassification{
					legacy:         true,
					legacyChecksum: tt.marker,
				},
			}
			err := applyMigrationsWithTx(context.Background(), tx, inventory)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected changed legacy marker to be rejected")
				}
				if len(tx.execs) != 0 {
					t.Fatalf("execs after changed legacy marker = %+v", tx.execs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(tx.classificationArgs) != 1 || tx.classificationArgs[0] != inventory[1].Checksum {
				t.Fatalf("legacy classification args = %#v, want current baseline checksum", tx.classificationArgs)
			}
		})
	}
}

func TestApplyMigrationsRejectsHistoricalLegacyBaselineChecksum(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{
			legacy:         true,
			legacyChecksum: "15dc6cee276dfd70602ad2f91ad9b93ec17bfc053380943af63a394b9ace2ca4",
		},
	}
	if err := applyMigrationsWithTx(context.Background(), tx, inventory); err == nil {
		t.Fatal("expected historical legacy baseline to be rejected")
	}
	if len(tx.execs) != 0 {
		t.Fatalf("execs after historical baseline rejection = %+v", tx.execs)
	}
}

func TestApplyMigrationsAppliesOnlyMissingManagedSuffix(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{classification: migrationClassification{managed: []ledgerEntry{
		{version: inventory[0].Version, checksum: inventory[0].Checksum},
		{version: inventory[1].Version, checksum: inventory[1].Checksum},
	}}}

	if err := applyMigrationsWithTx(context.Background(), tx, inventory); err != nil {
		t.Fatal(err)
	}
	remaining := inventory[2:]
	if len(tx.execs) != len(remaining)*3 {
		t.Fatalf("managed suffix execs = %d, want %d migration/ledger triples", len(tx.execs), len(remaining))
	}
	for index, migration := range remaining {
		assertDeferredChecks(t, tx.execs[index*3], migration)
		if tx.execs[index*3+1].sql != string(migration.SQL) {
			t.Fatalf("managed migration %s SQL was not applied", migration.Name)
		}
		assertLedgerInsert(t, tx.execs[index*3+2], migration)
	}
}

func TestExactIndexingJobResolverBackfillsQueueOriginLineageBeforeFallbackRemoval(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	var migration *dbmigrations.Migration
	for index := range inventory {
		if inventory[index].Version == 4 {
			migration = &inventory[index]
			break
		}
	}
	if migration == nil {
		t.Fatal("migration 0004_exact_indexing_job_resolver.sql is missing")
	}
	sqlText := string(migration.SQL)
	if !strings.Contains(sqlText, "ambiguous queue-origin enrichment lineage") ||
		!strings.Contains(sqlText, "HAVING count(DISTINCT item.id) <> 1") {
		t.Fatal("migration 0004 lacks an ambiguous-lineage preflight")
	}

}

func TestApplyMigrationsRejectsUnknownChecksumDriftAndBadState(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name           string
		classification migrationClassification
	}{
		{name: "unknown", classification: migrationClassification{}},
		{
			name: "managed checksum drift",
			classification: migrationClassification{
				managed: []ledgerEntry{{version: 0, checksum: "wrong"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &recordingMigrationTx{classification: tt.classification}
			if err := applyMigrationsWithTx(context.Background(), tx, inventory); err == nil {
				t.Fatal("expected migration state rejection")
			}
			if len(tx.execs) != 0 {
				t.Fatalf("execs after rejection = %+v", tx.execs)
			}
		})
	}
}

func TestApplyMigrationsUnknownStateReportsOnlyValidatedLegacyAnchor(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	validAnchor := strings.Repeat("a", 64)
	for _, tt := range []struct {
		name       string
		anchor     string
		wantAnchor string
	}{
		{name: "validated checksum", anchor: validAnchor, wantAnchor: validAnchor},
		{name: "arbitrary comment", anchor: "not-a-checksum"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &recordingMigrationTx{
				classification: migrationClassification{legacyAnchor: tt.anchor},
			}
			err := applyMigrationsWithTx(context.Background(), tx, inventory)
			if err == nil {
				t.Fatal("expected unknown migration state")
			}
			if tt.wantAnchor != "" && !strings.Contains(err.Error(), tt.wantAnchor) {
				t.Fatalf("error = %q, want validated legacy anchor", err)
			}
			if tt.wantAnchor == "" && strings.Contains(err.Error(), tt.anchor) {
				t.Fatalf("error = %q, arbitrary legacy comment leaked", err)
			}
			if len(tx.execs) != 0 {
				t.Fatalf("execs after unknown state = %+v", tx.execs)
			}
		})
	}
}

func TestApplyMigrationsRejectsRoguePreLedgerTypeBeforeLedgerMutation(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{empty: true, rogueType: true},
	}
	if err := applyMigrationsWithTx(context.Background(), tx, inventory); err == nil {
		t.Fatal("expected rogue pre-ledger type to be rejected")
	}
	if len(tx.execs) != 0 {
		t.Fatalf("ledger mutation after rogue object = %+v", tx.execs)
	}
	if !strings.Contains(tx.classificationSQL, "pg_catalog.pg_type") ||
		!strings.Contains(tx.classificationSQL, "pg_catalog.pg_namespace") {
		t.Fatalf("classification query does not inspect owned namespaces/types: %s", tx.classificationSQL)
	}
}

func TestApplyMigrationsRollsBackInjectedFailureBetweenAdoptionRows(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{legacy: true},
		failAt:         3,
	}
	if err := applyMigrationsWithTx(context.Background(), tx, inventory); err == nil {
		t.Fatal("expected injected transaction failure")
	}
	if len(tx.execs) != 3 {
		t.Fatalf("recorded execs = %d, want failure at third exec", len(tx.execs))
	}
}

func TestApplyMigrationsUsesTransactionBoundaryAndRestartsAfterCrash(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	first := &recordingMigrationTx{
		classification: migrationClassification{legacy: true},
		failAt:         3,
	}
	beginCalls := 0
	err = applyMigrationsWithBegin(context.Background(), func(context.Context) (pgx.Tx, error) {
		beginCalls++
		return first, nil
	}, inventory)
	if err == nil {
		t.Fatal("expected injected migration failure")
	}
	if beginCalls != 1 || first.commitCount != 0 || first.rollbackCount != 1 {
		t.Fatalf("transaction boundary = begin %d, commit %d, rollback %d", beginCalls, first.commitCount, first.rollbackCount)
	}
	if len(first.committedLedger) != 0 {
		t.Fatalf("crashed migration committed ledger rows = %+v", first.committedLedger)
	}

	restart := &recordingMigrationTx{classification: migrationClassification{legacy: true}}
	err = applyMigrationsWithBegin(context.Background(), func(context.Context) (pgx.Tx, error) {
		beginCalls++
		return restart, nil
	}, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if beginCalls != 2 || restart.commitCount != 1 || restart.rollbackCount != 1 {
		t.Fatalf("restart transaction boundary = begin %d, commit %d, rollback %d", beginCalls, restart.commitCount, restart.rollbackCount)
	}
	if len(restart.committedLedger) != len(inventory) {
		t.Fatalf("restart committed ledger rows = %+v, want %d versions", restart.committedLedger, len(inventory))
	}
}

func assertDeferredChecks(t *testing.T, exec recordedExec, migration dbmigrations.Migration) {
	t.Helper()
	if exec.sql != "SET CONSTRAINTS ALL DEFERRED" {
		t.Fatalf("migration %s did not start from deferred checks: %q", migration.Name, exec.sql)
	}
}

func assertLedgerInsert(t *testing.T, exec recordedExec, migration dbmigrations.Migration) {
	t.Helper()
	if !strings.Contains(exec.sql, "INSERT INTO public.schema_migrations") {
		t.Fatalf("SQL = %q, want parameterized ledger insert", exec.sql)
	}
	if len(exec.args) != 2 || exec.args[0] != migration.Version || exec.args[1] != migration.Checksum {
		t.Fatalf("ledger args = %#v, want version/checksum for %d", exec.args, migration.Version)
	}
}

type migrationClassification struct {
	empty          bool
	legacy         bool
	legacyChecksum string
	legacyAnchor   string
	rogueType      bool
	managed        []ledgerEntry
}

type recordedExec struct {
	sql  string
	args []any
}

type recordingMigrationTx struct {
	classification     migrationClassification
	execs              []recordedExec
	queries            []string
	classificationSQL  string
	classificationArgs []any
	failAt             int
	pendingLedger      []ledgerEntry
	committedLedger    []ledgerEntry
	commitCount        int
	rollbackCount      int
	activeJobs         int64
	quiescenceQueries  int
}

func (tx *recordingMigrationTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, recordedExec{sql: sql, args: append([]any(nil), args...)})
	if strings.Contains(sql, "INSERT INTO public.schema_migrations") && len(args) == 2 {
		tx.pendingLedger = append(tx.pendingLedger, ledgerEntry{version: args[0].(int64), checksum: args[1].(string)})
	}
	if tx.failAt > 0 && len(tx.execs) == tx.failAt {
		return pgconn.CommandTag{}, errors.New("injected migration failure")
	}
	return pgconn.CommandTag{}, nil
}

func (tx *recordingMigrationTx) Begin(context.Context) (pgx.Tx, error) { return tx, nil }

func (tx *recordingMigrationTx) Commit(context.Context) error {
	tx.commitCount++
	tx.committedLedger = append(tx.committedLedger, tx.pendingLedger...)
	tx.pendingLedger = nil
	return nil
}

func (tx *recordingMigrationTx) Rollback(context.Context) error {
	tx.rollbackCount++
	tx.pendingLedger = nil
	return nil
}

func (tx *recordingMigrationTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (tx *recordingMigrationTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }

func (tx *recordingMigrationTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (tx *recordingMigrationTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (tx *recordingMigrationTx) Conn() *pgx.Conn { return nil }

func (tx *recordingMigrationTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FROM jobs AS job") {
		tx.quiescenceQueries++
		return recordingMigrationRow{values: []any{tx.activeJobs}}
	}
	tx.classificationSQL = sql
	tx.classificationArgs = append([]any(nil), args...)
	legacy := tx.classification.legacy
	if tx.classification.legacyChecksum != "" {
		legacy = false
		for _, arg := range args {
			if arg == tx.classification.legacyChecksum {
				legacy = true
				break
			}
		}
	}
	values := []any{
		len(tx.classification.managed) > 0,
		tx.classification.empty && !tx.classification.rogueType,
		legacy,
		tx.classification.legacyAnchor,
	}
	return recordingMigrationRow{values: values}
}

func (tx *recordingMigrationTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	tx.queries = append(tx.queries, sql)
	rows := make([]ledgerEntry, len(tx.classification.managed))
	copy(rows, tx.classification.managed)
	return &recordingMigrationRows{entries: rows}, nil
}

type recordingMigrationRow struct {
	values []any
}

func (row recordingMigrationRow) Scan(dest ...any) error {
	for index, value := range row.values {
		if index >= len(dest) {
			break
		}
		switch target := dest[index].(type) {
		case *bool:
			*target = value.(bool)
		case *string:
			*target = value.(string)
		case *int64:
			*target = value.(int64)
		}
	}
	return nil
}

type recordingMigrationRows struct {
	entries []ledgerEntry
	index   int
}

func (rows *recordingMigrationRows) Close() {}

func (rows *recordingMigrationRows) Err() error { return nil }

func (rows *recordingMigrationRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (rows *recordingMigrationRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (rows *recordingMigrationRows) Next() bool {
	if rows.index >= len(rows.entries) {
		return false
	}
	rows.index++
	return true
}

func (rows *recordingMigrationRows) Scan(dest ...any) error {
	entry := rows.entries[rows.index-1]
	if len(dest) != 2 {
		return errors.New("unexpected migration row shape")
	}
	*dest[0].(*int64) = entry.version
	*dest[1].(*string) = entry.checksum
	return nil
}

func (rows *recordingMigrationRows) Values() ([]any, error) { return nil, nil }

func (rows *recordingMigrationRows) RawValues() [][]byte { return nil }

func (rows *recordingMigrationRows) Conn() *pgx.Conn { return nil }
