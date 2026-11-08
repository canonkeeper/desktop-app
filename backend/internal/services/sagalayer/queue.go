package sagalayer

import (
	"context"
	"errors"

	domain "canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// TextVersion names one text version of a project.
type TextVersion struct {
	ProjectID string
	View      string
}

// QueueStore is what the queueing of a layer Job asks about a project.
type QueueStore interface {
	Stamps(ctx context.Context, projectID, view string) ([]domain.Stamp, error)
	// BuiltChapters returns what the finished build was built from, nil without one.
	BuiltChapters(ctx context.Context, projectID string) ([]domain.Stamp, error)
	// QueueLayer queues the Job of the project; a Job already waiting is reused.
	QueueLayer(ctx context.Context, projectID, view string) error
}

// QueueDue queues the layer Job of a text version when it is worth paying for (R2.2). A chapter
// still without a card means the cards Job has work left, and its success asks again. A chapter on
// older cards means the operator left this saga on them (specs/2026-11-01-index-foundation.md, R3.1):
// such a saga, a reader's copy for one, pays for no layer.
type QueueDue struct {
	store QueueStore
}

func NewQueueDue(store QueueStore) *QueueDue {
	if store == nil {
		panic("saga layer: store is required")
	}
	return &QueueDue{store: store}
}

// Execute says whether it queued the Job.
func (useCase *QueueDue) Execute(ctx context.Context, projectID, view string) (bool, error) {
	stamps, err := useCase.store.Stamps(ctx, projectID, view)
	if err != nil || len(stamps) == 0 {
		return false, err
	}
	for _, stamp := range stamps {
		if stamp.CardVersion < sagamemory.CardVersion {
			return false, nil
		}
	}
	built, err := useCase.store.BuiltChapters(ctx, projectID)
	if err != nil || !domain.Due(stamps, built) {
		return false, err
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
