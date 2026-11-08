package jobworker

import (
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

var workerTestLogger = log.New(io.Discard, "", 0)

func TestWorkerRunsModelJobsToConfirmedTerminal(t *testing.T) {
	for _, kind := range []jobs.Kind{
		jobs.KindResearchChatTurn,
		jobs.KindChapterCards,
	} {
		t.Run(string(kind), func(t *testing.T) {
			store := newFakeWorkerStore(jobForWorkerTest("job-1", kind))
			handler := &committedHandlerForTest{kind: kind, store: store}
			worker := NewWorker(store, []Handler{handler}, workerTestLogger)

			ran, err := worker.RunOnce(context.Background())
			if err != nil || !ran {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
			}
		})
	}
}

func TestWorkerRunOnceDoesNotClaimSameQueuedJobTwice(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("first RunOnce() ran = false, want true")
	}

	ran, err = worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if ran {
		t.Fatal("second RunOnce() ran = true, want false after first worker claimed the only job")
	}
	if len(handler.handled) != 1 {
		t.Fatalf("handler handled %d jobs, want 1", len(handler.handled))
	}
	if store.jobs["job-1"].Status != jobs.StatusSucceeded {
		t.Fatalf("job status = %q, want %q", store.jobs["job-1"].Status, jobs.StatusSucceeded)
	}
}

func TestWorkerRunOnceMarksSucceededAfterHandlerSuccess(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(handler.handled) != 1 {
		t.Fatalf("handler handled %d jobs, want 1", len(handler.handled))
	}
	handled := handler.handled[0]
	if handled.Status != jobs.StatusRunning {
		t.Fatalf("handler saw status = %q, want %q", handled.Status, jobs.StatusRunning)
	}
	if handled.Progress == nil || *handled.Progress != 0.75 {
		t.Fatalf("handler saw progress = %v, want product payload preserved by claim", handled.Progress)
	}
	if store.jobs["job-1"].Status != jobs.StatusSucceeded {
		t.Fatalf("job status = %q, want committed success", store.jobs["job-1"].Status)
	}
}

func TestWorkerRunOnceRetryRequeuesImmediately(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	job.RetryCount = 0
	job.MaxAttempts = 3
	store := newFakeWorkerStore(job)
	handler := &committedHandlerForTest{kind: jobs.KindImport, handleErr: ErrRetryableHandler, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	got := store.jobs["job-1"]
	if got.Status != jobs.StatusQueued || got.RetryCount != 1 {
		t.Fatalf("requeued job = %#v, want queued retry_count=1", got)
	}
	if got.NextAttemptAt != workerTestNow().Add(15*time.Second) {
		t.Fatalf("next_attempt_at = %v, want %v", got.NextAttemptAt, workerTestNow().Add(15*time.Second))
	}
	if got.WorkerClaim.ClaimIncarnation != 2 {
		t.Fatalf("claim_incarnation = %d, want 2 after claim and immediate retry fence", got.WorkerClaim.ClaimIncarnation)
	}
}

func TestRetryDelayUsesSingleBoundedPolicy(t *testing.T) {
	for _, test := range []struct {
		retryCount int
		want       time.Duration
		wantErr    error
	}{
		{retryCount: 1, want: 15 * time.Second},
		{retryCount: 2, want: 60 * time.Second},
		{retryCount: 0, wantErr: jobs.ErrValidation},
		{retryCount: 3, wantErr: jobs.ErrValidation},
	} {
		got, err := jobs.RetryDelay(test.retryCount)
		if !errors.Is(err, test.wantErr) || got != test.want {
			t.Fatalf("RetryDelay(%d) = (%v, %v), want (%v, %v)", test.retryCount, got, err, test.want, test.wantErr)
		}
	}
}

func TestWorkerRunOnceSecondRetryUsesSixtySecondBackoffAndFencesOldClaim(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	job.RetryCount = 1
	job.MaxAttempts = jobs.MaxJobAttempts
	job.WorkerClaim.ClaimIncarnation = 4
	store := newFakeWorkerStore(job)
	handler := &committedHandlerForTest{kind: jobs.KindImport, handleErr: ErrRetryableHandler, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	oldClaim := handler.handled[0].WorkerClaim
	requeued := store.jobs[job.ID]
	if requeued.Status != jobs.StatusQueued || requeued.RetryCount != 2 ||
		requeued.NextAttemptAt != workerTestNow().Add(60*time.Second) ||
		requeued.WorkerClaim.ClaimIncarnation != oldClaim.ClaimIncarnation+1 {
		t.Fatalf("requeued job = %#v, want second bounded retry with immediate fence", requeued)
	}
	if err := store.HeartbeatWorkerClaim(context.Background(), oldClaim); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("old claim heartbeat error = %v, want ErrConflict", err)
	}
	decision, err := store.ClaimNextQueuedJobForWorker(context.Background(), []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatalf("next claim error = %v", err)
	}
	if decision.Claim.ClaimIncarnation != requeued.WorkerClaim.ClaimIncarnation+1 ||
		store.jobs[job.ID].Subject != job.Subject {
		t.Fatalf("next claim/job = %#v / %#v, want one more incarnation and immutable subject", decision, store.jobs[job.ID])
	}
}

func TestWorkerRunOnceRetainedRetryExhaustionUsesCommittedFailure(t *testing.T) {
	for _, kind := range []jobs.Kind{jobs.KindImport, jobs.KindCanonTextIndex} {
		t.Run(string(kind), func(t *testing.T) {
			job := jobForWorkerTest("job-1", kind)
			job.RetryCount = jobs.MaxJobAttempts - 1
			job.MaxAttempts = jobs.MaxJobAttempts
			store := newFakeWorkerStore(job)
			handler := &committedHandlerForTest{kind: kind, handleErr: ErrRetryableHandler, store: store}
			worker := NewWorker(store, []Handler{handler}, workerTestLogger)

			ran, err := worker.RunOnce(context.Background())
			if err != nil || !ran {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
			}
			if len(handler.failures) != 1 || handler.failures[0].Error == nil ||
				handler.failures[0].Error.Code != jobs.RetryExhaustedCode {
				t.Fatalf("committed failures = %#v, want retry exhausted", handler.failures)
			}
			got := store.jobs[job.ID]
			if got.Status != jobs.StatusFailed || got.RetryCount != jobs.MaxJobAttempts-1 ||
				got.WorkerClaim != handler.failures[0].WorkerClaim {
				t.Fatalf("exhausted job = %#v, want exact running claim sealed without requeue mutation", got)
			}
		})
	}
}

func TestWorkerRunOnceRetainedRetryExhaustionLetsExactCancellationWinAfterCommittedFailureError(t *testing.T) {
	tests := []struct {
		name       string
		kind       jobs.Kind
		failureErr error
	}{
		{name: "import retryable failure", kind: jobs.KindImport, failureErr: ErrRetryableHandler},
		{name: "text index conflict", kind: jobs.KindCanonTextIndex, failureErr: errors.New("text index conflict")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := jobForWorkerTest("job-1", tc.kind)
			job.RetryCount = jobs.MaxJobAttempts - 1
			job.MaxAttempts = jobs.MaxJobAttempts
			store := newFakeWorkerStore(job)
			handler := &committedHandlerForTest{
				kind:      tc.kind,
				handleErr: ErrRetryableHandler,
				store:     store,
				beforeFailure: func(job jobs.Job) {
					store.forceStatusForTest(job.ID, jobs.StatusCanceling)
				},
				failureErr: tc.failureErr,
			}
			worker := NewWorker(store, []Handler{handler}, workerTestLogger)

			ran, err := worker.RunOnce(context.Background())
			if err != nil || !ran {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
			}
			if len(handler.failures) != 1 || handler.failures[0].Error == nil ||
				handler.failures[0].Error.Code != jobs.RetryExhaustedCode {
				t.Fatalf("committed failures = %#v, want one retry-exhausted attempt", handler.failures)
			}
			got := store.jobs[job.ID]
			if len(handler.cancellations) != 1 || handler.cancellations[0].Status != jobs.StatusCanceling ||
				handler.cancellations[0].WorkerClaim != handler.failures[0].WorkerClaim ||
				got.Status != jobs.StatusCanceled || got.WorkerClaim != handler.failures[0].WorkerClaim {
				t.Fatalf("cancellations/job = %#v / %#v, want exact claim committed canceled", handler.cancellations, got)
			}
		})
	}
}

