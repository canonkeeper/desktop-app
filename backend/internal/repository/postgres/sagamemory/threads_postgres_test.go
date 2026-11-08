package sagamemory

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	layerservice "canonkeeper/backend/internal/services/sagalayer"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// layerAnswers answers the calls of a build; during runs what a test wants to happen during the call of
// that step.
type layerAnswers struct {
	mu     sync.Mutex
	calls  map[string]int
	during map[string]func()
}

func (model *layerAnswers) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	step := map[string]string{
		layerdomain.MapInstructions: "map", layerdomain.ReduceInstructions: "reduce",
		layerdomain.VerifyInstructions: "verify", layerdomain.GapInstructions: "gap",
	}[instructions]
	model.mu.Lock()
	if model.calls == nil {
		model.calls = map[string]int{}
	}
	model.calls[step]++
	hook := model.during[step]
	model.mu.Unlock()
	if hook != nil {
		hook()
	}
	usage := quota.ModelUsage{Calls: 1, InputTokens: 5}
	switch step {
	case "map":
		return `{"arcs":[{"title":"Арка","from":"1","to":"2","gist":"суть","outcome":"итог"}],"threads":[{"thread":"Нить тома","kind":"тайна","first":"1 ¶1","last":"1 ¶1","status":"open"}]}`, usage, nil
	case "reduce":
		return `{"open":[{"thread":"Кто такой Вахэнесс","kind":"тайна","first":"1 ¶1","last":"3 ¶1","weight":"major"}]}`, usage, nil
	case "gap":
		return `{"open":[]}`, usage, nil
	case "verify":
		return `{"v":"keep"}`, usage, nil
	}
	return "", usage, errors.New("unknown step of " + input[:min(len(input), 10)])
}

// cardedProject seeds the project with a card for every chapter.
func cardedProject(t *testing.T, name string) (*storedb.DB, *Repository) {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, name)
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	repository := New(db)
	build := service.NewBuildCards(repository, repository, &countingCardModel{}, nil, "test-model", 2, nil, nil)
	if status, err := build.RunForClaim(context.Background(), claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("cards = %q, %v", status, err)
	}
	return db, repository
}

func layerSubjectForTest() jobs.SagaLayerSubject {
	return jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: memoryProjectID, SourceView: governance.SourceViewDraft}
}

// claimLayerJobForTest queues the Job through its owner and claims it through the dispatcher a worker
// uses: the migration lets the kind in, and only a worker asking for this kind gets it.
func claimLayerJobForTest(t *testing.T, db *storedb.DB, repository *Repository) jobs.WorkerClaim {
	t.Helper()
	ctx := context.Background()
	if err := repository.QueueLayer(ctx, memoryProjectID, "draft"); err != nil {
		t.Fatal(err)
	}
	claimKind := func(kind jobs.Kind) (jobs.WorkerClaim, bool) {
		var schema string
		if err := db.Pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		var projectID, jobID string
		var incarnation int64
		err := db.Pool.QueryRow(ctx, `
			SELECT project_id::text, work_id::text, claim_incarnation
			FROM `+pgx.Identifier{schema + "_worker"}.Sanitize()+`.claim_next_queued_job_for_worker($1, clock_timestamp())
		`, []string{string(kind)}).Scan(&projectID, &jobID, &incarnation)
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.WorkerClaim{}, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation}, true
	}
	if claim, found := claimKind(jobs.KindChapterCards); found {
		t.Fatalf("a cards worker claimed %+v", claim)
	}
	claim, found := claimKind(jobs.KindSagaLayer)
	if !found {
		t.Fatal("the layer worker found no Job")
	}
	return claim
}

func layerBuildForTest(repository *Repository, model layerservice.Model) *layerservice.BuildLayer {
	return layerservice.NewBuildLayer(service.NewSaga(repository, repository), repository, model, nil, "test-model", 2, nil)
}

