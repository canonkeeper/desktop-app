package retention

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/services/deletion"
)

func TestNewServiceRequiresEveryDependency(t *testing.T) {
	worker := &fakeGovernedRetention{}
	tests := []struct {
		name string
		new  func()
	}{
		{name: "global store", new: func() { NewService(nil, worker, worker, worker, worker) }},
		{name: "import queues", new: func() { NewService(worker, nil, worker, worker, worker) }},
		{name: "claims", new: func() { NewService(worker, worker, nil, worker, worker) }},
		{name: "exact store", new: func() { NewService(worker, worker, worker, nil, worker) }},
		{name: "coordinator", new: func() { NewService(worker, worker, worker, worker, nil) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewService accepted a missing dependency")
				}
			}()
			test.new()
		})
	}
}

func TestReconcileUsesOnlyExactClaimsAndBoundedGlobalPolicies(t *testing.T) {
	worker := &fakeGovernedRetention{
		sourceClaims: []SourceHardPurgeClaim{{
			ProjectID:  "11111111-1111-4111-8111-111111111111",
			EntityKind: governance.SourceEntityChapter,
			EntityID:   "33333333-3333-4333-8333-333333333333",
		}},
		gcClaims: []DeletionAuditGCClaim{{
			ProjectID:   "11111111-1111-4111-8111-111111111111",
			TombstoneID: "55555555-5555-4555-8555-555555555555",
		}},
		counts: map[string]int64{
			"RevokeExpiredSessions":         2,
			"MarkRetentionDeadlineBreaches": 5,
			"RemoveExpiredBookUpdates":      3,
		},
		coordinatorOutcomes: []deletion.RunOutcome{deletion.RunCompleted},
	}
	service := NewService(worker, worker, worker, worker, worker)

	summary, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	want := Summary{
		ExpiredSessionsRevoked:            2,
		RetentionDeadlineBreachesRecorded: 5,
		ExpiredBookUpdatesRemoved:         3,
		SourceHardPurgesEnqueued:          1,
		DeletionProofsGarbageCollected:    1,
		DeletionTargetsReconciled:         1,
	}
	if summary != want {
		t.Fatalf("summary = %#v, want %#v", summary, want)
	}
	wantCalls := []string{
		"RevokeExpiredSessions", "MarkRetentionDeadlineBreaches", "RemoveExpiredBookUpdates",
		"ExpireStaleImportQueues", "ReconcileImportUploads", "ReconcileTerminalImportQueueJobs",
		"ClaimNextSourceHardPurgeForWorker", "EnqueueSourceHardPurgeForClaim",
		"ClaimNextDeletionAuditGCForWorker", "GarbageCollectDeletionProofForClaim",
		"DeletionCoordinator.RunOnce",
		"ReconcileTerminalImportQueueJobs", "ClaimNextSourceHardPurgeForWorker",
		"ClaimNextDeletionAuditGCForWorker",
		"DeletionCoordinator.RunOnce",
	}
	if !slices.Equal(worker.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", worker.calls, wantCalls)
	}

	typeOfSummary := reflect.TypeOf(summary)
	for index := 0; index < typeOfSummary.NumField(); index++ {
		field := typeOfSummary.Field(index)
		if field.Type.Kind() != reflect.Int64 {
			t.Fatalf("Summary.%s type = %s, want int64 count", field.Name, field.Type)
		}
	}
}

func TestReconcilePeriodicRunsImportQueueLifecycle(t *testing.T) {
	worker := &fakeGovernedRetention{coordinatorOutcomes: []deletion.RunOutcome{deletion.RunNoWork}}
	if _, err := NewService(worker, worker, worker, worker, worker).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(worker.calls, "ExpireStaleImportQueues") ||
		!slices.Contains(worker.calls, "ReconcileImportUploads") ||
		!slices.Contains(worker.calls, "ReconcileTerminalImportQueueJobs") {
		t.Fatalf("calls = %#v, want import queue lifecycle", worker.calls)
	}
}

