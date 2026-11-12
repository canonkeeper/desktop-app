package sagamemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

// countingMaps counts the maps a cards Job prepares: refreshes of the text of the Job under test (the draft one of
// cardsSubject), others of every other text.
type countingMaps struct {
	refreshes int
	others    map[string]int
	failure   error
	// after runs at each refresh: what the Job has done by then.
	after func()
}

func (maps *countingMaps) Refresh(_ context.Context, _ jobs.WorkerClaim, view string) (string, error) {
	if view == string(cardsSubject.SourceView) {
		maps.refreshes++
	} else {
		if maps.others == nil {
			maps.others = map[string]int{}
		}
		maps.others[view]++
	}
	if maps.after != nil {
		maps.after()
	}
	return "map_store", maps.failure
}

// specs/2026-11-11-cards-once.md CO2.2, specs/2026-11-04-saga-map.md R10.1: the cards Job of the published text waits for the
// draft one, so the Job of the draft text prepares the map of the published text — the one the site shows — at every
// portion and before it yields, and the map grows with the cards of a long first build. At its end the Job of the
// published text, which runs next, prepares its own map. A Job of the published text prepares only its own.
func TestTheDraftCardsJobGrowsTheMapOfThePublishedText(t *testing.T) {
	store := &cardsStore{existing: map[CardKey]bool{}}
	maps := &countingMaps{}
	build := NewBuildCards(manyChapters(12), store, &slowCardsModel{delay: 15 * time.Millisecond}, nil, "test-model", 1, nil, nil).WithMap(maps, nil)
	build.portion = 50 * time.Millisecond
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("RunForClaim() = %q, %v", status, err)
	}
	if store.asked < 2 || maps.others["published"] != store.asked || maps.refreshes != store.asked+1 {
		t.Fatalf("%d portions: the published map prepared %d times, the draft one %d; want the published one at every portion, the draft one also at the end",
			store.asked, maps.others["published"], maps.refreshes)
	}

	yielding := &cardsStore{existing: map[CardKey]bool{}, othersWait: true}
	maps = &countingMaps{}
	build = NewBuildCards(manyChapters(12), yielding, &slowCardsModel{delay: 15 * time.Millisecond}, nil, "test-model", 1, nil, nil).WithMap(maps, nil)
	build.portion = 50 * time.Millisecond
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusQueued ||
		maps.others["published"] != 1 || maps.refreshes != 1 {
		t.Fatalf("yield = %q, %v; the published map %d, the draft one %d; want both before the yield", status, err, maps.others["published"], maps.refreshes)
	}

	published := cardsSubject
	published.SourceView = governance.SourceViewPublished
	maps = &countingMaps{}
	build = NewBuildCards(manyChapters(12), &cardsStore{existing: map[CardKey]bool{}, subject: &published}, &slowCardsModel{delay: 15 * time.Millisecond}, nil, "test-model", 1, nil, nil).WithMap(maps, nil)
	build.portion = 50 * time.Millisecond
	if status, err := build.RunForClaim(context.Background(), cardsClaim, published, false); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("the published Job = %q, %v", status, err)
	}
	if maps.refreshes != 0 || len(maps.others) != 1 || maps.others["published"] < 3 {
		t.Fatalf("the published Job prepared the draft map %d times and %v; want only its own", maps.refreshes, maps.others)
	}
}

// specs/2026-11-04-saga-map.md R10.1: the map is prepared before a long build yields its turn, so it grows with the cards, and
// when the Job ends, well or with chapters left, before it says so.
func TestCardsJobPreparesTheMapWhereItStopsWorking(t *testing.T) {
	store := &cardsStore{existing: map[CardKey]bool{}, othersWait: true}
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

// specs/2026-11-10-map-nodes.md MN1.7: the rules of the nodes are saga-map/8, so every map of the rules before is stale and the
// catch-up at start builds it again without the model.
func TestTheRevisionOfTheMapNamesItsRules(t *testing.T) {
	version := domain.NewVersion("project", "draft", "fp", nil)
	stamp := CardsStamp{Cards: 17, Names: "names"}
	sum := sha256.Sum256([]byte("saga-map/8\x00" + cardsRevision(version, stamp)))
	if got, want := mapRevision(version, stamp), hex.EncodeToString(sum[:16]); got != want {
		t.Fatalf("revision = %s, want %s of saga-map/8", got, want)
	}
}
