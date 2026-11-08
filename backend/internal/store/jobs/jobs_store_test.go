package jobstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestJobsStoreImplementsJobsStoreContract(t *testing.T) {
	var _ jobs.Store = (*Store)(nil)
}

func TestJobProducerSessionWriteProofContract(t *testing.T) {
	tests := []struct {
		name            string
		serviceFile     string
		serviceFunction string
		serviceProof    string
		storeFile       string
		storeFunction   string
		storeProof      string
	}{
		{
			name:            "import",
			serviceFile:     "../../library/imports.go",
			serviceFunction: "func createImportJob(",
			serviceProof:    "SessionWriteProof:  input.SessionWriteProof",
			storeFile:       "../library/library_import_staging_store.go",
			storeFunction:   "func (s *Store) createImportStagingForActor(",
			storeProof:      "authstore.LockVerifiedSessionWriteProof(ctx, tx, actorUserUUID, input.SessionWriteProof)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceBody := jobProducerFunctionSource(t, test.serviceFile, test.serviceFunction)
			if !strings.Contains(serviceBody, test.serviceProof) {
				t.Fatalf("%s does not pass the exact SessionWriteProof to its store input", test.serviceFunction)
			}

			storeBody := jobProducerFunctionSource(t, test.storeFile, test.storeFunction)
			txIndex := strings.Index(storeBody, "s.WithTx(ctx, func(tx pgx.Tx) error")
			proofIndex := strings.Index(storeBody, test.storeProof)
			if txIndex < 0 || proofIndex < txIndex {
				t.Fatalf("%s does not validate the exact SessionWriteProof inside its owning transaction", test.storeFunction)
			}
		})
	}
}

func jobProducerFunctionSource(t *testing.T, filename, signature string) string {
	t.Helper()
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start := strings.Index(source, signature)
	if start < 0 {
		t.Fatalf("%s not found in %s", signature, filename)
	}
	body := source[start:]
	if end := strings.Index(body[len(signature):], "\nfunc "); end >= 0 {
		body = body[:len(signature)+end]
	}
	return body
}

func TestJobsStoreRequeueWorkerClaimRejectsModelBeforeDatabaseAccess(t *testing.T) {
	store := &Store{}
	claim := jobs.WorkerClaim{
		ProjectID:        "11111111-1111-4111-8111-111111111111",
		JobID:            "22222222-2222-4222-8222-222222222222",
		ClaimIncarnation: 1,
	}
	for _, kind := range []jobs.Kind{jobs.KindResearchChatTurn} {
		for retryCount := 0; retryCount < jobs.MaxJobAttempts; retryCount++ {
			_, err := store.RequeueWorkerClaim(context.Background(), jobs.RequeueWorkerClaimStoreInput{
				Claim:              claim,
				Kind:               kind,
				ExpectedRetryCount: retryCount,
			})
			if !errors.Is(err, jobs.ErrModelRetryFenceRequired) {
				t.Fatalf("RequeueWorkerClaim(%q, retry_count=%d) error = %v, want ErrModelRetryFenceRequired", kind, retryCount, err)
			}
		}
	}
}

func TestJobsStoreCanonTextIndexUsesOrdinaryRetryLifecycle(t *testing.T) {
	fixture := newCanonTextIndexRetryFixture(t)
	store := New(fixture.db)

	requeued, err := store.RequeueWorkerClaim(context.Background(), jobs.RequeueWorkerClaimStoreInput{
		Claim: fixture.claim, Kind: jobs.KindCanonTextIndex, ExpectedRetryCount: 0,
	})
	if err != nil {
		t.Fatalf("RequeueWorkerClaim() error = %v", err)
	}
	if requeued.Status != jobs.StatusQueued || requeued.RetryCount != 1 || requeued.WorkerClaim.ClaimIncarnation != 2 {
		t.Fatalf("requeued = %#v", requeued)
	}
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status='running', retry_count=2, claim_incarnation=2,
		    claimed_at=clock_timestamp(), updated_at=clock_timestamp()
		WHERE project_id=$1 AND id=$2
	`, fixture.projectID, fixture.claim.JobID); err != nil {
		t.Fatal(err)
	}
	exhausted := fixture.claim
	exhausted.ClaimIncarnation = 2
	if _, err := store.RequeueWorkerClaim(context.Background(), jobs.RequeueWorkerClaimStoreInput{
		Claim: exhausted, Kind: jobs.KindCanonTextIndex, ExpectedRetryCount: 2,
	}); !errors.Is(err, jobs.ErrRetryExhausted) {
		t.Fatalf("exhausted RequeueWorkerClaim() error = %v, want ErrRetryExhausted", err)
	}
}

func TestJobsStoreModelOwnerCleanupContractIsClosed(t *testing.T) {
	if jobs.ErrModelOwnerCleanupRequired == nil {
		t.Fatal("jobs.ErrModelOwnerCleanupRequired is nil")
	}
	modelKinds := map[jobs.Kind]bool{jobs.KindResearchChatTurn: true}
	for _, kind := range jobs.CurrentKinds() {
		if got := modelJobKindRequiresOwnerCleanup(kind); got != modelKinds[kind] {
			t.Fatalf("modelJobKindRequiresOwnerCleanup(%q) = %t, want %t", kind, got, modelKinds[kind])
		}
	}
}

func TestCanonTextIndexIntentSQLContract(t *testing.T) {
	queryBytes, err := os.ReadFile("../../../db/queries/jobs.sql")
	if err != nil {
		t.Fatalf("read jobs SQL: %v", err)
	}
	query := string(queryBytes)
	for _, fragment := range []string{
		"-- name: LockCanonTextIndexActiveWork :one",
		"-- name: CreateCanonTextIndexIntent :one",
		"-- name: UpdateQueuedCanonTextIndexIntent :one",
		"-- name: AdoptQueuedCanonTextIndexIntentCreator :one",
		"-- name: SupersedeCanonTextIndexPredecessors :execrows",
		"job.kind = 'canon_text_index'",
		"job.status IN ('queued', 'running', 'canceling')",
		"job.status IN ('queued', 'running')",
		"(job.subject_json->>'catalogRevision')::bigint DESC",
		"(job.subject_json->>'catalogRevision')::bigint < sqlc.arg(catalog_revision)::bigint",
		"job.subject_json->>'sourceView' = sqlc.arg(source_view)::text",
		"LIMIT 1",
		"FOR UPDATE OF job",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("text intent SQL missing %q", fragment)
		}
	}
}

func TestCanonTextIndexIntentCreateModeAndGeneratedFields(t *testing.T) {
	fixture := newCanonTextIntentFixture(t)
	input := fixture.input(t, "", governance.SourceViewDraft, 1, 1, "")
	input.CreateIfMissing = false
	input.CreatedAt = time.Time{}

	var absent jobs.Job
	var present bool
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		var err error
		absent, present, err = fixture.store.UpsertCanonTextIndexIntent(fixture.ctx, tx, input)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if present || absent != (jobs.Job{}) {
		t.Fatalf("automatic no-create result = (%+v, %t), want zero Job and false", absent, present)
	}
	var count int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'canon_text_index'
	`, fixture.projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("automatic no-create jobs = %d, want 0", count)
	}

	input.CreateIfMissing = true
	var created jobs.Job
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		var err error
		created, present, err = fixture.store.UpsertCanonTextIndexIntent(fixture.ctx, tx, input)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !present || created.ID == "" || created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("generated intent = (%+v, %t)", created, present)
	}
	if _, err := storedb.ParseUUID(created.ID); err != nil {
		t.Fatalf("generated Job ID = %q: %v", created.ID, err)
	}
}

