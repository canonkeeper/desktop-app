// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"sync"
	"time"
)

// Phase is the state of the link as getAccount says it (AC2.1).
type Phase string

const (
	// PhaseNone: the app is not linked.
	PhaseNone Phase = "none"
	// PhaseWaiting: a link waits for the confirmation in the browser.
	PhaseWaiting Phase = "waiting"
	// PhaseLinked: the app holds a pass.
	PhaseLinked Phase = "linked"
	// PhaseBroken: the site refused the pass — the link is broken.
	PhaseBroken Phase = "broken"
	// PhaseFailed: the last link did not go through.
	PhaseFailed Phase = "failed"
)

// Failure is why the last link did not go through.
type Failure string

const (
	// FailedExchange: the site did not change the code for a pass.
	FailedExchange Failure = "exchange"
	// FailedKeyring: the shell did not keep the pass.
	FailedKeyring Failure = "keyring"
	// FailedExpired: the waiting ended by its term without a return (specs/2026-11-15-desk-account-link-web.md AW1.6).
	FailedExpired Failure = "expired"
)

// Link is what the app knows of its link in this run, only in memory (AC2.1, AC2.3): the waiting whose code is being
// exchanged, why the last link failed, whether the site refused the pass, and the account the site told last — when, and
// whether the site did not answer since. A restart knows none of it: the pass alone says the app is linked.
type Link struct {
	mu          sync.Mutex
	exchanging  *Waiting
	failure     Failure
	broken      bool
	account     *Account
	at          time.Time
	asked       time.Time
	unreachable bool
}

// Known is a copy of what the link knows.
type Known struct {
	Exchanging  *Waiting
	Failure     Failure
	Broken      bool
	Account     *Account
	At          time.Time
	Unreachable bool
}

func (link *Link) Known() Known {
	link.mu.Lock()
	defer link.mu.Unlock()
	known := Known{Failure: link.failure, Broken: link.broken, At: link.at, Unreachable: link.unreachable}
	if link.exchanging != nil {
		waiting := *link.exchanging
		known.Exchanging = &waiting
	}
	if link.account != nil {
		account := *link.account
		known.Account = &account
	}
	return known
}

// Started: a new link begins; the failure and the broken link before it are past.
func (link *Link) Started() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.failure, link.broken = "", false
}

// Exchanging marks the waiting whose code goes to the site, until Exchanged.
func (link *Link) Exchanging(waiting Waiting) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.exchanging = &waiting
}

func (link *Link) Exchanged() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.exchanging = nil
}

// Linked: the pass is kept; the account of the exchange is known, and the next read asks the site for the rest.
func (link *Link) Linked(account Account, at time.Time) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.failure, link.broken, link.unreachable = "", false, false
	link.account, link.at, link.asked = &account, at, time.Time{}
}

// Failed: the link did not go through.
func (link *Link) Failed(failure Failure) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.failure, link.broken, link.account, link.unreachable = failure, false, nil, false
}

// Broke: the site refused the pass.
func (link *Link) Broke() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.broken, link.account, link.unreachable = true, nil, false
}

// Unlinked: the app is not linked any more; nothing of the account stays.
func (link *Link) Unlinked() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.failure, link.broken, link.account, link.unreachable, link.asked = "", false, nil, false, time.Time{}
}

// Due says whether the site is to be asked now — the page asked, or FreshEvery passed since the last time — and counts
// this time as asked.
func (link *Link) Due(now time.Time, fresh bool) bool {
	link.mu.Lock()
	defer link.mu.Unlock()
	if !fresh && !link.asked.IsZero() && now.Sub(link.asked) < FreshEvery {
		return false
	}
	link.asked = now
	return true
}

// Read: the site told the account.
func (link *Link) Read(account Account, at time.Time) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.account, link.at, link.unreachable = &account, at, false
}

// Unreachable: the site did not answer; what it told before stays.
func (link *Link) Unreachable() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.unreachable = true
}

// Stale makes the next read ask the site: an open or a close of a permit changed what it would tell
// (specs/2026-11-13-desk-account-ai.md AI2.11).
func (link *Link) Stale() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.asked = time.Time{}
}
