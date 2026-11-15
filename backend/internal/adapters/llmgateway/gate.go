// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"sync"

	"canonkeeper/backend/internal/domain/coresteps"
)

// lane is the turn of a request at the gate: the chat, the pass of a chapter whose card came, the rest of the reading.
type lane int

const (
	laneChat lane = iota
	lanePass
	laneReading
	lanes
)

// reads says the lane is the reading of the books: the pass of a chapter or the rest of the reading.
func (lane lane) reads() bool { return lane != laneChat }

// laneOf is the lane of a request: the pass of a chapter goes before new cards, so a stop of the reading finds at most
// «Запросов одновременно» chapters with a paid card and no pass (specs/2026-11-13-desk-consent.md CN3.5).
func laneOf(request call) lane {
	switch {
	case request.role == roleChat:
		return laneChat
	case request.purpose == string(coresteps.CardPass):
		return lanePass
	default:
		return laneReading
	}
}

// gate bounds the requests at the provider by «Запросов одновременно» of the author (GW1.5): a request over the limit
// waits for its turn instead of a 429 the clients would repeat with pauses. The limit is read whenever a place is taken
// or freed, so a change of the settings holds from the next request. A freed place goes to the chat before the reading
// of the books — the author waits for an answer, nobody waits for a card —, then to the pass of a chapter whose card came
// before a new card, and in turn within a lane. The reading may hold at most readingLimit places when it names one
// (specs/2026-11-13-desk-account-ai.md AI3.3: of the two places of the helper of the account the reading holds one, and the
// other is always free for a turn and its scan); zero is no bound of its own.
type gate struct {
	mu           sync.Mutex
	busy         int
	reading      int
	limit        func() int
	readingLimit func() int
	waiting      [lanes][]chan struct{}
}

func newGate(limit, readingLimit func() int) *gate {
	if readingLimit == nil {
		readingLimit = func() int { return 0 }
	}
	return &gate{limit: limit, readingLimit: readingLimit}
}

// acquire takes a place or waits for one; a request that ends while it waits takes none.
func (gate *gate) acquire(ctx context.Context, lane lane) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	granted := make(chan struct{})
	gate.mu.Lock()
	gate.waiting[lane] = append(gate.waiting[lane], granted)
	gate.wakeLocked()
	gate.mu.Unlock()
	select {
	case <-granted:
		return nil
	case <-ctx.Done():
		gate.mu.Lock()
		defer gate.mu.Unlock()
		for index, waiter := range gate.waiting[lane] {
			if waiter == granted {
				gate.waiting[lane] = append(gate.waiting[lane][:index], gate.waiting[lane][index+1:]...)
				return ctx.Err()
			}
		}
		// The place was handed over while the request ended: it goes on to the next.
		gate.freeLocked(lane)
		gate.wakeLocked()
		return ctx.Err()
	}
}

// release frees the place a request of the lane took.
func (gate *gate) release(lane lane) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.freeLocked(lane)
	gate.wakeLocked()
}

func (gate *gate) freeLocked(lane lane) {
	gate.busy--
	if lane.reads() {
		gate.reading--
	}
}

// openLocked says a request of the lane may take a free place: a place is free and the reading holds less than its bound.
func (gate *gate) openLocked(lane lane) bool {
	if gate.busy >= max(gate.limit(), 1) {
		return false
	}
	bound := gate.readingLimit()
	return !lane.reads() || bound <= 0 || gate.reading < bound
}

// wakeLocked hands the free places to the waiting, by their lanes; a lane of the reading at its bound lets the chat by.
func (gate *gate) wakeLocked() {
	for {
		woke := false
		for index := range gate.waiting {
			current := lane(index)
			if len(gate.waiting[current]) == 0 || !gate.openLocked(current) {
				continue
			}
			next := gate.waiting[current][0]
			gate.waiting[current] = gate.waiting[current][1:]
			gate.busy++
			if current.reads() {
				gate.reading++
			}
			close(next)
			woke = true
			break
		}
		if !woke {
			return
		}
	}
}
