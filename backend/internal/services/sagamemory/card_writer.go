package sagamemory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

const cardAttempts = 3

// partsAtOnce is how many parts of a chapter a writer without the policy of the reading — the site, the operator — gives the
// model at once (specs/2026-11-17-long-chapters.md LC1.3); under the policy they go one by one.
const partsAtOnce = 4

// CardThreads is how many threads the cards of a Job go under in each step (specs/2026-11-17-model-cache.md MC1.3): the
// instructions of a step are shared and come from the cache of the provider, and at 96 chapters at once one or two calls
// go under a thread — the account that holds it is not crowded.
const CardThreads = 48

// threadBucket is the bucket of a thread of the cards: the first 8 bytes of the SHA-256 of what names it as a number, modulo
// CardThreads.
func threadBucket(name string) string {
	sum := sha256.Sum256([]byte(name))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])%CardThreads, 10)
}

// CardWriter writes the card of one chapter text with one instruction set: up to three model
// attempts, each answer parsed and normalized by domain.ParseCard. The cards Job writes with the
// current instructions; the operator's chapter-cards build writes with the same writer
// (specs/2026-10-29-chapter-cards-quality.md, UC2). A chapter longer than domain.ChapterSplitRunes is
// written by its parts (LC1.3).
type CardWriter struct {
	model        CardModel
	instructions domain.CardInstructionSet
	// refusals ends a chapter at the first refusal of its request that repeating does not cure (domain.ErrRefused): the
	// writer of a builder with the policy of the reading (specs/2026-11-15-desk-reading-no-answer.md NA2.1); the site repeats
	// as it did.
	refusals bool
	// atOnce is how many parts of a chapter go to the model at once: partsAtOnce, or one under the policy of the reading.
	atOnce int
	// halted closes when the run stops handing out chapters — a stop of the reading or a refusal of the journal (CN3.4,
	// JR1.6): no new part of a chapter begins. Nil outside a run.
	halted <-chan struct{}
}

func NewCardWriter(model CardModel, instructions domain.CardInstructionSet) *CardWriter {
	if model == nil || instructions.Text == "" || instructions.Version < 1 {
		panic("saga memory: card model and instructions are required")
	}
	return &CardWriter{model: model, instructions: instructions, atOnce: partsAtOnce}
}

// WrittenCard is a card with what writing it took: the model calls made and their spend. Unread says why no card came
// after paid answers of the model — an answer cut by its limit or one that does not read
// (specs/2026-11-13-desk-consent.md CN3.6) — or after a refusal of the request the same settings get again
// (specs/2026-11-15-desk-reading-no-answer.md NA2.1); empty when the model was not reached. Parts is how many parts the
// chapter reads by (LC1.1).
type WrittenCard struct {
	Card     domain.Card
	Attempts int
	Usage    quota.ModelUsage
	Unread   string
	Parts    int
}

// Why a chapter did not read after paid answers (CN3.6).
const (
	UnreadCutAnswer  = "cut_answer"
	UnreadUnreadable = "unreadable"
)

// errPartsHalted is a chapter whose parts the run stopped beginning (LC1.3): it has no card, and it is not marked.
var errPartsHalted = errors.New("saga memory: the run stopped before every part of the chapter was read")

// cardPiece is what one card of a chapter reads (LC1.2, LC1.5): the chapter whole — the input of before, its paragraphs, the
// whole chapter named and the bucket of its key — or a part of it — the input of the part, its paragraphs named and the
// bucket of the key and the number of the part.
type cardPiece struct {
	input       string
	first, last int
	named       disclosure.Named
	bucket      string
}

// cardPieces are what the cards of a chapter read: the chapter whole when it is one part — the input of before, its
// paragraphs, the whole chapter named and the bucket of its key (specs/2026-11-14-desk-journal.md JR1.3, MC1.3) —, else each
// of its parts (domain.ChapterParts) — the input of the part, its paragraphs named and the bucket of the key and the number
// of the part. The writer gives the model their inputs, and the count of the characters of the card steps counts them.
func cardPieces(chapter *domain.Chapter) []cardPiece {
	parts := domain.ChapterParts(chapter)
	if len(parts) == 1 {
		piece := cardPiece{input: domain.ChapterText(chapter), first: 1, last: len(chapter.Paragraphs), bucket: threadBucket(chapter.Key)}
		piece.named.Chapter(chapter.ID, disclosure.Whole, 0, 0)
		return []cardPiece{piece}
	}
	pieces := make([]cardPiece, len(parts))
	for index, part := range parts {
		k := index + 1
		pieces[index] = cardPiece{
			input: domain.PartText(chapter, part, k, len(parts)), first: part.First, last: part.Last,
			bucket: threadBucket(chapter.Key + "\x00" + strconv.Itoa(k)),
		}
		pieces[index].named.Chapter(chapter.ID, disclosure.Paragraphs, part.First, part.Last)
	}
	return pieces
}

// CardInputCharacters are the characters of the inputs of the card steps of chapters of the selection
// (specs/2026-11-13-desk-account-ai.md AI3.4): each chapter keyed as the builder keys it, the input of each of its parts by
// the function the card writer gives the model — the head and the line of a part in it, not the pass
// (specs/2026-11-17-long-chapters.md LC1.6) — the characters of the reading the site of the account counts.
func CardInputCharacters(selection Selection, chapters []domain.Chapter) int64 {
	keys := chapterKeys(selection)
	var characters int64
	for index := range chapters {
		chapter := chapters[index]
		chapter.Key = keys[chapter.ID]
		for _, piece := range cardPieces(&chapter) {
			characters += int64(utf8.RuneCountInString(piece.input))
		}
	}
	return characters
}