func TestWorkerRunOnceStaleRetainedExhaustionRetriesCommittedCleanup(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindCanonTextIndex)
	job.RetryCount = jobs.MaxJobAttempts - 1
	job.MaxAttempts = jobs.MaxJobAttempts
	store := newFakeWorkerStore(job)
	store.exhaustedStatus = jobs.StatusFailed
	handler := &committedHandlerForTest{kind: jobs.KindCanonTextIndex, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran || len(handler.failures) != 1 || handler.failures[0].Error == nil ||
		handler.failures[0].Error.Code != jobs.RetryExhaustedCode {
		t.Fatalf("RunOnce/failures = (%v, %v) / %#v, want committed retry exhausted cleanup", ran, err, handler.failures)
	}
}

func TestWorkerRunOnceRetainedHandlerFailureUsesCommittedOwnerPath(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	store := newFakeWorkerStore(job)
	handler := &committedHandlerForTest{kind: jobs.KindImport, handleErr: errors.New("invalid import"), store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	if len(handler.failures) != 1 || handler.failures[0].Error == nil ||
		handler.failures[0].Error.Code != HandlerFailedCode ||
		handler.failures[0].Error.Message != HandlerFailedMessage {
		t.Fatalf("committed failures = %#v, want owner-aware handler_failed only", handler.failures)
	}
}

func TestWorkerRunOnceRetainedHandlerFailurePreservesAllowlistedSafeCode(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	store := newFakeWorkerStore(job)
	handler := &committedHandlerForTest{
		kind: jobs.KindImport,
		handleErr: safeLifecycleError{
			code: "runtime.session_unclosed",
			raw:  "SECRET_RUNTIME_DETAIL",
		},
		store: store,
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran || len(handler.failures) != 1 || handler.failures[0].Error == nil {
		t.Fatalf("RunOnce/failures = (%v, %v) / %#v", ran, err, handler.failures)
	}
	jobError := handler.failures[0].Error
	if jobError.Code != "runtime.session_unclosed" || jobError.Message != HandlerFailedMessage ||
		strings.Contains(jobError.Message, "SECRET_") {
		t.Fatalf("terminal job error = %#v", jobError)
	}
}

func TestWorkerRunOnceRetryConflictDispatchesOnlyExactCancelingClaim(t *testing.T) {
	t.Run("same incarnation cancel", func(t *testing.T) {
		job := jobForWorkerTest("job-1", jobs.KindImport)
		store := newFakeWorkerStore(job)
		store.beforeRequeue = func(record *jobs.Job, _ jobs.RequeueWorkerClaimStoreInput) {
			record.Status = jobs.StatusCanceling
		}
		handler := &committedHandlerForTest{kind: jobs.KindImport, handleErr: ErrRetryableHandler, store: store}
		worker := NewWorker(store, []Handler{handler}, workerTestLogger)

		ran, err := worker.RunOnce(context.Background())
		if err != nil || !ran || len(handler.cancellations) != 1 || store.jobs[job.ID].Status != jobs.StatusCanceled {
			t.Fatalf("RunOnce/cancellations/job = (%v, %v) / %#v / %#v, want committed cancellation", ran, err, handler.cancellations, store.jobs[job.ID])
		}
	})

	t.Run("stolen claim", func(t *testing.T) {
		job := jobForWorkerTest("job-1", jobs.KindImport)
		store := newFakeWorkerStore(job)
		store.beforeRequeue = func(record *jobs.Job, _ jobs.RequeueWorkerClaimStoreInput) {
			record.WorkerClaim.ClaimIncarnation++
		}
		handler := &committedHandlerForTest{kind: jobs.KindImport, handleErr: ErrRetryableHandler, store: store}
		worker := NewWorker(store, []Handler{handler}, workerTestLogger)

		ran, err := worker.RunOnce(context.Background())
		if !ran || !errors.Is(err, jobs.ErrConflict) || len(handler.failures) != 0 || len(handler.cancellations) != 0 {
			t.Fatalf("RunOnce/terminal calls = (%v, %v) / %#v / %#v, want conflict with zero writes", ran, err, handler.failures, handler.cancellations)
		}
	})
}

func TestWorkerRunOnceRetryInfrastructureErrorLeavesRunningClaimUnchanged(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	store := newFakeWorkerStore(job)
	injected := errors.New("database unavailable")
	store.requeueErr = injected
	handler := &committedHandlerForTest{kind: jobs.KindImport, handleErr: ErrRetryableHandler, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	got := store.jobs[job.ID]
	if !ran || !errors.Is(err, injected) || got.Status != jobs.StatusRunning || got.RetryCount != 0 ||
		got.WorkerClaim.ClaimIncarnation != 1 {
		t.Fatalf("RunOnce/job = (%v, %v) / %#v, want unchanged running claim", ran, err, got)
	}
}

func TestWorkerRunOnceRetryInfrastructureErrorPreservesHandlerClassification(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindChapterCards)
	store := newFakeWorkerStore(job)
	store.requeueErr = jobs.ErrModelRetryFenceRequired
	handler := &committedHandlerForTest{
		kind: jobs.KindChapterCards,
		handleErr: errors.Join(
			ErrRetryableHandler,
			safeLifecycleError{code: "runtime.thread_start_failed", raw: "provider detail"},
		),
		store: store,
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if !ran || !errors.Is(err, jobs.ErrModelRetryFenceRequired) || jobAttemptErrorCode(err) != "runtime.thread_start_failed" {
		t.Fatalf("RunOnce() = (%v, %v), code=%q", ran, err, jobAttemptErrorCode(err))
	}
	if got := store.jobs[job.ID]; got.Status != jobs.StatusRunning || got.WorkerClaim.ClaimIncarnation != 1 {
		t.Fatalf("job = %#v, want unchanged running claim", got)
	}
}

func TestWorkerRunOnceMarksFailedWithBoundedSafeError(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{
		kind:      jobs.KindImport,
		handleErr: errors.New("raw payload contained secret-token-123"),
		store:     store,
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(handler.failures) != 1 || handler.failures[0].Error == nil {
		t.Fatalf("committed failures = %#v, want one safe jobs.Error", handler.failures)
	}
	jobError := handler.failures[0].Error
	if jobError.Code != "worker.handler_failed" {
		t.Fatalf("failed code = %q, want stable worker.handler_failed", jobError.Code)
	}
	if jobError.Message == "" || len(jobError.Message) > 128 {
		t.Fatalf("failed message = %q, want non-empty bounded message", jobError.Message)
	}
	if strings.Contains(jobError.Message, "secret-token-123") || strings.Contains(jobError.Message, "raw payload") {
		t.Fatalf("failed message leaked handler error: %q", jobError.Message)
	}
}

func TestWorkerRunOnceDoesNotMarkSucceededAfterContextCancellation(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	ctx, cancel := context.WithCancel(context.Background())
	handler := &committedHandlerForTest{
		kind:  jobs.KindImport,
		store: store,
		onHandle: func(ctx context.Context, _ jobs.Job) error {
			cancel()
			if ctx.Err() == nil {
				t.Fatal("handler canceled context but ctx.Err() is nil")
			}
			return nil
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(ctx)
	if !ran {
		t.Fatal("RunOnce() ran = false, want true after claiming job")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce() error = %v, want context.Canceled", err)
	}
	if store.jobs["job-1"].Status != jobs.StatusQueued {
		t.Fatalf("job status = %q, want returned to the queue after cancellation, not succeeded", store.jobs["job-1"].Status)
	}
}

func TestWorkerRunOnceCompletesClaimedCancelingJobWithoutRunningHandler(t *testing.T) {
	canceling := jobForWorkerTest("job-1", jobs.KindImport)
	canceling.Status = jobs.StatusCanceling
	cancelingProgress := 0.5
	canceling.Progress = &cancelingProgress
	store := newFakeWorkerStore(canceling)
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(handler.handled) != 0 {
		t.Fatalf("handler handled jobs = %#v, want no handler execution for claimed canceling job", handler.handled)
	}
	if store.jobs["job-1"].Status != jobs.StatusCanceled {
		t.Fatalf("job status = %q, want %q", store.jobs["job-1"].Status, jobs.StatusCanceled)
	}
	if len(handler.cancellations) != 1 || handler.cancellations[0].ID != "job-1" {
		t.Fatalf("cancellation handler calls = %#v, want canceled job-1", handler.cancellations)
	}
}

func TestWorkerRunRetriesTransientRunOnceErrorAndProcessesLaterJob(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	store.claimErrors = []error{errors.New("temporary claim failure")}
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.idleDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)

	go func() {
		done <- worker.Run(ctx)
	}()

	deadline := time.After(time.Second)
	for store.statusForTest("job-1") != jobs.StatusSucceeded {
		select {
		case err := <-done:
			t.Fatalf("Run() returned before retrying transient error: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for worker to retry transient error and process job")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error after cancellation = %v, want context.Canceled", err)
	}
	if store.claimAttempts < 2 {
		t.Fatalf("claim attempts = %d, want retry after transient error", store.claimAttempts)
	}
	if len(handler.handled) != 1 || handler.handled[0].ID != "job-1" {
		t.Fatalf("handled jobs = %#v, want job-1 after retry", handler.handled)
	}
}

func TestWorkerRunOnceHeartbeatsWhileHandlerRunsAndStopsBeforeTerminalCAS(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	store.heartbeatSignal = make(chan struct{}, 1)
	handler := &committedHandlerForTest{
		kind:  jobs.KindImport,
		store: store,
		onHandle: func(ctx context.Context, _ jobs.Job) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-store.heartbeatSignal:
				return nil
			}
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	heartbeatsAtTerminal := store.heartbeatCountForTest()
	if heartbeatsAtTerminal < 1 {
		t.Fatal("handler completed without a worker heartbeat")
	}
	time.Sleep(5 * time.Millisecond)
	if got := store.heartbeatCountForTest(); got != heartbeatsAtTerminal {
		t.Fatalf("heartbeats continued after terminal CAS: before=%d after=%d", heartbeatsAtTerminal, got)
	}
}

func TestWorkerRunOnceCancelsHandlerWhenHeartbeatLosesClaim(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	store.heartbeatErrs = []error{jobs.ErrConflict}
	handler := &committedHandlerForTest{
		kind:  jobs.KindImport,
		store: store,
		onHandle: func(ctx context.Context, _ jobs.Job) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(context.Background())
	if !ran || !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("RunOnce() = (%v, %v), want claimed job and ErrConflict", ran, err)
	}
}

func TestWorkerRunOnceHeartbeatConflictFinishesExactCancelingClaim(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	store := newFakeWorkerStore(job)
	store.heartbeatErrs = []error{jobs.ErrConflict}
	store.beforeHeartbeat = func(claim jobs.WorkerClaim) {
		current := store.jobs[claim.JobID]
		current.Status = jobs.StatusCanceling
		store.jobs[claim.JobID] = current
	}
	handlerStopped := make(chan struct{})
	handler := &committedHandlerForTest{
		kind:  jobs.KindImport,
		store: store,
		onHandle: func(ctx context.Context, _ jobs.Job) error {
			<-ctx.Done()
			close(handlerStopped)
			return ctx.Err()
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want exact canceling claim completed", ran, err)
	}
	select {
	case <-handlerStopped:
	default:
		t.Fatal("owner cleanup ran before handler stopped")
	}
	if len(handler.cancellations) != 1 || handler.cancellations[0].WorkerClaim != store.jobs[job.ID].WorkerClaim ||
		handler.cancellations[0].Status != jobs.StatusCanceling || store.jobs[job.ID].Status != jobs.StatusCanceled {
		t.Fatalf("cancellations/job = %#v / %#v, want exact committed owner cleanup", handler.cancellations, store.jobs[job.ID])
	}
}

func TestWorkerRunOnceRequiresLaunchCleanupBeforeCancellationTerminalizes(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindChapterCards))
	store.heartbeatErrs = []error{jobs.ErrConflict}
	store.beforeHeartbeat = func(claim jobs.WorkerClaim) {
		current := store.jobs[claim.JobID]
		current.Status = jobs.StatusCanceling
		store.jobs[claim.JobID] = current
	}
	handler := &launchCleanupHandlerForTest{store: store, cleanupDone: make(chan struct{})}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want exact canceling claim completed", ran, err)
	}
	if got := store.jobs["job-1"].Status; got != jobs.StatusCanceled {
		t.Fatalf("job status = %q, want canceled", got)
	}
}

type launchCleanupHandlerForTest struct {
	store       *fakeWorkerStore
	cleanupDone chan struct{}
}

func (h *launchCleanupHandlerForTest) Kind() jobs.Kind { return jobs.KindChapterCards }

func (h *launchCleanupHandlerForTest) HandleResult(ctx context.Context, _ jobs.Job) (HandlerResult, error) {
	<-ctx.Done()
	close(h.cleanupDone)
	return HandlerResult{}, ctx.Err()
}

func (h *launchCleanupHandlerForTest) HandleCanceledResult(_ context.Context, job jobs.Job) (HandlerResult, error) {
	select {
	case <-h.cleanupDone:
	default:
		return HandlerResult{}, ErrTerminalProtocolInvariant
	}
	h.store.forceStatusForTest(job.ID, jobs.StatusCanceled)
	return HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (*launchCleanupHandlerForTest) HandleFailedResult(context.Context, jobs.Job) (HandlerResult, error) {
	return HandlerResult{}, ErrTerminalProtocolInvariant
}

func TestWorkerRunOnceHeartbeatConflictRejectsNonExactCancellationTargets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]jobs.Job, jobs.WorkerClaim)
	}{
		{
			name: "same claim still running",
			mutate: func(map[string]jobs.Job, jobs.WorkerClaim) {
			},
		},
		{
			name: "stolen claim",
			mutate: func(records map[string]jobs.Job, claim jobs.WorkerClaim) {
				current := records[claim.JobID]
				current.Status = jobs.StatusCanceling
				current.WorkerClaim.ClaimIncarnation++
				records[claim.JobID] = current
			},
		},
		{
			name: "missing job",
			mutate: func(records map[string]jobs.Job, claim jobs.WorkerClaim) {
				delete(records, claim.JobID)
			},
		},
		{
			name: "foreign project",
			mutate: func(records map[string]jobs.Job, claim jobs.WorkerClaim) {
				current := records[claim.JobID]
				current.Status = jobs.StatusCanceling
				current.ProjectID = "project-2"
				records[claim.JobID] = current
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := jobForWorkerTest("job-1", jobs.KindImport)
			store := newFakeWorkerStore(job)
			store.heartbeatErrs = []error{jobs.ErrConflict}
			store.beforeHeartbeat = func(claim jobs.WorkerClaim) {
				test.mutate(store.jobs, claim)
			}
			handlerStopped := make(chan struct{})
			handler := &committedHandlerForTest{
				kind:  jobs.KindImport,
				store: store,
				onHandle: func(ctx context.Context, _ jobs.Job) error {
					<-ctx.Done()
					close(handlerStopped)
					return ctx.Err()
				},
			}
			worker := NewWorker(store, []Handler{handler}, workerTestLogger)
			worker.heartbeatInterval = time.Millisecond

			ran, err := worker.RunOnce(context.Background())
			if !ran || !errors.Is(err, jobs.ErrConflict) {
				t.Fatalf("RunOnce() = (%v, %v), want claimed job and ErrConflict", ran, err)
			}
			select {
			case <-handlerStopped:
			default:
				t.Fatal("RunOnce returned before handler stopped")
			}
			if len(handler.cancellations) != 0 || len(handler.failures) != 0 {
				t.Fatalf("owner cancellations/failures = %#v / %#v, want zero mutation", handler.cancellations, handler.failures)
			}
		})
	}
}

func TestWorkerRunOnceAcceptsExactCommittedTerminalWithoutSecondWrite(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &resultHandlerForTest{
		kind: jobs.KindImport,
		onHandleResult: func(_ context.Context, job jobs.Job) (HandlerResult, error) {
			store.forceStatusForTest(job.ID, jobs.StatusSucceeded)
			return HandlerResult{Status: jobs.StatusSucceeded}, nil
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	ran, err = worker.RunOnce(context.Background())
	if err != nil || ran {
		t.Fatalf("duplicate RunOnce() = (%v, %v), want terminal job not claimable", ran, err)
	}
}

func TestWorkerRunOnceAcceptsExactTerminalCommittedBeforeHandlerError(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{
		kind:      jobs.KindImport,
		handleErr: jobs.ErrConflict,
		onHandle: func(_ context.Context, job jobs.Job) error {
			store.forceStatusForTest(job.ID, jobs.StatusSucceeded)
			return nil
		},
		store: store,
	}
	var output strings.Builder
	worker := NewWorker(store, []Handler{handler}, log.New(&output, "", 0))

	ran, err := worker.RunOnce(context.Background())
	logged := output.String()
	if err != nil || !ran || store.statusForTest("job-1") != jobs.StatusSucceeded || len(handler.failures) != 0 ||
		!strings.Contains(logged, `"event":"job_finished"`) ||
		!strings.Contains(logged, `"terminalStatus":"succeeded"`) || strings.Contains(logged, `"event":"job_attempt_failed"`) {
		t.Fatalf("RunOnce/job/failures/log = (%v, %v)/%q/%d/%q, want committed success without failure transition",
			ran, err, store.statusForTest("job-1"), len(handler.failures), logged)
	}
}

// The cancel operation of a running chat turn interrupts the turn and commits canceled itself; the
// interrupted handler fails meanwhile, and the worker's own failure or cancel write then loses to
// that commit. The canceled Job under the same claim is the outcome of the attempt, not a loop error.
func TestWorkerRunOnceAcceptsCancellationCommittedByTheCancelOperationAfterHandlerError(t *testing.T) {
	interrupted := errors.New("turn interrupted")
	lost := errors.New("job is no longer running")
	tests := []struct {
		name          string
		beforeFailure func(*fakeWorkerStore, jobs.Job)
		failureErr    error
		beforeCancel  func(*fakeWorkerStore, jobs.Job)
		cancelErr     error
	}{
		{
			name: "commit lands before the failure write",
			beforeFailure: func(store *fakeWorkerStore, job jobs.Job) {
				store.forceStatusForTest(job.ID, jobs.StatusCanceled)
			},
			failureErr: lost,
		},
		{
			name:       "commit lands before the cancel write",
			failureErr: lost,
			beforeCancel: func(store *fakeWorkerStore, job jobs.Job) {
				store.forceStatusForTest(job.ID, jobs.StatusCanceled)
			},
			cancelErr: lost,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
			handler := &committedHandlerForTest{
				kind:      jobs.KindImport,
				handleErr: interrupted,
				onHandle: func(_ context.Context, job jobs.Job) error {
					store.forceStatusForTest(job.ID, jobs.StatusCanceling)
					return nil
				},
				failureErr: tc.failureErr,
				cancelErr:  tc.cancelErr,
				store:      store,
			}
			if tc.beforeFailure != nil {
				handler.beforeFailure = func(job jobs.Job) { tc.beforeFailure(store, job) }
			}
			if tc.beforeCancel != nil {
				handler.beforeCancel = func(job jobs.Job) { tc.beforeCancel(store, job) }
			}
			var output strings.Builder
			worker := NewWorker(store, []Handler{handler}, log.New(&output, "", 0))

			ran, err := worker.RunOnce(context.Background())
			logged := output.String()
			if err != nil || !ran || store.statusForTest("job-1") != jobs.StatusCanceled ||
				!strings.Contains(logged, `"terminalStatus":"canceled"`) || strings.Contains(logged, `"event":"job_attempt_failed"`) {
				t.Fatalf("RunOnce/job/log = (%v, %v)/%q/%q, want the committed cancellation as the outcome",
					ran, err, store.statusForTest("job-1"), logged)
			}
		})
	}
}

func TestWorkerRunOnceDoesNotAcceptHandlerErrorFromStaleClaim(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{
		kind:      jobs.KindImport,
		handleErr: jobs.ErrConflict,
		onHandle: func(_ context.Context, job jobs.Job) error {
			store.reclaimForTest(job.ID)
			return nil
		},
		store: store,
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if !ran || !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("RunOnce() = (%v, %v), want stale claim conflict", ran, err)
	}
}

func TestWorkerRunOnceVerifiesCommittedQueuedSuccessorClaim(t *testing.T) {
	tests := []struct {
		name            string
		commit          bool
		claimAdvance    int64
		successorStatus jobs.Status
		wantErr         error
	}{
		{name: "queued i+1", commit: true, claimAdvance: 1, successorStatus: jobs.StatusQueued},
		{name: "canceled i+1", commit: true, claimAdvance: 1, successorStatus: jobs.StatusCanceled},
		{name: "missing successor", wantErr: jobs.ErrConflict},
		{name: "queued i+2", commit: true, claimAdvance: 2, successorStatus: jobs.StatusQueued, wantErr: jobs.ErrConflict},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
			handler := &resultHandlerForTest{
				kind: jobs.KindImport,
				onHandleResult: func(_ context.Context, job jobs.Job) (HandlerResult, error) {
					if test.commit {
						store.commitSuccessorForTest(job.ID, test.claimAdvance, test.successorStatus)
					}
					return HandlerResult{Status: jobs.StatusQueued}, nil
				},
			}
			worker := NewWorker(store, []Handler{handler}, workerTestLogger)

			ran, err := worker.RunOnce(context.Background())
			if !ran || !errors.Is(err, test.wantErr) {
				t.Fatalf("RunOnce() = (%v, %v), want claimed job and error %v", ran, err, test.wantErr)
			}
		})
	}
}

