package sagalayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

const (
	layerProjectID = "95000000-0000-4000-8000-000000000001"
	layerJobID     = "95000000-0000-4000-8000-000000000002"
)

var (
	layerClaim   = jobs.WorkerClaim{ProjectID: layerProjectID, JobID: layerJobID, ClaimIncarnation: 1}
	layerSubject = jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: layerProjectID, SourceView: governance.SourceViewDraft}
)

// layerStore keeps the row of the project in memory and copies what is saved through JSON, as the
// table does; failAfter fails the checkpoint writes after that many.
type layerStore struct {
	mu          sync.Mutex
	stamps      []domain.Stamp
	stored      *domain.Stored
	checkpoints int
	failAfter   int
	claims      []jobs.WorkerClaim
	outcome     *LayerOutcome
	queued      []string
	progress    []float64
}

func (store *layerStore) Stamps(context.Context, string, string) ([]domain.Stamp, error) {
	return store.stamps, nil
}

func (store *layerStore) StoredLayer(context.Context, string) (*domain.Stored, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.stored == nil {
		return nil, nil
	}
	copied := roundTrip(*store.stored)
	return &copied, nil
}

func roundTrip[T any](value T) T {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var copied T
	if err := json.Unmarshal(body, &copied); err != nil {
		panic(err)
	}
	return copied
}

var errCheckpoint = errors.New("checkpoint write failed")

func (store *layerStore) SaveCheckpoint(_ context.Context, claim jobs.WorkerClaim, _ []domain.Stamp, checkpoint Checkpoint, progress float64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failAfter > 0 && store.checkpoints >= store.failAfter {
		return errCheckpoint
	}
	store.checkpoints++
	store.claims = append(store.claims, claim)
	store.progress = append(store.progress, progress)
	if store.stored == nil {
		store.stored = &domain.Stored{}
	}
	if checkpoint.Books != nil {
		store.stored.CheckpointBooks = roundTrip(checkpoint.Books)
	}
	steps := roundTrip(checkpoint.Steps)
	store.stored.Steps = &steps
	return nil
}

func (store *layerStore) FinishLayer(_ context.Context, claim jobs.WorkerClaim, outcome LayerOutcome) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.claims = append(store.claims, claim)
	store.outcome = &outcome
	if outcome.Layer != nil {
		store.stored = &domain.Stored{Digest: outcome.Digest, Layer: roundTrip(*outcome.Layer)}
	}
	return nil
}

func (store *layerStore) BuiltChapters(context.Context, string) ([]domain.Stamp, error) {
	if store.stored == nil || store.stored.Digest == "" {
		return nil, nil
	}
	return store.stored.Layer.Chapters, nil
}

func (store *layerStore) QueueLayer(_ context.Context, projectID, view string) error {
	store.queued = append(store.queued, projectID+"/"+view)
	return nil
}

// layerModel answers by instructions; hooks change an answer or fail a call.
type layerModel struct {
	mu     sync.Mutex
	calls  map[string]int
	inputs map[string][]string
	verify func(input string) (string, error)
	books  func(input string) (string, error)
	gap    string
}

func newLayerModel() *layerModel {
	return &layerModel{calls: map[string]int{}, inputs: map[string][]string{}}
}

func stepName(instructions string) string {
	switch instructions {
	case domain.MapInstructions:
		return "map"
	case domain.ReduceInstructions:
		return "reduce"
	case domain.VerifyInstructions:
		return "verify"
	case domain.GapInstructions:
		return "gap"
	}
	return "unknown"
}

