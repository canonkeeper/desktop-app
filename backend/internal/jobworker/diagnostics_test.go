package jobworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
)

const diagnosticRequestID = "00000000-0000-4000-8000-000000000921"

func TestWorkerRunOnceTurnsHandlerPanicIntoWorkerPanicFailure(t *testing.T) {
	store, handler, output := newDiagnosticWorkerFixture(func(context.Context, jobs.Job) (HandlerResult, error) {
		var chapters map[string]int
		chapters["SECRET_PANIC_KEY"]++
		return HandlerResult{Status: jobs.StatusSucceeded}, nil
	})
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))

	ran, err := worker.RunOnce(t.Context())

	if !ran || err != nil {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil) after a handler panic", ran, err)
	}
	if got := store.statusForTest("job-1"); got != jobs.StatusFailed || handler.failedCode != PanicCode {
		t.Fatalf("job = %q with code %q, want failed %q", got, handler.failedCode, PanicCode)
	}
	diagnostic := diagnosticLogLine(t, output.String())
	if diagnostic.JobID != "job-1" || diagnostic.RequestID != diagnosticRequestID || diagnostic.ErrorCode != PanicCode ||
		!strings.Contains(diagnostic.Error, "assignment to entry in nil map") || !strings.Contains(diagnostic.Stack, "goroutine") {
		t.Fatalf("job_diagnostic = %+v", diagnostic)
	}
}

// R4.1: a panic in the heartbeat goroutine fails the Job with worker.panic; the process lives on.
func TestWorkerRunOnceTurnsHeartbeatPanicIntoWorkerPanicFailure(t *testing.T) {
	store, handler, output := newDiagnosticWorkerFixture(func(ctx context.Context, _ jobs.Job) (HandlerResult, error) {
		<-ctx.Done()
		return HandlerResult{}, ctx.Err()
	})
	store.beforeHeartbeat = func(jobs.WorkerClaim) { panic("heartbeat exploded") }
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))
	worker.heartbeatInterval = time.Millisecond

	ran, err := worker.RunOnce(t.Context())

	if !ran || err != nil {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil) after a heartbeat panic", ran, err)
	}
	if got := store.statusForTest("job-1"); got != jobs.StatusFailed || handler.failedCode != PanicCode {
		t.Fatalf("job = %q with code %q, want failed %q", got, handler.failedCode, PanicCode)
	}
	diagnostic := diagnosticLogLine(t, output.String())
	if diagnostic.ErrorCode != PanicCode || !strings.Contains(diagnostic.Error, "heartbeat exploded") ||
		!strings.Contains(diagnostic.Stack, "goroutine") {
		t.Fatalf("job_diagnostic = %+v", diagnostic)
	}
}

func TestWorkerRunOnceWritesBoundedDiagnosticForRuntimeFailure(t *testing.T) {
	cause := errors.New("bwrap: setting up uid map: Permission denied " + strings.Repeat("x", 800))
	store, handler, output := newDiagnosticWorkerFixture(func(context.Context, jobs.Job) (HandlerResult, error) {
		return HandlerResult{}, fmt.Errorf("start thread: %w", safeLifecycleError{code: "runtime.thread_start_failed", raw: cause.Error()})
	})
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))

	if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("log lines = %q, want job_started, job_finished and job_diagnostic", lines)
	}
	for _, line := range lines[:2] {
		if !strings.Contains(line, `"requestId":"`+diagnosticRequestID+`"`) {
			t.Fatalf("lifecycle line %q lacks the creating request ID", line)
		}
	}
	diagnostic := diagnosticLogLine(t, output.String())
	if diagnostic.ErrorCode != "runtime.thread_start_failed" || diagnostic.RequestID != diagnosticRequestID ||
		!strings.Contains(diagnostic.Error, "start thread: bwrap: setting up uid map: Permission denied") ||
		len(diagnostic.Error) > 500 || diagnostic.Stack != "" {
		t.Fatalf("job_diagnostic = %+v (error length %d)", diagnostic, len(diagnostic.Error))
	}
}

func TestWorkerRunOnceWritesDiagnosticForModelFailureCommittedByHandler(t *testing.T) {
	var store *fakeWorkerStore
	store, handler, output := newDiagnosticWorkerFixture(func(_ context.Context, job jobs.Job) (HandlerResult, error) {
		store.failForTest(job.ID, &jobs.Error{Code: "model.turn_failed", Message: "model turn failed"})
		return HandlerResult{}, errors.New("native turn did not complete: status=failed codexErrorInfo=serverOverloaded: upstream overloaded")
	})
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))

	if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	diagnostic := diagnosticLogLine(t, output.String())
	if diagnostic.ErrorCode != "model.turn_failed" || !strings.Contains(diagnostic.Error, "codexErrorInfo=serverOverloaded") {
		t.Fatalf("job_diagnostic = %+v", diagnostic)
	}
}

func TestWorkerRunOnceWritesNoDiagnosticForUnclassifiedHandlerFailure(t *testing.T) {
	store, handler, output := newDiagnosticWorkerFixture(func(context.Context, jobs.Job) (HandlerResult, error) {
		return HandlerResult{}, errors.New("SECRET_BOOK_TEXT in a parse error")
	})
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))

	if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	if handler.failedCode != HandlerFailedCode || strings.Contains(output.String(), "job_diagnostic") ||
		strings.Contains(output.String(), "SECRET_") {
		t.Fatalf("code %q, log %q: want worker.handler_failed without diagnostic", handler.failedCode, output.String())
	}
}

