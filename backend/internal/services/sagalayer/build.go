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

// BuildLayer runs the layer Job of one project.
type BuildLayer struct {
	text      SagaText
	store     Store
	model     Model
	usage     UsageRecorder
	modelName string
	parallel  int
	failure   FailureLog
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
func (build *BuildLayer) RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject) (jobs.Status, error) {
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
	carded := withCards(stamps)
	current := stored.Digest != "" && stored.Digest == domain.Digest(carded)
	waits := stored.Digest != "" && stored.Steps == nil && !domain.Due(stamps, stored.Layer.Chapters)
	if len(carded) == 0 || current || waits {
		return build.finish(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusSucceeded, Result: &jobs.SagaLayerResult{
			Type: jobs.KindSagaLayer, SourceView: subject.SourceView, Chapters: len(stored.Layer.Chapters), Threads: len(stored.Layer.Threads),
		}})
	}
	run, err := build.newRun(ctx, claim, subject, stamps, stored)
	if err != nil {
		return "", err
	}
	layer, result, err := run.runSteps()
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
	return build.store.FinishLayer(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusCanceled})
}

// FailForClaim finishes a layer Job the worker gave up on; the checkpoint stays for the next Job.
func (build *BuildLayer) FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.SagaLayerSubject, failure jobs.Error) error {
	return build.store.FinishLayer(ctx, claim, LayerOutcome{Subject: subject, Status: jobs.StatusFailed, Error: &failure})
}

func (build *BuildLayer) recordUsage(ctx context.Context, projectID string, usage quota.ModelUsage) {
	if build.usage == nil || usage.Empty() {
		return
	}
	build.usage.RecordModelUsage(context.WithoutCancel(ctx), quota.UsageRecord{
		Source: quota.UsageLayer, ProjectID: projectID, Model: build.modelName, Usage: usage,
	})
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