func TestCanonTextIndexIntentReplacesUnreadableLifecycleIntent(t *testing.T) {
	fixture := newCanonTextIntentFixture(t)
	oldJobID := "20000000-0000-4000-8000-000000000015"
	if _, err := fixture.upsert(t, fixture.input(t, oldJobID, governance.SourceViewDraft, 1, 1, "")); err != nil {
		t.Fatal(err)
	}
	var oldOwnerID string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT artifact_source_owner_id::text
		FROM jobs
		WHERE project_id=$1 AND id=$2
	`, fixture.projectID, oldJobID).Scan(&oldOwnerID); err != nil {
		t.Fatal(err)
	}

	conn, err := fixture.db.Pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(fixture.ctx, `SET session_replication_role = replica`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	_, updateErr := conn.Exec(fixture.ctx, `
		UPDATE projects SET lifecycle_generation=2 WHERE id=$1
	`, fixture.projectID)
	_, resetErr := conn.Exec(fixture.ctx, `SET session_replication_role = origin`)
	conn.Release()
	if updateErr != nil || resetErr != nil {
		t.Fatalf("advance project lifecycle: update=%v reset=%v", updateErr, resetErr)
	}

	newJobID := "20000000-0000-4000-8000-000000000016"
	replacement := fixture.input(t, newJobID, governance.SourceViewDraft, 2, 1, "")
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	projectRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 2,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	replacement.Source.Refs = []governance.ArtifactSourceRef{projectRef}
	created, err := fixture.upsert(t, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != newJobID {
		t.Fatalf("replacement Job ID = %q, want fresh %q", created.ID, newJobID)
	}

	var oldStatus, oldPayloadState string
	var oldSubjectPresent, oldOwnerPresent bool
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT status, governance_payload_state,
		       subject_json IS NOT NULL, artifact_source_owner_id IS NOT NULL
		FROM jobs
		WHERE project_id=$1 AND id=$2
	`, fixture.projectID, oldJobID).Scan(
		&oldStatus, &oldPayloadState, &oldSubjectPresent, &oldOwnerPresent,
	); err != nil {
		t.Fatal(err)
	}
	if oldStatus != string(jobs.StatusExpired) || oldPayloadState != "source_deleted" || oldSubjectPresent || oldOwnerPresent {
		t.Fatalf("old lifecycle intent = status %q payload %q subject=%t owner=%t",
			oldStatus, oldPayloadState, oldSubjectPresent, oldOwnerPresent)
	}
	if count := countJobsStoreRowsForTest(t, fixture.db, `
		SELECT count(*) FROM artifact_source_owners WHERE project_id=$1 AND id=$2
	`, fixture.projectID, oldOwnerID); count != 0 {
		t.Fatalf("old lifecycle owners = %d, want 0", count)
	}
	state := fixture.queuedState(t, governance.SourceViewDraft)
	if state.jobID != newJobID || state.revision != 2 {
		t.Fatalf("fresh lifecycle intent = %+v", state)
	}
	var projectGeneration int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT ref.project_generation
		FROM jobs AS job
		JOIN artifact_source_refs AS ref
		  ON ref.owner_id=job.artifact_source_owner_id AND ref.project_id=job.project_id
		WHERE job.project_id=$1 AND job.id=$2
	`, fixture.projectID, newJobID).Scan(&projectGeneration); err != nil {
		t.Fatal(err)
	}
	if projectGeneration != 2 {
		t.Fatalf("fresh lifecycle project generation = %d, want 2", projectGeneration)
	}
}

func TestCanonTextIndexIntentCoalescesNewestAndAdoptsCreatorOnce(t *testing.T) {
	fixture := newCanonTextIntentFixture(t)
	serviceID := "20000000-0000-4000-8000-000000000011"
	created, err := fixture.upsert(t, fixture.input(t, serviceID, governance.SourceViewDraft, 1, 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != serviceID || !created.CanCancel || created.CreatorUserID != "" || created.QueueClass != jobs.QueueClassUserDemanded {
		t.Fatalf("service-created intent = %+v", created)
	}

	adopted, err := fixture.upsert(t, fixture.input(t,
		"20000000-0000-4000-8000-000000000012", governance.SourceViewDraft, 1, 2, fixture.userID,
	))
	if err != nil {
		t.Fatal(err)
	}
	if adopted.ID != serviceID || adopted.CreatorUserID != fixture.userID {
		t.Fatalf("adopted intent = %+v", adopted)
	}
	state := fixture.queuedState(t, governance.SourceViewDraft)
	if state.revision != 1 || state.refCount != 1 || state.creatorUserID != fixture.userID {
		t.Fatalf("equal revision changed target: %+v", state)
	}

	newest, err := fixture.upsert(t, fixture.input(t,
		"20000000-0000-4000-8000-000000000013", governance.SourceViewDraft, 2, 2, fixture.otherUserID,
	))
	if err != nil {
		t.Fatal(err)
	}
	if newest.ID != serviceID || newest.CreatorUserID != fixture.userID {
		t.Fatalf("newest intent = %+v", newest)
	}
	state = fixture.queuedState(t, governance.SourceViewDraft)
	if state.revision != 2 || state.refCount != 2 || state.creatorUserID != fixture.userID {
		t.Fatalf("newest state = %+v", state)
	}

	late, err := fixture.upsert(t, fixture.input(t,
		"20000000-0000-4000-8000-000000000014", governance.SourceViewDraft, 1, 1, fixture.otherUserID,
	))
	if err != nil {
		t.Fatal(err)
	}
	if late.ID != serviceID {
		t.Fatalf("late input returned Job %s, want %s", late.ID, serviceID)
	}
	state = fixture.queuedState(t, governance.SourceViewDraft)
	if state.revision != 2 || state.refCount != 2 || state.creatorUserID != fixture.userID {
		t.Fatalf("late input downgraded state: %+v", state)
	}
}

func TestCanonTextIndexIntentClaimAndCancelFencesCreatorAdoption(t *testing.T) {
	for _, fence := range []string{"claim", "cancel"} {
		t.Run(fence, func(t *testing.T) {
			fixture := newCanonTextIntentFixture(t)
			originalID := "20000000-0000-4000-8000-000000000021"
			if _, err := fixture.upsert(t, fixture.input(t, originalID, governance.SourceViewDraft, 1, 1, "")); err != nil {
				t.Fatal(err)
			}
			var err error
			if fence == "claim" {
				_, err = fixture.db.Pool.Exec(fixture.ctx, `
					UPDATE jobs SET status = 'running', claim_incarnation = 1, claimed_at = $2
					WHERE project_id = $1 AND id = $3
				`, fixture.projectID, fixture.now, originalID)
			} else {
				_, err = fixture.db.Pool.Exec(fixture.ctx, `
					UPDATE jobs
					SET status = 'canceling', claim_incarnation = 1, claimed_at = $2, cancel_requested_at = $2
					WHERE project_id = $1 AND id = $3
				`, fixture.projectID, fixture.now, originalID)
			}
			if err != nil {
				t.Fatal(err)
			}

			currentInput := fixture.input(
				t, "20000000-0000-4000-8000-000000000022", governance.SourceViewDraft, 1, 1, fixture.userID,
			)
			currentInput.CreateIfMissing = false
			current, err := fixture.upsert(t, currentInput)
			if err != nil {
				t.Fatal(err)
			}
			if current.ID != originalID {
				t.Fatalf("current active intent = %s, want %s", current.ID, originalID)
			}

			successorID := "20000000-0000-4000-8000-000000000023"
			successorInput := fixture.input(t, successorID, governance.SourceViewDraft, 2, 2, fixture.userID)
			successorInput.CreateIfMissing = false
			successor, err := fixture.upsert(t, successorInput)
			if err != nil {
				t.Fatal(err)
			}
			if successor.ID != successorID || successor.CreatorUserID != fixture.userID {
				t.Fatalf("successor = %+v", successor)
			}
			var originalCreator string
			if err := fixture.db.Pool.QueryRow(fixture.ctx, `
				SELECT COALESCE(creator_user_id::text, '') FROM jobs WHERE project_id = $1 AND id = $2
			`, fixture.projectID, originalID).Scan(&originalCreator); err != nil {
				t.Fatal(err)
			}
			if originalCreator != "" {
				t.Fatalf("fenced original creator = %q, want null", originalCreator)
			}
		})
	}
}

func TestCanonTextIndexIntentCoalescesViewsIndependently(t *testing.T) {
	fixture := newCanonTextIntentFixture(t)
	for index, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		jobID := fmt.Sprintf("20000000-0000-4000-8000-%012d", 31+index)
		created, err := fixture.upsert(t, fixture.input(t, jobID, view, 1, 1, ""))
		if err != nil {
			t.Fatal(err)
		}
		if created.ID != jobID {
			t.Fatalf("%s Job ID = %s, want %s", view, created.ID, jobID)
		}
	}
	if draft, published := fixture.queuedState(t, governance.SourceViewDraft), fixture.queuedState(t, governance.SourceViewPublished); draft.jobID == published.jobID {
		t.Fatalf("draft and published reused Job %s", draft.jobID)
	}
}

var errCanonTextIntentInjected = errors.New("injected text intent failure")

func TestCanonTextIndexIntentRollsBackSourceTargetAndRefsAtEveryFailpoint(t *testing.T) {
	for _, point := range []string{
		"before_owner", "after_owner", "after_ref_0", "before_artifact",
		"after_artifact", "before_seal", "after_seal",
	} {
		t.Run(point, func(t *testing.T) {
			fixture := newCanonTextIntentFixture(t)
			jobID := "20000000-0000-4000-8000-000000000041"
			if _, err := fixture.upsert(t, fixture.input(t, jobID, governance.SourceViewDraft, 1, 1, "")); err != nil {
				t.Fatal(err)
			}
			store := New(fixture.db, withTextIndexIntentFailpoint(func(got string) error {
				if got == point {
					return errCanonTextIntentInjected
				}
				return nil
			}))
			err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(fixture.ctx, `UPDATE projects SET name = 'mutated' WHERE id = $1`, fixture.projectID); err != nil {
					return err
				}
				_, _, err := store.UpsertCanonTextIndexIntent(fixture.ctx, tx, fixture.input(
					t, "20000000-0000-4000-8000-000000000042", governance.SourceViewDraft, 2, 2, "",
				))
				return err
			})
			if !errors.Is(err, errCanonTextIntentInjected) {
				t.Fatalf("UpsertCanonTextIndexIntent error = %v, want injected failure", err)
			}
			var title string
			if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT name FROM projects WHERE id = $1`, fixture.projectID).Scan(&title); err != nil {
				t.Fatal(err)
			}
			if title != "Text Intent Project" {
				t.Fatalf("source mutation committed as title %q", title)
			}
			state := fixture.queuedState(t, governance.SourceViewDraft)
			if state.revision != 1 || state.refCount != 1 {
				t.Fatalf("failed update changed target: %+v", state)
			}
		})
	}
}

