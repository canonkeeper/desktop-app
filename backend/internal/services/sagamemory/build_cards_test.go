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
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

const (
	cardsProjectID = "94000000-0000-4000-8000-000000000001"
	cardsJobID     = "94000000-0000-4000-8000-000000000002"
)

var cardsClaim = jobs.WorkerClaim{ProjectID: cardsProjectID, JobID: cardsJobID, ClaimIncarnation: 1}

var cardsSubject = jobs.ChapterCardsSubject{
	Type: jobs.KindChapterCards, ProjectID: cardsProjectID, SourceView: governance.SourceViewDraft,
}

// R2.2, R2.5: only chapters without a card of their current text reach the model; a chapter
// that keeps failing leaves the others' cards saved, asks for another attempt, and on the last
// attempt fails the Job with the number of chapters left.
func TestBuildCardsBuildsOnlyMissingChaptersAndReportsFailures(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
		{ID: "c3", ContentHash: "h3", BookOrder: 1, Order: 3, Title: "Глава 3. Сбой"},
	}}
	model := &cardsModel{fail: "Глава 3. Сбой"}
	store := &cardsStore{existing: map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true}}
	usage := &cardsUsage{}
	build := NewBuildCards(text, store, model, usage, "test-model", 2, nil, nil)

	_, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if !errors.Is(err, ErrRetryCards) || store.outcome != nil {
		t.Fatalf("RunForClaim() = %v, outcome %+v; want a retry without a terminal state", err, store.outcome)
	}
	if model.calls["Глава 1. Старт"] != 0 || model.calls["Глава 2. Путь"] != 2 || model.calls["Глава 3. Сбой"] != cardAttempts {
		t.Fatalf("model calls = %v, want none for the carded chapter, the card and its pass for the second and three attempts for the failing one", model.calls)
	}
	// R7.2: every chapter sent to the model counts for the project, the failing one too.
	if spent := usage.total(); spent.Calls != 1+cardAttempts || spent.InputTokens != 50*(1+cardAttempts) {
		t.Fatalf("recorded usage = %+v, want %d calls", spent, 1+cardAttempts)
	}
	if len(store.saved) != 1 || store.saved[0].ChapterID != "c2" || store.saved[0].ContentHash != "h2" ||
		store.saved[0].Model != "test-model" || store.saved[0].Card.Summary != "Пересказ: Глава 2. Путь" {
		t.Fatalf("saved = %+v, want the card of c2 for its current text", store.saved)
	}

	store.existing[CardKey{ChapterID: "c2", ContentHash: "h2"}] = true
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, true)
	if err != nil || status != jobs.StatusFailed || store.outcome == nil || store.outcome.Status != jobs.StatusFailed ||
		store.outcome.Error == nil || store.outcome.Error.Code != IncompleteCardsCode ||
		!strings.Contains(store.outcome.Error.Message, "1 из 3") {
		t.Fatalf("last attempt = %q, %v, outcome %+v; want failed with 1 of 3 chapters left", status, err, store.outcome)
	}
	if _, err := jobs.EncodeError(*store.outcome.Error); err != nil {
		t.Fatalf("failure is not a valid Job error: %v", err)
	}
}

func TestBuildCardsSucceedsWhenEveryChapterHasACard(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
	}}
	store := &cardsStore{existing: map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true}}
	status, err := NewBuildCards(text, store, &cardsModel{}, nil, "test-model", 4, nil, nil).RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	want := jobs.ChapterCardsResult{Type: jobs.KindChapterCards, SourceView: governance.SourceViewDraft, Chapters: 2, Built: 1}
	if err != nil || status != jobs.StatusSucceeded || store.outcome == nil || store.outcome.Result == nil || *store.outcome.Result != want {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v; want succeeded %+v", status, err, store.outcome, want)
	}
	if store.progress[len(store.progress)-1] != 1 {
		t.Fatalf("progress = %v, want it to reach 1", store.progress)
	}
}

type cardsTextSource struct {
	chapters []SelectedChapter
}

func (source cardsTextSource) Selection(context.Context, string, string) (Selection, error) {
	return Selection{ProjectID: cardsProjectID, View: "draft", Fingerprint: "f", Chapters: source.chapters}, nil
}

func (source cardsTextSource) Chapters(_ context.Context, selection Selection) ([]domain.Chapter, error) {
	chapters := make([]domain.Chapter, len(selection.Chapters))
	for i, selected := range selection.Chapters {
		chapters[i] = domain.Chapter{
			ID: selected.ID, ContentHash: selected.ContentHash, BookOrder: selected.BookOrder, Order: selected.Order,
			Title: selected.Title, Paragraphs: []domain.Paragraph{{Text: "Текст главы."}},
		}
	}
	return chapters, nil
}

func (cardsTextSource) Stems(context.Context, []string) (map[string]string, error) { return nil, nil }

