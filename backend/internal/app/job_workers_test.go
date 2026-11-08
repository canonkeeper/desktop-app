package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
)

// Each pool runs its own workers until shutdown: chat turns do not wait behind imports.
func TestJobWorkerPoolRunsEveryWorkerUntilShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeImportWorkerStoreForAPI{claimed: true}
	done := startJobWorkerPool(ctx, store, []jobworker.Handler{modelHandlerForJobWorkersTest{kind: jobs.KindResearchChatTurn}}, 6, "chat", log.New(io.Discard, "", 0))
	if len(done) != 6 {
		t.Fatalf("chat workers = %d, want %d", len(done), 6)
	}
	for _, finished := range done {
		<-finished
	}
}

// Indexed text without cards gets its cards Jobs once at start, before any reindex.
func TestMissingCardsCatchUpRunsOnceAtStart(t *testing.T) {
	queue := &countingMissingCardsQueue{}
	<-startCatchUp(context.Background(), queue, "chapter cards", "cards_catch_up_failed", log.New(io.Discard, "", 0))
	if queue.runs != 1 {
		t.Fatalf("catch-up runs = %d, want one at start", queue.runs)
	}
}

type countingMissingCardsQueue struct {
	runs int
	err  error
}

func (queue *countingMissingCardsQueue) Execute(context.Context) (int, error) {
	queue.runs++
	return 1, queue.err
}

// A failed catch-up names its cause class, so a lost database grant is visible in the log.
func TestMissingCardsCatchUpLogsFailureCause(t *testing.T) {
	var output bytes.Buffer
	queue := &countingMissingCardsQueue{err: fmt.Errorf("queue cards: %w", catchUpSQLStateError{})}

	<-startCatchUp(context.Background(), queue, "chapter cards", "cards_catch_up_failed", log.New(&output, "", 0))

	got := output.String()
	if !strings.Contains(got, `"event":"cards_catch_up_failed"`) || !strings.Contains(got, `"errorClass":"database"`) ||
		!strings.Contains(got, `"errorCause":"42501"`) || strings.Contains(got, "SECRET_") {
		t.Fatalf("catch-up failure log = %q", got)
	}
}

type catchUpSQLStateError struct{}

func (catchUpSQLStateError) Error() string    { return "SECRET_SQL_DETAIL" }
func (catchUpSQLStateError) SQLState() string { return "42501" }

func TestInstallableWorkerKindsAreClosedAndComplete(t *testing.T) {
	workerKinds := make(map[jobs.Kind]bool)
	for _, kind := range jobs.CurrentKinds() {
		if jobs.IsWorkerKind(kind) {
			workerKinds[kind] = true
		}
	}
	if len(workerKinds) != len(jobs.CurrentKinds()) {
		t.Fatalf("worker kinds = %v", workerKinds)
	}
	jobworker.NewWorker(
		workerJobStore{},
		[]jobworker.Handler{modelHandlerForJobWorkersTest{kind: jobs.KindResearchChatTurn}},
		log.New(io.Discard, "", 0),
	)
}

type modelHandlerForJobWorkersTest struct{ kind jobs.Kind }

func (handler modelHandlerForJobWorkersTest) Kind() jobs.Kind {
	return handler.kind
}

func (modelHandlerForJobWorkersTest) HandleResult(context.Context, jobs.Job) (jobworker.HandlerResult, error) {
	return jobworker.HandlerResult{}, nil
}

func (modelHandlerForJobWorkersTest) HandleCanceledResult(context.Context, jobs.Job) (jobworker.HandlerResult, error) {
	return jobworker.HandlerResult{}, nil
}

func (modelHandlerForJobWorkersTest) HandleFailedResult(context.Context, jobs.Job) (jobworker.HandlerResult, error) {
	return jobworker.HandlerResult{}, nil
}
