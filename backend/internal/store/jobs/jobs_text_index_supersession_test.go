package jobstore

import (
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

func TestCanonTextIndexIntentSupersedesOlderWork(t *testing.T) {
	tests := []struct {
		name              string
		predecessorStatus jobs.Status
		claimIncarnation  int64
		wantStatus        jobs.Status
		wantClaimed       bool
	}{
		{name: "running becomes canceling", predecessorStatus: jobs.StatusRunning, claimIncarnation: 1, wantStatus: jobs.StatusCanceling, wantClaimed: true},
		{name: "retried queued becomes canceled", predecessorStatus: jobs.StatusQueued, claimIncarnation: 2, wantStatus: jobs.StatusCanceled},
		{name: "canceling remains unchanged", predecessorStatus: jobs.StatusCanceling, claimIncarnation: 1, wantStatus: jobs.StatusCanceling, wantClaimed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanonTextIntentFixture(t)
			predecessorID := "20000000-0000-4000-8000-000000000061"
			if _, err := fixture.upsert(t, fixture.input(
				t, predecessorID, governance.SourceViewDraft, 1, 1, "",
			)); err != nil {
				t.Fatal(err)
			}

			predecessorAt := fixture.now.Add(time.Second)
			var claimedAt, cancelRequestedAt any
			if test.wantClaimed {
				claimedAt = predecessorAt
			}
			if test.predecessorStatus == jobs.StatusCanceling {
				cancelRequestedAt = predecessorAt
			}
			if _, err := fixture.db.Pool.Exec(fixture.ctx, `
				UPDATE jobs
				SET status=$3, claim_incarnation=$4,
				    retry_count=CASE WHEN $3='queued' THEN 1 ELSE retry_count END,
				    claimed_at=$5,
				    cancel_requested_at=$6, updated_at=$7
				WHERE project_id=$1 AND id=$2
			`, fixture.projectID, predecessorID, string(test.predecessorStatus), test.claimIncarnation,
				claimedAt, cancelRequestedAt, predecessorAt); err != nil {
				t.Fatal(err)
			}

			successorID := "20000000-0000-4000-8000-000000000062"
			successorInput := fixture.input(t, successorID, governance.SourceViewDraft, 2, 2, fixture.userID)
			successor, err := fixture.upsert(t, successorInput)
			if err != nil {
				t.Fatal(err)
			}
			if successor.ID != successorID || successor.Status != jobs.StatusQueued {
				t.Fatalf("successor = %+v", successor)
			}

			expectedTransitionAt := successorInput.CreatedAt
			if test.predecessorStatus == jobs.StatusCanceling {
				expectedTransitionAt = predecessorAt
			}
			var status string
			var timestampMatches, claimed bool
			if err := fixture.db.Pool.QueryRow(fixture.ctx, `
				SELECT status,
				       cancel_requested_at = $3 AND updated_at = $3,
				       claimed_at IS NOT NULL
				FROM jobs
				WHERE project_id=$1 AND id=$2
			`, fixture.projectID, predecessorID, expectedTransitionAt).Scan(
				&status, &timestampMatches, &claimed,
			); err != nil {
				t.Fatal(err)
			}
			if status != string(test.wantStatus) || !timestampMatches || claimed != test.wantClaimed {
				t.Fatalf("predecessor = status %q timestampMatches=%t claimed=%t", status, timestampMatches, claimed)
			}

			state := fixture.queuedState(t, governance.SourceViewDraft)
			if state.jobID != successorID || state.revision != 2 {
				t.Fatalf("queued successor = %+v", state)
			}
			var queuedCount int
			if err := fixture.db.Pool.QueryRow(fixture.ctx, `
				SELECT count(*)
				FROM jobs
				WHERE project_id=$1 AND kind='canon_text_index' AND status='queued'
				  AND claim_incarnation=0 AND cancel_requested_at IS NULL
				  AND subject_json->>'sourceView'=$2
			`, fixture.projectID, governance.SourceViewDraft).Scan(&queuedCount); err != nil {
				t.Fatal(err)
			}
			if queuedCount != 1 {
				t.Fatalf("current queued successors = %d, want 1", queuedCount)
			}

			if test.predecessorStatus == jobs.StatusRunning {
				_, err := fixture.store.RequeueWorkerClaim(fixture.ctx, jobs.RequeueWorkerClaimStoreInput{
					Kind: jobs.KindCanonTextIndex,
					Claim: jobs.WorkerClaim{
						ProjectID: fixture.projectID, JobID: predecessorID, ClaimIncarnation: test.claimIncarnation,
					},
					ExpectedRetryCount: 0,
				})
				if !errors.Is(err, jobs.ErrConflict) {
					t.Fatalf("retry superseded predecessor error = %v, want conflict", err)
				}
			}
		})
	}
}
