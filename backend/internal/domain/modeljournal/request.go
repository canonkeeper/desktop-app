// SPDX-License-Identifier: AGPL-3.0-only

// Package modeljournal is the journal of the requests the local gateway of the desktop app sent to the author's provider
// (specs/2026-11-13-desk-gateway.md GW1.7; specs/2026-11-07-desktop-app.md DA6): what each request was for, under which
// Job, where it went, what it cost and how it ended — never the text of a request or of an answer.
package modeljournal

import (
	"regexp"
	"time"

	"canonkeeper/backend/internal/domain/coresteps"
)

// PurposeChat is the purpose of every request of Codex: a turn of the chat, its compaction included. A call of the core
// has the name of its step (coresteps) for its purpose; there is no other list of purposes.
const PurposeChat = "chat"

// PurposeProbe is a request that asks the provider about itself, not about a saga: the probe of the protocol of an
// address (specs/2026-11-13-desk-gateway.md GW1.3). It has no saga and no Job.
const PurposeProbe = "probe"

// Purposes are the purposes a request may have: the chat, the probe and every step of the core.
func Purposes() []string {
	purposes := []string{PurposeChat, PurposeProbe}
	for _, step := range coresteps.All() {
		purposes = append(purposes, string(step.Name))
	}
	return purposes
}

// Outcome is how a request ended.
type Outcome string

const (
	OutcomeCompleted  Outcome = "completed"
	OutcomeIncomplete Outcome = "incomplete"
	OutcomeFailed     Outcome = "failed"
	OutcomeCanceled   Outcome = "canceled"
)

// Tokens are the tokens of a request as the provider reported them.
type Tokens struct {
	Input, CachedInput, Output int64
}

// Cost is what a request cost in millionths of the currency of the prices of its minute
// (specs/2026-11-13-desk-consent.md CN3.3).
type Cost struct {
	Micros   int64
	Currency string
}

// Request is one request to the provider as the journal keeps it. JobID is empty when nobody named the Job; Tokens is
// nil when the provider reported no usage — the tokens are unknown, not zero. Cost is nil without the prices of its minute;
// Estimated says the cost, and the count of the reading, are by an estimate — the provider sent no usage. OutputCharacters
// are the characters of the answer the gateway saw go by; the journal does not keep them. RefusalCode is the code of the
// refusal of the site of the account (specs/2026-11-13-desk-account-ai.md AI1.7, AI4.2) — never its words, a body or a header.
type Request struct {
	// ID is the number of the record of its sending (specs/2026-11-14-desk-journal.md JR1.1): the gateway gives it; empty
	// leaves the journal to give one. CheckID is the number of the check of a probe.
	ID               string
	CheckID          string
	At               time.Time
	Purpose          string
	JobID            string
	Profile          string
	ProviderHost     string
	Model            string
	Tokens           *Tokens
	RequestBytes     int64
	Outcome          Outcome
	Duration         time.Duration
	Cost             *Cost
	Estimated        bool
	OutputCharacters int64
	RefusalCode      string
	// Failure is how a failed request failed (specs/2026-11-14-desk-journal.md JR2.4): FailureRefused, FailureConnection,
	// FailureTorn or FailureUnsent; empty for a request that did not fail or a failure of another kind.
	Failure string
}

// How a failed request failed: the helper refused it before its answer, no answer came for the connection, its answer broke
// off once it began, or its body did not leave the app after its record lay — the client of the site holds no pass or waits
// out the site's Retry-After.
const (
	FailureRefused    = "refused"
	FailureConnection = "connection"
	FailureTorn       = "torn"
	FailureUnsent     = "unsent"
)

// refusalCode is the shape of a code of a refusal of the site the journal keeps (migration 0151): the database keeps nothing
// else.
var refusalCode = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)+$`)

// RefusalCodeOf is the code of a refusal of the site as the journal keeps it (specs/2026-11-13-desk-account-ai.md AI4.2): a
// code of another shape is kept as none, so the row — and the count of the reading written with it — is never lost for it.
func RefusalCodeOf(code string) string {
	if len(code) < 3 || len(code) > 100 || !refusalCode.MatchString(code) {
		return ""
	}
	return code
}