func TestCanonTextIndexIntentConcurrentUniqueLoserCleansCandidateOwnerAndKeepsNewest(t *testing.T) {
	fixture := newCanonTextIntentFixture(t)
	ready := make(chan struct{}, 2)
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	type result struct {
		index int
		job   jobs.Job
		err   error
	}
	results := make(chan result, 2)
	inputs := []jobs.CanonTextIndexIntentInput{
		fixture.input(t, "20000000-0000-4000-8000-000000000052", governance.SourceViewDraft, 2, 1, ""),
		fixture.input(t, "20000000-0000-4000-8000-000000000053", governance.SourceViewDraft, 3, 2, ""),
	}
	for index, input := range inputs {
		index := index
		input := input
		go func() {
			var once sync.Once
			store := New(fixture.db, withTextIndexIntentFailpoint(func(point string) error {
				if point == "before_artifact" {
					once.Do(func() {
						ready <- struct{}{}
						<-release[index]
					})
				}
				return nil
			}))
			job, present, err := fixture.upsertWithStore(input, store)
			if err == nil && !present {
				err = errors.New("concurrent upsert returned absent Job")
			}
			results <- result{index: index, job: job, err: err}
		}()
	}
	<-ready
	<-ready
	close(release[1])
	first := <-results
	if first.index != 1 {
		t.Fatalf("full-chain contender finished at index %d, want 1", first.index)
	}
	close(release[0])
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent upsert errors = %v, %v", first.err, second.err)
	}
	if first.job.ID != second.job.ID {
		t.Fatalf("concurrent calls returned different successors %s and %s", first.job.ID, second.job.ID)
	}
	state := fixture.queuedState(t, governance.SourceViewDraft)
	if state.revision != 3 || state.refCount != 2 || state.jobID != first.job.ID {
		t.Fatalf("concurrent newest state = %+v, returned %s", state, first.job.ID)
	}
	if subject := second.job.Subject.(jobs.CanonTextIndexSubject); subject.CatalogRevision != 3 {
		t.Fatalf("concurrent merged subject = %+v, want revision 3", subject)
	}

	var ownerCount, refCount, jobCount int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT
		  (SELECT count(*) FROM artifact_source_owners WHERE project_id = $1 AND artifact_id IN ($2, $3)),
		  (SELECT count(*) FROM artifact_source_refs AS ref JOIN artifact_source_owners AS owner
		     ON owner.id = ref.owner_id AND owner.project_id = ref.project_id
		   WHERE owner.project_id = $1 AND owner.artifact_id IN ($2, $3)),
		  (SELECT count(*) FROM jobs WHERE project_id = $1 AND id IN ($2, $3))
	`, fixture.projectID, inputs[0].JobID, inputs[1].JobID).Scan(&ownerCount, &refCount, &jobCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 1 || refCount != 2 || jobCount != 1 {
		t.Fatalf("candidate cleanup = owners:%d refs:%d jobs:%d", ownerCount, refCount, jobCount)
	}
}

func TestJobsSQLCancelJobStatusUsesNormalizedCreator(t *testing.T) {
	queryBytes, err := os.ReadFile("../../../db/queries/jobs.sql")
	if err != nil {
		t.Fatalf("read jobs SQL: %v", err)
	}
	query := string(queryBytes)
	cancelStart := strings.Index(query, "-- name: CancelJobStatus")
	if cancelStart < 0 {
		t.Fatal("CancelJobStatus query not found")
	}
	cancelQuery := query[cancelStart:]
	if strings.Contains(cancelQuery, "JOIN import_job_requests") || strings.Contains(cancelQuery, "progress_json->>'creatorUserId'") {
		t.Fatal("CancelJobStatus derives creator authority from embedded payloads")
	}
	if !strings.Contains(cancelQuery, "j.creator_user_id = sqlc.arg(actor_user_id)") {
		t.Fatal("CancelJobStatus does not authorize against normalized creator_user_id")
	}
}

func TestJobsSQLRequeueWorkerClaimIsExactCAS(t *testing.T) {
	queryBytes, err := os.ReadFile("../../../db/queries/jobs.sql")
	if err != nil {
		t.Fatalf("read jobs SQL: %v", err)
	}
	query := string(queryBytes)
	start := strings.Index(query, "-- name: RequeueWorkerClaim :one")
	end := strings.Index(query, "-- name: CancelJobStatus :one")
	if start < 0 || end <= start {
		t.Fatal("RequeueWorkerClaim query boundary not found")
	}
	requeue := query[start:end]
	for _, fragment := range []string{
		"status = 'queued'",
		"retry_count = job.retry_count + 1",
		"claim_incarnation = job.claim_incarnation + 1",
		"claimed_at = NULL",
		"next_attempt_at = clock_timestamp() + make_interval",
		"job.claim_incarnation = sqlc.arg(claim_incarnation)",
		"job.status = 'running'",
		"job.cancel_requested_at IS NULL",
		"job.retry_count = sqlc.arg(expected_retry_count)",
		"job.subject_json->>'type' = job.kind",
		"job.subject_json->>'projectId' = job.project_id::text",
		"ck_artifact_source_owner_is_readable(",
	} {
		if !strings.Contains(requeue, fragment) {
			t.Fatalf("RequeueWorkerClaim missing %q", fragment)
		}
	}
}

func TestJobsStoreRequeueWorkerClaimUsesDBClockAndImmediateIncarnationFence(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000980"
	projectID := "00000000-0000-4000-8000-000000000981"
	membershipID := "00000000-0000-4000-8000-000000000982"
	jobID := "00000000-0000-4000-8000-000000000983"
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-requeue@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Requeue Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleAdmin, now, false)
	subject := jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: projectID,
		SourceView: governance.SourceViewDraft, CatalogRevision: 1,
	}
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID: jobID, ProjectID: projectID, Kind: jobs.KindCanonTextIndex, Status: jobs.StatusQueued,
		Subject: subject, CreatorUserID: actorID, QueueClass: jobs.QueueClassUserDemanded,
		MaxAttempts: jobs.MaxJobAttempts, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}

	firstDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	if err != nil {
		t.Fatalf("first ClaimNextQueuedJobForWorker() error = %v", err)
	}
	before := time.Now().UTC()
	firstRetry, err := store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: firstDecision.Claim, Kind: jobs.KindCanonTextIndex, ExpectedRetryCount: 0,
	})
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("first RequeueWorkerClaim() error = %v", err)
	}
	if firstRetry.Status != jobs.StatusQueued || firstRetry.RetryCount != 1 ||
		firstRetry.WorkerClaim.ClaimIncarnation != firstDecision.Claim.ClaimIncarnation+1 ||
		firstRetry.Subject != subject {
		t.Fatalf("first retry = %#v, want queued count=1 fenced immutable subject", firstRetry)
	}
	if firstRetry.NextAttemptAt.Before(before.Add(15*time.Second)) || firstRetry.NextAttemptAt.After(after.Add(15*time.Second)) {
		t.Fatalf("first next_attempt_at = %v, want DB now+15s between %v and %v", firstRetry.NextAttemptAt, before, after)
	}
	if err := store.HeartbeatWorkerClaim(ctx, firstDecision.Claim); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("old claim heartbeat error = %v, want ErrConflict", err)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE jobs SET next_attempt_at = clock_timestamp() - interval '1 second' WHERE id = $1", jobID); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	secondDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	if err != nil {
		t.Fatalf("second ClaimNextQueuedJobForWorker() error = %v", err)
	}
	before = time.Now().UTC()
	secondRetry, err := store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: secondDecision.Claim, Kind: jobs.KindCanonTextIndex, ExpectedRetryCount: 1,
	})
	after = time.Now().UTC()
	if err != nil {
		t.Fatalf("second RequeueWorkerClaim() error = %v", err)
	}
	if secondRetry.Status != jobs.StatusQueued || secondRetry.RetryCount != 2 ||
		secondRetry.WorkerClaim.ClaimIncarnation != secondDecision.Claim.ClaimIncarnation+1 ||
		secondRetry.Subject != subject {
		t.Fatalf("second retry = %#v, want queued count=2 fenced immutable subject", secondRetry)
	}
	if secondRetry.NextAttemptAt.Before(before.Add(60*time.Second)) || secondRetry.NextAttemptAt.After(after.Add(60*time.Second)) {
		t.Fatalf("second next_attempt_at = %v, want DB now+60s between %v and %v", secondRetry.NextAttemptAt, before, after)
	}
	if _, err := store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: secondDecision.Claim, Kind: jobs.KindCanonTextIndex, ExpectedRetryCount: 1,
	}); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("stale RequeueWorkerClaim() error = %v, want ErrConflict", err)
	}
}

func TestJobsStoreClaimNextQueuedJobReclaimsStaleRunningWithoutStealingActiveRunning(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000960"
	projectID := "00000000-0000-4000-8000-000000000961"
	membershipID := "00000000-0000-4000-8000-000000000962"
	activeJobID := "00000000-0000-4000-8000-000000000963"
	staleJobID := "00000000-0000-4000-8000-000000000964"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-claim-stale@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Claim Stale Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleAdmin, now, false)
	for _, record := range []struct {
		id        string
		createdAt time.Time
		claimedAt time.Time
	}{
		{id: activeJobID, createdAt: now.Add(-2 * time.Hour), claimedAt: now},
		{id: staleJobID, createdAt: now.Add(-time.Hour), claimedAt: now},
	} {
		if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID:        record.id,
			ProjectID: projectID,
			Kind:      jobs.KindCanonTextIndex,
			Status:    jobs.StatusRunning,
			Progress:  0.5,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID,
				SourceView: governance.SourceViewDraft, CatalogRevision: 1,
			},
			CanCancel:     true,
			CreatorUserID: actorID,
			QueueClass:    jobs.QueueClassUserDemanded,
			MaxAttempts:   3,
			NextAttemptAt: record.claimedAt,
			CreatedAt:     record.createdAt,
			UpdatedAt:     record.claimedAt,
		}); err != nil {
			t.Fatalf("CreateJob(%s) error = %v", record.id, err)
		}
		if _, err := db.Pool.Exec(ctx, "UPDATE jobs SET claimed_at = clock_timestamp(), updated_at = clock_timestamp() WHERE id = $1", record.id); err != nil {
			t.Fatalf("set claimed_at for %s: %v", record.id, err)
		}
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE jobs SET claimed_at = clock_timestamp() - interval '16 minutes', updated_at = clock_timestamp() - interval '16 minutes' WHERE id = $1", staleJobID); err != nil {
		t.Fatalf("age stale job: %v", err)
	}

	beforeClaim := time.Now().UTC()
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	claim := claimDecision.Claim
	afterClaim := time.Now().UTC()
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	if claim.JobID != staleJobID || claim.ProjectID != projectID {
		t.Fatalf("worker claim = %#v, want stale running job %s", claim, staleJobID)
	}
	claimed, err := store.LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		t.Fatalf("LoadJobForWorkerClaim() error = %v", err)
	}
	if claimed.Status != jobs.StatusRunning || jobProgressForStoreTest(t, claimed.Progress) != 0.5 {
		t.Fatalf("claimed stale job = %#v, want running job without claim-time product payload mutation", claimed)
	}
	if claim.ClaimIncarnation != 1 {
		t.Fatalf("claimed stale job incarnation = %d, want 1", claim.ClaimIncarnation)
	}
	if got := jobStatusForStoreTest(t, db, activeJobID); got != jobs.StatusRunning {
		t.Fatalf("active running job status = %q, want %q", got, jobs.StatusRunning)
	}
	if got := jobClaimedAtForStoreTest(t, db, staleJobID); got.Before(beforeClaim) || got.After(afterClaim) {
		t.Fatalf("stale job claimed_at = %s, want DB time between %s and %s", got, beforeClaim, afterClaim)
	}
}

func TestJobsStoreHeartbeatWorkerClaimRequiresActiveReadableSourceOwner(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000970"
	projectID := "00000000-0000-4000-8000-000000000971"
	membershipID := "00000000-0000-4000-8000-000000000972"
	jobID := "00000000-0000-4000-8000-000000000973"
	now := time.Date(2026, 7, 14, 14, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-heartbeat-source@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Heartbeat Source", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleAdmin, now, false)
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID: jobID, ProjectID: projectID, Kind: jobs.KindCanonTextIndex, Status: jobs.StatusRunning,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		CreatorUserID: actorID, QueueClass: jobs.QueueClassUserDemanded, MaxAttempts: 3,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET claim_incarnation = 1, claimed_at = clock_timestamp()
		WHERE project_id = $1 AND id = $2
	`, projectID, jobID); err != nil {
		t.Fatal(err)
	}
	claim := jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: 1}
	if err := store.HeartbeatWorkerClaim(ctx, claim); err != nil {
		t.Fatalf("HeartbeatWorkerClaim(active) error = %v", err)
	}

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	_, updateErr := conn.Exec(ctx, `
		UPDATE projects
		SET deleted_at = $2::timestamptz, governance_retention_started_at = $2::timestamptz,
		    purge_after = $2::timestamptz + interval '7 days', purge_deadline = $2::timestamptz + interval '8 days',
		    lifecycle_generation = lifecycle_generation + 1
		WHERE id = $1
	`, projectID, now.Add(time.Minute))
	_, resetErr := conn.Exec(ctx, `SET session_replication_role = origin`)
	conn.Release()
	if updateErr != nil || resetErr != nil {
		t.Fatalf("delete project source: update=%v reset=%v", updateErr, resetErr)
	}
	if err := store.HeartbeatWorkerClaim(ctx, claim); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("HeartbeatWorkerClaim(deleted source) error = %v, want ErrConflict", err)
	}
}

func TestJobsStoreClaimNextQueuedJobReclaimsStaleCancelingWithoutRestarting(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000965"
	projectID := "00000000-0000-4000-8000-000000000966"
	membershipID := "00000000-0000-4000-8000-000000000967"
	jobID := "00000000-0000-4000-8000-000000000968"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	previousClaimedAt := now
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-claim-canceling@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Claim Canceling Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleAdmin, now, false)
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID:        jobID,
		ProjectID: projectID,
		Kind:      jobs.KindCanonTextIndex,
		Status:    jobs.StatusCanceling,
		Progress:  0.65,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		CanCancel:     true,
		CreatorUserID: actorID,
		QueueClass:    jobs.QueueClassUserDemanded,
		MaxAttempts:   3,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     previousClaimedAt,
	}); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE jobs SET claimed_at = clock_timestamp() - interval '16 minutes', updated_at = clock_timestamp() - interval '16 minutes', cancel_requested_at = clock_timestamp() - interval '17 minutes' WHERE id = $1", jobID); err != nil {
		t.Fatalf("set claimed_at: %v", err)
	}

	beforeClaim := time.Now().UTC()
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
	claim := claimDecision.Claim
	afterClaim := time.Now().UTC()
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	claimed, err := store.LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		t.Fatalf("LoadJobForWorkerClaim() error = %v", err)
	}
	if claim.JobID != jobID || claimed.Status != jobs.StatusCanceling {
		t.Fatalf("claim = %#v job = %#v, want stale canceling job", claim, claimed)
	}
	if jobProgressForStoreTest(t, claimed.Progress) != 0.65 {
		t.Fatalf("claimed canceling progress = %v, want preserved 0.65", claimed.Progress)
	}
	if got := jobClaimedAtForStoreTest(t, db, jobID); got.Before(beforeClaim) || got.After(afterClaim) {
		t.Fatalf("canceling job claimed_at = %s, want DB time between %s and %s", got, beforeClaim, afterClaim)
	}
}

