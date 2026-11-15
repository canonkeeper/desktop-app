package sagamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// stoppingModel writes the cards of the chapters until it meets the title it stops at — on the card or on its pass — or
// cuts every answer of the title it cuts.
type stoppingModel struct {
	mu       sync.Mutex
	stopAt   string
	stopPass bool
	stop     domain.Stop
	unread   string
	// cut has every answer of its title cut short; failed has every request of its title fail after the stream began, with
	// nothing paid — as the gateway of the app says a refusal of a provider of Chat Completions.
	cut, failed string
	calls       []string
}

func (model *stoppingModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	title := strings.TrimSpace(strings.SplitN(strings.SplitN(input, "\n", 2)[0], ", ", 2)[1])
	title = strings.TrimSpace(strings.Split(title, " (гл.")[0])
	pass := instructions == domain.CurrentCardInstructions.Pass
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls = append(model.calls, title)
	if title == model.stopAt && pass == model.stopPass {
		return "", quota.ModelUsage{}, &domain.Stopped{Stop: model.stop}
	}
	if title == model.unread && !pass {
		return "не JSON", cardCallUsage, nil
	}
	if title == model.cut && !pass {
		return "", cardCallUsage, fmt.Errorf("the answer: %w", domain.ErrAnswerCut)
	}
	if title == model.failed && !pass {
		return "", quota.ModelUsage{Calls: 1}, errors.New("upstream upstream_error (HTTP 503): overloaded")
	}
	if pass {
		return `{}`, cardCallUsage, nil
	}
	return `{"summary": "Пересказ: ` + title + `", "entities": [{"name": "Путь", "type": "Локация", "p": 9}]}`, cardCallUsage, nil
}

func (model *stoppingModel) asked(title string) int {
	model.mu.Lock()
	defer model.mu.Unlock()
	count := 0
	for _, call := range model.calls {
		if call == title {
			count++
		}
	}
	return count
}

// readingPolicyOfTheTest allows the reading unless closed, keeps the chapters marked «не прочиталась» and the names of a set
// marked by their digest — markErr fails the writes of the marks of the names —, and reads as many chapters at once as it
// says (none — the builder's own number).
type readingPolicyOfTheTest struct {
	mu     sync.Mutex
	marked map[CardKey]string
	atOnce int
	closed bool
	// names are the reasons of the marked sets of names by their digest, views the text versions their marks named.
	names   map[string]string
	views   []string
	markErr error
}

func (policy *readingPolicyOfTheTest) ChaptersAtOnce(context.Context) int { return policy.atOnce }

func (policy *readingPolicyOfTheTest) MarksChapters(context.Context) bool { return true }

func (policy *readingPolicyOfTheTest) ReadingAllowed(context.Context, string, bool) (bool, error) {
	return !policy.closed, nil
}

func (policy *readingPolicyOfTheTest) NamesUnread(_ context.Context, _ string, digest string) (string, error) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	return policy.names[digest], nil
}

func (policy *readingPolicyOfTheTest) MarkNamesUnread(_ context.Context, _ string, view, digest, reason string) error {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if policy.markErr != nil {
		return policy.markErr
	}
	if policy.names == nil {
		policy.names = map[string]string{}
	}
	policy.names[digest] = reason
	policy.views = append(policy.views, view)
	return nil
}

func (policy *readingPolicyOfTheTest) Unread(_ context.Context, _ string, version int) (map[CardKey]bool, error) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	unread := map[CardKey]bool{}
	for key := range policy.marked {
		unread[key] = version == domain.CurrentCardInstructions.Version
	}
	return unread, nil
}

func (policy *readingPolicyOfTheTest) MarkUnread(_ context.Context, _ string, key CardKey, version int, reason string) error {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if policy.marked == nil {
		policy.marked = map[CardKey]string{}
	}
	if version == domain.CurrentCardInstructions.Version {
		policy.marked[key] = reason
	}
	return nil
}

func fourChapters() cardsTextSource {
	return cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
		{ID: "c3", ContentHash: "h3", BookOrder: 1, Order: 3, Title: "Глава 3. Перевал"},
		{ID: "c4", ContentHash: "h4", BookOrder: 1, Order: 4, Title: "Глава 4. Застава"},
	}}
}

