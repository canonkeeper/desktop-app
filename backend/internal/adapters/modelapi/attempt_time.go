package modelapi

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// The time of an HTTP attempt with its answer (specs/2026-11-17-long-chapters.md LC2.1): of the client of the site — a part
// of a long chapter in 300 000 characters is read in about 20 minutes —, and of the client of the app (CarryJob).
const (
	siteAttemptTime = 45 * time.Minute
	appAttemptTime  = 30 * time.Minute
)

// errAttemptTime is the cause of the context of an HTTP attempt its own time ended.
var errAttemptTime = errors.New("model API: the time of the attempt is over")

// attemptTimeout is the error of an attempt its own time ended: the call of the site repeats it once (LC2.1); typed as
// sagamemory.ErrAttemptTimeout.
type attemptTimeout struct{ err error }

func (out *attemptTimeout) Error() string {
	return "model API: the attempt ran out of its time: " + out.err.Error()
}

func (out *attemptTimeout) Unwrap() []error { return []error{out.err, sagamemory.ErrAttemptTimeout} }

// attempt is the context of an HTTP attempt of a call: the call's, ended by the own time of the attempt with its cause
// (LC2.1) — a deadline of the call that comes first stays.
func (client *Client) attempt(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadlineCause(ctx, time.Now().Add(client.attemptTime), errAttemptTime)
}

// timedOut is the error of an attempt that failed: the time of the attempt ended it while the call lives — typed for the
// client of the site, whose callers do not repeat it (LC2.1, LC1.3); the client of the app keeps its error as before — or
// err as it is. The time of a connection or of TLS of the transport is not the time of the attempt.
func (client *Client) timedOut(call, attemptCtx context.Context, outcome *attempt, err error) error {
	if call.Err() != nil || !errors.Is(context.Cause(attemptCtx), errAttemptTime) {
		return err
	}
	outcome.timedOut = true
	if client.config.CarryJob {
		return err
	}
	return &attemptTimeout{err: err}
}

// timedOutTwice counts the attempts of a call of the site their own time ended: the first is repeated, the second ends the
// call (LC2.1). The client of the app repeats as before.
func (client *Client) timedOutTwice(outcome attempt, timeouts *int) bool {
	if !outcome.timedOut || client.config.CarryJob {
		return false
	}
	*timeouts++
	return *timeouts > 1
}