func TestWorkerRunOnceAcceptsHeartbeatConflictWithMatchingCommittedTerminal(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	store.heartbeatSignal = make(chan struct{}, 1)
	handler := &resultHandlerForTest{
		kind: jobs.KindImport,
		onHandleResult: func(ctx context.Context, job jobs.Job) (HandlerResult, error) {
			store.forceStatusForTest(job.ID, jobs.StatusSucceeded)
			select {
			case <-ctx.Done():
				return HandlerResult{}, ctx.Err()
			case <-store.heartbeatSignal:
				return HandlerResult{Status: jobs.StatusSucceeded}, nil
			}
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want matching committed terminal accepted", ran, err)
	}
}

func TestWorkerRunOnceRejectsLyingOrMismatchedCommittedMarker(t *testing.T) {
	for _, test := range []struct {
		name       string
		storeState jobs.Status
		marker     jobs.Status
	}{
		{name: "no commit", storeState: jobs.StatusRunning, marker: jobs.StatusSucceeded},
		{name: "mismatched status", storeState: jobs.StatusFailed, marker: jobs.StatusSucceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
			handler := &resultHandlerForTest{
				kind: jobs.KindImport,
				onHandleResult: func(_ context.Context, job jobs.Job) (HandlerResult, error) {
					if test.storeState != jobs.StatusRunning {
						store.forceStatusForTest(job.ID, test.storeState)
					}
					return HandlerResult{Status: test.marker}, nil
				},
			}
			worker := NewWorker(store, []Handler{handler}, workerTestLogger)

			ran, err := worker.RunOnce(context.Background())
			if !ran || !errors.Is(err, ErrTerminalProtocolInvariant) {
				t.Fatalf("RunOnce() = (%v, %v), want invariant failure", ran, err)
			}
		})
	}
}