func layerRowsForTest(t *testing.T, db *storedb.DB) (rows int) {
	t.Helper()
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM saga_threads WHERE project_id = $1`, memoryProjectID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// UC2 through the database: the Job goes through the steps under its claim and leaves the build the
// chat reads; the checkpoint ends with it, and the same chapters call no model.
func TestLayerJobLeavesTheBuildTheChatReads(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_job")
	ctx := context.Background()
	if layer, err := repository.SagaThreads(ctx, memoryProjectID, true); err != nil || layer != nil {
		t.Fatalf("SagaThreads() before = %+v, %v", layer, err)
	}
	model := &layerAnswers{}
	build := layerBuildForTest(repository, model)
	claim := claimLayerJobForTest(t, db, repository)
	if status, err := build.RunForClaim(ctx, claim, layerSubjectForTest()); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if model.calls["map"] != 2 || model.calls["reduce"] != 1 || model.calls["verify"] != 1 || model.calls["gap"] != 1 {
		t.Fatalf("calls = %v", model.calls)
	}
	layer, err := repository.SagaThreads(ctx, memoryProjectID, false)
	if err != nil || layer == nil || len(layer.Threads) != 1 || layer.Threads[0].Text != "Кто такой Вахэнесс" || len(layer.Chapters) != 3 || layer.Books != nil {
		t.Fatalf("SagaThreads(without books) = %+v, %v", layer, err)
	}
	if layer.Threads[0].First[0].Chapter != memoryChapters[1].id || layer.Threads[0].Last[0].Chapter != memoryChapters[0].id {
		t.Fatalf("references = %+v, want chapter ids", layer.Threads[0])
	}
	withBooks, err := repository.SagaThreads(ctx, memoryProjectID, true)
	if err != nil || len(withBooks.Books) != 2 || withBooks.Books[0].Arcs[0].Title != "Арка" || withBooks.Books[0].Order != 1 {
		t.Fatalf("SagaThreads(with books) = %+v, %v", withBooks, err)
	}
	var checkpoint, status string
	if err := db.Pool.QueryRow(ctx, `SELECT coalesce(checkpoint_json::text, 'none') FROM saga_threads WHERE project_id = $1`, memoryProjectID).Scan(&checkpoint); err != nil || checkpoint != "none" {
		t.Fatalf("checkpoint = %q, %v", checkpoint, err)
	}
	var threads int
	if err := db.Pool.QueryRow(ctx, `SELECT status, (result_json->>'threads')::int FROM jobs WHERE id = $1`, claim.JobID).Scan(&status, &threads); err != nil || status != "succeeded" || threads != 1 {
		t.Fatalf("Job = %q with %d threads, %v", status, threads, err)
	}

	again := claimLayerJobForTest(t, db, repository)
	if status, err := build.RunForClaim(ctx, again, layerSubjectForTest()); err != nil || status != jobs.StatusSucceeded || model.calls["map"] != 2 {
		t.Fatalf("second Job = %q, %v, calls %v", status, err, model.calls)
	}
}

// R2.6: what a build paid for stays in the row while its Job fails, readers see no build, and the next
// claim goes on from it.
func TestCheckpointSurvivesAFailedAttempt(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_checkpoint")
	ctx := context.Background()
	model := &layerAnswers{during: map[string]func(){}}
	build := layerBuildForTest(repository, model)
	claim := claimLayerJobForTest(t, db, repository)
	model.during["gap"] = func() {
		if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status='queued', claim_incarnation=claim_incarnation+1, claimed_at=NULL, next_attempt_at=clock_timestamp() WHERE id=$1`, claim.JobID); err != nil {
			t.Error(err)
		}
	}
	if _, err := build.RunForClaim(ctx, claim, layerSubjectForTest()); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("a run that lost its claim = %v", err)
	}
	if layer, err := repository.SagaThreads(ctx, memoryProjectID, true); err != nil || layer != nil {
		t.Fatalf("a checkpoint is no build: %+v, %v", layer, err)
	}
	stored, err := repository.StoredLayer(ctx, memoryProjectID)
	if err != nil || stored == nil || stored.Digest != "" || len(stored.CheckpointBooks) != 2 || stored.Steps == nil || !stored.Steps.Reduced || len(stored.Steps.Verdicts) != 1 {
		t.Fatalf("StoredLayer() = %+v, %v", stored, err)
	}
	built, err := repository.BuiltChapters(ctx, memoryProjectID)
	if err != nil || built != nil {
		t.Fatalf("BuiltChapters() of a checkpoint = %+v, %v", built, err)
	}
	model.calls, model.during = map[string]int{}, nil
	next := claimLayerJobForTest(t, db, repository)
	if status, err := build.RunForClaim(ctx, next, layerSubjectForTest()); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("next attempt = %q, %v", status, err)
	}
	if model.calls["map"] != 0 || model.calls["reduce"] != 0 || model.calls["verify"] != 0 || model.calls["gap"] != 1 {
		t.Fatalf("calls of the next attempt = %v, want only the step that broke", model.calls)
	}
}