func TestWorkerRunOnceSurvivesPanicOutsideHandler(t *testing.T) {
	store, handler, output := newDiagnosticWorkerFixture(func(context.Context, jobs.Job) (HandlerResult, error) {
		return HandlerResult{}, errors.New("handler failed")
	})
	handler.failurePanic = true
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))

	ran, err := worker.RunOnce(t.Context())

	if err == nil || !strings.Contains(err.Error(), "failure handler broke") {
		t.Fatalf("RunOnce() = (%v, %v), want the recovered panic as an error", ran, err)
	}
}

func TestWorkerRunLogsLoopErrorsWithClassAndConsecutiveCount(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	permissionDenied := loopSQLStateError{state: "42501"}
	store.claimErrors = []error{permissionDenied, fmt.Errorf("claim: %w", permissionDenied), errors.New("SECRET_LOOP_DETAIL")}
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	output := &lockedBuffer{}
	worker := NewWorker(store, []Handler{handler}, log.New(output, "", 0))
	worker.idleDelay = time.Millisecond
	worker.errorRetryDelay = time.Millisecond
	worker.maxErrorRetryDelay = time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for store.statusForTest("job-1") != jobs.StatusSucceeded {
		select {
		case err := <-done:
			t.Fatalf("Run() stopped after loop errors: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for the worker to recover from loop errors")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done

	type loopErrorEvent struct {
		ErrorClass        string `json:"errorClass"`
		ErrorCause        string `json:"errorCause"`
		ConsecutiveErrors int    `json:"consecutiveErrors"`
	}
	var events []loopErrorEvent
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if !strings.Contains(line, `"event":"worker_loop_error"`) {
			continue
		}
		var event loopErrorEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 3 ||
		events[0].ErrorClass != "database" || events[0].ErrorCause != "42501" || events[0].ConsecutiveErrors != 1 ||
		events[1].ErrorClass != "database" || events[1].ErrorCause != "42501" || events[1].ConsecutiveErrors != 2 ||
		events[2].ErrorClass != "internal" || events[2].ErrorCause != "unclassified" || events[2].ConsecutiveErrors != 3 {
		t.Fatalf("worker_loop_error events = %+v", events)
	}
	if strings.Contains(output.String(), "SECRET_") {
		t.Fatalf("loop error log leaked error text: %q", output.String())
	}
}

type jobDiagnosticForTest struct {
	Event     string `json:"event"`
	JobID     string `json:"jobId"`
	RequestID string `json:"requestId"`
	ErrorCode string `json:"errorCode"`
	Error     string `json:"error"`
	Stack     string `json:"stack"`
}

func diagnosticLogLine(t *testing.T, output string) jobDiagnosticForTest {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if !strings.Contains(line, `"event":"job_diagnostic"`) {
			continue
		}
		var diagnostic jobDiagnosticForTest
		if err := json.Unmarshal([]byte(line), &diagnostic); err != nil {
			t.Fatalf("job_diagnostic is not JSON: %v: %q", err, line)
		}
		return diagnostic
	}
	t.Fatalf("log has no job_diagnostic line: %q", output)
	return jobDiagnosticForTest{}
}

func newDiagnosticWorkerFixture(
	handle func(context.Context, jobs.Job) (HandlerResult, error),
) (*fakeWorkerStore, *diagnosticHandlerForTest, *bytes.Buffer) {
	job := jobForWorkerTest("job-1", jobs.KindImport)
	job.CreatorRequestID = diagnosticRequestID
	store := newFakeWorkerStore(job)
	return store, &diagnosticHandlerForTest{store: store, handle: handle}, &bytes.Buffer{}
}

type diagnosticHandlerForTest struct {
	store        *fakeWorkerStore
	handle       func(context.Context, jobs.Job) (HandlerResult, error)
	failedCode   string
	failurePanic bool
}

func (*diagnosticHandlerForTest) Kind() jobs.Kind { return jobs.KindImport }

func (h *diagnosticHandlerForTest) HandleResult(ctx context.Context, job jobs.Job) (HandlerResult, error) {
	return h.handle(ctx, job)
}

func (h *diagnosticHandlerForTest) HandleFailedResult(_ context.Context, job jobs.Job) (HandlerResult, error) {
	if h.failurePanic {
		panic("failure handler broke")
	}
	h.failedCode = job.Error.Code
	h.store.failForTest(job.ID, job.Error)
	return HandlerResult{Status: jobs.StatusFailed}, nil
}

func (h *diagnosticHandlerForTest) HandleCanceledResult(_ context.Context, job jobs.Job) (HandlerResult, error) {
	h.store.forceStatusForTest(job.ID, jobs.StatusCanceled)
	return HandlerResult{Status: jobs.StatusCanceled}, nil
}

func (s *fakeWorkerStore) failForTest(jobID string, jobError *jobs.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.jobs[jobID]
	record.Status = jobs.StatusFailed
	record.Error = jobError
	s.jobs[jobID] = record
}

type loopSQLStateError struct{ state string }

func (err loopSQLStateError) Error() string    { return "SECRET_SQL_DETAIL" }
func (err loopSQLStateError) SQLState() string { return err.state }

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
