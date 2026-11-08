package governance

import "errors"

type WorkerClaimQuery string

const (
	WorkerClaimNextQueuedJob          WorkerClaimQuery = "ClaimNextQueuedJobForWorker"
	WorkerClaimNextDeletionTargetTask WorkerClaimQuery = "ClaimNextDeletionTargetTaskForWorker"
	WorkerClaimNextSourceHardPurge    WorkerClaimQuery = "ClaimNextSourceHardPurgeForWorker"
	WorkerClaimNextImportExpiry       WorkerClaimQuery = "ClaimNextImportExpiryForWorker"
	WorkerClaimNextDeletionAuditGC    WorkerClaimQuery = "ClaimNextDeletionAuditGCForWorker"
)

type WorkerClaimRegistration struct {
	Query    WorkerClaimQuery
	Schema   string
	Function string
}

var workerClaimRegistrations = []WorkerClaimRegistration{
	{WorkerClaimNextQueuedJob, "canonkeeper_worker", "claim_next_queued_job_for_worker"},
	{WorkerClaimNextDeletionTargetTask, "canonkeeper_worker", "claim_next_deletion_target_task_for_worker"},
	{WorkerClaimNextSourceHardPurge, "canonkeeper_worker", "claim_next_source_hard_purge_for_worker"},
	{WorkerClaimNextImportExpiry, "canonkeeper_worker", "claim_next_import_expiry_for_worker"},
	{WorkerClaimNextDeletionAuditGC, "canonkeeper_worker", "claim_next_deletion_audit_gc_for_worker"},
}

func WorkerClaimQueryRegistry() []WorkerClaimQuery {
	registry := make([]WorkerClaimQuery, 0, len(workerClaimRegistrations))
	for _, registration := range workerClaimRegistrations {
		registry = append(registry, registration.Query)
	}
	return registry
}

func WorkerClaimRegistrations() []WorkerClaimRegistration {
	return append([]WorkerClaimRegistration(nil), workerClaimRegistrations...)
}

func ValidateWorkerClaimQueryRegistry(registry []WorkerClaimQuery) error {
	if len(registry) != len(workerClaimRegistrations) {
		return errors.New("governance: worker claim query registry is incomplete")
	}
	for index, registration := range workerClaimRegistrations {
		if registry[index] != registration.Query {
			return errors.New("governance: worker claim query registry changed")
		}
	}
	return nil
}

func workerClaimRegistration(name string) (WorkerClaimRegistration, bool) {
	for _, registration := range workerClaimRegistrations {
		if string(registration.Query) == name {
			return registration, true
		}
	}
	return WorkerClaimRegistration{}, false
}
