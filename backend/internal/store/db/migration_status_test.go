package storedb

import (
	"context"
	"errors"
	"testing"

	dbmigrations "canonkeeper/backend/db/migrations"
)

func statusInventory() []dbmigrations.Migration {
	return []dbmigrations.Migration{
		{Version: 0, Name: "0000_ledger.sql", Checksum: "c0"},
		{Version: 1, Name: "0001_baseline.sql", Checksum: "c1"},
		{Version: 2, Name: "0002_next.sql", Checksum: "c2"},
	}
}

func ledger(versions ...int64) []ledgerEntry {
	entries := make([]ledgerEntry, 0, len(versions))
	for _, version := range versions {
		entries = append(entries, ledgerEntry{version: version, checksum: "c" + string(rune('0'+version))})
	}
	return entries
}

// specs/2026-11-12-desk-process.md PR3.5: the runner says what the desktop needs to know before it changes anything —
// the last applied version, how many migrations of the inventory are pending, whether a newer program migrated the
// database — and writes nothing while it reads.
func TestMigrationStatusReadsTheLedgerAgainstTheInventory(t *testing.T) {
	for _, test := range []struct {
		name           string
		classification migrationClassification
		want           MigrationStatus
	}{
		{name: "empty database", classification: migrationClassification{empty: true}, want: MigrationStatus{Last: -1, Pending: 3}},
		{name: "one behind", classification: migrationClassification{managed: ledger(0, 1)}, want: MigrationStatus{Last: 1, Pending: 1}},
		{name: "current", classification: migrationClassification{managed: ledger(0, 1, 2)}, want: MigrationStatus{Last: 2}},
		{name: "newer program", classification: migrationClassification{managed: ledger(0, 1, 2, 3, 4)}, want: MigrationStatus{Last: 4, Newer: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &recordingMigrationTx{classification: test.classification}
			got, err := migrationStatusWithTx(context.Background(), tx, statusInventory())
			if err != nil || got != test.want {
				t.Fatalf("status = %+v, %v; want %+v", got, err, test.want)
			}
			if len(tx.execs) != 0 || tx.quiescenceQueries != 0 {
				t.Fatalf("the status wrote %+v and asked %d quiescence queries", tx.execs, tx.quiescenceQueries)
			}
		})
	}
}

// PR3.5: a database the runner would refuse is refused by the status too, with the runner's error, whether migrations
// are pending or not — an applied migration whose file changed since is checksum drift; the status is not a second
// runner and writes nothing.
func TestMigrationStatusRefusesWhatTheRunnerRefuses(t *testing.T) {
	for _, test := range []struct {
		name           string
		classification migrationClassification
		want           error
	}{
		{name: "unrecognized objects", classification: migrationClassification{}, want: ErrUnknownMigrationState},
		{name: "ledger out of sequence", classification: migrationClassification{managed: []ledgerEntry{{version: 0, checksum: "c0"}, {version: 2, checksum: "c2"}}}, want: ErrUnknownMigrationState},
		{name: "edited migration, none pending", classification: migrationClassification{managed: []ledgerEntry{{version: 0, checksum: "c0"}, {version: 1, checksum: "edited"}, {version: 2, checksum: "c2"}}}, want: ErrMigrationChecksumDrift},
		{name: "edited migration, one pending", classification: migrationClassification{managed: []ledgerEntry{{version: 0, checksum: "c0"}, {version: 1, checksum: "edited"}}}, want: ErrMigrationChecksumDrift},
		{name: "edited migration under newer ones", classification: migrationClassification{managed: []ledgerEntry{{version: 0, checksum: "edited"}, {version: 1, checksum: "c1"}, {version: 2, checksum: "c2"}, {version: 3, checksum: "c3"}}}, want: ErrMigrationChecksumDrift},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &recordingMigrationTx{classification: test.classification}
			if _, err := migrationStatusWithTx(context.Background(), tx, statusInventory()); !errors.Is(err, test.want) {
				t.Fatalf("status error = %v; want %v", err, test.want)
			}
			if len(tx.execs) != 0 {
				t.Fatalf("the status wrote %+v", tx.execs)
			}
		})
	}
	if _, err := ReadMigrationStatus(context.Background(), nil); err == nil {
		t.Fatal("a status without a database")
	}
}

// PR3.5: a legacy baseline without a ledger — the site's before the ledger — the runner converts as it applies; the status
// does not stand in for that conversion and refuses it. A database of the desktop app always has a ledger.
func TestMigrationStatusRefusesALegacyBaseline(t *testing.T) {
	tx := &recordingMigrationTx{classification: migrationClassification{legacy: true}}
	if _, err := migrationStatusWithTx(context.Background(), tx, statusInventory()); !errors.Is(err, ErrUnknownMigrationState) {
		t.Fatalf("status error = %v; want ErrUnknownMigrationState", err)
	}
	if len(tx.execs) != 0 {
		t.Fatalf("the status wrote %+v", tx.execs)
	}
}

// PR3.5: the status of the embedded inventory itself — the program knows every migration it carries.
func TestMigrationStatusKnowsTheEmbeddedInventory(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	managed := make([]ledgerEntry, 0, len(inventory))
	for _, migration := range inventory {
		managed = append(managed, ledgerEntry{version: migration.Version, checksum: migration.Checksum})
	}
	tx := &recordingMigrationTx{classification: migrationClassification{managed: managed[:len(managed)-1]}}
	got, err := migrationStatusWithTx(context.Background(), tx, inventory)
	if err != nil || got != (MigrationStatus{Last: inventory[len(inventory)-2].Version, Pending: 1}) {
		t.Fatalf("status one migration behind = %+v, %v", got, err)
	}
}