func (model *layerModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	model.mu.Lock()
	step := stepName(instructions)
	model.calls[step]++
	model.inputs[step] = append(model.inputs[step], input)
	model.mu.Unlock()
	usage := quota.ModelUsage{Calls: 1, InputTokens: 10, OutputTokens: 1}
	switch step {
	case "map":
		if model.books != nil {
			output, err := model.books(input)
			return output, usage, err
		}
		return bookAnswer(input), usage, nil
	case "reduce":
		return `{"open":[{"thread":"Кто убил старика","kind":"тайна","first":"1 ¶1","last":"3 ¶2","weight":"major"},
			{"thread":"Лишняя запись","kind":"план","first":"1","last":"1","weight":"minor"},
			{"thread":"Неточная запись","kind":"долг","first":"3","last":"3","weight":"minor"}]}`, usage, nil
	case "gap":
		answer := model.gap
		if answer == "" {
			answer = `{"open":[{"thread":"Забытый меч","kind":"ружьё","first":"3 ¶1","last":"3 ¶1","weight":"major"}]}`
		}
		return answer, usage, nil
	case "verify":
		if model.verify != nil {
			output, err := model.verify(input)
			return output, usage, err
		}
		switch {
		case strings.Contains(input, "Лишняя запись"):
			return `{"v":"drop","why":"закрыта"}`, usage, nil
		case strings.Contains(input, "Неточная запись"):
			return `{"v":"fix","thread":"Точная запись","why":"поправил"}`, usage, nil
		}
		return `{"v":"keep"}`, usage, nil
	}
	return "", usage, errors.New("unexpected instructions")
}

func bookAnswer(input string) string {
	book := strings.SplitN(input, "\n", 2)[0]
	return fmt.Sprintf(`{"arcs":[{"title":"Арка %s","from":"1","to":"1","gist":"суть","outcome":"итог"}],
		"threads":[{"thread":"Нить из %s","kind":"тайна","first":"1 ¶1","last":"1 ¶1","status":"open"},
		{"thread":"Закрытая в %s","first":"1","last":"1","status":"closed","closed_how":"решена"}]}`, book, book, book)
}

func (model *layerModel) count(step string) int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.calls[step]
}

type layerUsage struct {
	mu      sync.Mutex
	records []quota.UsageRecord
}

func (usage *layerUsage) RecordModelUsage(_ context.Context, record quota.UsageRecord) {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	usage.records = append(usage.records, record)
}

func layerVersion(hashOfFirst string) *sagamemory.Version {
	paragraphs := func(texts ...string) []sagamemory.Paragraph {
		out := make([]sagamemory.Paragraph, len(texts))
		for i, text := range texts {
			out[i] = sagamemory.Paragraph{Text: text}
		}
		return out
	}
	return sagamemory.NewVersion(layerProjectID, "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", ContentHash: hashOfFirst, BookID: "b1", BookOrder: 1, Order: 1, Title: "Глава 1. Начало", Paragraphs: paragraphs("Текст первой главы.")},
		{ID: "c2", ContentHash: "h2", BookID: "b1", BookOrder: 1, Order: 2, Title: "Глава 2. Дорога", Paragraphs: paragraphs("Текст второй главы.")},
		{ID: "c3", ContentHash: "h3", BookID: "b2", BookOrder: 2, Order: 1, Title: "Глава 3. Город", Paragraphs: paragraphs("Текст третьей главы.", "Ещё абзац.")},
	})
}

func layerCards() map[int]*sagamemory.Card {
	return map[int]*sagamemory.Card{
		0: {Summary: "Старик убит.", Events: []sagamemory.Event{{Event: "убийство", P: 1, Key: true}}},
		1: {Summary: "Дорога на север."},
		2: {Summary: "Город и меч."},
	}
}

func stampsOf(version *sagamemory.Version) []domain.Stamp {
	stamps := make([]domain.Stamp, len(version.Chapters))
	for i, chapter := range version.Chapters {
		stamps[i] = domain.Stamp{Chapter: chapter.ID, Hash: chapter.ContentHash, CardVersion: 4}
	}
	return stamps
}

func layerFixture(model *layerModel, usage *layerUsage) (*layerStore, *BuildLayer) {
	version := layerVersion("h1")
	store := &layerStore{stamps: stampsOf(version)}
	return store, NewBuildLayer(sagamemorytest.NewSaga(version, nil, layerCards()), store, model, usage, "test-model", 4, nil)
}