func TestJobsStorePersistsListsAndLooksUpJobsForProjectMembers(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000910"
	outsiderID := "00000000-0000-4000-8000-000000000911"
	projectID := "00000000-0000-4000-8000-000000000912"
	membershipID := "00000000-0000-4000-8000-000000000913"
	jobID := "00000000-0000-4000-8000-000000000914"
	otherProjectID := "00000000-0000-4000-8000-000000000915"
	otherMembershipID := "00000000-0000-4000-8000-000000000916"
	otherJobID := "00000000-0000-4000-8000-000000000917"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID, outsiderID}, projectID)
	cleanupJobsStoreRows(t, db, []string{actorID}, otherProjectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-member@example.com")
	insertProjectStoreUserForTest(t, db, outsiderID, "jobs-outsider@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Project", now)
	insertProjectStoreProjectForTest(t, db, otherProjectID, actorID, "Other Jobs Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, now, false)
	insertProjectStoreMembershipForTest(t, db, otherMembershipID, otherProjectID, actorID, projects.RoleEditor, now, false)

	created, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID:        jobID,
		ProjectID: projectID,
		Kind:      jobs.KindCanonTextIndex,
		Status:    jobs.StatusQueued,
		Progress:  0.25,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		CanCancel:     true,
		CreatorUserID: actorID,
		QueueClass:    jobs.QueueClassUserDemanded,
		MaxAttempts:   3,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID: otherJobID, ProjectID: otherProjectID, Kind: jobs.KindCanonTextIndex,
		Status: jobs.StatusQueued, Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: otherProjectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		CanCancel: true, CreatorUserID: actorID, QueueClass: jobs.QueueClassUserDemanded,
		MaxAttempts: 3, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateJob(other project) error = %v", err)
	}
	if created.ID != jobID || created.Status != jobs.StatusQueued || jobProgressForStoreTest(t, created.Progress) != 0.25 || !created.CanCancel {
		t.Fatalf("created job = %#v, want queued cancellable job with progress", created)
	}
	var ownerReadable bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT ck_artifact_source_owner_is_readable(artifact_source_owner_id, project_id, 'job', id)
		FROM jobs
		WHERE id = $1 AND project_id = $2
	`, jobID, projectID).Scan(&ownerReadable); err != nil {
		t.Fatalf("read direct job governance owner: %v", err)
	}
	if !ownerReadable {
		t.Fatal("direct job governance owner is not readable")
	}

	listed, err := store.ListProjectJobs(ctx, jobs.ListProjectJobsStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
		Kind:        jobs.KindCanonTextIndex,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListProjectJobs() error = %v", err)
	}
	if listed.Count != 1 || len(listed.Readable) != 1 || listed.Readable[0].ID != jobID ||
		listed.LastID != jobID || !listed.LastCreatedAt.Equal(now) {
		t.Fatalf("listed jobs = %#v, want created job", listed)
	}

	access, err := store.ResolveJobAccessForActor(ctx, jobs.ResolveJobAccessForActorStoreInput{ActorUserID: actorID, JobID: jobID})
	if err != nil {
		t.Fatalf("ResolveJobAccessForActor(member) error = %v", err)
	}
	if access.ProjectID != projectID || access.JobID != jobID || access.CurrentRole != projects.RoleEditor || access.Visibility != jobs.JobAccessVisibilityActive {
		t.Fatalf("resolved job access = %#v, want exact active project/job/editor tuple", access)
	}
	read, err := store.GetJob(ctx, jobs.GetJobStoreInput{
		ActorUserID: actorID, ProjectID: access.ProjectID, JobID: access.JobID,
	})
	if err != nil {
		t.Fatalf("GetJob(member) error = %v", err)
	}
	if read.ProjectID != projectID || read.Subject.Project() != projectID {
		t.Fatalf("read job = %#v, want project subject", read)
	}
	if read.CreatorUserID != actorID || !read.CanCancel {
		t.Fatalf("read job creator/cancel = %q/%v, want %q/true", read.CreatorUserID, read.CanCancel, actorID)
	}
	if _, err := store.GetJob(ctx, jobs.GetJobStoreInput{
		ActorUserID: actorID, ProjectID: otherProjectID, JobID: access.JobID,
	}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("GetJob(project substitution) error = %v, want ErrNotFound", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE project_memberships SET removed_at = $3
		WHERE id = $1 AND project_id = $2
	`, membershipID, projectID, now.Add(time.Second)); err != nil {
		t.Fatalf("remove member after resolver: %v", err)
	}
	if _, err := store.GetJob(ctx, jobs.GetJobStoreInput{
		ActorUserID: actorID, ProjectID: access.ProjectID, JobID: access.JobID,
	}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("GetJob(role removal race) error = %v, want ErrNotFound", err)
	}

	_, err = store.ResolveJobAccessForActor(ctx, jobs.ResolveJobAccessForActorStoreInput{ActorUserID: outsiderID, JobID: jobID})
	if !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("ResolveJobAccessForActor(non-member) error = %v, want ErrNotFound", err)
	}
}

func TestJobsStoreDoesNotExposeJobsForDeletedProjects(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000915"
	projectID := "00000000-0000-4000-8000-000000000916"
	membershipID := "00000000-0000-4000-8000-000000000917"
	jobID := "00000000-0000-4000-8000-000000000918"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-deleted-project@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Deleted Jobs Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID:        jobID,
		ProjectID: projectID,
		Kind:      jobs.KindCanonTextIndex,
		Status:    jobs.StatusQueued,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		CanCancel:     true,
		CreatorUserID: actorID,
		QueueClass:    jobs.QueueClassUserDemanded,
		MaxAttempts:   3,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	fenceProjectDeletionForTest(t, db, actorID, projectID, now.Add(time.Minute))

	listed, err := store.ListProjectJobs(ctx, jobs.ListProjectJobsStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListProjectJobs(deleted project) error = %v", err)
	}
	if listed.Count != 0 || len(listed.Readable) != 0 {
		t.Fatalf("ListProjectJobs(deleted project) = %#v, want empty list", listed)
	}
	_, err = store.ResolveJobAccessForActor(ctx, jobs.ResolveJobAccessForActorStoreInput{ActorUserID: actorID, JobID: jobID})
	if !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("ResolveJobAccessForActor(deleted project) error = %v, want ErrNotFound", err)
	}
}

func TestJobsStoreUsesImportRequestCreatorAndCanCancel(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	creatorID := "00000000-0000-4000-8000-000000000924"
	projectID := "00000000-0000-4000-8000-000000000925"
	membershipID := "00000000-0000-4000-8000-000000000926"
	jobID := "00000000-0000-4000-8000-000000000927"
	uploadArtifactID := "00000000-0000-4000-8000-000000000928"
	uploadOwnerID := "00000000-0000-4000-8000-000000000929"
	requestOwnerID := "00000000-0000-4000-8000-00000000092a"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	storageKey := "jobs/import/request/source.fb2"
	cleanupJobsStoreRows(t, db, []string{creatorID}, projectID)
	insertProjectStoreUserForTest(t, db, creatorID, "jobs-import-creator@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, creatorID, "Import Jobs Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, creatorID, projects.RoleEditor, now, false)
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID:            jobID,
		ProjectID:     projectID,
		Kind:          jobs.KindImport,
		Status:        jobs.StatusQueued,
		Subject:       jobs.ImportSubject{Type: jobs.KindImport, ID: projectID, ProjectID: projectID},
		CanCancel:     true,
		CreatorUserID: creatorID,
		QueueClass:    jobs.QueueClassUserDemanded,
		MaxAttempts:   3,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	uploadSource := modelTerminalOwnerInput(t, projectID, uploadArtifactID, uploadOwnerID, governance.ArtifactImportUpload)
	requestSource := modelTerminalOwnerInput(t, projectID, jobID, requestOwnerID, governance.ArtifactImportJobRequest)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		writer := governancestore.New(db)
		if err := writer.CreateArtifactWithSources(ctx, tx, uploadSource,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				_, err := tx.Exec(ctx, `
					INSERT INTO import_uploads (
						storage_key, project_id, mime_type, size_bytes, sha256, content,
						expires_at, created_at, artifact_id, artifact_source_owner_id
					) VALUES (
						$1, $2, 'application/x-fictionbook+xml', 42, 'abc123', '\x01'::bytea,
						$3, $4, $5, $6
					)
				`, storageKey, projectID, now.Add(time.Hour), now, uploadArtifactID, ownerID)
				return err
			}); err != nil {
			return err
		}
		return writer.CreateArtifactWithSources(ctx, tx, requestSource,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				_, err := tx.Exec(ctx, `
					INSERT INTO import_job_requests (
						job_id, project_id, actor_user_id, storage_key, idempotency_key, request_method, request_path,
						request_fingerprint, source_file_name, extension, mime_type, size_bytes, file_sha256,
						import_mode, can_cancel, created_at, updated_at, artifact_source_owner_id
					)
					VALUES ($1, $2, $3, $4, 'idem-1', 'POST', '/projects/project/imports',
						'fingerprint', 'source.fb2', '.fb2', 'application/x-fictionbook+xml', 42, 'abc123',
						'new_book', true, $5, $5, $6)
				`, jobID, projectID, creatorID, storageKey, now, ownerID)
				return err
			})
	}); err != nil {
		t.Fatalf("insert owner-backed import artifacts: %v", err)
	}

	access, err := store.ResolveJobAccessForActor(ctx, jobs.ResolveJobAccessForActorStoreInput{ActorUserID: creatorID, JobID: jobID})
	if err != nil {
		t.Fatalf("ResolveJobAccessForActor(import creator) error = %v", err)
	}
	read, err := store.GetJob(ctx, jobs.GetJobStoreInput{
		ActorUserID: creatorID, ProjectID: access.ProjectID, JobID: access.JobID,
	})
	if err != nil {
		t.Fatalf("GetJob(import creator) error = %v", err)
	}
	if read.CreatorUserID != creatorID || !read.CanCancel {
		t.Fatalf("import job creator/cancel = %q/%v, want %q/true", read.CreatorUserID, read.CanCancel, creatorID)
	}

	canceled, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: creatorID,
		ProjectID:   projectID,
		JobID:       jobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CancelJobStatus(import request creator) error = %v", err)
	}
	if canceled.Status != jobs.StatusCanceled {
		t.Fatalf("CancelJobStatus(import request creator) status = %q, want %q", canceled.Status, jobs.StatusCanceled)
	}
}

func jobProgressForStoreTest(t *testing.T, progress *float64) float64 {
	t.Helper()
	if progress == nil {
		t.Fatal("job progress is nil")
	}
	return *progress
}

func TestJobsStoreCancelJobStatusAllowsCreatorAndProjectAdmins(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	creatorID := "00000000-0000-4000-8000-000000000930"
	adminID := "00000000-0000-4000-8000-000000000931"
	projectID := "00000000-0000-4000-8000-000000000932"
	creatorMembershipID := "00000000-0000-4000-8000-000000000933"
	adminMembershipID := "00000000-0000-4000-8000-000000000934"
	creatorJobID := "00000000-0000-4000-8000-000000000935"
	adminJobID := "00000000-0000-4000-8000-000000000936"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	updatedAt := now.Add(time.Hour)
	cleanupJobsStoreRows(t, db, []string{creatorID, adminID}, projectID)
	insertProjectStoreUserForTest(t, db, creatorID, "jobs-cancel-creator@example.com")
	insertProjectStoreUserForTest(t, db, adminID, "jobs-cancel-admin@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, creatorID, "Jobs Cancel Project", now)
	insertProjectStoreMembershipForTest(t, db, creatorMembershipID, projectID, creatorID, projects.RoleViewer, now, false)
	insertProjectStoreMembershipForTest(t, db, adminMembershipID, projectID, adminID, projects.RoleAdmin, now, false)
	for _, record := range []struct {
		jobID         string
		sourceView    governance.SourceView
		canCancel     bool
		creatorUserID string
	}{
		{jobID: creatorJobID, sourceView: governance.SourceViewDraft, canCancel: true, creatorUserID: creatorID},
		{jobID: adminJobID, sourceView: governance.SourceViewPublished, canCancel: false},
	} {
		if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID:        record.jobID,
			ProjectID: projectID,
			Kind:      jobs.KindCanonTextIndex,
			Status:    jobs.StatusQueued,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID,
				SourceView: record.sourceView, CatalogRevision: 1,
			},
			CanCancel:     record.canCancel,
			CreatorUserID: record.creatorUserID,
			QueueClass:    jobs.QueueClassUserDemanded,
			MaxAttempts:   3,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			t.Fatalf("CreateJob(%s) error = %v", record.jobID, err)
		}
	}

	creatorCanceled, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: creatorID,
		ProjectID:   projectID,
		JobID:       creatorJobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		t.Fatalf("CancelJobStatus(creator) error = %v", err)
	}
	if creatorCanceled.Status != jobs.StatusCanceled {
		t.Fatalf("CancelJobStatus(creator) status = %q, want %q", creatorCanceled.Status, jobs.StatusCanceled)
	}

	adminCanceled, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: adminID,
		ProjectID:   projectID,
		JobID:       adminJobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		t.Fatalf("CancelJobStatus(admin) error = %v", err)
	}
	if adminCanceled.Status != jobs.StatusCanceled {
		t.Fatalf("CancelJobStatus(admin) status = %q, want %q", adminCanceled.Status, jobs.StatusCanceled)
	}
}

