package sagamemory

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/testutil"
)

// Text indexed before cards existed gets its cards Job at backend start: once per version,
// reusing the Job still waiting, and none once every chapter has its card.
func TestMissingCardsAreQueuedOncePerVersion(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_catch_up")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	catchUp := service.NewQueueMissingCards(repository, repository, repository, repository)

	for run := 1; run <= 2; run++ {
		if queued, err := catchUp.Execute(ctx); err != nil || queued != 1 {
			t.Fatalf("run %d = %d, %v; want the draft version queued", run, queued, err)
		}
	}
	waiting := func() int {
		var count int
		if err := db.Pool.QueryRow(ctx, `
			SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards' AND status='queued'
		`, memoryProjectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := waiting(); count != 1 {
		t.Fatalf("queued cards Jobs = %d, want the waiting one reused", count)
	}

	build := mapCardsJob(t, repository, &namesCardModel{})
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("cards run = %q, %v", status, err)
	}
	if queued, err := catchUp.Execute(ctx); err != nil || queued != 0 || waiting() != 0 {
		t.Fatalf("after the cards = %d, %v, %d waiting; want nothing queued", queued, err, waiting())
	}
}

// A project nobody can open (its owner and members are disabled) gets no cards at start:
// test accounts disabled in production keep their projects without spending model time.
func TestMissingCardsSkipProjectsWithoutActiveMembers(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_inactive")
	seedMemoryProject(t, db)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET disabled_at = clock_timestamp() WHERE id = $1`, memoryOwnerID); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	queued, err := service.NewQueueMissingCards(repository, repository, repository, repository).Execute(ctx)
	if err != nil || queued != 0 {
		t.Fatalf("Execute() = %d, %v; want no cards Job for a project nobody can open", queued, err)
	}
}