// UC2: map per book, reduce, a check of each record, what was missed, a check of the additions, the save.
func TestBuildRunsTheMeasuredSteps(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)

	status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject)
	if err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("RunForClaim() = %q, %v", status, err)
	}
	if model.count("map") != 2 || model.count("reduce") != 1 || model.count("verify") != 4 || model.count("gap") != 1 {
		t.Fatalf("calls = %v", model.calls)
	}
	if !strings.HasPrefix(model.inputs["map"][0], "ТОМ ") || !containsAny(model.inputs["map"], "### 1 «Глава 1. Начало»\nПЕРЕСКАЗ: Старик убит.\nГЛАВНЫЕ СОБЫТИЯ: ¶1 убийство") {
		t.Fatalf("map inputs = %q", model.inputs["map"])
	}
	reduce := model.inputs["reduce"][0]
	if !strings.Contains(reduce, `"thread":"Нить из ТОМ 1"`) || !strings.Contains(reduce, `"first":"1 ¶1"`) || !strings.Contains(reduce, `"closed":[{"т":1,"thread":"Закрытая в ТОМ 1","how":"решена"}`) {
		t.Fatalf("reduce input = %s", reduce)
	}
	if evidence := findInput(model.inputs["verify"], "Кто убил старика"); !strings.Contains(evidence, "ЗАПИСЬ: Кто убил старика (возникла 1 ¶1, последний раз 3 ¶2)\n\nТЕКСТ ГЛАВ:\n=== Глава 1. Начало ===\nТекст первой главы.\n\n=== Глава 3. Город ===\nТекст третьей главы.\nЕщё абзац.\n\nПОЗДНЕЙШИЕ ГЛАВЫ:\n") {
		t.Fatalf("evidence = %s", evidence)
	}
	if gap := model.inputs["gap"][0]; !strings.HasPrefix(gap, `{"registry":["Кто убил старика","Точная запись"],"open":[`) {
		t.Fatalf("gap input = %s", gap)
	}
	layer := store.stored.Layer
	got := make([]string, len(layer.Threads))
	for i, thread := range layer.Threads {
		got[i] = thread.Text
	}
	if fmt.Sprint(got) != "[Кто убил старика Точная запись Забытый меч]" {
		t.Fatalf("registry = %v", got)
	}
	if first := layer.Threads[0]; first.Weight != domain.WeightMajor || first.First[0] != (domain.Ref{Chapter: "c1", P: 1}) || first.Last[0] != (domain.Ref{Chapter: "c3", P: 2}) {
		t.Fatalf("first record = %+v", first)
	}
	if len(layer.Books) != 2 || layer.Books[0].Order != 1 || layer.Books[0].Arcs[0].Title != "Арка ТОМ 1" || len(layer.Chapters) != 3 {
		t.Fatalf("books = %+v, chapters %+v", layer.Books, layer.Chapters)
	}
	outcome := store.outcome
	if outcome.Status != jobs.StatusSucceeded || outcome.Result == nil || outcome.Result.Chapters != 3 || outcome.Result.Threads != 3 ||
		outcome.Result.BooksBuilt != 2 || outcome.Result.Unverified != 0 || outcome.Model != "test-model" || outcome.Digest != domain.Digest(stampsOf(layerVersion("h1"))) {
		t.Fatalf("outcome = %+v", outcome)
	}
	for _, claim := range store.claims {
		if claim != layerClaim {
			t.Fatalf("a write under %+v", claim)
		}
	}
	// R2.8: every call is counted, as a source of its own, for the project.
	if len(usage.records) != 8 || usage.records[0].Source != quota.UsageLayer || usage.records[0].ProjectID != layerProjectID || usage.records[0].Model != "test-model" {
		t.Fatalf("usage = %+v", usage.records)
	}
	last := store.progress[len(store.progress)-1]
	if last <= 0 || last > 1 {
		t.Fatalf("progress = %v", store.progress)
	}

	// The same chapters call no model.
	store.outcome = nil
	if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("second run = %q, %v", status, err)
	}
	if model.count("map") != 2 || model.count("verify") != 4 || store.outcome.Result.Threads != 3 || store.outcome.Layer != nil {
		t.Fatalf("second run: calls %v, outcome %+v", model.calls, store.outcome)
	}
}

