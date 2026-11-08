package sagamemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// IncompleteCardsCode marks a cards Job that ended with chapters still lacking cards.
const IncompleteCardsCode = "cards.incomplete"

const cardAttempts = 3

// ErrRetryCards asks the worker to run the cards Job again later: some chapters failed and
// the next attempt builds only those.
var ErrRetryCards = errors.New("saga memory: chapters without cards remain")

// ErrCardsPortion is why a cards Job went back to the queue: its portion was built and other
// projects' Jobs take their turn (specs/2026-10-28-writer-quotas-and-fair-queue.md UC12). The
// worker log names it cards.portion.
var ErrCardsPortion error = cardsPortion{}

type cardsPortion struct{}

func (cardsPortion) Error() string {
	return "saga memory: cards portion built, the Job waits for its next turn"
}

func (cardsPortion) SafeErrorCode() string { return "cards.portion" }

// cardsPortion is how long a claimed cards Job hands new chapters to the model; chapters already
// started are finished (R12.1).
const cardsPortionTime = 120 * time.Second

// CardModel is the direct model client that writes a card from one chapter text; it reports
// what each call cost.
type CardModel interface {
	Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error)
}

// UsageRecorder adds model spend to the day's aggregates (quota spec UC7). It never fails the
// work it measures: a write that fails is logged by the recorder.
type UsageRecorder interface {
	RecordModelUsage(ctx context.Context, record quota.UsageRecord)
}

// CardKey names the chapter text a card was built from.
type CardKey struct {
	ChapterID   string
	ContentHash string
}

// StoredCard is one card with the chapter text it describes and the model that wrote it.
type StoredCard struct {
	CardKey
	Card  domain.Card
	Model string
}

// CardsOutcome is the terminal state of a cards Job.
type CardsOutcome struct {
	Subject jobs.ChapterCardsSubject
	Status  jobs.Status
	Result  *jobs.ChapterCardsResult
	Error   *jobs.Error
}

// CardStore is the port of the cards Job: existing cards, card writes under the claim, the name
// registry and the terminal state, which also deletes cards no chapter text matches any more.
type CardStore interface {
	ExistingCards(ctx context.Context, projectID string, keys []CardKey) (map[CardKey]bool, error)
	SaveCard(ctx context.Context, claim jobs.WorkerClaim, card StoredCard, progress float64) error
	Cards(ctx context.Context, projectID string, keys []CardKey, parts domain.CardParts) (map[CardKey]domain.Card, error)
	// NamesDigest returns the digest of the project's stored registry, empty without one.
	NamesDigest(ctx context.Context, projectID string) (string, error)
	// SaveNames writes the registry while the Job runs under the claim and every chapter text of the
	// list is current; otherwise nothing is written.
	SaveNames(ctx context.Context, claim jobs.WorkerClaim, names StoredNames) error
	FinishCards(ctx context.Context, claim jobs.WorkerClaim, outcome CardsOutcome) error
	// YieldCards puts the claimed Job back to the queue right away under the next claim
	// incarnation, keeping its attempts and progress (R12.2).
	YieldCards(ctx context.Context, claim jobs.WorkerClaim) error
}

// CardText is the port the cards Job reads the chapter selection and the chapter texts through.
type CardText interface {
	Selection(ctx context.Context, projectID, view string) (Selection, error)
	Chapters(ctx context.Context, selection Selection) ([]domain.Chapter, error)
}

// ConsentCardsCode ends a cards Job that left chapters without cards, or the name registry as it was, because the author
// has not allowed the model to build the cards of his saga (specs/2026-11-07-desktop-app.md DA8.1): the Job built what
// costs nothing — the rows of the map from the cards there are — and waits for his consent.
const ConsentCardsCode = "cards.consent"

// CardBuildPolicy answers whether the cards Job of a saga may call the model (DA8.1). The desktop asks the author,
// because he pays for the cards; the site builds without asking, and a builder without a policy is the site's.
type CardBuildPolicy interface {
	CardModelAllowed(ctx context.Context, projectID string) (bool, error)
}

// CardsDone hears that a cards Job of a text version succeeded: the thread layer counts the new cards
// (specs/2026-11-01-writer-assistant-index.md, R2.2). The Job is already over, so it fails nothing.
type CardsDone func(ctx context.Context, version TextVersion)

// BuildCards runs the cards Job of one project text version (UC2 steps 2–3).
type BuildCards struct {
	text         CardText
	store        CardStore
	model        CardModel
	writer       *CardWriter
	usage        UsageRecorder
	modelName    string
	parallel     int
	portion      time.Duration
	namesFailure NamesFailureLog
	done         CardsDone
	maps         MapRefresher
	mapFailure   NamesFailureLog
	policy       CardBuildPolicy
}

