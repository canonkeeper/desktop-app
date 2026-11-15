// Package sagalayer builds the thread layer of a saga in its own Job
// (specs/2026-11-01-writer-assistant-index.md, UC2): a call per book over the digests of its cards, one
// call to reduce the open threads of all books into a registry, a check of each record against the text
// of its chapters and later cards, a call for what was missed and a check of what it added.
package sagalayer

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// ErrInvalid reports a Job the builder cannot be given.
var ErrInvalid = errors.New("saga layer: invalid request")

// callAttempts is how many times one call is asked: an answer that fails or does not parse is asked
// again, as measured.
const callAttempts = 3

// Model is the direct model client of the build: the cards model behind the model_api gate, whose
// waiting chat calls go first (R2.1).
type Model interface {
	Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error)
}

// UsageRecorder adds the spend of the build to the day's aggregates; it never fails the build.
type UsageRecorder interface {
	RecordModelUsage(ctx context.Context, record quota.UsageRecord)
}

// SagaText reads the text version a build goes through.
type SagaText interface {
	Outline(ctx context.Context, projectID, view string) (*sagamemory.Outline, error)
	Chapters(ctx context.Context, outline *sagamemory.Outline, chapters []int) error
	Cards(ctx context.Context, outline *sagamemory.Outline, from, to int, parts sagamemory.CardParts) (*sagamemory.CardSet, error)
}

// Checkpoint is one saved step of a build; Books nil leaves the books of the checkpoint as they are.
type Checkpoint struct {
	Books []domain.Book
	Steps domain.Steps
	Model string
}

// LayerOutcome is the terminal state of a layer Job; Layer is set when it brings a new build.
type LayerOutcome struct {
	Subject jobs.SagaLayerSubject
	Status  jobs.Status
	Result  *jobs.SagaLayerResult
	Error   *jobs.Error
	Digest  string
	Layer   *domain.Layer
	Model   string
}

// Store is the port of the layer Job. Stamps lists the chapters of a version in reading order. Writes go
// under the claim and only while the chapter texts named by the stamps are current: a source deleted
// meanwhile leaves nothing of it behind (R2.8).
type Store interface {
	Stamps(ctx context.Context, projectID, view string) ([]domain.Stamp, error)
	StoredLayer(ctx context.Context, projectID string) (*domain.Stored, error)
	SaveCheckpoint(ctx context.Context, claim jobs.WorkerClaim, chapters []domain.Stamp, checkpoint Checkpoint, progress float64) error
	FinishLayer(ctx context.Context, claim jobs.WorkerClaim, outcome LayerOutcome) error
}

// FailureLog hears a step that went on without an answer: the stage and the error, for the log only.
type FailureLog func(projectID, stage string, err error)

// ConsentLayerCode ends a layer Job of a saga whose books the model may not read now (specs/2026-11-13-desk-gateway.md
// GW4.3): the app without its helper or without the author's consent. No model is called; the layer built before stays,
// and the cards Job of the consent queues the layer again.
const ConsentLayerCode = "layer.consent"

// UnreadLayerCode ends a layer Job whose question of a book, of the reduce or of what was missed gave no answer for a named
// reason — the answer was cut short, did not read through its three answers, was refused, nobody answered it
// (specs/2026-11-15-desk-reading-rest.md RR1.3): the build is marked and the Job is not repeated; the checkpoint stays.
const UnreadLayerCode = "layer.unread"

// ReadingPolicy answers whether the model may read the books of a saga (DA8.1): the app asks the author, because he pays
// for the reading; a builder without a policy — the site's — reads. It keeps the builds that did not come together (RR1.3).
type ReadingPolicy interface {
	ReadingAllowed(ctx context.Context, projectID string, sample bool) (bool, error)
	ThreadsMarks
	// MarkThreadsUnread marks the build of a text version over the stamps of its chapters — those its checkpoint keeps — with
	// why it did not come together (RR1.1, RR1.6).
	MarkThreadsUnread(ctx context.Context, projectID, view string, stamps []domain.Stamp, reason string) error
}

// ThreadsMarks say why the build of the threads over the chapters now is marked (RR1.3): a build of them did not come
// together for the helper of now, and the cards now have not passed the threshold of a rebuild from its stamps — "" when it
// is not marked or the mark does not act, by the one rule of the policy of the reading (RR1.4).
type ThreadsMarks interface {
	ThreadsUnread(ctx context.Context, projectID string, stamps []domain.Stamp) (string, error)
}

// BuildLayer runs the layer Job of one project.
type BuildLayer struct {
	text      SagaText
	store     Store
	model     Model
	usage     UsageRecorder
	modelName string
	parallel  int
	failure   FailureLog
	policy    ReadingPolicy
	// end hears the end of a Job (specs/2026-11-13-desk-account-ai.md AI3.2); nil on the site.
	end WorkEnded
}

