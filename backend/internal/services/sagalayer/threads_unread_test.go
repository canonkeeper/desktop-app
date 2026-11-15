package sagalayer

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// threadsPolicy allows the reading and keeps the mark of a build as the policy of the app does: it acts while the stamps now
// have not passed the threshold of a rebuild from the stamps of the marked build (RR1.3, RR1.4).
type threadsPolicy struct {
	mu     sync.Mutex
	stamps []domain.Stamp
	reason string
	views  []string
}

func (*threadsPolicy) ReadingAllowed(context.Context, string, bool) (bool, error) { return true, nil }

func (policy *threadsPolicy) ThreadsUnread(_ context.Context, _ string, stamps []domain.Stamp) (string, error) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if policy.reason == "" || domain.Due(stamps, policy.stamps) {
		return "", nil
	}
	return policy.reason, nil
}

func (policy *threadsPolicy) MarkThreadsUnread(_ context.Context, _ string, view string, stamps []domain.Stamp, reason string) error {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	policy.stamps, policy.reason, policy.views = append([]domain.Stamp(nil), stamps...), reason, append(policy.views, view)
	return nil
}

// oneAtATime is the fixture of the layer whose books are asked one after another.
func oneAtATime(model *layerModel) (*layerStore, *BuildLayer) {
	version := layerVersion("h1")
	store := &layerStore{stamps: stampsOf(version)}
	return store, NewBuildLayer(sagamemorytest.NewSaga(version, nil, layerCards()), store, model, &layerUsage{}, "test-model", 1, nil)
}

// specs/2026-11-15-desk-reading-rest.md RR1.3: with the policy of the app a book whose answer does not read is asked three
// times, then the build is marked unreadable over its stamps and the Job ends layer.unread without another attempt; the
// checkpoint keeps the book that answered.
func TestABookThatDoesNotReadMarksTheBuild(t *testing.T) {
	model := newLayerModel()
	model.books = func(input string) (string, error) {
		if strings.HasPrefix(input, "ТОМ 2") {
			return "нет JSON", nil
		}
		return bookAnswer(input), nil
	}
	store, build := oneAtATime(model)
	policy := &threadsPolicy{}
	build.WithPolicy(policy)
	status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject)
	if err != nil || status != jobs.StatusFailed || store.outcome == nil || store.outcome.Error == nil || store.outcome.Error.Code != UnreadLayerCode {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v; want the Job ended layer.unread", status, err, store.outcome)
	}
	if _, err := jobs.EncodeError(*store.outcome.Error); err != nil {
		t.Fatalf("the outcome is not a valid Job error: %v", err)
	}
	if model.count("map") != 1+callAttempts || policy.reason != sagamemory.UnreadUnreadable || !reflect.DeepEqual(policy.stamps, store.stamps) ||
		!reflect.DeepEqual(policy.views, []string{string(layerSubject.SourceView)}) {
		t.Fatalf("map calls %d, marked %q over %v by %v", model.count("map"), policy.reason, policy.stamps, policy.views)
	}
	if store.stored == nil || len(store.stored.CheckpointBooks) != 1 || store.stored.CheckpointBooks[0].ID != "b1" {
		t.Fatalf("the checkpoint = %+v", store.stored)
	}
}

// RR1.3: a cut answer, a refusal and no answer are asked once and mark the build; an answer of the reduce or of what was
// missed that does not read marks it too; a check of one thread that fails is as before — the thread stays, nothing is
// marked.
func TestEveryNamedReasonMarksTheBuild(t *testing.T) {
	for reason, failure := range map[string]error{
		sagamemory.UnreadCutAnswer: fmt.Errorf("the answer: %w", sagamemory.ErrAnswerCut),
		sagamemory.UnreadRefused:   fmt.Errorf("model API status 400: %w", sagamemory.ErrRefused),
		sagamemory.UnreadNoAnswer:  fmt.Errorf("model API status 503: %w", sagamemory.ErrNoAnswer),
	} {
		model := newLayerModel()
		model.books = func(string) (string, error) { return "", failure }
		store, build := oneAtATime(model)
		policy := &threadsPolicy{}
		build.WithPolicy(policy)
		if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusFailed ||
			store.outcome.Error.Code != UnreadLayerCode || model.count("map") != 1 || policy.reason != reason {
			t.Fatalf("%s: %q, %v, %d map calls, marked %q", reason, status, err, model.count("map"), policy.reason)
		}
	}
	for step, broken := range map[string]func(*layerModel){
		"reduce": func(model *layerModel) { model.reduce = "нет JSON" },
		"gap":    func(model *layerModel) { model.gap = "нет JSON" },
	} {
		model := newLayerModel()
		broken(model)
		store, build := oneAtATime(model)
		policy := &threadsPolicy{}
		build.WithPolicy(policy)
		if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusFailed ||
			store.outcome.Error.Code != UnreadLayerCode || model.count(step) != callAttempts || policy.reason != sagamemory.UnreadUnreadable {
			t.Fatalf("%s: %q, %v, %d calls, marked %q", step, status, err, model.count(step), policy.reason)
		}
	}
	model := newLayerModel()
	model.verify = func(string) (string, error) {
		return "", fmt.Errorf("model API status 503: %w", sagamemory.ErrNoAnswer)
	}
	store, build := oneAtATime(model)
	policy := &threadsPolicy{}
	build.WithPolicy(policy)
	if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded || policy.reason != "" ||
		store.outcome.Result.Unverified == 0 {
		t.Fatalf("a check that failed: %q, %v, marked %q, outcome %+v", status, err, policy.reason, store.outcome)
	}
}