// specs/2026-11-13-desk-consent.md CN3.4: a typed stop of the reading — the provider has no money of the author — stops the
// handing out of the chapters at once: no chapter after it reaches the model, the chapter is not repeated, the Job ends with
// the code of the stop instead of another attempt, and the names, the layer and the readiness of the saga hear nothing.
func TestAStopOfTheReadingStopsTheHandingOut(t *testing.T) {
	model := &stoppingModel{stopAt: "Глава 2. Путь", stop: domain.StopFunds}
	store := &cardsStore{existing: map[CardKey]bool{}}
	var heard []TextVersion
	build := NewBuildCards(fourChapters(), store, model, nil, "test-model", 1, nil, func(_ context.Context, version TextVersion) {
		heard = append(heard, version)
	}).WithPolicy(&readingPolicyOfTheTest{})
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusFailed || store.outcome == nil || store.outcome.Error == nil || store.outcome.Error.Code != "reading.funds" {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v; want the Job ended by the stop", status, err, store.outcome)
	}
	if model.asked("Глава 2. Путь") != 1 || model.asked("Глава 3. Перевал") != 0 || model.asked("Глава 4. Застава") != 0 {
		t.Fatalf("the model was asked %v", model.calls)
	}
	if len(store.saved) != 1 || store.saved[0].ChapterID != "c1" || store.names != nil || len(heard) != 0 {
		t.Fatalf("saved %+v, names %+v, heard %v", store.saved, store.names, heard)
	}
	if _, err := jobs.EncodeError(*store.outcome.Error); err != nil {
		t.Fatalf("the outcome is not a valid Job error: %v", err)
	}
}

// CN3.5: a chapter is a unit — a stop on its pass keeps no card of it; after the continuation it is read whole.
func TestAStopOnThePassKeepsNoCard(t *testing.T) {
	model := &stoppingModel{stopAt: "Глава 1. Старт", stopPass: true, stop: domain.StopPaused}
	store := &cardsStore{existing: map[CardKey]bool{}}
	build := NewBuildCards(cardsTextSource{chapters: fourChapters().chapters[:1]}, store, model, nil, "test-model", 1, nil, nil).
		WithPolicy(&readingPolicyOfTheTest{})
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusFailed || store.outcome.Error.Code != "reading.paused" || len(store.saved) != 0 {
		t.Fatalf("a stop on the pass = %q, %v, saved %+v", status, err, store.saved)
	}
	model.stopAt = ""
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded || len(store.saved) != 1 {
		t.Fatalf("after the continuation = %q, %v, saved %+v", status, err, store.saved)
	}
}

// CN3.6: a chapter whose card did not come after the paid answers of the model is marked «не прочиталась» for its text and
// the version of the instructions: the Job ends without asking for another attempt, and the next Job does not take it. The
// site — a builder without the policy of the reading — repeats it as before.
func TestAChapterThatDidNotReadIsMarked(t *testing.T) {
	model := &stoppingModel{unread: "Глава 3. Перевал"}
	store := &cardsStore{existing: map[CardKey]bool{}}
	policy := &readingPolicyOfTheTest{}
	build := NewBuildCards(fourChapters(), store, model, nil, "test-model", 2, nil, nil).WithPolicy(policy)
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || len(store.saved) != 3 {
		t.Fatalf("RunForClaim() = %q, %v, saved %d; want the Job done with the chapter marked", status, err, len(store.saved))
	}
	if reason := policy.marked[CardKey{ChapterID: "c3", ContentHash: "h3"}]; reason != "unreadable" || model.asked("Глава 3. Перевал") != 3 {
		t.Fatalf("the mark = %q after %d attempts", reason, model.asked("Глава 3. Перевал"))
	}
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.asked("Глава 3. Перевал") != 3 {
		t.Fatalf("the next Job asked the marked chapter again: %d, %v", model.asked("Глава 3. Перевал"), err)
	}

	cut := &stoppingModel{cut: "Глава 2. Путь", failed: "Глава 4. Застава"}
	policy = &readingPolicyOfTheTest{}
	build = NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, cut, nil, "test-model", 2, nil, nil).WithPolicy(policy)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != ErrRetryCards {
		t.Fatalf("a chapter that failed unpaid = %v; want another attempt of the Job", err)
	}
	// An answer cut short was paid and is marked; a request that failed with nothing paid — a refusal of a provider of Chat
	// Completions said in the stream — is no answer that did not read: the chapter is not marked and is tried again.
	if reason, marked := policy.marked[CardKey{ChapterID: "c2", ContentHash: "h2"}]; !marked || reason != "cut_answer" {
		t.Fatalf("the mark of a cut answer = %q, %v", reason, marked)
	}
	if reason, marked := policy.marked[CardKey{ChapterID: "c4", ContentHash: "h4"}]; marked {
		t.Fatalf("a chapter whose requests failed unpaid was marked %q", reason)
	}

	site := NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, &stoppingModel{unread: "Глава 3. Перевал"}, nil, "test-model", 2, nil, nil)
	if _, err := site.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != ErrRetryCards {
		t.Fatalf("the site = %v; want another attempt", err)
	}
}

