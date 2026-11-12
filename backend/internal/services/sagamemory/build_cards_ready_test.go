package sagamemory

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/jobs"
)

// specs/2026-11-09-first-session.md, FS1.3, FS6.3: a cards Job that leaves every chapter of its text with a card tells
// it once, before the name registry is asked of the model and before the Job ends; a Job that leaves chapters without
// cards tells nothing.
func TestTheCardsJobTellsThatEveryChapterIsCardedBeforeTheNames(t *testing.T) {
	model := &namesModel{answer: namesAnswer}
	store, build, failures := namesFixture(model)
	var heard []string
	build.WithReady(func(_ context.Context, projectID string) {
		if model.calls != 0 || store.names != nil || store.outcome != nil {
			t.Errorf("told after the registry or the end of the Job: %d registry calls, registry %+v, outcome %+v", model.calls, store.names, store.outcome)
		}
		heard = append(heard, projectID)
	})
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || len(*failures) != 0 || model.calls != 1 {
		t.Fatalf("RunForClaim() = %q, %v, failures %v, %d registry calls", status, err, *failures, model.calls)
	}
	if len(heard) != 1 || heard[0] != cardsProjectID {
		t.Fatalf("heard %v, want the project once", heard)
	}

	text := cardsTextSource{chapters: []SelectedChapter{{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Сбой"}}}
	failing := NewBuildCards(text, &cardsStore{}, &cardsModel{fail: "Глава 1. Сбой"}, nil, "test-model", 1, nil, nil).
		WithReady(func(context.Context, string) { t.Error("a Job with a chapter left told that the saga is carded") })
	if status, err := failing.RunForClaim(context.Background(), cardsClaim, cardsSubject, true); err != nil || status != jobs.StatusFailed {
		t.Fatalf("failing Job = %q, %v", status, err)
	}
}

// DA8.1 with FS1.3: a saga whose author has not allowed the model is told ready when its cards came with it, and is not
// while a chapter waits for the consent.
func TestTheCardsJobWithoutConsentTellsOnlyACardedSaga(t *testing.T) {
	model := &namesModel{answer: namesAnswer}
	store, build, _ := namesFixture(model)
	heard := 0
	build.WithPolicy(&cardsPolicy{}).WithReady(func(context.Context, string) { heard++ })
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || heard != 1 || model.calls != 0 {
		t.Fatalf("carded saga without consent: %v, heard %d, %d registry calls", err, heard, model.calls)
	}
	delete(store.existing, CardKey{ChapterID: "c2", ContentHash: "h2"})
	store.outcome = nil
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || heard != 1 {
		t.Fatalf("saga with a chapter waiting for the consent: %v, heard %d", err, heard)
	}
}

// growingText is a text version that gets one more chapter after it was read for the first time: the next volume of an
// import queue is indexed while the cards Job of the volume before it works.
type growingText struct {
	cardsTextSource
	read int
	late SelectedChapter
}

func (text *growingText) Selection(ctx context.Context, projectID, view string) (Selection, error) {
	selection, err := text.cardsTextSource.Selection(ctx, projectID, view)
	if text.read++; text.read > 1 {
		selection.Chapters = append(append([]SelectedChapter{}, selection.Chapters...), text.late)
	}
	return selection, err
}

// FS6.2: a Job that built its chapters and finds new ones without cards leaves the name registry to their Job, which
// the text index of those chapters has queued: the names of a half of the saga are not asked of the model twice. The
// Job still tells that its chapters are carded, prepares the map and ends well.
func TestCardsJobLeavesTheRegistryToTheJobOfNewChapters(t *testing.T) {
	model := &namesModel{answer: namesAnswer}
	store, build, failures := namesFixture(model)
	maps := &countingMaps{}
	heard := 0
	build.WithMap(maps, nil).WithReady(func(context.Context, string) { heard++ })
	build.text = &growingText{
		cardsTextSource: build.text.(cardsTextSource),
		late:            SelectedChapter{ID: "c3", ContentHash: "h3", BookOrder: 2, Order: 1, Title: "Глава 1"},
	}
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || len(*failures) != 0 {
		t.Fatalf("RunForClaim() = %q, %v, failures %v", status, err, *failures)
	}
	if model.calls != 0 || store.names != nil {
		t.Fatalf("the registry was asked %d times and saved %+v; want it left to the next Job", model.calls, store.names)
	}
	if heard != 1 || maps.refreshes != 1 || store.outcome == nil || store.outcome.Result == nil || store.outcome.Result.Chapters != 2 {
		t.Fatalf("heard %d, map %d, outcome %+v; want the Job to end as before", heard, maps.refreshes, store.outcome)
	}
}
