package sagamemory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
)

// unbuiltModel answers the card of each chapter by its title: the error of the title, an answer that does not read, or a
// card; cancel is called on the title it names.
type unbuiltModel struct {
	fail       map[string]error
	unreadable string
	cancelAt   string
	cancel     context.CancelFunc
}

func (model *unbuiltModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	title := strings.TrimSpace(strings.Split(strings.SplitN(strings.SplitN(input, "\n", 2)[0], ", ", 2)[1], " (гл.")[0])
	if title == model.cancelAt {
		model.cancel()
		return "", quota.ModelUsage{}, ctx.Err()
	}
	if instructions == domain.CurrentCardInstructions.Pass {
		return `{}`, cardCallUsage, nil
	}
	if err := model.fail[title]; err != nil {
		return "", cardCallUsage, err
	}
	if title == model.unreadable {
		return "не JSON", cardCallUsage, nil
	}
	return `{"summary": "Пересказ: ` + title + `", "entities": [{"name": "Путь", "type": "Локация", "p": 9}]}`, cardCallUsage, nil
}

// unbuiltLog keeps the lines of the chapters left without a card, by the chapter.
type unbuiltLog struct {
	mu    sync.Mutex
	lines []Unbuilt
}

func (log *unbuiltLog) hear(unbuilt Unbuilt) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.lines = append(log.lines, unbuilt)
}

func (log *unbuiltLog) sorted() []Unbuilt {
	log.mu.Lock()
	defer log.mu.Unlock()
	lines := append([]Unbuilt(nil), log.lines...)
	slices.SortFunc(lines, func(a, b Unbuilt) int { return strings.Compare(a.ChapterID, b.ChapterID) })
	for index := range lines {
		lines[index].Seconds = 0
	}
	return lines
}

// specs/2026-11-17-long-chapters.md LC4.1: every chapter an attempt of a cards Job leaves without a card once the model was
// called is one line of the log — the Job, the saga, the chapter, the parts, the attempts, the outcome, the seconds and the
// tokens; a chapter with a card, one the run did not begin after a stop and a canceled run give none.
func TestAChapterLeftWithoutACardIsALineOfTheLog(t *testing.T) {
	line := func(chapter, outcome string, attempts int) Unbuilt {
		return Unbuilt{JobID: cardsJobID, ProjectID: cardsProjectID, ChapterID: chapter, Parts: 1, Attempts: attempts, Outcome: outcome,
			InputTokens: int64(attempts) * cardCallUsage.InputTokens, OutputTokens: int64(attempts) * cardCallUsage.OutputTokens}
	}
	model := &unbuiltModel{unreadable: "Глава 2. Путь", fail: map[string]error{
		"Глава 3. Перевал": fmt.Errorf("the answer: %w", domain.ErrAnswerCut),
		"Глава 4. Застава": fmt.Errorf("the attempt: %w", domain.ErrAttemptTimeout),
	}}
	log := &unbuiltLog{}
	build := NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, model, nil, "test-model", 4, nil, nil).WithUnbuilt(log.hear)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); !errors.Is(err, ErrRetryCards) {
		t.Fatal(err)
	}
	want := []Unbuilt{line("c2", UnbuiltUnreadable, cardAttempts), line("c3", UnbuiltCut, cardAttempts), line("c4", UnbuiltTimeout, 1)}
	if lines := log.sorted(); !reflect.DeepEqual(lines, want) {
		t.Fatalf("the site logged %+v\nwant %+v", lines, want)
	}

	model = &unbuiltModel{fail: map[string]error{
		"Глава 2. Путь":    fmt.Errorf("upstream: %w", domain.ErrRefused),
		"Глава 3. Перевал": &domain.Stopped{Stop: domain.StopFunds},
	}}
	log = &unbuiltLog{}
	build = NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, model, nil, "test-model", 4, nil, nil).
		WithPolicy(&readingPolicyOfTheTest{atOnce: 1}).WithUnbuilt(log.hear)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil {
		t.Fatal(err)
	}
	want = []Unbuilt{line("c2", UnbuiltRefused, 1), line("c3", UnbuiltStopped, 1)}
	want[1].InputTokens, want[1].OutputTokens = cardCallUsage.InputTokens, cardCallUsage.OutputTokens
	if lines := log.sorted(); !reflect.DeepEqual(lines, want) {
		t.Fatalf("under the policy logged %+v\nwant %+v", lines, want)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log = &unbuiltLog{}
	model = &unbuiltModel{cancelAt: "Глава 2. Путь", cancel: cancel}
	build = NewBuildCards(fourChapters(), &cardsStore{existing: map[CardKey]bool{}}, model, nil, "test-model", 1, nil, nil).WithUnbuilt(log.hear)
	if _, err := build.RunForClaim(ctx, cardsClaim, cardsSubject, false); !errors.Is(err, context.Canceled) || len(log.sorted()) != 0 {
		t.Fatalf("a canceled run = %v, logged %+v", err, log.sorted())
	}

	// The second part does not read: the first one, which reads, has always begun before it, so the second begins too and the
	// attempts are those of both, whatever goroutine runs first (a first part that failed fast left the second unbegun).
	parts := &partsModel{unreadPart: "Часть 2 "}
	log = &unbuiltLog{}
	build = NewBuildCards(longChapters(1), &cardsStore{existing: map[CardKey]bool{}}, parts, nil, "test-model", 4, nil, nil).WithUnbuilt(log.hear)
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); !errors.Is(err, ErrRetryCards) {
		t.Fatal(err)
	}
	long := Unbuilt{JobID: cardsJobID, ProjectID: cardsProjectID, ChapterID: "c1", Parts: 2, Attempts: cardAttempts + 1, Outcome: UnbuiltUnreadable,
		InputTokens: (cardAttempts + 2) * cardCallUsage.InputTokens, OutputTokens: (cardAttempts + 2) * cardCallUsage.OutputTokens}
	if lines := log.sorted(); !reflect.DeepEqual(lines, []Unbuilt{long}) {
		t.Fatalf("a long chapter logged %+v\nwant %+v", lines, long)
	}
}

// LC4.1: the outcome of a chapter left without a card, by the error of its writer.
func TestTheOutcomeOfAChapterLeftWithoutACard(t *testing.T) {
	for _, row := range []struct {
		err     error
		unread  string
		outcome string
	}{
		{fmt.Errorf("x: %w", domain.ErrAnswerCut), UnreadCutAnswer, UnbuiltCut},
		{fmt.Errorf("x: %w", domain.ErrModelOutput), UnreadUnreadable, UnbuiltUnreadable},
		{fmt.Errorf("x: %w", domain.ErrRefused), domain.UnreadRefused, UnbuiltRefused},
		{&domain.Stopped{Stop: domain.StopConnection}, UnreadCutAnswer, UnbuiltStopped},
		{&disclosure.JournalError{Code: disclosure.CodeUnavailable}, "", UnbuiltStopped},
		{errPartsHalted, "", UnbuiltStopped},
		{fmt.Errorf("x: %w", domain.ErrAttemptTimeout), "", UnbuiltTimeout},
		{errors.New("model API status 503"), "", UnbuiltFailed},
	} {
		if outcome := unbuiltOutcome(row.err, row.unread); outcome != row.outcome {
			t.Errorf("%v (%q) = %s, want %s", row.err, row.unread, outcome, row.outcome)
		}
	}
}
