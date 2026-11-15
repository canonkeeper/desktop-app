// SPDX-License-Identifier: AGPL-3.0-only

package appwork

import (
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// The reasons a turn of the chat fails with on the helper of the account (AI2.5): the codes of researchchat that name them
// live there; these are the words of the table.
const (
	ReasonDayFence    = "day_fence"
	ReasonUnlinked    = "unlinked"
	ReasonPermitEnded = "permit_ended"
	ReasonUnavailable = "unavailable"
	ReasonUnfit       = "unfit"
	ReasonTooLarge    = "too_large"
	ReasonFailed      = "failed"
	ReasonUnreachable = "unreachable"
	// ReasonJournal: the record of a request of the turn did not lie in the journal of what went out, so nothing went — of
	// «Свой ИИ» and of the account alike (specs/2026-11-14-desk-journal.md JR1.6).
	ReasonJournal = "journal"
)

// Memory is what the app remembers of a refusal that repeats, so it spends no unit and no return on it (AI2.6): the fence
// of the day until the Moscow midnight, an app that does not fit the site until the restart, a thread too large for this
// chat.
type Memory int

const (
	RememberNothing Memory = iota
	RememberDay
	RememberRun
	RememberChat
)

// CodeAppUnfit is the refusal the app gives itself to work of a kind the site did not take from this version (AI2.6): the
// code of the counter of the fence says the same.
const CodeAppUnfit = quota.CounterUnfitCode

// Answer is how the gateway answers Codex for a refusal of the site (AI2.5): the status — one Codex does not repeat for a
// lasting refusal, the site's own for a refusal that passes —, whether the gateway waits for a place of the site instead,
// the reason a turn fails with when this refusal was the last one of its Job, and what the app remembers.
type Answer struct {
	Status   int
	Wait     bool
	Reason   string
	Remember Memory
}

// Lasting is the status Codex does not repeat: Codex 0.167.2 ends a turn on 400 at once, while 403, 404 and 409 it repeats as
// a broken connection for stream_max_retries — eleven requests over about three minutes (measured by the binary of the set,
// specs/2026-11-13-desk-account-ai.md Decision log). The client of the core does not repeat it either.
const Lasting = 400

const lasting = Lasting

// AnswerOf is the one table of the gateway (AI2.5, Data): a refusal the app cannot change by repeating is answered with a
// status Codex does not repeat; ai.busy is a queue, not a refusal — the gateway waits for the place; a refusal and a
// failure of the provider go with the site's status and Retry-After; anything else of the site is no connection.
func AnswerOf(refusal Refusal) Answer {
	switch refusal.Code {
	case CodeDayFence:
		return Answer{Status: lasting, Reason: ReasonDayFence, Remember: RememberDay}
	case CodePassInvalid:
		return Answer{Status: lasting, Reason: ReasonUnlinked}
	case CodePermitClosed, CodePermitNotFound:
		return Answer{Status: lasting, Reason: ReasonPermitEnded}
	case CodeUnavailable:
		return Answer{Status: lasting, Reason: ReasonUnavailable}
	case CodeValidation, CodeUnsupported, CodePermitKind, CodeStepInstruction, CodeStepUnknown, CodeAppUnfit:
		return Answer{Status: lasting, Reason: ReasonUnfit, Remember: RememberRun}
	case CodeAccountCharacters, CodeReadingFence:
		// The reading of the books stops at its pause (AI3.5): the client of the core does not repeat it.
		return Answer{Status: lasting}
	case CodeTooLarge:
		return Answer{Status: lasting, Reason: ReasonTooLarge, Remember: RememberChat}
	case CodeBusy:
		return Answer{Wait: true}
	case CodeProviderRefused, CodeProviderFailed:
		return Answer{Status: refusal.Status, Reason: ReasonFailed}
	default:
		return Answer{Status: 502, Reason: ReasonUnreachable}
	}
}

// Unreachable is the answer to no answer of the site — a broken connection, a term, a 5xx of the site: Codex repeats it,
// and the turn ends «нет связи с сервером» once its repeats are over.
func Unreachable() Answer { return Answer{Status: 502, Reason: ReasonUnreachable} }

// Refusals are the refusals that repeat, remembered by the process (AI2.6): the fence of the day until the Moscow midnight
// of the clock, the work of a kind the site does not take from this version until the restart, a chat whose thread is
// too large for the site. Nothing of it is kept on the disk.
type Refusals struct {
	dayUntil time.Time
	unfit    map[string]bool
	tooLarge map[string]bool
}

// NewRefusals is a memory of a run.
func NewRefusals() *Refusals {
	return &Refusals{unfit: map[string]bool{}, tooLarge: map[string]bool{}}
}

// Heard remembers a refusal of a work of the kind (the purpose of a request: chat, scan, materials, a step of the reading)
// of the chat at now.
func (refusals *Refusals) Heard(memory Memory, kind, chatID string, now time.Time) {
	switch memory {
	case RememberDay:
		refusals.dayUntil = quota.NextMoscowMidnight(now)
	case RememberRun:
		refusals.unfit[kind] = true
	case RememberChat:
		if chatID != "" {
			refusals.tooLarge[chatID] = true
		}
	}
}

// Of is the code of the refusal the app gives at once, before any permit, to a new work of the kind of the chat at now; ""
// lets it go to the site.
func (refusals *Refusals) Of(kind, chatID string, now time.Time) string {
	switch {
	case refusals.unfit[kind]:
		return CodeValidation
	case kind == string(KindTurn) && now.Before(refusals.dayUntil):
		return CodeDayFence
	case chatID != "" && refusals.tooLarge[chatID]:
		return CodeTooLarge
	default:
		return ""
	}
}

// Quota says the code is a refusal of the tier the site sends as on the site — the page reads it as a refusal of a limit.
func Quota(code string) bool { return strings.HasPrefix(code, "quota.") }