// context is the context of a call of a step of the card of the piece: what it gives named for the client of the app, which
// tells its gateway (the site reads nothing of it), and the thread of the step in the bucket of the piece within the Job.
// Outside a Job there is no thread.
func (piece cardPiece) context(ctx context.Context, step coresteps.Name) context.Context {
	return modelthread.With(disclosure.With(ctx, piece.named), "cards", jobs.JobIDOf(ctx), string(step), piece.bucket)
}

// Write asks the model for the card of one chapter, up to three times; the result carries what
// the attempts cost also when every attempt failed. A stop of the reading (domain.Stopped) is not
// repeated, and on the pass it keeps no card: the chapter is a unit (specs/2026-11-13-desk-consent.md
// CN3.5). A chapter longer than domain.ChapterSplitRunes is written by its parts (LC1.3).
func (writer *CardWriter) Write(ctx context.Context, chapter *domain.Chapter) (WrittenCard, error) {
	pieces := cardPieces(chapter)
	if len(pieces) == 1 {
		written, err := writer.writePiece(ctx, pieces[0])
		written.Parts = 1
		return written, err
	}
	return writer.writeParts(ctx, pieces)
}

// writePiece asks the model for the card of a piece, up to three times, and runs the pass over it.
func (writer *CardWriter) writePiece(ctx context.Context, piece cardPiece) (WrittenCard, error) {
	var lastErr error
	var written WrittenCard
	for range cardAttempts {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		written.Attempts++
		output, spent, err := writer.model.Complete(piece.context(ctx, coresteps.Card), writer.instructions.Text, piece.input)
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
			if errors.Is(err, domain.ErrAttemptTimeout) {
				// The client of the site already repeated an attempt its time cut: a provider that hangs holds the piece no
				// longer than two attempts (specs/2026-11-17-long-chapters.md LC1.3).
				return written, err
			}
			continue
		}
		card, err := domain.ParseCard(output, piece.first, piece.last)
		if err == nil {
			written.Card, written.Unread = card, ""
			if err := writer.addMissed(ctx, piece, &written); err != nil {
				return written, err
			}
			return written, nil
		}
		lastErr, written.Unread = err, UnreadUnreadable
	}
	return written, lastErr
}

// addMissed runs the pass over the written card: the model sees the chapter and the card and names
// what the card missed. A failed pass leaves the card as it is (specs/2026-11-01-index-foundation.md, R2.3);
// a stop of the reading on it is the error of the chapter, which keeps no card (CN3.5).
func (writer *CardWriter) addMissed(ctx context.Context, piece cardPiece, written *WrittenCard) error {
	if writer.instructions.Pass == "" {
		return nil
	}
	card, err := json.Marshal(written.Card)
	if err != nil {
		return nil
	}
	output, spent, err := writer.model.Complete(piece.context(ctx, coresteps.CardPass), writer.instructions.Pass, piece.input+"\n\nКАРТОЧКА:\n"+string(card))
	written.Usage = written.Usage.Add(spent)
	if _, stopped := domain.StopOf(err); stopped || errors.Is(err, disclosure.ErrJournal) {
		// A pass the journal did not send keeps no card, as a stop does: the chapter is a unit (JR1.6).
		return err
	}
	if err != nil {
		return nil
	}
	if more, err := domain.ParseCardAdditions(output, piece.first, piece.last); err == nil {
		written.Card.Add(more)
	}
	return nil
}

// writeParts writes the card of a chapter by its parts (LC1.3): each part as a chapter is written — up to three attempts of
// its card and its pass —, at most atOnce at a time, and the card of the chapter merged from theirs (LC1.4). A part left
// without a card leaves the chapter without one, with why the part did not read; once a part did not read, or the run halted,
// no new part begins, and the parts that go are waited for. The attempts and the spend are those of all the parts.
func (writer *CardWriter) writeParts(ctx context.Context, parts []cardPiece) (WrittenCard, error) {
	written := WrittenCard{Parts: len(parts)}
	cards := make([]domain.Card, len(parts))
	var mu sync.Mutex
	var failed, stopped error
	read := 0
	over := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if failed != nil || stopped != nil || ctx.Err() != nil {
			return true
		}
		select {
		case <-writer.halted:
			return true
		default:
			return false
		}
	}
	places := make(chan struct{}, max(writer.atOnce, 1))
	var going sync.WaitGroup
	for index, part := range parts {
		places <- struct{}{}
		if over() {
			<-places
			break
		}
		going.Add(1)
		go func() {
			defer going.Done()
			defer func() { <-places }()
			result, err := writer.writePiece(ctx, part)
			mu.Lock()
			defer mu.Unlock()
			written.Attempts += result.Attempts
			written.Usage = written.Usage.Add(result.Usage)
			_, stop := domain.StopOf(err)
			switch {
			case err == nil:
				cards[index] = result.Card
				read++
			case stop || errors.Is(err, disclosure.ErrJournal) || ctx.Err() != nil:
				if stopped == nil {
					stopped = err
				}
			case failed == nil:
				failed, written.Unread = err, result.Unread
			}
		}()
	}
	going.Wait()
	switch {
	case ctx.Err() != nil:
		written.Unread = ""
		return written, ctx.Err()
	case stopped != nil:
		written.Unread = ""
		return written, stopped
	case failed != nil:
		return written, failed
	case read < len(parts):
		return written, errPartsHalted
	}
	written.Card = domain.MergeCards(cards)
	return written, nil
}