func TestWorkerRunOnceRejectsStaleClaimBeforeTerminalCAS(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &resultHandlerForTest{
		kind: jobs.KindImport,
		onHandleResult: func(_ context.Context, job jobs.Job) (HandlerResult, error) {
			store.reclaimForTest(job.ID)
			return HandlerResult{Status: jobs.StatusSucceeded}, nil
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	ran, err := worker.RunOnce(context.Background())
	if !ran || !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("RunOnce() = (%v, %v), want stale claim conflict", ran, err)
	}
}

type fakeWorkerStore struct {
	mu              sync.Mutex
	jobs            map[string]jobs.Job
	claimErrors     []error
	claimAttempts   int
	heartbeats      int
	heartbeatErrs   []error
	heartbeatSignal chan struct{}
	beforeHeartbeat func(jobs.WorkerClaim)
	beforeLoad      func(jobs.WorkerClaim)
	beforeRequeue   func(*jobs.Job, jobs.RequeueWorkerClaimStoreInput)
	exhaustedStatus jobs.Status
	requeueErr      error
	releases        int
}

func newFakeWorkerStore(records ...jobs.Job) *fakeWorkerStore {
	store := &fakeWorkerStore{jobs: make(map[string]jobs.Job)}
	for _, record := range records {
		store.jobs[record.ID] = record
	}
	return store
}

func (s *fakeWorkerStore) ClaimNextQueuedJobForWorker(ctx context.Context, kinds []jobs.Kind) (jobs.WorkerClaimDecision, error) {
	if err := ctx.Err(); err != nil {
		return jobs.WorkerClaimDecision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimAttempts++
	if len(s.claimErrors) > 0 {
		err := s.claimErrors[0]
		s.claimErrors = s.claimErrors[1:]
		return jobs.WorkerClaimDecision{}, err
	}

	var claimedID string
	for id, record := range s.jobs {
		if !slices.Contains(kinds, record.Kind) || (record.Status != jobs.StatusQueued && record.Status != jobs.StatusCanceling) {
			continue
		}
		if claimedID == "" || record.CreatedAt.Before(s.jobs[claimedID].CreatedAt) ||
			(record.CreatedAt.Equal(s.jobs[claimedID].CreatedAt) && record.ID < s.jobs[claimedID].ID) {
			claimedID = id
		}
	}
	if claimedID == "" {
		return jobs.WorkerClaimDecision{}, jobs.ErrNotFound
	}
	claimed := s.jobs[claimedID]
	if claimed.Status == jobs.StatusQueued {
		claimed.Status = jobs.StatusRunning
	}
	claim := jobs.WorkerClaim{
		ProjectID:        claimed.ProjectID,
		JobID:            claimed.ID,
		ClaimIncarnation: claimed.WorkerClaim.ClaimIncarnation + 1,
	}
	claimed.WorkerClaim = claim
	s.jobs[claimedID] = claimed
	return jobs.WorkerClaimDecision{Claim: claim, ExhaustedStatus: s.exhaustedStatus}, nil
}

func (s *fakeWorkerStore) LoadJobForWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	if err := ctx.Err(); err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beforeLoad != nil {
		s.beforeLoad(claim)
	}
	record, ok := s.jobs[claim.JobID]
	if !ok || record.ProjectID != claim.ProjectID || record.WorkerClaim != claim {
		return jobs.Job{}, jobs.ErrNotFound
	}
	return record, nil
}

func TestHeartbeatLoopReadsTerminalAfterHeartbeatContextCancellation(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	decision, err := store.ClaimNextQueuedJobForWorker(context.Background(), []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatal(err)
	}
	job := jobForWorkerTest("job-1", jobs.KindImport)
	job.WorkerClaim = decision.Claim
	store.forceStatusForTest("job-1", jobs.StatusSucceeded)
	store.heartbeatErrs = []error{jobs.ErrConflict}
	enteredLoad := make(chan struct{})
	store.beforeLoad = func(jobs.WorkerClaim) {
		select {
		case <-enteredLoad:
		default:
			close(enteredLoad)
			<-store.heartbeatSignal
		}
	}
	store.heartbeatSignal = make(chan struct{})
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	defer cancelHeartbeat()
	done := make(chan heartbeatObservation, 1)

	worker := NewWorker(store, []Handler{&committedHandlerForTest{kind: jobs.KindImport, store: store}}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond
	cancelHandler := func() {}
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	go worker.heartbeatLoop(heartbeatCtx, parent, cancelHandler, job, done)

	select {
	case <-enteredLoad:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal recovery load")
	}
	cancelHeartbeat()
	store.heartbeatSignal <- struct{}{}

	select {
	case observation := <-done:
		if observation.terminalJob.Status != jobs.StatusSucceeded {
			t.Fatalf("heartbeat observation = %#v, want terminal succeeded job", observation)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not finish")
	}
}

func (s *fakeWorkerStore) HeartbeatWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats++
	if s.beforeHeartbeat != nil {
		s.beforeHeartbeat(claim)
	}
	if s.heartbeatSignal != nil {
		select {
		case s.heartbeatSignal <- struct{}{}:
		default:
		}
	}
	if len(s.heartbeatErrs) > 0 {
		err := s.heartbeatErrs[0]
		s.heartbeatErrs = s.heartbeatErrs[1:]
		return err
	}
	record, ok := s.jobs[claim.JobID]
	if !ok || record.ProjectID != claim.ProjectID || record.WorkerClaim != claim || record.Status != jobs.StatusRunning {
		return jobs.ErrConflict
	}
	return nil
}

func (s *fakeWorkerStore) RequeueWorkerClaim(ctx context.Context, input jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error) {
	if err := ctx.Err(); err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requeueErr != nil {
		return jobs.Job{}, s.requeueErr
	}
	record, ok := s.jobs[input.Claim.JobID]
	if !ok || record.ProjectID != input.Claim.ProjectID || record.WorkerClaim != input.Claim ||
		record.Kind != input.Kind || record.Status != jobs.StatusRunning || record.RetryCount != input.ExpectedRetryCount {
		return jobs.Job{}, jobs.ErrConflict
	}
	if s.beforeRequeue != nil {
		s.beforeRequeue(&record, input)
	}
	if record.Status != jobs.StatusRunning || record.WorkerClaim != input.Claim {
		s.jobs[input.Claim.JobID] = record
		return jobs.Job{}, jobs.ErrConflict
	}
	if input.ExpectedRetryCount == jobs.MaxJobAttempts-1 {
		return jobs.Job{}, jobs.ErrRetryExhausted
	}
	delay, err := jobs.RetryDelay(input.ExpectedRetryCount + 1)
	if err != nil {
		return jobs.Job{}, err
	}
	record.Status = jobs.StatusQueued
	record.RetryCount++
	record.NextAttemptAt = workerTestNow().Add(delay)
	record.WorkerClaim.ClaimIncarnation++
	s.jobs[input.Claim.JobID] = record
	return record, nil
}

func (s *fakeWorkerStore) ReleaseWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	if err := ctx.Err(); err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases++
	record, ok := s.jobs[claim.JobID]
	if !ok || record.ProjectID != claim.ProjectID || record.WorkerClaim != claim || record.Status != jobs.StatusRunning {
		return jobs.Job{}, jobs.ErrConflict
	}
	record.Status = jobs.StatusQueued
	record.WorkerClaim.ClaimIncarnation++
	s.jobs[claim.JobID] = record
	return record, nil
}

func (s *fakeWorkerStore) forceStatusForTest(jobID string, status jobs.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.jobs[jobID]
	record.Status = status
	s.jobs[jobID] = record
}

func (s *fakeWorkerStore) reclaimForTest(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.jobs[jobID]
	record.WorkerClaim.ClaimIncarnation++
	s.jobs[jobID] = record
}

func (s *fakeWorkerStore) commitSuccessorForTest(jobID string, claimAdvance int64, status jobs.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.jobs[jobID]
	record.WorkerClaim.ClaimIncarnation += claimAdvance
	record.Status = status
	s.jobs[jobID] = record
}

func (s *fakeWorkerStore) heartbeatCountForTest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats
}

