// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// specs/2026-11-13-desk-consent.md CN3.5: a freed place goes to the chat first, then to the pass of a chapter whose card came,
// then to a new card — a stop of the reading finds at most «Запросов одновременно» chapters with a paid card and no pass,
// not every chapter the builder handed out.
func TestThePassOfAChapterGoesBeforeNewCards(t *testing.T) {
	gate := newGate(func() int { return 1 }, nil)
	ctx := context.Background()
	if err := gate.acquire(ctx, laneOf(call{role: roleCards, purpose: string(coresteps.Card)})); err != nil {
		t.Fatal(err)
	}
	order := make(chan string, 4)
	queue := func(name string, request call) {
		t.Helper()
		lane := laneOf(request)
		gate.mu.Lock()
		before := len(gate.waiting[lane])
		gate.mu.Unlock()
		go func() {
			if err := gate.acquire(ctx, lane); err == nil {
				order <- name
			}
		}()
		for deadline := time.Now().Add(2 * time.Second); ; {
			gate.mu.Lock()
			queued := len(gate.waiting[lane]) > before
			gate.mu.Unlock()
			if queued {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not queue", name)
			}
			time.Sleep(time.Millisecond)
		}
	}
	queue("card 2", call{role: roleCards, purpose: string(coresteps.Card)})
	queue("card 3", call{role: roleCards, purpose: string(coresteps.Card)})
	queue("pass 1", call{role: roleCards, purpose: string(coresteps.CardPass)})
	queue("layer", call{role: roleCards, purpose: string(coresteps.LayerBook)})
	queue("chat", call{role: roleChat, purpose: modeljournal.PurposeChat})
	var got []string
	for range 5 {
		gate.release(laneReading)
		select {
		case name := <-order:
			got = append(got, name)
		case <-time.After(2 * time.Second):
			t.Fatalf("the places went to %v", got)
		}
	}
	want := []string{"chat", "pass 1", "card 2", "card 3", "layer"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("the places went to %v; want %v", got, want)
		}
	}
}
