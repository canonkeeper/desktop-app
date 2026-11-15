package sagamemory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// specs/2026-11-15-desk-reading-rest.md RR1.1, RR1.2: with the policy of the reading of the app each of the four reasons
// marks the set of names by the text version of the Job; the next Job with the same set does not call the model and writes no
// register; a new name is a new set — one call.
func TestTheNamesThatDidNotComeTogetherAreMarked(t *testing.T) {
	for reason, model := range map[string]*namesModel{
		domain.UnreadCutAnswer:  {err: fmt.Errorf("the answer: %w", domain.ErrAnswerCut)},
		domain.UnreadRefused:    {err: fmt.Errorf("model API status 400: %w", domain.ErrRefused)},
		domain.UnreadNoAnswer:   {err: fmt.Errorf("model API status 503: %w", domain.ErrNoAnswer)},
		domain.UnreadUnreadable: {answer: "Готово"},
	} {
		store, build, failures := namesFixture(model)
		policy := &readingPolicyOfTheTest{}
		build.WithPolicy(policy)
		status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
		if err != nil || status != jobs.StatusSucceeded || store.names != nil || len(*failures) != 1 {
			t.Fatalf("%s: RunForClaim() = %q, %v, registry %+v, failures %v", reason, status, err, store.names, *failures)
		}
		if len(policy.names) != 1 || !reflect.DeepEqual(policy.views, []string{string(cardsSubject.SourceView)}) {
			t.Fatalf("%s: marked %v by %v", reason, policy.names, policy.views)
		}
		for _, marked := range policy.names {
			if marked != reason {
				t.Fatalf("%s: marked with %q", reason, marked)
			}
		}
		store.outcome = nil
		if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded ||
			model.calls != 1 || store.names != nil || len(*failures) != 1 {
			t.Fatalf("%s: the same names = %q, %v, %d calls, failures %v; want no call", reason, status, err, model.calls, *failures)
		}
		card := store.cards[CardKey{ChapterID: "c2", ContentHash: "h2"}]
		card.Characters = append(card.Characters, domain.Mention{Name: "Любава"})
		store.cards[CardKey{ChapterID: "c2", ContentHash: "h2"}] = card
		if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.calls != 2 || len(policy.names) != 2 {
			t.Fatalf("%s: a new name = %v, %d calls, marks %v; want one new call", reason, err, model.calls, policy.names)
		}
	}
}