func TestJobsStoreCancelJobStatusEnforcesActorProjectBoundary(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	creatorID := "00000000-0000-4000-8000-000000000940"
	viewerID := "00000000-0000-4000-8000-000000000941"
	removedID := "00000000-0000-4000-8000-000000000942"
	projectID := "00000000-0000-4000-8000-000000000943"
	creatorMembershipID := "00000000-0000-4000-8000-000000000944"
	viewerMembershipID := "00000000-0000-4000-8000-000000000945"
	removedMembershipID := "00000000-0000-4000-8000-000000000946"
	viewerDeniedJobID := "00000000-0000-4000-8000-000000000947"
	removedDeniedJobID := "00000000-0000-4000-8000-000000000948"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	updatedAt := now.Add(time.Hour)
	cleanupJobsStoreRows(t, db, []string{creatorID, viewerID, removedID}, projectID)
	insertProjectStoreUserForTest(t, db, creatorID, "jobs-cancel-boundary-creator@example.com")
	insertProjectStoreUserForTest(t, db, viewerID, "jobs-cancel-boundary-viewer@example.com")
	insertProjectStoreUserForTest(t, db, removedID, "jobs-cancel-boundary-removed@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, creatorID, "Jobs Cancel Boundary Project", now)
	insertProjectStoreMembershipForTest(t, db, creatorMembershipID, projectID, creatorID, projects.RoleOwner, now, false)
	insertProjectStoreMembershipForTest(t, db, viewerMembershipID, projectID, viewerID, projects.RoleViewer, now, false)
	insertProjectStoreMembershipForTest(t, db, removedMembershipID, projectID, removedID, projects.RoleAdmin, now, true)
	for _, record := range []struct {
		jobID   string
		kind    jobs.Kind
		subject jobs.Subject
	}{
		{
			jobID: viewerDeniedJobID, kind: jobs.KindCanonTextIndex,
			subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID,
				SourceView: governance.SourceViewDraft, CatalogRevision: 1,
			},
		},
		{
			jobID: removedDeniedJobID, kind: jobs.KindCanonTextIndex,
			subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID,
				SourceView: governance.SourceViewPublished, CatalogRevision: 1,
			},
		},
	} {
		if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
			ID:            record.jobID,
			ProjectID:     projectID,
			Kind:          record.kind,
			Status:        jobs.StatusQueued,
			Subject:       record.subject,
			CanCancel:     true,
			CreatorUserID: creatorID,
			QueueClass:    jobs.QueueClassUserDemanded,
			MaxAttempts:   3,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			t.Fatalf("CreateJob(%s) error = %v", record.jobID, err)
		}
	}

	_, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: viewerID,
		ProjectID:   projectID,
		JobID:       viewerDeniedJobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   updatedAt,
	})
	if !errors.Is(err, jobs.ErrForbidden) {
		t.Fatalf("CancelJobStatus(non-creator viewer) error = %v, want ErrForbidden", err)
	}
	if got := jobStatusForStoreTest(t, db, viewerDeniedJobID); got != jobs.StatusQueued {
		t.Fatalf("job status after viewer denied cancel = %q, want %q", got, jobs.StatusQueued)
	}

	_, err = store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: removedID,
		ProjectID:   projectID,
		JobID:       removedDeniedJobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   updatedAt,
	})
	if !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("CancelJobStatus(removed member) error = %v, want ErrNotFound", err)
	}
	if got := jobStatusForStoreTest(t, db, removedDeniedJobID); got != jobs.StatusQueued {
		t.Fatalf("job status after removed member denied cancel = %q, want %q", got, jobs.StatusQueued)
	}

	fenceProjectDeletionForTest(t, db, creatorID, projectID, now.Add(time.Minute))
	_, err = store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: creatorID,
		ProjectID:   projectID,
		JobID:       viewerDeniedJobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   updatedAt,
	})
	if !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("CancelJobStatus(deleted project) error = %v, want ErrNotFound", err)
	}
	if got := jobStatusForStoreTest(t, db, viewerDeniedJobID); got != jobs.StatusQueued {
		t.Fatalf("job status after deleted project denied cancel = %q, want %q", got, jobs.StatusQueued)
	}
}

func TestJobsStoreCancelJobStatusRequiresExpectedCurrentStatus(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000950"
	projectID := "00000000-0000-4000-8000-000000000951"
	membershipID := "00000000-0000-4000-8000-000000000952"
	jobID := "00000000-0000-4000-8000-000000000953"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	updatedAt := now.Add(time.Hour)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-cancel-conflict@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Cancel Conflict Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
	if _, err := store.createJobForTest(ctx, jobs.CreateJobStoreInput{
		ID:        jobID,
		ProjectID: projectID,
		Kind:      jobs.KindCanonTextIndex,
		Status:    jobs.StatusRunning,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		CanCancel:     true,
		CreatorUserID: actorID,
		QueueClass:    jobs.QueueClassUserDemanded,
		MaxAttempts:   3,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}

	_, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
		JobID:       jobID,
		From:        jobs.StatusQueued,
		To:          jobs.StatusCanceled,
		Progress:    0,
		UpdatedAt:   updatedAt,
	})
	if !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("CancelJobStatus(wrong from) error = %v, want ErrConflict", err)
	}
	if got := jobStatusForStoreTest(t, db, jobID); got != jobs.StatusRunning {
		t.Fatalf("job status after conflicting cancel = %q, want %q", got, jobs.StatusRunning)
	}
}