func TestReconcileDrainsTerminalImportQueueJobs(t *testing.T) {
	worker := &fakeGovernedRetention{
		terminalImportQueueJobCounts: []int64{2, 1},
		coordinatorOutcomes:          []deletion.RunOutcome{deletion.RunNoWork},
	}
	summary, err := NewService(worker, worker, worker, worker, worker).Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.TerminalImportQueueJobsReconciled != 3 {
		t.Fatalf("reconciled terminal queue jobs = %d, want 3", summary.TerminalImportQueueJobsReconciled)
	}
}

func TestRetentionNeverUpdatesImportQueueTerminalState(t *testing.T) {
	source, err := os.ReadFile("../../internal/store/retention/import_queue_lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "UPDATE import_queues") {
		t.Fatal("retention adapter mutated Import Queue terminal state")
	}
}

func TestReconcileTreatsStaleExactCandidatesAsNoMutation(t *testing.T) {
	worker := &fakeGovernedRetention{
		sourceClaims: []SourceHardPurgeClaim{{
			ProjectID: "11111111-1111-4111-8111-111111111111", EntityKind: governance.SourceEntityProject,
			EntityID: "11111111-1111-4111-8111-111111111111",
		}},
		gcClaims: []DeletionAuditGCClaim{{
			ProjectID:   "11111111-1111-4111-8111-111111111111",
			TombstoneID: "33333333-3333-4333-8333-333333333333",
		}},
		forceNoMutation:     true,
		coordinatorOutcomes: []deletion.RunOutcome{deletion.RunNoWork},
	}
	service := NewService(worker, worker, worker, worker, worker)

	summary, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if summary.SourceHardPurgesEnqueued != 0 || summary.DeletionProofsGarbageCollected != 0 {
		t.Fatalf("stale candidates changed summary: %#v", summary)
	}
}

func TestReconcileDrainsGovernedWorkWithoutBroadProductDeletion(t *testing.T) {
	worker := &fakeGovernedRetention{
		sourceClaims: []SourceHardPurgeClaim{
			{ProjectID: "11111111-1111-4111-8111-111111111111", EntityKind: governance.SourceEntityBook, EntityID: "22222222-2222-4222-8222-222222222222"},
			{ProjectID: "11111111-1111-4111-8111-111111111111", EntityKind: governance.SourceEntityChapter, EntityID: "33333333-3333-4333-8333-333333333333"},
		},
		coordinatorOutcomes: []deletion.RunOutcome{deletion.RunCompleted, deletion.RunCompleted, deletion.RunNoWork},
	}
	service := NewService(worker, worker, worker, worker, worker)

	summary, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if summary.SourceHardPurgesEnqueued != 2 || summary.DeletionTargetsReconciled != 2 {
		t.Fatalf("Reconcile summary = %#v, want two source enqueues and two target completions", summary)
	}
	for _, call := range worker.calls {
		if strings.Contains(call, "SoftDeleted") || strings.Contains(call, "PurgeProjects") || strings.Contains(call, "PurgeChats") {
			t.Fatalf("Reconcile invoked broad product deletion %q", call)
		}
	}
}

func TestRetentionSQLContainsOnlyBoundedNonTenantPolicies(t *testing.T) {
	sqlBytes, err := os.ReadFile("../../db/queries/retention.sql")
	if err != nil {
		t.Fatalf("read retention.sql: %v", err)
	}
	sqlText := normalizeSQL(string(sqlBytes))
	for _, fragment := range []string{
		"-- name: revokeexpiredsessions :execrows",
		"-- name: markretentiondeadlinebreaches :execrows",
		"clock_timestamp()",
		"limit 100",
	} {
		if !strings.Contains(sqlText, normalizeSQL(fragment)) {
			t.Fatalf("retention.sql missing %q\nSQL:\n%s", fragment, sqlText)
		}
	}
	for _, forbidden := range []string{
		"purgesoftdeletedprojects", "purgesoftdeletedbooks", "purgesoftdeletedchapters",
		"purgesoftdeletedchatsessions", "purgesoftdeletedreaderannotations",
		"purgeterminalchatturnevents", "purgeterminaljobworkingfiles", "purgeexpiredauditlogs",
		"purgeexpiredglobalauditproof", "audit_logs",
		"delete from projects", "delete from books", "delete from chapters", "delete from chat_sessions",
		"sqlc.arg(now_at)",
	} {
		if strings.Contains(sqlText, forbidden) {
			t.Fatalf("retention.sql retains forbidden broad product path %q", forbidden)
		}
	}
}