// RR1.1, RR1.6: a stop, a cancel, a failed write of the register and an error the client did not type — the helper
// disconnected mid-step — mark nothing; a mark that was not written is logged and leaves the Job succeeded.
func TestWhatMarksNoNames(t *testing.T) {
	for name, model := range map[string]*namesModel{
		"stop":    {err: &domain.Stopped{Stop: domain.StopFunds}},
		"untyped": {err: errors.New("model API status 400: the helper is not connected")},
	} {
		store, build, _ := namesFixture(model)
		policy := &readingPolicyOfTheTest{}
		build.WithPolicy(policy)
		if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || len(policy.names) != 0 {
			t.Fatalf("%s: %v, marks %v, outcome %+v", name, err, policy.names, store.outcome)
		}
	}
	store, build, _ := namesFixture(&namesModel{answer: namesAnswer})
	store.namesErr = errors.New("store down")
	policy := &readingPolicyOfTheTest{}
	build.WithPolicy(policy)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || len(policy.names) != 0 {
		t.Fatalf("a failed save: %v, marks %v", err, policy.names)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store, build, _ = namesFixture(&namesModel{})
	build.model = &cancelingNamesModel{cancel: cancel}
	build.WithPolicy(policy)
	if _, err := build.RunForClaim(ctx, cardsClaim, cardsSubject, false); !errors.Is(err, context.Canceled) || len(policy.names) != 0 || store.outcome != nil {
		t.Fatalf("a cancel: %v, marks %v", err, policy.names)
	}
	store, build, failures := namesFixture(&namesModel{answer: "Готово"})
	build.WithPolicy(&readingPolicyOfTheTest{markErr: errors.New("mark down")})
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded ||
		store.outcome == nil || store.outcome.Status != jobs.StatusSucceeded || !reflect.DeepEqual(*failures, []string{cardsProjectID + " mark", cardsProjectID + " parse"}) {
		t.Fatalf("a mark not written: %q, %v, failures %v", status, err, *failures)
	}
}

// RR1.6: a Job canceled while the model answers marks nothing even when the answer is one of the named reasons — not a
// cancel of the client (context.Canceled has no reason anyway) but an answer that does not parse or is cut, with the Job
// canceled at the same moment.
func TestACanceledJobMarksNoNamesWhateverTheAnswer(t *testing.T) {
	for name, answer := range map[string]cancelingAnswer{
		"unreadable": {text: "Готово"},
		"cut":        {err: fmt.Errorf("the answer: %w", domain.ErrAnswerCut)},
		"refused":    {err: fmt.Errorf("model API status 400: %w", domain.ErrRefused)},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		answer.cancel = cancel
		store, build, failures := namesFixture(&namesModel{})
		build.model = &answer
		policy := &readingPolicyOfTheTest{}
		build.WithPolicy(policy)
		if _, err := build.RunForClaim(ctx, cardsClaim, cardsSubject, false); !errors.Is(err, context.Canceled) || answer.calls != 1 ||
			len(policy.names) != 0 || store.outcome != nil || len(*failures) != 0 {
			t.Fatalf("%s: %v, %d calls, marks %v, outcome %+v, failures %v; want a cancel and no mark", name, err, answer.calls,
				policy.names, store.outcome, *failures)
		}
		cancel()
	}
}

// cancelingAnswer is a model whose answer comes with the Job canceled: the text or the error it gives back is what the model
// said, not the cancel.
type cancelingAnswer struct {
	cancel context.CancelFunc
	text   string
	err    error
	calls  int
}

func (model *cancelingAnswer) Complete(context.Context, string, string) (string, quota.ModelUsage, error) {
	model.calls++
	model.cancel()
	return model.text, quota.ModelUsage{}, model.err
}

// RR1.2: names marked need no consent — a cards Job of a saga the model may not read now ends succeeded, not cards.consent,
// for them; names not marked keep the Job waiting for the consent.
func TestMarkedNamesNeedNoConsent(t *testing.T) {
	model := &namesModel{answer: "Готово"}
	store, build, _ := namesFixture(model)
	policy := &readingPolicyOfTheTest{}
	build.WithPolicy(policy)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || len(policy.names) != 1 {
		t.Fatalf("the names were not marked: %v, %v", err, policy.names)
	}
	policy.closed = true
	store.outcome = nil
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded || model.calls != 1 {
		t.Fatalf("marked names without the consent = %q, %v, %d calls; want succeeded", status, err, model.calls)
	}
	policy.names = nil
	store.outcome = nil
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusFailed ||
		store.outcome.Error == nil || store.outcome.Error.Code != ConsentCardsCode || model.calls != 1 {
		t.Fatalf("names that wait without the consent = %q, %v, %+v", status, err, store.outcome)
	}
}

// RR1.2, RR1.8: the catch-up queues no cards Job for what the marks keep — a chapter that did not read, names that did not
// come together, with a register or without one — and queues it, as before, without the marks; the start of the site keeps
// its rule.
func TestTheCatchUpLeavesWhatTheMarksKeep(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
	}}
	versions := indexedVersions{{ProjectID: "saga", View: "published"}}
	both := map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true, {ChapterID: "c2", ContentHash: "h2"}: true}
	named := namedVersions{versionCards: versionCards{"saga": both}, digests: map[string]string{"saga": ""}, text: text}
	_, digest := domain.BuildNameList(map[int]*domain.Card{0: named.card(), 1: named.card()})
	queued := func(useCase *QueueMissingCards, names bool) int {
		t.Helper()
		var count int
		var err error
		if names {
			count, err = useCase.ExecuteFor(context.Background(), "saga")
		} else {
			count, err = useCase.Execute(context.Background())
		}
		if err != nil {
			t.Fatal(err)
		}
		return count
	}

	// No register and the names of the cards marked: the start queues nothing; not marked — the Job, as on the site.
	marked := &readingPolicyOfTheTest{names: map[string]string{digest: domain.UnreadUnreadable}}
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}).WithPolicy(marked), false); got != 0 {
		t.Fatalf("marked names without a register queued %d", got)
	}
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}).WithPolicy(&readingPolicyOfTheTest{}), false); got != 1 {
		t.Fatalf("names without a register queued %d", got)
	}
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}), false); got != 1 {
		t.Fatalf("the site queued %d", got)
	}

	// A stale register: «Продолжить» queues nothing for marked names, the Job for names that wait.
	named.digests["saga"] = "the register of the first book"
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}).WithPolicy(marked), true); got != 0 {
		t.Fatalf("marked names with a stale register queued %d", got)
	}
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}).WithPolicy(&readingPolicyOfTheTest{}), true); got != 1 {
		t.Fatalf("names that wait queued %d", got)
	}

	// A chapter that did not read: no Job for it; the site queues it.
	named.versionCards = versionCards{"saga": {{ChapterID: "c1", ContentHash: "h1"}: true}}
	named.digests["saga"] = digest
	chapter := &readingPolicyOfTheTest{marked: map[CardKey]string{{ChapterID: "c2", ContentHash: "h2"}: domain.UnreadCutAnswer}}
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}).WithPolicy(chapter), false); got != 0 {
		t.Fatalf("a chapter that did not read queued %d", got)
	}
	if got := queued(NewQueueMissingCards(versions, text, named, &cardsQueue{}), false); got != 1 {
		t.Fatalf("the site queued %d for a chapter without a card", got)
	}
}