// WithPolicy makes the Job call the model only for a saga the policy allows; the build gives it once its modules are
// composed.
func (build *BuildLayer) WithPolicy(policy ReadingPolicy) *BuildLayer {
	build.policy = policy
	return build
}

// NewBuildLayer builds layers with the model; usage and failure may be nil where nothing records spend
// or reads a log.
func NewBuildLayer(text SagaText, store Store, model Model, usage UsageRecorder, modelName string, parallel int, failure FailureLog) *BuildLayer {
	if text == nil || store == nil || model == nil || modelName == "" {
		panic("saga layer: text, store, model and model name are required")
	}
	return &BuildLayer{text: text, store: store, model: model, usage: usage, modelName: modelName, parallel: max(1, parallel), failure: failure}
}

// RunForClaim brings the layer of the project up to the current cards, or leaves it when the chapters
// are those of the finished build or a Job has waited for less than the threshold (R2.2). A failure
// returns the error for the worker to retry: the checkpoint stays and the next attempt goes on from it.
// It returns the status it committed.
func (build *BuildLayer) RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject) (status jobs.Status, err error) {
	defer func() { build.ended(ctx, claim.JobID, status, err) }()
	if claim.Validate() != nil || claim.ProjectID != subject.ProjectID || subject.Type != jobs.KindSagaLayer {
		return "", ErrInvalid
	}
	stamps, err := build.store.Stamps(ctx, subject.ProjectID, string(subject.SourceView))
	if err != nil {
		return "", err
	}
	stored, err := build.store.StoredLayer(ctx, subject.ProjectID)
	if err != nil {
		return "", err
	}
	if stored == nil {
		stored = &domain.Stored{}
	}
	fresh := LayerOutcome{Subject: subject, Status: jobs.StatusSucceeded, Result: &jobs.SagaLayerResult{
		Type: jobs.KindSagaLayer, SourceView: subject.SourceView, Chapters: len(stored.Layer.Chapters), Threads: len(stored.Layer.Threads),
	}}
	if !due(stamps, stored) {
		return build.finish(ctx, claim, fresh)
	}
	if build.policy != nil {
		// A build of these chapters that did not come together for this helper is not paid again without the author: a Job
		// queued another way ends as at a fresh layer (specs/2026-11-15-desk-reading-rest.md RR1.3).
		unread, err := build.policy.ThreadsUnread(ctx, subject.ProjectID, stamps)
		if err != nil {
			return "", err
		}
		if unread != "" {
			return build.finish(ctx, claim, fresh)
		}
		allowed, err := build.policy.ReadingAllowed(ctx, subject.ProjectID, false)
		if err != nil {
			return "", err
		}
		if !allowed {
			return build.finish(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &jobs.Error{
				Code: ConsentLayerCode, Message: "Ждёт помощника или согласия автора на чтение книг",
			}})
		}
	}
	run, err := build.newRun(ctx, claim, subject, stamps, stored)
	if err != nil {
		return "", err
	}
	layer, result, err := run.runSteps()
	if stop, stopped := sagamemory.StopOf(err); stopped && ctx.Err() == nil {
		// A stop of the reading ends the Job with it, without another attempt (specs/2026-11-13-desk-consent.md CN3.4): the
		// checkpoint stays for the next Job.
		return build.finish(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &jobs.Error{
			Code: stop.Code(), Message: "Чтение книг остановлено",
		}})
	}
	var missed *unanswered
	if errors.As(err, &missed) && ctx.Err() == nil {
		// A question that gave no answer for a named reason marks the build and ends the Job without another attempt (RR1.3):
		// the checkpoint stays. A mark that was not written is logged and the Job ends all the same (RR1.6).
		if err := build.policy.MarkThreadsUnread(ctx, subject.ProjectID, string(subject.SourceView), run.stamps, missed.reason); err != nil && build.failure != nil {
			build.failure(subject.ProjectID, "mark", err)
		}
		return build.finish(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &jobs.Error{
			Code: UnreadLayerCode, Message: "Сюжетные нити не сведены",
		}})
	}
	if err != nil {
		return "", err
	}
	return build.finish(ctx, claim, LayerOutcome{
		Subject: subject, Status: jobs.StatusSucceeded, Result: result, Digest: run.digest, Layer: layer, Model: build.modelName,
	})
}

func (build *BuildLayer) finish(ctx context.Context, claim jobs.WorkerClaim, outcome LayerOutcome) (jobs.Status, error) {
	if err := build.store.FinishLayer(ctx, claim, outcome); err != nil {
		return "", err
	}
	return outcome.Status, nil
}

