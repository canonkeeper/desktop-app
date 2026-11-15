package sagalayer

import (
	"context"
	"errors"
)

// TextVersion names one text version of a project.
type TextVersion struct {
	ProjectID string
	View      string
}

// QueueStore is what the queueing of a layer Job asks about a project.
type QueueStore interface {
	LayerChapters
	// QueueLayer queues the Job of the project; a Job already waiting is reused.
	QueueLayer(ctx context.Context, projectID, view string) error
}

// QueueDue queues the layer Job of a text version when it is worth paying for (R2.2). A chapter
// still without a card means the cards Job has work left, and its success asks again. A chapter on
// older cards means the operator left this saga on them (specs/2026-11-01-index-foundation.md, R3.1):
// such a saga, a reader's copy for one, pays for no layer. A build marked by the policy of the reading
// is not queued (specs/2026-11-15-desk-reading-rest.md RR1.3), nor the layer of a saga the model may not
// read now: its Job would end at once without work (specs/2026-11-15-desk-reading-queue.md RQ2.1).
type QueueDue struct {
	store  QueueStore
	policy QueuePolicy
}

// QueuePolicy is the policy of the reading the queue of the layer asks: whether the model may read a saga now — the one rule
// of the builders — and the marks of its threads.
type QueuePolicy interface {
	ReadingAllowed(ctx context.Context, projectID string, sample bool) (bool, error)
	ThreadsMarks
}

func NewQueueDue(store QueueStore) *QueueDue {
	if store == nil {
		panic("saga layer: store is required")
	}
	return &QueueDue{store: store}
}

// WithPolicy makes the queue leave the builds the policy of the reading of the build marks and the sagas it may not read
// now; the build gives it once its modules are composed. Without it — the site — nothing is marked and every saga is read.
func (useCase *QueueDue) WithPolicy(policy QueuePolicy) *QueueDue {
	useCase.policy = policy
	return useCase
}

// Execute says whether it queued the Job.
func (useCase *QueueDue) Execute(ctx context.Context, projectID, view string) (bool, error) {
	due, _, _, err := toBuild(ctx, useCase.store, useCase.policy, projectID, view)
	if err != nil || !due {
		return false, err
	}
	if useCase.policy != nil {
		if allowed, err := useCase.policy.ReadingAllowed(ctx, projectID, false); err != nil || !allowed {
			return false, err
		}
	}
	return true, useCase.store.QueueLayer(ctx, projectID, view)
}

// LayerVersions lists the text versions with indexed chapters.
type LayerVersions interface {
	LayerVersions(ctx context.Context) ([]TextVersion, error)
}

// QueueMissingLayers runs at the start of the backend: a project with cards gets the layer it lacks, and
// one whose cards came while the Job could not be queued gets it on time. One failing version does not
// stop the others.
type QueueMissingLayers struct {
	versions LayerVersions
	due      *QueueDue
}

func NewQueueMissingLayers(versions LayerVersions, due *QueueDue) *QueueMissingLayers {
	if versions == nil || due == nil {
		panic("saga layer: versions and queue are required")
	}
	return &QueueMissingLayers{versions: versions, due: due}
}

// Execute returns how many versions it queued the Job for.
func (useCase *QueueMissingLayers) Execute(ctx context.Context) (int, error) {
	versions, err := useCase.versions.LayerVersions(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	var errs []error
	for _, version := range versions {
		done, err := useCase.due.Execute(ctx, version.ProjectID, version.View)
		if err != nil {
			errs = append(errs, err)
		} else if done {
			queued++
		}
	}
	return queued, errors.Join(errs...)
}
