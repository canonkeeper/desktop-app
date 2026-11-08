package deletion

import (
	"context"
	"errors"
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

func TestPostgresTargetRunsClosedRegistryAndReturnsCanonicalResult(t *testing.T) {
	now := time.Date(2033, 5, 18, 13, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetPostgreSQL)
	steps, err := deletiondomain.PostgresStepRegistry(governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration)
	if err != nil {
		t.Fatal(err)
	}
	store := &postgresTargetStoreForTest{}
	target, err := NewPostgresTarget(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != deletiondomain.DispositionPurged ||
		result.HandlerVersion != governance.HandlerPostgresExactInventoryV1 ||
		result.Reason != governance.ReasonPurged {
		t.Fatalf("unexpected PostgreSQL result: %#v", result)
	}
	if len(store.steps) != len(steps) || !store.verified {
		t.Fatalf("registry execution = %d/%d verified=%v", len(store.steps), len(steps), store.verified)
	}
	for index := range steps {
		if store.steps[index] != steps[index] {
			t.Fatalf("step %d = %#v, want %#v", index, store.steps[index], steps[index])
		}
	}
}

func TestPostgresTargetReturnsRetryAfterDurablePartialBatch(t *testing.T) {
	now := time.Date(2033, 5, 18, 13, 0, 0, 0, time.UTC)
	store := &postgresTargetStoreForTest{partialAt: 3}
	target, err := NewPostgresTarget(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(
		context.Background(),
		targetRequestForTest(t, now, governance.TargetPostgreSQL),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != deletiondomain.DispositionRetry || result.Reason != governance.ReasonStaleSource || store.verified {
		t.Fatalf("partial batch did not remain retryable: result=%#v verified=%v", result, store.verified)
	}
	if len(store.steps) != store.partialAt {
		t.Fatalf("steps after partial batch = %d, want %d", len(store.steps), store.partialAt)
	}
}

func TestPostgresTargetDoesNotPurgeProductDataForPermissionLoss(t *testing.T) {
	now := time.Date(2033, 5, 18, 13, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetPostgreSQL)
	request.Tombstone.Identity.PermissionLossActorUserID = "33333333-3333-4333-8333-333333333333"
	store := &postgresTargetStoreForTest{}
	target, err := NewPostgresTarget(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != deletiondomain.DispositionNoData || len(store.steps) != 0 || store.verified ||
		store.tombstoneCalls != 0 {
		t.Fatalf(
			"permission-loss PostgreSQL result/store = %#v/%d/%v/%d",
			result, len(store.steps), store.verified, store.tombstoneCalls,
		)
	}
}

func TestPostgresTargetTombstonesSourceDeletedModelJobsOnceBeforeSteps(t *testing.T) {
	now := time.Date(2033, 5, 18, 13, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetPostgreSQL)
	store := &postgresTargetStoreForTest{}
	target, err := NewPostgresTarget(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != deletiondomain.DispositionPurged || !store.verified {
		t.Fatalf("PostgreSQL result/verified = %#v/%v", result, store.verified)
	}
	if store.tombstoneCalls != 1 || len(store.events) == 0 || store.events[0] != "tombstone" {
		t.Fatalf("tombstone pre-pass calls/events = %d/%v", store.tombstoneCalls, store.events)
	}
	for _, event := range store.events[1:] {
		if event == "tombstone" {
			t.Fatalf("tombstone pre-pass repeated: %v", store.events)
		}
	}
}

func TestPostgresTargetStopsAtTombstonePrePassError(t *testing.T) {
	now := time.Date(2033, 5, 18, 13, 0, 0, 0, time.UTC)
	request := targetRequestForTest(t, now, governance.TargetPostgreSQL)
	store := &postgresTargetStoreForTest{tombstoneErr: deletiondomain.ErrLostTargetClaim}
	target, err := NewPostgresTarget(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(t.Context(), request)
	if !errors.Is(err, deletiondomain.ErrLostTargetClaim) {
		t.Fatalf("Apply() error = %v, want ErrLostTargetClaim", err)
	}
	if result.Disposition != "" || len(store.steps) != 0 || store.verified {
		t.Fatalf("failed pre-pass leaked steps/verified = %#v/%d/%v", result, len(store.steps), store.verified)
	}
}

type postgresTargetStoreForTest struct {
	steps          []deletiondomain.PostgresStep
	partialAt      int
	verified       bool
	events         []string
	tombstoneCalls int
	tombstoneErr   error
}

func (store *postgresTargetStoreForTest) RunPostgresDeletionStep(
	_ context.Context,
	_ deletiondomain.TargetRequest,
	step deletiondomain.PostgresStep,
) (PostgresStepOutcome, error) {
	store.steps = append(store.steps, step)
	store.events = append(store.events, "step:"+step.Name)
	if store.partialAt > 0 && len(store.steps) == store.partialAt {
		return PostgresStepProgressed, nil
	}
	return PostgresStepSucceeded, nil
}

func (store *postgresTargetStoreForTest) TombstoneSourceDeletedModelJobs(
	_ context.Context,
	_ deletiondomain.TargetRequest,
) error {
	store.tombstoneCalls++
	store.events = append(store.events, "tombstone")
	return store.tombstoneErr
}

func (store *postgresTargetStoreForTest) VerifyPostgresDeletionComplete(
	_ context.Context,
	_ deletiondomain.TargetRequest,
	steps []deletiondomain.PostgresStep,
) error {
	store.verified = true
	if len(steps) != len(store.steps) {
		return deletiondomain.ErrInvalidTargetRequest
	}
	return nil
}
