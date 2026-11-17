package sagamemory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
)

// namedCardsModel answers the card, the pass and the names, and keeps what each call named in its context; refuse names the
// instructions the journal of the app does not send.
type namedCardsModel struct {
	mu     sync.Mutex
	named  map[string][]disclosure.Named
	refuse string
}

func (model *namedCardsModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	named, _ := disclosure.Of(ctx)
	model.mu.Lock()
	if model.named == nil {
		model.named = map[string][]disclosure.Named{}
	}
	model.named[instructions] = append(model.named[instructions], named)
	model.mu.Unlock()
	spent := quota.ModelUsage{Calls: 1, OutputTokens: 10}
	if instructions == model.refuse {
		return "", spent, &disclosure.JournalError{Code: disclosure.CodeUnavailable}
	}
	switch instructions {
	case domain.CurrentCardInstructions.Pass:
		return `{"hooks": [{"what": "письмо"}]}`, spent, nil
	case domain.NameInstructions:
		return namesAnswer, spent, nil
	default:
		return `{"summary": "Пересказ.", "entities": [{"name": "Путь", "type": "Локация", "p": 9}]}`, spent, nil
	}
}

func (model *namedCardsModel) calls(instructions string) []disclosure.Named {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]disclosure.Named(nil), model.named[instructions]...)
}

// specs/2026-11-14-desk-journal.md JR1.3, JR1.4: the card and the pass of a chapter name it whole, the register its names by
// their count — in the context of each call, before it goes.
func TestTheCardThePassAndTheNamesNameWhatTheyGive(t *testing.T) {
	model := &namedCardsModel{}
	store, build, _ := namesFixture(&namesModel{answer: namesAnswer})
	build.model, build.writer = model, NewCardWriter(model, domain.CurrentCardInstructions)
	delete(store.existing, CardKey{ChapterID: "c2", ContentHash: "h2"})
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil {
		t.Fatal(err)
	}
	// The next Job finds every chapter carded and builds the register.
	store.existing[CardKey{ChapterID: "c2", ContentHash: "h2"}] = true
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil {
		t.Fatal(err)
	}
	for _, instructions := range []string{domain.CurrentCardInstructions.Text, domain.CurrentCardInstructions.Pass} {
		calls := model.calls(instructions)
		if len(calls) != 1 || len(calls[0].Chapters) != 1 || calls[0].Chapters[0].ID != "c2" || calls[0].Chapters[0].Precision != disclosure.Whole {
			t.Fatalf("%.20s… named %+v", instructions, calls)
		}
	}
	if names := model.calls(domain.NameInstructions); len(names) != 1 || names[0].Names < 2 || len(names[0].Chapters) != 0 {
		t.Fatalf("the register named %+v", names)
	}
}

// JR1.6: a pass the journal did not send keeps no card, a register it did not send leaves the Job not done — the worker
// repeats it; neither is a stop of the reading.
func TestARefusalOfTheJournalIsNoCardAndNoDoneJob(t *testing.T) {
	model := &namedCardsModel{refuse: domain.CurrentCardInstructions.Pass}
	store, build, _ := namesFixture(&namesModel{answer: namesAnswer})
	build.model, build.writer = model, NewCardWriter(model, domain.CurrentCardInstructions)
	delete(store.existing, CardKey{ChapterID: "c2", ContentHash: "h2"})
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, true)
	if !errors.Is(err, disclosure.ErrJournal) || status != "" || len(store.saved) != 0 || store.outcome != nil {
		t.Fatalf("a pass the journal refused = %q, %v, saved %d, outcome %+v", status, err, len(store.saved), store.outcome)
	}
	if _, stopped := domain.StopOf(err); stopped {
		t.Fatal("a refusal of the journal is a stop of the reading")
	}
	if attempts := len(model.calls(domain.CurrentCardInstructions.Text)); attempts != 1 {
		t.Fatalf("the card was asked %d times", attempts)
	}

	names := &namedCardsModel{refuse: domain.NameInstructions}
	store, build, failures := namesFixture(&namesModel{answer: namesAnswer})
	build.model = names
	status, err = build.RunForClaim(context.Background(), cardsClaim, cardsSubject, true)
	if !errors.Is(err, disclosure.ErrJournal) || status != "" || store.outcome != nil || store.names != nil || len(*failures) != 0 {
		t.Fatalf("a register the journal refused = %q, %v, outcome %+v, names %+v, failures %v", status, err, store.outcome, store.names, *failures)
	}
	if !strings.Contains(err.Error(), disclosure.CodeUnavailable) {
		t.Fatal(err)
	}
}

// specs/2026-11-17-long-chapters.md LC1.5: the card and the pass of each part of a long chapter name the paragraphs of the
// part, not the chapter whole.
func TestThePartsOfALongChapterNameTheirParagraphs(t *testing.T) {
	model := &namedCardsModel{}
	chapter := &domain.Chapter{ID: "c1", Key: "1", BookOrder: 1, Title: "Глава 1", Paragraphs: longParagraphs(overTheSplit()...)}
	if _, err := NewCardWriter(model, domain.CurrentCardInstructions).Write(context.Background(), chapter); err != nil {
		t.Fatal(err)
	}
	for _, instructions := range []string{domain.CurrentCardInstructions.Text, domain.CurrentCardInstructions.Pass} {
		var ranges []disclosure.Range
		for _, named := range model.calls(instructions) {
			if len(named.Chapters) != 1 || named.Chapters[0].ID != "c1" || named.Chapters[0].Precision != disclosure.Paragraphs {
				t.Fatalf("%.20s… named %+v", instructions, named)
			}
			ranges = append(ranges, named.Chapters[0].Paragraphs...)
		}
		slices.SortFunc(ranges, func(a, b disclosure.Range) int { return a.From - b.From })
		if want := []disclosure.Range{{From: 1, To: 175}, {From: 176, To: 351}}; !slices.Equal(ranges, want) {
			t.Fatalf("%.20s… named the paragraphs %+v", instructions, ranges)
		}
	}
}
