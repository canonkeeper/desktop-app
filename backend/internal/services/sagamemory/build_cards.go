package sagamemory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// IncompleteCardsCode marks a cards Job that ended with chapters still lacking cards.
const IncompleteCardsCode = "cards.incomplete"

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
	// OthersWait reports whether a cards Job of another project waits for a worker it could take now
	// (specs/2026-11-09-first-session.md, FS6.1).
	OthersWait(ctx context.Context, claim jobs.WorkerClaim) (bool, error)
}

// CardText is the port the cards Job reads the chapter selection and the chapter texts through.
type CardText interface {
	Selection(ctx context.Context, projectID, view string) (Selection, error)
	Chapters(ctx context.Context, selection Selection) ([]domain.Chapter, error)
}

// ConsentCardsCode ends a cards Job that left chapters without cards, or the name registry as it was, because the
// reading policy of the build does not let the model read the books of the saga now — the author has not allowed it
// (specs/2026-11-07-desktop-app.md DA8.1) or the app has no helper (specs/2026-11-13-desk-gateway.md GW4.5): the Job
// built what costs nothing — the rows of the map from the cards there are — and waits for the helper or the consent.
const ConsentCardsCode = "cards.consent"

// CardBuildPolicy answers whether the cards Job of a saga may call the model (DA8.1): the policy of the reading of the build
// (specs/2026-11-13-desk-consent.md CN3.1). The desktop asks the author, because he pays for the cards — a sample of three
// chapters by its own Job, the rest under the consent of the saga; the site builds without asking, and a builder without a
// policy is the site's.
type CardBuildPolicy interface {
	ReadingAllowed(ctx context.Context, projectID string, sample bool) (bool, error)
	// ChapterMarks are the chapters of a saga marked «не прочиталась» for the version of the instructions (CN3.6): the
	// reading does not take them until their text changes or the author asks to repeat them.
	ChapterMarks
	// MarkUnread marks a chapter whose card did not come after the paid answers of the model, with why.
	MarkUnread(ctx context.Context, projectID string, key CardKey, version int, reason string) error
	// MarksChapters says MarkUnread marks a chapter for the helper of now: only then does the writer end a chapter the
	// provider refused at its first refusal (specs/2026-11-15-desk-reading-no-answer.md NA2.1).
	MarksChapters(ctx context.Context) bool
	// ChaptersAtOnce is how many chapters the reading takes at once — the requests the provider takes at once —, 0 for the
	// builder's own number: a stop finds no more chapters with a paid card and no pass (CN3.5).
	ChaptersAtOnce(ctx context.Context) int
	NamesMarks
	// MarkNamesUnread marks the names of a set of a text version — the digest of their list — whose register did not come
	// together once the model was asked, with why (specs/2026-11-15-desk-reading-rest.md RR1.1, RR1.6).
	MarkNamesUnread(ctx context.Context, projectID, view, digest, reason string) error
}

// NamesMarks say why the names of a set were marked (specs/2026-11-15-desk-reading-rest.md RR1.2): the register of the names
// of the digest did not come together for the helper of now, so the model is not asked for it again without the author —
// "" when they are not marked or the mark does not act, by the one rule of the policy of the reading (RR1.4).
type NamesMarks interface {
	NamesUnread(ctx context.Context, projectID, digest string) (string, error)
}

// CardsDone hears that a cards Job of a text version succeeded: the thread layer counts the new cards
// (specs/2026-11-01-writer-assistant-index.md, R2.2). The Job is already over, so it fails nothing.
type CardsDone func(ctx context.Context, version TextVersion)

// SagaReady hears that a cards Job found a card for every chapter of its text, before the name registry and the map are
// refreshed: the wave of «Сага готова» closes there if the whole saga is ready (specs/2026-11-09-first-session.md,
// FS1.3, FS6.3). It fails nothing.
type SagaReady func(ctx context.Context, projectID string)

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
	ready        SagaReady
	// end hears the end of a Job (specs/2026-11-13-desk-account-ai.md AI3.2); nil on the site.
	end WorkEnded
	// unbuiltLog hears every chapter a Job leaves without a card (specs/2026-11-17-long-chapters.md LC4.1).
	unbuiltLog UnbuiltLog
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

