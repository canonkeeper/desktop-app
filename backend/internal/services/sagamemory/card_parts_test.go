package sagamemory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// longParagraphs are paragraphs of the saga «Северный предел» of the given runes each.
func longParagraphs(lengths ...int) []domain.Paragraph {
	const sentence = "Ветер выл над заставой. "
	paragraphs := make([]domain.Paragraph, len(lengths))
	for index, length := range lengths {
		runes := []rune(strings.Repeat(sentence, length/utf8.RuneCountInString(sentence)+1))
		paragraphs[index] = domain.Paragraph{Text: string(runes[:length-1]) + "."}
	}
	return paragraphs
}

// overTheSplit are the lengths of a chapter one rune over the split: two parts, ¶1–¶175 and ¶176–¶351.
func overTheSplit() []int {
	lengths := make([]int, 351)
	for index := range lengths[:350] {
		lengths[index] = 1_000
	}
	lengths[350] = 1
	return lengths
}

// partCall is a call the model of the parts heard: the step, the input, the thread and what it named.
type partCall struct {
	pass   bool
	input  string
	thread []string
	named  disclosure.Named
}

// partsModel answers each part by its line: a card whose events point at ¶100 and ¶200 and a pass that adds a hook at ¶180.
// unreadPart has the card of its part never read, stopPart stops the reading on its card; block holds every call until it
// closes once inFlight calls go at once.
type partsModel struct {
	mu         sync.Mutex
	calls      []partCall
	unreadPart string
	stopPart   string
	inFlight   int
	going, top int
	reached    chan struct{}
	closed     bool
	onCall     func(input string)
	// fail has the card of a part fail with its error, by the beginning of the line of the part ("" — every card).
	fail map[string]error
}

func (model *partsModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	thread, _ := modelthread.Of(ctx)
	named, _ := disclosure.Of(ctx)
	pass := instructions == domain.CurrentCardInstructions.Pass
	model.mu.Lock()
	model.calls = append(model.calls, partCall{pass: pass, input: input, thread: thread, named: named})
	model.going++
	model.top = max(model.top, model.going)
	if model.inFlight > 0 && model.going == model.inFlight && !model.closed {
		close(model.reached)
		model.closed = true
	}
	reached := model.reached
	onCall := model.onCall
	model.mu.Unlock()
	defer func() {
		model.mu.Lock()
		model.going--
		model.mu.Unlock()
	}()
	if reached != nil {
		select {
		case <-reached:
		case <-time.After(5 * time.Second):
		}
	}
	if onCall != nil {
		onCall(input)
	}
	line := strings.SplitN(input, "\n", 3)[1]
	if model.stopPart != "" && strings.HasPrefix(line, model.stopPart) && !pass {
		return "", quota.ModelUsage{}, &domain.Stopped{Stop: domain.StopFunds}
	}
	for prefix, err := range model.fail {
		if strings.HasPrefix(line, prefix) && !pass {
			return "", cardCallUsage, err
		}
	}
	if model.unreadPart != "" && strings.HasPrefix(line, model.unreadPart) && !pass {
		return "не JSON", cardCallUsage, nil
	}
	if pass {
		return `{"hooks": [{"what": "письмо", "p": 180}]}`, cardCallUsage, nil
	}
	summary := "Глава."
	if strings.HasPrefix(line, "Часть ") {
		summary = strings.SplitN(line, ":", 2)[0] + "."
	}
	return `{"summary": "` + summary + `", "events": [{"event": "Огни у ¶100", "p": 100}, {"event": "Огни у ¶200", "p": 200}],
	 "entities": [{"name": "Путь", "type": "Локация", "p": 100000}]}`, cardCallUsage, nil
}

func (model *partsModel) of(pass bool) []partCall {
	model.mu.Lock()
	defer model.mu.Unlock()
	var calls []partCall
	for _, call := range model.calls {
		if call.pass == pass {
			calls = append(calls, call)
		}
	}
	return calls
}

