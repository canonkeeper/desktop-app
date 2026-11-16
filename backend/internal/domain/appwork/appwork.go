// SPDX-License-Identifier: AGPL-3.0-only

// Package appwork is the count of the work of a linked app on the models of the site (specs/2026-11-13-desk-account-ai.md
// AI2, AI3): the kinds of the work and its source fixed at its start, the refs of the permits — derived from the Job, so no
// list of permits is kept on the computer —, what a permit of the site says, the refusals of the site, how the gateway
// answers Codex for each of them and what the app remembers of the refusals that repeat, and whether the unit of a
// question came back. The site decides every unit; the app only names its work and reads the answers.
package appwork

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// The places of a linked app on the models of the site (specs/2026-11-13-desk-account-ai.md AI3.3,
// specs/2026-11-13-desk-proxy.md PX1.5; the owner's word of 16.11): the site keeps PlacesPerApp requests of one app at
// once, and the reading of the books holds at most ReadingPlaces of them — so many chapters at once —, so a question of
// the chat always has its place. The site's gate and the app's gateway and policy of the reading read these two numbers.
const (
	PlacesPerApp  = 6
	ReadingPlaces = PlacesPerApp - 1
)

// Kind is the work a permit of the site lets the app do (the site's modelpermits.Kind, coresteps.Permit).
type Kind string

const (
	// KindTurn is a turn of the chat: a question of the day and a place of «разом».
	KindTurn Kind = "chat_turn"
	// KindMaterials is the reading of a file of the author's materials by the model: an upload of the day.
	KindMaterials Kind = "materials"
	// KindReading is the reading of the books by the model: no unit, the characters of the tier.
	KindReading Kind = "reading"
)

// Source is the helper a work of the app goes to, fixed at its start (AI1.4): «Свой ИИ» or the account of the site.
type Source string

const (
	SourceOwn     Source = "own"
	SourceAccount Source = "account"
)

// Ref is the ref of the permit number of the work of a Job (AI2.2, AI3.2): the SHA-256 of the salt of the app, the Job and
// the number in hex — the same after a restart, never the Job itself nor any word of the author. A new number is a new
// permit of the same work: one that went by unused while the work waited.
func Ref(salt []byte, jobID string, number int) string {
	return refOf(salt, "job\x00"+jobID+"\x00"+strconv.Itoa(number))
}

// SteerRef is the ref of the unit of a correction (AI2.2, AI2.8): derived from the key of its repeat, so a repeat of the same
// sending spends one unit.
func SteerRef(salt []byte, key string) string {
	return refOf(salt, "steer\x00"+key)
}

func refOf(salt []byte, value string) string {
	hash := sha256.New()
	hash.Write(salt)
	hash.Write([]byte{0})
	hash.Write([]byte(value))
	return hex.EncodeToString(hash.Sum(nil))
}

// State is the state of a permit as the site tells it.
type State string

const (
	StateOpen    State = "open"
	StateClosed  State = "closed"
	StateExpired State = "expired"
)

// Outcome is how a work ended, as the app closes its permit or the site ended it.
type Outcome string

const (
	OutcomeAnswered Outcome = "answered"
	OutcomeDone     Outcome = "done"
	OutcomeCanceled Outcome = "canceled"
	OutcomeFailed   Outcome = "failed"
	// OutcomeExpiredUnused is a permit the site ended with no request under it: its unit came back.
	OutcomeExpiredUnused Outcome = "expired_unused"
)

// Permit is a permit of the site as the app reads it: its id — which lives only in the memory of the process —, its kind,
// state, outcome and whether its unit came back.
type Permit struct {
	ID       string
	Kind     Kind
	State    State
	Outcome  Outcome
	Returned bool
}

// Live says the work goes on under the permit.
func (permit Permit) Live() bool { return permit.State == StateOpen }

// WentUnused says the permit ended before any request of the work — the work waited longer than its lease — and its unit
// already came back: the work takes the next number through the fence (AI2.3).
func (permit Permit) WentUnused() bool {
	return permit.State == StateExpired && permit.Outcome == OutcomeExpiredUnused
}

// Refusal is an answer of the site that refused an operation: the status it answered with, its code and its Retry-After.
type Refusal struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (refusal *Refusal) Error() string { return "appwork: the site refused: " + refusal.Code }

// AsRefusal is the refusal of the site an error carries.
func AsRefusal(err error) (*Refusal, bool) {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal, true
	}
	return nil, false
}

// The codes of the site the app reads (specs/2026-11-13-desk-permits.md, specs/2026-11-13-desk-proxy.md).
const (
	CodePassInvalid       = "app.pass_invalid"
	CodePermitClosed      = "app.permit_closed"
	CodePermitNotFound    = "app.permit_not_found"
	CodeDayFence          = "ai.daily_fence"
	CodeReadingFence      = "ai.reading_fence"
	CodeBusy              = "ai.busy"
	CodeUnavailable       = "ai.unavailable"
	CodeProviderRefused   = "ai.provider_refused"
	CodeProviderFailed    = "ai.provider_failed"
	CodeValidation        = "request.validation"
	CodeUnsupported       = "app.unsupported_capability"
	CodeTooLarge          = "app.request_too_large"
	CodeStepInstruction   = "app.step_instruction"
	CodeStepUnknown       = "app.step_unknown"
	CodeAccountCharacters = "quota.account_characters"
	CodeRateLimit         = "rate_limit.exceeded"
	CodePermitKind        = "app.permit_kind"
)

// Returned is whether the unit of a question came back (AI2.9): yes — the site returned it and nothing can take it back;
// no; pending — the close did not reach the site or a request was dropped mid-stream, so a late answer may still take it
// back; none — the question was not counted by a site (the site's own turn, «Свой ИИ», no helper).
type Returned string

const (
	ReturnedYes     Returned = "yes"
	ReturnedNo      Returned = "no"
	ReturnedPending Returned = "pending"
	ReturnedNone    Returned = "none"
)

// ReturnedOf is what a close of a turn says of its unit (AI2.9): «yes» only when the site answered with a return and no
// request of the turn was dropped mid-stream; a close that did not reach the site, or a dropped request, is pending.
func ReturnedOf(closed *Permit, dropped bool) Returned {
	switch {
	case closed == nil || dropped:
		return ReturnedPending
	case closed.Returned:
		return ReturnedYes
	default:
		return ReturnedNo
	}
}