type cardsModel struct {
	mu    sync.Mutex
	fail  string
	calls map[string]int
}

func (model *cardsModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	title := strings.TrimSpace(strings.SplitN(strings.SplitN(input, "\n", 2)[0], ", ", 2)[1])
	title = strings.TrimSpace(strings.Split(title, " (гл.")[0])
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.calls == nil {
		model.calls = map[string]int{}
	}
	model.calls[title]++
	if instructions != domain.CurrentCardInstructions.Text {
		return "", quota.ModelUsage{}, errors.New("unexpected instructions")
	}
	if title == model.fail {
		return "не JSON", cardCallUsage, nil
	}
	return `Вот карточка: {"summary": "Пересказ: ` + title + `", "entities": [{"name": "Путь", "type": "Локация", "p": 9}]}`, cardCallUsage, nil
}

type cardsStore struct {
	mu       sync.Mutex
	existing map[CardKey]bool
	saved    []StoredCard
	progress []float64
	outcome  *CardsOutcome
	yields   int

	cards      map[CardKey]domain.Card
	names      *StoredNames
	namesClaim jobs.WorkerClaim
	namesErr   error
}

func (store *cardsStore) Cards(_ context.Context, _ string, keys []CardKey, parts domain.CardParts) (map[CardKey]domain.Card, error) {
	if len(store.cards) > 0 && (!parts.Mentions || !parts.Facts || !parts.Knows || parts.Summary) {
		return nil, errors.New("the registry reads mentions, facts and knowledge only")
	}
	found := map[CardKey]domain.Card{}
	for _, key := range keys {
		if card, ok := store.cards[key]; ok {
			found[key] = card
		}
	}
	return found, nil
}

func (store *cardsStore) NamesDigest(context.Context, string) (string, error) {
	if store.names == nil {
		return "", nil
	}
	return store.names.Digest, nil
}

func (store *cardsStore) SaveNames(_ context.Context, claim jobs.WorkerClaim, names StoredNames) error {
	if store.namesErr != nil {
		return store.namesErr
	}
	store.names, store.namesClaim = &names, claim
	return nil
}

func (store *cardsStore) ExistingCards(_ context.Context, _ string, keys []CardKey) (map[CardKey]bool, error) {
	found := map[CardKey]bool{}
	for _, key := range keys {
		if store.existing[key] {
			found[key] = true
		}
	}
	return found, nil
}

func (store *cardsStore) SaveCard(_ context.Context, claim jobs.WorkerClaim, card StoredCard, progress float64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if claim != cardsClaim {
		return errors.New("wrong claim")
	}
	if card.Card.Entities[0].P != 0 || card.Card.Entities[0].Type != "локация" {
		return errors.New("card was not normalized")
	}
	store.saved = append(store.saved, card)
	store.progress = append(store.progress, progress)
	return nil
}

func (store *cardsStore) FinishCards(_ context.Context, claim jobs.WorkerClaim, outcome CardsOutcome) error {
	if claim != cardsClaim || outcome.Subject != cardsSubject {
		return errors.New("wrong claim")
	}
	store.outcome = &outcome
	return nil
}

func (store *cardsStore) YieldCards(_ context.Context, claim jobs.WorkerClaim) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if claim != cardsClaim {
		return errors.New("wrong claim")
	}
	store.yields++
	return nil
}

// slowCardsModel takes a while for each card; failing titles never parse.
type slowCardsModel struct {
	cardsModel
	delay time.Duration
	fail  bool
}

func (model *slowCardsModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	select {
	case <-ctx.Done():
		return "", quota.ModelUsage{}, ctx.Err()
	case <-time.After(model.delay):
	}
	if model.fail {
		return "не JSON", cardCallUsage, nil
	}
	return model.cardsModel.Complete(ctx, instructions, input)
}

func manyChapters(count int) cardsTextSource {
	chapters := make([]SelectedChapter, count)
	for index := range chapters {
		chapters[index] = SelectedChapter{
			ID: fmt.Sprintf("c%d", index+1), ContentHash: fmt.Sprintf("h%d", index+1), BookOrder: 1, Order: index + 1,
			Title: fmt.Sprintf("Глава %d. Путь", index+1),
		}
	}
	return cardsTextSource{chapters: chapters}
}