func containsAny(inputs []string, part string) bool { return findInput(inputs, part) != "" }

func findInput(inputs []string, part string) string {
	for _, input := range inputs {
		if strings.Contains(input, part) {
			return input
		}
	}
	return ""
}

// R2.3: an edited chapter asks the model about its book only; reduce and the checks run again.
func TestEditedChapterRebuildsOneBook(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)
	if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil {
		t.Fatal(err)
	}
	edited := layerVersion("h1-new")
	edited.Chapters[1].ContentHash = "h2-new"
	store.stamps = stampsOf(edited)
	// Two of three chapters, both of the first book, are over 3 %; one would wait
	// (specs/2026-10-27-update-book-from-file.md, R5.1).
	again := NewBuildLayer(sagamemorytest.NewSaga(edited, nil, layerCards()), store, model, usage, "test-model", 4, nil)
	model.calls = map[string]int{}
	model.inputs = map[string][]string{}
	if status, err := again.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("rebuild = %q, %v", status, err)
	}
	if model.count("map") != 1 || !strings.HasPrefix(model.inputs["map"][0], "ТОМ 1\n") || model.count("reduce") != 1 || model.count("gap") != 1 {
		t.Fatalf("calls = %v, map inputs %q", model.calls, model.inputs["map"])
	}
	if store.outcome.Result.BooksBuilt != 1 || len(store.stored.Layer.Books) != 2 || store.stored.Layer.Books[1].Arcs[0].Title != "Арка ТОМ 2" {
		t.Fatalf("outcome %+v, books %+v", store.outcome.Result, store.stored.Layer.Books)
	}
}

// R2.2: a Job that waited while the cards grew by less than the threshold builds nothing.
func TestJobBelowTheThresholdCallsNoModel(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)
	var many []domain.Stamp
	for i := range 641 {
		many = append(many, domain.Stamp{Chapter: fmt.Sprintf("x%d", i), Hash: "h", CardVersion: 4})
	}
	store.stored = &domain.Stored{Digest: domain.Digest(many), Layer: domain.Layer{Chapters: many, Threads: []domain.Thread{{Text: "нить"}}}}
	changed := append([]domain.Stamp(nil), many...)
	for i := range 4 {
		changed[i].Hash = "new"
	}
	store.stamps = changed
	if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("RunForClaim() = %q, %v", status, err)
	}
	if len(model.calls) != 0 || store.outcome.Layer != nil || store.outcome.Result.Threads != 1 {
		t.Fatalf("calls %v, outcome %+v", model.calls, store.outcome)
	}
}

// R2.6: a build that broke goes on from its last step: of the 8 calls of a build only those whose
// answer did not reach a checkpoint are asked again, whatever step it broke at.
func TestRetryContinuesFromTheLastStep(t *testing.T) {
	for failAfter := 1; failAfter <= 7; failAfter++ {
		t.Run(fmt.Sprintf("after %d writes", failAfter), func(t *testing.T) {
			model, usage := newLayerModel(), &layerUsage{}
			version := layerVersion("h1")
			store := &layerStore{stamps: stampsOf(version), failAfter: failAfter}
			build := NewBuildLayer(sagamemorytest.NewSaga(version, nil, layerCards()), store, model, usage, "test-model", 1, nil)
			if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); !errors.Is(err, errCheckpoint) {
				t.Fatalf("first run = %v", err)
			}
			if store.outcome != nil || store.checkpoints != failAfter {
				t.Fatalf("a failed run ends nothing: outcome %+v, %d writes", store.outcome, store.checkpoints)
			}
			model.calls = map[string]int{}
			store.failAfter = 0
			status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject)
			if err != nil || status != jobs.StatusSucceeded {
				t.Fatalf("second run = %q, %v", status, err)
			}
			again := model.count("map") + model.count("reduce") + model.count("verify") + model.count("gap")
			if again != 8-failAfter {
				t.Fatalf("%d calls after %d answers saved, want %d: %v", again, failAfter, 8-failAfter, model.calls)
			}
			if len(store.stored.Layer.Threads) != 3 || len(store.stored.Layer.Books) != 2 {
				t.Fatalf("layer = %+v", store.stored.Layer)
			}
		})
	}
}

