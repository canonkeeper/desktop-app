package storedb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrationQuiescenceUsesCurrentDurableAuthorities(t *testing.T) {
	tests := []struct {
		name     string
		jobs     int64
		wantIdle bool
	}{
		{name: "no executing jobs", wantIdle: true},
		{name: "running or canceling job", jobs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queryer := quiescenceQueryerForTest{row: quiescenceRowForTest{
				values: []any{tt.jobs},
			}}
			idle, err := checkMigrationQuiescence(context.Background(), &queryer)
			if err != nil {
				t.Fatal(err)
			}
			if idle != tt.wantIdle {
				t.Fatalf("idle = %t, want %t", idle, tt.wantIdle)
			}
			if queryer.query == "" {
				t.Fatal("quiescence query was not issued")
			}
		})
	}
}

func TestMigrationQuiescenceAllowsDurableQueuedJobs(t *testing.T) {
	queryer := quiescenceQueryerForTest{row: quiescenceRowForTest{values: []any{int64(0)}}}
	if _, err := checkMigrationQuiescence(context.Background(), &queryer); err != nil {
		t.Fatal(err)
	}
	query := strings.Join(strings.Fields(queryer.query), " ")
	for _, required := range []string{
		"job.status NOT IN ('queued', 'succeeded', 'failed', 'canceled', 'expired')",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("migration quiescence query missing %q", required)
		}
	}
	if strings.Contains(query, "import_queue_items") {
		t.Fatal("queued Job safety must not depend on queue-specific state")
	}
}

// The build of the threads of a saga runs for hours on a large saga and goes on from its checkpoint after a restart:
// a cutover of TEST does not wait for it, as a production release does not (specs/2026-10-28-release-artifacts.md).
func TestMigrationQuiescenceDoesNotWaitForTheSagaLayer(t *testing.T) {
	queryer := quiescenceQueryerForTest{row: quiescenceRowForTest{values: []any{int64(0)}}}
	if _, err := checkMigrationQuiescence(context.Background(), &queryer); err != nil {
		t.Fatal(err)
	}
	query := strings.Join(strings.Fields(queryer.query), " ")
	if !strings.Contains(query, "job.kind <> 'saga_layer'") {
		t.Fatal("migration quiescence must not wait for the saga layer Job, which resumes from its checkpoint")
	}
}

func TestMigrationQuiescenceFailsClosedUnlessExactOwnerIsUnreadable(t *testing.T) {
	queryer := quiescenceQueryerForTest{row: quiescenceRowForTest{values: []any{int64(0)}}}
	if _, err := checkMigrationQuiescence(context.Background(), &queryer); err != nil {
		t.Fatal(err)
	}
	query := strings.Join(strings.Fields(queryer.query), " ")
	for _, required := range []string{
		"job.status NOT IN ('queued', 'succeeded', 'failed', 'canceled', 'expired')",
		"job.artifact_source_owner_id IS NULL",
		"sealed.artifact_kind = 'job'",
		"sealed.artifact_id = job.id",
		"ck_artifact_source_owner_is_readable(job.artifact_source_owner_id, job.project_id, 'job', job.id)",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("migration quiescence query missing %q", required)
		}
	}
	if strings.Contains(query, "public.") {
		t.Fatal("migration quiescence query must use unqualified names for isolated schema search_path")
	}
}

type quiescenceQueryerForTest struct {
	query string
	row   pgx.Row
}

func (q *quiescenceQueryerForTest) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	q.query = query
	return q.row
}

type quiescenceRowForTest struct {
	values []any
}

func (row quiescenceRowForTest) Scan(dest ...any) error {
	if len(dest) != len(row.values) {
		return errors.New("unexpected quiescence row shape")
	}
	for index, value := range row.values {
		switch target := dest[index].(type) {
		case *int64:
			*target = value.(int64)
		default:
			return errors.New("unexpected quiescence destination")
		}
	}
	return nil
}
