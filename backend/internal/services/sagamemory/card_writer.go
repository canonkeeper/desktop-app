package sagamemory

import (
	"context"
	"encoding/json"
	"errors"

	"canonkeeper/backend/internal/domain/disclosure"
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
	// refusals ends a chapter at the first refusal of its request that repeating does not cure (domain.ErrRefused): the
	// writer of a builder with the policy of the reading (specs/2026-11-15-desk-reading-no-answer.md NA2.1); the site repeats
	// as it did.
	refusals bool
}

func NewCardWriter(model CardModel, instructions domain.CardInstructionSet) *CardWriter {
	if model == nil || instructions.Text == "" || instructions.Version < 1 {
		panic("saga memory: card model and instructions are required")
	}
	return &CardWriter{model: model, instructions: instructions}
}

// WrittenCard is a card with what writing it took: the model calls made and their spend. Unread says why no card came
// after paid answers of the model — an answer cut by its limit or one that does not read
// (specs/2026-11-13-desk-consent.md CN3.6) — or after a refusal of the request the same settings get again
// (specs/2026-11-15-desk-reading-no-answer.md NA2.1); empty when the model was not reached.
type WrittenCard struct {
	Card     domain.Card
	Attempts int
	Usage    quota.ModelUsage
	Unread   string
}

// Why a chapter did not read after paid answers (CN3.6).
const (
	UnreadCutAnswer  = "cut_answer"
	UnreadUnreadable = "unreadable"
)

// Write asks the model for the card of one chapter, up to three times; the result carries what
// the attempts cost also when every attempt failed. A stop of the reading (domain.Stopped) is not
// repeated, and on the pass it keeps no card: the chapter is a unit (specs/2026-11-13-desk-consent.md
// CN3.5).
func (writer *CardWriter) Write(ctx context.Context, chapter *domain.Chapter) (WrittenCard, error) {
	var lastErr error
	var written WrittenCard
	for range cardAttempts {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		written.Attempts++
		output, spent, err := writer.model.Complete(wholeChapter(ctx, chapter), writer.instructions.Text, domain.ChapterText(chapter))
		written.Usage = written.Usage.Add(spent)
		if _, stopped := domain.StopOf(err); stopped || errors.Is(err, disclosure.ErrJournal) {
			// A stop of the reading, or a request the journal of the app did not send (specs/2026-11-14-desk-journal.md
			// JR1.6): not repeated here; the Job repeats the step.
			return written, err
		}
		if err != nil {
			// Only an answer cut short was paid and did not read; a request that failed — refused before the answer or
			// torn — leaves the chapter to the next attempt (CN3.6). A refusal the next attempt would get again ends the
			// chapter at once under the policy of the reading (NA2.1).
			lastErr, written.Unread = err, ""
			if errors.Is(err, domain.ErrAnswerCut) {
				written.Unread = UnreadCutAnswer
			}
			if writer.refusals && errors.Is(err, domain.ErrRefused) {
				written.Unread = domain.UnreadRefused
				return written, err
			}
			continue
		}
		card, err := domain.ParseCard(output, len(chapter.Paragraphs))
		if err == nil {
			written.Card, written.Unread = card, ""
			if err := writer.addMissed(ctx, chapter, &written); err != nil {
				return written, err
			}
			return written, nil
		}
		lastErr, written.Unread = err, UnreadUnreadable
	}
	return written, lastErr
}

// wholeChapter is the context of a call that gives the chapter whole (specs/2026-11-14-desk-journal.md JR1.3): the client of
// the app names it to its gateway; the site reads nothing of it.
func wholeChapter(ctx context.Context, chapter *domain.Chapter) context.Context {
	var named disclosure.Named
	named.Chapter(chapter.ID, disclosure.Whole, 0, 0)
	return disclosure.With(ctx, named)
}

// addMissed runs the pass over the written card: the model sees the chapter and the card and names
// what the card missed. A failed pass leaves the card as it is (specs/2026-11-01-index-foundation.md, R2.3);
// a stop of the reading on it is the error of the chapter, which keeps no card (CN3.5).
func (writer *CardWriter) addMissed(ctx context.Context, chapter *domain.Chapter, written *WrittenCard) error {
	if writer.instructions.Pass == "" {
		return nil
	}
	card, err := json.Marshal(written.Card)
	if err != nil {
		return nil
	}
	output, spent, err := writer.model.Complete(wholeChapter(ctx, chapter), writer.instructions.Pass, domain.ChapterText(chapter)+"\n\nКАРТОЧКА:\n"+string(card))
	written.Usage = written.Usage.Add(spent)
	if _, stopped := domain.StopOf(err); stopped || errors.Is(err, disclosure.ErrJournal) {
		// A pass the journal did not send keeps no card, as a stop does: the chapter is a unit (JR1.6).
		return err
	}
	if err != nil {
		return nil
	}
	if more, err := domain.ParseCardAdditions(output, len(chapter.Paragraphs)); err == nil {
		written.Card.Add(more)
	}
	return nil
}