// R2.6: nothing is asked again of what a checkpoint holds.
func TestCheckpointHoldsEveryAnsweredStep(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)
	store.failAfter = 7 // Two books, reduce, three checks and what was missed; the last check does not reach it.
	if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); !errors.Is(err, errCheckpoint) {
		t.Fatalf("first run = %v", err)
	}
	model.calls = map[string]int{}
	store.failAfter = 0
	if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("second run = %q, %v", status, err)
	}
	if model.count("map") != 0 || model.count("reduce") != 0 || model.count("gap") != 0 || model.count("verify") != 1 {
		t.Fatalf("calls after the checkpoint = %v, want only the check of the addition", model.calls)
	}
	if store.stored.Steps != nil {
		t.Fatalf("a finished build ends its checkpoint: %+v", store.stored.Steps)
	}
}

// R2.4: a check that fails three times leaves the record as it is and is not paid again.
func TestFailedCheckKeepsTheRecord(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	failures := []string{}
	version := layerVersion("h1")
	store := &layerStore{stamps: stampsOf(version)}
	build := NewBuildLayer(sagamemorytest.NewSaga(version, nil, layerCards()), store, model, usage, "test-model", 4, func(projectID, stage string, err error) {
		failures = append(failures, stage)
	})
	model.verify = func(input string) (string, error) {
		if strings.Contains(input, "Неточная запись") {
			return "", errors.New("the model is down")
		}
		if strings.Contains(input, "Лишняя запись") {
			return "нет JSON", nil
		}
		return `{"v":"keep"}`, nil
	}
	status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject)
	if err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("RunForClaim() = %q, %v", status, err)
	}
	if model.count("verify") != 2+2*callAttempts {
		t.Fatalf("verify calls = %d", model.count("verify"))
	}
	threads := store.stored.Layer.Threads
	if len(threads) != 4 || threads[1].Text != "Лишняя запись" || threads[2].Text != "Неточная запись" {
		t.Fatalf("registry = %+v", threads)
	}
	if store.outcome.Result.Unverified != 2 || fmt.Sprint(failures) != "[verify]" {
		t.Fatalf("result = %+v, failures %v", store.outcome.Result, failures)
	}
}

// A stopped Job stops: a canceled call is no failed check, and nothing is written about it.
func TestCanceledRunMarksNoCheck(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model.verify = func(string) (string, error) {
		cancel()
		return "", context.Canceled
	}
	if _, err := build.RunForClaim(ctx, layerClaim, layerSubject); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunForClaim() = %v", err)
	}
	if store.outcome != nil || len(store.stored.Steps.Verdicts) != 0 {
		t.Fatalf("outcome %+v, verdicts %+v", store.outcome, store.stored.Steps.Verdicts)
	}
}

func TestMapAnswerIsAskedAgainAndFailsTheRunAfterThree(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)
	model.books = func(string) (string, error) { return "нет JSON", nil }
	if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err == nil {
		t.Fatal("a book that cannot be read fails the run, which the worker retries")
	}
	if model.count("map") < callAttempts || store.outcome != nil {
		t.Fatalf("map calls = %d, outcome %+v", model.count("map"), store.outcome)
	}
}

