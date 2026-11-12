package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"canonkeeper/backend/internal/retention"
	deletion "canonkeeper/backend/internal/services/deletion"
)

const (
	retentionRunInterval           = time.Hour
	deletionReconciliationInterval = time.Second
)

type retentionRunner interface {
	Reconcile(context.Context) (retention.Summary, error)
}

type deletionTargetRunner interface {
	RunOnceDiagnostic(context.Context) (deletion.RunDiagnostic, error)
}

func startRetentionRunner(
	ctx context.Context,
	runner retentionRunner,
	deletionRunner deletionTargetRunner,
	logger *log.Logger,
) <-chan struct{} {
	return startRetentionRunnerWithIntervals(
		ctx, runner, deletionRunner, retentionRunInterval, deletionReconciliationInterval, logger,
	)
}

func startRetentionRunnerWithIntervals(
	ctx context.Context,
	runner retentionRunner,
	deletionRunner deletionTargetRunner,
	retentionInterval time.Duration,
	deletionInterval time.Duration,
	logger *log.Logger,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRetention(ctx, runner, logger)

		retentionTicker := time.NewTicker(retentionInterval)
		defer retentionTicker.Stop()
		deletionTicker := time.NewTicker(deletionInterval)
		defer deletionTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-retentionTicker.C:
				runRetention(ctx, runner, logger)
			case <-deletionTicker.C:
				runDeletionTargetReconciliation(ctx, deletionRunner, logger)
			}
		}
	}()
	return done
}

// runDeletionTargetReconciliation обрабатывает только уже созданную target-task.
// Правила полного retention, включая 24-часовые сроки, остаются в runRetention.
func runDeletionTargetReconciliation(ctx context.Context, runner deletionTargetRunner, logger *log.Logger) {
	diagnostic, err := runner.RunOnceDiagnostic(ctx)
	if err != nil {
		if diagnostic.FailureCode == "" {
			diagnostic.FailureCode = "unknown_failure"
		}
		logger.Printf(
			"deletion target reconciliation failed: event=deletion_target_reconciliation_failed failure_code=%s%s",
			diagnostic.FailureCode, deletionDiagnosticFields(diagnostic),
		)
		return
	}
	switch diagnostic.Outcome {
	case deletion.RunNoWork:
		return
	case deletion.RunCompleted, deletion.RunRetryScheduled:
		logger.Printf(
			"deletion target reconciliation finished: event=deletion_target_reconciliation_finished outcome=%s%s",
			diagnostic.Outcome, deletionDiagnosticFields(diagnostic),
		)
	default:
		logger.Printf(
			"deletion target reconciliation failed: event=deletion_target_reconciliation_failed failure_code=invalid_reconciliation_outcome%s",
			deletionDiagnosticFields(diagnostic),
		)
	}
}

func deletionDiagnosticFields(diagnostic deletion.RunDiagnostic) string {
	return fmt.Sprintf(
		" task_id=%s project_id=%s source_kind=%s source_id=%s target=%s attempt=%d reason=%s retry_after_ms=%d",
		diagnostic.TaskID, diagnostic.ProjectID, diagnostic.SourceKind, diagnostic.SourceID,
		diagnostic.Target, diagnostic.Attempt, diagnostic.Reason, diagnostic.RetryAfter.Milliseconds(),
	)
}

func runRetention(ctx context.Context, runner retentionRunner, logger *log.Logger) {
	summary, err := runner.Reconcile(ctx)
	if err != nil {
		logger.Print("retention failed")
		return
	}
	logger.Printf(
		"retention completed expired_sessions_revoked=%d retention_deadline_breaches_recorded=%d source_hard_purges_enqueued=%d deletion_proofs_garbage_collected=%d deletion_targets_reconciled=%d deletion_target_retries_scheduled=%d terminal_import_queue_jobs_reconciled=%d expired_book_updates_removed=%d",
		summary.ExpiredSessionsRevoked,
		summary.RetentionDeadlineBreachesRecorded,
		summary.SourceHardPurgesEnqueued,
		summary.DeletionProofsGarbageCollected,
		summary.DeletionTargetsReconciled,
		summary.DeletionTargetRetriesScheduled,
		summary.TerminalImportQueueJobsReconciled,
		summary.ExpiredBookUpdatesRemoved,
	)
}
