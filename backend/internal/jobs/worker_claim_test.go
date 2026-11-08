package jobs

import "testing"

func TestWorkerClaimValidatesClosedIdentity(t *testing.T) {
	valid := WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid WorkerClaim: %v", err)
	}
	for _, invalid := range []WorkerClaim{
		{},
		{ProjectID: "project-1", JobID: "job-1"},
		{ProjectID: "project-1", ClaimIncarnation: 1},
		{JobID: "job-1", ClaimIncarnation: 1},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid WorkerClaim accepted: %#v", invalid)
		}
	}
}

func TestWorkerJobKindsAreClosed(t *testing.T) {
	for _, kind := range CurrentKinds() {
		if !IsWorkerKind(kind) {
			t.Fatalf("registered kind %q rejected", kind)
		}
	}
	for _, kind := range []Kind{"", "all", "unknown"} {
		if IsWorkerKind(kind) {
			t.Fatalf("unknown kind %q accepted", kind)
		}
	}
}

func TestWorkerClaimDecisionCarriesOnlyClaimAndExhaustionDisposition(t *testing.T) {
	decision := WorkerClaimDecision{
		Claim:           WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 1},
		ExhaustedStatus: StatusFailed,
	}
	if decision.Claim.Validate() != nil || decision.ExhaustedStatus != StatusFailed {
		t.Fatalf("decision = %#v", decision)
	}
}