func TestJobsStoreCancelJobStatusSupportsCanonTextIndex(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000954"
	projectID := "00000000-0000-4000-8000-000000000955"
	membershipID := "00000000-0000-4000-8000-000000000956"
	jobID := "00000000-0000-4000-8000-000000000957"
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-cancel-future@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Cancel Future Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
	draftView, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
	}, draftView, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, _, err := store.UpsertCanonTextIndexIntent(ctx, tx, jobs.CanonTextIndexIntentInput{
			JobID: jobID, CreateIfMissing: true,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID,
				SourceView: governance.SourceViewDraft, CatalogRevision: 1,
			},
			CreatorUserID: actorID,
			Source: jobs.JobSourceManifest{
				SourceView: draftView, Refs: []governance.ArtifactSourceRef{ref},
			},
			CreatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("UpsertCanonTextIndexIntent() error = %v", err)
	}
	canceled, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: actorID, ProjectID: projectID, JobID: jobID,
		From: jobs.StatusQueued, To: jobs.StatusCanceled, UpdatedAt: now.Add(time.Hour),
	})
	if err != nil || canceled.Status != jobs.StatusCanceled {
		t.Fatalf("CancelJobStatus(queued canonical) = %#v, %v", canceled, err)
	}

	runningJobID := "00000000-0000-4000-8000-000000000958"
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, _, err := store.UpsertCanonTextIndexIntent(ctx, tx, jobs.CanonTextIndexIntentInput{
			JobID:           runningJobID,
			CreateIfMissing: true,
			Subject:         jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: projectID, SourceView: governance.SourceViewDraft, CatalogRevision: 2},
			CreatorUserID:   actorID,
			Source:          jobs.JobSourceManifest{SourceView: draftView, Refs: []governance.ArtifactSourceRef{ref}},
			CreatedAt:       now.Add(2 * time.Hour),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status='running', claim_incarnation=1, claimed_at=clock_timestamp() WHERE project_id=$1 AND id=$2`, projectID, runningJobID); err != nil {
		t.Fatal(err)
	}
	canceling, err := store.CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
		ActorUserID: actorID, ProjectID: projectID, JobID: runningJobID,
		From: jobs.StatusRunning, To: jobs.StatusCanceling, UpdatedAt: now.Add(3 * time.Hour),
	})
	if err != nil || canceling.Status != jobs.StatusCanceling {
		t.Fatalf("CancelJobStatus(running canonical) = %#v, %v", canceling, err)
	}
}

func TestJobsStoreModelOwnerCleanupTerminalizesAtomically(t *testing.T) {
	tests := []struct {
		name   string
		status jobs.Status
		input  modelTerminalJobInTxInput
		gone   bool
	}{
		{name: "queued cancel", status: jobs.StatusQueued, input: modelTerminalJobInTxInput{from: jobs.StatusQueued, to: jobs.StatusCanceled, queuedCancel: true}},
		{name: "running cancel completion", status: jobs.StatusCanceling, input: modelTerminalJobInTxInput{from: jobs.StatusCanceling, to: jobs.StatusCanceled, claimIncarnation: 1}},
		{name: "succeeded", status: jobs.StatusRunning, input: modelTerminalJobInTxInput{from: jobs.StatusRunning, to: jobs.StatusSucceeded, claimIncarnation: 1}},
		{name: "failed", status: jobs.StatusRunning, input: modelTerminalJobInTxInput{from: jobs.StatusRunning, to: jobs.StatusFailed, claimIncarnation: 1}},
		{name: "immediate retry exhaustion", status: jobs.StatusRunning, input: modelTerminalJobInTxInput{from: jobs.StatusRunning, to: jobs.StatusFailed, claimIncarnation: 1}},
		{name: "stale process exhaustion", status: jobs.StatusRunning, input: modelTerminalJobInTxInput{from: jobs.StatusRunning, to: jobs.StatusFailed, claimIncarnation: 1}},
		{name: "source deletion", status: jobs.StatusRunning, input: modelTerminalJobInTxInput{to: jobs.StatusExpired, sourceDeleted: true}},
		{name: "parent purge", status: jobs.StatusRunning, input: modelTerminalJobInTxInput{purge: true}, gone: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelTerminalFixture(t, test.status)
			input := test.input
			input.projectID = mustUUID(fixture.projectID)
			input.jobID = mustUUID(fixture.jobID)
			if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
				return terminalizeModelJobInTx(fixture.ctx, tx, input, fixture.scrub)
			}); err != nil {
				t.Fatalf("terminalizeModelJobInTx() error = %v", err)
			}
			wantStatus := test.input.to
			if test.input.sourceDeleted {
				wantStatus = jobs.StatusExpired
			}
			assertModelTerminalFixtureState(t, fixture, wantStatus, false, false, !test.gone)
			if test.input.sourceDeleted {
				var subjectPresent, creatorPresent, canCancel, resultPresent, errorPresent, ownerPresent bool
				var progress, payloadState string
				if err := fixture.db.Pool.QueryRow(fixture.ctx, `
					SELECT subject_json IS NOT NULL, creator_user_id IS NOT NULL, can_cancel,
					       result_json IS NOT NULL, error_json IS NOT NULL,
					       artifact_source_owner_id IS NOT NULL, progress_json::text,
					       governance_payload_state
					FROM jobs WHERE id = $1 AND project_id = $2
				`, fixture.jobID, fixture.projectID).Scan(
					&subjectPresent, &creatorPresent, &canCancel, &resultPresent, &errorPresent,
					&ownerPresent, &progress, &payloadState,
				); err != nil {
					t.Fatal(err)
				}
				if subjectPresent || creatorPresent || canCancel || resultPresent || errorPresent || ownerPresent ||
					progress != "{}" || payloadState != "source_deleted" {
					t.Fatalf("source-deleted model tombstone retained identity or payload")
				}
				store := New(fixture.db)
				if _, err := store.GetJob(fixture.ctx, jobs.GetJobStoreInput{
					ActorUserID: fixture.actorID, ProjectID: fixture.projectID, JobID: fixture.jobID,
				}); !errors.Is(err, jobs.ErrNotFound) {
					t.Fatalf("GetJob(source-deleted model) error = %v, want ErrNotFound", err)
				}
				listed, err := store.ListProjectJobs(fixture.ctx, jobs.ListProjectJobsStoreInput{
					ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
					Kind: jobs.KindResearchChatTurn,
				})
				// The tombstone stays a position in the list but is neither readable nor decoded.
				if err != nil || listed.Count != 1 || len(listed.Readable) != 0 || listed.LastID != fixture.jobID {
					t.Fatalf("ListProjectJobs(source-deleted model) = %#v, %v, want one unreadable candidate", listed, err)
				}
				if _, err := store.LoadJobForWorkerClaim(fixture.ctx, jobs.WorkerClaim{
					ProjectID: fixture.projectID, JobID: fixture.jobID, ClaimIncarnation: 1,
				}); !errors.Is(err, jobs.ErrConflict) {
					t.Fatalf("LoadJobForWorkerClaim(source-deleted model) error = %v, want ErrConflict", err)
				}
			}
		})
	}
}

func TestJobsStoreModelOwnerCleanupRejectsIncompleteCleanup(t *testing.T) {
	injected := errors.New("injected private scrub failure")
	tests := []struct {
		name   string
		mutate func(modelTerminalFixture, *modelTerminalJobInTxInput) scrubPrivateInputInTx
		want   error
	}{
		{"nil callback", func(_ modelTerminalFixture, _ *modelTerminalJobInTxInput) scrubPrivateInputInTx { return nil }, jobs.ErrModelOwnerCleanupRequired},
		{"stale incarnation", func(f modelTerminalFixture, input *modelTerminalJobInTxInput) scrubPrivateInputInTx {
			input.claimIncarnation = 2
			return f.scrub
		}, jobs.ErrConflict},
		{"foreign project", func(f modelTerminalFixture, input *modelTerminalJobInTxInput) scrubPrivateInputInTx {
			input.projectID = mustUUID("00000000-0000-4000-8000-000000009999")
			return f.scrub
		}, jobs.ErrConflict},
		{"unreadable owner", func(f modelTerminalFixture, _ *modelTerminalJobInTxInput) scrubPrivateInputInTx {
			if _, err := f.db.Pool.Exec(f.ctx, `
				UPDATE artifact_source_owners
				SET read_state = 'source_deleted', fenced_at = clock_timestamp()
				WHERE project_id = $1
				  AND id = (SELECT artifact_source_owner_id FROM jobs WHERE project_id = $1 AND id = $2)
			`, f.projectID, f.jobID); err != nil {
				t.Fatalf("make model job owner unreadable: %v", err)
			}
			return f.scrub
		}, jobs.ErrConflict},
		{"callback failure", func(f modelTerminalFixture, _ *modelTerminalJobInTxInput) scrubPrivateInputInTx {
			return func(ctx context.Context, tx pgx.Tx, binding jobs.ModelBinding) (pgtype.UUID, error) {
				ownerID, err := f.scrub(ctx, tx, binding)
				if err != nil {
					return pgtype.UUID{}, err
				}
				return ownerID, injected
			}
		}, injected},
		{"binding only", func(f modelTerminalFixture, _ *modelTerminalJobInTxInput) scrubPrivateInputInTx {
			return func(context.Context, pgx.Tx, jobs.ModelBinding) (pgtype.UUID, error) {
				return mustUUID(f.privateOwnerID), nil
			}
		}, jobs.ErrModelOwnerCleanupRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelTerminalFixture(t, jobs.StatusRunning)
			input := modelTerminalJobInTxInput{
				projectID: mustUUID(fixture.projectID), jobID: mustUUID(fixture.jobID),
				claimIncarnation: 1, from: jobs.StatusRunning, to: jobs.StatusFailed,
			}
			scrub := test.mutate(fixture, &input)
			err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
				return terminalizeModelJobInTx(fixture.ctx, tx, input, scrub)
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("terminalizeModelJobInTx() error = %v, want %v", err, test.want)
			}
			assertModelTerminalFixtureState(t, fixture, jobs.StatusRunning, true, true, true)
		})
	}
}

func TestJobsStoreModelRetryFenceLeavesPrivateStateByteForByte(t *testing.T) {
	for retryCount := 0; retryCount < jobs.MaxJobAttempts; retryCount++ {
		t.Run(fmt.Sprintf("retry_count_%d", retryCount), func(t *testing.T) {
			fixture := newModelTerminalFixture(t, jobs.StatusRunning)
			before := modelRetrySnapshot(t, fixture)
			_, err := New(fixture.db).RequeueWorkerClaim(fixture.ctx, jobs.RequeueWorkerClaimStoreInput{
				Claim: jobs.WorkerClaim{
					ProjectID: fixture.projectID, JobID: fixture.jobID, ClaimIncarnation: 1,
				},
				Kind: jobs.KindResearchChatTurn, ExpectedRetryCount: retryCount,
			})
			if !errors.Is(err, jobs.ErrModelRetryFenceRequired) {
				t.Fatalf("RequeueWorkerClaim() error = %v, want ErrModelRetryFenceRequired", err)
			}
			after := modelRetrySnapshot(t, fixture)
			for index := range before {
				if before[index] != after[index] {
					t.Fatalf("model retry mutated private snapshot item %d", index)
				}
			}
		})
	}
}

func TestJobsStoreModelEffectRejectsTargetManifestSubstitution(t *testing.T) {
	fixture := newModelTerminalFixture(t, jobs.StatusRunning)
	otherBookID := "00000000-0000-4000-8000-000000009899"
	insertBookForExportStoreTest(t, fixture.db, otherBookID, fixture.projectID, "Other Model Effect Target", time.Now().UTC())
	wrong := modelTerminalJobOwnerInput(t, fixture.projectID, otherBookID, fixture.jobID, fixture.jobOwnerID)
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return governancestore.New(fixture.db).ReplaceArtifactSources(fixture.ctx, tx, governancestore.ReplaceArtifactSourcesInput{
			SealArtifactSourcesInput: wrong, ExpectedManifest: fixture.jobOwner.Manifest,
		}, func(context.Context, pgx.Tx, string) error { return nil })
	}); err != nil {
		t.Fatalf("replace job owner manifest: %v", err)
	}
	before := modelRetrySnapshot(t, fixture)
	store := New(fixture.db)
	claim := modelClaimForFixture(fixture)
	err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return store.ValidateModelLateEffect(fixture.ctx, tx, claim, fixture.modelTargetLocker())
	})
	if !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("ValidateModelLateEffect(other manifest) error = %v, want ErrConflict", err)
	}
	assertModelRetrySnapshot(t, fixture, before)
}

func TestJobsStoreModelEffectRejectsClaimCancelAndBindingDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(modelTerminalFixture, *jobs.ModelClaim)
	}{
		{name: "cancel requested", mutate: func(f modelTerminalFixture, _ *jobs.ModelClaim) {
			if _, err := f.db.Pool.Exec(f.ctx, `UPDATE jobs SET cancel_requested_at = clock_timestamp() WHERE project_id = $1 AND id = $2`, f.projectID, f.jobID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "stale incarnation", mutate: func(_ modelTerminalFixture, claim *jobs.ModelClaim) {
			claim.Claim.ClaimIncarnation++
		}},
		{name: "binding version", mutate: func(f modelTerminalFixture, _ *jobs.ModelClaim) {
			if _, err := f.db.Pool.Exec(f.ctx, `UPDATE job_model_bindings SET input_contract_version = 2 WHERE project_id = $1 AND job_id = $2`, f.projectID, f.jobID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "deleted owner", mutate: func(f modelTerminalFixture, _ *jobs.ModelClaim) {
			if _, err := f.db.Pool.Exec(f.ctx, `UPDATE artifact_source_owners SET read_state = 'source_deleted', fenced_at = clock_timestamp() WHERE project_id = $1 AND id = $2`, f.projectID, f.jobOwnerID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelTerminalFixture(t, jobs.StatusRunning)
			claim := modelClaimForFixture(fixture)
			test.mutate(fixture, &claim)
			before := modelRetrySnapshot(t, fixture)
			store := New(fixture.db)
			err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
				return store.ValidateModelLateEffect(fixture.ctx, tx, claim, fixture.modelTargetLocker())
			})
			if !errors.Is(err, jobs.ErrConflict) {
				t.Fatalf("ValidateModelLateEffect(drift) error = %v, want ErrConflict", err)
			}
			assertModelRetrySnapshot(t, fixture, before)
		})
	}
}

func TestJobsStoreModelEffectResearchChatRetrySentinelRemainsTableFree(t *testing.T) {
	for _, publicDeltaCommitted := range []bool{false, true} {
		t.Run(fmt.Sprintf("public_delta_committed_%t", publicDeltaCommitted), func(t *testing.T) {
			fixture := newModelTerminalFixture(t, jobs.StatusRunning)
			claim := configureModelTerminalUserClaim(t, fixture, jobs.KindResearchChatTurn, jobs.ResearchChatTurnSubject{
				Type: jobs.KindResearchChatTurn, ProjectID: fixture.projectID,
				ChatID:     "00000000-0000-4000-8000-000000009811",
				TurnID:     "00000000-0000-4000-8000-000000009812",
				SourceView: governance.SourceViewDraft, Scope: "project",
			})
			if _, err := fixture.db.Pool.Exec(fixture.ctx, `
				ALTER TABLE sentinel_model_inputs
				ADD COLUMN public_delta_committed boolean NOT NULL DEFAULT false
			`); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Pool.Exec(fixture.ctx, `
				UPDATE sentinel_model_inputs SET public_delta_committed = $1
				WHERE project_id = $2 AND id = $3
			`, publicDeltaCommitted, fixture.projectID, fixture.privateInputID); err != nil {
				t.Fatal(err)
			}
			before := modelRetrySnapshot(t, fixture)
			ownerCallbackCalls := 0
			fakeFutureOwnerState := func() bool {
				ownerCallbackCalls++
				return publicDeltaCommitted
			}
			_ = fakeFutureOwnerState

			_, err := New(fixture.db).RequeueWorkerClaim(fixture.ctx, jobs.RequeueWorkerClaimStoreInput{
				Claim: claim.Claim, Kind: jobs.KindResearchChatTurn, ExpectedRetryCount: 0,
			})
			if !errors.Is(err, jobs.ErrModelRetryFenceRequired) {
				t.Fatalf("generic immediate retry error = %v, want ErrModelRetryFenceRequired", err)
			}
			assertModelRetrySnapshot(t, fixture, before)
			if _, err := fixture.db.Pool.Exec(fixture.ctx, `
				UPDATE jobs
				SET claimed_at = clock_timestamp() - interval '16 minutes',
				    updated_at = clock_timestamp() - interval '16 minutes'
				WHERE project_id = $1 AND id = $2
			`, fixture.projectID, fixture.jobID); err != nil {
				t.Fatal(err)
			}
			before = modelRetrySnapshot(t, fixture)
			// A stale chat turn is reclaimed under a new claim incarnation (R3.8); the run
			// reconciles its persisted native turn, so the private input stays untouched.
			decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindResearchChatTurn})
			if err != nil || decision.Claim.JobID != fixture.jobID || decision.Claim.ClaimIncarnation != claim.Claim.ClaimIncarnation+1 {
				t.Fatalf("stale reclaim = %#v, %v", decision, err)
			}
			if ownerCallbackCalls != 0 {
				t.Fatalf("generic SQL invoked future owner callback %d times", ownerCallbackCalls)
			}
			after := modelRetrySnapshot(t, fixture)
			if !slices.Equal(after[1:], before[1:]) {
				t.Fatal("stale reclaim mutated the private model input, owner, refs or binding")
			}
		})
	}
}

type modelStartFixture struct {
	ctx              context.Context
	db               *storedb.DB
	store            *Store
	actorID          string
	projectID        string
	bookID           string
	chapterID        string
	sessionID        string
	sessionHash      string
	csrfHash         string
	now              time.Time
	expectedManifest jobs.JobSourceManifest
}

func newModelStartFixture(t *testing.T) modelStartFixture {
	t.Helper()
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000009811"
	projectID := "00000000-0000-4000-8000-000000009812"
	bookID := "00000000-0000-4000-8000-000000009813"
	chapterID := "00000000-0000-4000-8000-000000009818"
	sessionID := "00000000-0000-4000-8000-000000009814"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "model-start@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Model Start Project", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000009815", projectID, actorID, projects.RoleEditor, now, false)
	insertBookForExportStoreTest(t, db, bookID, projectID, "Model Start Book", now)
	insertChapterForExportStoreTest(t, db, chapterID, bookID, "Model Start Chapter", 1, "draft", 1, nil, nil, now)
	insertSessionForTest(t, db, sessionID, actorID, "model-session-hash", "model-csrf-hash", now.Add(time.Hour), now)
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE sentinel_model_inputs (
		  id uuid PRIMARY KEY,
		  project_id uuid NOT NULL,
		  artifact_source_owner_id uuid NOT NULL,
		  body bytea NOT NULL
		)
	`); err != nil {
		t.Fatalf("create sentinel model input table: %v", err)
	}
	jobOwner := modelTerminalJobOwnerInput(
		t,
		projectID,
		bookID,
		"00000000-0000-4000-8000-000000009816",
		"00000000-0000-4000-8000-000000009817",
	)
	return modelStartFixture{
		ctx: ctx, db: db, store: New(db), actorID: actorID, projectID: projectID,
		bookID: bookID, chapterID: chapterID, sessionID: sessionID, sessionHash: "model-session-hash",
		csrfHash: "model-csrf-hash", now: now,
		expectedManifest: jobs.JobSourceManifest{SourceView: jobOwner.SourceView, Refs: jobOwner.Refs},
	}
}

