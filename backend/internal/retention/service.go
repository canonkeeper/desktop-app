package retention

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/services/deletion"
)

var (
	ErrNoWork                 = errors.New("retention: no eligible work")
	ErrInvalidClaim           = errors.New("retention: invalid exact claim")
	ErrClaimAuthorityRequired = errors.New("retention: worker claim authority required")
)

const maxGovernedReconciliationCycles = 100

type Summary struct {
	ExpiredSessionsRevoked            int64
	RetentionDeadlineBreachesRecorded int64
	SourceHardPurgesEnqueued          int64
	DeletionProofsGarbageCollected    int64
	DeletionTargetsReconciled         int64
	DeletionTargetRetriesScheduled    int64
	TerminalImportQueueJobsReconciled int64
}

func (summary *Summary) add(other Summary) {
	summary.ExpiredSessionsRevoked += other.ExpiredSessionsRevoked
	summary.RetentionDeadlineBreachesRecorded += other.RetentionDeadlineBreachesRecorded
	summary.SourceHardPurgesEnqueued += other.SourceHardPurgesEnqueued
	summary.DeletionProofsGarbageCollected += other.DeletionProofsGarbageCollected
	summary.DeletionTargetsReconciled += other.DeletionTargetsReconciled
	summary.DeletionTargetRetriesScheduled += other.DeletionTargetRetriesScheduled
	summary.TerminalImportQueueJobsReconciled += other.TerminalImportQueueJobsReconciled
}

type SourceHardPurgeClaim struct {
	ProjectID  string
	EntityKind governance.SourceEntityKind
	EntityID   string
}

func (claim SourceHardPurgeClaim) Validate() error {
	if claim.ProjectID == "" || claim.EntityID == "" {
		return ErrInvalidClaim
	}
	switch claim.EntityKind {
	case governance.SourceEntityProject, governance.SourceEntityBook, governance.SourceEntityChapter:
		return nil
	default:
		return ErrInvalidClaim
	}
}

type DeletionAuditGCClaim struct {
	ProjectID   string
	TombstoneID string
}

func (claim DeletionAuditGCClaim) Validate() error {
	if claim.ProjectID == "" || claim.TombstoneID == "" {
		return ErrInvalidClaim
	}
	return nil
}

type BoundedGlobalStore interface {
	RevokeExpiredSessions(context.Context) (int64, error)
	MarkRetentionDeadlineBreaches(context.Context) (int64, error)
	ReconcileImportUploads(context.Context) error
}

type ImportQueueLifecycleStore interface {
	ExpireStaleImportQueues(context.Context) (int64, error)
	ReconcileTerminalImportQueueJobs(context.Context) (int64, error)
}

type WorkerClaims interface {
	ClaimNextSourceHardPurgeForWorker(context.Context) (SourceHardPurgeClaim, error)
	ClaimNextDeletionAuditGCForWorker(context.Context) (DeletionAuditGCClaim, error)
}

type ExactStore interface {
	EnqueueSourceHardPurgeForClaim(context.Context, SourceHardPurgeClaim) (bool, error)
	GarbageCollectDeletionProofForClaim(context.Context, DeletionAuditGCClaim) (bool, error)
}

type DeletionCoordinator interface {
	RunOnce(context.Context) (deletion.RunOutcome, error)
}

type Service struct {
	store        BoundedGlobalStore
	importQueues ImportQueueLifecycleStore
	claims       WorkerClaims
	exact        ExactStore
	coordinator  DeletionCoordinator
}

func NewService(
	store BoundedGlobalStore,
	importQueues ImportQueueLifecycleStore,
	claims WorkerClaims,
	exact ExactStore,
	coordinator DeletionCoordinator,
) *Service {
	if store == nil {
		panic("retention: BoundedGlobalStore is required")
	}
	if importQueues == nil {
		panic("retention: Import Queue lifecycle store is required")
	}
	if claims == nil {
		panic("retention: WorkerClaims is required")
	}
	if exact == nil {
		panic("retention: ExactStore is required")
	}
	if coordinator == nil {
		panic("retention: DeletionCoordinator is required")
	}
	return &Service{
		store: store, importQueues: importQueues, claims: claims, exact: exact, coordinator: coordinator,
	}
}

func (service *Service) Reconcile(ctx context.Context) (Summary, error) {
	var total Summary
	for cycle := 0; cycle < maxGovernedReconciliationCycles; cycle++ {
		summary, progressed, err := service.runCycle(ctx, cycle == 0)
		total.add(summary)
		if err != nil || !progressed {
			return total, err
		}
	}
	return total, nil
}

func (service *Service) runCycle(ctx context.Context, periodic bool) (Summary, bool, error) {
	var summary Summary
	var errs []error
	progressed := false
	if periodic {
		if count, err := service.store.RevokeExpiredSessions(ctx); err != nil {
			errs = append(errs, err)
		} else {
			summary.ExpiredSessionsRevoked = count
		}
		if count, err := service.store.MarkRetentionDeadlineBreaches(ctx); err != nil {
			errs = append(errs, err)
		} else {
			summary.RetentionDeadlineBreachesRecorded = count
		}
		if _, err := service.importQueues.ExpireStaleImportQueues(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := service.store.ReconcileImportUploads(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if count, err := service.importQueues.ReconcileTerminalImportQueueJobs(ctx); err != nil {
		errs = append(errs, err)
	} else {
		summary.TerminalImportQueueJobsReconciled = count
		if count > 0 {
			progressed = true
		}
	}

	if applied, err := service.enqueueOneSourceHardPurge(ctx); err != nil {
		errs = append(errs, err)
	} else if applied {
		summary.SourceHardPurgesEnqueued++
		progressed = true
	}
	if applied, err := service.collectOneDeletionProof(ctx); err != nil {
		errs = append(errs, err)
	} else if applied {
		summary.DeletionProofsGarbageCollected++
		progressed = true
	}

	outcome, err := service.coordinator.RunOnce(ctx)
	if err != nil {
		errs = append(errs, err)
	} else {
		switch outcome {
		case deletion.RunCompleted:
			summary.DeletionTargetsReconciled++
			progressed = true
		case deletion.RunRetryScheduled:
			summary.DeletionTargetRetriesScheduled++
			progressed = true
		case deletion.RunNoWork:
		default:
			errs = append(errs, ErrInvalidClaim)
		}
	}
	return summary, progressed, errors.Join(errs...)
}

func (service *Service) enqueueOneSourceHardPurge(ctx context.Context) (bool, error) {
	claim, err := service.claims.ClaimNextSourceHardPurgeForWorker(ctx)
	if errors.Is(err, ErrNoWork) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := claim.Validate(); err != nil {
		return false, err
	}
	return service.exact.EnqueueSourceHardPurgeForClaim(ctx, claim)
}

func (service *Service) collectOneDeletionProof(ctx context.Context) (bool, error) {
	claim, err := service.claims.ClaimNextDeletionAuditGCForWorker(ctx)
	if errors.Is(err, ErrNoWork) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := claim.Validate(); err != nil {
		return false, err
	}
	return service.exact.GarbageCollectDeletionProofForClaim(ctx, claim)
}
