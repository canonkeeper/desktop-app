package sagamemory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// specs/2026-11-15-desk-reading-no-answer.md NA1.1: a provider that does not answer — the stop «провайдер не отвечает» of the
// client of the app — stops the handing out of the chapters: the Job ends with reading.no_answer, the chapter is not marked
// and no chapter after it is asked; on the step of the names the Job ends the same and the names are not marked: the reading
// stands paused, and the author goes on.
func TestAProviderThatDoesNotAnswerStopsTheReadingAndMarksNothing(t *testing.T) {
	model := &stoppingModel{stopAt: "Глава 2. Путь", stop: domain.StopNoAnswer}
	store := &cardsStore{existing: map[CardKey]bool{}}
	policy := &readingPolicyOfTheTest{}
	build := NewBuildCards(fourChapters(), store, model, nil, "test-model", 1, nil, nil).WithPolicy(policy)
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusFailed || store.outcome == nil || store.outcome.Error == nil || store.outcome.Error.Code != "reading.no_answer" {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v; want the Job ended by the stop", status, err, store.outcome)
	}
	if model.asked("Глава 2. Путь") != 1 || model.asked("Глава 3. Перевал") != 0 || len(policy.marked) != 0 {
		t.Fatalf("asked %v, marked %v", model.calls, policy.marked)
	}

	names := &namesModel{err: &domain.Stopped{Stop: domain.StopNoAnswer}}
	namesStore, namesBuild, _ := namesFixture(names)
	namesPolicy := &readingPolicyOfTheTest{}
	namesBuild.WithPolicy(namesPolicy)
	status, err = namesBuild.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusFailed || namesStore.outcome == nil || namesStore.outcome.Error.Code != "reading.no_answer" || len(namesPolicy.names) != 0 {
		t.Fatalf("the names = %q, %v, outcome %+v, marks %v", status, err, namesStore.outcome, namesPolicy.names)
	}
}

// refusingModel writes the cards as stoppingModel does, but the provider does not take the card of the chapter it refuses —
// longer than the window of the model —, at every attempt.
type refusingModel struct {
	stoppingModel
	refused string
}

func (model *refusingModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	title := strings.TrimSpace(strings.SplitN(strings.SplitN(input, "\n", 2)[0], ", ", 2)[1])
	title = strings.TrimSpace(strings.Split(title, " (гл.")[0])
	if title == model.refused && instructions != domain.CurrentCardInstructions.Pass {
		model.mu.Lock()
		model.calls = append(model.calls, title)
		model.mu.Unlock()
		return "", quota.ModelUsage{}, fmt.Errorf("model API status 400: %w", domain.ErrRefused)
	}
	return model.stoppingModel.Complete(ctx, instructions, input)
}

// NA2.1–NA2.3: with the policy of the reading a chapter the provider did not take is marked refused after one attempt and
// the Job reads the other chapters through; the next Job leaves it while its mark acts, and reads it once the mark no longer
// acts (another model of the reading: the rule of the marks of the policy). A builder without the policy — the site — repeats
// the chapter three times, as it did.
func TestAChapterTheProviderRefusedIsMarkedAtOnce(t *testing.T) {
	model := &refusingModel{refused: "Глава 3. Перевал"}
	store := &cardsStore{existing: map[CardKey]bool{}}
	policy := &readingPolicyOfTheTest{}
	build := NewBuildCards(fourChapters(), store, model, nil, "test-model", 2, nil, nil).WithPolicy(policy)
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || len(store.saved) != 3 {
		t.Fatalf("RunForClaim() = %q, %v, saved %d; want the others read and the chapter marked", status, err, len(store.saved))
	}
	refused := CardKey{ChapterID: "c3", ContentHash: "h3"}
	if reason := policy.marked[refused]; reason != domain.UnreadRefused || model.asked("Глава 3. Перевал") != 1 {
		t.Fatalf("the mark = %q after %d attempts; want refused after one", reason, model.asked("Глава 3. Перевал"))
	}
	for _, saved := range store.saved {
		store.existing[saved.CardKey] = true
	}
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || model.asked("Глава 3. Перевал") != 1 {
		t.Fatalf("the next Job asked the marked chapter again: %d, %v", model.asked("Глава 3. Перевал"), err)
	}
	// Another model of the reading: the mark no longer acts, and the chapter reads — its card and its pass.
	policy.mu.Lock()
	policy.marked = nil
	policy.mu.Unlock()
	model.refused = ""
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded ||
		model.asked("Глава 3. Перевал") != 3 || len(store.saved) != 4 {
		t.Fatalf("a mark that does not act = %q, %v, asked %d, saved %d", status, err, model.asked("Глава 3. Перевал"), len(store.saved))
	}

	site := &refusingModel{refused: "Глава 3. Перевал"}
	siteBuild := NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, site, nil, "test-model", 2, nil, nil)
	if _, err := siteBuild.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != ErrRetryCards || site.asked("Глава 3. Перевал") != 3 {
		t.Fatalf("the site = %v after %d attempts; want three attempts and another of the Job", err, site.asked("Глава 3. Перевал"))
	}
}

// accountPolicy is the policy of the reading under the helper of the account: it marks no chapter — its MarkUnread writes
// nothing (specs/2026-11-13-desk-account-ai.md AI3.5).
type accountPolicy struct{ readingPolicyOfTheTest }

func (*accountPolicy) MarksChapters(context.Context) bool { return false }

func (*accountPolicy) MarkUnread(context.Context, string, CardKey, int, string) error { return nil }

// NA2.1, specs/2026-11-13-desk-account-ai.md AI3.5: a chapter the provider refused ends at once only where the policy marks
// it. Under the helper of the account, which marks none, it is repeated as it was before: three attempts and another run of
// the Job, and the last run fails by the chapters without cards — the Job never succeeds with a chapter that has neither its
// card nor a mark.
func TestUnderTheAccountARefusedChapterIsRepeatedAsBefore(t *testing.T) {
	model := &refusingModel{refused: "Глава 3. Перевал"}
	store := &cardsStore{existing: map[CardKey]bool{}}
	build := NewBuildCards(fourChapters(), store, model, nil, "test-model", 2, nil, nil).WithPolicy(&accountPolicy{})
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != ErrRetryCards || model.asked("Глава 3. Перевал") != 3 {
		t.Fatalf("a run = %v after %d attempts; want three attempts and another run of the Job", err, model.asked("Глава 3. Перевал"))
	}
	for _, saved := range store.saved {
		store.existing[saved.CardKey] = true
	}
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, true)
	if err != nil || status != jobs.StatusFailed || store.outcome == nil || store.outcome.Error == nil || store.outcome.Error.Code != IncompleteCardsCode ||
		model.asked("Глава 3. Перевал") != 6 {
		t.Fatalf("the last run = %q, %v, outcome %+v, asked %d; want failed by the chapter without its card", status, err, store.outcome, model.asked("Глава 3. Перевал"))
	}
}