// R2.8: a source going during a call leaves nothing of it: no write passes, and a checkpoint written
// before is gone with the row of the fence.
func TestSourceGoneDuringABuildLeavesNoRow(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_gone")
	ctx := context.Background()
	model := &layerAnswers{during: map[string]func(){}}
	build := layerBuildForTest(repository, model)
	claim := claimLayerJobForTest(t, db, repository)
	model.during["reduce"] = func() {
		// What the fence does: the search rows of the chapter and the row of the layer go in one transaction.
		for _, statement := range []string{
			`DELETE FROM chapter_search_texts WHERE chapter_id = '` + memoryChapters[2].id + `'`,
			`DELETE FROM chapter_text_index_state WHERE chapter_id = '` + memoryChapters[2].id + `'`,
			`DELETE FROM saga_threads WHERE project_id = '` + memoryProjectID + `'`,
		} {
			if _, err := db.Pool.Exec(ctx, statement); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := build.RunForClaim(ctx, claim, layerSubjectForTest()); !errors.Is(err, domain.ErrTextChanged) {
		t.Fatalf("a run with a gone text = %v", err)
	}
	if rows := layerRowsForTest(t, db); rows != 0 {
		t.Fatalf("rows = %d, want none", rows)
	}
}

// R2.1: a Job waiting is reused, one per project.
func TestQueueLayerReusesAWaitingJob(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_queue")
	ctx := context.Background()
	for range 2 {
		if err := repository.QueueLayer(ctx, memoryProjectID, "draft"); err != nil {
			t.Fatal(err)
		}
	}
	var waiting, class int
	var queueClass string
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE creator_user_id IS NULL), min(queue_class) FROM jobs WHERE project_id = $1 AND kind = 'saga_layer' AND status = 'queued'`, memoryProjectID).Scan(&waiting, &class, &queueClass); err != nil {
		t.Fatal(err)
	}
	if waiting != 1 || class != 1 || queueClass != "automatic" {
		t.Fatalf("waiting = %d, without a creator %d, class %q", waiting, class, queueClass)
	}
}

// R2.2 through the database: the start queues a saga with cards and no layer; a chapter below the
// threshold waits; stamps follow the card, not only the text.
func TestStartQueuesASagaWithoutALayerAndWaitsForTheThreshold(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_start")
	ctx := context.Background()
	stamps, err := repository.Stamps(ctx, memoryProjectID, "draft")
	if err != nil || len(stamps) != 3 || stamps[0].Chapter != memoryChapters[1].id || stamps[2].Chapter != memoryChapters[0].id || stamps[0].CardVersion != domain.CardVersion {
		t.Fatalf("Stamps() = %+v, %v, want reading order with the card version", stamps, err)
	}
	sweep := layerservice.NewQueueMissingLayers(repository, layerservice.NewQueueDue(repository))
	if queued, err := sweep.Execute(ctx); err != nil || queued != 1 {
		t.Fatalf("start = %d, %v", queued, err)
	}

	var hash, second []byte
	if err := db.Pool.QueryRow(ctx, `SELECT chapter_content_hash FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[0].id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT chapter_content_hash FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[1].id).Scan(&second); err != nil {
		t.Fatal(err)
	}
	build := layerBuildForTest(repository, &layerAnswers{})
	if status, err := build.RunForClaim(ctx, claimLayerJobForTest(t, db, repository), layerSubjectForTest()); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if queued, err := sweep.Execute(ctx); err != nil || queued != 0 {
		t.Fatalf("nothing new: start = %d, %v", queued, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chapter_cards SET version = version + 1 WHERE chapter_id = $1 AND content_hash = $2`, memoryChapters[0].id, hash); err != nil {
		t.Fatal(err)
	}
	stamps, err = repository.Stamps(ctx, memoryProjectID, "draft")
	if err != nil || stamps[2].CardVersion != domain.CardVersion+1 || hex.EncodeToString(hash) != stamps[2].Hash {
		t.Fatalf("Stamps() = %+v, %v", stamps, err)
	}
	// One of three chapters is over 3 %, but one new card waits for a second one
	// (specs/2026-10-27-update-book-from-file.md, R5.1); then a saga this short does not wait.
	if queued, err := sweep.Execute(ctx); err != nil || queued != 0 {
		t.Fatalf("one new card: start = %d, %v", queued, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chapter_cards SET version = version + 1 WHERE chapter_id = $1 AND content_hash = $2`, memoryChapters[1].id, second); err != nil {
		t.Fatal(err)
	}
	if queued, err := sweep.Execute(ctx); err != nil || queued != 1 {
		t.Fatalf("two new cards: start = %d, %v", queued, err)
	}
}

// R2.8: the spend is a source of its own, counted for the project owner.
func TestLayerSpendIsASourceOfItsOwn(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_usage")
	ctx := context.Background()
	usage := quotastore.NewUsageRecorder(db, log.New(io.Discard, "", 0))
	build := layerservice.NewBuildLayer(service.NewSaga(repository, repository), repository, &layerAnswers{}, usage, "test-model", 1, nil)
	if status, err := build.RunForClaim(ctx, claimLayerJobForTest(t, db, repository), layerSubjectForTest()); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	var calls int64
	var owner string
	if err := db.Pool.QueryRow(ctx, `SELECT coalesce(sum(calls), 0), coalesce(min(user_id::text), '') FROM model_usage_days WHERE source = 'layer' AND project_id = $1`, memoryProjectID).Scan(&calls, &owner); err != nil {
		t.Fatal(err)
	}
	if calls != 5 || !strings.EqualFold(owner, memoryOwnerID) {
		t.Fatalf("layer spend: %d calls for %q", calls, owner)
	}
}

// A Job the worker gave up on or that was canceled ends under its claim; what it paid for stays for
// the next Job, and the finished build, if any, is what the chat keeps reading.
func TestFailedAndCanceledJobsKeepTheCheckpoint(t *testing.T) {
	db, repository := cardedProject(t, "ck_saga_threads_end")
	ctx := context.Background()
	model := &layerAnswers{during: map[string]func(){}}
	build := layerBuildForTest(repository, model)
	claim := claimLayerJobForTest(t, db, repository)
	model.during["gap"] = func() {
		if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET claim_incarnation=claim_incarnation+1 WHERE id=$1`, claim.JobID); err != nil {
			t.Error(err)
		}
	}
	if _, err := build.RunForClaim(ctx, claim, layerSubjectForTest()); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("a run that lost its claim = %v", err)
	}
	claim.ClaimIncarnation++
	if err := build.FailForClaim(ctx, claim, layerSubjectForTest(), jobs.Error{Code: "layer.failed", Message: "Сборка не удалась"}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, claim.JobID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("status = %q, %v", status, err)
	}
	if stored, err := repository.StoredLayer(ctx, memoryProjectID); err != nil || stored == nil || stored.Steps == nil || len(stored.CheckpointBooks) != 2 {
		t.Fatalf("StoredLayer() after a failure = %+v, %v", stored, err)
	}

	next := claimLayerJobForTest(t, db, repository)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status='canceling', cancel_requested_at=clock_timestamp() WHERE id=$1`, next.JobID); err != nil {
		t.Fatal(err)
	}
	if err := build.CancelForClaim(ctx, next, layerSubjectForTest()); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, next.JobID).Scan(&status); err != nil || status != "canceled" {
		t.Fatalf("status = %q, %v", status, err)
	}
	if stored, err := repository.StoredLayer(ctx, memoryProjectID); err != nil || stored == nil || stored.Steps == nil {
		t.Fatalf("StoredLayer() after a cancel = %+v, %v", stored, err)
	}
}
