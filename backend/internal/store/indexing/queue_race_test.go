package indexingstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-15-desk-reading-queue.md RQ1.1, RQ1.3: two queues of the cards Job of one saga at once — the catch-up of the
// start in its transaction and «Продолжить» beside it — give one Job and no refusal: «such a Job waits already» is the success
// of the queue, not the unique index answering 23505. The same for the layer Job, which has no unique index and so got two
// Jobs. The second queue waits for the first one's transaction and reuses its Job; a queue over a waiting Job reuses it as
// before, and the store's own queues at once give one Job each.
func TestTwoQueuesOfASagaAtOnceGiveOneJobAndNoRefusal(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 8400, governance.SourceViewPublished)
	view := governance.SourceViewPublished
	queueIn := func(tx pgx.Tx, kind jobs.Kind) (jobs.Job, error) {
		id, err := storedb.ParseUUID(fixture.projectID)
		if err != nil {
			return jobs.Job{}, err
		}
		generation, err := lockCanonTextTerminalProject(ctx, tx, id)
		if err != nil {
			return jobs.Job{}, err
		}
		source, err := projectJobSource(fixture.projectID, generation, view)
		if err != nil {
			return jobs.Job{}, err
		}
		if kind == jobs.KindSagaLayer {
			return jobstore.New(fixture.db).UpsertSagaLayerIntent(ctx, tx, jobs.SagaLayerIntentInput{
				Subject: jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: fixture.projectID, SourceView: view}, Source: source,
			})
		}
		return jobstore.New(fixture.db).UpsertChapterCardsIntent(ctx, tx, jobs.ChapterCardsIntentInput{
			Subject: jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: fixture.projectID, SourceView: view}, Source: source,
		})
	}
	waitingOf := func(kind jobs.Kind) int {
		t.Helper()
		return countStoreRowsForTest(t, fixture.db, `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = '`+string(kind)+`' AND status = 'queued'`, fixture.projectID)
	}

	for _, kind := range []jobs.Kind{jobs.KindChapterCards, jobs.KindSagaLayer} {
		first, err := fixture.db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var firstPID uint32
		if err := first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&firstPID); err != nil {
			t.Fatal(err)
		}
		queued, err := queueIn(first, kind)
		if err != nil {
			t.Fatalf("%s: the first queue = %v", kind, err)
		}
		type outcome struct {
			job jobs.Job
			err error
		}
		second := make(chan outcome, 1)
		commit := make(chan bool)
		var done sync.WaitGroup
		done.Add(1)
		go func() {
			defer done.Done()
			tx, err := fixture.db.Pool.Begin(ctx)
			if err != nil {
				second <- outcome{err: err}
				return
			}
			job, err := queueIn(tx, kind)
			second <- outcome{job: job, err: err}
			if <-commit && err == nil {
				err = tx.Commit(ctx)
			}
			if err != nil {
				_ = tx.Rollback(ctx)
			}
		}()
		// The second queue has asked while the first transaction holds its Job: it waits for it, or — without the rule — it
		// already answered.
		var answered *outcome
		for deadline := time.Now().Add(10 * time.Second); answered == nil; time.Sleep(5 * time.Millisecond) {
			var blocked bool
			if err := fixture.db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::integer = ANY(pg_blocking_pids(pid)))`, int32(firstPID)).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case got := <-second:
				answered = &got
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the second queue neither waited nor answered", kind)
			}
		}
		if err := first.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if answered == nil {
			select {
			case got := <-second:
				answered = &got
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: the second queue did not answer after the first committed", kind)
			}
		}
		commit <- true
		done.Wait()
		if answered.err != nil || answered.job.ID != queued.ID {
			t.Fatalf("%s: the second queue at once = %+v, %v; want the Job of the first %s", kind, answered.job.ID, answered.err, queued.ID)
		}
		if got := waitingOf(kind); got != 1 {
			t.Fatalf("%s: %d Jobs wait after two queues at once; want one", kind, got)
		}
		// A queue over the waiting Job reuses it, as before.
		var again jobs.Job
		if err := pgx.BeginFunc(ctx, fixture.db.Pool, func(tx pgx.Tx) error {
			again, err = queueIn(tx, kind)
			return err
		}); err != nil || again.ID != queued.ID || waitingOf(kind) != 1 {
			t.Fatalf("%s: a queue over the waiting Job = %s, %v; %d wait", kind, again.ID, err, waitingOf(kind))
		}
	}

	// The store's own queues of another saga, many at once, from transactions of their own.
	fixture = newStoreFixture(t, 8500, view)
	var group sync.WaitGroup
	failures := make(chan error, 16)
	for range 8 {
		group.Add(2)
		go func() {
			defer group.Done()
			if _, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, view); err != nil {
				failures <- err
			}
		}()
		go func() {
			defer group.Done()
			if err := fixture.store.QueueSagaLayer(ctx, fixture.projectID, view); err != nil {
				failures <- err
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("a queue at once refused: %v", err)
	}
	if cards, layers := waitingOf(jobs.KindChapterCards), waitingOf(jobs.KindSagaLayer); cards != 1 || layers != 1 {
		t.Fatalf("the queues at once left %d cards Jobs and %d layer Jobs waiting; want one each", cards, layers)
	}
}
