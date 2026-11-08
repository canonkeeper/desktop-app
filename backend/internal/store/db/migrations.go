package storedb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	dbmigrations "canonkeeper/backend/db/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrUnknownMigrationState  = errors.New("unknown database migration state")
	ErrMigrationChecksumDrift = errors.New("migration checksum drift")
	ErrMigrationNotQuiescent  = errors.New("database has running or canceling jobs")
)

type migrationTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type migrationState struct {
	hasLedger    bool
	empty        bool
	legacy       bool
	legacyAnchor string
}

type ledgerEntry struct {
	version  int64
	checksum string
}

// ApplyMigrations applies the canonical embedded inventory in one transaction.
func ApplyMigrations(ctx context.Context, db *DB) error {
	if db == nil || db.Pool == nil {
		return errors.New("migration database is nil")
	}
	inventory, err := dbmigrations.Read()
	if err != nil {
		return err
	}
	return applyMigrationsWithBegin(ctx, db.Pool.Begin, inventory)
}

func applyMigrationsWithBegin(ctx context.Context, begin func(context.Context) (pgx.Tx, error), inventory []dbmigrations.Migration) error {
	tx, err := begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := applyMigrationsWithTx(ctx, tx, inventory); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func (db *DB) ApplyMigrations(ctx context.Context) error {
	return ApplyMigrations(ctx, db)
}

func applyMigrationsWithTx(ctx context.Context, tx migrationTx, inventory []dbmigrations.Migration) error {
	if len(inventory) < 2 || inventory[0].Version != 0 || inventory[1].Version != 1 {
		return fmt.Errorf("%w: migration 0000 and 0001 are required", ErrUnknownMigrationState)
	}
	state, err := classifyMigrationState(ctx, tx, inventory[1].Checksum)
	if err != nil {
		return err
	}
	if state.hasLedger {
		if err := requireMigrationQuiescence(ctx, tx); err != nil {
			return err
		}
		return applyManagedMigrations(ctx, tx, inventory)
	}
	if state.empty {
		return applyMigrationsFrom(ctx, tx, inventory, 0)
	}
	if state.legacy {
		if err := requireMigrationQuiescence(ctx, tx); err != nil {
			return err
		}
		if err := applyMigration(ctx, tx, inventory[0]); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, inventory[0]); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, inventory[1]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "COMMENT ON SCHEMA canonkeeper_worker IS NULL"); err != nil {
			return fmt.Errorf("clear legacy migration anchor: %w", err)
		}
		return applyMigrationsFrom(ctx, tx, inventory, 2)
	}
	if state.legacyAnchor != "" {
		return fmt.Errorf("%w: database contains unrecognized objects (legacy anchor checksum %s)", ErrUnknownMigrationState, state.legacyAnchor)
	}
	return fmt.Errorf("%w: database contains unrecognized objects", ErrUnknownMigrationState)
}

func requireMigrationQuiescence(ctx context.Context, tx migrationTx) error {
	quiescent, err := checkMigrationQuiescence(ctx, tx)
	if err != nil {
		return err
	}
	if !quiescent {
		return ErrMigrationNotQuiescent
	}
	return nil
}

func classifyMigrationState(ctx context.Context, tx migrationTx, baselineChecksum string) (migrationState, error) {
	const query = `
SELECT
  EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'public'
      AND relation.relname = 'schema_migrations'
  ),
  NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_namespace AS namespace
    WHERE namespace.nspname = 'canonkeeper_worker'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_namespace AS namespace
    WHERE namespace.nspname !~ '^pg_'
      AND namespace.nspname NOT IN ('information_schema', 'public')
  )
  AND NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname IN ('public', 'canonkeeper_worker')
  )
  AND NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_proc AS procedure
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = procedure.pronamespace
    WHERE namespace.nspname IN ('public', 'canonkeeper_worker')
  )
  AND NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_type AS type
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = type.typnamespace
    WHERE namespace.nspname IN ('public', 'canonkeeper_worker')
  ),
  COALESCE(
    pg_catalog.obj_description(
      pg_catalog.to_regnamespace('canonkeeper_worker'),
      'pg_namespace'
    ),
    ''
  ) = $1
    AND pg_catalog.to_regclass('public.users') IS NOT NULL
    AND pg_catalog.to_regclass('public.jobs') IS NOT NULL
    AND pg_catalog.to_regprocedure(
      'canonkeeper_worker.claim_next_deletion_target_task_for_worker(bytea,integer,boolean)'
    ) IS NOT NULL,
  COALESCE(
    pg_catalog.obj_description(
      pg_catalog.to_regnamespace('canonkeeper_worker'),
      'pg_namespace'
    ),
    ''
  )`
	var state migrationState
	var legacyAnchor string
	if err := tx.QueryRow(ctx, query, baselineChecksum).Scan(&state.hasLedger, &state.empty, &state.legacy, &legacyAnchor); err != nil {
		return migrationState{}, fmt.Errorf("classify migration state: %w", err)
	}
	state.legacyAnchor = validatedMigrationAnchor(legacyAnchor)
	return state, nil
}

func validatedMigrationAnchor(value string) string {
	if len(value) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func applyManagedMigrations(ctx context.Context, tx migrationTx, inventory []dbmigrations.Migration) error {
	entries, err := readLedger(ctx, tx)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("%w: schema_migrations has no version 0", ErrUnknownMigrationState)
	}
	for index, entry := range entries {
		if entry.version != int64(index) {
			return fmt.Errorf("%w: ledger version %d is out of sequence", ErrUnknownMigrationState, entry.version)
		}
		if index >= len(inventory) {
			return fmt.Errorf("%w: ledger contains unknown version %d", ErrUnknownMigrationState, entry.version)
		}
		if entry.checksum != inventory[index].Checksum {
			return fmt.Errorf("%w: version %d", ErrMigrationChecksumDrift, entry.version)
		}
	}
	return applyMigrationsFrom(ctx, tx, inventory, len(entries))
}

func readLedger(ctx context.Context, tx migrationTx) ([]ledgerEntry, error) {
	rows, err := tx.Query(ctx, `
SELECT version, checksum
FROM public.schema_migrations
ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	entries := make([]ledgerEntry, 0)
	for rows.Next() {
		var entry ledgerEntry
		if err := rows.Scan(&entry.version, &entry.checksum); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read migration ledger rows: %w", err)
	}
	return entries, nil
}

func applyMigrationsFrom(ctx context.Context, tx migrationTx, inventory []dbmigrations.Migration, start int) error {
	if start < 0 || start > len(inventory) {
		return fmt.Errorf("%w: invalid next migration index %d", ErrUnknownMigrationState, start)
	}
	for _, migration := range inventory[start:] {
		if err := applyMigration(ctx, tx, migration); err != nil {
			return err
		}
		if err := recordMigration(ctx, tx, migration); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration of the batch transaction. A migration may switch the
// transaction to immediate constraint checks; the next one starts from deferred checks again,
// as when it runs alone (the schema has no deferrable constraint that starts immediate).
func applyMigration(ctx context.Context, tx migrationTx, migration dbmigrations.Migration) error {
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return fmt.Errorf("apply migration %s: %w", migration.Name, err)
	}
	if _, err := tx.Exec(ctx, string(migration.SQL)); err != nil {
		return fmt.Errorf("apply migration %s: %w", migration.Name, err)
	}
	return nil
}

func recordMigration(ctx context.Context, tx migrationTx, migration dbmigrations.Migration) error {
	const query = `
INSERT INTO public.schema_migrations (version, checksum)
VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, query, migration.Version, migration.Checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.Name, err)
	}
	return nil
}
