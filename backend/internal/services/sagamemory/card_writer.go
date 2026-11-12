package sagamemory

import (
	"context"
	"encoding/json"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
)

const cardAttempts = 3

// CardWriter writes the card of one chapter text with one instruction set: up to three model
// attempts, each answer parsed and normalized by domain.ParseCard. The cards Job writes with the
// current instructions; the operator's chapter-cards build writes with the same writer
// (specs/2026-10-29-chapter-cards-quality.md, UC2).
type CardWriter struct {
	model        CardModel
	instructions domain.CardInstructionSet
}

func NewCardWriter(model CardModel, instructions domain.CardInstructionSet) *CardWriter {
	if model == nil || instructions.Text == "" || instructions.Version < 1 {
		panic("saga memory: card model and instructions are required")
	}
	return &CardWriter{model: model, instructions: instructions}
}

// WrittenCard is a card with what writing it took: the model calls made and their spend.
type WrittenCard struct {
	Card     domain.Card
	Attempts int
	Usage    quota.ModelUsage
}

// Write asks the model for the card of one chapter, up to three times; the result carries what
// the attempts cost also when every attempt failed.
func (writer *CardWriter) Write(ctx context.Context, chapter *domain.Chapter) (WrittenCard, error) {
	var lastErr error
	var written WrittenCard
	for range cardAttempts {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		written.Attempts++
		output, spent, err := writer.model.Complete(ctx, writer.instructions.Text, domain.ChapterText(chapter))
		written.Usage = written.Usage.Add(spent)
		if err != nil {
			lastErr = err
			continue
		}
		card, err := domain.ParseCard(output, len(chapter.Paragraphs))
		if err == nil {
			written.Card = card
			writer.addMissed(ctx, chapter, &written)
			return written, nil
		}
		lastErr = err
	}
	return written, lastErr
}

// addMissed runs the pass over the written card: the model sees the chapter and the card and names
// what the card missed. A failed pass leaves the card as it is (specs/2026-11-01-index-foundation.md, R2.3).
func (writer *CardWriter) addMissed(ctx context.Context, chapter *domain.Chapter, written *WrittenCard) {
	if writer.instructions.Pass == "" {
		return
	}
	card, err := json.Marshal(written.Card)
	if err != nil {
		return
	}
	output, spent, err := writer.model.Complete(ctx, writer.instructions.Pass, domain.ChapterText(chapter)+"\n\nКАРТОЧКА:\n"+string(card))
	written.Usage = written.Usage.Add(spent)
	if err != nil {
		return
	}
	if more, err := domain.ParseCardAdditions(output, len(chapter.Paragraphs)); err == nil {
		written.Card.Add(more)
	}
}