type modelTerminalFixture struct {
	ctx              context.Context
	db               *storedb.DB
	actorID          string
	projectID        string
	bookID           string
	chapterID        string
	jobID            string
	jobOwnerID       string
	privateInputID   string
	privateOwnerID   string
	jobOwner         governancestore.SealArtifactSourcesInput
	expectedManifest jobs.JobSourceManifest
	sessionID        string
	subject          jobs.ResearchChatTurnSubject
}

func newModelTerminalFixture(t *testing.T, status jobs.Status) modelTerminalFixture {
	t.Helper()
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000009801"
	projectID := "00000000-0000-4000-8000-000000009802"
	membershipID := "00000000-0000-4000-8000-000000009803"
	bookID := "00000000-0000-4000-8000-000000009804"
	jobID := "00000000-0000-4000-8000-000000009805"
	jobOwnerID := "00000000-0000-4000-8000-000000009806"
	privateInputID := "00000000-0000-4000-8000-000000009807"
	privateOwnerID := "00000000-0000-4000-8000-000000009808"
	chapterID := "00000000-0000-4000-8000-000000009809"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "model-terminal@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Model Terminal Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, now, false)
	insertBookForExportStoreTest(t, db, bookID, projectID, "Model Terminal Book", now)
	insertChapterForExportStoreTest(t, db, chapterID, bookID, "Model Terminal Chapter", 1, "draft", 1, nil, nil, now)
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE sentinel_model_inputs (
		  id uuid PRIMARY KEY,
		  project_id uuid NOT NULL,
		  artifact_source_owner_id uuid NOT NULL,
		  body bytea NOT NULL
		)
	`); err != nil {
		t.Fatalf("create sentinel model input table: %v", err)
	}
	t.Cleanup(func() {
		if err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `
				DELETE FROM sentinel_model_inputs WHERE id = $1 AND project_id = $2
			`, privateInputID, projectID); err != nil {
				return err
			}
			if _, err := tx.Exec(context.Background(), `
				DELETE FROM import_uploads WHERE artifact_id = $1 AND project_id = $2
			`, privateInputID, projectID); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), `
				DELETE FROM artifact_source_owners WHERE id = $1 AND project_id = $2
			`, privateOwnerID, projectID)
			return err
		}); err != nil {
			t.Errorf("cleanup model terminal private input: %v", err)
		}
	})
	privateSource := modelTerminalOwnerInput(t, projectID, privateInputID, privateOwnerID, governance.ArtifactImportUpload)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return governancestore.New(db).CreateArtifactWithSources(ctx, tx, privateSource,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				if _, err := tx.Exec(ctx, `
					INSERT INTO import_uploads (
					  storage_key, project_id, mime_type, size_bytes, sha256, content,
					  expires_at, artifact_id, artifact_source_owner_id
					) VALUES ($1, $2, 'application/octet-stream', 0, 'sentinel', NULL,
					  clock_timestamp() + interval '1 hour', $3, $4)
				`, "model-private-"+privateInputID, projectID, privateInputID, ownerID); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `
					INSERT INTO sentinel_model_inputs (id, project_id, artifact_source_owner_id, body)
					VALUES ($1, $2, $3, 'private model input')
				`, privateInputID, projectID, ownerID)
				return err
			})
	}); err != nil {
		t.Fatalf("create sentinel model input: %v", err)
	}
	jobSource := modelTerminalJobOwnerInput(t, projectID, bookID, jobID, jobOwnerID)
	subject := jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: projectID, ChatID: userModelStartChatID,
		TurnID: "00000000-0000-4000-8000-000000009840", SourceView: governance.SourceViewDraft, Scope: "project",
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindResearchChatTurn, subject)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "00000000-0000-4000-8000-000000009813"
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, clock_timestamp() + interval '1 hour', clock_timestamp())
	`, sessionID, actorID, hashForAuthStoreTest(t, 181), hashForAuthStoreTest(t, 182)); err != nil {
		t.Fatalf("insert model job session: %v", err)
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return governancestore.New(db).CreateArtifactWithSources(ctx, tx, jobSource,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				claimedAt := any(nil)
				claimIncarnation := int64(0)
				cancelRequestedAt := any(nil)
				if status == jobs.StatusRunning || status == jobs.StatusCanceling {
					claimedAt = now
					claimIncarnation = 1
				}
				if status == jobs.StatusCanceling {
					cancelRequestedAt = now
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO jobs (
					  id, project_id, kind, status, progress_json, subject_json,
					  result_json, error_json,
					  creator_user_id, can_cancel, queue_class, retry_count, max_attempts,
					  next_attempt_at, claimed_at, claim_incarnation, cancel_requested_at,
					  artifact_source_owner_id, governance_payload_state, created_at, updated_at
					) VALUES (
					  $1, $2, 'research_chat_turn', $3, '{}'::jsonb, $4::jsonb,
					  NULL, NULL,
					  $10, true, 'interactive', 0, 3, $5, $6, $7, $8, $9, 'active', $5, $5
					)
				`, jobID, projectID, string(status), subjectJSON, now, claimedAt,
					claimIncarnation, cancelRequestedAt, ownerID, actorID); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `
					INSERT INTO job_model_bindings (
					  job_id, project_id, artifact_source_owner_id, job_kind, origin,
					  actor_user_id, initiating_auth_session_id, input_contract_version
					) VALUES ($1, $2, $3, 'research_chat_turn', 'user', $4, $5, 1)
				`, jobID, projectID, ownerID, actorID, sessionID)
				return err
			})
	}); err != nil {
		t.Fatalf("create model job and binding: %v", err)
	}
	return modelTerminalFixture{
		ctx: ctx, db: db, actorID: actorID, projectID: projectID, jobID: jobID,
		bookID: bookID, chapterID: chapterID, jobOwnerID: jobOwnerID,
		privateInputID: privateInputID, privateOwnerID: privateOwnerID, jobOwner: jobSource,
		expectedManifest: jobs.JobSourceManifest{SourceView: jobSource.SourceView, Refs: jobSource.Refs},
		sessionID:        sessionID, subject: subject,
	}
}

func modelTerminalOwnerInput(
	t *testing.T,
	projectID, artifactID, ownerID string,
	kind governance.ArtifactKind,
) governancestore.SealArtifactSourcesInput {
	t.Helper()
	ref, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
	}, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: kind, ProjectID: projectID, ArtifactID: artifactID,
		OwnerSourceView: governance.AbsentSourceView(), Refs: []governance.ArtifactSourceRef{ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	return governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: projectID, ArtifactID: artifactID, Kind: kind,
		SourceView: governance.AbsentSourceView(), Refs: []governance.ArtifactSourceRef{ref}, Manifest: manifest,
	}
}

func modelTerminalJobOwnerInput(
	t *testing.T,
	projectID, bookID, jobID, ownerID string,
) governancestore.SealArtifactSourcesInput {
	t.Helper()
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
		BookID: bookID, BookGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: projectID, ArtifactID: jobID,
		OwnerSourceView: view, Refs: []governance.ArtifactSourceRef{ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	return governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: projectID, ArtifactID: jobID, Kind: governance.ArtifactJob,
		SourceView: view, Refs: []governance.ArtifactSourceRef{ref}, Manifest: manifest,
	}
}

func (fixture modelTerminalFixture) claim() jobs.WorkerClaim {
	return jobs.WorkerClaim{ProjectID: fixture.projectID, JobID: fixture.jobID, ClaimIncarnation: 1}
}

func modelClaimForFixture(fixture modelTerminalFixture) jobs.ModelClaim {
	return jobs.ModelClaim{
		Claim: fixture.claim(), Kind: jobs.KindResearchChatTurn, Subject: fixture.subject,
		Origin: jobs.ModelOriginUser, ActorUserID: fixture.actorID,
		InitiatingAuthSessionID: fixture.sessionID, InputContractVersion: 1,
	}
}

// configureModelTerminalUserClaim gives the fixture's chat turn Job another subject and
// returns the user model claim of that Job.
func configureModelTerminalUserClaim(
	t *testing.T,
	fixture modelTerminalFixture,
	kind jobs.Kind,
	subject jobs.Subject,
) jobs.ModelClaim {
	t.Helper()
	subjectJSON, err := jobs.EncodeSubject(kind, subject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE jobs SET subject_json = $1::jsonb WHERE project_id = $2 AND id = $3
	`, subjectJSON, fixture.projectID, fixture.jobID); err != nil {
		t.Fatalf("configure user model claim: %v", err)
	}
	return jobs.ModelClaim{
		Claim: fixture.claim(), Kind: kind, Subject: subject, Origin: jobs.ModelOriginUser,
		ActorUserID: fixture.actorID, InitiatingAuthSessionID: fixture.sessionID, InputContractVersion: 1,
	}
}

func (fixture modelTerminalFixture) modelTargetLocker() lockModelTargetInTx {
	return func(ctx context.Context, tx pgx.Tx, subject jobs.Subject) (jobs.JobSourceManifest, error) {
		if typed, ok := subject.(jobs.ResearchChatTurnSubject); !ok || typed != fixture.subject {
			return jobs.JobSourceManifest{}, jobs.ErrConflict
		}
		var bookID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id FROM books
			WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL AND lifecycle_generation = 1
			FOR SHARE
		`, fixture.projectID, fixture.bookID).Scan(&bookID); errors.Is(err, pgx.ErrNoRows) {
			return jobs.JobSourceManifest{}, jobs.ErrConflict
		} else if err != nil {
			return jobs.JobSourceManifest{}, err
		}
		return fixture.expectedManifest, nil
	}
}

func assertModelRetrySnapshot(t *testing.T, fixture modelTerminalFixture, want []string) {
	t.Helper()
	got := modelRetrySnapshot(t, fixture)
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("model retry mutated private snapshot item %d", index)
		}
	}
}

func (fixture modelTerminalFixture) scrub(
	ctx context.Context,
	tx pgx.Tx,
	_ jobs.ModelBinding,
) (pgtype.UUID, error) {
	ownerID := mustUUID(fixture.privateOwnerID)
	deleted, err := tx.Exec(ctx, `
		DELETE FROM import_uploads
		WHERE artifact_id = $1 AND project_id = $2 AND artifact_source_owner_id = $3
	`, fixture.privateInputID, fixture.projectID, ownerID)
	if err != nil || deleted.RowsAffected() != 1 {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	deleted, err = tx.Exec(ctx, `
		DELETE FROM sentinel_model_inputs
		WHERE id = $1 AND project_id = $2 AND artifact_source_owner_id = $3
	`, fixture.privateInputID, fixture.projectID, ownerID)
	if err != nil || deleted.RowsAffected() != 1 {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	deleted, err = tx.Exec(ctx, `
		DELETE FROM artifact_source_owners
		WHERE id = $1 AND project_id = $2
	`, ownerID, fixture.projectID)
	if err != nil || deleted.RowsAffected() != 1 {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	return ownerID, nil
}

func modelRetrySnapshot(t *testing.T, fixture modelTerminalFixture) []string {
	t.Helper()
	var job, input, owner, refs, binding string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT
		  (SELECT to_jsonb(row_value)::text FROM jobs AS row_value WHERE project_id = $1 AND id = $2),
		  (SELECT to_jsonb(row_value)::text FROM sentinel_model_inputs AS row_value WHERE project_id = $1 AND id = $3),
		  (SELECT to_jsonb(row_value)::text FROM artifact_source_owners AS row_value WHERE project_id = $1 AND id = $4),
		  (SELECT COALESCE(jsonb_agg(to_jsonb(row_value)), '[]'::jsonb)::text FROM artifact_source_refs AS row_value WHERE project_id = $1 AND owner_id = $4),
		  (SELECT to_jsonb(row_value)::text FROM job_model_bindings AS row_value WHERE project_id = $1 AND job_id = $2)
	`, fixture.projectID, fixture.jobID, fixture.privateInputID, fixture.privateOwnerID).Scan(
		&job, &input, &owner, &refs, &binding,
	); err != nil {
		t.Fatalf("snapshot model retry state: %v", err)
	}
	return []string{job, input, owner, refs, binding}
}

func assertModelTerminalFixtureState(
	t *testing.T,
	fixture modelTerminalFixture,
	wantStatus jobs.Status,
	wantPrivate, wantBinding, wantJob bool,
) {
	t.Helper()
	var privateCount, ownerCount, refCount, bindingCount, jobCount int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT
		  (SELECT count(*) FROM sentinel_model_inputs WHERE id = $1 AND project_id = $2),
		  (SELECT count(*) FROM artifact_source_owners WHERE id = $3 AND project_id = $2),
		  (SELECT count(*) FROM artifact_source_refs WHERE owner_id = $3 AND project_id = $2),
		  (SELECT count(*) FROM job_model_bindings WHERE job_id = $4 AND project_id = $2),
		  (SELECT count(*) FROM jobs WHERE id = $4 AND project_id = $2)
	`, fixture.privateInputID, fixture.projectID, fixture.privateOwnerID, fixture.jobID).Scan(
		&privateCount, &ownerCount, &refCount, &bindingCount, &jobCount,
	); err != nil {
		t.Fatal(err)
	}
	boolCount := func(value bool) int {
		if value {
			return 1
		}
		return 0
	}
	if privateCount != boolCount(wantPrivate) || ownerCount != boolCount(wantPrivate) ||
		refCount != boolCount(wantPrivate) || bindingCount != boolCount(wantBinding) || jobCount != boolCount(wantJob) {
		t.Fatalf("model private state = input:%d owner:%d refs:%d binding:%d job:%d",
			privateCount, ownerCount, refCount, bindingCount, jobCount)
	}
	if wantJob {
		if got := jobStatusForStoreTest(t, fixture.db, fixture.jobID); got != wantStatus {
			t.Fatalf("model job status = %q, want %q", got, wantStatus)
		}
	}
}

type canonTextIntentFixture struct {
	ctx         context.Context
	db          *storedb.DB
	store       *Store
	projectID   string
	userID      string
	otherUserID string
	chatID      string
	now         time.Time
}

type canonTextIntentState struct {
	jobID         string
	creatorUserID string
	revision      uint64
	refCount      int
}

func newCanonTextIntentFixture(t *testing.T) canonTextIntentFixture {
	t.Helper()
	fixture := canonTextIntentFixture{
		ctx:         context.Background(),
		db:          openCurrentStoreTestDB(t),
		projectID:   "20000000-0000-4000-8000-000000000001",
		userID:      "20000000-0000-4000-8000-000000000002",
		otherUserID: "20000000-0000-4000-8000-000000000003",
		chatID:      "20000000-0000-4000-8000-000000000004",
		now:         time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}
	fixture.store = New(fixture.db)
	insertProjectStoreUserForTest(t, fixture.db, fixture.userID, "text-intent-user@example.com")
	insertProjectStoreUserForTest(t, fixture.db, fixture.otherUserID, "text-intent-other@example.com")
	insertProjectStoreProjectForTest(t, fixture.db, fixture.projectID, fixture.userID, "Text Intent Project", fixture.now)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title, lifecycle_epoch, created_at, updated_at)
		VALUES ($1, $2, 'draft', 'project', 'Text intent source', 1, $3, $3)
	`, fixture.chatID, fixture.projectID, fixture.now); err != nil {
		t.Fatalf("insert text intent source chat: %v", err)
	}
	return fixture
}

