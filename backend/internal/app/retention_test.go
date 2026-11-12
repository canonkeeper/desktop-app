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

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/retention"
	deletion "canonkeeper/backend/internal/services/deletion"
)

type fakeRetentionRunner struct {
	mu             sync.Mutex
	summary        retention.Summary
	err            error
	runOnceCalls   int
	reconcileCalls int
	callsCh        chan struct{}
}

func (f *fakeRetentionRunner) RunOnce(ctx context.Context) (retention.Summary, error) {
	f.mu.Lock()
	f.runOnceCalls++
	f.mu.Unlock()
	return f.summary, f.err
}

func (f *fakeRetentionRunner) Reconcile(ctx context.Context) (retention.Summary, error) {
	f.mu.Lock()
	f.reconcileCalls++
	f.mu.Unlock()

	if f.callsCh != nil {
		select {
		case f.callsCh <- struct{}{}:
		default:
		}
	}

	return f.summary, f.err
}

func (f *fakeRetentionRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runOnceCalls + f.reconcileCalls
}

func (f *fakeRetentionRunner) callCounts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runOnceCalls, f.reconcileCalls
}

type fakeDeletionTargetRunner struct {
	mu          sync.Mutex
	diagnostics []deletion.RunDiagnostic
	err         error
	calls       int
	callsCh     chan struct{}
}

func (f *fakeDeletionTargetRunner) RunOnceDiagnostic(context.Context) (deletion.RunDiagnostic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.callsCh != nil {
		select {
		case f.callsCh <- struct{}{}:
		default:
		}
	}
	if len(f.diagnostics) == 0 {
		return deletion.RunDiagnostic{Outcome: deletion.RunNoWork}, f.err
	}
	diagnostic := f.diagnostics[0]
	f.diagnostics = f.diagnostics[1:]
	return diagnostic, f.err
}

func (f *fakeDeletionTargetRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestRetentionRunUsesBoundedReconciliationDrain(t *testing.T) {
	runner := &fakeRetentionRunner{}
	runRetention(context.Background(), runner, log.New(io.Discard, "", 0))
	runOnceCalls, reconcileCalls := runner.callCounts()
	if runOnceCalls != 0 || reconcileCalls != 1 {
		t.Fatalf("retention calls RunOnce/Reconcile = %d/%d, want 0/1", runOnceCalls, reconcileCalls)
	}
}

func TestRetentionRunLogsCountsOnly(t *testing.T) {
	runner := &fakeRetentionRunner{summary: retention.Summary{
		ExpiredSessionsRevoked:            1,
		RetentionDeadlineBreachesRecorded: 4,
		SourceHardPurgesEnqueued:          5,
		DeletionProofsGarbageCollected:    7,
		DeletionTargetsReconciled:         8,
		DeletionTargetRetriesScheduled:    9,
		TerminalImportQueueJobsReconciled: 11,
		ExpiredBookUpdatesRemoved:         12,
	}}
	var logs bytes.Buffer

	runRetention(context.Background(), runner, log.New(&logs, "", 0))

	if runner.callCount() != 1 {
		t.Fatalf("retention call count = %d, want 1", runner.callCount())
	}
	output := logs.String()
	for _, fragment := range []string{
		"retention completed",
		"expired_sessions_revoked=1",
		"retention_deadline_breaches_recorded=4",
		"source_hard_purges_enqueued=5",
		"deletion_proofs_garbage_collected=7",
		"deletion_targets_reconciled=8",
		"deletion_target_retries_scheduled=9",
		"terminal_import_queue_jobs_reconciled=11",
		"expired_book_updates_removed=12",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("log output = %q, want %q", output, fragment)
		}
	}
	if strings.Contains(output, "resource-") || strings.Contains(output, "storage-") {
		t.Fatalf("log output included resource-like payload detail: %q", output)
	}
}

func TestRetentionRunLogsGenericError(t *testing.T) {
	runner := &fakeRetentionRunner{err: errors.New("raw-retention-detail")}
	var logs bytes.Buffer

	runRetention(context.Background(), runner, log.New(&logs, "", 0))

	output := logs.String()
	if !strings.Contains(output, "failed") {
		t.Fatalf("log output = %q, want generic failure message", output)
	}
	if strings.Contains(output, "raw-retention-detail") {
		t.Fatalf("log output included raw error detail: %q", output)
	}
}

