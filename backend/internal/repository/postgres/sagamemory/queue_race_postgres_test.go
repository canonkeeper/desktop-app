package sagamemory

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-15-desk-reading-queue.md RQ1.2: «Продолжить» queues the reading of its saga (the Work of setReadingConsent —
// the catch-up of one saga) while the catch-up of the start holds the cards Job it queued in its open transaction: the queue
// of «Продолжить» waits for it, reuses its Job and answers no error — the page gets the consent, not 500 — and one Job waits.
func TestTheQueueOfTheConsentBesideTheCatchUpOfTheStartAnswersNoError(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_queue_race")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{ProjectID: memoryProjectID, ProjectGeneration: 1}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}

	start, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = start.Rollback(ctx) }()
	var startPID int32
	if err := start.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&startPID); err != nil {
		t.Fatal(err)
	}
	held, err := jobstore.New(db).UpsertChapterCardsIntent(ctx, start, jobs.ChapterCardsIntentInput{
		Subject: cardsSubjectForTest(), Source: jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}},
	})
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		queued int
		err    error
	}
	consent := make(chan outcome, 1)
	go func() {
		queued, err := service.NewQueueMissingCards(repository, repository, repository, repository).ExecuteFor(ctx, memoryProjectID)
		consent <- outcome{queued, err}
	}()
	var answered *outcome
	for deadline := time.Now().Add(10 * time.Second); answered == nil; time.Sleep(5 * time.Millisecond) {
		var blocked bool
		if err := db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::integer = ANY(pg_blocking_pids(pid)))`, startPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case got := <-consent:
			answered = &got
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the queue of the consent neither waited nor answered")
		}
	}
	if err := start.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if answered == nil {
		select {
		case got := <-consent:
			answered = &got
		case <-time.After(10 * time.Second):
			t.Fatal("the queue of the consent did not answer after the start committed")
		}
	}
	if answered.err != nil || answered.queued != 1 {
		t.Fatalf("the queue of the consent beside the start = %d, %v; want the waiting Job, no error", answered.queued, answered.err)
	}
	var waiting int
	var id string
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), min(id::text) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued'`,
		memoryProjectID).Scan(&waiting, &id); err != nil {
		t.Fatal(err)
	}
	if waiting != 1 || id != held.ID {
		t.Fatalf("%d cards Jobs wait (%s); want the one of the start %s", waiting, id, held.ID)
	}
}