// CancelForClaim finishes a canceled layer Job; the finished build and the checkpoint stay.
func (build *BuildLayer) CancelForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject) error {
	err := build.store.FinishLayer(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusCanceled})
	build.ended(ctx, claim.JobID, jobs.StatusCanceled, err)
	return err
}

// FailForClaim finishes a layer Job the worker gave up on; the checkpoint stays for the next Job.
func (build *BuildLayer) FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject, failure jobs.Error) error {
	err := build.store.FinishLayer(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &failure})
	build.ended(ctx, claim.JobID, jobs.StatusFailed, err)
	return err
}

// WorkEnded hears the end of a Job of the reading (specs/2026-11-13-desk-account-ai.md AI3.2): a build whose reading a site
// counts closes its permit there. The site has none.
type WorkEnded interface {
	Ended(ctx context.Context, jobID string, outcome jobs.Status)
}

// WithEnd makes every layer Job tell its end.
func (build *BuildLayer) WithEnd(ended WorkEnded) *BuildLayer {
	build.end = ended
	return build
}

// ended tells the end of the Job the builder committed; a failure the worker repeats is no end.
func (build *BuildLayer) ended(ctx context.Context, jobID string, status jobs.Status, err error) {
	if build.end != nil && err == nil && (status == jobs.StatusSucceeded || status == jobs.StatusFailed || status == jobs.StatusCanceled) {
		build.end.Ended(context.WithoutCancel(ctx), jobID, status)
	}
}

func (build *BuildLayer) recordUsage(ctx context.Context, projectID string, usage quota.ModelUsage) {
	if build.usage == nil || usage.Empty() {
		return
	}
	build.usage.RecordModelUsage(context.WithoutCancel(ctx), quota.UsageRecord{
		Source: quota.UsageLayer, ProjectID: projectID, Model: build.modelName, Usage: usage,
	})
}

// due says the layer of the chapters with cards is to be built: they are not those of the finished build, and enough of
// them are new — or a build was begun (R2.2).
func due(stamps []domain.Stamp, stored *domain.Stored) bool {
	carded := withCards(stamps)
	current := stored.Digest != "" && stored.Digest == domain.Digest(carded)
	waits := stored.Digest != "" && stored.Steps == nil && !domain.Due(stamps, stored.Layer.Chapters)
	return len(carded) > 0 && !current && !waits
}

// Pending says what the layer of the text version waits for (specs/2026-11-13-desk-consent.md CN1.6;
// specs/2026-11-15-desk-reading-rest.md RR1.3, RR2.1) by the rule of its queue (QueueDue): the model — a Job of it would call
// the model now —, or nothing because its build is marked, with why. A chapter without a card holds the layer back, as it
// holds back its queue.
func Pending(ctx context.Context, store LayerChapters, marks ThreadsMarks, projectID, view string) (wait bool, unread string, err error) {
	due, unread, _, err := toBuild(ctx, store, marks, projectID, view)
	return due, unread, err
}

// LayerChapters is what the rule of a layer to build reads: the stamps of a version and those of the finished build.
type LayerChapters interface {
	Stamps(ctx context.Context, projectID, view string) ([]domain.Stamp, error)
	// BuiltChapters returns what the finished build was built from, nil without one.
	BuiltChapters(ctx context.Context, projectID string) ([]domain.Stamp, error)
}

// toBuild is the one rule of a layer to build (R2.2; RR1.3), for the queue and the status alike: every chapter has its card
// of the current instructions — a chapter on older cards means the operator left the saga on them —, the threshold of a
// rebuild is passed since the finished build (domain.Due), and the build of these chapters is not marked; unread says why it
// is. It returns the stamps it read.
func toBuild(ctx context.Context, store LayerChapters, marks ThreadsMarks, projectID, view string) (due bool, unread string, stamps []domain.Stamp, err error) {
	stamps, err = store.Stamps(ctx, projectID, view)
	if err != nil || len(stamps) == 0 {
		return false, "", stamps, err
	}
	for _, stamp := range stamps {
		if stamp.CardVersion < sagamemory.CardVersion {
			return false, "", stamps, nil
		}
	}
	built, err := store.BuiltChapters(ctx, projectID)
	if err != nil || !domain.Due(stamps, built) {
		return false, "", stamps, err
	}
	if marks != nil {
		if unread, err = marks.ThreadsUnread(ctx, projectID, stamps); err != nil || unread != "" {
			return false, unread, stamps, err
		}
	}
	return true, "", stamps, nil
}

func withCards(stamps []domain.Stamp) []domain.Stamp {
	carded := make([]domain.Stamp, 0, len(stamps))
	for _, stamp := range stamps {
		if stamp.CardVersion > 0 {
			carded = append(carded, stamp)
		}
	}
	return carded
}
