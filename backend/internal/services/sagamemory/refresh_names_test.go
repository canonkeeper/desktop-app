package sagamemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

const namesAnswer = `{"persons":[{"name":"мирон","names":["ворон"]}],"shared":[{"name":"король","who":["мирон"]}],"wrong":[]}`

// namesModel answers the registry call; a chapter would be a mistake here, the chapters have cards.
type namesModel struct {
	calls  int
	input  string
	answer string
	err    error
}

func (model *namesModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions != domain.NameInstructions {
		return "", quota.ModelUsage{}, errors.New("unexpected instructions")
	}
	model.calls++
	model.input = input
	return model.answer, cardCallUsage, model.err
}

func namesFixture(model *namesModel) (*cardsStore, *BuildCards, *[]string) {
	store := &cardsStore{
		existing: map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true, {ChapterID: "c2", ContentHash: "h2"}: true},
		cards: map[CardKey]domain.Card{
			{ChapterID: "c1", ContentHash: "h1"}: {Characters: []domain.Mention{{Name: "Мирон", Aliases: []string{"Ворон"}, Note: "Герой"}}},
			{ChapterID: "c2", ContentHash: "h2"}: {Characters: []domain.Mention{{Name: "Ворон"}}, Facts: []domain.Fact{{Subject: "Ворон", Attribute: "возраст", Value: "24"}}},
		},
	}
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2"},
	}}
	var failures []string
	build := NewBuildCards(text, store, model, &cardsUsage{}, "test-model", 2, func(projectID, stage string, err error) {
		failures = append(failures, projectID+" "+stage)
	}, nil)
	return store, build, &failures
}

// R5.1, R5.2: a Job with no chapter left reaches the step: the model gets the list of the version in
// reading order, its answer is saved under the claim and paid for; the next Job with the same names
// does not call the model.
func TestCardsJobBuildsTheRegistryOnce(t *testing.T) {
	model := &namesModel{answer: namesAnswer}
	store, build, failures := namesFixture(model)
	usage := build.usage.(*cardsUsage)

	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || len(*failures) != 0 {
		t.Fatalf("RunForClaim() = %q, %v, failures %v", status, err, *failures)
	}
	if model.calls != 1 || !strings.Contains(model.input, "мирон | глав 1 (0–0), фактов 0 | также: Ворон | Герой") ||
		!strings.Contains(model.input, "ворон | глав 1 (1–1), фактов 1") {
		t.Fatalf("model calls = %d, input %q", model.calls, model.input)
	}
	if store.names == nil || store.names.Model != "test-model" || store.namesClaim != cardsClaim || len(store.names.Keys) != 2 ||
		store.names.Digest == "" || len(store.names.Registry.Persons) != 1 || store.names.Registry.Shared[0] != "король" {
		t.Fatalf("saved registry = %+v under %+v", store.names, store.namesClaim)
	}
	if spent := usage.total(); spent.Calls != 1 || spent.InputTokens != 50 {
		t.Fatalf("usage = %+v, want the registry call counted as cards", spent)
	}

	store.outcome = nil
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded || model.calls != 1 {
		t.Fatalf("second Job = %q, %v, %d model calls; want no call for the same names", status, err, model.calls)
	}

	card := store.cards[CardKey{ChapterID: "c2", ContentHash: "h2"}]
	card.Characters[0].Note = "Другая заметка"
	store.cards[CardKey{ChapterID: "c2", ContentHash: "h2"}] = card
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.calls != 1 {
		t.Fatalf("a changed note = %v, %d model calls; want no call", err, model.calls)
	}

	card.Characters = append(card.Characters, domain.Mention{Name: "Любава"})
	store.cards[CardKey{ChapterID: "c2", ContentHash: "h2"}] = card
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.calls != 2 {
		t.Fatalf("a new name = %v, %d model calls; want a new call", err, model.calls)
	}
}

// R5.1: a failed call, a bad answer or a failed save leave the Job and its cards alone; the next Job
// tries again, and what the call cost is counted.
func TestRegistryFailureDoesNotFailTheJob(t *testing.T) {
	for name, model := range map[string]*namesModel{
		"model": {err: errors.New("model down")},
		"parse": {answer: "не JSON"},
	} {
		store, build, failures := namesFixture(model)
		status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
		if err != nil || status != jobs.StatusSucceeded || store.outcome == nil || store.outcome.Status != jobs.StatusSucceeded {
			t.Fatalf("%s: RunForClaim() = %q, %v, outcome %+v; want the Job done", name, status, err, store.outcome)
		}
		if store.names != nil || len(*failures) != 1 || (*failures)[0] != cardsProjectID+" "+name {
			t.Fatalf("%s: registry %+v, failures %v", name, store.names, *failures)
		}
		if spent := build.usage.(*cardsUsage).total(); spent.Calls != 1 {
			t.Fatalf("%s: usage = %+v, want the call counted", name, spent)
		}
		model.err, model.answer = nil, namesAnswer
		if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.calls != 2 || store.names == nil {
			t.Fatalf("%s: next Job = %v, %d calls, registry %+v; want it built", name, err, model.calls, store.names)
		}
	}
	model := &namesModel{answer: namesAnswer}
	store, build, failures := namesFixture(model)
	store.namesErr = errors.New("store down")
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded || len(*failures) != 1 || (*failures)[0] != cardsProjectID+" store" {
		t.Fatalf("a failed save = %q, %v, failures %v", status, err, *failures)
	}
}

// A canceled or stopped Job stops at the step without a terminal state and without a failure report.
func TestRegistryStepStopsWithTheJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &cancelingNamesModel{cancel: cancel}
	store, build, failures := namesFixture(&namesModel{})
	build.model = model
	if status, err := build.RunForClaim(ctx, cardsClaim, cardsSubject, false); !errors.Is(err, context.Canceled) || status != "" || store.outcome != nil || len(*failures) != 0 {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v, failures %v", status, err, store.outcome, *failures)
	}
}

type cancelingNamesModel struct{ cancel context.CancelFunc }

func (model *cancelingNamesModel) Complete(ctx context.Context, _, _ string) (string, quota.ModelUsage, error) {
	model.cancel()
	return "", quota.ModelUsage{}, ctx.Err()
}

// Chapters still without cards leave the registry for the Job that finishes them.
func TestRegistryWaitsForTheLastCard(t *testing.T) {
	store := &cardsStore{existing: map[CardKey]bool{}}
	text := cardsTextSource{chapters: []SelectedChapter{{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Сбой"}}}
	build := NewBuildCards(text, store, &cardsModel{fail: "Глава 1. Сбой"}, nil, "test-model", 1, nil, nil)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); !errors.Is(err, ErrRetryCards) || store.names != nil {
		t.Fatalf("RunForClaim() = %v, registry %+v; want a retry and no registry", err, store.names)
	}
}

// Cards without a name for the list need no model: an empty registry is saved, so the start does
// not queue the version again.
func TestRegistryOfCardsWithoutNamesNeedsNoModel(t *testing.T) {
	model := &namesModel{answer: namesAnswer}
	store, build, _ := namesFixture(model)
	store.cards = map[CardKey]domain.Card{}
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.calls != 0 || store.names == nil || len(store.names.Registry.Persons) != 0 {
		t.Fatalf("RunForClaim() = %v, %d calls, registry %+v", err, model.calls, store.names)
	}
}