// specs/2026-11-17-long-chapters.md LC1.2, LC1.3, LC1.5: a chapter over the split is written by its parts — the card and the
// pass of each with the input of the part, under the thread of its bucket, naming its paragraphs — and its card is merged from
// theirs, each parsed within its part; the attempts and the spend are those of the parts.
func TestALongChapterIsWrittenByItsParts(t *testing.T) {
	chapter := &domain.Chapter{ID: "chapter-north", Key: "7", BookOrder: 1, Title: "Глава 7. Застава", Paragraphs: longParagraphs(overTheSplit()...)}
	model := &partsModel{}
	written, err := NewCardWriter(model, domain.CurrentCardInstructions).Write(jobs.WithJobID(context.Background(), "job-cards-north"), chapter)
	if err != nil {
		t.Fatal(err)
	}
	parts := []domain.PartRange{{First: 1, Last: 175}, {First: 176, Last: 351}}
	cards, passes := model.of(false), model.of(true)
	if len(cards) != 2 || len(passes) != 2 {
		t.Fatalf("the model heard %d cards and %d passes", len(cards), len(passes))
	}
	slices.SortFunc(cards, func(a, b partCall) int { return strings.Compare(a.input[:120], b.input[:120]) })
	slices.SortFunc(passes, func(a, b partCall) int { return strings.Compare(a.input[:120], b.input[:120]) })
	for index, part := range parts {
		input := domain.PartText(chapter, part, index+1, 2)
		bucket := bucketOf("7\x00" + strconv.Itoa(index+1))
		if cards[index].input != input || !strings.HasPrefix(passes[index].input, input+"\n\nКАРТОЧКА:\n{") {
			t.Fatalf("part %d was read from %.80q", index+1, cards[index].input)
		}
		if want := []string{"cards", "job-cards-north", "card", bucket}; !slices.Equal(cards[index].thread, want) {
			t.Fatalf("the card of part %d went under %q, want %q", index+1, cards[index].thread, want)
		}
		if want := []string{"cards", "job-cards-north", "card_pass", bucket}; !slices.Equal(passes[index].thread, want) {
			t.Fatalf("the pass of part %d went under %q, want %q", index+1, passes[index].thread, want)
		}
		want := []disclosure.Chapter{{ID: "chapter-north", Precision: disclosure.Paragraphs, Paragraphs: []disclosure.Range{{From: part.First, To: part.Last}}}}
		for _, call := range []partCall{cards[index], passes[index]} {
			if !reflect.DeepEqual(call.named.Chapters, want) {
				t.Fatalf("part %d named %+v", index+1, call.named)
			}
		}
	}
	first, err := domain.ParseCard(`{"summary": "Часть 1 из 2.", "events": [{"event": "Огни у ¶100", "p": 100}, {"event": "Огни у ¶200", "p": 200}], "entities": [{"name": "Путь", "type": "Локация"}]}`, 1, 175)
	if err != nil {
		t.Fatal(err)
	}
	first.Add(domain.Card{Hooks: []domain.Hook{{What: "письмо"}}})
	second, _ := domain.ParseCard(`{"summary": "Часть 2 из 2.", "events": [{"event": "Огни у ¶100"}, {"event": "Огни у ¶200", "p": 200}], "entities": [{"name": "Путь", "type": "Локация"}]}`, 176, 351)
	second.Add(domain.Card{Hooks: []domain.Hook{{What: "письмо", P: 180}}})
	if want := domain.MergeCards([]domain.Card{first, second}); !reflect.DeepEqual(written.Card, want) {
		t.Fatalf("the card of the chapter = %+v\nwant %+v", written.Card, want)
	}
	if written.Attempts != 2 || written.Parts != 2 || written.Usage.Calls != 4 || written.Unread != "" {
		t.Fatalf("written = %d attempts, %d parts, %+v, %q", written.Attempts, written.Parts, written.Usage, written.Unread)
	}

	short := &domain.Chapter{ID: "chapter-short", Key: "8", BookOrder: 1, Title: "Глава 8", Paragraphs: longParagraphs(overTheSplit()[:349]...)}
	model = &partsModel{}
	if written, err := NewCardWriter(model, domain.CurrentCardInstructions).Write(jobs.WithJobID(context.Background(), "job-cards-north"), short); err != nil || written.Parts != 1 {
		t.Fatalf("a chapter of %d paragraphs = %d parts, %v", len(short.Paragraphs), written.Parts, err)
	}
	if cards := model.of(false); len(cards) != 1 || cards[0].input != domain.ChapterText(short) ||
		!slices.Equal(cards[0].thread, []string{"cards", "job-cards-north", "card", bucketOf("8")}) || cards[0].named.Chapters[0].Precision != disclosure.Whole {
		t.Fatalf("a chapter up to the split was read as %+v", cards)
	}
}