func normalizeSQL(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

type fakeGovernedRetention struct {
	counts                       map[string]int64
	calls                        []string
	sourceClaims                 []SourceHardPurgeClaim
	gcClaims                     []DeletionAuditGCClaim
	coordinatorOutcomes          []deletion.RunOutcome
	nextSource                   int
	nextGC                       int
	nextCoordinator              int
	nextTerminalQueue            int
	forceNoMutation              bool
	terminalImportQueueJobCounts []int64
}

func (store *fakeGovernedRetention) RevokeExpiredSessions(ctx context.Context) (int64, error) {
	return store.record(ctx, "RevokeExpiredSessions")
}

func (store *fakeGovernedRetention) MarkRetentionDeadlineBreaches(ctx context.Context) (int64, error) {
	return store.record(ctx, "MarkRetentionDeadlineBreaches")
}

func (store *fakeGovernedRetention) RemoveExpiredBookUpdates(ctx context.Context) (int64, error) {
	return store.record(ctx, "RemoveExpiredBookUpdates")
}

func (store *fakeGovernedRetention) ExpireStaleImportQueues(ctx context.Context) (int64, error) {
	return store.record(ctx, "ExpireStaleImportQueues")
}

func (store *fakeGovernedRetention) ReconcileTerminalImportQueueJobs(ctx context.Context) (int64, error) {
	store.calls = append(store.calls, "ReconcileTerminalImportQueueJobs")
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if store.nextTerminalQueue >= len(store.terminalImportQueueJobCounts) {
		return 0, nil
	}
	count := store.terminalImportQueueJobCounts[store.nextTerminalQueue]
	store.nextTerminalQueue++
	return count, nil
}

func (store *fakeGovernedRetention) ReconcileImportUploads(ctx context.Context) error {
	store.record(ctx, "ReconcileImportUploads")
	return ctx.Err()
}

func (store *fakeGovernedRetention) ClaimNextSourceHardPurgeForWorker(ctx context.Context) (SourceHardPurgeClaim, error) {
	store.calls = append(store.calls, "ClaimNextSourceHardPurgeForWorker")
	if err := ctx.Err(); err != nil {
		return SourceHardPurgeClaim{}, err
	}
	if store.nextSource >= len(store.sourceClaims) {
		return SourceHardPurgeClaim{}, ErrNoWork
	}
	claim := store.sourceClaims[store.nextSource]
	store.nextSource++
	return claim, nil
}

func (store *fakeGovernedRetention) EnqueueSourceHardPurgeForClaim(ctx context.Context, claim SourceHardPurgeClaim) (bool, error) {
	store.calls = append(store.calls, "EnqueueSourceHardPurgeForClaim")
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return !store.forceNoMutation, nil
}

func (store *fakeGovernedRetention) ClaimNextDeletionAuditGCForWorker(ctx context.Context) (DeletionAuditGCClaim, error) {
	store.calls = append(store.calls, "ClaimNextDeletionAuditGCForWorker")
	if err := ctx.Err(); err != nil {
		return DeletionAuditGCClaim{}, err
	}
	if store.nextGC >= len(store.gcClaims) {
		return DeletionAuditGCClaim{}, ErrNoWork
	}
	claim := store.gcClaims[store.nextGC]
	store.nextGC++
	return claim, nil
}

func (store *fakeGovernedRetention) GarbageCollectDeletionProofForClaim(ctx context.Context, claim DeletionAuditGCClaim) (bool, error) {
	store.calls = append(store.calls, "GarbageCollectDeletionProofForClaim")
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return !store.forceNoMutation, nil
}

func (store *fakeGovernedRetention) RunOnce(ctx context.Context) (deletion.RunOutcome, error) {
	store.calls = append(store.calls, "DeletionCoordinator.RunOnce")
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if store.nextCoordinator >= len(store.coordinatorOutcomes) {
		return deletion.RunNoWork, nil
	}
	outcome := store.coordinatorOutcomes[store.nextCoordinator]
	store.nextCoordinator++
	return outcome, nil
}

func (store *fakeGovernedRetention) record(ctx context.Context, method string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	store.calls = append(store.calls, method)
	return store.counts[method], nil
}
