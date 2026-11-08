package governance

import "testing"

func TestWorkerClaimQueryRegistryIsClosed(t *testing.T) {
	want := []WorkerClaimQuery{
		WorkerClaimNextQueuedJob,
		WorkerClaimNextDeletionTargetTask,
		WorkerClaimNextSourceHardPurge,
		WorkerClaimNextImportExpiry,
		WorkerClaimNextDeletionAuditGC,
	}
	got := WorkerClaimQueryRegistry()
	if len(got) != len(want) {
		t.Fatalf("registry length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("registry[%d] = %q, want %q", index, got[index], want[index])
		}
	}
	if err := ValidateWorkerClaimQueryRegistry(got); err != nil {
		t.Fatalf("ValidateWorkerClaimQueryRegistry() error = %v", err)
	}
	if err := ValidateWorkerClaimQueryRegistry(append(got, WorkerClaimQuery("SixthGlobalWorkerQuery"))); err == nil {
		t.Fatal("registry accepted a sixth worker discovery query")
	}
}

func TestWorkerClaimFunctionsAreExact(t *testing.T) {
	for _, registration := range WorkerClaimRegistrations() {
		if registration.Query == "" || registration.Function == "" {
			t.Fatalf("incomplete worker registration: %+v", registration)
		}
		if registration.Schema != "canonkeeper_worker" {
			t.Fatalf("worker schema = %q, want canonkeeper_worker", registration.Schema)
		}
	}
}
