package modelapi

import (
	"context"
	"sync"
)

// Class orders waiters of the gate: a freed place goes to an interactive call (a chat turn's
// scan) before a background one (chapter cards); within a class callers wait in turn
// (specs/2026-10-28-writer-quotas-and-fair-queue.md UC11).
type Class int

const (
	Interactive Class = iota
	Background
	classes
)

// gate bounds the model calls of the whole process. A place is held for one HTTP attempt only,
// so a retry pause never keeps it.
type gate struct {
	mu      sync.Mutex
	free    int
	waiting [classes][]chan struct{}
}

func newGate(places int) *gate {
	return &gate{free: max(places, 1)}
}

// acquire takes a place or waits for one; a context that ends first takes no place.
func (gate *gate) acquire(ctx context.Context, class Class) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.mu.Lock()
	if gate.free > 0 {
		gate.free--
		gate.mu.Unlock()
		return nil
	}
	granted := make(chan struct{})
	gate.waiting[class] = append(gate.waiting[class], granted)
	gate.mu.Unlock()
	select {
	case <-granted:
		return nil
	case <-ctx.Done():
		gate.mu.Lock()
		defer gate.mu.Unlock()
		for index, waiter := range gate.waiting[class] {
			if waiter == granted {
				gate.waiting[class] = append(gate.waiting[class][:index], gate.waiting[class][index+1:]...)
				return ctx.Err()
			}
		}
		// The place was handed over while the context ended: pass it on.
		gate.releaseLocked()
		return ctx.Err()
	}
}

func (gate *gate) release() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.releaseLocked()
}

func (gate *gate) releaseLocked() {
	for class := range gate.waiting {
		if len(gate.waiting[class]) > 0 {
			next := gate.waiting[class][0]
			gate.waiting[class] = gate.waiting[class][1:]
			close(next)
			return
		}
	}
	gate.free++
}