func (s *fakeWorkerStore) statusForTest(jobID string) jobs.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[jobID].Status
}

type committedHandlerForTest struct {
	kind          jobs.Kind
	handleErr     error
	onHandle      func(context.Context, jobs.Job) error
	beforeFailure func(jobs.Job)
	failureErr    error
	beforeCancel  func(jobs.Job)
	cancelErr     error
	store         *fakeWorkerStore
	handled       []jobs.Job
	failures      []jobs.Job
	cancellations []jobs.Job
}

func (h *committedHandlerForTest) Kind() jobs.Kind { return h.kind }

func (h *committedHandlerForTest) HandleResult(ctx context.Context, job jobs.Job) (HandlerResult, error) {
	h.handled = append(h.handled, job)
	if h.onHandle != nil {
		if err := h.onHandle(ctx, job); err != nil {
			return HandlerResult{}, err
		}
	}
	if h.handleErr != nil {
		return HandlerResult{}, h.handleErr
	}
	if err := ctx.Err(); err != nil {
		return HandlerResult{}, err
	}
	h.store.forceStatusForTest(job.ID, jobs.StatusSucceeded)
	return HandlerResult{Status: jobs.StatusSucceeded}, nil
}

func (h *committedHandlerForTest) HandleFailedResult(_ context.Context, job jobs.Job) (HandlerResult, error) {
	h.failures = append(h.failures, job)
	if h.beforeFailure != nil {
		h.beforeFailure(job)
	}
	if h.failureErr != nil {
		return HandlerResult{}, h.failureErr
	}
	h.store.forceStatusForTest(job.ID, jobs.StatusFailed)
	return HandlerResult{Status: jobs.StatusFailed}, nil
}

