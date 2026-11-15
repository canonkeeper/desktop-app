package app

import (
	"context"
	"errors"
)

// readingQueues are the owners of the queue of the reading of the books as the paths without a word of the author ask them
// (specs/2026-11-13-desk-consent.md CN3.2; specs/2026-11-15-desk-reading-rest.md RR1.8): the catch-up of a change of the
// settings queues what the start queues — the cards Job of what a version misses and the layer Job of what is due —, and the
// consent of a saga queues its reading. What the marks of the policy of the reading keep, each owner leaves alone itself.
type readingQueues struct {
	cards interface {
		Execute(ctx context.Context) (int, error)
		ExecuteFor(ctx context.Context, projectID string) (int, error)
	}
	layers interface {
		Execute(ctx context.Context) (int, error)
	}
	due interface {
		Execute(ctx context.Context, projectID, view string) (bool, error)
	}
	// layer: the build has the thread layer.
	layer bool
}

// CatchUp queues the cards Job of every version that misses cards, names or its map and, in a build with the thread layer,
// the layer Job of every version whose layer is due — a layer whose cards need no Job waits for no success of one.
func (queues readingQueues) CatchUp(ctx context.Context) (int, error) {
	queued, err := queues.cards.Execute(ctx)
	if !queues.layer {
		return queued, err
	}
	layers, layersErr := queues.layers.Execute(ctx)
	return queued + layers, errors.Join(err, layersErr)
}

// Queue queues the reading of one saga: the cards Job of what it misses, or else the layer Job when it is due — the success
// of a cards Job queues the layer otherwise.
func (queues readingQueues) Queue(ctx context.Context, projectID string) error {
	queued, err := queues.cards.ExecuteFor(ctx, projectID)
	if err != nil || queued > 0 || !queues.layer {
		return err
	}
	for _, view := range []string{"published", "draft"} {
		waits, err := queues.due.Execute(ctx, projectID, view)
		if err != nil || waits {
			return err
		}
	}
	return nil
}
