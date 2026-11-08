package jobstore

import (
	"testing"

	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
)

// TestMigrationQuiescenceIgnoresOnlyUnclaimableExecutingJobs закрепляет fail-closed
// контракт `migrate --check-idle`: readable running/canceling Job блокирует cutover,
// а точный запечатанный job owner в deletion fence больше не является executing work,
// которое artifact cutover мог бы оставить висящим.
func TestMigrationQuiescenceIgnoresOnlyUnclaimableExecutingJobs(t *testing.T) {
	for _, status := range []jobs.Status{jobs.StatusRunning, jobs.StatusCanceling} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newModelTerminalFixture(t, status)

			idle, err := storedb.MigrationQuiescent(fixture.ctx, fixture.db)
			if err != nil {
				t.Fatal(err)
			}
			if idle {
				t.Fatalf("readable %s job must block migration quiescence", status)
			}

			fenceHeartbeatOwnerForTest(t, fixture)

			idle, err = storedb.MigrationQuiescent(fixture.ctx, fixture.db)
			if err != nil {
				t.Fatal(err)
			}
			if !idle {
				t.Fatalf("fenced %s job must not block migration quiescence", status)
			}
		})
	}
}
