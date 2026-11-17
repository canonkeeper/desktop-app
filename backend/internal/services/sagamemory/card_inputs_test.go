package sagamemory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
)

// inputModel hears the inputs the card writer sends and refuses them.
type inputModel struct {
	mu     sync.Mutex
	inputs []string
}

func (model *inputModel) Complete(_ context.Context, _ string, input string) (string, quota.ModelUsage, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.inputs = append(model.inputs, input)
	return "", quota.ModelUsage{}, errors.New("the model of the test answers nothing")
}

// specs/2026-11-13-desk-account-ai.md AI3.4: the characters of the inputs of the card steps the estimate of the account
// counts are those the card writer sends for the chapters the builder keyed — the key of the chapter in its heading
// included —, so for new chapters the estimate is the count of the site.
func TestTheCharactersOfTheCardInputsAreThoseTheWriterSends(t *testing.T) {
	selection := Selection{ProjectID: "saga-north", View: "published", Fingerprint: "north", Chapters: []SelectedChapter{
		{ID: "chapter-3", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Северный предел", Order: 1, Title: "Глава 3. Ледяной перевал"},
		{ID: "chapter-4", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Северный предел", Order: 2, Title: "Глава 4. Застава"},
	}}
	chapters := []domain.Chapter{
		{ID: "chapter-3", BookOrder: 1, Title: "Глава 3. Ледяной перевал", Paragraphs: []domain.Paragraph{{Text: "Застава стояла у перевала."}}},
		{ID: "chapter-4", BookOrder: 1, Title: "Глава 4. Застава", Paragraphs: []domain.Paragraph{{Text: "Огни горели до утра."}, {Text: "Вера ждала."}}},
	}
	model := &inputModel{}
	keys := chapterKeys(selection)
	var sent int64
	for _, chapter := range chapters {
		keyed := chapter
		keyed.Key = keys[keyed.ID]
		model.inputs = nil
		_, _ = NewCardWriter(model, domain.CardInstructionsV4).Write(context.Background(), &keyed)
		if keyed.Key == "" || len(model.inputs) == 0 {
			t.Fatalf("the builder keyed %q, the writer sent %d inputs", keyed.Key, len(model.inputs))
		}
		sent += int64(utf8.RuneCountInString(model.inputs[0]))
	}
	if counted := CardInputCharacters(selection, chapters); counted != sent {
		t.Fatalf("the estimate counts %d characters; the writer sends %d", counted, sent)
	}
}

// cardStepModel writes a card of every part and keeps the inputs of the card step.
type cardStepModel struct {
	mu     sync.Mutex
	inputs []string
}

func (model *cardStepModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions != domain.CurrentCardInstructions.Text {
		return `{}`, quota.ModelUsage{}, nil
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	model.inputs = append(model.inputs, input)
	return `{"summary": "Застава."}`, quota.ModelUsage{}, nil
}

// specs/2026-11-17-long-chapters.md LC1.6: of a chapter read in parts the characters are those of the inputs of the card
// steps of all its parts — the head and the line of each part in them, the pass not —, what the site counts by them.
func TestTheCharactersOfALongChapterAreThoseOfTheCardsOfItsParts(t *testing.T) {
	selection := Selection{ProjectID: "saga-north", View: "published", Fingerprint: "north", Chapters: []SelectedChapter{
		{ID: "chapter-7", BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Северный предел", Order: 1, Title: "Глава 7. Застава"},
	}}
	chapters := []domain.Chapter{{ID: "chapter-7", BookOrder: 1, Title: "Глава 7. Застава", Paragraphs: longParagraphs(overTheSplit()...)}}
	keyed := chapters[0]
	keyed.Key = chapterKeys(selection)["chapter-7"]
	model := &cardStepModel{}
	if _, err := NewCardWriter(model, domain.CurrentCardInstructions).Write(context.Background(), &keyed); err != nil || len(model.inputs) != 2 {
		t.Fatalf("the writer sent %d inputs of the card, %v", len(model.inputs), err)
	}
	var sent int64
	for _, input := range model.inputs {
		sent += int64(utf8.RuneCountInString(input))
	}
	if counted := CardInputCharacters(selection, chapters); counted != sent {
		t.Fatalf("the estimate counts %d characters; the writer sends %d", counted, sent)
	}
}