// RR1.6: a Job canceled while the model answers marks no build even when the answer is a named reason — the third answer of a
// book that does not read, a cut answer or a refusal, with the Job canceled at the same moment — and does not end layer.unread.
func TestACanceledJobMarksNoBuildWhateverTheAnswer(t *testing.T) {
	for name, answer := range map[string]struct {
		text string
		err  error
		// at is the answer that comes with the cancel: an answer that does not read is asked three times.
		at int
	}{
		"unreadable": {text: "нет JSON", at: callAttempts},
		"cut":        {err: fmt.Errorf("the answer: %w", sagamemory.ErrAnswerCut), at: 1},
		"refused":    {err: fmt.Errorf("model API status 400: %w", sagamemory.ErrRefused), at: 1},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		model := newLayerModel()
		asked := 0
		model.books = func(string) (string, error) {
			if asked++; asked == answer.at {
				cancel()
			}
			return answer.text, answer.err
		}
		store, build := oneAtATime(model)
		policy := &threadsPolicy{}
		build.WithPolicy(policy)
		_, err := build.RunForClaim(ctx, layerClaim, layerSubject)
		if err == nil || model.count("map") != answer.at || policy.reason != "" || len(policy.views) != 0 || store.outcome != nil {
			t.Fatalf("%s: %v, %d map calls, marked %q by %v, outcome %+v; want an error, no mark and no outcome", name, err,
				model.count("map"), policy.reason, policy.views, store.outcome)
		}
		cancel()
	}
}

// RR1.3, RR1.8: after the mark one new card does not make the build due — the queue does not queue it, a Job queued another
// way calls no model and ends as at a fresh layer, the status says the threads do not wait but are marked; two new cards
// in a saga of three chapters pass the threshold of a rebuild — the queue queues it.
func TestAMarkedBuildWaitsForTheThreshold(t *testing.T) {
	model := newLayerModel()
	model.reduce = "нет JSON"
	store, build := oneAtATime(model)
	policy := &threadsPolicy{}
	build.WithPolicy(policy)
	if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || policy.reason == "" {
		t.Fatalf("the build was not marked: %v", err)
	}
	queue := NewQueueDue(store).WithPolicy(policy)
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || queued {
		t.Fatalf("the marked build was queued: %v, %v", queued, err)
	}
	if queued, err := NewQueueMissingLayers(layerVersions{{ProjectID: layerProjectID, View: "draft"}}, queue).Execute(context.Background()); err != nil || queued != 0 {
		t.Fatalf("the start queued the marked build: %d, %v", queued, err)
	}
	store.stamps[0].Hash = "h1-edited"
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || queued {
		t.Fatalf("one new card queued the build: %v, %v", queued, err)
	}
	if wait, unread, err := Pending(context.Background(), store, policy, layerProjectID, "draft"); err != nil || wait || unread != sagamemory.UnreadUnreadable {
		t.Fatalf("Pending() = %v, %q, %v; want marked", wait, unread, err)
	}
	calls := model.count("map") + model.count("reduce")
	store.outcome = nil
	if status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil || status != jobs.StatusSucceeded ||
		model.count("map")+model.count("reduce") != calls {
		t.Fatalf("a Job queued another way = %q, %v; calls %d → %d", status, err, calls, model.count("map")+model.count("reduce"))
	}
	store.stamps[1].Hash = "h2-edited"
	if queued, err := queue.Execute(context.Background(), layerProjectID, "draft"); err != nil || !queued {
		t.Fatalf("two new cards did not queue the build: %v, %v", queued, err)
	}
	if wait, unread, err := Pending(context.Background(), store, policy, layerProjectID, "draft"); err != nil || !wait || unread != "" {
		t.Fatalf("Pending() past the threshold = %v, %q, %v", wait, unread, err)
	}
}