// NewBuildCards builds cards with the model; usage, namesFailure and done may be nil where nothing
// records spend, reads a log or waits for the cards.
func NewBuildCards(text CardText, store CardStore, model CardModel, usage UsageRecorder, modelName string, parallel int, namesFailure NamesFailureLog, done CardsDone) *BuildCards {
	if text == nil || store == nil || model == nil || modelName == "" {
		panic("saga memory: text source, card store, model and model name are required")
	}
	return &BuildCards{
		text: text, store: store, model: model, writer: NewCardWriter(model, domain.CurrentCardInstructions), usage: usage,
		modelName: modelName, parallel: max(1, parallel), portion: cardsPortionTime, namesFailure: namesFailure, done: done,
	}
}

// WithMap makes the Job prepare the saga map from the cards it leaves (specs/2026-11-04-saga-map.md, UC10).
func (build *BuildCards) WithMap(maps MapRefresher, failure NamesFailureLog) *BuildCards {
	build.maps, build.mapFailure = maps, failure
	return build
}

// WithPolicy makes the Job call the model only for a saga the policy allows (DA8.1).
func (build *BuildCards) WithPolicy(policy CardBuildPolicy) *BuildCards {
	build.policy = policy
	return build
}

// modelAllowed reports whether the Job may call the model for the project: always without a policy.
func (build *BuildCards) modelAllowed(ctx context.Context, projectID string) (bool, error) {
	if build.policy == nil {
		return true, nil
	}
	return build.policy.CardModelAllowed(ctx, projectID)
}

// refreshMap prepares the map from the cards there are. Whatever fails is logged and leaves the cards and the Job alone: the map
// keeps its last build, and the next Job tries again. A stopped Job reports nothing.
func (build *BuildCards) refreshMap(ctx context.Context, claim jobs.WorkerClaim, view string) {
	if build.maps == nil {
		return
	}
	stage, err := build.maps.Refresh(ctx, claim, view)
	if err != nil && ctx.Err() == nil {
		build.mapFailure(claim.ProjectID, stage, err)
	}
}

// RunForClaim builds the cards of the version's chapters that have none for their current text,
// in parallel and with up to three model attempts per chapter. New chapters go to the model for
// one portion of time only; then a Job that built at least one card yields its turn and returns
// queued (UC12). Otherwise it finishes the Job: succeeded when every chapter has a card, after the
// name registry is refreshed, else ErrRetryCards, or failed with the number of chapters left on the
// last attempt. It returns the status it committed.
func (build *BuildCards) RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, lastAttempt bool) (jobs.Status, error) {
	if claim.Validate() != nil || claim.ProjectID != subject.ProjectID || subject.Type != jobs.KindChapterCards {
		return "", ErrInvalid
	}
	selection, missing, err := missingCards(ctx, build.text, build.store, subject.ProjectID, string(subject.SourceView))
	if err != nil {
		return "", err
	}
	allowed, err := build.modelAllowed(ctx, subject.ProjectID)
	if err != nil {
		return "", err
	}
	if !allowed {
		return build.waitForConsent(ctx, claim, subject, selection, missing)
	}
	chapters, err := build.text.Chapters(ctx, missing)
	if err != nil {
		return "", err
	}
	chapterKeys := chapterKeys(selection)
	total, done := len(selection.Chapters), len(selection.Chapters)-len(missing.Chapters)

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var mu sync.Mutex
	var storeErr error
	built, failed := 0, 0
	work := make(chan int)
	var workers sync.WaitGroup
	for range build.parallel {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				chapter := &chapters[index]
				chapter.Key = chapterKeys[chapter.ID]
				written, err := build.writer.Write(runCtx, chapter)
				card, spent := written.Card, written.Usage
				build.recordUsage(ctx, claim.ProjectID, spent)
				if err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				mu.Lock()
				done++
				progress := float64(done) / float64(max(total, 1))
				mu.Unlock()
				stored := StoredCard{
					CardKey: CardKey{ChapterID: chapter.ID, ContentHash: missing.Chapters[index].ContentHash},
					Card:    card, Model: build.modelName,
				}
				if err := build.store.SaveCard(runCtx, claim, stored, progress); err != nil {
					mu.Lock()
					if storeErr == nil {
						storeErr = err
					}
					mu.Unlock()
					stop()
					continue
				}
				mu.Lock()
				built++
				mu.Unlock()
			}
		}()
	}
	portion := time.NewTimer(build.portion)
	defer portion.Stop()
	portionEnded := false