func (fixture canonTextIntentFixture) input(
	t *testing.T,
	jobID string,
	view governance.SourceView,
	revision uint64,
	refCount int,
	creatorUserID string,
) jobs.CanonTextIndexIntentInput {
	t.Helper()
	viewArm, err := governance.PresentSourceView(view)
	if err != nil {
		t.Fatal(err)
	}
	vector := governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 1,
	}
	projectRef, err := governance.NewViewBoundProjectRef(vector, viewArm, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	refs := []governance.ArtifactSourceRef{projectRef}
	if refCount == 2 {
		chat, err := governance.PresentChatArm(
			fixture.projectID, fixture.chatID, governance.ChatLifecycleEpoch(1),
		)
		if err != nil {
			t.Fatal(err)
		}
		chatRef, err := governance.NewViewBoundProjectRef(vector, viewArm, chat)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, chatRef)
	} else if refCount != 1 {
		t.Fatalf("unsupported synthetic text intent ref count %d", refCount)
	}
	return jobs.CanonTextIndexIntentInput{
		JobID: jobID, CreateIfMissing: true,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: fixture.projectID,
			SourceView: view, CatalogRevision: revision,
		},
		CreatorUserID: creatorUserID,
		Source: jobs.JobSourceManifest{
			SourceView: viewArm,
			Refs:       refs,
		},
		CreatedAt: fixture.now.Add(time.Duration(revision) * time.Second),
	}
}

func (fixture canonTextIntentFixture) upsert(t *testing.T, input jobs.CanonTextIndexIntentInput) (jobs.Job, error) {
	t.Helper()
	job, present, err := fixture.upsertWithStore(input, fixture.store)
	if err == nil && !present {
		t.Fatal("UpsertCanonTextIndexIntent returned absent Job for CreateIfMissing")
	}
	return job, err
}

func (fixture canonTextIntentFixture) upsertWithStore(
	input jobs.CanonTextIndexIntentInput,
	store *Store,
) (jobs.Job, bool, error) {
	var result jobs.Job
	var present bool
	err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		var err error
		result, present, err = store.UpsertCanonTextIndexIntent(fixture.ctx, tx, input)
		return err
	})
	return result, present, err
}

func (fixture canonTextIntentFixture) queuedState(t *testing.T, view governance.SourceView) canonTextIntentState {
	t.Helper()
	var state canonTextIntentState
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.id::text, COALESCE(job.creator_user_id::text, ''),
		       (job.subject_json->>'catalogRevision')::bigint, owner.ref_count
		FROM jobs AS job
		JOIN artifact_source_owners AS owner
		  ON owner.id = job.artifact_source_owner_id AND owner.project_id = job.project_id
		WHERE job.project_id = $1 AND job.kind = 'canon_text_index'
		  AND job.status = 'queued' AND job.claim_incarnation = 0
		  AND job.cancel_requested_at IS NULL AND job.subject_json->>'sourceView' = $2
	`, fixture.projectID, string(view)).Scan(
		&state.jobID, &state.creatorUserID, &state.revision, &state.refCount,
	); err != nil {
		t.Fatalf("read queued text intent: %v", err)
	}
	return state
}

func cleanupJobsStoreRows(t *testing.T, db *storedb.DB, userIDs []string, projectID string) {
	t.Helper()
	cleanup := func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM import_job_requests WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM jobs WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM import_uploads WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_invitations WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_memberships WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", projectID)
		for _, userID := range userIDs {
			_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
		}
	}
	t.Cleanup(cleanup)
	cleanup()
}

func countJobsStoreRowsForTest(t *testing.T, db *storedb.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func jobStatusForStoreTest(t *testing.T, db *storedb.DB, jobID string) jobs.Status {
	t.Helper()
	var status string
	if err := db.Pool.QueryRow(context.Background(), "SELECT status FROM jobs WHERE id = $1", jobID).Scan(&status); err != nil {
		t.Fatalf("read job status: %v", err)
	}
	return jobs.Status(status)
}

func jobClaimedAtForStoreTest(t *testing.T, db *storedb.DB, jobID string) time.Time {
	t.Helper()
	var claimedAt time.Time
	if err := db.Pool.QueryRow(context.Background(), "SELECT claimed_at FROM jobs WHERE id = $1", jobID).Scan(&claimedAt); err != nil {
		t.Fatalf("read job claimed_at: %v", err)
	}
	return claimedAt
}
