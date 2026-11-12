package sagamemory

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	"canonkeeper/backend/internal/testutil"
)

// siteRepository is the saga repository of the site's process: its one text index store queues the cards Job always.
func siteRepository(db *storedb.DB) *Repository { return New(db).WithTextIndex(indexingstore.New(db)) }

// Text indexed before cards existed gets its cards Job at backend start: once per version,
// reusing the Job still waiting, and none once every chapter has its card.
func TestMissingCardsAreQueuedOncePerVersion(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_catch_up")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
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

type helperForCatchUpTest bool

func (connected helperForCatchUpTest) Connected(context.Context) bool { return bool(connected) }

// specs/2026-11-12-desk-window.md WN3.4: in a build whose helper is not connected the start queues no cards Job and says
// it queued none — the count is of the Jobs that wait, not of the versions it asked for; once the helper is there, the
// same start queues the version and counts it.
func TestTheCatchUpCountsOnlyTheCardsItQueued(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_catch_up_helper")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	waiting := func() int {
		var count int
		if err := db.Pool.QueryRow(ctx, `
			SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards' AND status='queued'
		`, memoryProjectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	without := New(db).WithTextIndex(indexingstore.New(db, indexingstore.WithHelper(helperForCatchUpTest(false))))
	if queued, err := service.NewQueueMissingCards(without, without, without, without).Execute(ctx); err != nil || queued != 0 || waiting() != 0 {
		t.Fatalf("without the helper = %d, %v, %d waiting; want nothing queued and nothing counted", queued, err, waiting())
	}

	// The rule is the one text index store of the process: a repository given none builds no store of its own past the
	// rule — it queues nothing and says why.
	bare := New(db)
	if queued, err := service.NewQueueMissingCards(bare, bare, bare, bare).Execute(ctx); !errors.Is(err, errNoTextIndex) || queued != 0 || waiting() != 0 {
		t.Fatalf("without a text index store = %d, %v, %d waiting; want nothing queued and the refusal", queued, err, waiting())
	}

	with := New(db).WithTextIndex(indexingstore.New(db, indexingstore.WithHelper(helperForCatchUpTest(true))))
	if queued, err := service.NewQueueMissingCards(with, with, with, with).Execute(ctx); err != nil || queued != 1 || waiting() != 1 {
		t.Fatalf("with the helper = %d, %v, %d waiting; want the version queued", queued, err, waiting())
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
	repository := siteRepository(db)
	queued, err := service.NewQueueMissingCards(repository, repository, repository, repository).Execute(ctx)
	if err != nil || queued != 0 {
		t.Fatalf("Execute() = %d, %v; want no cards Job for a project nobody can open", queued, err)
	}
}
