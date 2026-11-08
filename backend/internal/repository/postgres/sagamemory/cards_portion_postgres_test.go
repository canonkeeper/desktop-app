package sagamemory

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

const otherCardsProjectID = "93000000-0000-4000-8000-0000000000f2"

func claimCardsForTest(t *testing.T, db *storedb.DB) (string, bool) {
	t.Helper()
	var schema string
	if err := db.Pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	var jobID string
	err := db.Pool.QueryRow(context.Background(), `
		SELECT work_id::text FROM `+pgx.Identifier{schema + "_worker"}.Sanitize()+`.claim_next_queued_job_for_worker($1, NULL)
	`, []string{string(jobs.KindChapterCards)}).Scan(&jobID)
	if err == pgx.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return jobID, true
}

func queueCardsJobForTest(t *testing.T, db *storedb.DB, projectID string) string {
	t.Helper()
	ctx := context.Background()
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{ProjectID: projectID, ProjectGeneration: 1}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		job, err = jobstore.New(db).UpsertChapterCardsIntent(ctx, tx, jobs.ChapterCardsIntentInput{
			Subject: jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: governance.SourceViewDraft},
			Source:  jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}},
			// Claims compare next_attempt_at with the database clock.
			CreatedAt: time.Now().UTC().Add(-time.Second),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

// R12.2: a yielded cards Job is queued at once under the next claim incarnation with its
// attempts and progress, and the next claim takes it again.
func TestYieldCardsRequeuesUnderNextIncarnation(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_yield")
	seedMemoryProject(t, db)
	ctx := context.Background()
	claim := claimCardsJobForTest(t, db)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET progress_json='{"value":0.333}' WHERE id=$1`, claim.JobID); err != nil {
		t.Fatal(err)
	}
	if err := New(db).YieldCards(ctx, claim); err != nil {
		t.Fatalf("YieldCards() error = %v", err)
	}
	var status string
	var incarnation int64
	var retries int
	var progress float64
	if err := db.Pool.QueryRow(ctx, `
		SELECT status, claim_incarnation, retry_count, (progress_json->>'value')::double precision
		FROM jobs WHERE id=$1
	`, claim.JobID).Scan(&status, &incarnation, &retries, &progress); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || incarnation != 2 || retries != 0 || progress != 0.333 {
		t.Fatalf("yielded Job = %s, incarnation %d, retries %d, progress %v", status, incarnation, retries, progress)
	}
	if err := New(db).YieldCards(ctx, claim); err == nil {
		t.Fatal("a stale claim yielded the Job again")
	}
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != claim.JobID {
		t.Fatalf("next claim = %q %v, want the yielded Job", jobID, ok)
	}
}

// R12.4: a project runs one cards Job at a time; another project's Job is claimed meanwhile.
func TestCardsClaimSkipsProjectWithCardsJobInWork(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_one_cards")
	seedMemoryProject(t, db)
	ctx := context.Background()
	running := claimCardsJobForTest(t, db)
	waiting := queueCardsJobForTest(t, db, memoryProjectID)
	if waiting == running.JobID {
		t.Fatal("a second cards Job was not queued")
	}
	if jobID, ok := claimCardsForTest(t, db); ok {
		t.Fatalf("claimed %s while the project's cards Job is in work", jobID)
	}
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Other','',$3,$3)`,
		otherCardsProjectID, memoryOwnerID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'draft',1,1)`,
		otherCardsProjectID); err != nil {
		t.Fatal(err)
	}
	other := queueCardsJobForTest(t, db, otherCardsProjectID)
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != other {
		t.Fatalf("claim = %q %v, want the other project's Job %s", jobID, ok, other)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status='succeeded', completed_at=clock_timestamp(), can_cancel=false WHERE id=$1`, running.JobID); err != nil {
		t.Fatal(err)
	}
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != waiting {
		t.Fatalf("claim after the first finished = %q %v, want %s", jobID, ok, waiting)
	}
}