func (h *committedHandlerForTest) HandleCanceledResult(_ context.Context, job jobs.Job) (HandlerResult, error) {
	h.cancellations = append(h.cancellations, job)
	if h.beforeCancel != nil {
		h.beforeCancel(job)
	}
	if h.cancelErr != nil {
		return HandlerResult{}, h.cancelErr
	}
	h.store.forceStatusForTest(job.ID, jobs.StatusCanceled)
	return HandlerResult{Status: jobs.StatusCanceled}, nil
}

type resultHandlerForTest struct {
	kind           jobs.Kind
	onHandleResult func(context.Context, jobs.Job) (HandlerResult, error)
}

func (h *resultHandlerForTest) Kind() jobs.Kind {
	return h.kind
}

func (h *resultHandlerForTest) HandleResult(ctx context.Context, job jobs.Job) (HandlerResult, error) {
	return h.onHandleResult(ctx, job)
}

func (*resultHandlerForTest) HandleCanceledResult(context.Context, jobs.Job) (HandlerResult, error) {
	return HandlerResult{}, nil
}

func (*resultHandlerForTest) HandleFailedResult(context.Context, jobs.Job) (HandlerResult, error) {
	return HandlerResult{}, nil
}

func jobForWorkerTest(id string, kind jobs.Kind) jobs.Job {
	progress := 0.75
	var subject jobs.Subject
	switch kind {
	case jobs.KindImport:
		subject = jobs.ImportSubject{Type: kind, ID: "project-1", ProjectID: "project-1"}
	case jobs.KindChapterCards:
		subject = jobs.ChapterCardsSubject{Type: kind, ProjectID: "project-1", SourceView: governance.SourceViewDraft}
	default:
		subject = jobs.CanonTextIndexSubject{Type: kind, ProjectID: "project-1", SourceView: governance.SourceViewDraft, CatalogRevision: 1}
	}
	return jobs.Job{
		ID:        id,
		ProjectID: "project-1",
		Kind:      kind,
		Status:    jobs.StatusQueued,
		Progress:  &progress,
		Subject:   subject,
		CanCancel: true,
		CreatedAt: workerTestNow(),
		UpdatedAt: workerTestNow(),
	}
}

