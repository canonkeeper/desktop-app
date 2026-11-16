// SPDX-License-Identifier: AGPL-3.0-only

package desktoptest

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// WaitsVariable names the scale of the waits of the process tests (specs/2026-11-15-desk-hygiene2.md HY1.3): a machine
// under load answers the same events later, so the checks that run the tests beside other work scale every deadline of a
// wait for an event by it — the start of the backend, an answer of its window, the end of a Job. Unset, the waits are as
// written; the scale never shortens them. What a test asserts of a time — a cold start within 30 s — is not a wait.
const WaitsVariable = "CANONKEEPER_DESKTOP_WAITS"

var waitsScale = sync.OnceValues(func() (float64, error) {
	value := os.Getenv(WaitsVariable)
	if value == "" {
		return 1, nil
	}
	scale, err := strconv.ParseFloat(value, 64)
	if err != nil || scale < 1 || scale > 100 {
		return 0, fmt.Errorf("%s must be a number from 1 to 100, not %q", WaitsVariable, value)
	}
	return scale, nil
})

// Waited is a deadline of a wait of a process test scaled by WaitsVariable.
func Waited(wait time.Duration) time.Duration {
	scale, err := waitsScale()
	if err != nil {
		panic(err)
	}
	return time.Duration(float64(wait) * scale)
}

// Deadline is the moment a wait of a process test begun now gives up, scaled by WaitsVariable.
func Deadline(wait time.Duration) time.Time {
	return time.Now().Add(Waited(wait))
}

// places holds the process tests of a test binary that run at once.
var places = make(chan struct{}, processesAtOnce)

// processesAtOnce is how many process tests of a test binary run at once: each runs its own backend, PostgreSQL and Codex
// in its own data directory on ports of its own, and most of its time waits for them and for the timers of the product —
// the package of the app went from 920 s one by one to 411 s four at once on a quiet machine
// (specs/2026-11-15-desk-hygiene2.md HY1.2).
const processesAtOnce = 4

// Parallel runs a process test beside the other process tests of its binary, at most processesAtOnce at a time; the test
// holds its place until it ends and its processes are stopped. A test calls it before it starts anything of its own. How
// long the test waited for its place is in its log: the time of a test less that wait is its own.
func Parallel(t *testing.T) {
	t.Helper()
	t.Parallel()
	asked := time.Now()
	places <- struct{}{}
	t.Logf("desktoptest: a place among the process tests after %s", time.Since(asked).Round(time.Millisecond))
	t.Cleanup(func() { <-places })
}