// chaptersAtOnce is how many chapters a run takes at once: the builder's number, or fewer by the policy of the reading.
func (build *BuildCards) chaptersAtOnce(ctx context.Context) int {
	if build.policy != nil {
		if atOnce := build.policy.ChaptersAtOnce(ctx); atOnce > 0 {
			return min(build.parallel, atOnce)
		}
	}
	return build.parallel
}

// WithMap makes the Job prepare the saga map from the cards it leaves (specs/2026-11-04-saga-map.md, UC10).
func (build *BuildCards) WithMap(maps MapRefresher, failure NamesFailureLog) *BuildCards {
	build.maps, build.mapFailure = maps, failure
	return build
}

// WithPolicy makes the Job call the model only for a saga the policy allows (DA8.1); where the policy marks chapters, its
// writer ends a chapter the provider refused at once, and the Job marks it (specs/2026-11-15-desk-reading-no-answer.md NA2.1).
func (build *BuildCards) WithPolicy(policy CardBuildPolicy) *BuildCards {
	build.policy = policy
	return build
}

// writerOfRun is the writer of one run: it ends a chapter the provider refused at once only where the policy marks chapters
// for the helper of now (NA2.1); a builder without the policy — the site — and the helper of the account repeat it as before.
// Under a policy that names the chapters the reading takes at once the parts of a chapter go one by one, so the run gives the
// model no more requests at once than the chapters (specs/2026-11-17-long-chapters.md LC1.3); no part begins once the run
// halted.
func (build *BuildCards) writerOfRun(ctx context.Context, halted <-chan struct{}) *CardWriter {
	writer := *build.writer
	writer.refusals = build.policy != nil && build.policy.MarksChapters(ctx)
	if build.policy != nil && build.policy.ChaptersAtOnce(ctx) > 0 {
		writer.atOnce = 1
	}
	writer.halted = halted
	return &writer
}

// WithReady makes the Job tell when every chapter of its text has a card (FS1.3).
func (build *BuildCards) WithReady(ready SagaReady) *BuildCards {
	build.ready = ready
	return build
}

// carded tells that every chapter of the Job's text has a card. The author is told before the name registry and the
// map, which only sharpen the answers he may already ask for (FS6.3).
func (build *BuildCards) carded(ctx context.Context, projectID string) {
	if build.ready != nil && ctx.Err() == nil {
		build.ready(ctx, projectID)
	}
}

// modelAllowed reports whether the Job may call the model for the project: always without a policy; the sample of the app
// by its own Job (specs/2026-11-13-desk-consent.md CN2.2).
func (build *BuildCards) modelAllowed(ctx context.Context, subject jobs.ChapterCardsSubject) (bool, error) {
	if build.policy == nil {
		return true, nil
	}
	return build.policy.ReadingAllowed(ctx, subject.ProjectID, subject.Sample != nil)
}