// longTextSource is a version whose chapters hold the paragraphs of their lengths.
type longTextSource struct {
	chapters []SelectedChapter
	lengths  map[string][]int
}

func (source longTextSource) Selection(context.Context, string, string) (Selection, error) {
	return Selection{ProjectID: cardsProjectID, View: "draft", Fingerprint: "f", Chapters: source.chapters}, nil
}

func (source longTextSource) Chapters(_ context.Context, selection Selection) ([]domain.Chapter, error) {
	chapters := make([]domain.Chapter, len(selection.Chapters))
	for index, selected := range selection.Chapters {
		chapters[index] = domain.Chapter{
			ID: selected.ID, ContentHash: selected.ContentHash, BookOrder: selected.BookOrder, Order: selected.Order,
			Title: selected.Title, Paragraphs: longParagraphs(source.lengths[selected.ID]...),
		}
	}
	return chapters, nil
}

func longChapters(count int) longTextSource {
	source := longTextSource{lengths: map[string][]int{}}
	for number := 1; number <= count; number++ {
		id := "c" + strconv.Itoa(number)
		source.chapters = append(source.chapters, SelectedChapter{ID: id, ContentHash: "h" + id, BookOrder: 1, Order: number, Title: "Глава " + strconv.Itoa(number)})
		source.lengths[id] = overTheSplit()
	}
	return source
}

// LC1.3, CN3.5: under the policy of the reading the parts of a chapter go one by one, so the run gives the model no more
// requests at once than the chapters it takes; the site gives it up to four parts of a chapter at once.
func TestTheRunGivesTheModelNoMoreRequestsAtOnceThanThePolicySays(t *testing.T) {
	ctx := context.Background()
	site := NewBuildCards(longChapters(1), &cardsStore{}, &partsModel{}, nil, "test-model", 8, nil, nil)
	if atOnce := site.writerOfRun(ctx, nil).atOnce; atOnce != 4 {
		t.Fatalf("the site reads %d parts of a chapter at once", atOnce)
	}
	if atOnce := site.WithPolicy(&readingPolicyOfTheTest{}).writerOfRun(ctx, nil).atOnce; atOnce != 4 {
		t.Fatalf("a policy that names no number reads %d parts at once", atOnce)
	}
	if atOnce := site.WithPolicy(&readingPolicyOfTheTest{atOnce: 2}).writerOfRun(ctx, nil).atOnce; atOnce != 1 {
		t.Fatalf("under the policy of two chapters at once the parts go %d at once", atOnce)
	}

	model := &partsModel{inFlight: 2, reached: make(chan struct{})}
	store := &cardsStore{existing: map[CardKey]bool{}}
	build := NewBuildCards(longChapters(3), store, model, nil, "test-model", 8, nil, nil).WithPolicy(&readingPolicyOfTheTest{atOnce: 2})
	if _, err := build.RunForClaim(ctx, cardsClaim, cardsSubject, false); err != nil {
		t.Fatal(err)
	}
	if model.top != 2 || len(store.saved) != 3 || len(model.of(false)) != 6 {
		t.Fatalf("under the policy of two at once %d calls went at once; %d cards of 3 saved", model.top, len(store.saved))
	}
	steps := map[string][]string{}
	for _, call := range model.calls {
		lines := strings.SplitN(call.input, "\n", 3)
		steps[lines[0]] = append(steps[lines[0]], strings.SplitN(lines[1], ":", 2)[0]+map[bool]string{false: " card", true: " pass"}[call.pass])
	}
	for chapter, said := range steps {
		if want := []string{"Часть 1 из 2 card", "Часть 1 из 2 pass", "Часть 2 из 2 card", "Часть 2 из 2 pass"}; !slices.Equal(said, want) {
			t.Fatalf("%s was read as %q", chapter, said)
		}
	}

	model = &partsModel{inFlight: 4, reached: make(chan struct{})}
	source := longChapters(1)
	source.lengths["c1"] = make([]int, 1_700)
	for index := range source.lengths["c1"] {
		source.lengths["c1"][index] = 1_000
	}
	store = &cardsStore{existing: map[CardKey]bool{}}
	if _, err := NewBuildCards(source, store, model, nil, "test-model", 8, nil, nil).RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil {
		t.Fatal(err)
	}
	if model.top != 4 || len(model.of(false)) != 6 || len(store.saved) != 1 {
		t.Fatalf("the site gave %d of the 6 parts at once; %d saved", model.top, len(store.saved))
	}
}

