package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartIndexingWorkerRunnerLogsGenericError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs synchronizedBufferForAPI
	runner := &fakeJobRunnerForAPI{err: errors.New("raw manuscript SHOULD_NOT_LEAK"), done: make(chan struct{})}

	startJobWorkerRunner(ctx, runner, "indexing", log.New(&logs, "", 0))

	select {
	case <-runner.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker runner")
	}
	output := waitForLogOutputForAPI(t, &logs)
	if !strings.Contains(output, "indexing worker stopped") {
		t.Fatalf("log output = %q, want generic worker stop message", output)
	}
	if strings.Contains(output, "SHOULD_NOT_LEAK") {
		t.Fatalf("log output leaked raw worker error: %q", output)
	}
}

func TestStartJobWorkerRunnerDoesNotRestartAfterError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &retryingJobRunnerForAPI{
		calls: make(chan int, 2),
	}

	done := startJobWorkerRunner(ctx, runner, "indexing", log.New(io.Discard, "", 0))

	if got := <-runner.calls; got != 1 {
		t.Fatalf("runner call = %d, want 1", got)
	}
	<-done
	select {
	case got := <-runner.calls:
		t.Fatalf("runner restarted with call %d", got)
	default:
	}
}

type fakeJobRunnerForAPI struct {
	err  error
	done chan struct{}
	once sync.Once
}

func (r *fakeJobRunnerForAPI) Run(context.Context) error {
	r.once.Do(func() {
		close(r.done)
	})
	return r.err
}

type retryingJobRunnerForAPI struct {
	calls chan int
	count int
}

func (r *retryingJobRunnerForAPI) Run(ctx context.Context) error {
	r.count++
	r.calls <- r.count
	if r.count == 1 {
		return errors.New("temporary runner failure")
	}
	<-ctx.Done()
	return ctx.Err()
}

type synchronizedBufferForAPI struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBufferForAPI) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(payload)
}

func (buffer *synchronizedBufferForAPI) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func waitForLogOutputForAPI(t *testing.T, logs *synchronizedBufferForAPI) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		output := logs.String()
		if output != "" {
			return output
		}
		time.Sleep(time.Millisecond)
	}
	return logs.String()
}
