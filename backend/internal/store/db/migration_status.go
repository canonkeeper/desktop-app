package storedb

import (
	"context"
	"errors"
	"fmt"

	dbmigrations "canonkeeper/backend/db/migrations"

	"github.com/jackc/pgx/v5"
)

// MigrationStatus is what the numbered runner knows of a database before it applies anything
// (specs/2026-11-12-desk-process.md PR3.5): the desktop app copies the data before an update by it and refuses the data
// of a newer program. The ledger and the inventory stay the runner's.
type MigrationStatus struct {
	// Last is the last version the ledger records; -1 for an empty database.
	Last int64
	// Pending counts the migrations of the inventory the database has not had.
	Pending int
	// Newer says the ledger records a version the inventory does not know: a newer program migrated the database.
	Newer bool
}

// ReadMigrationStatus reads the ledger against the embedded inventory in a read-only transaction. A database the runner
// would refuse is refused here too, with the runner's error and whether migrations are pending or not: an applied
// migration whose file changed since is ErrMigrationChecksumDrift. A ledger past the inventory is Newer, not an error. A
// legacy baseline without a ledger, which the runner converts for the site, is refused: the desktop never had one.
func ReadMigrationStatus(ctx context.Context, db *DB) (MigrationStatus, error) {
	if db == nil || db.Pool == nil {
		return MigrationStatus{}, errors.New("migration database is nil")
	}
	inventory, err := dbmigrations.Read()
	if err != nil {
		return MigrationStatus{}, err
	}
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("begin migration status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return migrationStatusWithTx(ctx, tx, inventory)
}

func migrationStatusWithTx(ctx context.Context, tx migrationTx, inventory []dbmigrations.Migration) (MigrationStatus, error) {
	if len(inventory) < 2 || inventory[0].Version != 0 || inventory[1].Version != 1 {
		return MigrationStatus{}, fmt.Errorf("%w: migration 0000 and 0001 are required", ErrUnknownMigrationState)
	}
	state, err := classifyMigrationState(ctx, tx, inventory[1].Checksum)
	if err != nil {
		return MigrationStatus{}, err
	}
	if state.empty && !state.hasLedger {
		return MigrationStatus{Last: -1, Pending: len(inventory)}, nil
	}
	if !state.hasLedger {
		return MigrationStatus{}, fmt.Errorf("%w: the database has no migration ledger", ErrUnknownMigrationState)
	}
	entries, err := readLedger(ctx, tx)
	if err != nil {
		return MigrationStatus{}, err
	}
	if len(entries) == 0 {
		return MigrationStatus{}, fmt.Errorf("%w: schema_migrations has no version 0", ErrUnknownMigrationState)
	}
	for index, entry := range entries {
		if entry.version != int64(index) {
			return MigrationStatus{}, fmt.Errorf("%w: ledger version %d is out of sequence", ErrUnknownMigrationState, entry.version)
		}
		if index < len(inventory) && entry.checksum != inventory[index].Checksum {
			return MigrationStatus{}, fmt.Errorf("%w: version %d", ErrMigrationChecksumDrift, entry.version)
		}
	}
	last := entries[len(entries)-1].version
	if len(entries) > len(inventory) {
		return MigrationStatus{Last: last, Newer: true}, nil
	}
	return MigrationStatus{Last: last, Pending: len(inventory) - len(entries)}, nil
}
