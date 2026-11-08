// SPDX-License-Identifier: AGPL-3.0-only

package sagamemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/jobs"
)

// cardsPolicy is the author's consent to the model work of the cards of a saga.
type cardsPolicy struct {
	allowed bool
	err     error
	asked   int
}

func (policy *cardsPolicy) CardModelAllowed(_ context.Context, projectID string) (bool, error) {
	policy.asked++
	if projectID != cardsProjectID {
		return false, errors.New("wrong project")
	}
	return policy.allowed, policy.err
}

// mapRefresher counts the free builds of the map rows.
type mapRefresher struct{ refreshed int }

func (refresher *mapRefresher) Refresh(context.Context, jobs.WorkerClaim, string) (string, error) {
	refresher.refreshed++
	return "", nil
}

// specs/2026-11-07-desktop-app.md DA8.1: without the author's consent the cards Job sends nothing to the model, builds
// the map from the cards there are and ends waiting for the consent; with it, it builds the missing cards.
func TestTheCardsJobCallsTheModelOnlyWithTheAuthorsConsent(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
	}}
	model := &cardsModel{}
	store := &cardsStore{existing: map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true}}
	policy := &cardsPolicy{}
	maps := &mapRefresher{}
	var heard []TextVersion
	build := NewBuildCards(text, store, model, &cardsUsage{}, "test-model", 2, nil, func(_ context.Context, version TextVersion) {
		heard = append(heard, version)
	}).WithMap(maps, nil).WithPolicy(policy)

	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusFailed || store.outcome == nil || store.outcome.Error == nil ||
		store.outcome.Error.Code != ConsentCardsCode || !strings.Contains(store.outcome.Error.Message, "1 из 2") {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v; want the Job waiting for the consent with 1 of 2 chapters", status, err, store.outcome)
	}
	if len(model.calls) != 0 || len(store.saved) != 0 || maps.refreshed != 1 || len(heard) != 0 {
		t.Fatalf("model calls %v, saved %d, map builds %d, heard %v; want only the map", model.calls, len(store.saved), maps.refreshed, heard)
	}
	if _, err := jobs.EncodeError(*store.outcome.Error); err != nil {
		t.Fatalf("the outcome is not a valid Job error: %v", err)
	}

	policy.allowed = true
	store.outcome = nil
	status, err = build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || model.calls["Глава 2. Путь"] == 0 || len(store.saved) != 1 || store.saved[0].ChapterID != "c2" {
		t.Fatalf("allowed Job = %q, %v, model calls %v, saved %+v; want the card of c2", status, err, model.calls, store.saved)
	}
	if model.calls["Глава 1. Старт"] != 0 || len(heard) != 1 {
		t.Fatalf("model calls %v, heard %v; want the carded chapter untouched and the success heard", model.calls, heard)
	}
}

// DA8.1: the name registry waits for the consent too; a saga whose cards and registry need no model — a saga file
// brought them — ends succeeded with its map.
func TestTheNameRegistryWaitsForTheConsentAndNothingElseDoes(t *testing.T) {
	model := &namesModel{answer: namesAnswer}
	store, build, _ := namesFixture(model)
	maps := &mapRefresher{}
	policy := &cardsPolicy{}
	build.WithMap(maps, nil).WithPolicy(policy)

	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusFailed || store.outcome.Error == nil || store.outcome.Error.Code != ConsentCardsCode {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v; want the registry waiting for the consent", status, err, store.outcome)
	}
	if model.calls != 0 || store.names != nil || maps.refreshed != 1 {
		t.Fatalf("registry calls %d, names %+v, map builds %d", model.calls, store.names, maps.refreshed)
	}

	policy.allowed = true
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded || model.calls != 1 {
		t.Fatalf("allowed Job = %q, %v, registry calls %d", status, err, model.calls)
	}

	policy.allowed = false
	status, err = build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || store.outcome.Result == nil || store.outcome.Result.Chapters != 2 || model.calls != 1 {
		t.Fatalf("Job without anything to build = %q, %v, outcome %+v, registry calls %d", status, err, store.outcome, model.calls)
	}
}

// DA8.1: a policy that cannot answer stops the attempt before any model call; the site's builder asks no policy.
func TestAPolicyThatCannotAnswerStopsTheAttempt(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"}}}
	model := &cardsModel{}
	store := &cardsStore{existing: map[CardKey]bool{}}
	unavailable := errors.New("database unavailable")
	build := NewBuildCards(text, store, model, nil, "test-model", 1, nil, nil).WithPolicy(&cardsPolicy{err: unavailable})
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); !errors.Is(err, unavailable) || len(model.calls) != 0 || store.outcome != nil {
		t.Fatalf("RunForClaim() = %v, model calls %v, outcome %+v", err, model.calls, store.outcome)
	}
}