func workerTestNow() time.Time {
	return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
}

// R1.2: a graceful stop returns the interrupted Job to the queue without spending an attempt.
func TestWorkerRunOnceReleasesClaimOnShutdown(t *testing.T) {
	job := jobForWorkerTest("job-1", jobs.KindChapterCards)
	job.RetryCount = 1
	store := newFakeWorkerStore(job)
	ctx, stop := context.WithCancel(context.Background())
	handler := &committedHandlerForTest{kind: jobs.KindChapterCards, store: store, onHandle: func(handlerCtx context.Context, _ jobs.Job) error {
		stop()
		<-handlerCtx.Done()
		return handlerCtx.Err()
	}}
	var logs strings.Builder
	worker := NewWorker(store, []Handler{handler}, log.New(&logs, "", 0))

	ran, err := worker.RunOnce(ctx)
	if !ran || !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce() = (%v, %v), want (true, context.Canceled)", ran, err)
	}
	got := store.jobs["job-1"]
	if got.Status != jobs.StatusQueued || got.RetryCount != 1 || got.WorkerClaim.ClaimIncarnation != 2 {
		t.Fatalf("released job = status %q retry %d claim %+v, want queued, retry 1, incarnation 2",
			got.Status, got.RetryCount, got.WorkerClaim)
	}
	if !strings.Contains(logs.String(), `"event":"job_requeued"`) || !strings.Contains(logs.String(), `"errorCode":"worker.shutdown"`) {
		t.Fatalf("logs = %s, want job_requeued with worker.shutdown", logs.String())
	}
}

