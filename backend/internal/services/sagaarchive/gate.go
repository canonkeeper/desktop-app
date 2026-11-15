// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"sync"
	"time"
)

// The limits of the saves of a saga into a file (SF2.4).
const (
	MaxSavesAtOnce  = 4
	MaxSavesPerHour = 10
	busyRetry       = time.Minute
	SaveDeadline    = 30 * time.Minute
	savesWindow     = time.Hour
)

// Gate lets a save through: one running for an author, MaxSavesAtOnce for the process, MaxSavesPerHour for an author in the
// last hour (SF2.4). A slow reader of one author keeps nobody else out.
type Gate struct {
	mu      sync.Mutex
	running map[string]bool
	starts  map[string][]time.Time
	total   int
	now     func() time.Time
}

// NewGate is the gate of a process.
func NewGate(now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	return &Gate{running: map[string]bool{}, starts: map[string][]time.Time{}, now: now}
}

// Enter takes a place for a save of the author: release gives it back. Without a place it says when to ask again.
func (gate *Gate) Enter(userID string) (release func(), retryAfter time.Duration, ok bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.now()
	starts := gate.starts[userID][:0]
	for _, at := range gate.starts[userID] {
		if now.Sub(at) < savesWindow {
			starts = append(starts, at)
		}
	}
	gate.starts[userID] = starts
	switch {
	case gate.running[userID] || gate.total >= MaxSavesAtOnce:
		return nil, busyRetry, false
	case len(starts) >= MaxSavesPerHour:
		return nil, starts[0].Add(savesWindow).Sub(now), false
	}
	gate.running[userID] = true
	gate.total++
	gate.starts[userID] = append(starts, now)
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.mu.Lock()
			defer gate.mu.Unlock()
			delete(gate.running, userID)
			gate.total--
		})
	}, 0, true
}
