// SPDX-License-Identifier: AGPL-3.0-only

package obsidianvault

import (
	"errors"
	"testing"
	"time"

	service "canonkeeper/backend/internal/services/obsidian"
)

// errHeld stands for a hold of Windows in the table: a scanner or a synchronizer has the file open.
var errHeld = errors.New("held")

// specs/2026-11-15-desk-obsidian-export.md OE1.4: a rename Windows holds is tried again for at most the wait, and the file is
// looked at again before every try — the author may save it while the app waits —; the check that fails ends the tries.
func TestAHoldIsWaitedOutWithACheckBeforeEveryTry(t *testing.T) {
	cases := []struct {
		name string
		// ops are what the tries of the rename answer, the last one repeated; checks what the looks before them answer.
		ops, checks   []error
		want          error
		tries, looks  int
		heldAfterWait bool
		waitedAtLeast time.Duration
		waitedAtMost  time.Duration
	}{
		{name: "the first try goes", ops: []error{nil}, want: nil, tries: 1, looks: 1},
		{name: "a hold, then the try goes", ops: []error{errHeld, errHeld, nil}, want: nil, tries: 3, looks: 3},
		{name: "the author saved during the hold", ops: []error{errHeld}, checks: []error{nil, service.ErrChangedMeanwhile},
			want: service.ErrChangedMeanwhile, tries: 1, looks: 2},
		{name: "a file came to the path during the hold", ops: []error{errHeld}, checks: []error{nil, nil, service.ErrOccupied},
			want: service.ErrOccupied, tries: 2, looks: 3},
		{name: "the check fails before the first try", ops: []error{nil}, checks: []error{service.ErrGoneMeanwhile},
			want: service.ErrGoneMeanwhile, tries: 0, looks: 1},
		{name: "a failure that is no hold", ops: []error{errors.New("no space")}, want: nil, tries: 1, looks: 1},
		{name: "held past the wait", ops: []error{errHeld}, want: errHeld, heldAfterWait: true, waitedAtLeast: 2 * time.Second,
			waitedAtMost: 2 * time.Second},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clock := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
			start := clock
			holds := waiter{
				held: func(err error) bool { return errors.Is(err, errHeld) }, wait: 2 * time.Second,
				now: func() time.Time { return clock }, sleep: func(pause time.Duration) { clock = clock.Add(pause) },
			}
			tries, looks := 0, 0
			err := holds.checked(func() error {
				looks++
				if looks <= len(test.checks) {
					return test.checks[looks-1]
				}
				return nil
			}, func() error {
				tries++
				return test.ops[min(tries, len(test.ops))-1]
			})
			switch {
			case test.name == "a failure that is no hold":
				if err == nil || errors.Is(err, errHeld) || tries != 1 || looks != 1 {
					t.Fatalf("checked = %v after %d tries and %d looks", err, tries, looks)
				}
				return
			case !errors.Is(err, test.want) || err == nil && test.want != nil:
				t.Fatalf("checked = %v, want %v", err, test.want)
			}
			if test.heldAfterWait {
				if waited := clock.Sub(start); waited < test.waitedAtLeast || waited > test.waitedAtMost || tries < 2 || looks != tries {
					t.Fatalf("held past the wait: waited %v, %d tries, %d looks", waited, tries, looks)
				}
				return
			}
			if tries != test.tries || looks != test.looks {
				t.Fatalf("%d tries and %d looks, want %d and %d", tries, looks, test.tries, test.looks)
			}
		})
	}
}
