package sagamemory

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

const otherCardsProjectID = "93000000-0000-4000-8000-0000000000f2"

// claimCardsQueryForTest is the claim a cards worker asks the queue with.
func claimCardsQueryForTest(t *testing.T, db *storedb.DB) (string, []any) {
	t.Helper()
	var schema string
	if err := db.Pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return `
		SELECT work_id::text, claim_incarnation FROM ` + pgx.Identifier{schema + "_worker"}.Sanitize() + `.claim_next_queued_job_for_worker($1, NULL)
	`, []any{[]string{string(jobs.KindChapterCards)}}
}

func claimCardsForTest(t *testing.T, db *storedb.DB) (string, bool) {
	t.Helper()
	query, args := claimCardsQueryForTest(t, db)
	var jobID string
	var incarnation int64
	err := db.Pool.QueryRow(context.Background(), query, args...).Scan(&jobID, &incarnation)
	if err == pgx.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return jobID, true
}

// claimedCardsForTest is what one worker got from the queue: a Job and its claim incarnation, or nothing.
type claimedCardsForTest struct {
	jobID       string
	incarnation int64
	none        bool
}

func (claimed *claimedCardsForTest) scan(row pgx.Row) error {
	err := row.Scan(&claimed.jobID, &claimed.incarnation)
	if err == pgx.ErrNoRows {
		claimed.none = true
		return nil
	}
	return err
}

func queueCardsJobForTest(t *testing.T, db *storedb.DB, projectID string) string {
	t.Helper()
	return queueCardsJobOfViewForTest(t, db, projectID, governance.SourceViewDraft)
}