func TestDeletionTargetReconciliationSkipsNoWorkLog(t *testing.T) {
	var logs bytes.Buffer
	runDeletionTargetReconciliation(
		context.Background(),
		&fakeDeletionTargetRunner{diagnostics: []deletion.RunDiagnostic{{Outcome: deletion.RunNoWork}}},
		log.New(&logs, "", 0),
	)
	if logs.Len() != 0 {
		t.Fatalf("no-work reconciliation log = %q, want no log", logs.String())
	}
}

func TestDeletionTargetReconciliationLogsSafeOutcome(t *testing.T) {
	diagnostic := deletion.RunDiagnostic{
		Outcome: deletion.RunCompleted, TaskID: "22222222-2222-4222-8222-222222222222",
		ProjectID:  "11111111-1111-4111-8111-111111111111",
		SourceKind: governance.DeletionSelectionProject,
		SourceID:   "11111111-1111-4111-8111-111111111111",
		Target:     governance.TargetPostgreSQL, Attempt: 3, Reason: governance.ReasonPurged,
	}
	var logs bytes.Buffer
	runDeletionTargetReconciliation(
		context.Background(),
		&fakeDeletionTargetRunner{diagnostics: []deletion.RunDiagnostic{diagnostic}},
		log.New(&logs, "", 0),
	)
	for _, fragment := range []string{
		"outcome=completed",
		"task_id=22222222-2222-4222-8222-222222222222",
		"project_id=11111111-1111-4111-8111-111111111111",
		"source_kind=project",
		"source_id=11111111-1111-4111-8111-111111111111",
		"target=postgresql",
		"attempt=3",
		"reason=purged",
	} {
		if !strings.Contains(logs.String(), fragment) {
			t.Fatalf("completed reconciliation log = %q, want %q", logs.String(), fragment)
		}
	}

	logs.Reset()
	diagnostic.Outcome = ""
	diagnostic.FailureCode = deletion.RunFailureHandlerExecution
	runDeletionTargetReconciliation(
		context.Background(),
		&fakeDeletionTargetRunner{diagnostics: []deletion.RunDiagnostic{diagnostic}, err: errors.New("raw-deletion-detail")},
		log.New(&logs, "", 0),
	)
	if output := logs.String(); !strings.Contains(output, "event=deletion_target_reconciliation_failed") ||
		!strings.Contains(output, "failure_code=handler_execution_failed") ||
		!strings.Contains(output, "task_id=22222222-2222-4222-8222-222222222222") ||
		strings.Contains(output, "raw-deletion-detail") {
		t.Fatalf("failed reconciliation log = %q", output)
	}

	logs.Reset()
	diagnostic.Outcome = deletion.RunRetryScheduled
	diagnostic.FailureCode = ""
	diagnostic.Reason = governance.ReasonValidationFailed
	diagnostic.RetryAfter = 17 * time.Second
	runDeletionTargetReconciliation(
		context.Background(),
		&fakeDeletionTargetRunner{diagnostics: []deletion.RunDiagnostic{diagnostic}},
		log.New(&logs, "", 0),
	)
	if output := logs.String(); !strings.Contains(output, "outcome=retry_scheduled") ||
		!strings.Contains(output, "reason=validation_failed") ||
		!strings.Contains(output, "retry_after_ms=17000") {
		t.Fatalf("retry reconciliation log = %q", output)
	}
}

func TestStartRetentionRunnerRunsAtStartupAndReconcilesDeletionTargets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	retentionCalls := make(chan struct{}, 1)
	deletionCalls := make(chan struct{}, 1)
	runner := &fakeRetentionRunner{callsCh: retentionCalls}
	deletionRunner := &fakeDeletionTargetRunner{callsCh: deletionCalls}

	done := startRetentionRunnerWithIntervals(
		ctx, runner, deletionRunner, time.Hour, time.Millisecond, log.New(io.Discard, "", 0),
	)

	select {
	case <-retentionCalls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for startup retention run")
	}
	select {
	case <-deletionCalls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deletion-only reconciliation")
	}
	if runner.callCount() != 1 || deletionRunner.callCount() == 0 {
		t.Fatalf("retention/deletion calls = %d/%d, want one startup retention and fast deletion reconciliation", runner.callCount(), deletionRunner.callCount())
	}
	cancel()
	<-done
}
