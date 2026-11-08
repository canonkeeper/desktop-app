package modelapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// R11.2: with every place taken, a freed place goes to the interactive waiter even when a
// background one waited longer.
func TestGateGivesFreedPlaceToInteractiveFirst(t *testing.T) {
	gate := newGate(2)
	ctx := t.Context()
	for range 2 {
		if err := gate.acquire(ctx, Background); err != nil {
			t.Fatal(err)
		}
	}
	order := make(chan Class, 2)
	waitFor := func(class Class) {
		go func() {
			if err := gate.acquire(ctx, class); err == nil {
				order <- class
			}
		}()
	}
	waitFor(Background)
	waitUntilWaiting(t, gate, Background, 1)
	waitFor(Interactive)
	waitUntilWaiting(t, gate, Interactive, 1)
	gate.release()
	if first := <-order; first != Interactive {
		t.Fatalf("first freed place went to class %d, want interactive", first)
	}
	gate.release()
	if second := <-order; second != Background {
		t.Fatalf("second freed place went to class %d, want background", second)
	}
}

// R11.3: a waiter whose context ends takes no place and leaves the queue.
func TestGateWaiterLeavesOnContextEnd(t *testing.T) {
	gate := newGate(1)
	if err := gate.acquire(t.Context(), Background); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := gate.acquire(ctx, Interactive); err == nil {
		t.Fatal("waiter got a place that was never freed")
	}
	gate.release()
	if gate.free != 1 || len(gate.waiting[Interactive]) != 0 {
		t.Fatalf("gate after a waiter left: free %d, waiting %d", gate.free, len(gate.waiting[Interactive]))
	}
}

// R11.1: the retry pause after 429 holds no place, so another call runs meanwhile.
func TestRetryPauseFreesGatePlace(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, Model: "m", Parallel: 1})
	pausing := make(chan struct{})
	resume := make(chan struct{})
	client.pause = func(int) time.Duration {
		close(pausing)
		<-resume
		return 0
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := client.Background().Complete(t.Context(), "", "first")
		done <- err
	}()
	<-pausing
	other, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, _, err := client.Interactive().Complete(other, "", "second"); err != nil {
		t.Fatalf("a call during the retry pause did not get the free place: %v", err)
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatalf("retried call: %v", err)
	}
}

func waitUntilWaiting(t *testing.T, gate *gate, class Class, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gate.mu.Lock()
		waiting := len(gate.waiting[class])
		gate.mu.Unlock()
		if waiting == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("class %d waiters did not reach %d", class, count)
}