// seedPublishedViewForTest gives the project its published text version, as the import that publishes a book does.
func seedPublishedViewForTest(t *testing.T, db *storedb.DB, projectID string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'published',1,1)`,
		projectID); err != nil {
		t.Fatal(err)
	}
}

func seedOtherCardsProjectForTest(t *testing.T, db *storedb.DB) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Other','',$3,$3)`,
		otherCardsProjectID, memoryOwnerID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'draft',1,1)`,
		otherCardsProjectID); err != nil {
		t.Fatal(err)
	}
}

func queueCardsJobOfViewForTest(t *testing.T, db *storedb.DB, projectID string, sourceView governance.SourceView) string {
	t.Helper()
	ctx := context.Background()
	view, err := governance.PresentSourceView(sourceView)
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
			Subject: jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: sourceView},
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
	seedOtherCardsProjectForTest(t, db)
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

// specs/2026-11-09-first-session.md, FS6.1: a running cards Job is told that somebody waits only by a queued cards Job
// of another project that a worker could take now — not by its own successor, not by a Job whose time has not come,
// and not by one whose project already runs cards.
func TestOthersWaitCountsOnlyCardsAnotherProjectCouldStartNow(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_others_wait")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	running := claimCardsJobForTest(t, db)
	waits := func(step string, want bool) {
		t.Helper()
		got, err := repository.OthersWait(ctx, running)
		if err != nil || got != want {
			t.Fatalf("%s: OthersWait() = %v, %v; want %v", step, got, err, want)
		}
	}
	waits("nobody else", false)
	// The successor of the same project waits for this very Job: it is nobody to give the turn to.
	queueCardsJobForTest(t, db, memoryProjectID)
	waits("the successor of the same project", false)

	seedOtherCardsProjectForTest(t, db)
	other := queueCardsJobForTest(t, db, otherCardsProjectID)
	waits("cards of another project", true)

	// Its time has not come: a retry waits out its pause.
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET next_attempt_at = clock_timestamp() + interval '1 minute' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	waits("a Job whose time has not come", false)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET next_attempt_at = clock_timestamp() - interval '1 second' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	waits("its time has come", true)

	// The other project starts its cards: a second worker took the Job, and its own successor waits for it.
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != other {
		t.Fatalf("claim = %q %v, want the other project's Job %s", jobID, ok, other)
	}
	waits("the other project's Job is in work", false)
	queueCardsJobForTest(t, db, otherCardsProjectID)
	waits("the successor of the other project's Job in work", false)
}

// specs/2026-11-11-cards-once.md CO1.1, CO1.3, CO1.4: the two cards workers of the site wake in the same tick and ask the
// queue at one moment — the claim of the first is not committed yet when the second asks (the incident of 11.11: the
// Jobs of the draft and the published text of one saga started in the same second and both paid for its cards). One
// cards Job of a saga goes to work; the second waits without spending an attempt and is taken as soon as the first is
// over, while the cards of another saga go to work beside it.
func TestTwoWorkersAskingAtOnceStartOneCardsJobOfASaga(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_at_once")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	// A Job that gave up its turn is not reused by the next text index (quota spec, 28.10): two Jobs of one saga wait.
	yielded := queueCardsJobForTest(t, db, memoryProjectID)
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != yielded {
		t.Fatalf("claim = %q %v, want %s", jobID, ok, yielded)
	}
	if err := repository.YieldCards(ctx, jobs.WorkerClaim{ProjectID: memoryProjectID, JobID: yielded, ClaimIncarnation: 1}); err != nil {
		t.Fatal(err)
	}
	successor := queueCardsJobForTest(t, db, memoryProjectID)
	if successor == yielded {
		t.Fatal("the yielded Job was reused; want a second Job of the saga")
	}

	query, args := claimCardsQueryForTest(t, db)
	var first, second claimedCardsForTest
	if firstErr, secondErr := testutil.AtOnce(t, db, query, args, first.scan, second.scan); firstErr != nil || secondErr != nil {
		t.Fatal(firstErr, secondErr)
	}
	if first.none || (first.jobID != yielded && first.jobID != successor) {
		t.Fatalf("the first worker got %+v, want a cards Job of the saga", first)
	}
	if !second.none {
		t.Fatalf("the second worker got %s while the saga's cards Job %s is in work", second.jobID, first.jobID)
	}

	// The cards of another saga go to work while the first saga builds its own; its second Job still waits.
	seedOtherCardsProjectForTest(t, db)
	other := queueCardsJobForTest(t, db, otherCardsProjectID)
	var third, fourth claimedCardsForTest
	if thirdErr, fourthErr := testutil.AtOnce(t, db, query, args, third.scan, fourth.scan); thirdErr != nil || fourthErr != nil {
		t.Fatal(thirdErr, fourthErr)
	}
	if third.jobID != other || !fourth.none {
		t.Fatalf("workers got %+v and %+v, want the other saga's Job %s and nothing", third, fourth, other)
	}

	// The first Job is over: the one that waited is taken at once, with every attempt it had.
	waiting := successor
	if first.jobID == successor {
		waiting = yielded
	}
	if err := repository.FinishCards(ctx, jobs.WorkerClaim{ProjectID: memoryProjectID, JobID: first.jobID, ClaimIncarnation: first.incarnation},
		service.CardsOutcome{Subject: cardsSubjectForTest(), Status: jobs.StatusSucceeded, Result: &jobs.ChapterCardsResult{
			Type: jobs.KindChapterCards, SourceView: governance.SourceViewDraft, Chapters: 3, Built: 3,
		}}); err != nil {
		t.Fatal(err)
	}
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != waiting {
		t.Fatalf("claim after the first is over = %q %v, want the Job that waited %s", jobID, ok, waiting)
	}
	var retries int
	if err := db.Pool.QueryRow(ctx, `SELECT retry_count FROM jobs WHERE id=$1`, waiting).Scan(&retries); err != nil || retries != 0 {
		t.Fatalf("attempts spent by the Job that waited = %d, %v; want 0", retries, err)
	}
}

// CO2.2 (a): the cards of a saga are built first by the Job of its draft text — the text the answers of the example
// were recorded for. The Job of the published text waits while the draft text still has a Job to index or to card, also
// when it was queued earlier; a running Job of another saga is not told that it waits (FS6.1).
func TestTheCardsJobOfTheDraftTextGoesFirst(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_draft_cards_first")
	seedMemoryProject(t, db)
	seedPublishedViewForTest(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	published := queueCardsJobOfViewForTest(t, db, memoryProjectID, governance.SourceViewPublished)
	draft := queueCardsJobForTest(t, db, memoryProjectID)
	if jobID, ok := claimCardsForTest(t, db); !ok || jobID != draft {
		t.Fatalf("claim = %q %v, want the Job of the draft text %s before the published one %s", jobID, ok, draft, published)
	}

	// Another saga: the draft Job pauses before its retry; the published one waits for it and is nobody to yield to.
	seedOtherCardsProjectForTest(t, db)
	seedPublishedViewForTest(t, db, otherCardsProjectID)
	otherDraft := queueCardsJobForTest(t, db, otherCardsProjectID)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET next_attempt_at = clock_timestamp() + interval '1 minute' WHERE id=$1`, otherDraft); err != nil {
		t.Fatal(err)
	}
	queueCardsJobOfViewForTest(t, db, otherCardsProjectID, governance.SourceViewPublished)
	running := jobs.WorkerClaim{ProjectID: memoryProjectID, JobID: draft, ClaimIncarnation: 1}
	if waits, err := repository.OthersWait(ctx, running); err != nil || waits {
		t.Fatalf("OthersWait() = %v, %v; want false: the published Job of the other saga waits for its draft", waits, err)
	}
	if jobID, ok := claimCardsForTest(t, db); ok {
		t.Fatalf("claimed %s: the published Job of the other saga goes before its draft Job", jobID)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET next_attempt_at = clock_timestamp() - interval '1 second' WHERE id=$1`, otherDraft); err != nil {
		t.Fatal(err)
	}
	if waits, err := repository.OthersWait(ctx, running); err != nil || !waits {
		t.Fatalf("OthersWait() = %v, %v; want true: the draft Job of the other saga can start", waits, err)
	}
}
