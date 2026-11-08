package sagamemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
)

type countingMaps struct {
	refreshes int
	failure   error
	// after runs at each refresh: what the Job has done by then.
	after func()
}

func (maps *countingMaps) Refresh(context.Context, jobs.WorkerClaim, string) (string, error) {
	maps.refreshes++
	if maps.after != nil {
		maps.after()
	}
	return "map_store", maps.failure
}

// specs/2026-11-04-saga-map.md R10.1: the map is prepared before a long build yields its turn, so it grows with the cards, and
// when the Job ends, well or with chapters left, before it says so.
func TestCardsJobPreparesTheMapWhereItStopsWorking(t *testing.T) {
	store := &cardsStore{existing: map[CardKey]bool{}}
	maps := &countingMaps{}
	maps.after = func() {
		if store.yields != 0 || store.outcome != nil {
			t.Errorf("the map came after the Job gave up its turn: yields %d, outcome %+v", store.yields, store.outcome)
		}
	}
	build := NewBuildCards(manyChapters(12), store, &slowCardsModel{delay: 15 * time.Millisecond}, nil, "test-model", 1, nil, nil).WithMap(maps, nil)
	build.portion = 50 * time.Millisecond
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusQueued || maps.refreshes != 1 {
		t.Fatalf("yield = %q, %v, %d refreshes", status, err, maps.refreshes)
	}
	for _, card := range store.saved {
		store.existing[card.CardKey] = true
	}
	store.yields = 0
	build.portion = time.Minute
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded || maps.refreshes != 2 {
		t.Fatalf("success = %q, %v, %d refreshes", status, err, maps.refreshes)
	}

	incomplete := &cardsStore{existing: map[CardKey]bool{}}
	maps = &countingMaps{}
	maps.after = func() {
		if incomplete.outcome != nil {
			t.Errorf("the map came after the outcome %+v", incomplete.outcome)
		}
	}
	build = NewBuildCards(manyChapters(2), incomplete, &slowCardsModel{fail: true}, nil, "test-model", 1, nil, nil).WithMap(maps, nil)
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, true); err != nil || status != jobs.StatusFailed || maps.refreshes != 1 {
		t.Fatalf("incomplete = %q, %v, %d refreshes", status, err, maps.refreshes)
	}
}

// R10.1: a build of the map that fails is logged by its stage and leaves the Job alone.
func TestAFailedMapLeavesTheCardsJobAlone(t *testing.T) {
	store := &cardsStore{existing: map[CardKey]bool{}}
	var failed []string
	failure := errors.New("no map today")
	build := NewBuildCards(manyChapters(2), store, &slowCardsModel{}, nil, "test-model", 1, nil, nil).WithMap(&countingMaps{failure: failure}, func(_, stage string, err error) {
		if !errors.Is(err, failure) {
			t.Errorf("logged %v", err)
		}
		failed = append(failed, stage)
	})
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || store.outcome == nil || store.outcome.Status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v, outcome %+v", status, err, store.outcome)
	}
	if len(failed) != 1 || failed[0] != "map_store" {
		t.Fatalf("failures = %v", failed)
	}

	// A stopped Job says nothing about its map.
	ctx, cancel := context.WithCancel(context.Background())
	stopped := &countingMaps{failure: failure, after: cancel}
	build = NewBuildCards(manyChapters(1), &cardsStore{existing: map[CardKey]bool{}}, &slowCardsModel{}, nil, "test-model", 1, nil, nil).WithMap(stopped, func(string, string, error) {
		t.Error("a stopped Job logged its map")
	})
	if _, err := build.RunForClaim(ctx, cardsClaim, cardsSubject, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped = %v", err)
	}
}
