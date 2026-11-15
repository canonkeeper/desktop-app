package storedb

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbmigrations "canonkeeper/backend/db/migrations"

	"github.com/jackc/pgx/v5"
)

// oneProcessTx is the recording transaction of the runner on a cluster whose setting canonkeeper.start is mark.
type oneProcessTx struct {
	*recordingMigrationTx
	mark       string
	markAsked  int
	markBefore int // the statements executed before the mark was asked
}

func (tx *oneProcessTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "current_setting") {
		tx.markAsked++
		tx.markBefore = len(tx.execs)
		return recordingMigrationRow{values: []any{tx.mark}}
	}
	return tx.recordingMigrationTx.QueryRow(ctx, sql, args...)
}

func (tx *oneProcessTx) Begin(context.Context) (pgx.Tx, error) { return tx, nil }

// specs/2026-11-13-desk-upgrade.md UG1.1, UG1.2: the database of one process — the desktop app's — is migrated whatever
// the status of its Jobs: a Job a dead process left running or canceling is data, and no guard of quiescence is asked.
// The proof that the database is one process's is the mark of the cluster the caller started in this start; another
// cluster — one without the mark, or with the mark of another start — is refused before anything changes.
func TestTheDatabaseOfOneProcessIsMigratedWithItsJobsInAnyStatus(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	behind := []ledgerEntry{{version: inventory[0].Version, checksum: inventory[0].Checksum}}
	tx := &oneProcessTx{
		recordingMigrationTx: &recordingMigrationTx{classification: migrationClassification{managed: behind}, activeJobs: 2},
		mark:                 "4f1c0de2a9b8",
	}
	if err := applyOneProcessMigrationsWithBegin(context.Background(), tx.Begin, inventory, "4f1c0de2a9b8"); err != nil {
		t.Fatal(err)
	}
	if tx.quiescenceQueries != 0 {
		t.Fatalf("the runner of one process asked %d quiescence queries", tx.quiescenceQueries)
	}
	if tx.markAsked != 1 || tx.markBefore != 0 {
		t.Fatalf("the mark was asked %d times, after %d statements", tx.markAsked, tx.markBefore)
	}
	if want := (len(inventory) - 1) * 3; len(tx.execs) != want || tx.commitCount != 1 {
		t.Fatalf("execs = %d (want %d), commits = %d", len(tx.execs), want, tx.commitCount)
	}

	for _, mark := range []string{"", "a-mark-of-another-start"} {
		other := &oneProcessTx{
			recordingMigrationTx: &recordingMigrationTx{classification: migrationClassification{managed: behind}},
			mark:                 mark,
		}
		err := applyOneProcessMigrationsWithBegin(context.Background(), other.Begin, inventory, "4f1c0de2a9b8")
		if !errors.Is(err, ErrNotOneProcess) {
			t.Fatalf("a cluster with the mark %q: %v", mark, err)
		}
		if len(other.execs) != 0 || other.commitCount != 0 {
			t.Fatalf("a cluster with the mark %q was changed: %+v", mark, other.execs)
		}
	}
	// An empty mark proves nothing.
	empty := &oneProcessTx{recordingMigrationTx: &recordingMigrationTx{classification: migrationClassification{managed: behind}}}
	if err := applyOneProcessMigrationsWithBegin(context.Background(), empty.Begin, inventory, ""); !errors.Is(err, ErrNotOneProcess) {
		t.Fatalf("an empty mark: %v", err)
	}
}

// UG1.1: the runner of the site keeps its guard: a running or canceling Job stops the migrations of a cutover.
func TestTheRunnerOfTheSiteStillRefusesActiveJobs(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	tx := &recordingMigrationTx{
		classification: migrationClassification{managed: []ledgerEntry{{version: inventory[0].Version, checksum: inventory[0].Checksum}}},
		activeJobs:     1,
	}
	if err := applyMigrationsWithBegin(context.Background(), tx.Begin, inventory); !errors.Is(err, ErrMigrationNotQuiescent) {
		t.Fatalf("error = %v, want ErrMigrationNotQuiescent", err)
	}
	if len(tx.execs) != 0 || tx.commitCount != 0 {
		t.Fatalf("the runner of the site migrated past active Jobs: %+v", tx.execs)
	}
}