// sampleOf keeps of a version the chapters of the sample of the app (CN2.2): those its subject named, by the text they had
// when the author pressed «Попробовать»; a yield of a portion or another attempt adds no chapter.
func sampleOf(selection, missing Selection, sample *jobs.CardsSample) (Selection, Selection) {
	named := make(map[CardKey]bool, len(sample.Chapters))
	for _, chapter := range sample.Chapters {
		named[CardKey{ChapterID: chapter.ChapterID, ContentHash: chapter.ContentHash}] = true
	}
	keep := func(from Selection) Selection {
		kept := from
		kept.Chapters = nil
		for _, chapter := range from.Chapters {
			if named[CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}] {
				kept.Chapters = append(kept.Chapters, chapter)
			}
		}
		return kept
	}
	return keep(selection), keep(missing)
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
// in parallel and with up to three model attempts per chapter. New chapters go to the model in
// portions of time; at the end of a portion a Job that built at least one card yields its turn to
// the cards of another project that wait and returns queued (UC12, FS6.1). Otherwise it finishes
// the Job: succeeded when every chapter has a card, after the name registry is refreshed, else
// ErrRetryCards, or failed with the number of chapters left on the last attempt. It returns the
// status it committed.
func (build *BuildCards) RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, lastAttempt bool) (status jobs.Status, err error) {
	defer func() { build.ended(ctx, claim.JobID, status, err) }()
	if claim.Validate() != nil || claim.ProjectID != subject.ProjectID || subject.Type != jobs.KindChapterCards {
		return "", ErrInvalid
	}
	selection, missing, err := missingCards(ctx, build.text, build.store, subject.ProjectID, string(subject.SourceView))
	if err != nil {
		return "", err
	}
	if subject.Sample != nil {
		selection, missing = sampleOf(selection, missing, subject.Sample)
	}
	if missing, err = build.leaveUnread(ctx, subject.ProjectID, missing); err != nil {
		return "", err
	}
	allowed, err := build.modelAllowed(ctx, subject)
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
	built, failed, marked := 0, 0, 0
	// A stop of the reading — the funds, the key, the model, no connection, a pause (CN3.4) — stops the handing out at once;
	// the chapters that run end, and the Job ends with the stop.
	halted := make(chan struct{})
	var halt sync.Once
	var stopped domain.Stop
	var journaled error
	work := make(chan int)
	writer := build.writerOfRun(ctx, halted)
	var workers sync.WaitGroup
	for range build.chaptersAtOnce(ctx) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				// A chapter handed out in the same instant as the stop is not read: the handing out and the stop may be ready
				// together, and the stop wins (CN3.4).
				select {
				case <-halted:
					continue
				default:
				}
				chapter := &chapters[index]
				chapter.Key = chapterKeys[chapter.ID]
				began := time.Now()
				written, err := writer.Write(runCtx, chapter)
				card, spent := written.Card, written.Usage
				build.recordUsage(ctx, claim.ProjectID, spent)
				build.unbuilt(runCtx, claim, chapter.ID, written, err, time.Since(began))
				if stop, ok := domain.StopOf(err); ok {
					halt.Do(func() {
						stopped = stop
						close(halted)
					})
					continue
				}
				if errors.Is(err, disclosure.ErrJournal) {
					// The journal of the app sent nothing (specs/2026-11-14-desk-journal.md JR1.6): no pause of the reading — the
					// handing out stops and the worker repeats the Job; the chapter keeps no card.
					halt.Do(func() {
						journaled = err
						close(halted)
					})
					continue
				}
				if err != nil && build.markUnread(ctx, runCtx, claim.ProjectID, CardKey{ChapterID: chapter.ID, ContentHash: missing.Chapters[index].ContentHash}, written) {
					mu.Lock()
					marked++
					mu.Unlock()
					continue
				}
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
	portionEnded := build.handOut(ctx, runCtx, halted, claim, string(subject.SourceView), work, len(chapters))
	close(work)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if storeErr != nil {
		return "", storeErr
	}
	if stopped != "" {
		return build.stop(ctx, claim, subject, stopped, len(chapters)-built-marked, total)
	}
	if journaled != nil {
		return "", journaled
	}
	left := len(chapters) - built - marked
	if portionEnded && left > 0 && built > 0 {
		// Every yield builds at least one card, so a Job always ends (R12.3). The map grows with the cards of a long first build.
		build.growMaps(ctx, claim, string(subject.SourceView))
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
	} else if subject.Sample == nil {
		// The sample of the app builds its cards alone: no names, no readiness, nothing for the layer (CN2.2).
		build.carded(ctx, subject.ProjectID)
		// Chapters indexed while the Job worked — the next volume of an import queue — have no cards yet. The Job their
		// text index queued builds the registry of all the names; this one does not ask the model for a half of them
		// (FS6.2).
		_, late, err := missingCards(ctx, build.text, build.store, subject.ProjectID, string(subject.SourceView))
		if err != nil {
			return "", err
		}
		// A chapter that did not read waits for the author; the names of the others are built (CN3.6).
		if late, err = build.leaveUnread(ctx, subject.ProjectID, late); err != nil {
			return "", err
		}
		if len(late.Chapters) == 0 {
			stop, stopped, journaled := build.refreshNames(ctx, claim, selection)
			if stopped {
				return build.stop(ctx, claim, subject, stop, 0, total)
			}
			if journaled != nil {
				return "", journaled
			}
		}
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
	if outcome.Status == jobs.StatusSucceeded && build.done != nil && subject.Sample == nil {
		build.done(ctx, TextVersion{ProjectID: subject.ProjectID, View: string(subject.SourceView)})
	}
	return outcome.Status, nil
}

// waitForConsent is the Job of a saga whose books the model may not read now — the author has not allowed it (DA8.1) or
// the app has no helper (GW4.5): it calls no model, prepares the map from the cards there are and ends succeeded when
// nothing needs the model — every chapter has its card and the registry is of their names — or else failed with
// ConsentCardsCode, the chapters without cards counted.
func (build *BuildCards) waitForConsent(
	ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, selection, missing Selection,
) (jobs.Status, error) {
	waiting := len(missing.Chapters) > 0
	if !waiting && subject.Sample == nil {
		build.carded(ctx, subject.ProjectID)
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
			Message: fmt.Sprintf("Ждёт помощника или согласия автора: глав без карточек — %d из %d", len(missing.Chapters), total),
		}}
	}
	if err := build.store.FinishCards(ctx, claim, outcome); err != nil {
		return "", err
	}
	if outcome.Status == jobs.StatusSucceeded && build.done != nil && subject.Sample == nil {
		build.done(ctx, TextVersion{ProjectID: subject.ProjectID, View: string(subject.SourceView)})
	}
	return outcome.Status, nil
}

