// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"
	"sync"
	"time"
)

// The limits of the operations that read a vault (OB0.1).
const (
	// MaxAtOnce is how many operations read vaults in the process at once; a saga has one.
	MaxAtOnce = 2
	// busyRetry is when to ask again past a busy gate.
	busyRetry = 5 * time.Second
)

// Limits are the times an operation reads a vault within (OB0.1): the look, the other operations, one note; and Write,
// the time of the writes an operation makes once its reading returned in time
// (specs/2026-11-14-desk-obsidian-books.md OK0.1).
type Limits struct {
	Scan, Read, Note, Write time.Duration
}

// DefaultLimits are the times of OB0.1 and OK0.1: the writes of a load upload up to tens of volumes of up to 50 MB each.
var DefaultLimits = Limits{Scan: 20 * time.Second, Read: 2 * time.Minute, Note: 5 * time.Second, Write: 10 * time.Minute}

// Busy is an operation the gate does not let through now: the saga has one running or the process its two
// (obsidian.busy).
type Busy struct{ RetryAfter time.Duration }

func (Busy) Error() string { return "obsidian: busy" }

// ErrSlow is a vault that did not answer within the time of its operation (obsidian.slow): nothing is written.
var ErrSlow = errors.New("obsidian: the vault is slow")

// Gate lets an operation that reads a vault through: one for a saga, MaxAtOnce for the process (OB0.1). The place is
// given back when every reading of the operation has returned, not when its time ends (gated): a file system that holds a
// reading holds its place, so no more readings wait on files at once than the gate lets through.
type Gate struct {
	mu      sync.Mutex
	running map[string]bool
	total   int
	// choosing is the one choice of the folder of a first export of the process (specs/2026-11-15-desk-obsidian-export.md
	// OE1.5): a choice holds it until the folder it chose holds its manifest, so two sagas of one title never take one folder.
	choosing chan struct{}
}

// NewGate is the gate of a process.
func NewGate() *Gate { return &Gate{running: map[string]bool{}, choosing: make(chan struct{}, 1)} }

// choose takes the choice of a folder of a first export, waiting within ctx for another one to end; free gives it back.
func (gate *Gate) choose(ctx context.Context) (free func(), err error) {
	select {
	case gate.choosing <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(func() { <-gate.choosing }) }, nil
}

// Enter takes the place of an operation of the saga; release gives it back.
func (gate *Gate) Enter(projectID string) (release func(), err error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.running[projectID] || gate.total >= MaxAtOnce {
		return nil, Busy{RetryAfter: busyRetry}
	}
	gate.running[projectID] = true
	gate.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.mu.Lock()
			defer gate.mu.Unlock()
			delete(gate.running, projectID)
			gate.total--
		})
	}, nil
}

// gated runs work in the place of an operation of the saga (OB0.1): busy when the gate lets nothing through; past limit the
// caller hears ErrSlow at once, and the place is given back once the work and every reading it started have returned.
func gated[T any](ctx context.Context, gate *Gate, projectID string, limit time.Duration, work func(context.Context) (T, error)) (T, error) {
	release, err := gate.Enter(projectID)
	if err != nil {
		var zero T
		return zero, err
	}
	return held(ctx, limit, release, work)
}

// held runs work within limit and calls release once the work and every reading it started within its context have
// returned — at once when they returned before the answer, later when a file system still holds one.
func held[T any](ctx context.Context, limit time.Duration, release func(), work func(context.Context) (T, error)) (T, error) {
	tracked := &readings{release: release}
	value, err := within(context.WithValue(ctx, readingsKey{}, tracked), limit, work)
	tracked.end()
	return value, err
}

type readingsKey struct{}

// readings counts the readings of one operation that still run: its work and the readings of its notes.
type readings struct {
	mu      sync.Mutex
	running int
	ended   bool
	release func()
}

func (tracked *readings) start() {
	if tracked == nil {
		return
	}
	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	tracked.running++
}

func (tracked *readings) finish() {
	if tracked == nil {
		return
	}
	tracked.mu.Lock()
	tracked.running--
	free := tracked.ended && tracked.running == 0
	tracked.mu.Unlock()
	if free {
		tracked.release()
	}
}

// end says the operation answered: what it holds is given back when nothing of it runs any more.
func (tracked *readings) end() {
	tracked.mu.Lock()
	tracked.ended = true
	free := tracked.running == 0
	tracked.mu.Unlock()
	if free {
		tracked.release()
	}
}

// within runs work on the files of a vault under a limit of time: past it the answer is ErrSlow, and the work — held,
// maybe, by a file system that does not answer — is left to end by itself; it sees its context end. A caller that went
// away gets its own end. The readings of the operation (held) count the work until it returns.
func within[T any](ctx context.Context, limit time.Duration, work func(context.Context) (T, error)) (T, error) {
	return withinOr(ctx, limit, work, nil)
}

// withinOr is within whose work may answer with a value that holds something — a vault open, a lock — once its caller heard
// ErrSlow or its own end: such a late answer goes to discard, which gives back what it holds. A work that answers right at
// the end of its time is late too when the caller heard the end first; nothing it holds is left behind.
func withinOr[T any](ctx context.Context, limit time.Duration, work func(context.Context) (T, error), discard func(T)) (T, error) {
	limited, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	type result struct {
		value T
		err   error
	}
	tracked, _ := ctx.Value(readingsKey{}).(*readings)
	tracked.start()
	done := make(chan result, 1)
	go func() {
		value, err := work(limited)
		// The count drops before the answer: an operation that answers with nothing running gives its place back at once.
		tracked.finish()
		done <- result{value: value, err: err}
	}()
	var zero T
	select {
	case got := <-done:
		if got.err != nil && errors.Is(got.err, context.DeadlineExceeded) && ctx.Err() == nil {
			return zero, ErrSlow
		}
		return got.value, got.err
	case <-limited.Done():
		if discard != nil {
			go func() {
				if got := <-done; got.err == nil {
					discard(got.value)
				}
			}()
		}
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return zero, ErrSlow
	}
}

// writing is an operation that reads a vault and then writes (specs/2026-11-14-desk-obsidian-books.md OK0.1): it holds the
// place of its saga from its first step to the end of its writes. Only its reading goes within the time of the operation —
// obsidian.slow is heard only there, and then nothing is written —; its writes start once the reading returned in time and
// go to their end on a context of their own, which neither the time of the reading nor a caller who went away ends.
type writing struct {
	ctx     context.Context
	tracked *readings
}

// enter takes the place of the saga for an operation that writes; end gives it back once every reading of it returned.
func enter(ctx context.Context, gate *Gate, projectID string) (*writing, error) {
	release, err := gate.Enter(projectID)
	if err != nil {
		return nil, err
	}
	tracked := &readings{release: release}
	return &writing{ctx: context.WithValue(ctx, readingsKey{}, tracked), tracked: tracked}, nil
}

func (operation *writing) end() { operation.tracked.end() }

// writes is the context of the writes of the operation: detached from the caller, with its own time.
func (operation *writing) writes(limit time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(operation.ctx), limit)
}

// readIn runs the reading of an operation that writes within limit (OB0.1).
func readIn[T any](operation *writing, limit time.Duration, work func(context.Context) (T, error)) (T, error) {
	return within(operation.ctx, limit, work)
}

// readInOr is readIn whose late answer goes to discard (withinOr).
func readInOr[T any](operation *writing, limit time.Duration, work func(context.Context) (T, error), discard func(T)) (T, error) {
	return withinOr(operation.ctx, limit, work, discard)
}