// R1.2: a Job its handler finished before the stop stays as it is.
func TestWorkerRunOnceKeepsCommittedResultOnShutdown(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindChapterCards))
	ctx, stop := context.WithCancel(context.Background())
	handler := &resultHandlerForTest{kind: jobs.KindChapterCards, onHandleResult: func(_ context.Context, job jobs.Job) (HandlerResult, error) {
		store.forceStatusForTest(job.ID, jobs.StatusSucceeded)
		stop()
		return HandlerResult{Status: jobs.StatusSucceeded}, nil
	}}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)

	if _, err := worker.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce() error = %v, want context.Canceled", err)
	}
	if got := store.statusForTest("job-1"); got != jobs.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", got)
	}
}

// Quota spec UC12: a handler that gave its Job back to the queue (a cards portion) committed
// queued with the next claim incarnation; a heartbeat tick between that commit and the
// handler's return sees a conflict, which is the handler's outcome and not a lost claim.
func TestWorkerRunOnceAcceptsHeartbeatConflictAfterHandlerRequeue(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	store.heartbeatSignal = make(chan struct{}, 1)
	handler := &resultHandlerForTest{
		kind: jobs.KindImport,
		onHandleResult: func(ctx context.Context, job jobs.Job) (HandlerResult, error) {
			store.commitSuccessorForTest(job.ID, 1, jobs.StatusQueued)
			<-store.heartbeatSignal
			// Let the heartbeat observe the conflict before the handler returns.
			deadline := time.Now().Add(time.Second)
			for ctx.Err() == nil && time.Now().Before(deadline) && store.heartbeatCountForTest() < 2 {
				time.Sleep(time.Millisecond)
			}
			return HandlerResult{Status: jobs.StatusQueued, RequeueCause: errors.New("portion done")}, nil
		},
	}
	worker := NewWorker(store, []Handler{handler}, workerTestLogger)
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran || store.statusForTest("job-1") != jobs.StatusQueued {
		t.Fatalf("RunOnce() = (%v, %v), status %s; want the handler's requeue accepted", ran, err, store.statusForTest("job-1"))
	}
}