// leaveUnread leaves out of the chapters to read those marked «не прочиталась» (CN3.6); a build without the policy of the
// reading — the site — marks none.
func (build *BuildCards) leaveUnread(ctx context.Context, projectID string, missing Selection) (Selection, error) {
	if build.policy == nil {
		return missing, nil
	}
	return withoutUnread(ctx, build.policy, projectID, missing)
}

// ChapterMarks are the chapters of a saga marked «не прочиталась» (CN3.6) by the one rule of the policy of the reading
// (specs/2026-11-15-desk-reading-rest.md RR1.4, RR1.5).
type ChapterMarks interface {
	Unread(ctx context.Context, projectID string, version int) (map[CardKey]bool, error)
}

// withoutUnread leaves out of the chapters to read those the marks keep.
func withoutUnread(ctx context.Context, marks ChapterMarks, projectID string, missing Selection) (Selection, error) {
	if len(missing.Chapters) == 0 {
		return missing, nil
	}
	unread, err := marks.Unread(ctx, projectID, domain.CurrentCardInstructions.Version)
	if err != nil || len(unread) == 0 {
		return missing, err
	}
	kept := missing
	kept.Chapters = nil
	for _, chapter := range missing.Chapters {
		if !unread[CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}] {
			kept.Chapters = append(kept.Chapters, chapter)
		}
	}
	return kept, nil
}

// stop ends a Job a stop of the reading halted (CN3.4): the map is prepared from the cards there are, and the Job ends with
// the code of the stop — no other attempt, no names, no readiness and nothing for the layer.
func (build *BuildCards) stop(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, stop domain.Stop, left, total int) (jobs.Status, error) {
	build.refreshMap(ctx, claim, string(subject.SourceView))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	outcome := CardsOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &jobs.Error{
		Code: stop.Code(), Message: fmt.Sprintf("Чтение книг остановлено: глав без карточек — %d из %d", left, total),
	}}
	if err := build.store.FinishCards(ctx, claim, outcome); err != nil {
		return "", err
	}
	return outcome.Status, nil
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
	err := build.store.FinishCards(ctx, claim, CardsOutcome{Subject: subject, Status: jobs.StatusCanceled})
	build.ended(ctx, claim.JobID, jobs.StatusCanceled, err)
	return err
}

// FailForClaim finishes a cards Job the worker gave up on.
func (build *BuildCards) FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.ChapterCardsSubject, failure jobs.Error) error {
	err := build.store.FinishCards(ctx, claim, CardsOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &failure})
	build.ended(ctx, claim.JobID, jobs.StatusFailed, err)
	return err
}

// WorkEnded hears the end of a Job of the reading (specs/2026-11-13-desk-account-ai.md AI3.2): a build whose reading a site
// counts closes its permit there — succeeded, canceled or failed. The site has none; a Job that asked no model holds no
// permit and sends nothing.
type WorkEnded interface {
	Ended(ctx context.Context, jobID string, outcome jobs.Status)
}

// WithEnd makes every cards Job tell its end.
func (build *BuildCards) WithEnd(ended WorkEnded) *BuildCards {
	build.end = ended
	return build
}

// ended tells the end of the Job the builder committed; a portion given back to the queue and a failure the worker repeats
// are no end.
func (build *BuildCards) ended(ctx context.Context, jobID string, status jobs.Status, err error) {
	if build.end != nil && err == nil && (status == jobs.StatusSucceeded || status == jobs.StatusFailed || status == jobs.StatusCanceled) {
		build.end.Ended(context.WithoutCancel(ctx), jobID, status)
	}
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
