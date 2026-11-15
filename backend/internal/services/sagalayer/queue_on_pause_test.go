package sagalayer

import (
	"context"
	"testing"
)

// specs/2026-11-15-desk-reading-queue.md RQ2.1, RQ2.2: a layer Job of a saga the model may not read now has nothing to do —
// it ends layer.consent at once — so the queue of a due layer queues none: neither the catch-up of the start nor the success
// of a cards Job adds an empty Job on a pause. The reading allowed queues it as before, and so does the site, without a
// policy.
func TestTheLayerOfASagaTheModelMayNotReadIsNotQueued(t *testing.T) {
	store := &layerStore{stamps: stampsOf(layerVersion("h1"))}
	if queued, err := NewQueueDue(store).WithPolicy(readingPolicy{allowed: false}).Execute(context.Background(), layerProjectID, "draft"); err != nil || queued || len(store.queued) != 0 {
		t.Fatalf("a due layer the model may not read = %v, %v, queued %v; want none", queued, err, store.queued)
	}
	sweep := NewQueueMissingLayers(layerVersions{{ProjectID: layerProjectID, View: "draft"}}, NewQueueDue(store).WithPolicy(readingPolicy{allowed: false}))
	if queued, err := sweep.Execute(context.Background()); err != nil || queued != 0 || len(store.queued) != 0 {
		t.Fatalf("the start on a pause = %d, %v, queued %v; want none", queued, err, store.queued)
	}
	if queued, err := NewQueueDue(store).WithPolicy(readingPolicy{allowed: true}).Execute(context.Background(), layerProjectID, "draft"); err != nil || !queued {
		t.Fatalf("a due layer the model may read = %v, %v", queued, err)
	}
	if queued, err := NewQueueDue(store).Execute(context.Background(), layerProjectID, "draft"); err != nil || !queued {
		t.Fatalf("the site = %v, %v", queued, err)
	}
}