// R12.1, R12.2: new chapters go to the model only during the portion; a portion that built
// cards yields the Job, and the next claim builds the rest.
func TestBuildCardsYieldsAfterPortion(t *testing.T) {
	text := manyChapters(12)
	store := &cardsStore{existing: map[CardKey]bool{}}
	build := NewBuildCards(text, store, &slowCardsModel{delay: 15 * time.Millisecond}, nil, "test-model", 1, nil, nil)
	build.portion = 50 * time.Millisecond
	status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusQueued || store.yields != 1 || store.outcome != nil {
		t.Fatalf("first portion = %q, %v, yields %d, outcome %+v; want a yield", status, err, store.yields, store.outcome)
	}
	if len(store.saved) == 0 || len(store.saved) >= 12 {
		t.Fatalf("first portion built %d of 12 chapters", len(store.saved))
	}
	for _, card := range store.saved {
		store.existing[card.CardKey] = true
	}
	build.portion = time.Minute
	status, err = build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false)
	if err != nil || status != jobs.StatusSucceeded || len(store.saved) != 12 || store.yields != 1 {
		t.Fatalf("next portion = %q, %v, saved %d, yields %d; want the rest built", status, err, len(store.saved), store.yields)
	}
}

// R12.3: a portion that built nothing and had failures spends an attempt as before.
func TestBuildCardsPortionWithoutCardsRetries(t *testing.T) {
	store := &cardsStore{existing: map[CardKey]bool{}}
	build := NewBuildCards(manyChapters(5), store, &slowCardsModel{delay: 15 * time.Millisecond, fail: true}, nil, "test-model", 1, nil, nil)
	build.portion = 20 * time.Millisecond
	if _, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); !errors.Is(err, ErrRetryCards) || store.yields != 0 {
		t.Fatalf("RunForClaim() = %v, yields %d; want ErrRetryCards", err, store.yields)
	}
}

// cardCallUsage is what one stubbed card call costs.
var cardCallUsage = quota.ModelUsage{Calls: 1, InputTokens: 50, OutputTokens: 5}

type cardsUsage struct {
	mu      sync.Mutex
	records []quota.UsageRecord
}

func (usage *cardsUsage) RecordModelUsage(_ context.Context, record quota.UsageRecord) {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	usage.records = append(usage.records, record)
}

func (usage *cardsUsage) total() quota.ModelUsage {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	var total quota.ModelUsage
	for _, record := range usage.records {
		if record.Source != quota.UsageCards || record.ProjectID != cardsProjectID || record.Model != "test-model" {
			return quota.ModelUsage{}
		}
		total = total.Add(record.Usage)
	}
	return total
}

type passModel struct {
	calls   int
	failing bool
}

func (model *passModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	model.calls++
	spent := quota.ModelUsage{Calls: 1, OutputTokens: 10}
	if model.calls == 1 {
		return `{"summary": "Глава.", "hooks": [{"what": "шрам"}]}`, spent, nil
	}
	if model.failing {
		return "", spent, errors.New("model down")
	}
	if !strings.Contains(input, "КАРТОЧКА:") || !strings.Contains(input, "шрам") {
		return "", spent, errors.New("the pass does not see the card")
	}
	return `{"hooks": [{"what": "шрам"}, {"what": "письмо"}]}`, spent, nil
}

// specs/2026-11-01-index-foundation.md R2.3, R2.6: the pass adds what the card missed and is paid for;
// when it fails, the card stays.
func TestCardWriterPassOverTheCard(t *testing.T) {
	chapter := &domain.Chapter{Paragraphs: []domain.Paragraph{{Text: "Текст."}}}
	model := &passModel{}
	written, err := NewCardWriter(model, domain.CardInstructionsV4).Write(context.Background(), chapter)
	if err != nil || len(written.Card.Hooks) != 2 || written.Usage.Calls != 2 || written.Attempts != 1 {
		t.Fatalf("written %+v, err %v", written, err)
	}
	model = &passModel{failing: true}
	written, err = NewCardWriter(model, domain.CardInstructionsV4).Write(context.Background(), chapter)
	if err != nil || len(written.Card.Hooks) != 1 || written.Usage.Calls != 2 {
		t.Fatalf("a failed pass: written %+v, err %v", written, err)
	}
}

// specs/2026-11-01-writer-assistant-index.md, R2.2: a cards Job that ended with every chapter carded tells
// the thread layer; one that yields or fails does not.
func TestCardsDoneHearsOnlyASuccess(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"}}}
	var heard []TextVersion
	done := func(_ context.Context, version TextVersion) { heard = append(heard, version) }

	failing := NewBuildCards(text, &cardsStore{}, &cardsModel{fail: "Глава 1. Старт"}, nil, "test-model", 1, nil, done)
	if status, err := failing.RunForClaim(context.Background(), cardsClaim, cardsSubject, true); err != nil || status != jobs.StatusFailed || len(heard) != 0 {
		t.Fatalf("failed Job = %q, %v, heard %+v", status, err, heard)
	}
	build := NewBuildCards(text, &cardsStore{}, &cardsModel{}, nil, "test-model", 1, nil, done)
	if status, err := build.RunForClaim(context.Background(), cardsClaim, cardsSubject, false); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("Job = %q, %v", status, err)
	}
	if len(heard) != 1 || heard[0] != (TextVersion{ProjectID: cardsProjectID, View: "draft"}) {
		t.Fatalf("heard %+v", heard)
	}
}
