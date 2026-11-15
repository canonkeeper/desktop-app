package app

import (
	"context"
	"reflect"
	"testing"
)

// cardsCatchUp, layersCatchUp and layerDue are the owners of the queue of the test: how many Jobs each would queue now.
type cardsCatchUp struct{ queued int }

func (cards cardsCatchUp) Execute(context.Context) (int, error)            { return cards.queued, nil }
func (cards cardsCatchUp) ExecuteFor(context.Context, string) (int, error) { return cards.queued, nil }

type layersCatchUp struct{ queued *int }

func (layers layersCatchUp) Execute(context.Context) (int, error) {
	*layers.queued = 1
	return 1, nil
}

type layerDue struct{ views *[]string }

func (due layerDue) Execute(_ context.Context, _, view string) (bool, error) {
	*due.views = append(*due.views, view)
	return false, nil
}

// specs/2026-11-15-desk-reading-rest.md RR1.8: a change of the settings queues what the start queues — the cards Job and the
// layer Job of what is due —: a layer whose cards need no Job is queued, not left waiting for the success of a cards Job
// that never comes; a build without the layer queues no layer. The consent of a saga asks the layer of each version only
// when no cards Job was queued.
func TestTheCatchUpQueuesTheLayerToo(t *testing.T) {
	var layers int
	var views []string
	queues := readingQueues{cards: cardsCatchUp{}, layers: layersCatchUp{queued: &layers}, due: layerDue{views: &views}, layer: true}
	if queued, err := queues.CatchUp(context.Background()); err != nil || queued != 1 || layers != 1 {
		t.Fatalf("CatchUp() = %d, %v; the layer asked %d", queued, err, layers)
	}
	layers = 0
	queues.layer = false
	if queued, err := queues.CatchUp(context.Background()); err != nil || queued != 0 || layers != 0 {
		t.Fatalf("a build without the layer: CatchUp() = %d, %v; the layer asked %d", queued, err, layers)
	}
	queues.layer = true
	if err := queues.Queue(context.Background(), "saga"); err != nil || !reflect.DeepEqual(views, []string{"published", "draft"}) {
		t.Fatalf("Queue() = %v; the layer asked for %v", err, views)
	}
	views = nil
	queues.cards = cardsCatchUp{queued: 1}
	if err := queues.Queue(context.Background(), "saga"); err != nil || len(views) != 0 {
		t.Fatalf("a queued cards Job: Queue() = %v; the layer asked for %v", err, views)
	}
}