// LC1.3: a part that did not read leaves the chapter without a card, with why the part did not read; no new part begins, and
// so after a stop of the reading or a run that halted; the attempts and the spend are those of the parts that went.
func TestAPartThatDoesNotReadOrAStopBeginsNoNewPart(t *testing.T) {
	chapter := &domain.Chapter{ID: "chapter-north", Key: "7", BookOrder: 1, Title: "Глава 7. Застава", Paragraphs: longParagraphs(overTheSplit()...)}
	one := func(model CardModel) *CardWriter {
		writer := NewCardWriter(model, domain.CurrentCardInstructions)
		writer.atOnce = 1
		return writer
	}
	model := &partsModel{unreadPart: "Часть 1 "}
	written, err := one(model).Write(context.Background(), chapter)
	if !errors.Is(err, domain.ErrModelOutput) || written.Unread != UnreadUnreadable || written.Attempts != cardAttempts || written.Usage.Calls != cardAttempts {
		t.Fatalf("a part that did not read = %v, %q, %d attempts, %+v", err, written.Unread, written.Attempts, written.Usage)
	}
	if calls := len(model.calls); calls != cardAttempts {
		t.Fatalf("after the part that did not read the model heard %d calls", calls)
	}

	model = &partsModel{unreadPart: "Часть 2 "}
	written, err = one(model).Write(context.Background(), chapter)
	if !errors.Is(err, domain.ErrModelOutput) || written.Unread != UnreadUnreadable || written.Attempts != 1+cardAttempts || written.Usage.Calls != 2+cardAttempts {
		t.Fatalf("the second part that did not read = %v, %q, %d attempts, %+v", err, written.Unread, written.Attempts, written.Usage)
	}

	model = &partsModel{stopPart: "Часть 1 "}
	written, err = one(model).Write(context.Background(), chapter)
	if stop, ok := domain.StopOf(err); !ok || stop != domain.StopFunds || written.Unread != "" || len(model.calls) != 1 {
		t.Fatalf("a stop on a part = %v, %q, %d calls", err, written.Unread, len(model.calls))
	}

	halted := make(chan struct{})
	model = &partsModel{onCall: func(input string) {
		if strings.Contains(input, "Часть 1 из 2") {
			select {
			case <-halted:
			default:
				close(halted)
			}
		}
	}}
	writer := one(model)
	writer.halted = halted
	written, err = writer.Write(context.Background(), chapter)
	if !errors.Is(err, errPartsHalted) || written.Unread != "" || written.Usage.Calls != 2 || len(model.of(false)) != 1 {
		t.Fatalf("a run that halted = %v, %q, %+v", err, written.Unread, written.Usage)
	}

	// The site gives four parts at once: whatever went is waited for, and its spend is the chapter's.
	source := make([]int, 1_700)
	for index := range source {
		source[index] = 1_000
	}
	six := &domain.Chapter{ID: "chapter-six", Key: "9", BookOrder: 1, Title: "Глава 9", Paragraphs: longParagraphs(source...)}
	model = &partsModel{unreadPart: "Часть 1 "}
	written, err = NewCardWriter(model, domain.CurrentCardInstructions).Write(context.Background(), six)
	if !errors.Is(err, domain.ErrModelOutput) || written.Unread != UnreadUnreadable || written.Parts != 6 || written.Usage.Calls != int64(len(model.calls)) {
		t.Fatalf("a part of six that did not read = %v, %q, %d parts, %+v of %d calls", err, written.Unread, written.Parts, written.Usage, len(model.calls))
	}
}