dispatch:
	for index := range chapters {
		if runCtx.Err() != nil {
			break
		}
		select {
		case work <- index:
		case <-runCtx.Done():
		case <-portion.C:
			portionEnded = true
			break dispatch
		}
	}
	close(work)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if storeErr != nil {
		return "", storeErr
	}
	left := len(chapters) - built
	if portionEnded && left > 0 && built > 0 {
		// Every yield builds at least one card, so a Job always ends (R12.3). The map grows with the cards of a long first build.
		build.refreshMap(ctx, claim, string(subject.SourceView))
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := build.store.YieldCards(ctx, claim); err != nil {
			return "", err
		}
		return jobs.StatusQueued, nil
	}
	result := &jobs.ChapterCardsResult{
		Type: jobs.KindChapterCards, SourceView: subject.SourceView, Chapters: total, Built: built,
	}
	outcome := CardsOutcome{Subject: subject, Status: jobs.StatusSucceeded, Result: result}
	if left > 0 {
		if !lastAttempt {
			return "", ErrRetryCards
		}
		outcome = CardsOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &jobs.Error{
			Code: IncompleteCardsCode, Message: fmt.Sprintf("Глав без карточек: %d из %d", left, total),
		}}
	} else {
		build.refreshNames(ctx, claim, selection)
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	build.refreshMap(ctx, claim, string(subject.SourceView))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := build.store.FinishCards(ctx, claim, outcome); err != nil {
		return "", err
	}
	if outcome.Status == jobs.StatusSucceeded && build.done != nil {
		build.done(ctx, TextVersion{ProjectID: subject.ProjectID, View: string(subject.SourceView)})
	}
	return outcome.Status, nil
}

// waitForConsent is the Job of a saga whose author has not allowed the model (DA8.1): it calls no model, prepares the
// map from the cards there are and ends succeeded when nothing needs the model — every chapter has its card and the
// registry is of their names — or else failed with ConsentCardsCode, the chapters without cards counted.
func (build *BuildCards) waitForConsent(
	ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, selection, missing Selection,
) (jobs.Status, error) {
	waiting := len(missing.Chapters) > 0
	if !waiting {
		needs, err := build.namesNeedModel(ctx, subject.ProjectID, selection)
		if err != nil {
			return "", err
		}
		waiting = needs
	}
	build.refreshMap(ctx, claim, string(subject.SourceView))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	total := len(selection.Chapters)
	outcome := CardsOutcome{Subject: subject, Status: jobs.StatusSucceeded, Result: &jobs.ChapterCardsResult{
		Type: jobs.KindChapterCards, SourceView: subject.SourceView, Chapters: total,
	}}
	if waiting {
		outcome = CardsOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &jobs.Error{
			Code:    ConsentCardsCode,
			Message: fmt.Sprintf("Ждёт согласия автора: глав без карточек — %d из %d", len(missing.Chapters), total),
		}}
	}
	if err := build.store.FinishCards(ctx, claim, outcome); err != nil {
		return "", err
	}
	if outcome.Status == jobs.StatusSucceeded && build.done != nil {
		build.done(ctx, TextVersion{ProjectID: subject.ProjectID, View: string(subject.SourceView)})
	}
	return outcome.Status, nil
}

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

// recordUsage counts the spend of one chapter for the project owner (R7.2), also when the card
// failed or the portion stopped it.
func (build *BuildCards) recordUsage(ctx context.Context, projectID string, usage quota.ModelUsage) {
	if build.usage == nil || usage.Empty() {
		return
	}
	build.usage.RecordModelUsage(context.WithoutCancel(ctx), quota.UsageRecord{
		Source: quota.UsageCards, ProjectID: projectID, Model: build.modelName, Usage: usage,
	})
}

// CancelForClaim finishes a canceled cards Job; the cards built so far stay.
func (build *BuildCards) CancelForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject) error {
	return build.store.FinishCards(ctx, claim, CardsOutcome{Subject: subject, Status: jobs.StatusCanceled})
}

// FailForClaim finishes a cards Job the worker gave up on.
func (build *BuildCards) FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, failure jobs.Error) error {
	return build.store.FinishCards(ctx, claim, CardsOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &failure})
}

// chapterKeys assigns the agent-facing chapter keys of the version without loading text.
func chapterKeys(selection Selection) map[string]string {
	version := outlineVersion(selection)
	keys := make(map[string]string, len(version.Chapters))
	for _, chapter := range version.Chapters {
		keys[chapter.ID] = chapter.Key
	}
	return keys
}