// samplePolicy records whether the reading was asked for the sample.
type samplePolicy struct {
	readingPolicyOfTheTest
	samples []bool
}

func (policy *samplePolicy) ReadingAllowed(_ context.Context, _ string, sample bool) (bool, error) {
	policy.samples = append(policy.samples, sample)
	return true, nil
}

// specs/2026-11-13-desk-consent.md CN2.1, CN2.2: the sample reads only the chapters of its subject — by the text they had —
// in another run of its Job too; it asks the policy as the sample, builds no
// names, closes no wave and queues no layer, even when it read every chapter of a saga of two.
func TestTheSampleReadsOnlyTheChaptersOfItsSubject(t *testing.T) {
	model := &stoppingModel{unread: "Глава 3. Перевал"}
	store := &cardsStore{existing: map[CardKey]bool{}}
	policy := &samplePolicy{}
	var heard []TextVersion
	readied := 0
	build := NewBuildCards(fourChapters(), store, model, nil, "test-model", 1, nil, func(_ context.Context, version TextVersion) {
		heard = append(heard, version)
	}).WithPolicy(policy).WithReady(func(context.Context, string) { readied++ })
	subject := cardsSubject
	subject.Sample = &jobs.CardsSample{Host: "api.deepseek.com", Chapters: []jobs.SampleChapter{
		{ChapterID: "c2", ContentHash: "h2"}, {ChapterID: "c3", ContentHash: "other-text"},
	}}
	store.subject = &subject
	for range 2 {
		// A second run — another attempt, or the Job back from its portion — reads the same chapters, none more.
		status, err := build.RunForClaim(context.Background(), cardsClaim, subject, false)
		if err != nil || status != jobs.StatusSucceeded {
			t.Fatalf("the sample = %q, %v", status, err)
		}
		for _, saved := range store.saved {
			store.existing[saved.CardKey] = true
		}
	}
	for _, title := range []string{"Глава 1. Старт", "Глава 3. Перевал", "Глава 4. Застава"} {
		if model.asked(title) != 0 {
			t.Fatalf("the sample read %s: %v", title, model.calls)
		}
	}
	if len(store.saved) != 1 || store.saved[0].ChapterID != "c2" || store.names != nil || len(heard) != 0 || readied != 0 {
		t.Fatalf("saved %+v, names %+v, heard %v, ready %d", store.saved, store.names, heard, readied)
	}
	for _, sample := range policy.samples {
		if !sample {
			t.Fatal("the sample asked the policy as the consent of the saga")
		}
	}
	if store.outcome.Result == nil || store.outcome.Result.Chapters != 1 {
		t.Fatalf("the result of the sample = %+v", store.outcome.Result)
	}
}

// concurrentModel writes every card and its pass after a short while and counts the calls that run at once.
type concurrentModel struct {
	mu          sync.Mutex
	running     int
	most        int
	calls       int
	passesFirst []bool
}

func (model *concurrentModel) Complete(_ context.Context, instructions, _ string) (string, quota.ModelUsage, error) {
	model.mu.Lock()
	model.running++
	model.calls++
	model.most = max(model.most, model.running)
	model.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	model.mu.Lock()
	model.running--
	model.mu.Unlock()
	if instructions == domain.CurrentCardInstructions.Pass {
		return `{}`, cardCallUsage, nil
	}
	return `{"summary": "Пересказ", "entities": [{"name": "Путь", "type": "Локация", "p": 9}]}`, cardCallUsage, nil
}

// specs/2026-11-13-desk-consent.md CN3.5: the reading takes as many chapters at once as its policy says — «Запросов
// одновременно» of the author —, so a stop finds at most that many chapters with a paid card and no pass; the site, a
// builder without the policy, keeps its own number.
func TestTheReadingTakesAsManyChaptersAtOnceAsItsPolicySays(t *testing.T) {
	model := &concurrentModel{}
	build := NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, model, nil, "test-model", 32, nil, nil).
		WithPolicy(&readingPolicyOfTheTest{atOnce: 1})
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("RunForClaim() = %q, %v", status, err)
	}
	if model.most != 1 || model.calls != 8 {
		t.Fatalf("the reading of one chapter at once ran %d calls at once of %d", model.most, model.calls)
	}

	site := &concurrentModel{}
	build = NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, site, nil, "test-model", 4, nil, nil)
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("the site = %q, %v", status, err)
	}
	if site.most < 2 {
		t.Fatalf("the site ran %d calls at once; want its own number", site.most)
	}
}