// oneCardModel answers every card with its answer and every pass with nothing new.
type oneCardModel struct{ answer string }

func (model oneCardModel) Complete(_ context.Context, instructions, _ string) (string, quota.ModelUsage, error) {
	if instructions == domain.CurrentCardInstructions.Pass {
		return `{}`, cardCallUsage, nil
	}
	return model.answer, cardCallUsage, nil
}

// LC1.1: a chapter of one part is not merged — two records of a hero of one name by Normalize with other notes stay two, and
// the time of the chapter keeps how long it lasts, as before the parts.
func TestAChapterOfOnePartIsNotMerged(t *testing.T) {
	model := oneCardModel{answer: `{"summary": "Застава.", "when": {"opens": "На рассвете", "lasts": {"n": 2, "unit": "час"}},
	 "characters": [{"name": "Мирон", "note": "капитан заставы", "p": 1}, {"name": "мирон", "note": "ранен у перевала", "p": 1}]}`}
	chapter := &domain.Chapter{ID: "chapter-north", Key: "7", BookOrder: 1, Title: "Глава 7", Paragraphs: longParagraphs(1_000, 1_000)}
	written, err := NewCardWriter(model, domain.CurrentCardInstructions).Write(context.Background(), chapter)
	if err != nil || written.Parts != 1 {
		t.Fatalf("a chapter of one part = %d parts, %v", written.Parts, err)
	}
	if len(written.Card.Characters) != 2 || written.Card.When == nil || written.Card.When.Lasts == nil {
		t.Fatalf("a chapter of one part was merged: %+v, when %+v", written.Card.Characters, written.Card.When)
	}
}

// LC1.3 (the decision of the coordinator): the attempt of a piece the client of the site ended with the error of its time —
// it repeated it once already — is not repeated by the attempts of the card: the piece did not read at once, and no new part
// begins; a chapter of one part too.
func TestAPieceWhoseTimeRanOutIsNotAskedAgain(t *testing.T) {
	timedOut := fmt.Errorf("model API: the attempt ran out of its time: %w", domain.ErrAttemptTimeout)
	long := &domain.Chapter{ID: "chapter-north", Key: "7", BookOrder: 1, Title: "Глава 7. Застава", Paragraphs: longParagraphs(overTheSplit()...)}
	model := &partsModel{fail: map[string]error{"Часть 1 ": timedOut}}
	writer := NewCardWriter(model, domain.CurrentCardInstructions)
	writer.atOnce = 1
	written, err := writer.Write(context.Background(), long)
	if !errors.Is(err, domain.ErrAttemptTimeout) || written.Attempts != 1 || written.Unread != "" || len(model.calls) != 1 {
		t.Fatalf("a part whose time ran out = %v, %d attempts, %q, %d calls", err, written.Attempts, written.Unread, len(model.calls))
	}
	short := &domain.Chapter{ID: "chapter-short", Key: "8", BookOrder: 1, Title: "Глава 8", Paragraphs: longParagraphs(1_000)}
	whole := &partsModel{fail: map[string]error{"": timedOut}}
	written, err = NewCardWriter(whole, domain.CurrentCardInstructions).Write(context.Background(), short)
	if !errors.Is(err, domain.ErrAttemptTimeout) || written.Attempts != 1 || len(whole.calls) != 1 {
		t.Fatalf("a chapter whose time ran out = %v, %d attempts, %d calls", err, written.Attempts, len(whole.calls))
	}
}