func TestQueueDueCountsNewCards(t *testing.T) {
	store := &layerStore{}
	queue := NewQueueDue(store)
	stamps := stampsOf(layerVersion("h1"))

	store.stamps = nil
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || queued {
		t.Fatalf("no chapters: %v, %v", queued, err)
	}
	store.stamps = append([]domain.Stamp(nil), stamps...)
	store.stamps[1].CardVersion = 0
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || queued {
		t.Fatalf("a chapter without a card waits for the cards Job: %v, %v", queued, err)
	}
	store.stamps = append([]domain.Stamp(nil), stamps...)
	store.stamps[1].CardVersion = sagamemory.CardVersion - 1
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || queued {
		t.Fatalf("a saga the operator did not move to the current cards gets no layer: %v, %v", queued, err)
	}
	store.stamps = append([]domain.Stamp(nil), stamps...)
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || !queued || len(store.queued) != 1 || store.queued[0] != layerProjectID+"/draft" {
		t.Fatalf("no build yet: %v, %v, queued %v", queued, err, store.queued)
	}
	// A checkpoint is no build: the Job goes on with it.
	store.stored = &domain.Stored{Steps: &domain.Steps{Digest: "x"}}
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || !queued {
		t.Fatalf("a checkpoint only: %v, %v", queued, err)
	}
	store.stored = &domain.Stored{Digest: "d", Layer: domain.Layer{Chapters: stamps}}
	store.queued = nil
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || queued || len(store.queued) != 0 {
		t.Fatalf("nothing new: %v, %v, queued %v", queued, err, store.queued)
	}
}

type layerVersions []TextVersion

func (versions layerVersions) LayerVersions(context.Context) ([]TextVersion, error) {
	return versions, nil
}

func TestQueueMissingLayersGoesThroughEveryVersion(t *testing.T) {
	store := &layerStore{stamps: stampsOf(layerVersion("h1"))}
	sweep := NewQueueMissingLayers(layerVersions{{ProjectID: "p1", View: "draft"}, {ProjectID: "p2", View: "published"}}, NewQueueDue(store))
	queued, err := sweep.Execute(context.Background())
	if err != nil || queued != 2 || fmt.Sprint(store.queued) != "[p1/draft p2/published]" {
		t.Fatalf("Execute() = %d, %v, queued %v", queued, err, store.queued)
	}
}

// specs/2026-11-11-ux7-reorder.md RK2.3: the volumes put in another order make the threads due with no new card; the
// build asks no volume again — its answer is bound to its chapters — and gathers the registry in the new order, the
// moved volume under its new number.
func TestAMoveOfTheVolumesGathersTheThreadsAgainWithoutAskingTheVolumes(t *testing.T) {
	model, usage := newLayerModel(), &layerUsage{}
	store, build := layerFixture(model, usage)
	if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil {
		t.Fatal(err)
	}
	moved := layerVersion("h1")
	chapters := append([]sagamemory.Chapter(nil), moved.Chapters...)
	for i := range chapters {
		chapters[i].BookOrder = 3 - chapters[i].BookOrder
	}
	moved = sagamemory.NewVersion(layerProjectID, "draft", "fp-moved", chapters)
	store.stamps = stampsOf(moved)
	queued, err := NewQueueDue(store).Execute(context.Background(), layerProjectID, "draft")
	if err != nil || !queued {
		t.Fatalf("the threads after a move of the volumes: queued %v, %v", queued, err)
	}
	again := NewBuildLayer(sagamemorytest.NewSaga(moved, nil, map[int]*sagamemory.Card{
		0: layerCards()[2], 1: layerCards()[0], 2: layerCards()[1],
	}), store, model, usage, "test-model", 4, nil)
	model.calls, model.inputs = map[string]int{}, map[string][]string{}
	if status, err := again.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("the build after the move = %q, %v", status, err)
	}
	if model.count("map") != 0 || model.count("reduce") != 1 {
		t.Fatalf("calls after the move = %v", model.calls)
	}
	reduce := model.inputs["reduce"][0]
	if !strings.HasPrefix(reduce, `{"open":[{"т":1,`) || !strings.Contains(reduce, `"Нить из ТОМ 2"`) {
		t.Fatalf("the registry after the move is gathered from %s", reduce)
	}
	if books := store.stored.Layer.Books; len(books) != 2 || books[0].ID != "b2" || books[0].Order != 1 || books[1].Order != 2 {
		t.Fatalf("the volumes of the build after the move = %+v", books)
	}
}
